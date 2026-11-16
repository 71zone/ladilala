package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/profile"
)

func open(t *testing.T) (*Store, *MemorySecrets, string) {
	t.Helper()
	dir := t.TempDir()
	sec := &MemorySecrets{}
	s, err := Open(context.Background(), dir, sec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, sec, dir
}

func testMacro(name string) *macro.Macro {
	return &macro.Macro{
		SchemaVersion: 1, ID: "m1", Name: name,
		AllowedOrigins: []string{"https://example.com"},
		Steps:          []macro.Step{{ID: "s1", Action: macro.ActionNavigate, Value: &macro.Value{Expr: "https://example.com/"}}},
	}
}

func TestMigrations(t *testing.T) {
	ctx := context.Background()
	s, _, dir := open(t)
	latest, err := LatestVersion()
	if err != nil || latest < 1 {
		t.Fatalf("latest = %d, %v", latest, err)
	}
	if v, _ := s.Version(ctx); v != latest {
		t.Fatalf("version = %d, want %d", v, latest)
	}
	if err := s.Migrate(ctx); err != nil { // idempotent
		t.Fatal(err)
	}
	if _, err := s.Macros.Put(ctx, testMacro("a")); err != nil {
		t.Fatal(err)
	}
	// reopen: data stays, version stays
	s.Close()
	s2, err := Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.Macros.Get(ctx, "m1"); err != nil {
		t.Fatalf("macro lost after reopen: %v", err)
	}
	// down to zero removes the tables, up brings them back
	if err := s2.MigrateDown(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if v, _ := s2.Version(ctx); v != 0 {
		t.Fatalf("version after down = %d", v)
	}
	var n int
	if err := s2.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('runs','profiles','run_rows')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tables left after down: %d, %v", n, err)
	}
	if err := s2.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := s2.Version(ctx); v != latest {
		t.Fatalf("version after up = %d", v)
	}
	var mode string
	if err := s2.DB().QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
}

func TestProfilesProxiesSecrets(t *testing.T) {
	ctx := context.Background()
	s, sec, dir := open(t)
	pw := "hunter2-secret-password"
	px, err := s.Proxies.Put(ctx, ProxyRecord{Name: "p1", Scheme: profile.ProxySOCKS5, Host: "10.0.0.1", Port: 1080, Username: "bob"}, &pw)
	if err != nil {
		t.Fatal(err)
	}
	if !px.HasSecret {
		t.Fatal("HasSecret not set")
	}
	fp := profile.NewFingerprint(7, profile.Options{Platform: profile.PlatformWindows, BrowserVersion: "150.0.7871.186"})
	in := ProfileRecord{Profile: profile.Profile{Name: "P", Group: "g", Tags: []string{"a", "b"}, Notes: "n", Fingerprint: fp, DataDir: filepath.Join(dir, "d")}, ProxyID: px.ID}
	saved, err := s.Profiles.Put(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Profiles.Get(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := in.Profile
	want.ID = saved.ID
	want.Proxy = profile.Proxy{Scheme: profile.ProxySOCKS5, Host: "10.0.0.1", Port: 1080, Username: "bob", Password: pw}
	if !reflect.DeepEqual(got.Profile, want) {
		t.Fatalf("profile round trip differs\n got %+v\nwant %+v", got.Profile, want)
	}
	if got.ProxyID != px.ID {
		t.Fatalf("ProxyID = %q", got.ProxyID)
	}
	// the password is in the secret store only
	if len(sec.Keys()) != 1 || sec.Keys()[0] != ProxySecretKey(px.ID) {
		t.Fatalf("secret keys = %v", sec.Keys())
	}
	s.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	for _, f := range []string{"ladilala.db", "ladilala.db-wal"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err == nil && bytes.Contains(b, []byte(pw)) {
			t.Fatalf("password found in %s", f)
		}
	}
	// update keeps the password when nil, removes it with ""
	px.Name = "renamed"
	if _, err := s.Proxies.Put(ctx, px, nil); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Proxies.Password(ctx, px.ID); p != pw {
		t.Fatalf("password lost on update: %q", p)
	}
	empty := ""
	if _, err := s.Proxies.Put(ctx, px, &empty); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Proxies.Password(ctx, px.ID); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("password not removed: %v", err)
	}
	list, err := s.Profiles.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %d, %v", len(list), err)
	}
	if err := s.Proxies.Delete(ctx, px.ID); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Profiles.Get(ctx, saved.ID); g.ProxyID != "" || g.Proxy.Scheme != profile.ProxyNone {
		t.Fatalf("profile kept a deleted proxy: %+v", g.Proxy)
	}
	if err := s.Profiles.Delete(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Profiles.Get(ctx, saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestMacrosAndSources(t *testing.T) {
	ctx := context.Background()
	s, _, _ := open(t)
	a, err := s.Macros.Put(ctx, testMacro("first"))
	if err != nil || a.Version != 1 {
		t.Fatalf("put: %+v %v", a, err)
	}
	same, _ := s.Macros.Put(ctx, testMacro("first"))
	if same.Version != 1 || same.Hash != a.Hash {
		t.Fatalf("unchanged macro changed version: %+v", same)
	}
	m2 := testMacro("second")
	b, _ := s.Macros.Put(ctx, m2)
	if b.Version != 2 || b.Hash == a.Hash {
		t.Fatalf("changed macro: %+v", b)
	}
	got, err := s.Macros.Get(ctx, "m1")
	if err != nil || got.Name != "second" || HashBody(got.Body) != got.Hash {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := macro.Load(bytes.NewReader(got.Body)); err != nil {
		t.Fatalf("stored body does not load: %v", err)
	}
	bad := testMacro("bad")
	bad.AllowedOrigins = nil
	if _, err := s.Macros.Put(ctx, bad); err == nil {
		t.Fatal("invalid macro stored")
	}
	d, err := s.Sources.Put(ctx, SourceRecord{Name: "leads", Kind: "csv", Config: []byte(`{"path":"/x.csv"}`)})
	if err != nil {
		t.Fatal(err)
	}
	gd, err := s.Sources.Get(ctx, d.ID)
	if err != nil || string(gd.Config) != `{"path":"/x.csv"}` || gd.Kind != "csv" {
		t.Fatalf("source: %+v %v", gd, err)
	}
	if _, err := s.Sources.Put(ctx, SourceRecord{Name: "x", Kind: "csv", Config: []byte(`{`)}); err == nil {
		t.Fatal("invalid config stored")
	}
}

func makeRun(t *testing.T, s *Store, n int) RunRecord {
	t.Helper()
	var seeds []RowSeed
	for i := range n {
		seeds = append(seeds, RowSeed{Index: i, Data: map[string]string{"a": "x"}})
	}
	r, err := s.Runs.Create(context.Background(), RunRecord{MacroID: "m1", MacroName: "n", MacroVersion: 1, MacroHash: "h", MacroBody: []byte(`{}`), State: "created", ProfileIDs: []string{"p1"}, RowFrom: 0, RowTo: -1}, seeds)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLedger(t *testing.T) {
	ctx := context.Background()
	s, _, _ := open(t)
	run := makeRun(t, s, 20)
	got, err := s.Runs.Get(ctx, run.ID)
	if err != nil || got.State != "created" || got.StartedAt != nil || !reflect.DeepEqual(got.ProfileIDs, []string{"p1"}) {
		t.Fatalf("run: %+v %v", got, err)
	}
	// concurrent claims never give the same row twice
	var mu sync.Mutex
	seen := map[int]int{}
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for {
				row, ok, err := s.Rows.Claim(ctx, run.ID, string(rune('a'+w)))
				if err != nil {
					t.Error(err)
					return
				}
				if !ok {
					return
				}
				mu.Lock()
				seen[row.Index]++
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if len(seen) != 20 {
		t.Fatalf("claimed %d rows, want 20", len(seen))
	}
	for i, n := range seen {
		if n != 1 {
			t.Fatalf("row %d claimed %d times", i, n)
		}
	}
	c, _ := s.Rows.Counts(ctx, run.ID)
	if c["running"] != 20 {
		t.Fatalf("counts = %v", c)
	}
	// finish, release, commit
	if err := s.Rows.Finish(ctx, run.ID, 0, "done", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Rows.Finish(ctx, run.ID, 0, "failed", "x", "y", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("second finish: %v", err)
	}
	if err := s.Rows.Release(ctx, run.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Rows.SetCommit(ctx, run.ID, 2, "submitting", "s9"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rows.Release(ctx, run.ID, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("release after commit: %v", err)
	}
	if err := s.Rows.Finish(ctx, run.ID, 3, "failed", "locator", "no", "s2"); err != nil {
		t.Fatal(err)
	}
	// recover: row 2 (commit) -> uncertain, the other running ones -> pending
	p, u, err := s.Rows.Recover(ctx, run.ID)
	if err != nil || u != 1 || p != 16 {
		t.Fatalf("recover: pending=%d uncertain=%d err=%v", p, u, err)
	}
	r2, _ := s.Rows.Get(ctx, run.ID, 2)
	if r2.State != "uncertain" || r2.CommitState != "submitting" || r2.StepID != "s9" || r2.IdempotencyKey != run.ID+":2" {
		t.Fatalf("row 2: %+v", r2)
	}
	// reset touches failed only, never uncertain
	n, err := s.Rows.Reset(ctx, RowQuery{RunID: run.ID, States: []string{"failed", "uncertain", "done"}})
	if err != nil || n != 1 {
		t.Fatalf("reset = %d, %v", n, err)
	}
	if r2, _ = s.Rows.Get(ctx, run.ID, 2); r2.State != "uncertain" {
		t.Fatal("uncertain row was reset")
	}
	if err := s.Rows.Resolve(ctx, run.ID, 2, "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rows.Resolve(ctx, run.ID, 2, "failed"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second resolve: %v", err)
	}
	page, total, err := s.Rows.List(ctx, RowQuery{RunID: run.ID, States: []string{"pending"}, Limit: 5, Offset: 2})
	if err != nil || total != 18 || len(page) != 5 || page[0].Index >= page[1].Index {
		t.Fatalf("page: total=%d len=%d err=%v", total, len(page), err)
	}
	// run state timestamps
	if err := s.Runs.SetState(ctx, run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Runs.Get(ctx, run.ID)
	if got.StartedAt == nil || got.FinishedAt != nil {
		t.Fatalf("running: %+v", got)
	}
	_ = s.Runs.SetState(ctx, run.ID, "finished", "")
	if got, _ = s.Runs.Get(ctx, run.ID); got.FinishedAt == nil {
		t.Fatal("no finished_at")
	}
}

func TestEventsAppendOnly(t *testing.T) {
	ctx := context.Background()
	s, _, _ := open(t)
	run := makeRun(t, s, 1)
	idx := 0
	id1, err := s.Events.Append(ctx, run.ID, &idx, "row.done", map[string]any{"k": 1})
	if err != nil {
		t.Fatal(err)
	}
	id2, _ := s.Events.Append(ctx, run.ID, nil, "run.state", nil)
	if id2 <= id1 {
		t.Fatalf("ids %d %d", id1, id2)
	}
	ev, err := s.Events.List(ctx, run.ID, id1, 10)
	if err != nil || len(ev) != 1 || ev[0].Kind != "run.state" || ev[0].RowIndex != nil {
		t.Fatalf("list: %+v %v", ev, err)
	}
	if _, err := s.DB().Exec(`UPDATE run_events SET kind='x'`); err == nil {
		t.Fatal("update of run_events succeeded")
	}
	// deleting the run removes ledger rows and events
	if err := s.Runs.Delete(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if ev, _ = s.Events.List(ctx, run.ID, 0, 10); len(ev) != 0 {
		t.Fatal("events outlived the run")
	}
}

func TestFileSecrets(t *testing.T) {
	ctx := context.Background()
	sec := DefaultSecrets(t.TempDir())
	if err := sec.Set(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, err := sec.Get(ctx, "k"); err != nil || v != "v" {
		t.Fatalf("%q %v", v, err)
	}
	if _, err := sec.Get(ctx, "zz"); !errors.Is(err, ErrNoSecret) {
		t.Fatal(err)
	}
	_ = sec.Delete(ctx, "k")
	if _, err := sec.Get(ctx, "k"); !errors.Is(err, ErrNoSecret) {
		t.Fatal(err)
	}
}
