package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/profile"
)

// Harness for docs/m0-engine-matrix.md. Subcommands: m0detect (detector pages
// and page/iframe/worker key values for one engine), m0reliab (fresh-launch
// reliability of a fingerprint-chromium build).

// m0Engine returns the launch parts for one matrix engine:
//
//	bare  stock Chrome, no injector, no emulation (control A)
//	tier1 stock Chrome with the Ladilala injector and emulation (B)
//	fpc   fingerprint-chromium build under dir, its own flags (C, D)
func m0Engine(kind, dir string) (driver.BrowserEngine, driver.Install, string, error) {
	switch kind {
	case "bare", "tier1":
		in, err := pickInstall("chrome", "")
		if err != nil {
			return nil, in, "", err
		}
		v, _ := driver.StockChrome{}.Version(in)
		if kind == "bare" {
			return plainChrome{}, in, v, nil
		}
		return driver.StockChrome{}, in, v, nil
	case "fpc":
		fe := driver.FingerprintChromium{SearchDirs: []string{dir}}
		installs, err := fe.Detect()
		if err != nil || len(installs) == 0 {
			return nil, driver.Install{}, "", fmt.Errorf("no fingerprint-chromium under %q (%v)", dir, err)
		}
		v, _ := fe.Version(installs[0])
		return fe, installs[0], v, nil
	}
	return nil, driver.Install{}, "", fmt.Errorf("unknown engine kind %q", kind)
}

// plainChrome is stock Chrome with no engine flags at all: the launcher adds
// only the data dir, first-run suppression and the debugging pipe.
type plainChrome struct{ driver.StockChrome }

func (plainChrome) Args(profile.Profile, string) ([]string, error) { return nil, nil }

// m0Fingerprint is the Windows profile every engine gets, so the matrix
// compares engines and not profiles.
func m0Fingerprint(seed uint64, version string) profile.Fingerprint {
	return profile.NewFingerprint(seed, profile.Options{Platform: profile.PlatformWindows, BrowserVersion: version})
}

func m0Launch(ctx context.Context, l *driver.Launcher, kind string, eng driver.BrowserEngine, in driver.Install, p profile.Profile) (*driver.StockSession, error) {
	if kind == "tier1" {
		return l.LaunchStockUnverified(ctx, eng, in, p, "")
	}
	return l.LaunchStockBare(ctx, eng, in, p, "")
}

type m0page struct {
	name, url, until string
	min, max         time.Duration
}

var m0pages = []m0page{
	{"sannysoft", "https://bot.sannysoft.com/", "Fingerprint Scanner", 6 * time.Second, 40 * time.Second},
	{"rebrowser", "https://bot-detector.rebrowser.net/", "runtimeEnableLeak", 10 * time.Second, 60 * time.Second},
	{"creepjs", "https://abrahamjuliot.github.io/creepjs/", "trust score", 25 * time.Second, 90 * time.Second},
	{"browserscan-bot", "https://www.browserscan.net/bot-detection", "Bot Detection", 15 * time.Second, 60 * time.Second},
	{"browserscan-fp", "https://www.browserscan.net/", "Authenticity", 15 * time.Second, 60 * time.Second},
	{"pixelscan-fp", "https://pixelscan.net/fingerprint-check", "Canvas", 20 * time.Second, 70 * time.Second},
	{"pixelscan-bot", "https://pixelscan.net/bot-check", "Bot", 15 * time.Second, 60 * time.Second},
	{"bl-canvas", "https://browserleaks.com/canvas", "Canvas", 10 * time.Second, 40 * time.Second},
	{"bl-webgl", "https://browserleaks.com/webgl", "WebGL", 10 * time.Second, 40 * time.Second},
	{"bl-fonts", "https://browserleaks.com/fonts", "Font", 10 * time.Second, 40 * time.Second},
	{"bl-rects", "https://browserleaks.com/rects", "Rect", 8 * time.Second, 30 * time.Second},
	{"bl-webrtc", "https://browserleaks.com/webrtc", "WebRTC", 12 * time.Second, 40 * time.Second},
	{"bl-js", "https://browserleaks.com/javascript", "JavaScript", 10 * time.Second, 40 * time.Second},
	{"turnstile", "https://nopecha.com/demo/turnstile", "zzz-never", 35 * time.Second, 36 * time.Second},
	{"cf-challenge", "https://nopecha.com/demo/cloudflare", "zzz-never", 35 * time.Second, 36 * time.Second},
	{"cf-rucaptcha", "https://rucaptcha.com/demo/cloudflare-turnstile-challenge", "zzz-never", 40 * time.Second, 41 * time.Second},
	{"google-login", "https://accounts.google.com/signin", "Sign in", 6 * time.Second, 25 * time.Second},
	{"amiunique", "https://amiunique.org/fingerprint", "fingerprint", 10 * time.Second, 40 * time.Second},
}

