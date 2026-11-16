package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/profile"
)

// Harness for docs/stock-engine.md. Subcommands: stockdetect (public detector
// pages), stocklaunches (N fresh launches), cft (Chrome for Testing).

func pickInstall(engine, cft string) (driver.Install, error) {
	if engine == "cft" {
		if _, err := os.Stat(cft); err != nil {
			return driver.Install{}, err
		}
		return driver.Install{Path: cft, Brand: driver.BrandChromium}, nil
	}
	installs, err := driver.StockChrome{}.Detect()
	if err != nil {
		return driver.Install{}, err
	}
	want := driver.BrandChrome
	if engine == "edge" {
		want = driver.BrandEdge
	}
	for _, in := range installs {
		if in.Brand == want {
			return in, nil
		}
	}
	return driver.Install{}, fmt.Errorf("no %s install found", want)
}

// stockFingerprint is a Windows profile that matches the installed browser's
// version and brand, so the UA agrees with the real feature set.
func stockFingerprint(seed uint64, in driver.Install, version string) profile.Fingerprint {
	fp := profile.NewFingerprint(seed, profile.Options{Platform: profile.PlatformWindows, BrowserVersion: version})
	if in.Brand == driver.BrandEdge {
		fp = profile.Shuffle(withBrand(fp, "Edge"), seed)
	}
	return fp
}

func withBrand(fp profile.Fingerprint, brand string) profile.Fingerprint {
	fp.Brand = brand
	return fp
}

type detector struct {
	name, url string
	until     string // substring that shows the page finished
	min, max  time.Duration
}

var detectors = []detector{
	{"sannysoft", "https://bot.sannysoft.com/", "Fingerprint Scanner", 6 * time.Second, 40 * time.Second},
	{"rebrowser", "https://bot-detector.rebrowser.net/", "runtimeEnableLeak", 10 * time.Second, 60 * time.Second},
	{"creepjs", "https://abrahamjuliot.github.io/creepjs/", "trust score", 25 * time.Second, 90 * time.Second},
	{"browserscan", "https://www.browserscan.net/bot-detection", "Bot Detection", 15 * time.Second, 60 * time.Second},
}

