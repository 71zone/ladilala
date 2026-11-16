// Command winspike launches one browser through driver.Launcher, prints its
// PID and profile directory, and blocks. It exists only for the Windows spike
// tests in core/driver.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/profile"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "winspike:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "canvasmatrix":
			return runCanvasMatrix(os.Args[2:])
		case "stockdetect":
			return runStockDetect(os.Args[2:])
		case "stocklaunches":
			return runStockLaunches(os.Args[2:])
		case "cft":
			return runCfT(os.Args[2:])
		}
	}
	engineName := flag.String("engine", "chrome", "chrome | edge | fpc")
	dataDir := flag.String("datadir", "", "absolute profile data directory")
	seed := flag.Uint64("seed", 1, "fingerprint seed")
	engineDir := flag.String("enginedir", "", "directory holding the fingerprint-chromium build (fpc only)")
	flag.Parse()

	var engine driver.BrowserEngine = driver.SystemChrome{}
	var install driver.Install
	switch *engineName {
	case "chrome", "edge":
		installs, err := driver.SystemChrome{}.Detect()
		if err != nil {
			return err
		}
		want := driver.BrandChrome
		if *engineName == "edge" {
			want = driver.BrandEdge
		}
		found := false
		for _, in := range installs {
			if in.Brand == want {
				install, found = in, true
				break
			}
		}
		if !found {
			return fmt.Errorf("no %s install found", want)
		}
	case "fpc":
		fe := driver.FingerprintChromium{SearchDirs: []string{*engineDir}}
		installs, err := fe.Detect()
		if err != nil {
			return err
		}
		if len(installs) == 0 {
			return fmt.Errorf("no fingerprint-chromium under %q", *engineDir)
		}
		engine, install = fe, installs[0]
	default:
		return fmt.Errorf("unknown engine %q", *engineName)
	}

	l, err := driver.NewLauncher()
	if err != nil {
		return err
	}
	defer l.Close()

	p := profile.Profile{
		ID:          "winspike",
		DataDir:     *dataDir,
		Fingerprint: profile.NewFingerprint(*seed, profile.Options{Platform: profile.PlatformWindows}),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s, err := l.Launch(ctx, engine, install, p, "")
	if err != nil {
		return err
	}
	defer s.Close()
	fmt.Printf("PID=%d\n", s.PID)
	fmt.Printf("PROFILE=%s\n", *dataDir)
	select {
	case <-ctx.Done():
	case <-s.Ctx.Done():
	}
	return nil
}
