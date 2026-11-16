// Package rectest holds what the recorder and player end-to-end tests and the
// recspike harness share: launching a stock browser with a hook, and a
// scripted user that drives a page with real CDP input events.
package rectest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/profile"
)

// Browser is a launched stock browser and what it needs to be cleaned up.
type Browser struct {
	Session  *driver.StockSession
	Install  driver.Install
	Launcher *driver.Launcher
	dataDir  string
}

// Close closes the browser and removes its profile directory.
func (b *Browser) Close() {
	_ = b.Session.Close()
	_ = b.Launcher.Close()
	for i := 0; i < 5; i++ {
		if os.RemoveAll(b.dataDir) == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// PickInstall finds Chrome (brand "chrome") or Edge (brand "edge").
func PickInstall(brand string) (driver.Install, error) {
	installs, err := driver.StockChrome{}.Detect()
	if err != nil {
		return driver.Install{}, err
	}
	want := driver.BrandChrome
	if brand == "edge" {
		want = driver.BrandEdge
	}
	for _, in := range installs {
		if in.Brand == want {
			return in, nil
		}
	}
	return driver.Install{}, fmt.Errorf("no %s install found", want)
}

// Launch starts a verified-free stock browser (LaunchStockUnverified: the
// injector and emulation are active) in a fresh data directory. hook may be nil.
func Launch(ctx context.Context, brand string, seed uint64, hook func(context.Context, *driver.Page) error) (*Browser, error) {
	if brand == "fpc" {
		return launchFPC(ctx, seed, hook)
	}
	in, err := PickInstall(brand)
	if err != nil {
		return nil, err
	}
	version, _ := driver.StockChrome{}.Version(in)
	plat := profile.PlatformWindows
	switch runtime.GOOS {
	case "darwin":
		plat = profile.PlatformMacOS
	case "linux":
		plat = profile.PlatformLinux
	}
	fp := profile.NewFingerprint(seed, profile.Options{Platform: plat, BrowserVersion: version})
	if in.Brand == driver.BrandEdge {
		fp.Brand = "Edge"
		fp = profile.Shuffle(fp, seed)
	}
	dir, err := os.MkdirTemp("", "ladi-rec-")
	if err != nil {
		return nil, err
	}
	p := profile.Profile{ID: "rec", DataDir: filepath.Join(dir, "p"), Fingerprint: fp}
	l, err := driver.NewLauncher()
	if err != nil {
		return nil, err
	}
	l.StockHook = hook
	s, err := l.LaunchStockUnverified(ctx, driver.StockChrome{}, in, p, "")
	if err != nil {
		_ = l.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	return &Browser{Session: s, Install: in, Launcher: l, dataDir: dir}, nil
}

// launchFPC starts the fingerprint-chromium build under LADILALA_FPC_DIR with
// its own flags and no injector (the engine spoofs by itself). The recorder
// and player run on the same CDP session as with stock Chrome.
func launchFPC(ctx context.Context, seed uint64, hook func(context.Context, *driver.Page) error) (*Browser, error) {
	fe := driver.FingerprintChromium{SearchDirs: []string{os.Getenv("LADILALA_FPC_DIR")}}
	installs, err := fe.Detect()
	if err != nil || len(installs) == 0 {
		return nil, fmt.Errorf("no fingerprint-chromium under LADILALA_FPC_DIR (%v)", err)
	}
	in := installs[0]
	version, _ := fe.Version(in)
	fp := profile.NewFingerprint(seed, profile.Options{Platform: profile.PlatformWindows, BrowserVersion: version})
	dir, err := os.MkdirTemp("", "ladi-rec-")
	if err != nil {
		return nil, err
	}
	p := profile.Profile{ID: "rec", DataDir: filepath.Join(dir, "p"), Fingerprint: fp}
	l, err := driver.NewLauncher()
	if err != nil {
		return nil, err
	}
	l.StockHook = hook
	s, err := l.LaunchStockBare(ctx, fe, in, p, "")
	if err != nil {
		_ = l.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	return &Browser{Session: s, Install: in, Launcher: l, dataDir: dir}, nil
}
