package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/player"
	"github.com/8bu/ladilala/core/proxy"
	"github.com/8bu/ladilala/core/recorder"
	"github.com/8bu/ladilala/core/store"
)

// MacroService is the UI service for macros: the library, the editor
// checks, import and export, and the recording flow (recording_service.go).
type MacroService struct {
	b    *Backend
	meta *metaStore
	// emit sends a UI event. It is b.Emit; a test replaces it.
	emit func(name string, data any)

	mu      sync.Mutex
	reviews map[string]*importReview
	recs    map[string]*recording
}

func newMacroService(b *Backend) *MacroService {
	return &MacroService{b: b, meta: metaOf(b), emit: b.Emit, reviews: map[string]*importReview{}, recs: map[string]*recording{}}
}

// ProblemDTO is one validation problem at a document path.
type ProblemDTO struct {
	Path string `json:"path"`
	Msg  string `json:"msg"`
}

// MacroSummary is one row of the Macros list.
type MacroSummary struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	StepCount    int      `json:"stepCount"`
	Version      int      `json:"version"`
	Hash         string   `json:"hash"`
	Variables    []string `json:"variables"`
	SourceID     string   `json:"sourceId"`
	WarningCount int      `json:"warningCount"`
	UpdatedAt    string   `json:"updatedAt"`
}

