package driver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"

	"github.com/8bu/ladilala/core/profile"
)

// closeTimeout bounds the graceful browser shutdown before the process is
// killed.
const closeTimeout = 10 * time.Second

// Launcher starts browser processes. One Launcher serves the whole app: it
// owns the Job object that kills every browser if the app dies, and it tracks
// which data directories are open so a profile cannot be launched twice.
// The zero value works but gives no kill-on-exit guarantee on Windows.
type Launcher struct {
	Job *Job

	// StockHook, when set, runs for every page and out-of-process frame a
	// stock session attaches to, after the injector and emulation and before
	// the target resumes, so scripts added there run before the page's own.
	// An error is reported through StockSession.SetupErrors.
	StockHook func(ctx context.Context, p *Page) error

	mu   sync.Mutex
	open map[string]struct{}
}

// NewLauncher creates a Launcher with its shared Job object.
func NewLauncher() (*Launcher, error) {
	j, err := NewJob()
	if err != nil {
		return nil, err
	}
	return &Launcher{Job: j}, nil
}

// Close kills every browser the Launcher started that is still running on
// Windows (closing the Job) and is otherwise a no-op. Close sessions first
// for a graceful shutdown.
func (l *Launcher) Close() error {
	if l.Job == nil {
		return nil
	}
	return l.Job.Close()
}

// Session is one running browser. Ctx is a chromedp context bound to its
// first tab; it ends when the browser exits for any reason, including the
// user closing the window.
type Session struct {
	Ctx context.Context
	PID int

	closeOnce sync.Once
	closeErr  error
	closeFn   func() error
}

// Close shuts the browser down gracefully, then kills what is left. It is
// safe to call more than once.
func (s *Session) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.closeFn() })
	return s.closeErr
}

// Launch starts install for profile p. proxyAddr is the local relay address
// ("127.0.0.1:port"), or empty for a direct connection. The browser lives as
// long as ctx or until Session.Close.
func (l *Launcher) Launch(ctx context.Context, engine BrowserEngine, install Install, p profile.Profile, proxyAddr string) (*Session, error) {
	args, key, err := l.begin(engine, install, p, proxyAddr)
	if err != nil {
		return nil, err
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocatorOptions(install.Path, p.DataDir, args)...)
	bctx, cancelCtx := chromedp.NewContext(allocCtx)
	// An empty Do starts the browser and opens the first tab.
	if err := chromedp.Do(bctx); err != nil {
		cancelCtx()
		cancelAlloc()
		l.release(key)
		return nil, classifyStartError(err, install, p.DataDir)
	}
	proc := chromedp.FromContext(bctx).Browser.Process()
	if proc == nil {
		cancelCtx()
		cancelAlloc()
		l.release(key)
		return nil, errors.New("driver: browser started without a process handle")
	}
	pid := proc.Pid
	if l.Job != nil {
		if err := l.Job.Assign(pid); err != nil {
			cancelCtx()
			cancelAlloc()
			l.release(key)
			return nil, fmt.Errorf("driver: assign browser to job: %w", err)
		}
	}

	s := &Session{Ctx: bctx, PID: pid}
	s.closeFn = func() error {
		defer l.release(key)
		var err error
		if bctx.Err() == nil {
			// Cancel closes the browser through CDP so Chrome flushes cookies
			// and the profile before exiting.
			tctx, cancel := context.WithTimeout(bctx, closeTimeout)
			err = chromedp.Cancel(tctx)
			cancel()
		}
		cancelCtx()
		cancelAlloc()
		killTree(pid)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			err = nil
		}
		return err
	}
	// The user can close the window; free the profile and resources then.
	go func() {
		<-bctx.Done()
		_ = s.Close()
	}()
	return s, nil
}

// begin runs the checks both launch paths share and reserves the data
// directory. It returns the engine args and the reservation key; the caller
// must release the key if it does not end up with a running browser.
func (l *Launcher) begin(engine BrowserEngine, install Install, p profile.Profile, proxyAddr string) (args []string, key string, err error) {
	if err := validateDataDir(p.DataDir); err != nil {
		return nil, "", err
	}
	if st, err := os.Stat(install.Path); err != nil || !st.Mode().IsRegular() {
		return nil, "", fmt.Errorf("%w: %s", ErrBinaryNotFound, install.Path)
	}
	args, err = engine.Args(p, proxyAddr)
	if err != nil {
		return nil, "", err
	}
	if debuggingBlockedByPolicy(install.Brand) {
		return nil, "", ErrDebuggingBlocked
	}
	if err := os.MkdirAll(p.DataDir, 0o700); err != nil {
		return nil, "", fmt.Errorf("driver: create data dir: %w", err)
	}
	key = dirKey(p.DataDir)
	if !l.acquire(key) {
		return nil, "", fmt.Errorf("%w: %s", ErrProfileLocked, p.DataDir)
	}
	if profileLockedOnDisk(p.DataDir) {
		l.release(key)
		return nil, "", fmt.Errorf("%w: %s", ErrProfileLocked, p.DataDir)
	}
	return args, key, nil
}

