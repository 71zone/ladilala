package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/8bu/ladilala/core/data"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/store"
)

// quietWait bounds how long PauseRun and Takeover wait for the browsers to
// stop acting (ADR 0014: pause and resume under 15 seconds).
const quietWait = 15 * time.Second

// Service is the API the UI calls. Create one per app with NewService.
type Service struct {
	st       *store.Store
	launcher Launcher
	hub      hub

	mu   sync.Mutex
	runs map[string]*runtime
}

// NewService returns a Service on the store, starting browsers through l.
// Call Recover once at app start.
func NewService(st *store.Store, l Launcher) *Service {
	return &Service{st: st, launcher: l, runs: map[string]*runtime{}}
}

// CreateRunRequest describes a run. The engine copies the rows of the range
// into the ledger and does not close src.
type CreateRunRequest struct {
	MacroID    string
	Source     data.Source
	SourceID   string // optional: the saved source config this came from
	From, To   int    // 0-based row range [From,To); To < 0 means to the end
	ProfileIDs []string
	Options    RunOptions
}

// CreateRun checks the request, copies the macro and the data rows into the
// ledger and returns the run in state created.
func (s *Service) CreateRun(ctx context.Context, req CreateRunRequest) (Run, error) {
	if len(req.ProfileIDs) == 0 {
		return Run{}, ErrNoProfiles
	}
	req.Options.fill()
	if req.Options.Concurrency > len(req.ProfileIDs) {
		return Run{}, ErrNotEnoughProfiles
	}
	if req.Source == nil {
		return Run{}, errors.New("engine: a run needs a data source")
	}
	mrec, err := s.st.Macros.Get(ctx, req.MacroID)
	if err != nil {
		return Run{}, fmt.Errorf("engine: macro %q: %w", req.MacroID, err)
	}
	if _, err := macro.Load(bytes.NewReader(mrec.Body)); err != nil {
		return Run{}, fmt.Errorf("engine: macro %q: %w", req.MacroID, err)
	}
	seen := map[string]bool{}
	for _, id := range req.ProfileIDs {
		if seen[id] {
			return Run{}, fmt.Errorf("engine: profile %q is listed twice", id)
		}
		seen[id] = true
		if _, err := s.st.Profiles.Get(ctx, id); err != nil {
			return Run{}, fmt.Errorf("engine: profile %q: %w", id, err)
		}
	}
	to := req.To
	rows, err := req.Source.Open(ctx, req.From, to)
	if err != nil {
		return Run{}, err
	}
	defer rows.Close()
	var seeds []store.RowSeed
	for {
		idx, row, ok, err := rows.Next(ctx)
		var re data.RowError
		if errors.As(err, &re) {
			seeds = append(seeds, store.RowSeed{Index: re.RowIndex(), Data: map[string]string{}, State: string(RowSkipped), ErrorKind: KindData, ErrorMsg: err.Error()})
			if req.Options.TestOne {
				break
			}
			continue
		}
		if err != nil {
			return Run{}, err
		}
		if !ok {
			break
		}
		seeds = append(seeds, store.RowSeed{Index: idx, Data: row})
		if req.Options.TestOne {
			break
		}
	}
	if len(seeds) == 0 {
		return Run{}, ErrNoRows
	}
	opts, _ := json.Marshal(req.Options)
	rec, err := s.st.Runs.Create(ctx, store.RunRecord{
		MacroID: mrec.ID, MacroName: mrec.Name, MacroVersion: mrec.Version, MacroHash: mrec.Hash, MacroBody: mrec.Body,
		SourceID: req.SourceID, State: string(StateCreated), Options: opts, ProfileIDs: req.ProfileIDs, RowFrom: req.From, RowTo: to,
	}, seeds)
	if err != nil {
		return Run{}, err
	}
	return s.toRun(ctx, rec)
}

// StartRun starts a run in state created.
func (s *Service) StartRun(ctx context.Context, id string) error {
	rec, err := s.run(ctx, id)
	if err != nil {
		return err
	}
	if RunState(rec.State) != StateCreated {
		return &StateError{RunID: id, State: RunState(rec.State), Op: "start"}
	}
	return s.launch(rec, "")
}

