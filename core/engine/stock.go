package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/player"
	"github.com/8bu/ladilala/core/profile"
	"github.com/8bu/ladilala/core/proxy"
)

// StockLauncher starts the vendor Chrome (or Edge) through core/driver's stock
// path: raw CDP, no Runtime.enable, injector on every page. A profile with a
// proxy gets a local relay that holds the credentials.
type StockLauncher struct {
	Launcher *driver.Launcher
	// Brand is driver.BrandChrome (default) or driver.BrandEdge.
	Brand string
	// Unverified skips the canvas and hardware check after launch.
	Unverified bool

	mu       sync.Mutex
	override *driver.Install
	pick     func(profile.Profile) *driver.Install
}

// SetPicker sets a function that may choose the browser for one profile. When
// it returns nil, the launcher uses UseInstall or the stock browser of Brand.
func (s *StockLauncher) SetPicker(pick func(profile.Profile) *driver.Install) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pick = pick
}

// UseInstall makes every later launch use in, whatever its brand. Pass nil to
// go back to detecting the stock browser of Brand. A running browser is not
// affected.
func (s *StockLauncher) UseInstall(in *driver.Install) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.override = in
}

// UseBrand sets Brand under the launcher lock.
func (s *StockLauncher) UseBrand(brand string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Brand = brand
}

// NewStockLauncher creates a StockLauncher with its own driver.Launcher (one
// Job object for all browsers of the app).
func NewStockLauncher() (*StockLauncher, error) {
	l, err := driver.NewLauncher()
	if err != nil {
		return nil, err
	}
	return &StockLauncher{Launcher: l}, nil
}

// Close kills the browsers that are still running (Windows Job object).
func (s *StockLauncher) Close() error { return s.Launcher.Close() }

func (s *StockLauncher) install(p profile.Profile) (driver.Install, error) {
	s.mu.Lock()
	override, brand, pick := s.override, s.Brand, s.pick
	s.mu.Unlock()
	if pick != nil {
		if in := pick(p); in != nil {
			return *in, nil
		}
	}
	if override != nil {
		return *override, nil
	}
	installs, err := driver.StockChrome{}.Detect()
	if err != nil {
		return driver.Install{}, err
	}
	want := brand
	if want == "" {
		want = driver.BrandChrome
	}
	for _, in := range installs {
		if in.Brand == want {
			return in, nil
		}
	}
	return driver.Install{}, fmt.Errorf("%w: no %s install", driver.ErrBinaryNotFound, want)
}

// Launch starts a browser for p.
func (s *StockLauncher) Launch(ctx context.Context, p profile.Profile) (Browser, error) {
	in, err := s.install(p)
	if err != nil {
		return nil, err
	}
	rctx, rcancel := context.WithCancel(context.Background())
	var relay *proxy.Relay
	addr := ""
	if p.Proxy.Scheme != profile.ProxyNone {
		if relay, err = proxy.New(p.Proxy); err != nil {
			rcancel()
			return nil, err
		}
		if addr, err = relay.Start(rctx); err != nil {
			rcancel()
			return nil, err
		}
	}
	cleanup := func() {
		if relay != nil {
			relay.Close()
		}
		rcancel()
	}
	var sess *driver.StockSession
	if in.FPC {
		// fingerprint-chromium spoofs inside the engine: no injector, no
		// canvas verification against the injector's config, but a self-check
		// that the build survives a canvas and WebGL read.
		var rep driver.FPCReport
		sess, rep, err = s.Launcher.LaunchFPC(ctx, in, p, addr)
		for _, o := range rep.Oddities {
			slog.Warn("fingerprint-chromium: known engine oddity", "profile", p.ID, "oddity", o)
		}
	} else if s.Unverified {
		sess, err = s.Launcher.LaunchStockUnverified(ctx, driver.StockChrome{}, in, p, addr)
	} else {
		sess, _, err = s.Launcher.LaunchStock(ctx, driver.StockChrome{}, in, p, addr)
	}
	if err != nil {
		cleanup()
		return nil, err
	}
	if _, err := sess.WaitPage(ctx); err != nil { // keep the first tab: closing the last tab ends the browser
		_ = sess.Close()
		cleanup()
		return nil, err
	}
	return &stockBrowser{sess: sess, cleanup: cleanup}, nil
}

type stockBrowser struct {
	sess    *driver.StockSession
	cleanup func()

	mu  sync.Mutex
	cur *driver.Page
}

func (b *stockBrowser) Done() <-chan struct{} { return b.sess.Done() }

func (b *stockBrowser) died() bool {
	select {
	case <-b.sess.Done():
		return true
	default:
		return false
	}
}

func (b *stockBrowser) PlayRow(ctx context.Context, m *macro.Macro, row map[string]string, h player.Hooks) error {
	if b.died() {
		return ErrBrowserDied
	}
	pg, err := b.sess.NewPage(ctx, "about:blank")
	if err != nil {
		if b.died() {
			return fmt.Errorf("%w: %v", ErrBrowserDied, err)
		}
		return err
	}
	b.mu.Lock()
	b.cur = pg
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.cur = nil
		b.mu.Unlock()
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = b.sess.Call(cctx, "Target.closeTarget", map[string]any{"targetId": pg.TargetID}, nil)
	}()
	p := player.New(b.sess, pg)
	p.Hooks = h
	err = p.Run(ctx, m, row)
	if err != nil && b.died() {
		return fmt.Errorf("%w: %v", ErrBrowserDied, err)
	}
	return err
}

func (b *stockBrowser) Recheck(ctx context.Context, m *macro.Macro) error {
	b.mu.Lock()
	pg := b.cur
	b.mu.Unlock()
	if pg == nil {
		return nil
	}
	return player.New(b.sess, pg).CheckGuards(ctx, m)
}

func (b *stockBrowser) Close() error {
	err := b.sess.Close()
	b.cleanup()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// Session returns the raw browser session. The profile service uses it to
// reach cookies and the first tab, and tests use it to read page values.
func (b *stockBrowser) Session() *driver.StockSession { return b.sess }