func runStockDetect(args []string) error {
	fs := flag.NewFlagSet("stockdetect", flag.ExitOnError)
	engine := fs.String("engine", "chrome", "chrome | edge | cft")
	cft := fs.String("cft", "", "chrome.exe of a Chrome for Testing build (engine cft)")
	mode := fs.String("mode", "inject", "inject | bare")
	out := fs.String("out", "stock-out", "directory for page texts")
	seed := fs.Uint64("seed", 7, "fingerprint seed")
	shots := fs.Int("shots", 0, "screenshots to save (rebrowser only)")
	only := fs.String("only", "", "comma list of detectors")
	fs.Parse(args)

	in, err := pickInstall(*engine, *cft)
	if err != nil {
		return err
	}
	version, _ := driver.StockChrome{}.Version(in)
	fp := stockFingerprint(*seed, in, version)
	p := profile.Profile{ID: "stockdetect", DataDir: filepath.Join(os.TempDir(), fmt.Sprintf("wm-det-%s-%s-%d", *engine, *mode, time.Now().UnixNano())), Fingerprint: fp}
	defer os.RemoveAll(p.DataDir)
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	l, err := driver.NewLauncher()
	if err != nil {
		return err
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	var s *driver.StockSession
	if *mode == "bare" {
		s, err = l.LaunchStockBare(ctx, driver.StockChrome{}, in, p, "")
	} else {
		s, err = l.LaunchStockUnverified(ctx, driver.StockChrome{}, in, p, "")
	}
	if err != nil {
		return err
	}
	defer s.Close()
	fmt.Printf("ENGINE %s %s version=%s mode=%s\n", *engine, in.Path, version, *mode)
	for _, d := range detectors {
		if *only != "" && !strings.Contains(*only, d.name) {
			continue
		}
		text, err := readDetector(ctx, s, d, *shots > 0 && d.name == "rebrowser", filepath.Join(*out, fmt.Sprintf("%s-%s-%s.png", *engine, *mode, d.name)))
		file := filepath.Join(*out, fmt.Sprintf("%s-%s-%s.txt", *engine, *mode, d.name))
		_ = os.WriteFile(file, []byte(text), 0o644)
		fmt.Printf("DETECTOR %s chars=%d err=%v file=%s\n", d.name, len(text), err, file)
	}
	return nil
}

func readDetector(ctx context.Context, s *driver.StockSession, d detector, shot bool, shotPath string) (string, error) {
	dctx, cancel := context.WithTimeout(ctx, d.max+30*time.Second)
	defer cancel()
	pg, err := s.NewPage(dctx, "about:blank")
	if err != nil {
		return "", err
	}
	nctx, ncancel := context.WithTimeout(dctx, 30*time.Second)
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
	if d.name == "rebrowser" {
		// The page keeps its verdicts in window.detections; innerText loses the colours.
		var js string
		if pg.Eval(dctx, `JSON.stringify((window.detections||[]).map(d => ({type: d.type, rating: d.rating, note: String(d.note).replace(/<[^>]+>/g, '')})))`, &js) == nil {
			text += "\n--JSON--\n" + js
		}
	}
	if d.name == "creepjs" {
		var js string
		if pg.Eval(dctx, `JSON.stringify({lies: (window.Fingerprint && window.Fingerprint.lies) || null, trust: (window.Fingerprint && window.Fingerprint.trustScore) || null})`, &js) == nil {
			text += "\n--JSON--\n" + js
		}
	}
	if shot {
		_ = pg.Screenshot(dctx, shotPath)
	}
	if navErr != nil && text == "" {
		return text, navErr
	}
	return text, nil
}

const probeJS = `function () {
  const mk = (w, h) => typeof document !== 'undefined'
    ? Object.assign(document.createElement('canvas'), { width: w, height: h }) : new OffscreenCanvas(w, h);
  const out = { hw: navigator.hardwareConcurrency, mem: navigator.deviceMemory, plat: navigator.platform,
                lang: navigator.language, ua: navigator.userAgent, gpu: '', px: 0 };
  try {
    const g = mk(8, 8).getContext('webgl'), e = g.getExtension('WEBGL_debug_renderer_info');
    out.gpu = g.getParameter(e.UNMASKED_RENDERER_WEBGL);
  } catch (_) {}
  const x = mk(32, 32).getContext('2d');
  x.fillStyle = '#336699'; x.fillRect(0, 0, 32, 32); x.fillStyle = '#c86400'; x.fillRect(4, 4, 20, 20);
  const d = x.getImageData(0, 0, 32, 32).data;
  let h = 0; for (let i = 0; i < d.length; i++) h = (Math.imul(h, 31) + d[i]) | 0;
  out.px = h;
  return out;
}`

// The page records what its very first script sees, before anything else
// runs; that is where a late injector would leak the real values.
const firstScriptPage = `<!doctype html><title>first</title><script>window.__first = (` + probeJS + `)();</script><body>x</body>`

const workerJS = `(async () => {
  const probe = ` + probeJS + `;
  const w = new Worker(URL.createObjectURL(new Blob(['postMessage((' + probe.toString() + ')())'])));
  return await new Promise((ok, no) => { w.onmessage = (e) => ok(e.data); w.onerror = (e) => no(new Error(String(e.message))); });
})()`

type probeOut struct {
	HW   int    `json:"hw"`
	Mem  int    `json:"mem"`
	Plat string `json:"plat"`
	Lang string `json:"lang"`
	UA   string `json:"ua"`
	GPU  string `json:"gpu"`
	Px   int32  `json:"px"`
}

func runStockLaunches(args []string) error {
	fs := flag.NewFlagSet("stocklaunches", flag.ExitOnError)
	engine := fs.String("engine", "chrome", "chrome | edge | cft")
	cft := fs.String("cft", "", "chrome.exe of a CfT build")
	n := fs.Int("n", 20, "fresh launches")
	seed := fs.Uint64("seed", 1000, "first seed")
	fs.Parse(args)

	in, err := pickInstall(*engine, *cft)
	if err != nil {
		return err
	}
	version, _ := driver.StockChrome{}.Version(in)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, firstScriptPage) }))
	defer srv.Close()
	l, err := driver.NewLauncher()
	if err != nil {
		return err
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Real values of this machine, from a bare launch.
	real, err := bareProbe(ctx, l, in, version, srv.URL)
	if err != nil {
		return fmt.Errorf("bare probe: %w", err)
	}
	fmt.Printf("ENGINE %s version=%s real: hw=%d mem=%d gpu=%q px=%d\n", *engine, version, real.HW, real.Mem, real.GPU, real.Px)
	fmt.Println("i  launch_ms verify_ms attempts hash             first_script_leak worker_ok page_ok")

	var launchMs []float64
	failures, leaks, workerBad, noGL := 0, 0, 0, 0
	for i := 0; i < *n; i++ {
		fp := stockFingerprint(*seed+uint64(i), in, version)
		p := profile.Profile{ID: "launches", DataDir: filepath.Join(os.TempDir(), fmt.Sprintf("wm-l-%s-%d-%d", *engine, i, time.Now().UnixNano())), Fingerprint: fp}
		t0 := time.Now()
		s, err := l.LaunchStockUnverified(ctx, driver.StockChrome{}, in, p, "")
		if err != nil {
			fmt.Printf("%-2d launch failed: %v\n", i, err)
			failures++
			os.RemoveAll(p.DataDir)
			continue
		}
		launch := time.Since(t0)
		t1 := time.Now()
		hash, verr := s.Verify(ctx)
		verify := time.Since(t1)
		pg, perr := s.WaitPage(ctx)
		var first, later, worker probeOut
		if perr == nil && pg.Navigate(ctx, srv.URL) == nil {
			_ = pg.Eval(ctx, `window.__first`, &first)
			_ = pg.Eval(ctx, "("+probeJS+")()", &later)
			_ = pg.Eval(ctx, workerJS, &worker)
		}
		leak := first.HW != fp.HardwareThreads || first.Mem != fp.DeviceMemoryGB || (first.GPU != "" && first.GPU != fp.GPURenderer) || first.Px != later.Px || first.Plat != "Win32"
		workerOK := worker.HW == fp.HardwareThreads && worker.Mem == fp.DeviceMemoryGB && (worker.GPU == "" || worker.GPU == fp.GPURenderer) && worker.Plat == "Win32" && worker.UA == fp.UserAgent && worker.Px == later.Px
		pageOK := later.HW == fp.HardwareThreads && later.Mem == fp.DeviceMemoryGB && (later.GPU == "" || later.GPU == fp.GPURenderer)
		if later.GPU == "" {
			noGL++
		}
		if verr != nil {
			hash = "ERR:" + verr.Error()
		}
		if leak {
			leaks++
		}
		if !workerOK {
			workerBad++
			fmt.Printf("   worker mismatch: %+v want hw=%d mem=%d gpu=%q\n", worker, fp.HardwareThreads, fp.DeviceMemoryGB, fp.GPURenderer)
		}
		launchMs = append(launchMs, float64(launch.Milliseconds()))
		fmt.Printf("%-2d %9d %9d %8d %-16s %-17v %-9v %v\n", i, launch.Milliseconds(), verify.Milliseconds(), 1, hash, leak, workerOK, pageOK)
		_ = s.Close()
		os.RemoveAll(p.DataDir)
	}
	sort.Float64s(launchMs)
	if len(launchMs) > 0 {
		fmt.Printf("SUMMARY launches=%d failed=%d firstScriptLeaks=%d workerMismatch=%d noWebGL=%d launch_ms min=%.0f median=%.0f max=%.0f\n",
			*n, failures, leaks, workerBad, noGL, launchMs[0], launchMs[len(launchMs)/2], launchMs[len(launchMs)-1])
	}
	return nil
}

