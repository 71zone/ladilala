package driver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/8bu/ladilala/core/profile"
)

// TestE2ESystemChrome launches the real browser installed on this machine. It
// opens a visible window, so it only runs on request: LADILALA_E2E=1.
func TestE2ESystemChrome(t *testing.T) {
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	installs, err := SystemChrome{}.Detect()
	if err != nil || len(installs) == 0 {
		t.Skip("no system Chrome or Edge found")
	}
	in := installs[0]
	engine := SystemChrome{}

	ctxPre, cancel := contextWithTimeout(t, 60*time.Second)
	defer cancel()
	if err := Preflight(ctxPre, engine, in); err != nil {
		t.Fatalf("preflight: %v", err)
	}

	l, err := NewLauncher()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	fp := profile.NewFingerprint(42, profile.Options{Platform: profile.PlatformWindows, Language: "de-DE", Timezone: "Europe/Berlin"})
	p := profile.Profile{ID: "e2e", DataDir: filepath.Join(t.TempDir(), "p1"), Fingerprint: fp}

	ctx, cancel2 := contextWithTimeout(t, 90*time.Second)
	defer cancel2()
	s, err := l.Launch(ctx, engine, in, p, "")
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if s.PID == 0 {
		t.Fatal("no pid")
	}
	if _, err := l.Launch(ctx, engine, in, p, ""); err == nil {
		t.Fatal("same profile launched twice")
	}

	if err := chromedp.Do(s.Ctx, EmulationPlan(p)...); err != nil {
		t.Fatalf("emulation: %v", err)
	}
	if err := chromedp.Do(s.Ctx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatal(err)
	}
	checks := map[string]any{
		"navigator.userAgent":                              fp.UserAgent,
		"navigator.platform":                               "Win32",
		"Intl.DateTimeFormat().resolvedOptions().timeZone": fp.Timezone,
		"navigator.hardwareConcurrency":                    float64(fp.HardwareThreads),
		"screen.width":                                     float64(fp.ScreenWidth),
		"screen.height":                                    float64(fp.ScreenHeight),
	}
	for expr, want := range checks {
		got, err := chromedp.Run(s.Ctx, chromedp.Evaluate[any](expr))
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if got != want {
			t.Errorf("%s = %v, want %v", expr, got, want)
		}
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// The profile is free again after Close.
	s2, err := l.Launch(ctx, engine, in, p, "")
	if err != nil {
		t.Fatalf("relaunch: %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}
}