// kvFn runs in page, iframe and dedicated worker scope and returns what the
// scope reports. Page-only surfaces (audio, rects, fonts) are skipped in a
// worker.
const kvFn = `async function () {
  const isPage = typeof document !== 'undefined';
  const o = { scope: isPage ? 'page' : 'worker' };
  const H = (a) => { let h = 2166136261; for (let i = 0; i < a.length; i++) h = Math.imul(h ^ (typeof a === 'string' ? a.charCodeAt(i) : a[i]), 16777619) >>> 0; return h.toString(16); };
  const mk = (w, h) => isPage ? Object.assign(document.createElement('canvas'), { width: w, height: h }) : new OffscreenCanvas(w, h);
  o.ua = navigator.userAgent; o.plat = navigator.platform; o.hw = navigator.hardwareConcurrency; o.mem = navigator.deviceMemory;
  o.lang = navigator.language; o.webdriver = navigator.webdriver;
  o.plugins = navigator.plugins ? navigator.plugins.length : -1;
  try {
    const u = navigator.userAgentData;
    o.brands = u.brands.map(b => b.brand + ' ' + b.version).join('; '); o.mobile = u.mobile; o.uaplat = u.platform;
    const h = await u.getHighEntropyValues(['platformVersion', 'architecture', 'bitness', 'uaFullVersion', 'fullVersionList']);
    o.he = JSON.stringify(h);
  } catch (e) { o.brands = 'ERR ' + e; }
  try {
    const g = mk(8, 8).getContext('webgl'), e = g.getExtension('WEBGL_debug_renderer_info');
    o.gpuV = g.getParameter(e.UNMASKED_VENDOR_WEBGL); o.gpu = g.getParameter(e.UNMASKED_RENDERER_WEBGL);
    o.glVer = g.getParameter(g.VERSION); o.glExt = g.getSupportedExtensions().length;
    const c = mk(64, 64), gl = c.getContext('webgl', { preserveDrawingBuffer: true });
    const sh = (t, s) => { const x = gl.createShader(t); gl.shaderSource(x, s); gl.compileShader(x); return x; };
    const p = gl.createProgram();
    gl.attachShader(p, sh(gl.VERTEX_SHADER, 'attribute vec2 a;varying vec2 v;void main(){v=a;gl_Position=vec4(a,0,1);}'));
    gl.attachShader(p, sh(gl.FRAGMENT_SHADER, 'precision mediump float;varying vec2 v;void main(){gl_FragColor=vec4(v.x*.5+.5,v.y*.5+.5,.3,1);}'));
    gl.linkProgram(p); gl.useProgram(p);
    const b = gl.createBuffer(); gl.bindBuffer(gl.ARRAY_BUFFER, b);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, 0, 1]), gl.STATIC_DRAW);
    const a = gl.getAttribLocation(p, 'a'); gl.enableVertexAttribArray(a); gl.vertexAttribPointer(a, 2, gl.FLOAT, false, 0, 0);
    gl.clearColor(.1, .2, .3, 1); gl.clear(gl.COLOR_BUFFER_BIT); gl.drawArrays(gl.TRIANGLES, 0, 3);
    const px = new Uint8Array(64 * 64 * 4); gl.readPixels(0, 0, 64, 64, gl.RGBA, gl.UNSIGNED_BYTE, px);
    o.glPx = H(px);
  } catch (e) { o.gpu = o.gpu || ('ERR ' + e); }
  {
    const c = mk(220, 60), x = c.getContext('2d');
    x.textBaseline = 'top'; x.font = '16px Arial'; x.fillStyle = '#f60'; x.fillRect(10, 5, 100, 30);
    x.fillStyle = '#069'; x.fillText('ladilala fp 1.0 \u{1F600}', 4, 20);
    o.canvas = H(x.getImageData(0, 0, 220, 60).data);
    if (isPage) o.canvasUrl = H(c.toDataURL());
  }
  if (isPage) {
    try {
      const ctx = new OfflineAudioContext(1, 44100, 44100), osc = ctx.createOscillator(), cmp = ctx.createDynamicsCompressor();
      osc.type = 'triangle'; osc.frequency.value = 10000; osc.connect(cmp); cmp.connect(ctx.destination); osc.start(0);
      const buf = await ctx.startRendering(), d = buf.getChannelData(0);
      let s = 0; for (let i = 4500; i < 5000; i++) s += Math.abs(d[i]);
      o.audioSum = s.toFixed(12); o.audio = H(new Uint8Array(d.buffer));
    } catch (e) { o.audio = 'ERR ' + e; }
    const d = document.createElement('div');
    d.style.cssText = 'position:absolute;left:10.3px;top:20.7px;width:123.4px;height:45.6px;transform:rotate(7.3deg);font:15px Arial';
    d.textContent = 'rect probe text for client rects'; document.body.appendChild(d);
    const r = d.getBoundingClientRect(), rr = document.createRange(); rr.selectNodeContents(d);
    const q = rr.getClientRects()[0];
    o.rect = [r.x, r.y, r.width, r.height, q.x, q.y, q.width, q.height].join(','); d.remove();
    const base = ['monospace', 'sans-serif', 'serif'], list = ['Arial', 'Arial Black', 'Calibri', 'Cambria', 'Candara', 'Consolas', 'Courier New', 'Franklin Gothic', 'Georgia', 'Impact', 'Lucida Console', 'Microsoft Sans Serif', 'Palatino Linotype', 'Segoe UI', 'Tahoma', 'Times New Roman', 'Trebuchet MS', 'Verdana', 'Helvetica', 'Helvetica Neue', 'Menlo', 'Monaco', 'Optima', 'Gill Sans', 'Futura', 'Avenir', 'Ubuntu', 'DejaVu Sans', 'Liberation Sans', 'Noto Sans', 'Roboto', 'Segoe UI Emoji', 'Segoe Print', 'MS Gothic', 'SimSun', 'Malgun Gothic', 'Yu Gothic', 'Bahnschrift', 'Sylfaen', 'Gabriola', 'Ebrima', 'Nirmala UI', 'Leelawadee UI', 'Marlett', 'Wingdings', 'Symbol', 'Comic Sans MS', 'Century Gothic', 'Book Antiqua', 'Garamond'];
    const sp = document.createElement('span'); sp.textContent = 'mmmmmmmmmmlli'; sp.style.cssText = 'position:absolute;left:-9999px;font-size:72px'; document.body.appendChild(sp);
    const w = (f) => { sp.style.fontFamily = f; return sp.offsetWidth; };
    const bw = base.map(b => w(b)); let n = 0;
    for (const f of list) if (base.some((b, i) => w("'" + f + "'," + b) !== bw[i])) n++;
    sp.remove(); o.fonts = n + '/' + list.length;
    { const x = document.createElement('canvas').getContext('2d'); x.font = '18px Arial'; o.measure = x.measureText('mmmmmmmmmmlli fingerprint').width; }
    o.tz = Intl.DateTimeFormat().resolvedOptions().timeZone + ' ' + new Date().getTimezoneOffset();
    o.screen = [screen.width, screen.height, screen.availWidth, screen.availHeight, devicePixelRatio, screen.colorDepth].join('x');
    o.inner = innerWidth + 'x' + innerHeight + ' outer ' + outerWidth + 'x' + outerHeight;
  }
  return o;
}`

