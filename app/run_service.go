package main

import (
	"context"
	"encoding/csv"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/8bu/ladilala/core/data"
	"github.com/8bu/ladilala/core/engine"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/player"
	"github.com/8bu/ladilala/core/store"
)

// Events the RunService sends to the UI (docs/api-contract.md, RunService).
const (
	evRunProgress  = "run.progress"  // coalesced counts, at most 5 per second per run
	evRunState     = "run.state"     // the run state changed
	evRunSession   = "run.session"   // a browser started, died or did not start
	evRunLog       = "run.log"       // batched log lines, same pace as run.progress
	evRunUncertain = "run.uncertain" // a row became uncertain
	evRunRecovered = "run.recovered" // Recover settled runs after a crash
)

// progressEvery is the flush period of run.progress and run.log: 5 per second.
const progressEvery = 200 * time.Millisecond

// maxLogBatch caps the lines of one run.log event. Older lines of a burst are
// dropped and the event says how many.
const maxLogBatch = 200

// RunService is the Wails service over the run engine. It resolves saved
// macros and sources, calls engine.Service and forwards engine events to the UI.
type RunService struct {
	b    *Backend
	emit func(name string, data any)
	// savePath asks the user where to save an export. Empty result means cancel.
	savePath func(defaultName string) (string, error)

	mu        sync.Mutex
	watchers  map[string]*watcher
	recovered []RecoveryDTO
	closed    bool
}

// newRunService builds the service and settles runs a dead process left open.
// The result of Recover is kept for Recovered and sent as run.recovered.
func newRunService(b *Backend) *RunService {
	s := &RunService{b: b, emit: b.Emit, watchers: map[string]*watcher{}}
	s.savePath = func(name string) (string, error) {
		if b.App == nil {
			return "", apiErr("internal", "the app window is not ready")
		}
		return b.App.Dialog.SaveFile().SetFilename(name).AddFilter("CSV", "*.csv").PromptForSingleSelection()
	}
	s.recover(context.Background())
	return s
}

func (s *RunService) recover(ctx context.Context) {
	recs, err := s.b.Engine.Recover(ctx)
	if err != nil {
		log.Printf("run recover: %v", err)
		return
	}
	out := make([]RecoveryDTO, 0, len(recs))
	for _, r := range recs {
		out = append(out, RecoveryDTO{RunID: r.RunID, BackToQueue: r.BackToQueue, Uncertain: r.Uncertain})
	}
	s.mu.Lock()
	s.recovered = out
	s.mu.Unlock()
	if len(out) > 0 {
		s.emit(evRunRecovered, out)
	}
}

// ServiceShutdown stops the event forwarders. Wails calls it when the app quits.
func (s *RunService) ServiceShutdown() error {
	s.mu.Lock()
	s.closed = true
	for id, w := range s.watchers {
		w.cancel()
		delete(s.watchers, id)
	}
	s.mu.Unlock()
	return nil
}

var _ application.ServiceShutdown = (*RunService)(nil)

// ---- DTOs ---------------------------------------------------------------------------

// RunOptionsDTO tunes one run.
type RunOptionsDTO struct {
	Concurrency     int  `json:"concurrency"`
	DelayMs         int  `json:"delayMs"`
	StopAfterErrors int  `json:"stopAfterErrors"`
	MaxRestarts     int  `json:"maxRestarts"`
	DryRun          bool `json:"dryRun"`
	TestOne         bool `json:"testOne"`
}

// CreateRunRequest is what the setup dialog sends. To < 0 means to the end of the source.
type CreateRunRequest struct {
	MacroID    string        `json:"macroId"`
	SourceID   string        `json:"sourceId"`
	From       int           `json:"from"`
	To         int           `json:"to"`
	ProfileIDs []string      `json:"profileIds"`
	Options    RunOptionsDTO `json:"options"`
}

