package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/8bu/ladilala/core/profile"
)

// TestE2EEdge (spike R3) launches the installed Edge through the Launcher in
// pipe mode with its own data directory.
func TestE2EEdge(t *testing.T) {
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	installs, err := SystemChrome{}.Detect()
	if err != nil {
		t.Fatal(err)
	}
	var edge *Install
	for i := range installs {
		if installs[i].Brand == BrandEdge {
			edge = &installs[i]
			break
		}
	}
	if edge == nil {
		t.Skip("no Edge install found")
	}
	engine := SystemChrome{}

	pctx, cancel := contextWithTimeout(t, 60*time.Second)
	defer cancel()
	if err := Preflight(pctx, engine, *edge); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	l, err := NewLauncher()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	p := profile.Profile{ID: "edge", DataDir: filepath.Join(t.TempDir(), "edge-p"), Fingerprint: profile.NewFingerprint(7, profile.Options{})}
	ctx, cancel2 := contextWithTimeout(t, 90*time.Second)
	defer cancel2()
	s, err := l.Launch(ctx, engine, *edge, p, "")
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer s.Close()
	if err := chromedp.Do(s.Ctx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatal(err)
	}
	ua, err := chromedp.Run(s.Ctx, chromedp.Evaluate[string]("navigator.userAgent"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("edge pid=%d version=%s ua=%s", s.PID, mustVersion(t, engine, *edge), ua)
	if !strings.Contains(ua, "Edg/") {
		t.Errorf("user agent %q does not identify Edge", ua)
	}
}

func mustVersion(t *testing.T, e BrowserEngine, in Install) string {
	t.Helper()
	v, err := e.Version(in)
	if err != nil {
		t.Logf("version: %v", err)
	}
	return v
}

type fpSnapshot struct {
	UA, Canvas string
	Threads    float64
}

const snapshotJS = `(() => {
  const c = document.createElement('canvas');
  c.width = 220; c.height = 60;
  const x = c.getContext('2d');
  x.textBaseline = 'top'; x.font = '16px Arial';
  x.fillStyle = '#f60'; x.fillRect(10, 5, 100, 30);
  x.fillStyle = '#069'; x.fillText('ladilala fp 1.0 \u{1F600}', 4, 20);
  return {UA: navigator.userAgent, Canvas: c.toDataURL(), Threads: navigator.hardwareConcurrency};
})()`

// TestE2EFingerprintChromium (spike R8) drives the fingerprint-chromium build
// with the engine's flags. Set LADILALA_FPC_DIR to the unpacked build.
func TestE2EFingerprintChromium(t *testing.T) {
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	dir := os.Getenv("LADILALA_FPC_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "engines", "fingerprint-chromium")
	}
	engine := FingerprintChromium{SearchDirs: []string{dir}}
	installs, err := engine.Detect()
	if err != nil || len(installs) == 0 {
		t.Skipf("no fingerprint-chromium under %s (err=%v)", dir, err)
	}
	in := installs[0]
	t.Logf("binary=%s version=%s", in.Path, mustVersion(t, engine, in))

	pctx, cancel := contextWithTimeout(t, 90*time.Second)
	defer cancel()
	if err := Preflight(pctx, engine, in); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	l, err := NewLauncher()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	root := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html></html>")) }))
	defer srv.Close()
	// Hung or leaking launches are closed and retried by LaunchVerified.
	run := func(name string, seed uint64) fpSnapshot {
		t.Helper()
		fp := profile.NewFingerprint(seed, profile.Options{Platform: profile.PlatformWindows})
		args, _ := engine.Args(profile.Profile{Fingerprint: fp}, "")
		t.Logf("%s flags: %v", name, args)
		p := profile.Profile{ID: name, DataDir: filepath.Join(root, name), Fingerprint: fp}
		ctx, cancel := contextWithTimeout(t, 3*time.Minute)
		defer cancel()
		s, res, err := l.LaunchVerified(ctx, engine, in, p, "")
		if err != nil {
			t.Fatalf("launch %s: %v", name, err)
		}
		defer s.Close()
		t.Logf("%s: %d attempts, %d hangs, %d leaks", name, res.Attempts, res.Hangs, res.Leaks)
		if err := chromedp.Do(s.Ctx, chromedp.Navigate(srv.URL)); err != nil {
			t.Fatalf("navigate %s: %v", name, err)
		}
		snap, err := chromedp.Run(s.Ctx, chromedp.Evaluate[fpSnapshot](snapshotJS))
		if err != nil {
			t.Fatalf("snapshot %s: %v", name, err)
		}
		sum := sha256.Sum256([]byte(snap.Canvas))
		t.Logf("%s: ua=%s threads=%v canvas=%s", name, snap.UA, snap.Threads, hex.EncodeToString(sum[:8]))
		return snap
	}

	a1 := run("a", 111111)
	b := run("b", 222222)
	a2 := run("a", 111111)

	if a1.Canvas == b.Canvas {
		t.Error("different seeds produced the same canvas")
	}
	if a1 != a2 {
		t.Errorf("profile a is not stable across relaunch:\n%+v\n%+v", a1, a2)
	}
}