// kvIframe and kvWorker wrap the JSON string literal of the probe call.
const kvIframe = `(async () => { const f = document.createElement('iframe'); f.srcdoc = '<html></html>'; document.body.appendChild(f);
  await new Promise(r => f.onload = r); return await f.contentWindow.eval(__SRC__); })()`

const kvWorker = `(async () => { const w = new Worker(URL.createObjectURL(new Blob([__SRC__ + '.then(postMessage)'])));
  return await new Promise((ok, no) => { w.onmessage = (e) => ok(e.data); w.onerror = (e) => no(new Error(String(e.message))); }); })()`

const rtcJS = `(async () => {
  const pc = new RTCPeerConnection({ iceServers: [{ urls: 'stun:stun.l.google.com:19302' }] });
  pc.createDataChannel('x');
  const cands = [];
  pc.onicecandidate = (e) => { if (e.candidate) cands.push(e.candidate.candidate); };
  await pc.setLocalDescription(await pc.createOffer());
  await new Promise(r => setTimeout(r, 5000)); pc.close();
  let pub = ''; try { pub = (await (await fetch('https://api.ipify.org')).text()).trim(); } catch (_) {}
  const parsed = cands.map(c => { const p = c.split(' '); return { type: p[7], addr: p[4] }; });
  return { types: parsed.map(p => p.type + (p.addr.endsWith('.local') ? '(mdns)' : '')).join(','),
           srflxIsPublic: !!pub && parsed.some(p => p.type === 'srflx' && p.addr === pub),
           hostRawPrivate: parsed.some(p => p.type === 'host' && /^(10|192\.168|172\.(1[6-9]|2\d|3[01]))\./.test(p.addr)), n: cands.length };
})()`

