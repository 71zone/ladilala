package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/8bu/ladilala/core/data"
	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/proxy"
	"github.com/8bu/ladilala/core/recorder"
	"github.com/8bu/ladilala/core/store"
)

// Recording states sent in the record.state event.
const (
	recStarting  = "starting"
	recRecording = "recording"
	recPaused    = "paused"
	// recStopped: the user pressed Stop in the page overlay. The UI then calls StopRecording.
	recStopped = "stopped"
	// recClosed: the browser ended. The steps so far stay readable.
	recClosed = "closed"
)

// RecordWarning is something the user did that the recorder cannot record:
// a click on a canvas or on a file input. No step exists for it.
type RecordWarning struct {
	// Kind is canvas_unsupported or file_input_unsupported.
	Kind string `json:"kind"`
	// StepIndex is the position in the macro where the missing step belongs.
	StepIndex int            `json:"stepIndex"`
	Locator   *macro.Locator `json:"locator,omitempty"`
	Message   string         `json:"message,omitempty"`
}

// StartRecordingInput starts a recording in a stopped profile.
type StartRecordingInput struct {
	ProfileID string `json:"profileId"`
	StartURL  string `json:"startUrl"`
	Name      string `json:"name"`
	// Lang is "vi" or "en": the language of the overlay texts.
	Lang string `json:"lang,omitempty"`
	// Hint shows the first-use hint card in the page overlay.
	Hint bool `json:"hint,omitempty"`
}

// RecordColumn is a data column the user made by typing {{name}} in a page field.
type RecordColumn struct {
	Name string `json:"name"`
	// Kind is text, email, phone, number, url, date or password.
	Kind      string `json:"kind"`
	Protected bool   `json:"protected"`
	// Steps is how many steps read the column.
	Steps int `json:"steps"`
}

// RecordingDraft is the result of StopRecording. The macro is not saved.
type RecordingDraft struct {
	Macro    macro.Macro     `json:"macro"`
	Warnings []RecordWarning `json:"warnings"`
	// Problems lists why the draft cannot be saved yet (for example no step was recorded).
	Problems []ProblemDTO `json:"problems"`
	// Columns lists the data columns of the draft in macro order.
	Columns []RecordColumn `json:"columns"`
}

// RecordingStatus is the state of a recording that is open.
type RecordingStatus struct {
	RecordID     string `json:"recordId"`
	State        string `json:"state"`
	StepCount    int    `json:"stepCount"`
	WarningCount int    `json:"warningCount"`
}

type recording struct {
	id      string
	rec     *recorder.Recorder
	l       *driver.Launcher
	sess    *driver.StockSession
	cleanup func()
	cancel  context.CancelFunc
	quit    chan struct{} // closed to end the watcher
	watched chan struct{} // closed when the watcher has ended

	mu    sync.Mutex
	state string
	steps []macro.Step
	warns int
	// hintSent and hintNever remember the last hint event; only the watcher uses them.
	hintSent, hintNever bool
}

func (r *recording) setState(s string) (changed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == s {
		return false
	}
	r.state = s
	return true
}

func (r *recording) getState() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func toRecordWarning(w recorder.Warning) RecordWarning {
	out := RecordWarning{Kind: string(w.Kind), StepIndex: w.StepIndex, Message: w.Message}
	if w.Locator.Value != "" {
		l := w.Locator
		out.Locator = &l
	}
	return out
}

