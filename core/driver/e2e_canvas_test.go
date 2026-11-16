package driver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/profile"
)

// TestE2ECanvasVerified launches 20 fresh fingerprint-chromium browsers through
// LaunchVerified and checks that none leaks the real canvas and none stays
// hung after the bounded retry. It logs the raw hang and leak rates. Set
// LADILALA_FPC_DIR to the unpacked build.
func TestE2ECanvasVerified(t *testing.T) {
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
	l, err := NewLauncher()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	root := t.TempDir()
	ctx := context.Background()

	// The real canvas of this machine: launches with canvas spoofing off.
	real := map[string]bool{}
	var realList []string
	for i := 0; i < 6 && len(real) < 2; i++ {
		pr := Probe{Name: "real", Extra: []string{"--disable-spoofing=canvas"}, Nav: false, Wait: SettleDelay}
		r := RunProbe(ctx, l, engine, in, pr, 7, filepath.Join(root, "real", string(rune('a'+i))), "")
		if r.First != "" && !real[r.First] {
			real[r.First] = true
			realList = append(realList, r.First)
		}
	}
	t.Logf("real canvas hashes: %v", realList)

	const n = 20
	var attempts, hangs, leaks int
	for i := range n {
		p := profile.Profile{
			ID:          "v",
			DataDir:     filepath.Join(root, "p", string(rune('a'+i))),
			Fingerprint: profile.NewFingerprint(uint64(5000+i), profile.Options{Platform: profile.PlatformWindows}),
		}
		lctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		s, res, err := l.LaunchVerified(lctx, engine, in, p, "", realList...)
		attempts += res.Attempts
		hangs += res.Hangs
		leaks += res.Leaks
		if err != nil {
			cancel()
			t.Errorf("launch %d: unrecovered after %d attempts: %v", i, res.Attempts, err)
			continue
		}
		// A verified session must keep returning the same spoofed image.
		h, err := ReadCanvas(s)
		if err != nil || h != res.Hash || real[h] {
			t.Errorf("launch %d: later read %q (err %v), verified %q, real %v", i, h, err, res.Hash, real[h])
		}
		s.Close()
		cancel()
	}
	t.Logf("%d launches, %d attempts, raw hangs %d, raw leaks %d", n, attempts, hangs, leaks)
}
