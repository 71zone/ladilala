package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/engine"
	"github.com/8bu/ladilala/core/profile"
)

// TestRealFPCInstallAndLaunch downloads the pinned fingerprint-chromium, runs
// the install job with its self-test, the doctor test launch, and one profile
// launch that reads navigator.userAgent. It needs Windows, the network and
// about 600 MB, so it runs only when LADILALA_FPC_E2E names a data folder.
func TestRealFPCInstallAndLaunch(t *testing.T) {
	dir := os.Getenv("LADILALA_FPC_E2E")
	if dir == "" {
		t.Skip("LADILALA_FPC_E2E not set")
	}
	cfg, err := openSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	l := &driver.Launcher{}
	s := newEngineService(&Backend{DataDir: dir, Settings: cfg})
	s.launcher = &engine.StockLauncher{Launcher: l}
	s.fpcOK = true
	ctx := context.Background()

	start := time.Now()
	job, err := s.InstallEngine(ctx, InstallRequest{EngineID: engineFPC})
	if err != nil {
		t.Fatal(err)
	}
	var last JobDTO
	for time.Since(start) < 15*time.Minute {
		last, _ = s.GetJob(ctx, job.ID)
		if last.State == jobDone || last.State == jobFailed {
			break
		}
		time.Sleep(time.Second)
	}
	t.Logf("install: state %s in %s, %d bytes, error %+v", last.State, time.Since(start).Round(time.Second), last.BytesDone, last.Error)
	for _, st := range last.Stages {
		t.Logf("  stage %s %s %s %v", st.Key, st.State, st.Code, st.Params)
	}
	if last.State != jobDone {
		t.Fatalf("install did not finish")
	}

	steps, err := s.TestLaunch(ctx, engineFPC)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range steps {
		t.Logf("test step %s %s %s %v", st.ID, st.State, st.Code, st.Params)
	}
	for _, st := range steps[:3] {
		if st.State != "ok" {
			t.Fatalf("test launch step %+v", st)
		}
	}

	in := s.installFor(engineFPC)
	if in == nil || !in.FPC {
		t.Fatalf("installFor = %+v", in)
	}
	sl := &engine.StockLauncher{Launcher: l}
	sl.UseInstall(in)
	p := profile.Profile{ID: "fpc-e2e", DataDir: filepath.Join(dir, "profile-e2e"), Fingerprint: profile.NewFingerprint(4242, profile.Options{Platform: profile.PlatformWindows, Language: "en-US", Timezone: "Asia/Ho_Chi_Minh"})}
	lctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	b, err := sl.Launch(lctx, p)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer b.Close()
	sess := b.(interface{ Session() *driver.StockSession }).Session()
	pg, err := sess.WaitPage(lctx)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		UA     string  `json:"ua"`
		Mem    float64 `json:"mem"`
		HW     int     `json:"hw"`
		Depth  int     `json:"depth"`
		Screen string  `json:"screen"`
	}
	if err := pg.Eval(lctx, `({ua: navigator.userAgent, mem: navigator.deviceMemory || 0, hw: navigator.hardwareConcurrency, depth: screen.colorDepth, screen: screen.width + 'x' + screen.height})`, &out); err != nil {
		t.Fatal(err)
	}
	t.Logf("page reads: %+v", out)
	if !strings.Contains(out.UA, "Chrome/148") {
		t.Fatalf("userAgent %q does not say Chrome/148", out.UA)
	}
	if rep, ok := sess.FPCReport(); ok {
		t.Logf("self-check: hash %s webgl %v oddities %v", rep.CanvasHash, rep.WebGL, rep.Oddities)
	}
}