// RunDTO is one run with its row counts.
type RunDTO struct {
	ID           string         `json:"id"`
	MacroID      string         `json:"macroId"`
	MacroName    string         `json:"macroName"`
	MacroVersion int            `json:"macroVersion"`
	MacroHash    string         `json:"macroHash"`
	SourceID     string         `json:"sourceId"`
	State        string         `json:"state"`
	Reason       string         `json:"reason"`
	Options      RunOptionsDTO  `json:"options"`
	ProfileIDs   []string       `json:"profileIds"`
	RowFrom      int            `json:"rowFrom"`
	RowTo        int            `json:"rowTo"`
	Total        int            `json:"total"`
	Counts       map[string]int `json:"counts"`
	CreatedAt    time.Time      `json:"createdAt"`
	StartedAt    *time.Time     `json:"startedAt,omitempty"`
	FinishedAt   *time.Time     `json:"finishedAt,omitempty"`
}

// RowDTO is one ledger row. Data can hold passwords: the UI masks it.
type RowDTO struct {
	RunID          string            `json:"runId"`
	Index          int               `json:"index"`
	IdempotencyKey string            `json:"idempotencyKey"`
	State          string            `json:"state"`
	CommitState    string            `json:"commitState"`
	ProfileID      string            `json:"profileId"`
	Attempts       int               `json:"attempts"`
	Data           map[string]string `json:"data"`
	ErrorKind      string            `json:"errorKind"`
	ErrorMsg       string            `json:"errorMsg"`
	StepID         string            `json:"stepId"`
	StartedAt      *time.Time        `json:"startedAt,omitempty"`
	FinishedAt     *time.Time        `json:"finishedAt,omitempty"`
}

// RowFilter selects ledger rows. Limit 0 means 100.
type RowFilter struct {
	States  []string `json:"states"`
	Indexes []int    `json:"indexes"`
	Offset  int      `json:"offset"`
	Limit   int      `json:"limit"`
}

// RowPage is one page of the ledger. Total counts every row that matches.
type RowPage struct {
	Rows  []RowDTO `json:"rows"`
	Total int      `json:"total"`
}

// RunFilter selects runs, newest first. Limit 0 means 100.
type RunFilter struct {
	States []string `json:"states"`
	Offset int      `json:"offset"`
	Limit  int      `json:"limit"`
}

// RecoveryDTO says what Recover did to one run.
type RecoveryDTO struct {
	RunID       string `json:"runId"`
	BackToQueue int    `json:"backToQueue"`
	Uncertain   int    `json:"uncertain"`
}

