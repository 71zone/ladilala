package engine

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/player"
	"github.com/8bu/ladilala/core/store"
)

var bg = context.Background()

func TestRun50Rows(t *testing.T) {
	e := newEnv(t, 1)
	run := e.create(t, 50, RunOptions{})
	if run.State != StateCreated || run.Total != 50 || run.Counts[RowPending] != 50 {
		t.Fatalf("created run: %+v", run)
	}
	if err := e.svc.StartRun(bg, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.StartRun(bg, run.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("second start: %v", err)
	}
	r := e.waitState(t, run.ID, StateFinished, 20*time.Second)
	if r.Counts[RowDone] != 50 || r.Total != 50 || r.StartedAt == nil || r.FinishedAt == nil {
		t.Fatalf("finished run: %+v", r)
	}
	page, err := e.svc.ListRows(bg, run.ID, RowFilter{Limit: 100})
	if err != nil || page.Total != 50 {
		t.Fatal(page.Total, err)
	}
	for i, row := range page.Rows {
		if row.Index != i || row.State != RowDone || row.CommitState != CommitSubmitted || row.Attempts != 1 || row.StepID != "submit" ||
			row.IdempotencyKey != run.ID+":"+strconv.Itoa(i) || row.ProfileID != e.profiles[0] || row.Data["name"] != "user"+strconv.Itoa(i) {
			t.Fatalf("row %d: %+v", i, row)
		}
		if got := e.f.playsOf(i); len(got) != 1 {
			t.Fatalf("row %d played %d times", i, len(got))
		}
	}
	// the event log has the state changes and one done event per row
	var done, states int
	var after int64
	for {
		ev, err := e.st.Events.List(bg, run.ID, after, 100)
		if err != nil || len(ev) == 0 {
			break
		}
		for _, x := range ev {
			after = x.ID
			switch x.Kind {
			case string(EventRowDone):
				done++
			case string(EventRunState):
				states++
			}
		}
	}
	if done != 50 || states != 2 {
		t.Fatalf("log: %d done events, %d state events", done, states)
	}
	if e.f.launches[e.profiles[0]] != 1 {
		t.Fatalf("browser launched %d times", e.f.launches[e.profiles[0]])
	}
}

func TestStopAfterConsecutiveErrors(t *testing.T) {
	e := newEnv(t, 1)
	failing := true
	var mu sync.Mutex
	e.f.hook = func(ctx context.Context, b *fakeBrowser, idx int, h player.Hooks) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		if failing && idx >= 3 {
			return true, &player.StepError{StepID: "name", Action: macro.ActionFill, Err: &player.LocatorError{Reason: player.ErrNotFound}}
		}
		return false, nil
	}
	run := e.create(t, 20, RunOptions{StopAfterErrors: 3})
	if err := e.svc.StartRun(bg, run.ID); err != nil {
		t.Fatal(err)
	}
	r := e.waitState(t, run.ID, StatePaused, 10*time.Second)
	if r.Counts[RowDone] != 3 || r.Counts[RowFailed] != 3 || r.Counts[RowPending] != 14 {
		t.Fatalf("counts after the stop: %v (%s)", r.Counts, r.Reason)
	}
	row := e.row(t, run.ID, 3)
	if row.State != RowFailed || row.ErrorKind != KindLocator || row.StepID != "name" || row.CommitState != CommitNone {
		t.Fatalf("failed row: %+v", row)
	}
	// fix the problem, retry the failed rows, resume
	mu.Lock()
	failing = false
	mu.Unlock()
	n, err := e.svc.RetryRows(bg, run.ID, RowFilter{States: []RowState{RowFailed}})
	if err != nil || n != 3 {
		t.Fatalf("retry: %d %v", n, err)
	}
	if err := e.svc.ResumeRun(bg, run.ID); err != nil {
		t.Fatal(err)
	}
	r = e.waitState(t, run.ID, StateFinished, 10*time.Second)
	if r.Counts[RowDone] != 20 {
		t.Fatalf("counts: %v", r.Counts)
	}
	if row := e.row(t, run.ID, 3); row.Attempts != 2 || row.State != RowDone {
		t.Fatalf("retried row: %+v", row)
	}
}