func runM0Detect(args []string) error {
	fs := flag.NewFlagSet("m0detect", flag.ExitOnError)
	kind := fs.String("engine", "bare", "bare | tier1 | fpc")
	dir := fs.String("enginedir", "", "fingerprint-chromium directory (fpc)")
	tag := fs.String("tag", "", "name for output files")
	out := fs.String("out", "m0-out", "output directory")
	seed := fs.Uint64("seed", 7, "fingerprint seed")
	only := fs.String("only", "", "comma list of page names (default all; 'keys' = local key values only)")
	shots := fs.String("shots", "creepjs,browserscan-bot,browserscan-fp,rebrowser,pixelscan-fp,pixelscan-bot,sannysoft,turnstile,cf-challenge", "pages to screenshot")
	tz := fs.String("tz", "", "override the profile timezone (default: the profile's own)")
	extra := fs.String("extra", "", "extra browser switches, comma separated")
	fs.Parse(args)
	if *tag == "" {
		*tag = *kind
	}
	eng, in, version, err := m0Engine(*kind, *dir)
	if err != nil {
		return err
	}
	if *extra != "" {
		eng = driver.WithExtraArgs(eng, strings.Split(*extra, ",")...)
	}
	fp := m0Fingerprint(*seed, version)
	if *tz != "" {
		fp.Timezone = *tz
	}
	p := profile.Profile{ID: "m0", DataDir: filepath.Join(os.TempDir(), fmt.Sprintf("m0-det-%s-%d", *tag, time.Now().UnixNano())), Fingerprint: fp}
	defer os.RemoveAll(p.DataDir)
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	if a, err := eng.Args(p, ""); err == nil {
		fmt.Printf("ARGS %s\n", strings.Join(a, " "))
	}
	l, err := driver.NewLauncher()
	if err != nil {
		return err
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	t0 := time.Now()
	s, err := m0Launch(ctx, l, *kind, eng, in, p)
	if err != nil {
		return err
	}
	defer s.Close()
	var ver struct{ Product, UserAgent string }
	cdpErr := s.Call(ctx, "Browser.getVersion", nil, &ver)
	fmt.Printf("ENGINE %s tag=%s path=%s version=%s product=%q launch+cdp_ms=%d err=%v\n", *kind, *tag, in.Path, version, ver.Product, time.Since(t0).Milliseconds(), cdpErr)
	fmt.Printf("PROFILE seed=%d ua=%s\n", *seed, fp.UserAgent)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<!doctype html><title>m0</title><body>m0</body>")
	}))
	defer srv.Close()

	want := map[string]bool{}
	for _, n := range strings.Split(*only, ",") {
		want[n] = true
	}
	if *only == "" || want["keys"] {
		if err := m0Keys(ctx, s, srv.URL, filepath.Join(*out, *tag+"-keys.json")); err != nil {
			fmt.Println("KEYS error:", err)
		}
	}
	for _, d := range m0pages {
		if *only != "" && !want[d.name] {
			continue
		}
		file := filepath.Join(*out, fmt.Sprintf("%s-%s", *tag, d.name))
		text, err := m0Read(ctx, s, d, strings.Contains(","+*shots+",", ","+d.name+","), file+".png")
		_ = os.WriteFile(file+".txt", []byte(text), 0o644)
		fmt.Printf("PAGE %s chars=%d err=%v\n", d.name, len(text), err)
	}
	return nil
}

