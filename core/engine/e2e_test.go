package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/data"
	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/profile"
	"github.com/8bu/ladilala/core/recorder"
	"github.com/8bu/ladilala/core/recorder/rectest"
	"github.com/8bu/ladilala/core/store"
)

func e2eBrowser() string {
	if b := os.Getenv("LADILALA_E2E_BROWSER"); b != "" {
		return b
	}
	return "chrome"
}

// recordMacro records the scripted session and turns the typed literals into
// columns. The last click (the shadow page submit) becomes the commit point.
func recordMacro(ctx context.Context, site *recorder.Site) (*macro.Macro, error) {
	rec := recorder.New(recorder.Options{ID: "e2e-signup", Name: "E2E sign-up"})
	b, err := rectest.Launch(ctx, e2eBrowser(), 31, rec.Hook)
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

// TestE2ERunEngine records a macro, runs it for 10 data rows on 2 real Chrome
// profiles and checks that the test site saw 10 distinct submissions and that
// the ledger agrees.
//
//	LADILALA_E2E=1 go test -count=1 -v -run TestE2ERunEngine ./core/engine/
func TestE2ERunEngine(t *testing.T) {
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
	m, err := recordMacro(ctx, site)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	t.Logf("macro: %d steps, origins %v", len(m.Steps), m.AllowedOrigins)
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
	in, err := rectest.PickInstall(e2eBrowser())
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
	src := &memSource{}
	for i := range rows {
		src.rows = append(src.rows, data.Row(rectest.Row(i)))
	}
	launcher, err := NewStockLauncher()
	if err != nil {
		t.Fatal(err)
	}
	defer launcher.Close()
	launcher.Brand = in.Brand
	svc := NewService(st, launcher)
	run, err := svc.CreateRun(ctx, CreateRunRequest{MacroID: m.ID, Source: src, To: -1, ProfileIDs: ids, Options: RunOptions{Concurrency: 2}})
	if err != nil {
		t.Fatal(err)
	}
	evs, err := svc.Subscribe(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for ev := range evs {
			t.Logf("event %s row=%d profile=%.6s state=%s %s", ev.Kind, ev.RowIndex, ev.ProfileID, ev.RowState, ev.Message)
		}
	}()
	t0 := time.Now()
	if err := svc.StartRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	var r Run
	for {
		if r, err = svc.GetRun(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		if r.State == StateFinished || r.State == StateFailed || r.State == StatePaused {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("timeout: %s %v", r.State, r.Counts)
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Logf("run %s in %s: %v (%s)", r.State, time.Since(t0).Round(time.Millisecond), r.Counts, r.Reason)
	pg, _ := svc.ListRows(ctx, run.ID, RowFilter{Limit: 100})
	for _, row := range pg.Rows {
		t.Logf("row %d: %s commit=%s profile=%.6s %s %s", row.Index, row.State, row.CommitState, row.ProfileID, row.ErrorKind, row.ErrorMsg)
	}
	if r.State != StateFinished || r.Counts[RowDone] != rows {
		t.Fatalf("run did not finish clean: %s %v", r.State, r.Counts)
	}
	profiles := map[string]int{}
	for _, row := range pg.Rows {
		if row.State != RowDone || row.CommitState != CommitSubmitted || row.Attempts != 1 {
			t.Errorf("ledger row %d: %+v", row.Index, row)
		}
		profiles[row.ProfileID]++
	}
	if len(profiles) != 2 {
		t.Errorf("rows ran on %d profiles: %v", len(profiles), profiles)
	}
	// the site: 10 submissions per page, and every row's values arrived as often
	// as the data holds that exact set of values (the nick column repeats)
	same := func(a, b map[string]string) bool {
		for k, v := range a {
			if b[k] != v {
				return false
			}
		}
		return true
	}
	for i := range rows {
		for page, want := range rectest.ExpectedSinks(rectest.Row(i)) {
			n, expect := 0, 0
			for j := range rows {
				if same(want, rectest.ExpectedSinks(rectest.Row(j))[page]) {
					expect++
				}
			}
			for _, sub := range site.Sinks(page) {
				got := map[string]string{}
				for k := range want {
					got[k] = fmt.Sprint(sub[k])
				}
				if same(want, got) {
					n++
				}
			}
			if n != expect {
				t.Errorf("row %d: page %s received its values %d times, want %d", i, page, n, expect)
			}
		}
	}
	for _, page := range []string{"form", "page2", "delayed", "frame-same", "frame-cross", "shadow"} {
		if n := len(site.Sinks(page)); n != rows {
			t.Errorf("page %s got %d submissions, want %d", page, n, rows)
		}
	}
}