// ProgressEvent is the payload of run.progress.
type ProgressEvent struct {
	RunID  string         `json:"runId"`
	State  string         `json:"state"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
	// Resync is true when the forwarder lost events: reload the visible rows.
	Resync bool `json:"resync,omitempty"`
}

// StateEvent is the payload of run.state.
type StateEvent struct {
	RunID  string `json:"runId"`
	State  string `json:"state"`
	Reason string `json:"reason"`
	At     int64  `json:"at"` // Unix milliseconds
}

// SessionEvent is the payload of run.session: one browser of one profile.
type SessionEvent struct {
	RunID     string `json:"runId"`
	ProfileID string `json:"profileId"`
	Message   string `json:"message"`
	At        int64  `json:"at"`
}

// UncertainEvent is the payload of run.uncertain.
type UncertainEvent struct {
	RunID     string `json:"runId"`
	RowIndex  int    `json:"rowIndex"`
	ProfileID string `json:"profileId"`
	Message   string `json:"message"`
	At        int64  `json:"at"`
}

// LogLine is one line of the run log. Kind is an engine event kind.
type LogLine struct {
	At        int64  `json:"at"`
	Kind      string `json:"kind"`
	RowIndex  int    `json:"rowIndex"` // -1 for run events
	ProfileID string `json:"profileId"`
	Message   string `json:"message"`
}

// LogEvent is the payload of run.log.
type LogEvent struct {
	RunID   string    `json:"runId"`
	Lines   []LogLine `json:"lines"`
	Skipped int       `json:"skipped,omitempty"`
}

func toRunDTO(r engine.Run) RunDTO {
	counts := make(map[string]int, len(r.Counts))
	for k, v := range r.Counts {
		counts[string(k)] = v
	}
	ids := r.ProfileIDs
	if ids == nil {
		ids = []string{}
	}
	return RunDTO{
		ID: r.ID, MacroID: r.MacroID, MacroName: r.MacroName, MacroVersion: r.MacroVersion, MacroHash: r.MacroHash,
		SourceID: r.SourceID, State: string(r.State), Reason: r.Reason,
		Options: RunOptionsDTO{Concurrency: r.Options.Concurrency, DelayMs: r.Options.DelayMs, StopAfterErrors: r.Options.StopAfterErrors,
			MaxRestarts: r.Options.MaxRestarts, DryRun: r.Options.DryRun, TestOne: r.Options.TestOne},
		ProfileIDs: ids, RowFrom: r.RowFrom, RowTo: r.RowTo, Total: r.Total, Counts: counts,
		CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
	}
}

func toRowDTO(r engine.Row) RowDTO {
	d := r.Data
	if d == nil {
		d = map[string]string{}
	}
	return RowDTO{
		RunID: r.RunID, Index: r.Index, IdempotencyKey: r.IdempotencyKey, State: string(r.State), CommitState: string(r.CommitState),
		ProfileID: r.ProfileID, Attempts: r.Attempts, Data: d, ErrorKind: r.ErrorKind, ErrorMsg: r.ErrorMsg, StepID: r.StepID,
		StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
	}
}

func (f RowFilter) engine() engine.RowFilter {
	out := engine.RowFilter{Indexes: f.Indexes, Offset: f.Offset, Limit: f.Limit}
	for _, st := range f.States {
		out.States = append(out.States, engine.RowState(st))
	}
	return out
}

// ---- errors -------------------------------------------------------------------------

// toAPIError maps core errors to the contract codes. It is the only place that does.
func toAPIError(err error) error {
	if err == nil {
		return nil
	}
	var api *APIError
	if errors.As(err, &api) {
		return err
	}
	var gf *engine.GuardFiredError
	if errors.As(err, &gf) {
		return apiErr("run.guard", err.Error())
	}
	var se *engine.StateError
	if errors.As(err, &se) {
		return apiErr("run.state", err.Error(), "state", string(se.State), "op", se.Op)
	}
	var mv *macro.ValidationError
	var mc *player.MissingColumnError
	var rowErr data.RowError
	switch {
	case errors.Is(err, engine.ErrRunNotFound):
		return apiErr("run.notFound", err.Error())
	case errors.Is(err, engine.ErrBadState):
		return apiErr("run.state", err.Error())
	case errors.Is(err, engine.ErrNoRows):
		return apiErr("run.noRows", err.Error())
	case errors.Is(err, engine.ErrNoProfiles):
		return apiErr("run.noProfiles", err.Error())
	case errors.Is(err, engine.ErrNotEnoughProfiles):
		return apiErr("run.tooFewProfiles", err.Error())
	case errors.Is(err, engine.ErrNotUncertain):
		return apiErr("run.notUncertain", err.Error())
	case errors.Is(err, engine.ErrBrowserDied):
		return &APIError{Code: "run.browserDied", Message: err.Error(), Retryable: true}
	case errors.Is(err, store.ErrNotFound):
		return apiErr("notFound", err.Error())
	case errors.As(err, &mv):
		return apiErr("invalid", err.Error())
	case errors.As(err, &mc):
		return apiErr("macro.missingColumn", err.Error())
	case errors.As(err, &rowErr), errors.Is(err, data.ErrEmpty), errors.Is(err, data.ErrClosed):
		return apiErr("data.format", err.Error())
	}
	return &APIError{Code: "internal", Message: err.Error()}
}

// ---- methods ------------------------------------------------------------------------

// CreateRun opens the saved source with the macro's saved column mapping,
// copies the rows into the ledger and returns the run in state created.
func (s *RunService) CreateRun(ctx context.Context, req CreateRunRequest) (RunDTO, error) {
	if req.MacroID == "" || req.SourceID == "" {
		return RunDTO{}, apiErr("invalid", "macroId and sourceId are required")
	}
	if req.From < 0 {
		return RunDTO{}, apiErr("invalid", "from must not be negative", "field", "from")
	}
	if req.To >= 0 && req.To <= req.From {
		return RunDTO{}, apiErr("invalid", "to must be greater than from", "field", "to")
	}
	src, err := openMappedSource(ctx, s.b, req.MacroID, req.SourceID)
	if err != nil {
		return RunDTO{}, toAPIError(err)
	}
	defer src.Close()
	run, err := s.b.Engine.CreateRun(ctx, engine.CreateRunRequest{
		MacroID: req.MacroID, Source: src, SourceID: req.SourceID, From: req.From, To: req.To, ProfileIDs: req.ProfileIDs,
		Options: engine.RunOptions{Concurrency: req.Options.Concurrency, DelayMs: req.Options.DelayMs, StopAfterErrors: req.Options.StopAfterErrors,
			MaxRestarts: req.Options.MaxRestarts, DryRun: req.Options.DryRun, TestOne: req.Options.TestOne},
	})
	if err != nil {
		return RunDTO{}, toAPIError(err)
	}
	return toRunDTO(run), nil
}

// StartRun starts a run in state created.
func (s *RunService) StartRun(ctx context.Context, id string) error {
	return s.withWatch(id, func() error { return s.b.Engine.StartRun(ctx, id) })
}

// PauseRun stops the run at the next step of every browser.
func (s *RunService) PauseRun(ctx context.Context, id string) error {
	return toAPIError(s.b.Engine.PauseRun(ctx, id))
}

// ResumeRun continues a paused run, or restarts a failed or recovered one.
func (s *RunService) ResumeRun(ctx context.Context, id string) error {
	return s.withWatch(id, func() error { return s.b.Engine.ResumeRun(ctx, id) })
}

// StopRun ends the run. Rows in flight go back to pending, or become uncertain.
func (s *RunService) StopRun(ctx context.Context, id string) error {
	return toAPIError(s.b.Engine.StopRun(ctx, id))
}

// Takeover pauses the run and keeps the browsers open for the user.
func (s *RunService) Takeover(ctx context.Context, id string) error {
	return toAPIError(s.b.Engine.Takeover(ctx, id))
}

// ReleaseTakeover gives the browsers back. Error run.guard keeps the takeover.
func (s *RunService) ReleaseTakeover(ctx context.Context, id string) error {
	return toAPIError(s.b.Engine.ReleaseTakeover(ctx, id))
}

// RetryRows resets failed and skipped rows that match f. It returns their count.
func (s *RunService) RetryRows(ctx context.Context, id string, f RowFilter) (int, error) {
	n, err := s.b.Engine.RetryRows(ctx, id, f.engine())
	return n, toAPIError(err)
}

// MarkUncertainRow records what the user found out. res is "done" or "failed".
func (s *RunService) MarkUncertainRow(ctx context.Context, id string, index int, res string) error {
	if res != string(engine.ResolveDone) && res != string(engine.ResolveFailed) {
		return apiErr("invalid", "resolution must be done or failed", "field", "res")
	}
	return toAPIError(s.b.Engine.MarkUncertainRow(ctx, id, index, engine.Resolution(res)))
}

// ListRuns returns runs, newest first. Runs that are active get a forwarder,
// so the UI sees live events for a run it did not start.
func (s *RunService) ListRuns(ctx context.Context, f RunFilter) ([]RunDTO, error) {
	ef := engine.RunFilter{Offset: f.Offset, Limit: f.Limit}
	for _, st := range f.States {
		ef.States = append(ef.States, engine.RunState(st))
	}
	runs, err := s.b.Engine.ListRuns(ctx, ef)
	if err != nil {
		return nil, toAPIError(err)
	}
	out := make([]RunDTO, 0, len(runs))
	for _, r := range runs {
		out = append(out, toRunDTO(r))
	}
	return out, nil
}

// GetRun returns one run with its row counts.
func (s *RunService) GetRun(ctx context.Context, id string) (RunDTO, error) {
	r, err := s.b.Engine.GetRun(ctx, id)
	if err != nil {
		return RunDTO{}, toAPIError(err)
	}
	return toRunDTO(r), nil
}

// ListRows returns one page of the ledger of a run.
func (s *RunService) ListRows(ctx context.Context, id string, f RowFilter) (RowPage, error) {
	p, err := s.b.Engine.ListRows(ctx, id, f.engine())
	if err != nil {
		return RowPage{}, toAPIError(err)
	}
	out := RowPage{Rows: make([]RowDTO, 0, len(p.Rows)), Total: p.Total}
	for _, r := range p.Rows {
		out.Rows = append(out.Rows, toRowDTO(r))
	}
	return out, nil
}

// Recovered returns what Recover settled at app start. The UI asks once on
// load, because the run.recovered event can come before the window listens.
func (s *RunService) Recovered(ctx context.Context) ([]RecoveryDTO, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RecoveryDTO{}, s.recovered...), nil
}

// DismissRecovered clears the list that Recovered returns (the user chose "leave").
func (s *RunService) DismissRecovered(ctx context.Context) error {
	s.mu.Lock()
	s.recovered = nil
	s.mu.Unlock()
	return nil
}

// Watch starts the event forwarder of a run that is already active. The UI
// calls it for a run it opens after a reload. It does nothing for a finished run.
func (s *RunService) Watch(ctx context.Context, id string) error {
	r, err := s.b.Engine.GetRun(ctx, id)
	if err != nil {
		return toAPIError(err)
	}
	if r.State != engine.StateFinished && r.State != engine.StateFailed && r.State != engine.StateCrashedRecovered {
		return s.startWatch(id)
	}
	return nil
}

// ExportLedger writes the ledger of a run as CSV and returns the path. An
// empty path opens a save dialog; the result is "" when the user cancels.
// Source data is not exported: it can hold passwords.
func (s *RunService) ExportLedger(ctx context.Context, id, path string) (string, error) {
	if _, err := s.b.Engine.GetRun(ctx, id); err != nil {
		return "", toAPIError(err)
	}
	var err error
	if path == "" {
		path, err = s.savePath("run-" + id + ".csv")
		if err != nil {
			return "", toAPIError(err)
		}
		if path == "" {
			return "", nil
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", apiErr("disk.full", err.Error())
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"row", "state", "commit", "profile", "attempts", "step", "errorKind", "errorMessage", "startedAt", "finishedAt"})
	const page = 1000
	for off := 0; ; off += page {
		p, err := s.b.Engine.ListRows(ctx, id, engine.RowFilter{Offset: off, Limit: page})
		if err != nil {
			f.Close()
			return "", toAPIError(err)
		}
		for _, row := range p.Rows {
			_ = w.Write([]string{strconv.Itoa(row.Index + 1), string(row.State), string(row.CommitState), csvSafe(row.ProfileID), strconv.Itoa(row.Attempts),
				csvSafe(row.StepID), row.ErrorKind, csvSafe(row.ErrorMsg), fmtTime(row.StartedAt), fmtTime(row.FinishedAt)})
		}
		if len(p.Rows) < page {
			break
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close()
		return "", apiErr("disk.full", err.Error())
	}
	if err := f.Close(); err != nil {
		return "", apiErr("disk.full", err.Error())
	}
	return path, nil
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// csvSafe stops a spreadsheet from running a cell as a formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// ---- event forwarding ---------------------------------------------------------------

// withWatch starts the forwarder before op so no early event is lost. If op
// fails and the run is not active, the forwarder goes away again.
func (s *RunService) withWatch(id string, op func() error) error {
	created := s.startWatchNew(id)
	if err := op(); err != nil {
		if created {
			s.stopWatch(id)
		}
		return toAPIError(err)
	}
	return nil
}

func (s *RunService) startWatch(id string) error {
	s.startWatchNew(id)
	return nil
}

// startWatchNew subscribes to the run unless a forwarder exists. It reports
// whether it made one.
func (s *RunService) startWatchNew(id string) bool {
	s.mu.Lock()
	if s.closed || s.watchers[id] != nil {
		s.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.b.Engine.Subscribe(ctx, id)
	if err != nil {
		s.mu.Unlock()
		cancel()
		return false
	}
	w := &watcher{cancel: cancel}
	s.watchers[id] = w
	s.mu.Unlock()
	go s.forward(ctx, w, id, ch)
	return true
}

func (s *RunService) stopWatch(id string) {
	s.mu.Lock()
	if w := s.watchers[id]; w != nil {
		w.cancel()
		delete(s.watchers, id)
	}
	s.mu.Unlock()
}

// dropWatch removes w if it is still the forwarder of the run.
func (s *RunService) dropWatch(id string, w *watcher) {
	s.mu.Lock()
	if s.watchers[id] == w {
		w.cancel()
		delete(s.watchers, id)
	}
	s.mu.Unlock()
}

// watcher is one event forwarder. The pointer tells a new forwarder from an old one.
type watcher struct{ cancel context.CancelFunc }

// forward reads engine events and sends them to the UI. It never blocks the
// engine: the engine hub drops the oldest event of a slow reader. State,
// session and uncertain events go out at once; row events only mark the run
// dirty, and the 200 ms tick sends one run.progress and one run.log.
func (s *RunService) forward(ctx context.Context, w *watcher, id string, ch <-chan engine.Event) {
	defer s.dropWatch(id, w)
	tick := time.NewTicker(progressEvery)
	defer tick.Stop()
	var (
		lines   []LogLine
		skipped int
		dirty   bool
		resync  bool
	)
	flush := func() {
		if len(lines) > 0 {
			s.emit(evRunLog, LogEvent{RunID: id, Lines: lines, Skipped: skipped})
			lines, skipped = nil, 0
		}
		if !dirty {
			return
		}
		dirty = false
		r, err := s.b.Engine.GetRun(context.Background(), id)
		if err != nil {
			return
		}
		counts := make(map[string]int, len(r.Counts))
		for k, v := range r.Counts {
			counts[string(k)] = v
		}
		s.emit(evRunProgress, ProgressEvent{RunID: id, State: string(r.State), Total: r.Total, Counts: counts, Resync: resync})
		resync = false
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			flush()
		case ev, ok := <-ch:
			if !ok {
				flush()
				return
			}
			at := ev.Time.UnixMilli()
			if ev.Dropped > 0 {
				dirty, resync = true, true
			}
			if len(lines) >= maxLogBatch {
				lines = lines[1:]
				skipped++
			}
			lines = append(lines, LogLine{At: at, Kind: string(ev.Kind), RowIndex: ev.RowIndex, ProfileID: ev.ProfileID, Message: ev.Message})
			dirty = true
			switch ev.Kind {
			case engine.EventRunState:
				s.emit(evRunState, StateEvent{RunID: id, State: string(ev.RunState), Reason: ev.Message, At: at})
				if ev.RunState == engine.StateFinished || ev.RunState == engine.StateFailed {
					flush()
					return
				}
			case engine.EventBrowser:
				s.emit(evRunSession, SessionEvent{RunID: id, ProfileID: ev.ProfileID, Message: ev.Message, At: at})
			case engine.EventRowUncertain:
				s.emit(evRunUncertain, UncertainEvent{RunID: id, RowIndex: ev.RowIndex, ProfileID: ev.ProfileID, Message: ev.Message, At: at})
			}
		}
	}
}