func m0Keys(ctx context.Context, s *driver.StockSession, url, file string) error {
	kctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	pg, err := s.WaitPage(kctx)
	if err != nil {
		return err
	}
	if err := pg.Navigate(kctx, url); err != nil {
		return err
	}
	res := map[string]any{}
	var page, iframe, worker map[string]any
	if err := pg.Eval(kctx, "("+kvFn+")()", &page); err != nil {
		return fmt.Errorf("page: %w", err)
	}
	src, _ := json.Marshal("(" + kvFn + ")()")
	if err := pg.Eval(kctx, strings.ReplaceAll(kvIframe, "__SRC__", string(src)), &iframe); err != nil {
		res["iframeErr"] = err.Error()
	}
	if err := pg.Eval(kctx, strings.ReplaceAll(kvWorker, "__SRC__", string(src)), &worker); err != nil {
		res["workerErr"] = err.Error()
	}
	var mouse map[string]float64
	if err := pg.Eval(kctx, `(window.__m = null, addEventListener('mousedown', e => { window.__m = { dx: e.screenX - e.clientX, dy: e.screenY - e.clientY, wx: window.screenX, wy: window.screenY }; }, { once: true }), 1)`, nil); err == nil {
		for _, t := range []string{"mouseMoved", "mousePressed"} {
			_ = pg.Call(kctx, "Input.dispatchMouseEvent", map[string]any{"type": t, "x": 100, "y": 100, "button": "left", "clickCount": 1}, nil)
		}
		time.Sleep(300 * time.Millisecond)
		_ = pg.Eval(kctx, `window.__m`, &mouse)
		res["cdpMouse"] = mouse
		fmt.Printf("KEY cdpMouse screenX-clientX=%v,%v window.screenX/Y=%v,%v\n", mouse["dx"], mouse["dy"], mouse["wx"], mouse["wy"])
	}
	var rtc map[string]any
	if err := pg.Eval(kctx, rtcJS, &rtc); err != nil {
		res["rtcErr"] = err.Error()
	}
	res["page"], res["iframe"], res["worker"], res["rtc"] = page, iframe, worker, rtc
	b, _ := json.MarshalIndent(res, "", " ")
	_ = os.WriteFile(file, b, 0o644)
	for _, k := range []string{"ua", "plat", "hw", "mem", "gpu", "glPx", "canvas", "lang"} {
		ok := true
		for _, o := range []map[string]any{iframe, worker} {
			if o != nil && fmt.Sprint(o[k]) != fmt.Sprint(page[k]) {
				ok = false
			}
		}
		fmt.Printf("KEY %-7s consistent=%-5v page=%v\n", k, ok, page[k])
	}
	for _, k := range []string{"brands", "he", "uaplat", "webdriver", "plugins", "gpuV", "glExt", "canvasUrl", "audio", "audioSum", "rect", "fonts", "measure", "tz", "screen", "inner"} {
		fmt.Printf("KEY %-9s %v\n", k, page[k])
	}
	fmt.Printf("KEY rtc %v err=%v\n", rtc, res["rtcErr"])
	return nil
}