// PauseRun stops the run at the next step boundary of every browser and
// returns when all of them are quiet or after 15 seconds. The run is paused
// either way.
func (s *Service) PauseRun(ctx context.Context, id string) error {
	rt, err := s.live(ctx, id, "pause")
	if err != nil {
		return err
	}
	rt.opMu.Lock()
	defer rt.opMu.Unlock()
	if st, ok := rt.transition([]RunState{StateRunning}, StatePaused, "paused by the user"); !ok {
		return &StateError{RunID: id, State: st, Op: "pause"}
	}
	rt.waitQuiet(ctx, quietWait)
	return nil
}

// ResumeRun continues a paused run. It also restarts a run that failed, was
// recovered after a crash, or was reset for a retry: pending rows go on, rows
// done or uncertain are never touched.
func (s *Service) ResumeRun(ctx context.Context, id string) error {
	rec, err := s.run(ctx, id)
	if err != nil {
		return err
	}
	if rt := s.active(id); rt != nil {
		rt.opMu.Lock()
		defer rt.opMu.Unlock()
		if st, ok := rt.transition([]RunState{StatePaused}, StateRunning, "resumed by the user"); !ok {
			return &StateError{RunID: id, State: st, Op: "resume"}
		}
		return nil
	}
	switch RunState(rec.State) {
	case StatePaused, StateFailed, StateCrashedRecovered:
		return s.launch(rec, "resumed by the user")
	}
	return &StateError{RunID: id, State: RunState(rec.State), Op: "resume"}
}

// StopRun ends the run. Browsers close, rows in flight go back to pending
// (uncertain when past the commit point) and the run finishes.
func (s *Service) StopRun(ctx context.Context, id string) error {
	rec, err := s.run(ctx, id)
	if err != nil {
		return err
	}
	rt := s.active(id)
	if rt == nil {
		switch RunState(rec.State) {
		case StateCreated, StatePaused, StateFailed, StateCrashedRecovered:
			return s.setIdleState(id, StateFinished, "stopped by the user")
		}
		return &StateError{RunID: id, State: RunState(rec.State), Op: "stop"}
	}
	rt.opMu.Lock()
	defer rt.opMu.Unlock()
	if st, ok := rt.transition([]RunState{StateRunning, StatePaused, StateTakeover}, StateStopping, "stopped by the user"); !ok {
		return &StateError{RunID: id, State: st, Op: "stop"}
	}
	rt.cancel()
	select {
	case <-rt.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(quietWait):
		return nil
	}
}

// Takeover pauses the run and keeps the browsers open so the user can work in
// them. A row in progress waits at its current step.
func (s *Service) Takeover(ctx context.Context, id string) error {
	rt, err := s.live(ctx, id, "take over")
	if err != nil {
		return err
	}
	rt.opMu.Lock()
	defer rt.opMu.Unlock()
	if st, ok := rt.transition([]RunState{StateRunning, StatePaused}, StateTakeover, "the user controls the browser"); !ok {
		return &StateError{RunID: id, State: st, Op: "take over"}
	}
	rt.waitQuiet(ctx, quietWait)
	return nil
}

// ReleaseTakeover gives the browsers back. Each page in progress is checked
// against the macro guards first; if one fires the run stays in takeover and
// the error is a *GuardFiredError.
func (s *Service) ReleaseTakeover(ctx context.Context, id string) error {
	rt, err := s.live(ctx, id, "release takeover of")
	if err != nil {
		return err
	}
	rt.opMu.Lock()
	defer rt.opMu.Unlock()
	if st := rt.currentState(); st != StateTakeover {
		return &StateError{RunID: id, State: st, Op: "release takeover of"}
	}
	for _, b := range rt.activeBrowsers() {
		if err := b.Recheck(ctx, rt.m); err != nil {
			return &GuardFiredError{Err: err}
		}
	}
	if st, ok := rt.transition([]RunState{StateTakeover}, StateRunning, "takeover released"); !ok {
		return &StateError{RunID: id, State: st, Op: "release takeover of"}
	}
	return nil
}

