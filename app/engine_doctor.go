package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/profile"
)

// Diag is the result of one doctor check. The core returns codes and params;
// the UI writes the text.
type Diag struct {
	ID     string         `json:"id"`    // browser | policy | pipe | folder | job | relay
	Level  string         `json:"level"` // ok | warn | fail
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
	// FixID is a fix the UI can offer: installCft (download Chrome for
	// Testing, which ignores the Google and Edge policy keys).
	FixID string `json:"fixId,omitempty"`
}

// RunDiagnostics runs the doctor checks one after the other. The pipe check
// starts a browser and waits at most the probe timeout (2 seconds).
func (s *EngineService) RunDiagnostics(ctx context.Context) ([]Diag, error) {
	var out []Diag
	add := func(d Diag) { out = append(out, d) }

	chrome, edge := s.detectStock()
	def := s.effectiveDefault()
	defIn := s.installFor(def)

	// browser: is there a browser to drive?
	var found []map[string]any
	for _, f := range []*stockFound{chrome, edge} {
		if f != nil {
			found = append(found, map[string]any{"path": f.in.Path, "version": f.version, "brand": f.in.Brand})
		}
	}
	if cin := s.installFor(engineCfT); cin != nil {
		found = append(found, map[string]any{"path": cin.Path, "brand": driver.BrandChromium, "version": s.readState().Active})
	}
	if m, in, ok := s.fpcInstalled(); ok && s.fpcOK {
		found = append(found, map[string]any{"path": in.Path, "brand": driver.BrandChromium, "version": m.Tag})
	}
	if len(found) == 0 {
		add(Diag{ID: "browser", Level: "fail", Code: "none", FixID: "installCft"})
	} else {
		add(Diag{ID: "browser", Level: "ok", Code: "found", Params: map[string]any{"n": len(found), "found": found}})
	}

	// policy: does an enterprise policy forbid remote debugging?
	var blocked []string
	for _, f := range []*stockFound{chrome, edge} {
		if f != nil && driver.DebuggingBlockedByPolicy(f.in.Brand) {
			blocked = append(blocked, f.in.Brand)
		}
	}
	if len(blocked) > 0 {
		add(Diag{ID: "policy", Level: "fail", Code: "blocked", Params: map[string]any{"brands": blocked}, FixID: "installCft"})
	} else {
		add(Diag{ID: "policy", Level: "ok", Code: "ok"})
	}

	// pipe: does the default browser start and answer over the CDP pipe?
	switch {
	case defIn == nil:
		add(Diag{ID: "pipe", Level: "warn", Code: "skipped"})
	default:
		pctx, cancel := context.WithTimeout(ctx, s.probeTimeout)
		start := time.Now()
		ver, err := s.probe(pctx, *defIn)
		cancel()
		switch {
		case err == nil:
			add(Diag{ID: "pipe", Level: "ok", Code: "ok", Params: map[string]any{"engine": def, "version": ver, "ms": time.Since(start).Milliseconds()}})
		case errors.Is(err, driver.ErrDebuggingBlocked):
			add(Diag{ID: "pipe", Level: "fail", Code: "blocked", Params: map[string]any{"engine": def}, FixID: "installCft"})
		default:
			code := "failed"
			if errors.Is(err, context.DeadlineExceeded) {
				code = "timeout"
			}
			add(Diag{ID: "pipe", Level: "fail", Code: code, Params: map[string]any{"engine": def, "error": redact(err.Error()), "ms": s.probeTimeout.Milliseconds()}})
		}
	}

	// folder: can the app write to its data folder?
	if f, err := os.CreateTemp(s.dataDir, ".ladilala-doctor-*"); err != nil {
		add(Diag{ID: "folder", Level: "fail", Code: "notWritable", Params: map[string]any{"path": s.dataDir, "error": redact(err.Error())}})
	} else {
		f.Close()
		_ = os.Remove(f.Name())
		add(Diag{ID: "folder", Level: "ok", Code: "ok", Params: map[string]any{"path": s.dataDir}})
	}

	// job: can the app create the Job object that kills browsers with the app?
	if j, err := driver.NewJob(); err != nil {
		add(Diag{ID: "job", Level: "fail", Code: "failed", Params: map[string]any{"error": redact(err.Error())}})
	} else {
		_ = j.Close()
		add(Diag{ID: "job", Level: "ok", Code: "ok"})
	}

	// relay: can the app open a local port for the proxy relay?
	if ln, err := net.Listen("tcp", "127.0.0.1:0"); err != nil {
		add(Diag{ID: "relay", Level: "fail", Code: "failed", Params: map[string]any{"error": redact(err.Error())}})
	} else {
		addr := ln.Addr().String()
		_ = ln.Close()
		add(Diag{ID: "relay", Level: "ok", Code: "ok", Params: map[string]any{"addr": addr}})
	}
	return out, nil
}