func bareProbe(ctx context.Context, l *driver.Launcher, in driver.Install, version, url string) (probeOut, error) {
	var out probeOut
	fp := stockFingerprint(1, in, version)
	p := profile.Profile{ID: "bare", DataDir: filepath.Join(os.TempDir(), fmt.Sprintf("wm-bare-%d", time.Now().UnixNano())), Fingerprint: fp}
	defer os.RemoveAll(p.DataDir)
	s, err := l.LaunchStockBare(ctx, driver.StockChrome{}, in, p, "")
	if err != nil {
		return out, err
	}
	defer s.Close()
	pg, err := s.WaitPage(ctx)
	if err != nil {
		return out, err
	}
	if err := pg.Navigate(ctx, url); err != nil {
		return out, err
	}
	return out, pg.Eval(ctx, "("+probeJS+")()", &out)
}

// ---- Chrome for Testing ---------------------------------------------------

const extManifest = `{"manifest_version":3,"name":"wm-cft-probe","version":"1.0",
 "content_scripts":[{"matches":["http://127.0.0.1/*"],"js":["m.js"],"run_at":"document_start","world":"MAIN","all_frames":true}]}`
const extScript = `window.__wmExt = 'main-world';`

func runCfT(args []string) error {
	fs := flag.NewFlagSet("cft", flag.ExitOnError)
	cft := fs.String("cft", "", "chrome.exe of the CfT build")
	fs.Parse(args)
	in, err := pickInstall("cft", *cft)
	if err != nil {
		return err
	}
	version, _ := driver.StockChrome{}.Version(in)
	fmt.Printf("CFT %s version=%s\n", in.Path, version)
	extDir, err := os.MkdirTemp("", "wm-ext-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(extDir)
	_ = os.WriteFile(filepath.Join(extDir, "manifest.json"), []byte(extManifest), 0o644)
	_ = os.WriteFile(filepath.Join(extDir, "m.js"), []byte(extScript), 0o644)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<!doctype html><title>cft</title>ok") }))
	defer srv.Close()
	l, err := driver.NewLauncher()
	if err != nil {
		return err
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dir := func(tag string) string {
		return filepath.Join(os.TempDir(), fmt.Sprintf("wm-cft-%s-%d", tag, time.Now().UnixNano()))
	}
	// A: --load-extension, with the injector on.
	{
		fp := stockFingerprint(5, in, version)
		p := profile.Profile{ID: "cftA", DataDir: dir("a"), Fingerprint: fp}
		defer os.RemoveAll(p.DataDir)
		eng := driver.WithExtraArgs(driver.StockChrome{}, "--load-extension="+extDir)
		t0 := time.Now()
		s, err := l.LaunchStockUnverified(ctx, eng, in, p, "")
		if err != nil {
			fmt.Println("A launch failed:", err)
		} else {
			var ver struct {
				Product string `json:"product"`
			}
			cdpErr := s.Call(ctx, "Browser.getVersion", nil, &ver)
			fmt.Printf("A launch_ms=%d cdp_answers=%v product=%s\n", time.Since(t0).Milliseconds(), cdpErr == nil, ver.Product)
			pg, _ := s.WaitPage(ctx)
			if pg != nil && pg.Navigate(ctx, srv.URL) == nil {
				var marker string
				var hw int
				_ = pg.Eval(ctx, `window.__wmExt || 'absent'`, &marker)
				_ = pg.Eval(ctx, `navigator.hardwareConcurrency`, &hw)
				fmt.Printf("A load-extension content script (MAIN world): %s; injected hw=%d want %d\n", marker, hw, fp.HardwareThreads)
			}
			_ = s.Close()
		}
	}
	// B: Extensions.loadUnpacked over the pipe, bare, 20 s bound.
	{
		p := profile.Profile{ID: "cftB", DataDir: dir("b"), Fingerprint: stockFingerprint(6, in, version)}
		defer os.RemoveAll(p.DataDir)
		eng := driver.WithExtraArgs(driver.StockChrome{}, "--enable-unsafe-extension-debugging")
		s, err := l.LaunchStockBare(ctx, eng, in, p, "")
		if err != nil {
			fmt.Println("B launch failed:", err)
		} else {
			cctx, ccancel := context.WithTimeout(ctx, 20*time.Second)
			t0 := time.Now()
			var res struct {
				ID string `json:"id"`
			}
			err := s.Call(cctx, "Extensions.loadUnpacked", map[string]string{"path": extDir}, &res)
			ccancel()
			status := "ok id=" + res.ID
			if err != nil {
				status = "error/hang: " + err.Error()
			}
			fmt.Printf("B Extensions.loadUnpacked after %dms: %s\n", time.Since(t0).Milliseconds(), status)
			if err == nil {
				pg, _ := s.WaitPage(ctx)
				if pg != nil && pg.Navigate(ctx, srv.URL) == nil {
					var marker string
					_ = pg.Eval(ctx, `window.__wmExt || 'absent'`, &marker)
					fmt.Printf("B content script after loadUnpacked: %s\n", marker)
				}
			}
			_ = s.Close()
		}
	}
	b, _ := json.Marshal(map[string]string{"version": version})
	fmt.Println("CFTDONE", string(b))
	return nil
}
