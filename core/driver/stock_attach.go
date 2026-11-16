package driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	jsonv2 "github.com/chromedp/cdproto/cdp/jsonv2"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/8bu/ladilala/core/inject"
)

const attachTimeout = 15 * time.Second

// onEvent runs on the read loop; attach handling sends commands, so it moves
// to its own goroutine.
func (s *StockSession) onEvent(parent, method string, params []byte) {
	switch method {
	case "Target.attachedToTarget":
		var ev target.EventAttachedToTarget
		if jsonv2.Unmarshal(params, &ev, chromedp.DefaultUnmarshalOptions) == nil && ev.TargetInfo != nil {
			go s.attach(parent, ev)
		}
	case "Target.detachedFromTarget":
		var ev struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(params, &ev) == nil {
			s.forget(ev.SessionID)
		}
	}
}

// attach prepares one new target and then lets it run. The target is paused
// (waitForDebuggerOnStart) until runIfWaitingForDebugger, so the injector and
// the emulation overrides are in place before its first script. Runtime.enable
// is never sent; runIfWaitingForDebugger and Runtime.evaluate do not need it.
func (s *StockSession) attach(parent string, ev target.EventAttachedToTarget) {
	ctx, cancel := context.WithTimeout(s.ctx, attachTimeout)
	defer cancel()
	sess := s.conn.session(string(ev.SessionID))
	var err error
	pg := &Page{SessionID: string(ev.SessionID), TargetID: string(ev.TargetInfo.TargetID), Type: ev.TargetInfo.Type, ParentSessionID: parent, s: s}
	switch ev.TargetInfo.Type {
	case "page", "iframe":
		err = s.preparePage(ctx, sess, ev.TargetInfo.Type == "iframe")
		if err == nil && s.hook != nil {
			err = s.hook(ctx, pg)
		}
	case "worker", "shared_worker", "service_worker":
		err = s.prepareWorker(ctx, sess)
	}
	if ev.WaitingForDebugger {
		if _, rerr := cdp.Call(ctx, sess, runtime.RunIfWaitingForDebugger, cdp.Empty{}); rerr != nil && err == nil {
			err = fmt.Errorf("resume %s: %w", ev.TargetInfo.Type, rerr)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.setupEr = append(s.setupEr, fmt.Errorf("attach %s %s: %w", ev.TargetInfo.Type, ev.TargetInfo.URL, err))
	}
	if t := ev.TargetInfo.Type; t == "page" || t == "iframe" {
		s.pages[pg.SessionID] = pg
		s.byTgt[pg.TargetID] = pg
		close(s.changed)
		s.changed = make(chan struct{})
	}
}

func (s *StockSession) autoAttach(ctx context.Context, sess rawSession) error {
	_, err := cdp.Call(ctx, sess, target.SetAutoAttach, target.SetAutoAttachParams{
		AutoAttach: true, WaitForDebuggerOnStart: true, Flatten: ptr(true),
	})
	return err
}

func (s *StockSession) preparePage(ctx context.Context, sess rawSession, oopif bool) error {
	// Page.enable is required before addScriptToEvaluateOnNewDocument takes
	// effect (measured), and does not trigger the Runtime.enable tell.
	if _, err := cdp.Call(ctx, sess, page.Enable, page.EnableParams{}); err != nil {
		return err
	}
	if s.script != "" {
		if _, err := cdp.Call(ctx, sess, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{
			Source: s.script, IncludeCommandLineAPI: ptr(false), RunImmediately: ptr(true),
		}); err != nil {
			return err
		}
	}
	for _, st := range s.steps {
		if err := st(ctx, sess); err != nil {
			// An out-of-process frame follows its embedding tab's metrics; the
			// browser refuses device metrics there (measured, Chrome 157).
			if oopif && strings.Contains(err.Error(), "top-level targets") {
				continue
			}
			return err
		}
	}
	return s.autoAttach(ctx, sess)
}

func (s *StockSession) prepareWorker(ctx context.Context, sess rawSession) error {
	if s.script == "" {
		return nil
	}
	res, err := cdp.Call(ctx, sess, runtime.Evaluate, runtime.EvaluateParams{Expression: s.script, Silent: ptr(true)})
	if err != nil {
		return err
	}
	if res.ExceptionDetails != nil {
		return fmt.Errorf("injector threw in worker: %s", exceptionText(res.ExceptionDetails))
	}
	_ = s.autoAttach(ctx, sess) // nested workers; not every worker type supports it
	return nil
}

func (s *StockSession) forget(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pg := s.pages[sessionID]; pg != nil {
		delete(s.pages, sessionID)
		delete(s.byTgt, pg.TargetID)
	}
}

// waitPage blocks until a page satisfies pred.
func (s *StockSession) waitPage(ctx context.Context, pred func(*Page) bool) (*Page, error) {
	for {
		s.mu.Lock()
		for _, pg := range s.pages {
			if pred(pg) {
				s.mu.Unlock()
				return pg, nil
			}
		}
		ch := s.changed
		s.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.exited:
			return nil, errors.New("driver: browser exited")
		}
	}
}

