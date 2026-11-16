package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/player"
	"github.com/8bu/ladilala/core/store"
)

var errLedger = errors.New("engine: ledger write failed")

// runtime is the live part of a run: its workers and its gate.
type runtime struct {
	svc  *Service
	id   string
	m    *macro.Macro
	opts RunOptions

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	wg     sync.WaitGroup

	closing atomic.Bool // Service.Close: end as paused, not finished
	opMu    sync.Mutex  // one public operation at a time

	smu         sync.Mutex // guards the fields below
	state       RunState
	reason      string
	gate        chan struct{} // non-nil while workers must wait (paused, takeover)
	blocked     int           // workers waiting at the gate
	alive       int           // workers not yet exited
	consecutive int           // failed rows in a row
	fatal       error
	active      map[string]Browser // profile ID -> browser with a row in progress
}

// launch starts the workers of a run. reason is the state reason to record.
func (s *Service) launch(rec store.RunRecord, reason string) error {
	m, err := macro.Load(bytes.NewReader(rec.MacroBody))
	if err != nil {
		return fmt.Errorf("engine: run %s macro: %w", rec.ID, err)
	}
	var opts RunOptions
	if err := json.Unmarshal(rec.Options, &opts); err != nil {
		return err
	}
	opts.fill()
	ctx, cancel := context.WithCancel(context.Background())
	rt := &runtime{svc: s, id: rec.ID, m: m, opts: opts, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		state: RunState(rec.State), active: map[string]Browser{}}
	s.mu.Lock()
	if old := s.runs[rec.ID]; old != nil {
		select {
		case <-old.done:
		default:
			s.mu.Unlock()
			cancel()
			return &StateError{RunID: rec.ID, State: old.currentState(), Op: "start"}
		}
	}
	s.runs[rec.ID] = rt
	s.mu.Unlock()
	if _, ok := rt.transition([]RunState{StateCreated, StatePaused, StateFailed, StateCrashedRecovered}, StateRunning, reasonOr(reason, "started")); !ok {
		cancel()
		close(rt.done)
		return &StateError{RunID: rec.ID, State: rt.currentState(), Op: "start"}
	}
	n := opts.Concurrency
	if n > len(rec.ProfileIDs) {
		n = len(rec.ProfileIDs)
	}
	spares := make(chan string, len(rec.ProfileIDs))
	for _, p := range rec.ProfileIDs[n:] {
		spares <- p
	}
	rt.alive = n
	rt.wg.Add(n)
	for _, p := range rec.ProfileIDs[:n] {
		go rt.worker(p, spares)
	}
	go func() {
		rt.wg.Wait()
		rt.finalize()
	}()
	return nil
}

func reasonOr(r, d string) string {
	if r == "" {
		return d
	}
	return r
}

func (r *runtime) currentState() RunState {
	r.smu.Lock()
	defer r.smu.Unlock()
	return r.state
}

// transition moves the run to `to` when it is in one of `from`. It persists
// the state, moves the gate and emits the event. It returns the state it found.
func (r *runtime) transition(from []RunState, to RunState, reason string) (RunState, bool) {
	r.smu.Lock()
	defer r.smu.Unlock()
	ok := false
	for _, f := range from {
		ok = ok || f == r.state
	}
	if !ok {
		return r.state, false
	}
	r.state, r.reason = to, reason
	switch to {
	case StatePaused, StateTakeover:
		if r.gate == nil {
			r.gate = make(chan struct{})
		}
	default:
		if r.gate != nil {
			close(r.gate)
			r.gate = nil
		}
	}
	if to == StateRunning {
		r.consecutive = 0
	}
	_ = r.svc.st.Runs.SetState(context.Background(), r.id, string(to), reason)
	r.svc.emit(Event{RunID: r.id, Kind: EventRunState, RowIndex: -1, RunState: to, Message: reason})
	return to, true
}

// waitOpen blocks while the run is paused or in takeover. It returns an error
// when the run is stopped meanwhile.
func (r *runtime) waitOpen(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.ctx.Err(); err != nil {
			return err
		}
		r.smu.Lock()
		g := r.gate
		if g != nil {
			r.blocked++
		}
		r.smu.Unlock()
		if g == nil {
			return nil
		}
		select {
		case <-g:
		case <-ctx.Done():
		case <-r.ctx.Done():
		}
		r.smu.Lock()
		r.blocked--
		r.smu.Unlock()
	}
}

