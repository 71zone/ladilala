package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/engine"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/profile"
	"github.com/8bu/ladilala/core/recorder"
	"github.com/8bu/ladilala/core/recorder/rectest"
	"github.com/8bu/ladilala/core/store"
)

func runE2EBrowser() string {
	if b := os.Getenv("LADILALA_E2E_BROWSER"); b != "" {
		return b
	}
	return "chrome"
}

// runE2ERecord records the scripted session and turns the typed literals into
// columns. The last click (the shadow page submit) becomes the commit point.
func runE2ERecord(ctx context.Context, site *recorder.Site) (*macro.Macro, error) {
	rec := recorder.New(recorder.Options{ID: "e2e-signup", Name: "E2E sign-up"})
	b, err := rectest.Launch(ctx, runE2EBrowser(), 31, rec.Hook)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	pg, err := b.Session.WaitPage(ctx)
	if err != nil {
		return nil, err
	}
	if err := rectest.Scenario(rectest.NewUser(ctx, b.Session, pg), site); err != nil {
		return nil, err
	}
	time.Sleep(400 * time.Millisecond)
	rec.Stop()
	m, err := rec.Macro()
	if err != nil {
		return nil, err
	}
	if _, err := recorder.Parameterize(m, rectest.Recorded); err != nil {
		return nil, err
	}
	for i := len(m.Steps) - 1; i >= 0; i-- {
		if m.Steps[i].Action == macro.ActionClick {
			m.Steps[i].CommitPoint = true
			break
		}
	}
	raw, err := macro.Marshal(m)
	if err != nil {
		return nil, err
	}
	return macro.Load(bytes.NewReader(raw))
}

// TestE2ERunService records a macro, saves it with a CSV source and two
// profiles, and runs 10 rows through RunService on real Chrome. The ledger
// (read through the service) and the test site must agree.
//
//	LADILALA_E2E=1 go test -count=1 -v -run TestE2ERunService ./app/
func TestE2ERunService(t *testing.T) {
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	const rows = 10
	site, err := recorder.NewSite()
	if err != nil {
		t.Fatal(err)
	}
	defer site.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	m, err := runE2ERecord(ctx, site)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	site.ResetSinks()

	dir := t.TempDir()
	st, err := store.Open(ctx, dir, &store.MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Macros.Put(ctx, m); err != nil {
		t.Fatal(err)
	}
	in, err := rectest.PickInstall(runE2EBrowser())
	if err != nil {
		t.Skipf("no browser: %v", err)
	}
	version, _ := driver.StockChrome{}.Version(in)
	plat := profile.PlatformWindows
	switch goruntime.GOOS {
	case "darwin":
		plat = profile.PlatformMacOS
	case "linux":
		plat = profile.PlatformLinux
	}
	var ids []string
	for i := range 2 {
		fp := profile.NewFingerprint(uint64(500+i), profile.Options{Platform: plat, BrowserVersion: version})
		rec, err := st.Profiles.Put(ctx, store.ProfileRecord{Profile: profile.Profile{Name: fmt.Sprintf("e2e%d", i), Fingerprint: fp, DataDir: filepath.Join(dir, fmt.Sprintf("profile%d", i))}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, rec.ID)
	}
	// the source: a saved CSV with the columns the macro uses
	cols := make([]string, 0, len(rectest.Row(0)))
	for k := range rectest.Row(0) {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	path := filepath.Join(dir, "rows.csv")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := csv.NewWriter(f)
	_ = w.Write(cols)
	for i := range rows {
		r := rectest.Row(i)
		rec := make([]string, len(cols))
		for j, c := range cols {
			rec[j] = r[c]
		}
		_ = w.Write(rec)
	}
	w.Flush()
	f.Close()
	cfg, _ := json.Marshal(dataSourceConfig{Path: path})
	srcRec, err := st.Sources.Put(ctx, store.SourceRecord{Name: "e2e rows", Kind: "csv", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}

	launcher, err := engine.NewStockLauncher()
	if err != nil {
		t.Fatal(err)
	}
	defer launcher.Close()
	launcher.Brand = in.Brand
	b := &Backend{DataDir: dir, Store: st, Engine: engine.NewService(st, launcher)}
	cp := &runCaptured{}
	svc := &RunService{b: b, emit: cp.emit, watchers: map[string]*watcher{}}
	defer svc.ServiceShutdown()

	run, err := svc.CreateRun(ctx, CreateRunRequest{MacroID: m.ID, SourceID: srcRec.ID, To: -1, ProfileIDs: ids, Options: RunOptionsDTO{Concurrency: 2}})
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	if err := svc.StartRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	var r RunDTO
	for {
		if r, err = svc.GetRun(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		if r.State == "finished" || r.State == "failed" || r.State == "paused" {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("timeout: %s %v", r.State, r.Counts)
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Logf("run %s in %s: %v (%s)", r.State, time.Since(t0).Round(time.Millisecond), r.Counts, r.Reason)
	pg, err := svc.ListRows(ctx, run.ID, RowFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if r.State != "finished" || r.Counts["done"] != rows || pg.Total != rows {
		for _, row := range pg.Rows {
			t.Logf("row %d: %s %s %s", row.Index, row.State, row.ErrorKind, row.ErrorMsg)
		}
		t.Fatalf("run did not finish clean: %s %v", r.State, r.Counts)
	}
	profiles := map[string]int{}
	for _, row := range pg.Rows {
		if row.State != "done" || row.CommitState != "submitted" || row.Attempts != 1 {
			t.Errorf("ledger row %d: %+v", row.Index, row)
		}
		profiles[row.ProfileID]++
	}
	if len(profiles) != 2 {
		t.Errorf("rows ran on %d profiles: %v", len(profiles), profiles)
	}
	// the site saw one submission per row on every page
	for _, page := range []string{"form", "page2", "delayed", "frame-same", "frame-cross", "shadow"} {
		if n := len(site.Sinks(page)); n != rows {
			t.Errorf("page %s got %d submissions, want %d", page, n, rows)
		}
	}
	// the events the UI got end at the same counts the ledger holds
	time.Sleep(2 * progressEvery)
	prog := cp.named(evRunProgress)
	if len(prog) == 0 {
		t.Fatal("no progress events")
	}
	if last := prog[len(prog)-1].data.(ProgressEvent); last.Counts["done"] != rows {
		t.Errorf("last progress event: %+v", last)
	}
	out := filepath.Join(dir, "ledger.csv")
	if got, err := svc.ExportLedger(ctx, run.ID, out); err != nil || got != out {
		t.Errorf("export: %q %v", got, err)
	}
}
