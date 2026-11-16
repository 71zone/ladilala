package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

type fakeUpdater struct {
	mu        sync.Mutex
	release   *updater.Release
	checkErr  error
	dlErr     error
	dlBlock   chan struct{}
	skipped   string
	checks    int
	downloads int
	restarts  atomic.Int32
}

func (f *fakeUpdater) Check(context.Context) (*updater.Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	if f.checkErr != nil {
		return nil, f.checkErr
	}
	if f.release != nil && f.release.Version == f.skipped {
		return nil, nil
	}
	return f.release, nil
}

func (f *fakeUpdater) DownloadAndInstall(ctx context.Context) error {
	f.mu.Lock()
	f.downloads++
	block, err := f.dlBlock, f.dlErr
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *fakeUpdater) Restart(context.Context) error { f.restarts.Add(1); return nil }
func (f *fakeUpdater) SkipVersion(v string)          { f.mu.Lock(); f.skipped = v; f.mu.Unlock() }

func newUpdateFixture(t *testing.T, feed string) (*AppUpdateService, *fakeUpdater) {
	t.Helper()
	t.Setenv("LADILALA_UPDATE_URL", "")
	dir := t.TempDir()
	cfg, err := openSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Update(func(s *Settings) error { s.Update.FeedURL = feed; return nil })
	s := newAppUpdateService(&Backend{DataDir: dir, Settings: cfg}, nil)
	s.version = "1.0.0"
	s.idleGap = 5 * time.Millisecond
	f := &fakeUpdater{release: &updater.Release{
		Version: "1.1.0", Channel: "stable", Notes: "Faster runs", Artifact: updater.Artifact{Size: 1000},
		Metadata: map[string]any{"minEngine": float64(150), "news": []any{"loop"}, "fixed": []any{"bom"}},
	}}
	s.up = func() appUpdater { return f }
	s.emit = nil
	return s, f
}

func waitPhase(t *testing.T, s *AppUpdateService, phase string) AppUpdateState {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		if st, _ := s.GetAppUpdateState(context.Background()); st.Phase == phase {
			return st
		}
	}
	st, _ := s.GetAppUpdateState(context.Background())
	t.Fatalf("phase %s, want %s: %+v", st.Phase, phase, st)
	return st
}

func TestAppUpdateNotConfigured(t *testing.T) {
	s, f := newUpdateFixture(t, "")
	st, _ := s.GetAppUpdateState(context.Background())
	if st.Configured || st.Phase != phaseIdle || st.Current != "1.0.0" {
		t.Fatalf("state %+v", st)
	}
	if _, err := s.CheckAppUpdate(context.Background(), true); !isCode(err, "update.notConfigured") {
		t.Fatalf("check without a feed: %v", err)
	}
	if f.checks != 0 {
		t.Error("the updater was called without a feed")
	}
	// The environment variable configures the feed.
	t.Setenv("LADILALA_UPDATE_URL", "http://127.0.0.1:9/m.json")
	if st, _ = s.GetAppUpdateState(context.Background()); !st.Configured || !st.FeedFromEnv || st.FeedURL != "http://127.0.0.1:9/m.json" {
		t.Errorf("env feed %+v", st)
	}
	// The provider has no feed to call.
	t.Setenv("LADILALA_UPDATE_URL", "")
	if _, err := newFeedProvider(s.cfg).Check(context.Background(), updater.CheckRequest{CurrentVersion: "1.0.0"}); !errors.Is(err, errUpdatesNotConfigured) {
		t.Errorf("provider: %v", err)
	}
}

func TestAppUpdateCheckDownloadRestart(t *testing.T) {
	s, f := newUpdateFixture(t, "https://updates.example/m.json")
	ctx := context.Background()
	st, err := s.CheckAppUpdate(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	r := st.Release
	if st.Phase != phaseAvailable || r == nil || r.Version != "1.1.0" || r.MinEngine != 150 || r.SizeBytes != 1000 || r.News[0] != "loop" || r.Fixed[0] != "bom" || st.LastChecked == "" {
		t.Fatalf("after check %+v %+v", st, r)
	}
	// Engine 0 is below the need: shown, not enforced.
	if st.EngineCompatible {
		t.Errorf("compat should be false: %+v", st)
	}
	if err := s.RestartNow(ctx); err == nil {
		t.Error("restart before download")
	}
	f.dlBlock = make(chan struct{})
	if _, err := s.DownloadAppUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, s, phaseDownloading)
	s.onProgress(500, 1000, 100)
	if st, _ = s.GetAppUpdateState(ctx); st.BytesDone != 500 || st.BytesPerSec != 100 || st.Stages[1].State != "active" {
		t.Errorf("progress %+v", st)
	}
	s.onProgress(1000, 1000, 100)
	if st, _ = s.GetAppUpdateState(ctx); st.Stages[1].State != "done" || st.Stages[2].State != "active" {
		t.Errorf("verify stage %+v", st.Stages)
	}
	// Cancel returns to available.
	s.CancelAppUpdate(ctx)
	waitPhase(t, s, phaseAvailable)

	close(f.dlBlock)
	if _, err := s.DownloadAppUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	st = waitPhase(t, s, phaseReady)
	for _, g := range st.Stages {
		if g.State != "done" {
			t.Errorf("stage %+v", g)
		}
	}
	// A run is active: restart is refused, restart-when-idle waits.
	var runs atomic.Int32
	runs.Store(1)
	s.runs = func() int { return int(runs.Load()) }
	if err := s.RestartNow(ctx); !isCode(err, "conflict") {
		t.Fatalf("restart during a run: %v", err)
	}
	if st, err = s.RestartWhenIdle(ctx); err != nil || !st.WhenIdle || st.ActiveRuns != 1 {
		t.Fatalf("idle %+v %v", st, err)
	}
	time.Sleep(40 * time.Millisecond)
	if f.restarts.Load() != 0 {
		t.Fatal("restarted while a run was active")
	}
	runs.Store(0)
	for end := time.Now().Add(3 * time.Second); f.restarts.Load() == 0 && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
	}
	if f.restarts.Load() != 1 {
		t.Fatalf("restarts %d", f.restarts.Load())
	}
}

