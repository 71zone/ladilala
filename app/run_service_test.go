package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/engine"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/player"
	"github.com/8bu/ladilala/core/profile"
	"github.com/8bu/ladilala/core/store"
)

var rbg = context.Background()

// runLauncher gives browsers that play the steps of a macro with small sleeps.
type runLauncher struct {
	delay time.Duration
	hook  func(ctx context.Context, b *runBrowser, n int, h player.Hooks) (handled bool, err error)

	mu    sync.Mutex
	plays map[int]int
}

func (f *runLauncher) Launch(_ context.Context, p profile.Profile) (engine.Browser, error) {
	return &runBrowser{f: f, profile: p.ID, done: make(chan struct{})}, nil
}

type runBrowser struct {
	f       *runLauncher
	profile string
	done    chan struct{}
	once    sync.Once
}

func (b *runBrowser) Done() <-chan struct{}                       { return b.done }
func (b *runBrowser) Close() error                                { b.kill(); return nil }
func (b *runBrowser) kill()                                       { b.once.Do(func() { close(b.done) }) }
func (b *runBrowser) Recheck(context.Context, *macro.Macro) error { return nil }

func (b *runBrowser) PlayRow(ctx context.Context, m *macro.Macro, row map[string]string, h player.Hooks) error {
	n, _ := strconv.Atoi(row["n"])
	b.f.mu.Lock()
	b.f.plays[n]++
	b.f.mu.Unlock()
	if b.f.hook != nil {
		if handled, err := b.f.hook(ctx, b, n, h); handled {
			return err
		}
	}
	for i := range m.Steps {
		s := &m.Steps[i]
		if h.BeforeStep != nil {
			if err := h.BeforeStep(ctx, i, s); err != nil {
				return err
			}
		}
		if s.CommitPoint {
			if h.DryRun {
				continue
			}
			if h.BeforeCommit != nil {
				if err := h.BeforeCommit(ctx, s); err != nil {
					return err
				}
			}
			if h.AfterCommit != nil {
				h.AfterCommit(ctx, s, nil)
			}
			continue
		}
		select {
		case <-time.After(b.f.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func runMacro() *macro.Macro {
	css := func(sel string) *macro.Target {
		return &macro.Target{Locators: []macro.Locator{{Kind: macro.LocCSS, Value: sel}}}
	}
	return &macro.Macro{
		SchemaVersion: 1, ID: "signup", Name: "Sign up", AllowedOrigins: []string{"https://example.com"},
		Variables: []macro.VariableDecl{{Name: "n", Type: macro.VarString, Required: true}, {Name: "name", Type: macro.VarString}},
		Steps: []macro.Step{
			{ID: "open", Action: macro.ActionNavigate, Value: &macro.Value{Expr: "https://example.com/form"}},
			{ID: "name", Action: macro.ActionFill, Target: css("#name"), Value: &macro.Value{Expr: "{{name}}"}},
			{ID: "submit", Action: macro.ActionClick, Target: css("button"), CommitPoint: true},
		},
	}
}

// runCaptured collects the events the service sends to the UI.
type runCaptured struct {
	mu sync.Mutex
	ev []runCapturedEvent
}
type runCapturedEvent struct {
	name string
	data any
	at   time.Time
}

func (c *runCaptured) emit(name string, data any) {
	c.mu.Lock()
	c.ev = append(c.ev, runCapturedEvent{name, data, time.Now()})
	c.mu.Unlock()
}
func (c *runCaptured) named(name string) []runCapturedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []runCapturedEvent
	for _, e := range c.ev {
		if e.name == name {
			out = append(out, e)
		}
	}
	return out
}

type runEnv struct {
	dir      string
	st       *store.Store
	l        *runLauncher
	svc      *RunService
	cap      *runCaptured
	profiles []string
	sourceID string
}

// openRunEnv opens a store in dir and a RunService over a fresh engine.
func openRunEnv(t *testing.T, dir string, l *runLauncher) *runEnv {
	t.Helper()
	st, err := store.Open(rbg, dir, &store.MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	b := &Backend{DataDir: dir, Store: st, Engine: engine.NewService(st, l)}
	c := &runCaptured{}
	b.App = nil
	s := &RunService{b: b, emit: c.emit, watchers: map[string]*watcher{}}
	s.recover(rbg)
	t.Cleanup(func() { s.ServiceShutdown() })
	return &runEnv{dir: dir, st: st, l: l, svc: s, cap: c}
}

// seed saves the macro, profiles and a CSV source of n rows (columns n, name).
func (e *runEnv) seed(t *testing.T, nProfiles, rows int) {
	t.Helper()
	if _, err := e.st.Macros.Put(rbg, runMacro()); err != nil {
		t.Fatal(err)
	}
	for i := range nProfiles {
		fp := profile.NewFingerprint(uint64(i+1), profile.Options{Platform: profile.PlatformWindows, BrowserVersion: "150.0.7871.186"})
		rec, err := e.st.Profiles.Put(rbg, store.ProfileRecord{Profile: profile.Profile{Name: fmt.Sprintf("p%d", i), Fingerprint: fp, DataDir: filepath.Join(e.dir, fmt.Sprintf("pd%d", i))}})
		if err != nil {
			t.Fatal(err)
		}
		e.profiles = append(e.profiles, rec.ID)
	}
	path := filepath.Join(e.dir, "rows.csv")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"n", "name"})
	for i := range rows {
		_ = w.Write([]string{strconv.Itoa(i), fmt.Sprintf("user%d", i)})
	}
	w.Flush()
	f.Close()
	cfg, _ := json.Marshal(dataSourceConfig{Path: path})
	rec, err := e.st.Sources.Put(rbg, store.SourceRecord{Name: "rows", Kind: "csv", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	e.sourceID = rec.ID
}

func (e *runEnv) create(t *testing.T, opts RunOptionsDTO) RunDTO {
	t.Helper()
	r, err := e.svc.CreateRun(rbg, CreateRunRequest{MacroID: "signup", SourceID: e.sourceID, To: -1, ProfileIDs: e.profiles, Options: opts})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (e *runEnv) waitState(t *testing.T, id, want string) RunDTO {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		r, err := e.svc.GetRun(rbg, id)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == want {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("run is %s (%s), want %s; counts %v", r.State, r.Reason, want, r.Counts)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *runEnv) waitCount(t *testing.T, id, state string, atLeast int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		r, _ := e.svc.GetRun(rbg, id)
		if r.Counts[state] >= atLeast {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d rows %s of %d: %v", r.Counts[state], state, atLeast, r.Counts)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func runErrCode(err error) string {
	var a *APIError
	if errors.As(err, &a) {
		return a.Code
	}
	return ""
}

func TestRunServiceLifecycleAndEvents(t *testing.T) {
	e := openRunEnv(t, t.TempDir(), &runLauncher{delay: 3 * time.Millisecond, plays: map[int]int{}})
	e.seed(t, 2, 60)
	run := e.create(t, RunOptionsDTO{Concurrency: 2})
	if run.State != "created" || run.Total != 60 || run.Counts["pending"] != 60 || run.SourceID != e.sourceID {
		t.Fatalf("created: %+v", run)
	}
	t0 := time.Now()
	if err := e.svc.StartRun(rbg, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.StartRun(rbg, run.ID); runErrCode(err) != "run.state" {
		t.Fatalf("second start: %v", err)
	}
	r := e.waitState(t, run.ID, "finished")
	took := time.Since(t0)
	if r.Counts["done"] != 60 {
		t.Fatalf("finished: %v", r.Counts)
	}
	time.Sleep(2 * progressEvery) // let the last flush go out

	// paging and filters
	p, err := e.svc.ListRows(rbg, run.ID, RowFilter{Offset: 50, Limit: 25})
	if err != nil || p.Total != 60 || len(p.Rows) != 10 || p.Rows[0].Index != 50 || p.Rows[0].Data["name"] != "user50" {
		t.Fatalf("page: %d rows, total %d, %v", len(p.Rows), p.Total, err)
	}
	if p, _ = e.svc.ListRows(rbg, run.ID, RowFilter{States: []string{"failed"}}); p.Total != 0 || p.Rows == nil {
		t.Fatalf("failed filter: %+v", p)
	}
	if p, _ = e.svc.ListRows(rbg, run.ID, RowFilter{Indexes: []int{3, 4}, States: []string{"done"}}); p.Total != 2 {
		t.Fatalf("index filter: %+v", p)
	}
	runs, err := e.svc.ListRuns(rbg, RunFilter{States: []string{"finished"}})
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("runs: %+v %v", runs, err)
	}

	// events: state changes arrive, progress is coalesced and ends at the truth
	states := e.cap.named(evRunState)
	if len(states) < 2 || states[len(states)-1].data.(StateEvent).State != "finished" {
		t.Fatalf("state events: %+v", states)
	}
	prog := e.cap.named(evRunProgress)
	if len(prog) == 0 {
		t.Fatal("no progress events")
	}
	if max := int(took/progressEvery) + 3; len(prog) > max {
		t.Fatalf("%d progress events in %s, want at most %d (5 per second)", len(prog), took, max)
	}
	for i := 1; i < len(prog)-1; i++ { // the last one may come early: it flushes when the run ends
		if gap := prog[i].at.Sub(prog[i-1].at); gap < progressEvery-40*time.Millisecond {
			t.Fatalf("progress events %d and %d are %s apart", i-1, i, gap)
		}
	}
	last := prog[len(prog)-1].data.(ProgressEvent)
	if last.RunID != run.ID || last.Counts["done"] != 60 || last.Total != 60 {
		t.Fatalf("last progress: %+v", last)
	}
	lines := 0
	for _, ev := range e.cap.named(evRunLog) {
		lines += len(ev.data.(LogEvent).Lines)
	}
	if lines < 60 {
		t.Fatalf("%d log lines for 60 rows", lines)
	}
	if len(e.cap.named(evRunSession)) == 0 {
		t.Fatal("no session events")
	}
}

func TestRunServicePauseResumeStop(t *testing.T) {
	e := openRunEnv(t, t.TempDir(), &runLauncher{delay: 15 * time.Millisecond, plays: map[int]int{}})
	e.seed(t, 1, 40)
	run := e.create(t, RunOptionsDTO{})
	if err := e.svc.PauseRun(rbg, run.ID); runErrCode(err) != "run.state" {
		t.Fatalf("pause before start: %v", err)
	}
	if err := e.svc.StartRun(rbg, run.ID); err != nil {
		t.Fatal(err)
	}
	e.waitCount(t, run.ID, "done", 2)
	if err := e.svc.PauseRun(rbg, run.ID); err != nil {
		t.Fatal(err)
	}
	r := e.waitState(t, run.ID, "paused")
	frozen := r.Counts["done"]
	time.Sleep(150 * time.Millisecond)
	if r2, _ := e.svc.GetRun(rbg, run.ID); r2.Counts["done"] != frozen {
		t.Fatalf("rows moved while paused: %d to %d", frozen, r2.Counts["done"])
	}
	if err := e.svc.ResumeRun(rbg, run.ID); err != nil {
		t.Fatal(err)
	}
	e.waitCount(t, run.ID, "done", frozen+3)
	if err := e.svc.StopRun(rbg, run.ID); err != nil {
		t.Fatal(err)
	}
	r = e.waitState(t, run.ID, "finished")
	if r.Counts["done"] < frozen+3 || r.Counts["done"]+r.Counts["pending"] != 40 {
		t.Fatalf("after stop: %v", r.Counts)
	}
	if err := e.svc.ResumeRun(rbg, "nope"); runErrCode(err) != "run.notFound" {
		t.Fatalf("unknown run: %v", err)
	}
}

func TestRunServiceRetryAndMarkUncertain(t *testing.T) {
	l := &runLauncher{delay: time.Millisecond, plays: map[int]int{}}
	var failing = true
	var mu sync.Mutex
	l.hook = func(ctx context.Context, b *runBrowser, n int, h player.Hooks) (bool, error) {
		mu.Lock()
		fail := failing
		mu.Unlock()
		switch {
		case n == 2 && fail:
			return true, &player.StepError{StepID: "name", Action: macro.ActionFill, Err: &player.LocatorError{Reason: player.ErrNotFound}}
		case n == 4 || n == 5:
			step := runMacro().Steps[2]
			if err := h.BeforeCommit(ctx, &step); err != nil {
				return true, err
			}
			b.kill()
			return true, engine.ErrBrowserDied
		}
		return false, nil
	}
	e := openRunEnv(t, t.TempDir(), l)
	e.seed(t, 3, 8)
	run := e.create(t, RunOptionsDTO{StopAfterErrors: 50})
	if err := e.svc.StartRun(rbg, run.ID); err != nil {
		t.Fatal(err)
	}
	r := e.waitState(t, run.ID, "finished")
	if r.Counts["failed"] != 1 || r.Counts["uncertain"] != 2 || r.Counts["done"] != 5 {
		t.Fatalf("counts: %v", r.Counts)
	}
	if n := len(e.cap.named(evRunUncertain)); n != 2 {
		t.Fatalf("%d uncertain events, want 2", n)
	}
	// uncertain rows are not retried
	mu.Lock()
	failing = false
	mu.Unlock()
	n, err := e.svc.RetryRows(rbg, run.ID, RowFilter{})
	if err != nil || n != 1 {
		t.Fatalf("retry: %d %v", n, err)
	}
	if err := e.svc.MarkUncertainRow(rbg, run.ID, 2, "done"); runErrCode(err) != "run.notUncertain" {
		t.Fatalf("mark a failed row: %v", err)
	}
	if err := e.svc.MarkUncertainRow(rbg, run.ID, 4, "maybe"); runErrCode(err) != "invalid" {
		t.Fatalf("bad resolution: %v", err)
	}
	if err := e.svc.MarkUncertainRow(rbg, run.ID, 4, "done"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.MarkUncertainRow(rbg, run.ID, 5, "failed"); err != nil {
		t.Fatal(err)
	}
	r = e.waitState(t, run.ID, "paused")
	if r.Counts["uncertain"] != 0 || r.Counts["done"] != 6 {
		t.Fatalf("after resolving: %v", r.Counts)
	}
	l.hook = nil
	if n, err = e.svc.RetryRows(rbg, run.ID, RowFilter{States: []string{"failed"}}); err != nil || n != 1 {
		t.Fatalf("retry the row marked failed: %d %v", n, err)
	}
	if err := e.svc.ResumeRun(rbg, run.ID); err != nil {
		t.Fatal(err)
	}
	r = e.waitState(t, run.ID, "finished")
	if r.Counts["done"] != 8 {
		t.Fatalf("final: %v", r.Counts)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.plays[4] != 1 {
		t.Fatalf("row 4 (marked submitted) played %d times", l.plays[4])
	}
}

func TestRunServiceRestartRecoversCrashedRun(t *testing.T) {
	dir := t.TempDir()
	hang := make(chan struct{})
	reached := make(chan struct{})
	l := &runLauncher{delay: time.Millisecond, plays: map[int]int{}}
	l.hook = func(ctx context.Context, b *runBrowser, n int, h player.Hooks) (bool, error) {
		if n != 7 {
			return false, nil
		}
		step := runMacro().Steps[2]
		if err := h.BeforeCommit(ctx, &step); err != nil {
			return true, err
		}
		close(reached)
		<-hang
		return true, nil
	}
	e := openRunEnv(t, dir, l)
	e.seed(t, 1, 20)
	run := e.create(t, RunOptionsDTO{})
	if err := e.svc.StartRun(rbg, run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("row 7 never reached its commit point")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(rbg, 10*time.Second)
		defer cancel()
		go e.svc.b.Engine.Close(ctx)
		time.Sleep(50 * time.Millisecond)
		close(hang)
	})

	// "restart": a new service on the same database
	e2 := openRunEnv(t, dir, &runLauncher{delay: time.Millisecond, plays: map[int]int{}})
	rec, err := e2.svc.Recovered(rbg)
	if err != nil || len(rec) != 1 || rec[0].RunID != run.ID || rec[0].Uncertain != 1 {
		t.Fatalf("recovered: %+v %v", rec, err)
	}
	if ev := e2.cap.named(evRunRecovered); len(ev) != 1 {
		t.Fatalf("%d run.recovered events", len(ev))
	}
	r, _ := e2.svc.GetRun(rbg, run.ID)
	if r.State != "crashed_recovered" || r.Counts["uncertain"] != 1 || r.Counts["done"] != 7 {
		t.Fatalf("recovered run: %s %v", r.State, r.Counts)
	}
	p, _ := e2.svc.ListRows(rbg, run.ID, RowFilter{States: []string{"uncertain"}})
	if len(p.Rows) != 1 || p.Rows[0].Index != 7 || p.Rows[0].CommitState != "submitting" {
		t.Fatalf("uncertain row: %+v", p.Rows)
	}
	if err := e2.svc.MarkUncertainRow(rbg, run.ID, 7, "done"); err != nil {
		t.Fatal(err)
	}
	if err := e2.svc.ResumeRun(rbg, run.ID); err != nil {
		t.Fatal(err)
	}
	r = e2.waitState(t, run.ID, "finished")
	if r.Counts["done"] != 20 {
		t.Fatalf("after resume: %v", r.Counts)
	}
	if err := e2.svc.DismissRecovered(rbg); err != nil {
		t.Fatal(err)
	}
	if rec, _ = e2.svc.Recovered(rbg); len(rec) != 0 {
		t.Fatalf("dismissed list: %+v", rec)
	}
}

func TestRunServiceRequestChecksAndExport(t *testing.T) {
	e := openRunEnv(t, t.TempDir(), &runLauncher{delay: time.Millisecond, plays: map[int]int{}})
	e.seed(t, 1, 5)
	for name, req := range map[string]CreateRunRequest{
		"no macro":      {SourceID: e.sourceID, To: -1, ProfileIDs: e.profiles},
		"bad range":     {MacroID: "signup", SourceID: e.sourceID, From: 4, To: 2, ProfileIDs: e.profiles},
		"negative from": {MacroID: "signup", SourceID: e.sourceID, From: -1, To: -1, ProfileIDs: e.profiles},
	} {
		if _, err := e.svc.CreateRun(rbg, req); runErrCode(err) != "invalid" {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for name, c := range map[string]struct {
		req  CreateRunRequest
		code string
	}{
		"unknown macro":   {CreateRunRequest{MacroID: "zzz", SourceID: e.sourceID, To: -1, ProfileIDs: e.profiles}, "notFound"},
		"unknown source":  {CreateRunRequest{MacroID: "signup", SourceID: "zzz", To: -1, ProfileIDs: e.profiles}, "notFound"},
		"no profiles":     {CreateRunRequest{MacroID: "signup", SourceID: e.sourceID, To: -1}, "run.noProfiles"},
		"too few":         {CreateRunRequest{MacroID: "signup", SourceID: e.sourceID, To: -1, ProfileIDs: e.profiles, Options: RunOptionsDTO{Concurrency: 3}}, "run.tooFewProfiles"},
		"range past data": {CreateRunRequest{MacroID: "signup", SourceID: e.sourceID, From: 50, To: 60, ProfileIDs: e.profiles}, "run.noRows"},
	} {
		if _, err := e.svc.CreateRun(rbg, c.req); runErrCode(err) != c.code {
			t.Fatalf("%s: got %v, want %s", name, err, c.code)
		}
	}
	// row range and test one row
	r, err := e.svc.CreateRun(rbg, CreateRunRequest{MacroID: "signup", SourceID: e.sourceID, From: 1, To: 4, ProfileIDs: e.profiles, Options: RunOptionsDTO{TestOne: true}})
	if err != nil || r.Total != 1 || r.RowFrom != 1 || !r.Options.TestOne {
		t.Fatalf("test one: %+v %v", r, err)
	}
	// export
	run := e.create(t, RunOptionsDTO{})
	_ = e.svc.StartRun(rbg, run.ID)
	e.waitState(t, run.ID, "finished")
	out := filepath.Join(e.dir, "ledger.csv")
	got, err := e.svc.ExportLedger(rbg, run.ID, out)
	if err != nil || got != out {
		t.Fatal(got, err)
	}
	raw, _ := os.ReadFile(out)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 6 || !strings.HasPrefix(lines[0], "row,state,commit,profile") || !strings.HasPrefix(lines[1], "1,done,submitted,") {
		t.Fatalf("csv:\n%s", raw)
	}
	if strings.Contains(string(raw), "user3") {
		t.Fatal("the export holds source data")
	}
	if csvSafe("=cmd()") != "'=cmd()" || csvSafe("ok") != "ok" {
		t.Fatal("csvSafe")
	}
	e.svc.savePath = func(string) (string, error) { return "", nil }
	if got, err = e.svc.ExportLedger(rbg, run.ID, ""); got != "" || err != nil {
		t.Fatalf("cancelled dialog: %q %v", got, err)
	}
}
