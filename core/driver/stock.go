package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/chromedp/cdproto/target"

	"github.com/8bu/ladilala/core/inject"
	"github.com/8bu/ladilala/core/profile"
)

// StockChrome is tier 1: the vendor-signed Chrome (then Edge) installed on
// the machine, driven over --remote-debugging-pipe with the profile's own
// user-data-dir. Detection and flags are SystemChrome's; what differs is the
// session: LaunchStock attaches with the raw CDP layer, never sends
// Runtime.enable, and injects the profile's script into every page, frame and
// worker before their first script runs.
type StockChrome struct{ SystemChrome }

func (StockChrome) Name() string { return "stock-chrome" }

func ptr[T any](v T) *T { return &v }

// StockSession is one running stock browser. It has no chromedp context on
// purpose: chromedp enables the Runtime domain on every target it attaches.
type StockSession struct {
	PID int

	fp     profile.Fingerprint
	conn   *rawConn
	script string
	steps  []EmulationStep
	ctx    context.Context
	stop   context.CancelFunc

	mu      sync.Mutex
	pages   map[string]*Page // by CDP session ID
	byTgt   map[string]*Page // by target ID
	changed chan struct{}    // closed and replaced whenever pages changes
	setupEr []error

	hook func(ctx context.Context, p *Page) error

	exited    chan struct{}
	closeOnce sync.Once
	closeErr  error
	closeFn   func() error
}

// Page is one tab (or out-of-process frame) the session is attached to.
type Page struct {
	SessionID string
	TargetID  string // for pages and out-of-process frames this is also the main frame ID
	Type      string // "page" or "iframe" (out-of-process frame)
	// ParentSessionID is the session that auto-attached this target: the
	// embedding page for an out-of-process frame, empty for tabs.
	ParentSessionID string
	s               *StockSession
}

// Done is closed when the browser process has exited.
func (s *StockSession) Done() <-chan struct{} { return s.exited }

// SetupErrors returns the errors from attaching to targets so far. Verify
// treats any as fatal; they never block the browser.
func (s *StockSession) SetupErrors() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]error(nil), s.setupEr...)
}

// SentMethods returns how often each CDP command was sent on this session.
// Tests use it to prove Runtime.enable never goes out.
func (s *StockSession) SentMethods() map[string]int { return s.conn.sentMethods() }

// Close shuts the browser down gracefully, then kills what is left.
func (s *StockSession) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.closeFn() })
	return s.closeErr
}

// stockArgs turns the launcher flags into a plain command line. Unlike
// chromedp there are no defaults to remove, so false flags are dropped.
func stockArgs(dataDir string, engineArgs []string) []string {
	var args []string
	for _, f := range launchFlags(dataDir, engineArgs) {
		switch v := f.value.(type) {
		case bool:
			if v {
				args = append(args, "--"+f.name)
			}
		case string:
			args = append(args, "--"+f.name+"="+v)
		}
	}
	return append(args, "--remote-debugging-pipe", "about:blank")
}

// LaunchStock starts install for profile p, attaches the injector and returns
// a verified session. The canvas must read back identical three times and the
// page must report the profile's hardware and GPU; otherwise the browser is
// closed and relaunched, at most MaxVerifyAttempts launches in total.
func (l *Launcher) LaunchStock(ctx context.Context, engine BrowserEngine, install Install, p profile.Profile, proxyAddr string) (*StockSession, VerifyResult, error) {
	var res VerifyResult
	var last error
	for res.Attempts < MaxVerifyAttempts {
		res.Attempts++
		s, err := l.launchStockOnce(ctx, engine, install, p, proxyAddr, false)
		if err != nil {
			return nil, res, err
		}
		h, err := s.Verify(ctx)
		if err == nil {
			res.Hash = h
			return s, res, nil
		}
		_ = s.Close()
		last = err
		switch {
		case errors.Is(err, ErrCanvasHang):
			res.Hangs++
		case errors.Is(err, ErrCanvasLeak):
			res.Leaks++
		default:
			return nil, res, err
		}
		if ctx.Err() != nil {
			return nil, res, ctx.Err()
		}
	}
	return nil, res, fmt.Errorf("driver: stock browser not verified after %d launches: %w", res.Attempts, last)
}