// WaitPage returns the first tab, waiting for the browser to open it.
func (s *StockSession) WaitPage(ctx context.Context) (*Page, error) {
	return s.waitPage(ctx, func(pg *Page) bool { return pg.Type == "page" })
}

// NewPage opens a tab on url and waits until its load event fired.
func (s *StockSession) NewPage(ctx context.Context, url string) (*Page, error) {
	res, err := cdp.Call(ctx, s.conn.session(""), target.CreateTarget, target.CreateTargetParams{URL: "about:blank"})
	if err != nil {
		return nil, err
	}
	pg, err := s.waitPage(ctx, func(pg *Page) bool { return pg.TargetID == string(res.TargetID) })
	if err != nil {
		return nil, err
	}
	if url != "" && url != "about:blank" {
		if err := pg.Navigate(ctx, url); err != nil {
			return nil, err
		}
	}
	return pg, nil
}

func (p *Page) sess() rawSession { return p.s.conn.session(p.SessionID) }

// Navigate loads url and waits for the load event.
func (p *Page) Navigate(ctx context.Context, url string) error {
	loaded, stop := p.sess().Subscribe("Page.loadEventFired")
	defer stop()
	res, err := cdp.Call(ctx, p.sess(), page.Navigate, page.NavigateParams{URL: url})
	if err != nil {
		return err
	}
	if res.ErrorText != "" {
		return fmt.Errorf("driver: navigate %s: %s", url, res.ErrorText)
	}
	select {
	case <-loaded:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Eval runs expr in the page's main world with Runtime.evaluate (no
// Runtime.enable), awaits a returned promise and decodes the JSON value into
// out (nil to discard).
func (p *Page) Eval(ctx context.Context, expr string, out any) error {
	res, err := cdp.Call(ctx, p.sess(), runtime.Evaluate, runtime.EvaluateParams{
		Expression: expr, ReturnByValue: ptr(true), AwaitPromise: ptr(true),
	})
	if err != nil {
		return err
	}
	if res.ExceptionDetails != nil {
		return fmt.Errorf("driver: eval: %s", exceptionText(res.ExceptionDetails))
	}
	if out == nil || res.Result == nil || len(res.Result.Value) == 0 {
		return nil
	}
	return json.Unmarshal(res.Result.Value, out)
}

// deviceMemory is undefined on about:blank (insecure context), so a zero
// reading is not a mismatch; the E2E test checks it on an http page.

// identityJS reads what a page sees about the profile.
const identityJS = `(() => {
  let gpu = '';
  try {
    const g = document.createElement('canvas').getContext('webgl');
    const e = g.getExtension('WEBGL_debug_renderer_info');
    gpu = g.getParameter(e.UNMASKED_RENDERER_WEBGL);
  } catch (_) {}
  return { hw: navigator.hardwareConcurrency, mem: navigator.deviceMemory, plat: navigator.platform,
           lang: navigator.language, gpu };
})()`

// Verify checks that the injector took effect in the first tab: hardware,
// platform, language and GPU follow the profile, and the canvas reads back
// identical stableReads times. It returns the canvas hash.
func (s *StockSession) Verify(ctx context.Context, real ...string) (string, error) {
	vctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pg, err := s.WaitPage(vctx)
	if err != nil {
		return "", err
	}
	if errs := s.SetupErrors(); len(errs) > 0 {
		return "", fmt.Errorf("driver: injector setup failed: %w", errors.Join(errs...))
	}
	cfg := inject.NewConfig(s.fp)
	var id struct {
		HW   int    `json:"hw"`
		Mem  int    `json:"mem"`
		Plat string `json:"plat"`
		Lang string `json:"lang"`
		GPU  string `json:"gpu"`
	}
	if err := pg.Eval(vctx, identityJS, &id); err != nil {
		return "", err
	}
	if id.HW != cfg.HardwareThreads || (id.Mem != 0 && id.Mem != cfg.DeviceMemory) || id.Plat != cfg.NavigatorPlatform || id.Lang != cfg.Languages[0] ||
		(cfg.WebGL && id.GPU != "" && id.GPU != cfg.GPURenderer) {
		return "", fmt.Errorf("%w: page reports %+v, profile wants %+v", ErrCanvasLeak, id, cfg)
	}
	// The first canvas read of a fresh browser process can return garbage even
	// without an injector (measured on Windows Chrome 157 and Edge 157: a
	// different large image per launch, then a stable one). Discard one read.
	if _, err := s.readCanvas(vctx, pg); err != nil {
		return "", err
	}
	time.Sleep(300 * time.Millisecond)
	var first string
	for i := range stableReads {
		h, err := s.readCanvas(vctx, pg)
		if err != nil {
			return "", err
		}
		if i == 0 {
			first = h
		} else if h != first {
			return "", fmt.Errorf("%w: %s then %s", ErrCanvasLeak, first, h)
		}
	}
	for _, r := range real {
		if r == first {
			return "", fmt.Errorf("%w: %s is the real canvas", ErrCanvasLeak, first)
		}
	}
	return first, nil
}

func (s *StockSession) readCanvas(ctx context.Context, pg *Page) (string, error) {
	rctx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	var url string
	if err := pg.Eval(rctx, CanvasJS, &url); err != nil {
		if rctx.Err() != nil && ctx.Err() == nil {
			return "", ErrCanvasHang
		}
		return "", err
	}
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:8]), nil
}

func exceptionText(d *runtime.ExceptionDetails) string {
	if d.Exception != nil && d.Exception.Description != "" {
		return d.Exception.Description
	}
	return d.Text
}

// Screenshot saves a PNG of the page to path.
func (p *Page) Screenshot(ctx context.Context, path string) error {
	res, err := cdp.Call(ctx, p.sess(), page.CaptureScreenshot, page.CaptureScreenshotParams{})
	if err != nil {
		return err
	}
	return os.WriteFile(path, res.Data, 0o644)
}

// Call sends one command on this target's CDP session.
func (p *Page) Call(ctx context.Context, method string, params, res any) error {
	return p.sess().Call(ctx, method, params, res)
}

// Subscribe returns a channel of the raw params of every event method this
// target's session emits, and a function that cancels the subscription.
func (p *Page) Subscribe(method string) (<-chan jsonv2.Value, func()) {
	return p.sess().Subscribe(method)
}

// Pages returns the tabs and out-of-process frames attached so far.
func (s *StockSession) Pages() []*Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Page, 0, len(s.pages))
	for _, pg := range s.pages {
		out = append(out, pg)
	}
	return out
}