func m0Read(ctx context.Context, s *driver.StockSession, d m0page, shot bool, shotPath string) (string, error) {
	dctx, cancel := context.WithTimeout(ctx, d.max+60*time.Second)
	defer cancel()
	pg, err := s.NewPage(dctx, "about:blank")
	if err != nil {
		return "", err
	}
	nctx, ncancel := context.WithTimeout(dctx, 40*time.Second)
	navErr := pg.Navigate(nctx, d.url)
	ncancel()
	start := time.Now()
	var text string
	stable := 0
	for time.Since(start) < d.max {
		time.Sleep(time.Second)
		var cur string
		if err := pg.Eval(dctx, `document.body ? document.body.innerText : ''`, &cur); err != nil {
			continue
		}
		if len(cur) == len(text) {
			stable++
		} else {
			stable = 0
		}
		text = cur
		if time.Since(start) >= d.min && strings.Contains(strings.ToLower(text), strings.ToLower(d.until)) && stable >= 4 {
			break
		}
	}
	var url, title string
	_ = pg.Eval(dctx, `location.href`, &url)
	_ = pg.Eval(dctx, `document.title`, &title)
	text = fmt.Sprintf("URL: %s\nTITLE: %s\n%s", url, title, text)
	switch d.name {
	case "rebrowser":
		var js string
		if pg.Eval(dctx, `JSON.stringify((window.detections||[]).map(d => ({type: d.type, rating: d.rating, note: String(d.note).replace(/<[^>]+>/g, '')})))`, &js) == nil {
			text += "\n--JSON--\n" + js
		}
	case "creepjs":
		// The trust score and lies live in the rendered DOM; keep the HTML
		// of the lies and trust blocks too.
		var html string
		if pg.Eval(dctx, `(document.querySelector('.visitor-info')||document.body).innerText`, &html) == nil {
			text += "\n--VISITOR--\n" + html
		}
		var doc string
		if pg.Eval(dctx, `document.documentElement.outerHTML`, &doc) == nil {
			_ = os.WriteFile(strings.TrimSuffix(shotPath, ".png")+".html", []byte(doc), 0o644)
		}
		// Hidden detail blocks carry the lie lists; textContent includes them.
		var all string
		if pg.Eval(dctx, `Array.from(document.querySelectorAll('[class*="lie"], [class*="trust"], .bold-fail, .erase, details')).map(e => (e.className||e.tagName) + ': ' + e.textContent.replace(/\s+/g, ' ').slice(0, 600)).join('\n')`, &all) == nil {
			text += "\n--HIDDEN--\n" + all
		}
	}
	if shot {
		var res struct {
			Data []byte `json:"data"`
		}
		err := pg.Call(dctx, "Page.captureScreenshot", map[string]any{"format": "png", "clip": map[string]any{"x": 0, "y": 0, "width": 1280, "height": 800, "scale": 0.6}}, &res)
		if err == nil {
			_ = os.WriteFile(shotPath, res.Data, 0o644)
		}
	}
	if navErr != nil && len(text) < 60 {
		return text, navErr
	}
	return text, nil
}

// ---- reliability -----------------------------------------------------------

// relJS reads canvas 2D (getImageData and toDataURL) and WebGL readPixels.
const relJS = `(function () {
  const H = (a) => { let h = 2166136261; for (let i = 0; i < a.length; i++) h = Math.imul(h ^ (typeof a === 'string' ? a.charCodeAt(i) : a[i]), 16777619) >>> 0; return h.toString(16); };
  const c = document.createElement('canvas'); c.width = 220; c.height = 60;
  const x = c.getContext('2d'); x.textBaseline = 'top'; x.font = '16px Arial';
  x.fillStyle = '#f60'; x.fillRect(10, 5, 100, 30); x.fillStyle = '#069'; x.fillText('ladilala fp 1.0 \u{1F600}', 4, 20);
  const o = { img: H(x.getImageData(0, 0, 220, 60).data), url: H(c.toDataURL()), gl: '' };
  try {
    const g = document.createElement('canvas'); g.width = g.height = 32;
    const gl = g.getContext('webgl', { preserveDrawingBuffer: true });
    gl.clearColor(.2, .5, .7, 1); gl.clear(gl.COLOR_BUFFER_BIT);
    const px = new Uint8Array(32 * 32 * 4); gl.readPixels(0, 0, 32, 32, gl.RGBA, gl.UNSIGNED_BYTE, px); o.gl = H(px);
  } catch (e) { o.gl = 'ERR'; }
  return o;
})()`

type relRead struct{ Img, URL, GL string }

func (r relRead) key() string { return r.Img + "/" + r.URL + "/" + r.GL }

type relSess interface {
	navigate(ctx context.Context, url string) error
	eval(ctx context.Context, expr string, out any) error
	cdpMs() int64
	crashed() bool
	close()
}

type rawRel struct {
	s     *driver.StockSession
	pg    *driver.Page
	t     time.Duration
	crash chan struct{}
}

func (r *rawRel) navigate(ctx context.Context, url string) error { return r.pg.Navigate(ctx, url) }
func (r *rawRel) eval(ctx context.Context, e string, o any) error {
	return r.pg.Eval(ctx, e, o)
}
func (r *rawRel) cdpMs() int64 { return r.t.Milliseconds() }
func (r *rawRel) crashed() bool {
	select {
	case <-r.crash:
		return true
	default:
		return false
	}
}
func (r *rawRel) close() { _ = r.s.Close() }

type legacyRel struct {
	s     *driver.Session
	t     time.Duration
	crash chan struct{}
}

// tab derives a context bound to the browser tab with ctx's deadline.
func (r *legacyRel) tab(ctx context.Context) (context.Context, context.CancelFunc) {
	if d, ok := ctx.Deadline(); ok {
		return context.WithDeadline(r.s.Ctx, d)
	}
	return context.WithCancel(r.s.Ctx)
}