// LaunchStockUnverified is LaunchStock without the canvas and hardware check.
// Measurements use it to see what the injector alone does.
func (l *Launcher) LaunchStockUnverified(ctx context.Context, engine BrowserEngine, install Install, p profile.Profile, proxyAddr string) (*StockSession, error) {
	return l.launchStockOnce(ctx, engine, install, p, proxyAddr, false)
}

// LaunchStockBare attaches like LaunchStock but injects nothing and applies
// no emulation: the control for measurements.
func (l *Launcher) LaunchStockBare(ctx context.Context, engine BrowserEngine, install Install, p profile.Profile, proxyAddr string) (*StockSession, error) {
	return l.launchStockOnce(ctx, engine, install, p, proxyAddr, true)
}

// Call sends one browser-level command (no target session).
func (s *StockSession) Call(ctx context.Context, method string, params, res any) error {
	return s.conn.call(ctx, "", method, params, res)
}

func (l *Launcher) launchStockOnce(ctx context.Context, engine BrowserEngine, install Install, p profile.Profile, proxyAddr string, bare bool) (*StockSession, error) {
	if p.Fingerprint.Seed == 0 {
		return nil, ErrNoFingerprintSeed
	}
	var script string
	if !bare {
		var err error
		if script, err = inject.ScriptFor(p.Fingerprint); err != nil {
			return nil, err
		}
	}
	engineArgs, key, err := l.begin(engine, install, p, proxyAddr)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			l.release(key)
		}
	}()

	childR, parentW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	parentR, childW, err := os.Pipe()
	if err != nil {
		childR.Close()
		parentW.Close()
		return nil, err
	}
	cmd := exec.Command(install.Path, stockArgs(p.DataDir, engineArgs)...)
	configureCmd(cmd)
	closeParent := func() { parentR.Close(); parentW.Close() }
	if err := setChildPipes(cmd, childR, childW, parentR, parentW); err != nil {
		childR.Close()
		childW.Close()
		closeParent()
		return nil, err
	}
	err = cmd.Start()
	childR.Close() // the browser holds its own copies now
	childW.Close()
	if err != nil {
		closeParent()
		return nil, classifyStartError(err, install, p.DataDir)
	}
	pid := cmd.Process.Pid
	if l.Job != nil {
		if err := l.Job.Assign(pid); err != nil {
			killTree(pid)
			closeParent()
			return nil, fmt.Errorf("driver: assign browser to job: %w", err)
		}
	}

	sctx, stop := context.WithCancel(context.Background())
	s := &StockSession{
		PID: pid, fp: p.Fingerprint, script: script, steps: stepsFor(p, bare), ctx: sctx, stop: stop,
		pages: map[string]*Page{}, byTgt: map[string]*Page{}, changed: make(chan struct{}),
		exited: make(chan struct{}), hook: l.StockHook,
	}
	s.conn = newRawConn(parentR, parentW, closeFiles{parentR, parentW}, s.onEvent)
	go func() {
		_ = cmd.Wait()
		close(s.exited)
		s.conn.close()
		stop()
	}()
	s.closeFn = func() error {
		defer l.release(key)
		defer s.stop()
		if !isClosed(s.exited) {
			cctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
			_ = s.conn.call(cctx, "", "Browser.close", nil, nil)
			cancel()
			select {
			case <-s.exited:
			case <-time.After(closeTimeout):
			}
		}
		killTree(pid)
		s.conn.close()
		return nil
	}
	ok = true

	// Browser-level auto-attach: every page, worker, shared worker and
	// service worker is paused until our script ran in it.
	if err := s.conn.call(ctx, "", target.SetAutoAttach.Method, target.SetAutoAttachParams{
		AutoAttach: true, WaitForDebuggerOnStart: true, Flatten: ptr(true),
	}, nil); err != nil {
		_ = s.Close()
		return nil, classifyStartError(fmt.Errorf("driver: attach to browser: %w", err), install, p.DataDir)
	}
	return s, nil
}

type closeFiles struct{ a, b *os.File }

func (c closeFiles) Close() error { c.a.Close(); return c.b.Close() }

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func stepsFor(p profile.Profile, bare bool) []EmulationStep {
	if bare {
		return nil
	}
	return EmulationSteps(p)
}