// RetryRows puts failed and skipped rows that match f back to pending. A
// finished, failed or crashed run becomes paused: call ResumeRun to go on.
// Uncertain rows are never reset, decide them with MarkUncertainRow. It
// returns the number of rows reset.
func (s *Service) RetryRows(ctx context.Context, id string, f RowFilter) (int, error) {
	rec, err := s.run(ctx, id)
	if err != nil {
		return 0, err
	}
	st := RunState(rec.State)
	switch st {
	case StateCreated, StatePaused, StateFinished, StateFailed, StateCrashedRecovered:
	default:
		return 0, &StateError{RunID: id, State: st, Op: "retry rows of"}
	}
	q := store.RowQuery{RunID: id, Indexes: f.Indexes}
	for _, rs := range f.States {
		q.States = append(q.States, string(rs))
	}
	n, err := s.st.Rows.Reset(ctx, q)
	if err != nil || n == 0 {
		return n, err
	}
	if rt := s.active(id); rt == nil && (st == StateFinished || st == StateFailed || st == StateCrashedRecovered) {
		if err := s.setIdleState(id, StatePaused, fmt.Sprintf("%d rows reset for retry", n)); err != nil {
			return n, err
		}
	}
	return n, nil
}

// MarkUncertainRow records what the user found out about an uncertain row.
func (s *Service) MarkUncertainRow(ctx context.Context, id string, index int, res Resolution) error {
	if res != ResolveDone && res != ResolveFailed {
		return fmt.Errorf("engine: resolution must be %q or %q", ResolveDone, ResolveFailed)
	}
	if _, err := s.run(ctx, id); err != nil {
		return err
	}
	if err := s.st.Rows.Resolve(ctx, id, index, string(res)); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return ErrNotUncertain
		}
		return err
	}
	rs := RowDone
	if res == ResolveFailed {
		rs = RowFailed
	}
	s.emit(Event{RunID: id, Kind: EventRowResolved, RowIndex: index, RowState: rs, Message: "marked " + string(res) + " by the user"})
	return nil
}