func (r *legacyRel) navigate(ctx context.Context, url string) error {
	c, cancel := r.tab(ctx)
	defer cancel()
	_, err := chromedp.Run(c, chromedp.Navigate(url))
	return err
}
func (r *legacyRel) eval(ctx context.Context, e string, o any) error {
	c, cancel := r.tab(ctx)
	defer cancel()
	m, err := chromedp.Run(c, chromedp.Evaluate[map[string]string](e))
	if err == nil {
		*(o.(*map[string]string)) = m
	}
	return err
}
func (r *legacyRel) cdpMs() int64 { return r.t.Milliseconds() }
func (r *legacyRel) crashed() bool {
	select {
	case <-r.crash:
		return true
	default:
		return false
	}
}
func (r *legacyRel) close() { _ = r.s.Close() }

type relOut struct {
	launchMs, cdpMs int64
	err             string
	hung, crashed   bool
	first           relRead
	later           [3]relRead
	hungAt          string
	pid             int
}

func relLaunch(ctx context.Context, path string, l *driver.Launcher, eng driver.BrowserEngine, in driver.Install, p profile.Profile) (relSess, time.Duration, error) {
	t0 := time.Now()
	crash := make(chan struct{}, 1)
	if path == "legacy" {
		s, err := l.Launch(ctx, eng, in, p, "")
		if err != nil {
			return nil, 0, err
		}
		launch := time.Since(t0)
		return &legacyRel{s: s, t: launch, crash: crash}, launch, nil
	}
	s, err := l.LaunchStockBare(ctx, eng, in, p, "")
	if err != nil {
		return nil, 0, err
	}
	var v struct{ Product string }
	if err := s.Call(ctx, "Browser.getVersion", nil, &v); err != nil {
		_ = s.Close()
		return nil, 0, err
	}
	cdp := time.Since(t0)
	pg, err := s.WaitPage(ctx)
	if err != nil {
		_ = s.Close()
		return nil, 0, err
	}
	r := &rawRel{s: s, pg: pg, t: cdp, crash: crash}
	_ = pg.Call(ctx, "Inspector.enable", nil, nil)
	ch, _ := pg.Subscribe("Inspector.targetCrashed")
	go func() {
		if _, ok := <-ch; ok {
			crash <- struct{}{}
		}
	}()
	return r, cdp, nil
}