// waitQuiet returns when every live worker waits at the gate, or after max.
func (r *runtime) waitQuiet(ctx context.Context, max time.Duration) {
	deadline := time.NewTimer(max)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		r.smu.Lock()
		quiet := r.blocked >= r.alive
		r.smu.Unlock()
		if quiet {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (r *runtime) activeBrowsers() []Browser {
	r.smu.Lock()
	defer r.smu.Unlock()
	out := make([]Browser, 0, len(r.active))
	for _, b := range r.active {
		out = append(out, b)
	}
	return out
}

func (r *runtime) setActive(profileID string, b Browser) {
	r.smu.Lock()
	defer r.smu.Unlock()
	if b == nil {
		delete(r.active, profileID)
	} else {
		r.active[profileID] = b
	}
}

func (r *runtime) setFatal(err error) {
	r.smu.Lock()
	if r.fatal == nil {
		r.fatal = err
	}
	r.smu.Unlock()
}

func (r *runtime) emit(kind EventKind, row int, profileID string, rs RowState, msg string) {
	r.svc.emit(Event{RunID: r.id, Kind: kind, RowIndex: row, ProfileID: profileID, RowState: rs, Message: msg})
}

// finalize settles the run after the last worker left.
func (r *runtime) finalize() {
	defer func() {
		close(r.done)
		r.svc.mu.Lock()
		if r.svc.runs[r.id] == r {
			delete(r.svc.runs, r.id)
		}
		r.svc.mu.Unlock()
	}()
	counts, _ := r.svc.st.Rows.Counts(context.Background(), r.id)
	pending := counts[string(RowPending)]
	r.smu.Lock()
	fatal, st := r.fatal, r.state
	r.smu.Unlock()
	all := []RunState{StateRunning, StatePaused, StateTakeover, StateStopping}
	switch {
	case r.closing.Load():
		r.transition(all, StatePaused, "the app closed")
	case st == StateStopping:
		r.transition(all, StateFinished, "stopped by the user")
	case fatal != nil && pending > 0:
		r.transition(all, StateFailed, fatal.Error())
	case pending > 0:
		r.transition(all, StatePaused, fmt.Sprintf("%d rows are still pending", pending))
	default:
		r.transition(all, StateFinished, "all rows are settled")
	}
}

// ---- workers ----------------------------------------------------------------------

func (r *runtime) worker(profileID string, spares chan string) {
	defer r.wg.Done()
	defer func() {
		r.smu.Lock()
		r.alive--
		r.smu.Unlock()
	}()
	var b Browser
	defer func() {
		if b != nil {
			_ = b.Close()
		}
	}()
	restarts := 0
	// giveUp is called when the profile's browser cannot be kept alive. It
	// returns true when the worker goes on with a spare profile.
	giveUp := func(err error) bool {
		select {
		case p := <-spares:
			r.emit(EventBrowser, -1, profileID, "", fmt.Sprintf("profile %s gave up (%v); using %s", profileID, err, p))
			profileID, restarts = p, 0
			return true
		default:
			r.setFatal(fmt.Errorf("profile %s: %w", profileID, err))
			return false
		}
	}
	for {
		if r.waitOpen(r.ctx) != nil {
			return
		}
		row, ok, err := r.svc.st.Rows.Claim(r.ctx, r.id, profileID)
		if err != nil {
			if r.ctx.Err() == nil {
				r.setFatal(fmt.Errorf("ledger: %w", err))
			}
			return
		}
		if !ok {
			return
		}
		r.emit(EventRowStarted, row.Index, profileID, RowRunning, "")
		if _, perr := player.Preflight(r.m, row.Data); perr != nil {
			r.finishRow(row, profileID, RowSkipped, kindOf(perr), perr.Error(), stepOf(perr))
			continue
		}
		if b != nil {
			select {
			case <-b.Done():
				_ = b.Close()
				b = nil
			default:
			}
		}
		if b == nil {
			nb, err := r.launchBrowser(profileID)
			if err != nil {
				r.release(row, profileID, "browser did not start: "+err.Error())
				restarts++
				if r.ctx.Err() != nil {
					return
				}
				r.emit(EventBrowser, -1, profileID, "", "browser did not start: "+err.Error())
				if restarts > r.opts.MaxRestarts && !giveUp(err) {
					return
				}
				if !sleepCtx(r.ctx, time.Duration(restarts)*300*time.Millisecond) {
					return
				}
				continue
			}
			b = nb
			r.emit(EventBrowser, -1, profileID, "", "browser started")
		}
		r.setActive(profileID, b)
		committed, err := r.play(b, row, profileID)
		r.setActive(profileID, nil)
		died := errors.Is(err, ErrBrowserDied)
		switch {
		case err == nil:
			r.finishRow(row, profileID, RowDone, "", "", "")
			r.resetConsecutive()
		case r.ctx.Err() != nil:
			r.settleInterrupted(row, profileID, committed, KindStopped, "the run was stopped")
		case died:
			r.settleInterrupted(row, profileID, committed, KindBrowser, err.Error())
			_ = b.Close()
			b = nil
			restarts++
			r.emit(EventBrowser, -1, profileID, "", "browser died: "+err.Error())
			if restarts > r.opts.MaxRestarts && !giveUp(err) {
				return
			}
		default:
			r.settleError(row, profileID, committed, err)
		}
		if r.opts.DelayMs > 0 && !sleepCtx(r.ctx, time.Duration(r.opts.DelayMs)*time.Millisecond) {
			return
		}
	}
}

func (r *runtime) launchBrowser(profileID string) (Browser, error) {
	rec, err := r.svc.st.Profiles.Get(r.ctx, profileID)
	if err != nil {
		return nil, err
	}
	return r.svc.launcher.Launch(r.ctx, rec.Profile)
}

// play runs the macro for one row. committed is true when the commit point
// step was entered (the "submitting" mark is in the ledger).
func (r *runtime) play(b Browser, row store.RowRecord, profileID string) (committed bool, err error) {
	dbc := context.Background()
	h := player.Hooks{
		DryRun: r.opts.DryRun,
		BeforeStep: func(ctx context.Context, _ int, _ *macro.Step) error {
			return r.waitOpen(ctx)
		},
		BeforeCommit: func(ctx context.Context, s *macro.Step) error {
			if err := r.svc.st.Rows.SetCommit(dbc, r.id, row.Index, string(CommitSubmitting), s.ID); err != nil {
				return fmt.Errorf("%w: %v", errLedger, err)
			}
			committed = true
			r.emit(EventRowCommit, row.Index, profileID, RowRunning, "submitting at step "+s.ID)
			return nil
		},
		AfterCommit: func(ctx context.Context, s *macro.Step, err error) {
			if err == nil {
				_ = r.svc.st.Rows.SetCommit(dbc, r.id, row.Index, string(CommitSubmitted), s.ID)
			}
		},
		Manual: func(ctx context.Context, s *macro.Step, instruction string) error {
			r.transition([]RunState{StateRunning, StatePaused, StateTakeover}, StateTakeover, "manual step "+s.ID+": "+instruction)
			return r.waitOpen(ctx)
		},
	}
	err = b.PlayRow(r.ctx, r.m, row.Data, h)
	return committed, err
}

func (r *runtime) finishRow(row store.RowRecord, profileID string, st RowState, kind, msg, step string) {
	err := r.svc.st.Rows.Finish(context.Background(), r.id, row.Index, string(st), kind, msg, step)
	if err != nil {
		r.emit(EventBrowser, row.Index, profileID, "", fmt.Sprintf("ledger refused the %s mark of row %d: %v", st, row.Index, err))
		return
	}
	k := map[RowState]EventKind{RowDone: EventRowDone, RowFailed: EventRowFailed, RowSkipped: EventRowSkipped, RowUncertain: EventRowUncertain}[st]
	r.emit(k, row.Index, profileID, st, msg)
}

func (r *runtime) release(row store.RowRecord, profileID, why string) {
	if err := r.svc.st.Rows.Release(context.Background(), r.id, row.Index); err != nil {
		return
	}
	r.emit(EventRowReleased, row.Index, profileID, RowPending, why)
}

// settleInterrupted handles a row cut short by a stop or a dead browser.
func (r *runtime) settleInterrupted(row store.RowRecord, profileID string, committed bool, kind, msg string) {
	if committed {
		r.finishRow(row, profileID, RowUncertain, kind, msg+" after the commit point", "")
		return
	}
	r.release(row, profileID, msg)
}

// settleError handles a row that failed with a step or guard error.
func (r *runtime) settleError(row store.RowRecord, profileID string, committed bool, err error) {
	kind, step := kindOf(err), stepOf(err)
	var ge *player.GuardError
	if errors.As(err, &ge) && ge.Guard.Action != macro.GuardFail {
		// needsLogin and pause: the page is not ready. Put the row back and wait for the user.
		r.settleInterrupted(row, profileID, committed, KindGuard, err.Error())
		r.transition([]RunState{StateRunning}, StatePaused, "guard: "+ge.Why)
		return
	}
	if committed {
		r.finishRow(row, profileID, RowUncertain, kind, err.Error()+" (after the commit point)", step)
		return
	}
	r.finishRow(row, profileID, RowFailed, kind, err.Error(), step)
	r.smu.Lock()
	r.consecutive++
	hit := r.consecutive >= r.opts.StopAfterErrors
	n := r.consecutive
	r.smu.Unlock()
	if hit {
		r.transition([]RunState{StateRunning}, StatePaused, fmt.Sprintf("%d rows failed in a row", n))
	}
}

func (r *runtime) resetConsecutive() {
	r.smu.Lock()
	r.consecutive = 0
	r.smu.Unlock()
}

func kindOf(err error) string {
	switch {
	case errors.Is(err, errLedger):
		return KindLedger
	case errors.Is(err, player.ErrOrigin):
		return KindOrigin
	case errors.Is(err, player.ErrMissingColumn), errors.Is(err, player.ErrBadValue):
		return KindData
	case errors.Is(err, player.ErrNotFound), errors.Is(err, player.ErrAmbiguous), errors.Is(err, player.ErrObscured):
		return KindLocator
	case errors.Is(err, player.ErrGuard):
		return KindGuard
	case errors.Is(err, context.DeadlineExceeded):
		return KindTimeout
	}
	return KindStep
}

func stepOf(err error) string {
	var se *player.StepError
	if errors.As(err, &se) {
		return se.StepID
	}
	return ""
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