// ListRuns returns runs, newest first.
func (s *Service) ListRuns(ctx context.Context, f RunFilter) ([]Run, error) {
	sf := store.RunFilter{Offset: f.Offset, Limit: f.Limit}
	for _, st := range f.States {
		sf.States = append(sf.States, string(st))
	}
	recs, err := s.st.Runs.List(ctx, sf)
	if err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(recs))
	for _, r := range recs {
		run, err := s.toRun(ctx, r)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

// GetRun returns one run with its row counts.
func (s *Service) GetRun(ctx context.Context, id string) (Run, error) {
	rec, err := s.run(ctx, id)
	if err != nil {
		return Run{}, err
	}
	return s.toRun(ctx, rec)
}

// ListRows returns one page of the ledger of a run.
func (s *Service) ListRows(ctx context.Context, id string, f RowFilter) (RowPage, error) {
	if _, err := s.run(ctx, id); err != nil {
		return RowPage{}, err
	}
	q := store.RowQuery{RunID: id, Indexes: f.Indexes, Offset: f.Offset, Limit: f.Limit}
	for _, rs := range f.States {
		q.States = append(q.States, string(rs))
	}
	recs, total, err := s.st.Rows.List(ctx, q)
	if err != nil {
		return RowPage{}, err
	}
	page := RowPage{Total: total, Rows: make([]Row, 0, len(recs))}
	for _, r := range recs {
		page.Rows = append(page.Rows, Row{
			RunID: r.RunID, Index: r.Index, IdempotencyKey: r.IdempotencyKey, State: RowState(r.State), CommitState: CommitState(r.CommitState),
			ProfileID: r.ProfileID, Attempts: r.Attempts, Data: r.Data, ErrorKind: r.ErrorKind, ErrorMsg: r.ErrorMsg, StepID: r.StepID,
			StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		})
	}
	return page, nil
}

// Subscribe returns a channel of events of the run. The channel closes when
// ctx ends. It has a buffer of 256; a reader that is too slow loses the
// oldest events (Event.Dropped counts them) and never slows the run.
func (s *Service) Subscribe(ctx context.Context, id string) (<-chan Event, error) {
	if _, err := s.run(ctx, id); err != nil {
		return nil, err
	}
	return s.hub.subscribe(ctx, id), nil
}

// Recover settles runs a dead process left in running, paused, takeover or
// stopping. Rows in flight go back to pending, or become uncertain when they
// passed their commit point. The runs become crashed_recovered; nothing is
// resumed. Call it once at app start, before any StartRun or ResumeRun.
func (s *Service) Recover(ctx context.Context) ([]Recovery, error) {
	recs, err := s.st.Runs.List(ctx, store.RunFilter{
		States: []string{string(StateRunning), string(StatePaused), string(StateTakeover), string(StateStopping)}, Limit: 10000,
	})
	if err != nil {
		return nil, err
	}
	var out []Recovery
	for _, rec := range recs {
		if s.active(rec.ID) != nil {
			continue
		}
		p, u, err := s.st.Rows.Recover(ctx, rec.ID)
		if err != nil {
			return out, err
		}
		if err := s.setIdleState(rec.ID, StateCrashedRecovered, fmt.Sprintf("found %s after a crash; %d rows back in the queue, %d uncertain", rec.State, p, u)); err != nil {
			return out, err
		}
		out = append(out, Recovery{RunID: rec.ID, BackToQueue: p, Uncertain: u})
	}
	return out, nil
}

// Close stops every live run without finishing it: browsers close, rows in
// flight are settled like after a stop and each run becomes paused.
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	var all []*runtime
	for _, rt := range s.runs {
		all = append(all, rt)
	}
	s.mu.Unlock()
	for _, rt := range all {
		rt.closing.Store(true)
		rt.cancel()
	}
	for _, rt := range all {
		select {
		case <-rt.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// ---- helpers ----------------------------------------------------------------------

func (s *Service) run(ctx context.Context, id string) (store.RunRecord, error) {
	rec, err := s.st.Runs.Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return rec, ErrRunNotFound
	}
	return rec, err
}

// ActiveRuns counts the runs that have workers: running, paused or taken over.
// The app updater restarts only when it is zero.
func (s *Service) ActiveRuns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, rt := range s.runs {
		select {
		case <-rt.done:
			delete(s.runs, id)
		default:
			n++
		}
	}
	return n
}

func (s *Service) active(id string) *runtime {
	s.mu.Lock()
	defer s.mu.Unlock()
	rt := s.runs[id]
	if rt == nil {
		return nil
	}
	select {
	case <-rt.done:
		delete(s.runs, id)
		return nil
	default:
		return rt
	}
}

// live returns the runtime of a run that has workers, or a state error.
func (s *Service) live(ctx context.Context, id, op string) (*runtime, error) {
	rec, err := s.run(ctx, id)
	if err != nil {
		return nil, err
	}
	rt := s.active(id)
	if rt == nil {
		return nil, &StateError{RunID: id, State: RunState(rec.State), Op: op}
	}
	return rt, nil
}

// setIdleState writes a state for a run that has no workers.
func (s *Service) setIdleState(id string, to RunState, reason string) error {
	if err := s.st.Runs.SetState(context.Background(), id, string(to), reason); err != nil {
		return err
	}
	s.emit(Event{RunID: id, Kind: EventRunState, RowIndex: -1, RunState: to, Message: reason})
	return nil
}

func (s *Service) toRun(ctx context.Context, rec store.RunRecord) (Run, error) {
	counts, err := s.st.Rows.Counts(ctx, rec.ID)
	if err != nil {
		return Run{}, err
	}
	r := Run{
		ID: rec.ID, MacroID: rec.MacroID, MacroName: rec.MacroName, MacroVersion: rec.MacroVersion, MacroHash: rec.MacroHash, SourceID: rec.SourceID,
		State: RunState(rec.State), Reason: rec.Reason, ProfileIDs: rec.ProfileIDs, RowFrom: rec.RowFrom, RowTo: rec.RowTo,
		Counts: map[RowState]int{}, CreatedAt: rec.CreatedAt, StartedAt: rec.StartedAt, FinishedAt: rec.FinishedAt,
	}
	_ = json.Unmarshal(rec.Options, &r.Options)
	for st, n := range counts {
		r.Counts[RowState(st)] = n
		r.Total += n
	}
	return r, nil
}

// emit logs the event in the ledger (append-only) and hands it to the
// subscribers without blocking.
func (s *Service) emit(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	var ri *int
	if e.RowIndex >= 0 {
		i := e.RowIndex
		ri = &i
	}
	_, _ = s.st.Events.Append(context.Background(), e.RunID, ri, string(e.Kind), map[string]any{
		"profile": e.ProfileID, "runState": e.RunState, "rowState": e.RowState, "message": e.Message,
	})
	s.hub.publish(e)
}
