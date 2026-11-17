package driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/8bu/ladilala/core/profile"
)

// fpcPageWait bounds the wait for the first tab of a fresh fingerprint-chromium.
const fpcPageWait = 15 * time.Second

// FPCUnsupported lists the profile fingerprint fields fingerprint-chromium
// cannot set. They stay real in the page, so the app must not claim them as
// spoofed: screen size, device memory (reads 16) and color depth (reads 32).
var FPCUnsupported = []string{"screen", "deviceMemory", "colorDepth"}

// FPCReport is what the launch self-check read back.
type FPCReport struct {
	CanvasHash   string // hash of the canvas data URL
	WebGL        bool   // false when the page has no WebGL context (not a failure)
	MeasureText  float64
	DeviceMemory float64 // 0 when the page does not report it
	ColorDepth   int
	// Oddities are known engine bugs a page can see. They are logged, never fatal.
	Oddities []string
}

// fpcCheckJS reads the canvas through getImageData and toDataURL and WebGL
// through readPixels: the reads on which 150.0.7871.186 crashes. It also
// reads the three values the 148 build gets wrong.
const fpcCheckJS = `(() => {
  const o = {};
  const c = document.createElement('canvas');
  c.width = 220; c.height = 60;
  const x = c.getContext('2d');
  x.textBaseline = 'top'; x.font = '16px Arial';
  x.fillStyle = '#f60'; x.fillRect(10, 5, 100, 30);
  x.fillStyle = '#069'; x.fillText('ladilala fp 1.0 \u{1F600}', 4, 20);
  o.pixels = x.getImageData(0, 0, 220, 60).data.length;
  o.url = c.toDataURL();
  const gl = document.createElement('canvas').getContext('webgl');
  o.webgl = !!gl;
  if (gl) {
    const px = new Uint8Array(4);
    gl.clearColor(0.2, 0.4, 0.6, 1); gl.clear(gl.COLOR_BUFFER_BIT);
    gl.readPixels(0, 0, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, px);
  }
  const m = document.createElement('canvas').getContext('2d');
  m.font = '16px Arial';
  o.measure = m.measureText('mmmmmmmmmmlli').width;
  o.mem = navigator.deviceMemory || 0;
  o.depth = screen.colorDepth;
  return o;
})()`

// FPCSelfCheck runs the launch self-check on the first tab: a canvas read
// and a WebGL read, 3 seconds at most. A build that hangs or crashes returns
// ErrFPCCanvasBroken. measureText, deviceMemory and colorDepth oddities go
// into the report and never fail the check.
func (s *StockSession) FPCSelfCheck(ctx context.Context) (FPCReport, error) {
	wctx, cancel := context.WithTimeout(ctx, fpcPageWait)
	pg, err := s.WaitPage(wctx)
	cancel()
	if err != nil {
		return FPCReport{}, err
	}
	rctx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	var res struct {
		Pixels  int     `json:"pixels"`
		URL     string  `json:"url"`
		WebGL   bool    `json:"webgl"`
		Measure float64 `json:"measure"`
		Mem     float64 `json:"mem"`
		Depth   int     `json:"depth"`
	}
	if err := pg.Eval(rctx, fpcCheckJS, &res); err != nil {
		if ctx.Err() != nil {
			return FPCReport{}, ctx.Err()
		}
		if rctx.Err() != nil || isClosed(s.exited) {
			return FPCReport{}, fmt.Errorf("%w: no answer within %s or the renderer died", ErrFPCCanvasBroken, ReadTimeout)
		}
		return FPCReport{}, fmt.Errorf("%w: %v", ErrFPCCanvasBroken, err)
	}
	if res.Pixels == 0 || res.URL == "" {
		return FPCReport{}, fmt.Errorf("%w: the canvas read returned no data", ErrFPCCanvasBroken)
	}
	sum := sha256.Sum256([]byte(res.URL))
	r := FPCReport{CanvasHash: hex.EncodeToString(sum[:8]), WebGL: res.WebGL, MeasureText: res.Measure, DeviceMemory: res.Mem, ColorDepth: res.Depth}
	if res.Measure < 1 {
		r.Oddities = append(r.Oddities, fmt.Sprintf("measureText width is %g (a real browser reports about 247)", res.Measure))
	}
	if res.Mem == 16 {
		r.Oddities = append(r.Oddities, "navigator.deviceMemory is 16 (the engine cannot set it)")
	}
	if res.Depth == 32 {
		r.Oddities = append(r.Oddities, "screen.colorDepth is 32 (a real Chrome reports 24)")
	}
	return r, nil
}

// LaunchFPC starts a fingerprint-chromium install for profile p. The injector
// is off: the engine spoofs inside the browser and a second layer of noise on
// top of it makes the fingerprint worse. No emulation runs either, so the
// fields in FPCUnsupported stay real.
//
// The browser's own version goes into --fingerprint-brand-version so the user
// agent matches what the engine can do. Before it returns, FPCSelfCheck runs;
// a build that fails it is closed and the error is returned.
func (l *Launcher) LaunchFPC(ctx context.Context, install Install, p profile.Profile, proxyAddr string) (*StockSession, FPCReport, error) {
	eng := FingerprintChromium{}
	if v, err := eng.Version(install); err == nil && v != "" {
		p.Fingerprint.BrowserVersion = v
	}
	s, err := l.launchStockOnce(ctx, eng, install, p, proxyAddr, true)
	if err != nil {
		return nil, FPCReport{}, err
	}
	rep, err := s.FPCSelfCheck(ctx)
	if err != nil {
		_ = s.Close()
		return nil, rep, err
	}
	s.fpc = &rep
	return s, rep, nil
}

// FPCReport returns what the launch self-check read, for a session LaunchFPC
// started.
func (s *StockSession) FPCReport() (FPCReport, bool) {
	if s.fpc == nil {
		return FPCReport{}, false
	}
	return *s.fpc, true
}
