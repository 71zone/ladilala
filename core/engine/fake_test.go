package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/data"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/player"
	"github.com/8bu/ladilala/core/profile"
	"github.com/8bu/ladilala/core/store"
)

// memSource is a data.Source over a slice.
type memSource struct{ rows []data.Row }

func newSource(n int) *memSource {
	s := &memSource{}
	for i := range n {
		s.rows = append(s.rows, data.Row{"n": strconv.Itoa(i), "name": fmt.Sprintf("user%d", i)})
	}
	return s
}

func (s *memSource) Columns(context.Context) ([]string, error) { return []string{"n", "name"}, nil }
func (s *memSource) Count(context.Context) (int, error)        { return len(s.rows), nil }
func (s *memSource) Close() error                              { return nil }
func (s *memSource) Open(_ context.Context, from, to int) (data.Rows, error) {
	if to < 0 || to > len(s.rows) {
		to = len(s.rows)
	}
	return &memRows{s: s, i: from, to: to}, nil
}

type memRows struct {
	s     *memSource
	i, to int
}

func (r *memRows) Next(context.Context) (int, data.Row, bool, error) {
	if r.i >= r.to {
		return 0, nil, false, nil
	}
	i := r.i
	r.i++
	return i, r.s.rows[i], true, nil
}
func (r *memRows) Close() error { return nil }

// fake is a Launcher whose browsers play the macro steps with small sleeps.
type fake struct {
	stepDelay time.Duration

	mu       sync.Mutex
	launches map[string]int
	plays    map[int][]string // row index -> profiles that played it
	live     int
	maxLive  int

	// hook, when set, runs first in PlayRow. handled=true ends PlayRow with err.
	hook func(ctx context.Context, f *fakeBrowser, idx int, h player.Hooks) (handled bool, err error)
	// launchErr, when set, can fail a launch (n counts launches of that profile).
	launchErr func(profileID string, n int) error
	// recheck is what fakeBrowser.Recheck returns.
	recheck func() error
}

func newFake() *fake {
	return &fake{stepDelay: time.Millisecond, launches: map[string]int{}, plays: map[int][]string{}}
}

func (f *fake) Launch(ctx context.Context, p profile.Profile) (Browser, error) {
	f.mu.Lock()
	f.launches[p.ID]++
	n := f.launches[p.ID]
	fail := f.launchErr
	f.mu.Unlock()
	if fail != nil {
		if err := fail(p.ID, n); err != nil {
			return nil, err
		}
	}
	return &fakeBrowser{f: f, profile: p.ID, done: make(chan struct{})}, nil
}

func (f *fake) playsOf(idx int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.plays[idx]...)
}

type fakeBrowser struct {
	f       *fake
	profile string
	done    chan struct{}
	once    sync.Once
}

func (b *fakeBrowser) Done() <-chan struct{} { return b.done }
func (b *fakeBrowser) Close() error          { b.once.Do(func() { close(b.done) }); return nil }
func (b *fakeBrowser) kill()                 { b.once.Do(func() { close(b.done) }) }
func (b *fakeBrowser) Recheck(context.Context, *macro.Macro) error {
	if b.f.recheck != nil {
		return b.f.recheck()
	}
	return nil
}

func (b *fakeBrowser) PlayRow(ctx context.Context, m *macro.Macro, row map[string]string, h player.Hooks) error {
	idx, _ := strconv.Atoi(row["n"])
	f := b.f
	f.mu.Lock()
	f.plays[idx] = append(f.plays[idx], b.profile)
	f.live++
	if f.live > f.maxLive {
		f.maxLive = f.live
	}
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.live--; f.mu.Unlock() }()
	if f.hook != nil {
		if handled, err := f.hook(ctx, b, idx, h); handled {
			return err
		}
	}
	return b.playSteps(ctx, m, h)
}

// playSteps mimics core/player: gate, commit hooks, dry run.
func (b *fakeBrowser) playSteps(ctx context.Context, m *macro.Macro, h player.Hooks) error {
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
			time.Sleep(b.f.stepDelay)
			if h.AfterCommit != nil {
				h.AfterCommit(ctx, s, nil)
			}
			continue
		}
		select {
		case <-time.After(b.f.stepDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// testMacro has a navigate, a fill and a commit point click.
func testMacro() *macro.Macro {
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

type env struct {
	st       *store.Store
	svc      *Service
	f        *fake
	profiles []string
	dir      string
}

func newEnv(t *testing.T, nProfiles int) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir, &store.MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{st: st, f: newFake(), dir: dir}
	e.svc = NewService(st, e.f)
	if _, err := st.Macros.Put(context.Background(), testMacro()); err != nil {
		t.Fatal(err)
	}
	for i := range nProfiles {
		fp := profile.NewFingerprint(uint64(i+1), profile.Options{Platform: profile.PlatformWindows, BrowserVersion: "150.0.7871.186"})
		rec, err := st.Profiles.Put(context.Background(), store.ProfileRecord{Profile: profile.Profile{Name: fmt.Sprintf("p%d", i), Fingerprint: fp, DataDir: filepath.Join(dir, fmt.Sprintf("pd%d", i))}})
		if err != nil {
			t.Fatal(err)
		}
		e.profiles = append(e.profiles, rec.ID)
	}
	return e
}

func (e *env) create(t *testing.T, rows int, opts RunOptions) Run {
	t.Helper()
	run, err := e.svc.CreateRun(context.Background(), CreateRunRequest{MacroID: "signup", Source: newSource(rows), To: -1, ProfileIDs: e.profiles, Options: opts})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func (e *env) waitState(t *testing.T, id string, want RunState, timeout time.Duration) Run {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		r, err := e.svc.GetRun(context.Background(), id)
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

func (e *env) waitCount(t *testing.T, id string, st RowState, atLeast int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		r, _ := e.svc.GetRun(context.Background(), id)
		if r.Counts[st] >= atLeast {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d rows %s of %d: %v", r.Counts[st], st, atLeast, r.Counts)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *env) row(t *testing.T, id string, idx int) Row {
	t.Helper()
	p, err := e.svc.ListRows(context.Background(), id, RowFilter{Indexes: []int{idx}})
	if err != nil || len(p.Rows) != 1 {
		t.Fatalf("row %d: %v %v", idx, p, err)
	}
	return p.Rows[0]
}