func TestPreflightSkipsRowsWithoutTouchingTheBrowser(t *testing.T) {
	e := newEnv(t, 1)
	src := newSource(6)
	delete(src.rows[2], "n") // required variable missing
	run, err := e.svc.CreateRun(bg, CreateRunRequest{MacroID: "signup", Source: src, To: -1, ProfileIDs: e.profiles, Options: RunOptions{StopAfterErrors: 1}})
	if err != nil {
		t.Fatal(err)
	}
	_ = e.svc.StartRun(bg, run.ID)
	r := e.waitState(t, run.ID, StateFinished, 10*time.Second)
	if r.Counts[RowSkipped] != 1 || r.Counts[RowDone] != 5 {
		t.Fatalf("counts: %v", r.Counts)
	}
	var skipped Row
	pg, _ := e.svc.ListRows(bg, run.ID, RowFilter{States: []RowState{RowSkipped}})
	skipped = pg.Rows[0]
	if skipped.Index != 2 || skipped.ErrorKind != KindData {
		t.Fatalf("skipped row: %+v", skipped)
	}
}

// A process that dies after the commit point write leaves the row uncertain.
// The restarted engine never plays it again.
func TestCrashAfterCommitPoint(t *testing.T) {
	e := newEnv(t, 1)
	hang := make(chan struct{})
	reached := make(chan struct{})
	e.f.hook = func(ctx context.Context, b *fakeBrowser, idx int, h player.Hooks) (bool, error) {
		if idx != 7 {
			return false, nil
		}
		for i := range testMacroSteps() { // the steps before the commit point
			if err := h.BeforeStep(ctx, i, &testMacroSteps()[i]); err != nil {
				return true, err
			}
		}
		if err := h.BeforeCommit(ctx, &testMacroSteps()[2]); err != nil {
			return true, err
		}
		close(reached)
		<-hang // the click is "in flight": the process dies here
		return true, nil
	}
	run := e.create(t, 20, RunOptions{})
	if err := e.svc.StartRun(bg, run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("row 7 never reached its commit point")
	}
	t.Cleanup(func() { // let the abandoned worker go: stop it first so it claims nothing
		ctx, cancel := context.WithTimeout(bg, 10*time.Second)
		defer cancel()
		go e.svc.Close(ctx)
		time.Sleep(50 * time.Millisecond)
		close(hang)
	})
	// "restart": a second store and engine on the same database file
	st2, err := store.Open(bg, e.dir, &store.MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	f2 := newFake()
	svc2 := NewService(st2, f2)
	rec, err := svc2.Recover(bg)
	if err != nil || len(rec) != 1 || rec[0].Uncertain != 1 || rec[0].RunID != run.ID {
		t.Fatalf("recover: %+v %v", rec, err)
	}
	r, _ := svc2.GetRun(bg, run.ID)
	if r.State != StateCrashedRecovered || r.Counts[RowUncertain] != 1 || r.Counts[RowDone] != 7 || r.Counts[RowPending] != 12 {
		t.Fatalf("recovered run: %s %v", r.State, r.Counts)
	}
	pg, _ := svc2.ListRows(bg, run.ID, RowFilter{States: []RowState{RowUncertain}})
	if len(pg.Rows) != 1 || pg.Rows[0].Index != 7 || pg.Rows[0].CommitState != CommitSubmitting || pg.Rows[0].ErrorKind != KindCrash || pg.Rows[0].StepID != "submit" {
		t.Fatalf("uncertain row: %+v", pg.Rows)
	}
	if err := svc2.ResumeRun(bg, run.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		r, _ = svc2.GetRun(bg, run.ID)
		if r.State == StateFinished {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not finished: %s %v", r.State, r.Counts)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r.Counts[RowDone] != 19 || r.Counts[RowUncertain] != 1 {
		t.Fatalf("final counts: %v", r.Counts)
	}
	if got := f2.playsOf(7); len(got) != 0 {
		t.Fatalf("the uncertain row was played again: %v", got)
	}
	if n, err := svc2.RetryRows(bg, run.ID, RowFilter{}); err != nil || n != 0 {
		t.Fatalf("retry touched the uncertain row: %d %v", n, err)
	}
	// the user decides
	if err := svc2.MarkUncertainRow(bg, run.ID, 7, ResolveDone); err != nil {
		t.Fatal(err)
	}
	if err := svc2.MarkUncertainRow(bg, run.ID, 7, ResolveFailed); !errors.Is(err, ErrNotUncertain) {
		t.Fatalf("second resolution: %v", err)
	}
	if err := svc2.MarkUncertainRow(bg, run.ID, 8, ResolveDone); !errors.Is(err, ErrNotUncertain) {
		t.Fatalf("resolving a done row: %v", err)
	}
	if r, _ = svc2.GetRun(bg, run.ID); r.Counts[RowDone] != 20 {
		t.Fatalf("counts: %v", r.Counts)
	}
}

func testMacroSteps() []macro.Step { return testMacroCached }

var testMacroCached = testMacro().Steps

func TestPauseAndResumeNoDuplicates(t *testing.T) {
	e := newEnv(t, 1)
	e.f.stepDelay = 4 * time.Millisecond
	run := e.create(t, 60, RunOptions{})
	_ = e.svc.StartRun(bg, run.ID)
	e.waitCount(t, run.ID, RowDone, 10, 10*time.Second)
	t0 := time.Now()
	if err := e.svc.PauseRun(bg, run.ID); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t0); d > 15*time.Second {
		t.Fatalf("pause took %s", d)
	}
	if err := e.svc.PauseRun(bg, run.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("pause twice: %v", err)
	}
	r, _ := e.svc.GetRun(bg, run.ID)
	if r.State != StatePaused {
		t.Fatal(r.State)
	}
	done := r.Counts[RowDone]
	time.Sleep(150 * time.Millisecond)
	r2, _ := e.svc.GetRun(bg, run.ID)
	if r2.Counts[RowDone] != done || r2.Counts[RowRunning] > 1 {
		t.Fatalf("rows moved while paused: %v -> %v", r.Counts, r2.Counts)
	}
	t0 = time.Now()
	if err := e.svc.ResumeRun(bg, run.ID); err != nil {
		t.Fatal(err)
	}
	r = e.waitState(t, run.ID, StateFinished, 20*time.Second)
	t.Logf("resume to finish: %s", time.Since(t0))
	if r.Counts[RowDone] != 60 {
		t.Fatalf("counts: %v", r.Counts)
	}
	for i := range 60 {
		if got := e.f.playsOf(i); len(got) != 1 {
			t.Fatalf("row %d played %d times", i, len(got))
		}
	}
}

func TestTakeoverRechecksGuards(t *testing.T) {
	e := newEnv(t, 1)
	e.f.stepDelay = 5 * time.Millisecond
	run := e.create(t, 30, RunOptions{})
	_ = e.svc.StartRun(bg, run.ID)
	e.waitCount(t, run.ID, RowDone, 3, 10*time.Second)
	if err := e.svc.Takeover(bg, run.ID); err != nil {
		t.Fatal(err)
	}
	e.waitState(t, run.ID, StateTakeover, time.Second)
	blocked := true
	e.f.recheck = func() error {
		if blocked {
			return &player.GuardError{Guard: macro.Guard{Kind: macro.GuardURLMatches, Action: macro.GuardNeedsLogin}, Why: "login page"}
		}
		return nil
	}
	err := e.svc.ReleaseTakeover(bg, run.ID)
	var ge *GuardFiredError
	if !errors.As(err, &ge) || !errors.Is(err, player.ErrGuard) {
		t.Fatalf("release with a failing guard: %v", err)
	}
	if r, _ := e.svc.GetRun(bg, run.ID); r.State != StateTakeover {
		t.Fatalf("state = %s", r.State)
	}
	blocked = false
	if err := e.svc.ReleaseTakeover(bg, run.ID); err != nil {
		t.Fatal(err)
	}
	r := e.waitState(t, run.ID, StateFinished, 20*time.Second)
	if r.Counts[RowDone] != 30 {
		t.Fatalf("counts: %v", r.Counts)
	}
}

func TestManualStepWaitsForTheUser(t *testing.T) {
	e := newEnv(t, 1)
	e.f.hook = func(ctx context.Context, b *fakeBrowser, idx int, h player.Hooks) (bool, error) {
		if idx != 1 {
			return false, nil
		}
		return true, h.Manual(ctx, &macro.Step{ID: "captcha", Action: macro.ActionManual}, "solve the captcha")
	}
	run := e.create(t, 3, RunOptions{})
	_ = e.svc.StartRun(bg, run.ID)
	r := e.waitState(t, run.ID, StateTakeover, 10*time.Second)
	if r.Reason == "" || r.Counts[RowDone] != 1 {
		t.Fatalf("manual: %+v", r)
	}
	if err := e.svc.ReleaseTakeover(bg, run.ID); err != nil {
		t.Fatal(err)
	}
	e.waitState(t, run.ID, StateFinished, 10*time.Second)
}

func TestConcurrencyKeepsEachRowOnOneProfile(t *testing.T) {
	e := newEnv(t, 3)
	e.f.stepDelay = 3 * time.Millisecond
	run := e.create(t, 45, RunOptions{Concurrency: 3})
	_ = e.svc.StartRun(bg, run.ID)
	r := e.waitState(t, run.ID, StateFinished, 20*time.Second)
	if r.Counts[RowDone] != 45 {
		t.Fatalf("counts: %v", r.Counts)
	}
	used := map[string]int{}
	pg, _ := e.svc.ListRows(bg, run.ID, RowFilter{Limit: 100})
	for _, row := range pg.Rows {
		plays := e.f.playsOf(row.Index)
		if len(plays) != 1 || plays[0] != row.ProfileID {
			t.Fatalf("row %d: played on %v, ledger says %s", row.Index, plays, row.ProfileID)
		}
		used[row.ProfileID]++
	}
	if len(used) != 3 {
		t.Fatalf("profiles used: %v", used)
	}
	if e.f.maxLive != 3 {
		t.Fatalf("max rows at the same time = %d, want 3", e.f.maxLive)
	}
	if _, err := e.svc.CreateRun(bg, CreateRunRequest{MacroID: "signup", Source: newSource(3), To: -1, ProfileIDs: e.profiles[:2], Options: RunOptions{Concurrency: 3}}); !errors.Is(err, ErrNotEnoughProfiles) {
		t.Fatalf("concurrency above the pool: %v", err)
	}
}

func TestEventsNeverBlockTheRun(t *testing.T) {
	e := newEnv(t, 1)
	run := e.create(t, 300, RunOptions{})
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	idle, err := e.svc.Subscribe(ctx, run.ID) // never read during the run
	if err != nil {
		t.Fatal(err)
	}
	_ = e.svc.StartRun(bg, run.ID)
	e.waitState(t, run.ID, StateFinished, 20*time.Second)
	n, dropped := 0, 0
	for len(idle) > 0 {
		ev := <-idle
		n++
		dropped += ev.Dropped
	}
	if n != subBuffer {
		t.Fatalf("buffered %d events, want %d", n, subBuffer)
	}
	if dropped == 0 {
		t.Fatal("no event reported dropped events")
	}
	cancel()
	for range idle { // closes after cancel
	}
}

func TestStopRunSettlesRowsInFlight(t *testing.T) {
	e := newEnv(t, 1)
	reached := make(chan struct{})
	e.f.hook = func(ctx context.Context, b *fakeBrowser, idx int, h player.Hooks) (bool, error) {
		if idx != 4 {
			return false, nil
		}
		if err := h.BeforeCommit(ctx, &testMacroSteps()[2]); err != nil {
			return true, err
		}
		close(reached)
		<-ctx.Done()
		return true, ctx.Err()
	}
	run := e.create(t, 10, RunOptions{})
	_ = e.svc.StartRun(bg, run.ID)
	<-reached
	if err := e.svc.StopRun(bg, run.ID); err != nil {
		t.Fatal(err)
	}
	r := e.waitState(t, run.ID, StateFinished, 5*time.Second)
	if r.Counts[RowDone] != 4 || r.Counts[RowUncertain] != 1 || r.Counts[RowPending] != 5 {
		t.Fatalf("counts: %v", r.Counts)
	}
	if row := e.row(t, run.ID, 4); row.ErrorKind != KindStopped {
		t.Fatalf("row 4: %+v", row)
	}
}

func TestBrowserDiesAndRestarts(t *testing.T) {
	e := newEnv(t, 1)
	var once sync.Once
	e.f.hook = func(ctx context.Context, b *fakeBrowser, idx int, h player.Hooks) (bool, error) {
		if idx != 5 {
			return false, nil
		}
		var died bool
		once.Do(func() { died = true })
		if died {
			b.kill()
			return true, ErrBrowserDied
		}
		return false, nil
	}
	run := e.create(t, 10, RunOptions{})
	_ = e.svc.StartRun(bg, run.ID)
	r := e.waitState(t, run.ID, StateFinished, 10*time.Second)
	if r.Counts[RowDone] != 10 {
		t.Fatalf("counts: %v", r.Counts)
	}
	if got := e.f.playsOf(5); len(got) != 2 {
		t.Fatalf("row 5 played %d times, want 2 (died, then again)", len(got))
	}
	if e.f.launches[e.profiles[0]] != 2 {
		t.Fatalf("launches: %d", e.f.launches[e.profiles[0]])
	}
}

func TestBrowserDiesAfterCommitIsUncertain(t *testing.T) {
	e := newEnv(t, 1)
	e.f.hook = func(ctx context.Context, b *fakeBrowser, idx int, h player.Hooks) (bool, error) {
		if idx != 2 {
			return false, nil
		}
		if err := h.BeforeCommit(ctx, &testMacroSteps()[2]); err != nil {
			return true, err
		}
		b.kill()
		return true, ErrBrowserDied
	}
	run := e.create(t, 6, RunOptions{})
	_ = e.svc.StartRun(bg, run.ID)
	r := e.waitState(t, run.ID, StateFinished, 10*time.Second)
	if r.Counts[RowDone] != 5 || r.Counts[RowUncertain] != 1 {
		t.Fatalf("counts: %v", r.Counts)
	}
	if got := e.f.playsOf(2); len(got) != 1 {
		t.Fatalf("uncertain row played %d times", len(got))
	}
}

func TestLaunchFailureUsesSpareProfile(t *testing.T) {
	e := newEnv(t, 2)
	e.f.launchErr = func(id string, n int) error {
		if id == e.profiles[0] {
			return errors.New("chrome is not installed for this profile")
		}
		return nil
	}
	run := e.create(t, 5, RunOptions{MaxRestarts: 1})
	_ = e.svc.StartRun(bg, run.ID)
	r := e.waitState(t, run.ID, StateFinished, 20*time.Second)
	if r.Counts[RowDone] != 5 {
		t.Fatalf("counts: %v", r.Counts)
	}
	// without a spare the run fails and keeps its rows pending
	e2 := newEnv(t, 1)
	e2.f.launchErr = func(string, int) error { return errors.New("no browser") }
	run2 := e2.create(t, 5, RunOptions{MaxRestarts: 1})
	_ = e2.svc.StartRun(bg, run2.ID)
	r2 := e2.waitState(t, run2.ID, StateFailed, 20*time.Second)
	if r2.Counts[RowPending] != 5 || r2.Reason == "" {
		t.Fatalf("failed run: %v %s", r2.Counts, r2.Reason)
	}
}

func TestGuardPausesTheRunAndKeepsTheRow(t *testing.T) {
	e := newEnv(t, 1)
	var once sync.Once
	e.f.hook = func(ctx context.Context, b *fakeBrowser, idx int, h player.Hooks) (bool, error) {
		if idx != 2 {
			return false, nil
		}
		var fire bool
		once.Do(func() { fire = true })
		if fire {
			return true, &player.GuardError{Guard: macro.Guard{Kind: macro.GuardURLMatches, Action: macro.GuardNeedsLogin}, Why: "login page"}
		}
		return false, nil
	}
	run := e.create(t, 6, RunOptions{})
	_ = e.svc.StartRun(bg, run.ID)
	r := e.waitState(t, run.ID, StatePaused, 10*time.Second)
	if r.Counts[RowDone] != 2 || r.Counts[RowPending] != 4 || r.Counts[RowFailed] != 0 {
		t.Fatalf("counts: %v (%s)", r.Counts, r.Reason)
	}
	if err := e.svc.ResumeRun(bg, run.ID); err != nil {
		t.Fatal(err)
	}
	r = e.waitState(t, run.ID, StateFinished, 10*time.Second)
	if r.Counts[RowDone] != 6 {
		t.Fatalf("counts: %v", r.Counts)
	}
}

func TestDryRunAndTestOne(t *testing.T) {
	e := newEnv(t, 1)
	seen := make(chan bool, 100)
	e.f.hook = func(ctx context.Context, b *fakeBrowser, idx int, h player.Hooks) (bool, error) {
		seen <- h.DryRun
		return false, nil
	}
	run := e.create(t, 5, RunOptions{DryRun: true})
	_ = e.svc.StartRun(bg, run.ID)
	e.waitState(t, run.ID, StateFinished, 10*time.Second)
	pg, _ := e.svc.ListRows(bg, run.ID, RowFilter{})
	for _, row := range pg.Rows {
		if row.CommitState != CommitNone || row.State != RowDone {
			t.Fatalf("dry run row: %+v", row)
		}
	}
	if !<-seen {
		t.Fatal("DryRun not passed to the player")
	}
	one := e.create(t, 5, RunOptions{TestOne: true})
	if one.Total != 1 {
		t.Fatalf("test run has %d rows", one.Total)
	}
	_ = e.svc.StartRun(bg, one.ID)
	r := e.waitState(t, one.ID, StateFinished, 10*time.Second)
	if r.Counts[RowDone] != 1 {
		t.Fatal(r.Counts)
	}
}

func TestStateErrorsAndLists(t *testing.T) {
	e := newEnv(t, 1)
	if _, err := e.svc.GetRun(bg, "nope"); !errors.Is(err, ErrRunNotFound) {
		t.Fatal(err)
	}
	run := e.create(t, 3, RunOptions{})
	for name, f := range map[string]func() error{
		"pause":   func() error { return e.svc.PauseRun(bg, run.ID) },
		"resume":  func() error { return e.svc.ResumeRun(bg, run.ID) },
		"takeovr": func() error { return e.svc.Takeover(bg, run.ID) },
		"release": func() error { return e.svc.ReleaseTakeover(bg, run.ID) },
	} {
		var se *StateError
		if err := f(); !errors.As(err, &se) || se.State != StateCreated {
			t.Fatalf("%s on a created run: %v", name, err)
		}
	}
	if err := e.svc.StopRun(bg, run.ID); err != nil { // a created run can be stopped
		t.Fatal(err)
	}
	runs, err := e.svc.ListRuns(bg, RunFilter{States: []RunState{StateFinished}})
	if err != nil || len(runs) != 1 || runs[0].Counts[RowPending] != 3 {
		t.Fatalf("list: %+v %v", runs, err)
	}
	if _, err := e.svc.CreateRun(bg, CreateRunRequest{MacroID: "signup", Source: newSource(0), To: -1, ProfileIDs: e.profiles}); !errors.Is(err, ErrNoRows) {
		t.Fatalf("empty source: %v", err)
	}
	if _, err := e.svc.CreateRun(bg, CreateRunRequest{MacroID: "signup", Source: newSource(1), To: -1}); !errors.Is(err, ErrNoProfiles) {
		t.Fatalf("no profiles: %v", err)
	}
	if _, err := e.svc.CreateRun(bg, CreateRunRequest{MacroID: "zzz", Source: newSource(1), To: -1, ProfileIDs: e.profiles}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown macro: %v", err)
	}
	if _, err := e.svc.Subscribe(bg, "nope"); !errors.Is(err, ErrRunNotFound) {
		t.Fatal(err)
	}
}

func TestRowRangeKeepsSourceIndexes(t *testing.T) {
	e := newEnv(t, 1)
	run, err := e.svc.CreateRun(bg, CreateRunRequest{MacroID: "signup", Source: newSource(20), From: 5, To: 9, ProfileIDs: e.profiles})
	if err != nil {
		t.Fatal(err)
	}
	pg, _ := e.svc.ListRows(bg, run.ID, RowFilter{})
	if pg.Total != 4 || pg.Rows[0].Index != 5 || pg.Rows[3].Index != 8 {
		t.Fatalf("rows: %+v", pg)
	}
}