func relProbe(ctx context.Context, path string, l *driver.Launcher, eng driver.BrowserEngine, in driver.Install, p profile.Profile, url string) relOut {
	var o relOut
	lctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	t0 := time.Now()
	rs, cdp, err := relLaunch(lctx, path, l, eng, in, p)
	if err != nil {
		o.err = "launch: " + err.Error()
		return o
	}
	defer rs.close()
	o.launchMs, o.cdpMs = time.Since(t0).Milliseconds(), cdp.Milliseconds()
	read := func(label string) (relRead, bool) {
		rctx, rcancel := context.WithTimeout(ctx, 3*time.Second)
		defer rcancel()
		var r relRead
		var out map[string]string
		err := rs.eval(rctx, relJS, &out)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || rctx.Err() != nil {
				o.hung, o.hungAt = true, label
			} else {
				if strings.Contains(strings.ToLower(err.Error()), "crash") {
					o.crashed = true
				}
				if o.err == "" {
					o.err = label + ": " + err.Error()
				}
			}
			return r, false
		}
		return relRead{out["img"], out["url"], out["gl"]}, true
	}
	nctx, ncancel := context.WithTimeout(ctx, 15*time.Second)
	err = rs.navigate(nctx, url)
	ncancel()
	if err != nil {
		o.err = "navigate: " + err.Error()
		return o
	}
	var ok bool
	if o.first, ok = read("0s"); !ok {
		o.crashed = o.crashed || rs.crashed()
		return o
	}
	time.Sleep(time.Second)
	for i := range o.later {
		if o.later[i], ok = read(fmt.Sprintf("1s#%d", i+1)); !ok {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	o.crashed = o.crashed || rs.crashed()
	return o
}

func runM0Reliab(args []string) error {
	fs := flag.NewFlagSet("m0reliab", flag.ExitOnError)
	dir := fs.String("enginedir", "", "fingerprint-chromium directory")
	path := fs.String("path", "legacy", "legacy (chromedp pipe) | raw (own CDP over pipe)")
	n := fs.Int("n", 100, "fresh launches")
	logp := fs.String("out", "", "JSON-lines log")
	hold := fs.Duration("hold", 0, "keep each browser open this long after the reads (RAM sampling)")
	fs.Parse(args)
	eng, in, version, err := m0Engine("fpc", *dir)
	if err != nil {
		return err
	}
	l, err := driver.NewLauncher()
	if err != nil {
		return err
	}
	defer l.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html><body>m0</body></html>")) }))
	defer srv.Close()
	root, err := os.MkdirTemp("", "m0rel-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	var logf *os.File
	if *logp != "" {
		if logf, err = os.OpenFile(*logp, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err != nil {
			return err
		}
		defer logf.Close()
	}
	ctx := context.Background()

	// Real (unspoofed) hashes of this machine: canvas and GPU spoofing off.
	real := map[string]bool{}
	realEng := driver.WithExtraArgs(eng, "--disable-spoofing=canvas,gpu")
	for i := range 5 {
		p := profile.Profile{ID: "real", DataDir: filepath.Join(root, fmt.Sprintf("real-%d", i)), Fingerprint: m0Fingerprint(uint64(900+i), version)}
		o := relProbe(ctx, *path, l, realEng, in, p, srv.URL)
		for _, r := range append([]relRead{o.first}, o.later[:]...) {
			if r.Img != "" {
				real[r.Img] = true
			}
		}
	}
	fmt.Printf("ENGINE %s path=%s real canvas hashes: %d distinct\n", version, *path, len(real))

	var launches, cdps []float64
	var hangs, crashes, errs, leak0, unstable, realLate, hang0, hangLate int
	seen := map[string]bool{}
	for i := range *n {
		seed := uint64(7000 + i)
		p := profile.Profile{ID: "m0", DataDir: filepath.Join(root, fmt.Sprintf("p-%d", i)), Fingerprint: m0Fingerprint(seed, version)}
		o := relProbe(ctx, *path, l, eng, in, p, srv.URL)
		if *hold > 0 {
			time.Sleep(*hold)
		}
		switch {
		case o.err != "":
			errs++
		}
		if o.hung {
			hangs++
			if o.hungAt == "0s" {
				hang0++
			} else {
				hangLate++
			}
		}
		if o.crashed {
			crashes++
		}
		if !o.hung && o.err == "" {
			launches = append(launches, float64(o.launchMs))
			cdps = append(cdps, float64(o.cdpMs))
			if o.first.Img != o.later[0].Img || o.first.GL != o.later[0].GL {
				leak0++
			}
			if o.later[0] != o.later[1] || o.later[1] != o.later[2] {
				unstable++
			}
			if real[o.later[0].Img] || real[o.later[2].Img] {
				realLate++
			}
			seen[o.later[2].Img] = true
		}
		if logf != nil {
			b, _ := json.Marshal(map[string]any{"i": i, "seed": seed, "path": *path, "engine": version, "launch_ms": o.launchMs, "cdp_ms": o.cdpMs,
				"hung": o.hung, "hung_at": o.hungAt, "crashed": o.crashed, "err": o.err, "first": o.first.key(), "later": []string{o.later[0].key(), o.later[1].key(), o.later[2].key()}})
			fmt.Fprintln(logf, string(b))
		}
		os.RemoveAll(p.DataDir)
		if i%10 == 9 {
			fmt.Fprintf(os.Stderr, "done %d hangs=%d crashes=%d errs=%d\n", i+1, hangs, crashes, errs)
		}
	}
	sort.Float64s(launches)
	sort.Float64s(cdps)
	med := func(a []float64) float64 {
		if len(a) == 0 {
			return -1
		}
		return a[len(a)/2]
	}
	fmt.Printf("SUMMARY engine=%s path=%s n=%d hangs=%d (at0s=%d later=%d) crashes=%d errors=%d firstReadDiffersFromLater=%d lateUnstable=%d lateEqualsReal=%d distinctLateHashes=%d launch_ms median=%.0f cdp_ms median=%.0f max=%.0f\n",
		version, *path, *n, hangs, hang0, hangLate, crashes, errs, leak0, unstable, realLate, len(seen), med(launches), med(cdps), func() float64 {
			if len(cdps) == 0 {
				return -1
			}
			return cdps[len(cdps)-1]
		}())
	return nil
}