func TestAppUpdateFailuresAndFixes(t *testing.T) {
	s, f := newUpdateFixture(t, "https://updates.example/m.json")
	ctx := context.Background()
	f.checkErr = errors.New("endpoint: dial tcp: no route to host")
	st, _ := s.CheckAppUpdate(ctx, false)
	if st.Phase != phaseFailed || st.Failure.Code != "update.network" || st.Failure.Details["fixId"] != "retry" {
		t.Fatalf("check failure %+v", st)
	}
	f.checkErr = nil
	if st, err := s.CheckAppUpdate(ctx, true); err != nil || st.Phase != phaseAvailable || st.Failure != nil {
		t.Fatalf("recheck %+v %v", st, err)
	}
	f.dlErr = errors.New("updater: signature verification failed")
	s.DownloadAppUpdate(ctx)
	st = waitPhase(t, s, phaseFailed)
	if st.Failure.Code != "update.signature" || st.Failure.Details["fixId"] != "redownload" || st.Stages[1].State != "fail" {
		t.Fatalf("signature failure %+v", st)
	}
	f.dlErr = nil
	if _, err := s.ApplyUpdateFix(ctx, "redownload"); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, s, phaseReady)
	if f.downloads != 2 {
		t.Errorf("downloads %d", f.downloads)
	}
	if _, err := s.ApplyUpdateFix(ctx, "nope"); !isCode(err, "invalid") {
		t.Errorf("unknown fix: %v", err)
	}
}

func TestAppUpdateSkipAndSettings(t *testing.T) {
	s, f := newUpdateFixture(t, "https://updates.example/m.json")
	ctx := context.Background()
	s.CheckAppUpdate(ctx, false)
	st, err := s.SkipVersion(ctx, "1.1.0")
	if err != nil || st.Release != nil || st.Skipped != "1.1.0" || st.Phase != phaseIdle {
		t.Fatalf("skip %+v %v", st, err)
	}
	if st, _ = s.CheckAppUpdate(ctx, false); st.Release != nil {
		t.Errorf("a background check showed a skipped release: %+v", st)
	}
	if st, _ = s.CheckAppUpdate(ctx, true); st.Release == nil || st.Skipped != "" {
		t.Errorf("Check Now must show it again: %+v", st)
	}
	_ = f

	in := AppUpdateSettings{Channel: "beta", AutoCheck: "weekly", InstallOnQuit: false, FeedURL: "https://r2.example/beta.json"}
	if st, err = s.SetAppUpdateSettings(ctx, in); err != nil || st.Channel != "beta" || st.AutoCheck != "weekly" || st.InstallOnQuit || st.FeedURL != in.FeedURL || st.Release != nil {
		t.Fatalf("settings %+v %v", st, err)
	}
	for _, bad := range []AppUpdateSettings{{Channel: "x", AutoCheck: "daily"}, {Channel: "beta", AutoCheck: "hourly"}, {Channel: "beta", AutoCheck: "daily", FeedURL: "http://evil.example/m.json"}} {
		if _, err := s.SetAppUpdateSettings(ctx, bad); !isCode(err, "invalid") {
			t.Errorf("%+v accepted", bad)
		}
	}
	if _, err := s.GetReleaseNotes(ctx, "9.9.9"); !isCode(err, "notFound") {
		t.Errorf("notes: %v", err)
	}
}

// A ready update is handed to the updater on the first quit request, which is
// held back; the Quit that Restart causes then passes.
func TestBeforeQuitHandsOffReadyUpdate(t *testing.T) {
	s, f := newUpdateFixture(t, "https://updates.example/m.json")
	ctx := context.Background()
	if !s.BeforeQuit() {
		t.Fatal("quit held back with no update ready")
	}
	if _, err := s.CheckAppUpdate(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DownloadAppUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, s, phaseReady)
	var runs atomic.Int32
	runs.Store(1)
	s.runs = func() int { return int(runs.Load()) }
	if !s.BeforeQuit() || f.restarts.Load() != 0 {
		t.Fatal("update installed while a run is active")
	}
	runs.Store(0)
	if s.BeforeQuit() {
		t.Fatal("quit not held back for the hand-off")
	}
	for end := time.Now().Add(3 * time.Second); f.restarts.Load() == 0 && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
	}
	if f.restarts.Load() != 1 {
		t.Fatalf("restarts %d", f.restarts.Load())
	}
	if !s.BeforeQuit() {
		t.Fatal("the quit caused by Restart was held back again")
	}
}