// MacroDTO is a macro with what the editor keeps beside it.
type MacroDTO struct {
	Macro       macro.Macro     `json:"macro"`
	Version     int             `json:"version"`
	Hash        string          `json:"hash"`
	Description string          `json:"description"`
	SourceID    string          `json:"sourceId"`
	Warnings    []RecordWarning `json:"warnings"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
}

// SaveMacroInput is the editor's Save. An empty Macro.ID creates a macro.
// Warnings nil keeps the stored ones; an empty list clears them.
type SaveMacroInput struct {
	Macro       macro.Macro      `json:"macro"`
	Description string           `json:"description"`
	Warnings    *[]RecordWarning `json:"warnings"`
}

// ImportReview is what the user checks before an imported macro is saved
// (threat model T-1, T-2: show the origins and the commit points).
type ImportReview struct {
	// ReviewID is empty when the document has problems: it cannot be confirmed.
	ReviewID         string       `json:"reviewId"`
	Macro            *macro.Macro `json:"macro"`
	AllowedOrigins   []string     `json:"allowedOrigins"`
	CommitPointSteps []string     `json:"commitPointSteps"`
	Variables        []string     `json:"variables"`
	Problems         []ProblemDTO `json:"problems"`
	Hash             string       `json:"hash"`
	StepCount        int          `json:"stepCount"`
}

type importReview struct {
	m       *macro.Macro
	created time.Time
}

const reviewTTL = 30 * time.Minute

// StepPreview is one step value rendered for one data row.
type StepPreview struct {
	StepID string `json:"stepId"`
	Action string `json:"action"`
	// Value is the rendered text; empty when the step has no value or Error is set.
	Value string `json:"value"`
	// Error is "missingColumn" or "invalid" when the value cannot be built.
	Error  string `json:"error,omitempty"`
	Column string `json:"column,omitempty"`
}

func problemsOf(err error) []ProblemDTO {
	var ve *macro.ValidationError
	if !errors.As(err, &ve) {
		return nil
	}
	out := make([]ProblemDTO, len(ve.Problems))
	for i, p := range ve.Problems {
		out[i] = ProblemDTO{Path: p.Path, Msg: p.Msg}
	}
	return out
}

// toMacroAPIError maps core errors to the contract's codes.
func toMacroAPIError(err error) error {
	if err == nil {
		return nil
	}
	var api *APIError
	if errors.As(err, &api) {
		return err
	}
	var ve *macro.ValidationError
	switch {
	case errors.As(err, &ve):
		return apiErr("invalid", "macro is invalid", "problems", problemsOf(err))
	case errors.Is(err, store.ErrNotFound):
		return apiErr("notFound", err.Error())
	case errors.Is(err, driver.ErrProfileLocked):
		return apiErr("profile.locked", err.Error())
	case errors.Is(err, driver.ErrInvalidDataDir):
		return apiErr("profile.dataDir", err.Error())
	case errors.Is(err, driver.ErrBinaryNotFound):
		return apiErr("engine.notFound", err.Error())
	case errors.Is(err, driver.ErrDebuggingBlocked):
		return apiErr("engine.policyBlocked", err.Error())
	case errors.Is(err, driver.ErrCanvasHang), errors.Is(err, driver.ErrCanvasLeak):
		return apiErr("engine.hang", err.Error())
	case errors.Is(err, proxy.ErrUpstreamAuth):
		return apiErr("proxy.auth", err.Error())
	}
	return apiErr("internal", err.Error())
}

func newMacroID() string { return "m" + store.NewID()[:12] }

func loadMacro(rec store.MacroRecord) (*macro.Macro, error) {
	return macro.Load(bytes.NewReader(rec.Body))
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func (s *MacroService) dto(rec store.MacroRecord) (MacroDTO, error) {
	m, err := loadMacro(rec)
	if err != nil {
		return MacroDTO{}, err
	}
	meta, err := s.meta.get(rec.ID)
	if err != nil {
		return MacroDTO{}, err
	}
	if meta.Warnings == nil {
		meta.Warnings = []RecordWarning{}
	}
	return MacroDTO{Macro: *m, Version: rec.Version, Hash: rec.Hash, Description: meta.Description, SourceID: meta.SourceID,
		Warnings: meta.Warnings, CreatedAt: stamp(rec.CreatedAt), UpdatedAt: stamp(rec.UpdatedAt)}, nil
}

// ListMacros returns every macro, newest edit first.
func (s *MacroService) ListMacros(ctx context.Context) ([]MacroSummary, error) {
	recs, err := s.b.Store.Macros.List(ctx)
	if err != nil {
		return nil, toMacroAPIError(err)
	}
	all, err := s.meta.all()
	if err != nil {
		return nil, toMacroAPIError(err)
	}
	out := make([]MacroSummary, 0, len(recs))
	for _, r := range recs {
		full, err := s.b.Store.Macros.Get(ctx, r.ID) // List returns no bodies
		if err != nil {
			return nil, toMacroAPIError(err)
		}
		m, err := loadMacro(full)
		if err != nil {
			// A stored macro that no longer loads still appears so the user can delete it.
			out = append(out, MacroSummary{ID: r.ID, Name: r.Name, Version: r.Version, Hash: r.Hash, Variables: []string{}, UpdatedAt: stamp(r.UpdatedAt)})
			continue
		}
		vars := make([]string, len(m.Variables))
		for i, v := range m.Variables {
			vars[i] = v.Name
		}
		meta := all[r.ID]
		out = append(out, MacroSummary{ID: r.ID, Name: r.Name, Description: meta.Description, StepCount: len(m.Steps), Version: r.Version, Hash: r.Hash,
			Variables: vars, SourceID: meta.SourceID, WarningCount: len(meta.Warnings), UpdatedAt: stamp(r.UpdatedAt)})
	}
	return out, nil
}

// GetMacro returns one macro for the editor.
func (s *MacroService) GetMacro(ctx context.Context, id string) (MacroDTO, error) {
	rec, err := s.b.Store.Macros.Get(ctx, id)
	if err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	d, err := s.dto(rec)
	return d, toMacroAPIError(err)
}

// nameTaken reports whether another macro already uses name.
func (s *MacroService) nameTaken(ctx context.Context, id, name string) (bool, error) {
	recs, err := s.b.Store.Macros.List(ctx)
	if err != nil {
		return false, err
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for _, r := range recs {
		if r.ID != id && strings.ToLower(strings.TrimSpace(r.Name)) == want {
			return true, nil
		}
	}
	return false, nil
}

// SaveMacro validates and stores a macro. It returns "invalid" with every
// problem, or "conflict" when another macro has the name.
func (s *MacroService) SaveMacro(ctx context.Context, in SaveMacroInput) (MacroDTO, error) {
	m := in.Macro
	if m.ID == "" {
		m.ID = newMacroID()
	}
	m.Name = strings.TrimSpace(m.Name)
	macro.DeclareUsed(&m)
	if err := macro.Validate(&m); err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	taken, err := s.nameTaken(ctx, m.ID, m.Name)
	if err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	if taken {
		return MacroDTO{}, apiErr("conflict", "a macro has this name", "field", "name")
	}
	rec, err := s.b.Store.Macros.Put(ctx, &m)
	if err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	err = s.meta.update(m.ID, func(mm *macroMeta) {
		mm.Description = strings.TrimSpace(in.Description)
		if in.Warnings != nil {
			mm.Warnings = *in.Warnings
		}
	})
	if err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	d, err := s.dto(rec)
	return d, toMacroAPIError(err)
}

// ValidateMacro is the live check while editing. A macro without an ID is
// checked as if it had one.
func (s *MacroService) ValidateMacro(_ context.Context, m macro.Macro) ([]ProblemDTO, error) {
	if m.ID == "" {
		m.ID = "draft"
	}
	m.Variables = append([]macro.VariableDecl(nil), m.Variables...)
	macro.DeclareUsed(&m)
	if err := macro.Validate(&m); err != nil {
		return problemsOf(err), nil
	}
	return []ProblemDTO{}, nil
}

// DuplicateMacro copies a macro under a new ID and name. Steps, variables
// and the source link are copied. Recording warnings are copied too.
func (s *MacroService) DuplicateMacro(ctx context.Context, id, name string) (MacroDTO, error) {
	rec, err := s.b.Store.Macros.Get(ctx, id)
	if err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	m, err := loadMacro(rec)
	if err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	src, err := s.meta.get(id)
	if err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	m.ID = newMacroID()
	base := strings.TrimSpace(name)
	if base == "" {
		base = rec.Name
	}
	m.Name = base
	for i := 2; ; i++ {
		taken, err := s.nameTaken(ctx, m.ID, m.Name)
		if err != nil {
			return MacroDTO{}, toMacroAPIError(err)
		}
		if !taken {
			break
		}
		m.Name = fmt.Sprintf("%s (%d)", base, i)
	}
	nrec, err := s.b.Store.Macros.Put(ctx, m)
	if err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	err = s.meta.update(m.ID, func(mm *macroMeta) {
		mm.Description, mm.SourceID, mm.Warnings = src.Description, src.SourceID, src.Warnings
		if len(src.Mappings) > 0 {
			mm.Mappings = map[string]map[string]string{}
			for k, v := range src.Mappings {
				c := map[string]string{}
				for a, b := range v {
					c[a] = b
				}
				mm.Mappings[k] = c
			}
		}
	})
	if err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	d, err := s.dto(nrec)
	return d, toMacroAPIError(err)
}

// activeRunCount counts the runs that can still start or resume and use the
// value in column (macro_id or source_id). Finished and failed runs keep a
// copy of the macro, so they do not block a delete.
func activeRunCount(ctx context.Context, st *store.Store, column, id string) (int, error) {
	var n int
	err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE `+column+`=? AND state IN ('created','running','paused','takeover','stopping','crashed_recovered')`, id).Scan(&n)
	return n, err
}

