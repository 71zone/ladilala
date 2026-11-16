package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/profile"
)

// Shell is the Go service the UI calls. Methods are bound as main.Shell.<Name>.
type Shell struct {
	app     *application.App
	started time.Time
	dataDir string

	mu       sync.Mutex
	launcher *driver.Launcher
	sessions []*driver.StockSession
	page     *http.Server
	pageURL  string
	stopTick chan struct{}
}

func newShell(started time.Time, dataDir string) *Shell {
	return &Shell{started: started, dataDir: dataDir, stopTick: make(chan struct{})}
}

// VersionInfo describes the running build.
type VersionInfo struct {
	App      string `json:"app"`
	Go       string `json:"go"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	WebView2 string `json:"webview2"`
	PID      int    `json:"pid"`
}

// Metrics are startup numbers measured against the process creation time.
type Metrics struct {
	FirstPaintMs float64 `json:"firstPaintMs"`
}

// LaunchResult describes the stock browser the app started.
type LaunchResult struct {
	Brand   string `json:"brand"`
	Path    string `json:"path"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
	Title   string `json:"title"`
	Hash    string `json:"hash"`
}

// UpdateInfo is the result of an update check.
type UpdateInfo struct {
	Current   string `json:"current"`
	Available bool   `json:"available"`
	Version   string `json:"version"`
	Notes     string `json:"notes"`
}

// ServiceStartup starts the one-second tick event the UI subscribes to.
func (s *Shell) ServiceStartup(context.Context, application.ServiceOptions) error {
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for n := 1; ; n++ {
			select {
			case <-s.stopTick:
				return
			case <-t.C:
				if s.app != nil {
					s.app.Event.Emit("gate:tick", n)
				}
			}
		}
	}()
	return nil
}

// ServiceShutdown stops browsers. Closing the launcher closes its Job object,
// which also kills any browser a graceful close missed.
func (s *Shell) ServiceShutdown() error {
	close(s.stopTick)
	return s.CloseBrowsers()
}

func (s *Shell) Ping() string { return "pong" }

func (s *Shell) Version() VersionInfo {
	return VersionInfo{
		App: version, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		WebView2: webView2Version(), PID: os.Getpid(),
	}
}

// Ready takes the first-paint time from the page (epoch milliseconds) and
// returns the cold start: process creation to first paint.
func (s *Shell) Ready(firstPaintEpochMs float64) Metrics {
	m := Metrics{FirstPaintMs: firstPaintEpochMs - float64(s.started.UnixMilli())}
	if os.Getenv("LADILALA_GATE_METRICS") != "" {
		if f, err := os.OpenFile(filepath.Join(s.dataDir, "startup.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "%s first paint %.0f ms\n", time.Now().Format(time.RFC3339), m.FirstPaintMs)
			f.Close()
		}
	}
	return m
}

// Autorun reports whether the gate self-test was requested for this run.
func (s *Shell) Autorun() bool { return os.Getenv("LADILALA_GATE_AUTORUN") != "" }

// AutoInstall reports whether the self-test must install a found update.
func (s *Shell) AutoInstall() bool { return os.Getenv("LADILALA_GATE_AUTORUN") == "update" }

// WriteReport stores the self-test result next to the user data.
func (s *Shell) WriteReport(body string) error {
	return os.WriteFile(filepath.Join(s.dataDir, "gate-report.json"), []byte(body), 0o600)
}

// LaunchChrome starts the machine's stock Chrome (or Edge) with a fresh
// profile, opens a local page and reads its title back over CDP.
func (s *Shell) LaunchChrome() (LaunchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.launcher == nil {
		l, err := driver.NewLauncher()
		if err != nil {
			return LaunchResult{}, err
		}
		s.launcher = l
	}
	if s.page == nil {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return LaunchResult{}, err
		}
		s.page = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "<!doctype html><title>Ladilala gate page</title><h1>Launched from the Wails shell</h1>")
		})}
		s.pageURL = "http://" + ln.Addr().String()
		go s.page.Serve(ln)
	}
	installs, _ := driver.StockChrome{}.Detect()
	if len(installs) == 0 {
		return LaunchResult{}, errors.New("no Chrome or Edge is installed")
	}
	in := installs[0]
	fp := profile.NewFingerprint(7, profile.Options{Platform: profile.PlatformWindows, Language: "en-US", Timezone: "Asia/Ho_Chi_Minh"})
	p := profile.Profile{
		ID:          fmt.Sprintf("gate-%d", time.Now().UnixNano()),
		DataDir:     filepath.Join(s.dataDir, "gate-profile", fmt.Sprint(time.Now().UnixNano())),
		Fingerprint: fp,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sess, res, err := s.launcher.LaunchStock(context.Background(), driver.StockChrome{}, in, p, "")
	if err != nil {
		return LaunchResult{}, err
	}
	s.sessions = append(s.sessions, sess)
	pg, err := sess.WaitPage(ctx)
	if err != nil {
		return LaunchResult{}, err
	}
	if err := pg.Navigate(ctx, s.pageURL); err != nil {
		return LaunchResult{}, err
	}
	var title string
	if err := pg.Eval(ctx, "document.title", &title); err != nil {
		return LaunchResult{}, err
	}
	ver, _ := driver.StockChrome{}.Version(in)
	return LaunchResult{Brand: string(in.Brand), Path: in.Path, Version: ver, PID: sess.PID, Title: title, Hash: res.Hash}, nil
}

// CloseBrowsers closes every browser the app started, then the Job object.
func (s *Shell) CloseBrowsers() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for _, sess := range s.sessions {
		errs = append(errs, sess.Close())
	}
	s.sessions = nil
	if s.launcher != nil {
		errs = append(errs, s.launcher.Close())
		s.launcher = nil
	}
	if s.page != nil {
		_ = s.page.Close()
		s.page = nil
	}
	return errors.Join(errs...)
}

// CheckUpdate asks the update feed whether a newer version exists.
func (s *Shell) CheckUpdate() (UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rel, err := s.app.Updater.Check(ctx)
	if err != nil {
		return UpdateInfo{Current: version}, err
	}
	if rel == nil {
		return UpdateInfo{Current: version}, nil
	}
	return UpdateInfo{Current: version, Available: true, Version: rel.Version, Notes: rel.Notes}, nil
}

// InstallUpdate downloads and verifies the update, then swaps and restarts.
func (s *Shell) InstallUpdate() error {
	ctx := context.Background()
	if err := s.app.Updater.DownloadAndInstall(ctx); err != nil {
		return err
	}
	return s.app.Updater.Restart(ctx)
}