func (l *Launcher) acquire(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open == nil {
		l.open = map[string]struct{}{}
	}
	if _, busy := l.open[key]; busy {
		return false
	}
	l.open[key] = struct{}{}
	return true
}

func (l *Launcher) release(key string) {
	l.mu.Lock()
	delete(l.open, key)
	l.mu.Unlock()
}

func dirKey(dir string) string {
	k := filepath.Clean(dir)
	if caseInsensitiveFS(runtime.GOOS) {
		k = strings.ToLower(k)
	}
	return k
}

// flag is one browser switch; a false value removes a chromedp default.
type flag struct {
	name  string
	value any
}

// launchFlags are applied after chromedp's defaults. The engine's own args go
// last so they win over the launcher's.
func launchFlags(dataDir string, engineArgs []string) []flag {
	fl := []flag{
		// Automation tells and headless leftovers from chromedp's defaults.
		// popup blocking and the forced sRGB profile differ from a real
		// desktop browser, so they go too.
		{"headless", false}, {"hide-scrollbars", false}, {"mute-audio", false},
		{"enable-automation", false}, {"disable-extensions", false},
		{"disable-popup-blocking", false}, {"force-color-profile", false},
		{"enable-features", false}, {"start-maximized", false},
		{"disable-features", "Translate"},

		{"no-first-run", true}, {"no-default-browser-check", true},
		// Two spellings: the first is older Chrome, the second is 120+.
		{"disable-session-crashed-bubble", true}, {"hide-crash-restore-bubble", true},
		{"disable-search-engine-choice-screen", true},
		{"user-data-dir", dataDir},
	}
	for _, a := range engineArgs {
		name, val, hasVal := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		if hasVal {
			fl = append(fl, flag{name, val})
		} else {
			fl = append(fl, flag{name, true})
		}
	}
	return fl
}

// allocatorOptions starts from chromedp's defaults, switches to a visible
// window, and applies launchFlags. Pipe mode (the chromedp default) is kept:
// it needs no debugging port, so nothing local can attach to the browser.
func allocatorOptions(execPath, dataDir string, engineArgs []string) []chromedp.ExecAllocatorOption {
	opts := slices.Clone(chromedp.DefaultExecAllocatorOptions[:])
	opts = append(opts,
		chromedp.ExecPath(execPath),
		chromedp.VisibleWindow,
		chromedp.ModifyCmdFunc(configureCmd),
	)
	for _, f := range launchFlags(dataDir, engineArgs) {
		opts = append(opts, chromedp.Flag(f.name, f.value))
	}
	return opts
}

func classifyStartError(err error, install Install, dataDir string) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%w: %s (%v)", ErrBinaryNotFound, install.Path, err)
	case debuggingBlockedByPolicy(install.Brand):
		return fmt.Errorf("%w (%v)", ErrDebuggingBlocked, err)
	case profileLockedOnDisk(dataDir):
		return fmt.Errorf("%w: %s (%v)", ErrProfileLocked, dataDir, err)
	}
	return fmt.Errorf("driver: start browser: %w", err)
}

// DebuggingBlockedByPolicy reports whether an enterprise policy forbids remote
// debugging for the browser brand on this machine. It is always false outside
// Windows.
func DebuggingBlockedByPolicy(brand string) bool { return debuggingBlockedByPolicy(brand) }

// Preflight launches the install with a throwaway profile, asks it for its
// version over CDP, and closes it. It tells the user before the first real
// launch whether this machine can drive the browser at all.
func Preflight(ctx context.Context, engine BrowserEngine, install Install) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	dir, err := os.MkdirTemp("", "ladilala-preflight-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	l := &Launcher{}
	s, err := l.Launch(ctx, engine, install, profile.Profile{
		ID:          "preflight",
		DataDir:     dir,
		Fingerprint: profile.Fingerprint{Seed: 1},
	}, "")
	if err != nil {
		return err
	}
	defer s.Close()
	if _, err := chromedp.CallBrowser(s.Ctx, browser.GetVersion, cdp.Empty{}); err != nil {
		return fmt.Errorf("driver: browser did not answer over CDP: %w", err)
	}
	return nil
}