// DiagnosticsReport returns the doctor result as plain text for a support
// message. Home folder paths and the user name are replaced.
func (s *EngineService) DiagnosticsReport(ctx context.Context) (string, error) {
	ds, _ := s.RunDiagnostics(ctx)
	var b strings.Builder
	fmt.Fprintf(&b, "Ladilala engine report %s\n", time.Now().UTC().Format(time.RFC3339))
	list, _ := s.ListEngines(ctx)
	for _, e := range list {
		fmt.Fprintf(&b, "engine %s tier %d status %s active %q path %q\n", e.ID, e.Tier, e.Status, e.Active, redact(e.Path))
	}
	for _, d := range ds {
		fmt.Fprintf(&b, "[%s] %s: %s", d.Level, d.ID, d.Code)
		for k, v := range d.Params {
			fmt.Fprintf(&b, " %s=%v", k, redactAny(v))
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func redactAny(v any) any {
	if s, ok := v.(string); ok {
		return redact(s)
	}
	return v
}

// redact removes the home folder and the user name from a text.
func redact(s string) string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		s = strings.ReplaceAll(s, h, "~")
		s = strings.ReplaceAll(s, filepath.ToSlash(h), "~")
	}
	if u := os.Getenv("USERNAME"); u != "" && len(u) > 2 {
		s = strings.ReplaceAll(s, u, "user")
	}
	return s
}

// ---- probe and test launch ---------------------------------------------------------

// probeStock starts in with a throw-away profile over the CDP pipe and asks it
// for its version. It closes the browser before it returns.
func (s *EngineService) probeStock(ctx context.Context, in driver.Install) (string, error) {
	sess, cleanup, _, err := s.startProbe(ctx, in)
	if err != nil {
		return "", err
	}
	defer cleanup()
	return browserVersion(ctx, sess)
}

func (s *EngineService) startProbe(ctx context.Context, in driver.Install) (*driver.StockSession, func(), time.Duration, error) {
	dir, err := os.MkdirTemp("", "ladilala-probe-")
	if err != nil {
		return nil, nil, 0, err
	}
	l := &driver.Launcher{}
	if s.launcher != nil {
		l = s.launcher.Launcher
	}
	p := profile.Profile{
		ID: "engine-probe", DataDir: dir,
		Fingerprint: profile.NewFingerprint(7, profile.Options{Platform: profile.PlatformWindows, Language: "en-US", Timezone: "Asia/Ho_Chi_Minh"}),
	}
	start := time.Now()
	var sess *driver.StockSession
	if in.FPC {
		// The self-check of fingerprint-chromium (canvas and WebGL read) runs
		// inside the launch.
		sess, _, err = l.LaunchFPC(ctx, in, p, "")
	} else {
		sess, err = l.LaunchStockUnverified(ctx, driver.StockChrome{}, in, p, "")
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, 0, err
	}
	cleanup := func() {
		_ = sess.Close()
		_ = os.RemoveAll(dir)
	}
	return sess, cleanup, time.Since(start), nil
}

func browserVersion(ctx context.Context, sess *driver.StockSession) (string, error) {
	var res struct {
		Product string `json:"product"`
	}
	if err := sess.Call(ctx, "Browser.getVersion", nil, &res); err != nil {
		return "", fmt.Errorf("the browser did not answer over CDP: %w", err)
	}
	return strings.TrimPrefix(res.Product, "Chrome/"), nil
}

// TestStep is one step of the test launch.
type TestStep struct {
	ID     string         `json:"id"`    // launch | cdp | fingerprint | proxy
	State  string         `json:"state"` // ok | fail | skipped
	Code   string         `json:"code,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}

// TestLaunch starts the engine with a test profile and checks four things:
// the browser starts, it answers over CDP, the fingerprint reads back the same,
// and the proxy. The proxy step is skipped: it needs a profile with a proxy,
// and ProxyService checks proxies. A failed step skips the later ones.
func (s *EngineService) TestLaunch(ctx context.Context, engineID string) ([]TestStep, error) {
	if engineID == "" {
		engineID = s.effectiveDefault()
	}
	in := s.installFor(engineID)
	if in == nil {
		return nil, apiErr("engine.missing", "engine is not installed", "id", engineID)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	steps := []TestStep{{ID: "launch", State: "skipped"}, {ID: "cdp", State: "skipped"}, {ID: "fingerprint", State: "skipped"}, {ID: "proxy", State: "skipped", Code: "noProfile"}}
	fail := func(i int, err error) []TestStep {
		code := "failed"
		switch {
		case errors.Is(err, driver.ErrDebuggingBlocked):
			code = "blocked"
		case errors.Is(err, driver.ErrBinaryNotFound):
			code = "binary"
		case errors.Is(err, driver.ErrFPCCanvasBroken):
			code = "canvas"
		case errors.Is(err, context.DeadlineExceeded):
			code = "timeout"
		}
		steps[i] = TestStep{ID: steps[i].ID, State: "fail", Code: code, Params: map[string]any{"error": redact(err.Error())}}
		return steps
	}
	sess, cleanup, took, err := s.startProbe(ctx, *in)
	if err != nil {
		return fail(0, err), nil
	}
	defer cleanup()
	steps[0] = TestStep{ID: "launch", State: "ok", Params: map[string]any{"ms": took.Milliseconds()}}
	ver, err := browserVersion(ctx, sess)
	if err != nil {
		return fail(1, err), nil
	}
	steps[1] = TestStep{ID: "cdp", State: "ok", Params: map[string]any{"version": ver}}
	if _, err := sess.WaitPage(ctx); err != nil {
		return fail(2, err), nil
	}
	var h string
	if rep, ok := sess.FPCReport(); ok {
		h = rep.CanvasHash
	} else if h, err = sess.Verify(ctx); err != nil {
		return fail(2, err), nil
	}
	steps[2] = TestStep{ID: "fingerprint", State: "ok", Params: map[string]any{"hash": h}}
	return steps, nil
}
