package driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/8bu/ladilala/core/profile"
)

// CanvasJS draws a fixed scene and returns its data URL. It is the read the
// canvas probes and VerifySpoof use.
const CanvasJS = `(() => {
  const c = document.createElement('canvas');
  c.width = 220; c.height = 60;
  const x = c.getContext('2d');
  x.textBaseline = 'top'; x.font = '16px Arial';
  x.fillStyle = '#f60'; x.fillRect(10, 5, 100, 30);
  x.fillStyle = '#069'; x.fillText('ladilala fp 1.0 \u{1F600}', 4, 20);
  return c.toDataURL();
})()`

// ReadTimeout bounds one canvas read. A fingerprint-chromium browser that
// hangs on toDataURL never answers, so every read needs a deadline.
const ReadTimeout = 3 * time.Second

// ErrCanvasHang means toDataURL did not answer within ReadTimeout.
var ErrCanvasHang = errors.New("driver: canvas read timed out")

// ReadCanvas runs CanvasJS in the session's first tab and returns the hash of
// the data URL. It returns ErrCanvasHang on timeout.
func ReadCanvas(s *Session) (string, error) {
	return readCanvasIn(s.Ctx, s.Ctx)
}

// readCanvasIn reads in the tab context tab; browser is the session context,
// used to tell a hang from a closed browser.
func readCanvasIn(browser, tab context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(tab, ReadTimeout)
	defer cancel()
	url, err := chromedp.Run(ctx, chromedp.Evaluate[string](CanvasJS))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil && browser.Err() == nil {
			return "", ErrCanvasHang
		}
		return "", err
	}
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:8]), nil
}

// extraArgsEngine appends fixed switches to another engine's args, so a probe
// can vary one flag at a time without touching the profile model.
type extraArgsEngine struct {
	BrowserEngine
	extra []string
}

func (e extraArgsEngine) Args(p profile.Profile, proxy string) ([]string, error) {
	a, err := e.BrowserEngine.Args(p, proxy)
	if err != nil {
		return nil, err
	}
	return append(a, e.extra...), nil
}

// WithExtraArgs wraps engine so every launch also gets extra switches.
func WithExtraArgs(engine BrowserEngine, extra ...string) BrowserEngine {
	return extraArgsEngine{engine, extra}
}

// Probe describes one cell of the canvas experiment matrix.
type Probe struct {
	Name   string
	Extra  []string      // extra browser switches
	Page   string        // "blank" | "http" | "data"
	Nav    bool          // navigate to Page before the first read; false reads the initial tab
	Wait   time.Duration // pause between load and first read
	NewTab bool          // after the first tab, open a second tab and read it at once
}

// ProbeResult is one fresh launch of a Probe.
type ProbeResult struct {
	Hung     bool   // first read timed out
	First    string // hash of the first read ("" if hung)
	Second   string // hash of the second read ("" if it hung)
	Relaunch string // hash of the first read after relaunching the same seed
	Tab2     string // hash of the first read in a second tab (NewTab only)
	Tab2Hung bool
	Err      error
}

// RunProbe launches a fresh browser (new data dir under root) with the given
// seed, performs the reads described by pr and closes it.
func RunProbe(ctx context.Context, l *Launcher, engine BrowserEngine, in Install, pr Probe, seed uint64, dir, httpURL string) ProbeResult {
	var r ProbeResult
	got, err := probeOnce(ctx, l, engine, in, pr, seed, dir, httpURL)
	if err != nil && !errors.Is(err, ErrCanvasHang) {
		r.Err = err
		return r
	}
	r.First, r.Second, r.Tab2, r.Tab2Hung = got.First, got.Second, got.Tab2, got.Tab2Hung
	r.Hung = errors.Is(err, ErrCanvasHang) && got.First == ""
	if !r.Hung {
		again := pr
		again.NewTab = false
		if got2, err2 := probeOnce(ctx, l, engine, in, again, seed, dir+"-relaunch", httpURL); err2 == nil {
			r.Relaunch = got2.First
		}
	}
	return r
}

// probeRead is what one launch observed.
type probeRead struct {
	First, Second, Tab2 string
	Tab2Hung            bool
}

func probeOnce(ctx context.Context, l *Launcher, engine BrowserEngine, in Install, pr Probe, seed uint64, dir, httpURL string) (probeRead, error) {
	var out probeRead
	eng := WithExtraArgs(engine, pr.Extra...)
	p := profile.Profile{
		ID:          pr.Name,
		DataDir:     dir,
		Fingerprint: profile.NewFingerprint(seed, profile.Options{Platform: profile.PlatformWindows}),
	}
	lctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	s, err := l.Launch(lctx, eng, in, p, "")
	if err != nil {
		return out, fmt.Errorf("launch: %w", err)
	}
	defer s.Close()
	if pr.Nav {
		url := "about:blank"
		switch pr.Page {
		case "http":
			url = httpURL
		case "data":
			url = "data:text/html,<html></html>"
		}
		nctx, ncancel := context.WithTimeout(s.Ctx, 15*time.Second)
		_, err := chromedp.Run(nctx, chromedp.Navigate(url))
		ncancel()
		if err != nil {
			return out, fmt.Errorf("navigate: %w", err)
		}
	}
	if pr.Wait > 0 {
		time.Sleep(pr.Wait)
	}
	if out.First, err = ReadCanvas(s); err != nil {
		return out, err
	}
	out.Second, err = ReadCanvas(s)
	if errors.Is(err, ErrCanvasHang) {
		return out, nil
	}
	if err != nil || !pr.NewTab {
		return out, err
	}
	// A second tab gets its own renderer process; read it as soon as it loads.
	tctx, tcancel := chromedp.NewContext(s.Ctx)
	defer tcancel()
	if _, err := chromedp.Run(tctx, chromedp.Navigate(httpURL)); err != nil {
		return out, fmt.Errorf("new tab: %w", err)
	}
	out.Tab2, err = readCanvasIn(s.Ctx, tctx)
	if errors.Is(err, ErrCanvasHang) {
		out.Tab2Hung, err = true, nil
	}
	return out, err
}