// DeleteMacro removes a macro. It is refused with "conflict" while a run that
// is not finished uses it.
func (s *MacroService) DeleteMacro(ctx context.Context, id string) error {
	if _, err := s.b.Store.Macros.Get(ctx, id); err != nil {
		return toMacroAPIError(err)
	}
	n, err := activeRunCount(ctx, s.b.Store, "macro_id", id)
	if err != nil {
		return toMacroAPIError(err)
	}
	if n > 0 {
		return apiErr("conflict", "a run uses this macro", "reason", "run", "runs", n)
	}
	if err := s.b.Store.Macros.Delete(ctx, id); err != nil {
		return toMacroAPIError(err)
	}
	return toMacroAPIError(s.meta.update(id, func(mm *macroMeta) { *mm = macroMeta{} }))
}

func reviewOf(m *macro.Macro, id string) ImportReview {
	r := ImportReview{ReviewID: id, Macro: m, AllowedOrigins: append([]string{}, m.AllowedOrigins...), CommitPointSteps: []string{}, Variables: []string{}, Problems: []ProblemDTO{}, StepCount: len(m.Steps)}
	for _, st := range m.Steps {
		if st.CommitPoint {
			r.CommitPointSteps = append(r.CommitPointSteps, st.ID)
		}
	}
	for _, v := range m.Variables {
		r.Variables = append(r.Variables, v.Name)
	}
	if body, err := macro.Marshal(m); err == nil {
		r.Hash = store.HashBody(body)
	}
	return r
}

// ImportMacro parses a macro document with the strict loader and returns a
// review. It saves nothing. A document with problems comes back with
// Problems set (paths included) and no ReviewID.
func (s *MacroService) ImportMacro(_ context.Context, content string) (ImportReview, error) {
	m, err := macro.Load(strings.NewReader(content))
	if err != nil {
		ps := problemsOf(err)
		if ps == nil {
			ps = []ProblemDTO{{Path: "$", Msg: err.Error()}}
		}
		return ImportReview{AllowedOrigins: []string{}, CommitPointSteps: []string{}, Variables: []string{}, Problems: ps}, nil
	}
	id := store.NewID()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, r := range s.reviews {
		if time.Since(r.created) > reviewTTL {
			delete(s.reviews, k)
		}
	}
	s.reviews[id] = &importReview{m: m, created: time.Now()}
	return reviewOf(m, id), nil
}

// ConfirmImport saves a reviewed macro. It gets a new ID and, when the name is
// taken, a numbered name, so an import never overwrites a macro.
func (s *MacroService) ConfirmImport(ctx context.Context, reviewID string) (MacroDTO, error) {
	s.mu.Lock()
	r, ok := s.reviews[reviewID]
	if ok {
		delete(s.reviews, reviewID)
	}
	s.mu.Unlock()
	if !ok {
		return MacroDTO{}, apiErr("notFound", "the import review expired", "what", "review")
	}
	m := *r.m
	m.ID = newMacroID()
	base := strings.TrimSpace(m.Name)
	m.Name = base
	for i := 2; ; i++ {
		taken, err := s.nameTaken(ctx, m.ID, m.Name)
		if err != nil {
			return MacroDTO{}, toMacroAPIError(err)
		}
		if !taken {
			break
		}
		m.Name = fmt.Sprintf("%s (%d)", base, i)
	}
	rec, err := s.b.Store.Macros.Put(ctx, &m)
	if err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	d, err := s.dto(rec)
	return d, toMacroAPIError(err)
}