// StartRecording launches the profile through the stock path with the
// recorder attached and opens startURL. Events: record.state, record.step,
// record.warning. It returns the record ID.
func (s *MacroService) StartRecording(ctx context.Context, in StartRecordingInput) (string, error) {
	u, err := url.Parse(strings.TrimSpace(in.StartURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", apiErr("invalid", "the start URL must be http or https", "problems", []ProblemDTO{{Path: "startUrl", Msg: "must be an http or https URL"}})
	}
	prof, err := s.b.Store.Profiles.Get(ctx, in.ProfileID)
	if err != nil {
		return "", toMacroAPIError(err)
	}
	brand, unverified := driver.BrandChrome, false
	if s.b.Launcher != nil {
		if s.b.Launcher.Brand != "" {
			brand = s.b.Launcher.Brand
		}
		unverified = s.b.Launcher.Unverified
	}
	installs, err := driver.StockChrome{}.Detect()
	if err != nil {
		return "", toMacroAPIError(err)
	}
	var install driver.Install
	found := false
	for _, c := range installs {
		if c.Brand == brand {
			install, found = c, true
		}
	}
	if !found {
		return "", toMacroAPIError(fmt.Errorf("%w: no %s install", driver.ErrBinaryNotFound, brand))
	}
	name := strings.TrimSpace(in.Name)
	r := &recording{id: store.NewID(), state: recStarting, quit: make(chan struct{}), watched: make(chan struct{})}
	r.rec = recorder.New(recorder.Options{ID: newMacroID(), Name: name})
	r.rec.SetOptions(in.Lang, in.Hint)

	// The relay and the browser outlive this call: they live until StopRecording.
	lifeCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	var relay *proxy.Relay
	addr := ""
	if prof.Proxy.Scheme != "" {
		if relay, err = proxy.New(prof.Proxy); err != nil {
			cancel()
			return "", toMacroAPIError(err)
		}
		if addr, err = relay.Start(lifeCtx); err != nil {
			cancel()
			return "", toMacroAPIError(err)
		}
	}
	r.cleanup = func() {
		if relay != nil {
			relay.Close()
		}
		cancel()
	}
	if r.l, err = driver.NewLauncher(); err != nil {
		r.cleanup()
		return "", toMacroAPIError(err)
	}
	r.l.StockHook = r.rec.Hook
	if unverified {
		r.sess, err = r.l.LaunchStockUnverified(ctx, driver.StockChrome{}, install, prof.Profile, addr)
	} else {
		r.sess, _, err = r.l.LaunchStock(ctx, driver.StockChrome{}, install, prof.Profile, addr)
	}
	if err != nil {
		_ = r.l.Close()
		r.cleanup()
		return "", toMacroAPIError(err)
	}
	pg, err := r.sess.WaitPage(ctx)
	if err == nil {
		err = pg.Navigate(ctx, u.String())
	}
	if err != nil {
		r.close()
		return "", toMacroAPIError(err)
	}
	s.mu.Lock()
	s.recs[r.id] = r
	s.mu.Unlock()
	r.setState(recRecording)
	s.emit("record.state", map[string]any{"recordId": r.id, "state": recRecording})
	go s.watch(r)
	return r.id, nil
}

// watch turns the recorder's progress into events until the recording ends.
func (s *MacroService) watch(r *recording) {
	defer close(r.watched)
	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	flush := func() {
		steps := r.rec.Steps()
		r.mu.Lock()
		prev := r.steps
		r.steps = steps
		r.mu.Unlock()
		// A step that only grew the list is sent alone. A change inside the list (a
		// column renamed or deleted in the overlay) sends the whole list.
		rewritten := len(steps) < len(prev)
		for i := 0; i < len(prev) && i < len(steps) && !rewritten; i++ {
			rewritten = !reflect.DeepEqual(prev[i], steps[i])
		}
		if rewritten {
			s.emit("record.steps", map[string]any{"recordId": r.id, "steps": steps})
		} else {
			for i := len(prev); i < len(steps); i++ {
				s.emit("record.step", map[string]any{"recordId": r.id, "stepIndex": i, "step": steps[i]})
			}
		}
		if closed, never := r.rec.HintState(); closed && (!r.hintSent || never != r.hintNever) {
			r.hintSent, r.hintNever = true, never
			s.emit("record.hint", map[string]any{"recordId": r.id, "closed": closed, "never": never})
		}
		ws := r.rec.Warnings()
		r.mu.Lock()
		from := r.warns
		r.warns = len(ws)
		r.mu.Unlock()
		for i := from; i < len(ws); i++ {
			w := toRecordWarning(ws[i])
			s.emit("record.warning", map[string]any{"recordId": r.id, "kind": w.Kind, "stepIndex": w.StepIndex, "locator": w.Locator, "message": w.Message})
		}
	}
	overlay, sessDone := r.rec.Done(), r.sess.Done()
	for {
		select {
		case <-t.C:
			flush()
		case <-overlay:
			flush()
			if r.setState(recStopped) {
				s.emit("record.state", map[string]any{"recordId": r.id, "state": recStopped})
			}
			overlay = nil
		case <-sessDone:
			flush()
			if r.setState(recClosed) {
				s.emit("record.state", map[string]any{"recordId": r.id, "state": recClosed})
			}
			sessDone = nil
		case <-r.quit:
			flush()
			return
		}
	}
}

// close ends the browser, the launcher and the relay. It is safe to call twice.
func (r *recording) close() {
	_ = r.sess.Close()
	_ = r.l.Close()
	r.cleanup()
}

func (s *MacroService) takeRecording(id string) (*recording, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.recs[id]
	if !ok {
		return nil, apiErr("notFound", "no such recording", "what", "recording")
	}
	return r, nil
}

func (s *MacroService) endRecording(r *recording) {
	r.rec.Stop()
	close(r.quit)
	<-r.watched
	r.close()
	s.mu.Lock()
	delete(s.recs, r.id)
	s.mu.Unlock()
}

// PauseRecording stops capturing until ResumeRecording. The browser stays open.
func (s *MacroService) PauseRecording(_ context.Context, recordID string) error {
	return s.setPaused(recordID, true)
}

// ResumeRecording continues a paused recording.
func (s *MacroService) ResumeRecording(_ context.Context, recordID string) error {
	return s.setPaused(recordID, false)
}

func (s *MacroService) setPaused(id string, paused bool) error {
	r, err := s.takeRecording(id)
	if err != nil {
		return err
	}
	if st := r.getState(); st != recRecording && st != recPaused {
		return apiErr("conflict", "the recording is not running", "state", st)
	}
	r.rec.SetPaused(paused)
	state := recRecording
	if paused {
		state = recPaused
	}
	if r.setState(state) {
		s.emit("record.state", map[string]any{"recordId": id, "state": state})
	}
	return nil
}

// StopRecording ends the recording, closes the browser and returns the macro
// draft with the warnings. Nothing is saved: the UI shows the draft, then
// calls SaveMacro.
func (s *MacroService) StopRecording(_ context.Context, recordID string) (RecordingDraft, error) {
	r, err := s.takeRecording(recordID)
	if err != nil {
		return RecordingDraft{}, err
	}
	s.endRecording(r)
	m, merr := r.rec.Macro()
	draft := RecordingDraft{Macro: *m, Warnings: []RecordWarning{}, Problems: []ProblemDTO{}, Columns: draftColumns(r.rec)}
	for _, w := range r.rec.Warnings() {
		draft.Warnings = append(draft.Warnings, toRecordWarning(w))
	}
	if merr != nil {
		var ps = problemsOf(merr)
		if ps == nil {
			return RecordingDraft{}, toMacroAPIError(errors.Join(errors.New("recorder"), merr))
		}
		draft.Problems = ps
	}
	if draft.Macro.Steps == nil {
		draft.Macro.Steps = []macro.Step{}
	}
	if draft.Macro.AllowedOrigins == nil {
		draft.Macro.AllowedOrigins = []string{}
	}
	return draft, nil
}

// draftColumns lists the columns the steps still read, in order of first use.
func draftColumns(rec *recorder.Recorder) []RecordColumn {
	m, _ := rec.Macro()
	out := []RecordColumn{}
	if m == nil {
		return out
	}
	kinds := map[string]recorder.Column{}
	for _, c := range rec.Columns() {
		kinds[c.Name] = c
	}
	for _, c := range data.TemplateColumns(m) {
		out = append(out, RecordColumn{Name: c.Name, Kind: string(c.Kind), Protected: c.Kind == macro.KindPassword || kinds[c.Name].Protected, Steps: c.Steps})
	}
	return out
}

// DiscardRecording closes the browser and drops the recording.
func (s *MacroService) DiscardRecording(_ context.Context, recordID string) error {
	r, err := s.takeRecording(recordID)
	if err != nil {
		return err
	}
	s.endRecording(r)
	return nil
}

// GetRecordingStatus reports the state and the counts of an open recording.
func (s *MacroService) GetRecordingStatus(_ context.Context, recordID string) (RecordingStatus, error) {
	r, err := s.takeRecording(recordID)
	if err != nil {
		return RecordingStatus{}, err
	}
	return RecordingStatus{RecordID: r.id, State: r.getState(), StepCount: len(r.rec.Steps()), WarningCount: len(r.rec.Warnings())}, nil
}

// PickMacroSavePath opens the native save dialog for a macro export. It
// returns an empty path when the user cancels.
func (s *MacroService) PickMacroSavePath(_ context.Context, fileName string) (string, error) {
	return pickSavePath(s.b, fileName, "JSON", "*.json")
}