// ExportMacro writes the macro as indented JSON to path.
func (s *MacroService) ExportMacro(ctx context.Context, id, path string) error {
	rec, err := s.b.Store.Macros.Get(ctx, id)
	if err != nil {
		return toMacroAPIError(err)
	}
	m, err := loadMacro(rec)
	if err != nil {
		return toMacroAPIError(err)
	}
	body, err := macro.Marshal(m)
	if err != nil {
		return toMacroAPIError(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return apiErr("internal", err.Error())
	}
	return nil
}

// MapVariable binds one fill or select step to a data column: the step value
// becomes {{column}} and the column is declared as a required string variable.
// Only the chosen step changes, even when other steps hold the same text. An
// empty column is refused. The macro is saved and returned.
func (s *MacroService) MapVariable(ctx context.Context, macroID, stepID, column string) (MacroDTO, error) {
	rec, err := s.b.Store.Macros.Get(ctx, macroID)
	if err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	m, err := loadMacro(rec)
	if err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	column = strings.TrimSpace(column)
	idx := -1
	for i := range m.Steps {
		if m.Steps[i].ID == stepID {
			idx = i
		}
	}
	switch {
	case idx < 0:
		return MacroDTO{}, apiErr("notFound", "no such step", "what", "step")
	case column == "":
		return MacroDTO{}, apiErr("invalid", "the column name is empty", "problems", []ProblemDTO{{Path: "column", Msg: "required"}})
	case (m.Steps[idx].Action != macro.ActionFill && m.Steps[idx].Action != macro.ActionSelect) || m.Steps[idx].Value == nil:
		return MacroDTO{}, apiErr("invalid", "only fill and select steps take a column", "problems", []ProblemDTO{{Path: "steps[" + stepID + "].action", Msg: "only fill and select steps take a column"}})
	}
	// Mark the one step with a literal no other step has, so Parameterize
	// rewrites only that step.
	marker := "\x00map:" + stepID
	m.Steps[idx].Value.Expr = marker
	if _, err := recorder.Parameterize(m, map[string]string{column: marker}); err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	if err := macro.Validate(m); err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	nrec, err := s.b.Store.Macros.Put(ctx, m)
	if err != nil {
		return MacroDTO{}, toMacroAPIError(err)
	}
	d, err := s.dto(nrec)
	return d, toMacroAPIError(err)
}

// SetMacroSource links a data source to a macro. An empty sourceID clears the link.
func (s *MacroService) SetMacroSource(ctx context.Context, macroID, sourceID string) error {
	if _, err := s.b.Store.Macros.Get(ctx, macroID); err != nil {
		return toMacroAPIError(err)
	}
	if sourceID != "" {
		if _, err := s.b.Store.Sources.Get(ctx, sourceID); err != nil {
			return toMacroAPIError(err)
		}
	}
	return toMacroAPIError(s.meta.update(macroID, func(mm *macroMeta) { mm.SourceID = sourceID }))
}

// PreviewRow renders the value of every step for one data row, with the
// saved mapping applied. A value that cannot be built has Error set.
func (s *MacroService) PreviewRow(ctx context.Context, macroID, sourceID string, rowIndex int) ([]StepPreview, error) {
	rec, err := s.b.Store.Macros.Get(ctx, macroID)
	if err != nil {
		return nil, toMacroAPIError(err)
	}
	m, err := loadMacro(rec)
	if err != nil {
		return nil, toMacroAPIError(err)
	}
	row, err := mappedRow(ctx, s.b, macroID, m, sourceID, rowIndex)
	if err != nil {
		return nil, toDataAPIError(err)
	}
	vars, verr := player.BuildVars(m, row)
	out := make([]StepPreview, 0, len(m.Steps))
	for _, st := range m.Steps {
		p := StepPreview{StepID: st.ID, Action: string(st.Action)}
		if st.Value != nil && !st.Disabled {
			parts, perr := macro.ParseExpr(st.Value.Expr)
			switch {
			case perr != nil:
				p.Error = "invalid"
			default:
				for _, name := range macro.Refs(parts) {
					if _, ok := vars[name]; !ok && p.Error == "" {
						p.Error, p.Column = "missingColumn", name
					}
				}
				if p.Error == "" {
					if v, err := st.Value.Eval(vars); err != nil {
						p.Error = "invalid"
					} else {
						p.Value = v
					}
				}
			}
		}
		out = append(out, p)
	}
	_ = verr // a missing required variable shows on the step that uses it
	return out, nil
}
