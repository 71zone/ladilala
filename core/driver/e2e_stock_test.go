package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/inject"
	"github.com/8bu/ladilala/core/profile"
)

// probeSrc reads what a script in the current scope can see. It runs in the
// page, in iframes and in a dedicated worker, so it avoids DOM-only calls.
const probeSrc = `function () {
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

const stockPageJS = `(async () => {
  const probe = ` + probeSrc + `;
  const res = { page: probe() };
  const srcdoc = document.createElement('iframe'); srcdoc.srcdoc = '<p>x</p>';
  document.body.appendChild(srcdoc);
  res.srcdoc = srcdoc.contentWindow.eval('(' + probe.toString() + ')()');
  const blank = document.createElement('iframe'); document.body.appendChild(blank);
  res.blank = blank.contentWindow.eval('(' + probe.toString() + ')()');
  const w = new Worker(URL.createObjectURL(new Blob(['postMessage((' + probe.toString() + ')())'])));
  res.worker = await new Promise((ok, no) => { w.onmessage = (e) => ok(e.data); w.onerror = (e) => no(new Error(String(e.message))); });
  return res;
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

// TestE2EStockChrome launches stock Chrome and Edge through LaunchStock and
// checks that the page, srcdoc and about:blank iframes and a dedicated worker
// all report the profile. Runs only with LADILALA_E2E=1.
func TestE2EStockChrome(t *testing.T) {
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	installs, _ := StockChrome{}.Detect()
	if len(installs) == 0 {
		t.Skip("no Chrome or Edge installed")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<!doctype html><title>stock</title><body>hi</body>")
	}))
	defer srv.Close()

	l, err := NewLauncher()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	seen := map[string]bool{}
	for _, in := range installs {
		if seen[in.Brand] {
			continue
		}
		seen[in.Brand] = true
		t.Run(in.Brand, func(t *testing.T) {
			fp := profile.NewFingerprint(42, profile.Options{Platform: profile.PlatformWindows, Language: "de-DE", Timezone: "Europe/Berlin"})
			p := profile.Profile{ID: "stock-" + in.Brand, DataDir: filepath.Join(t.TempDir(), "p"), Fingerprint: fp}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			s, res, err := l.LaunchStock(ctx, StockChrome{}, in, p, "")
			if err != nil {
				t.Fatalf("launch: %v", err)
			}
			defer s.Close()
			t.Logf("%s verified: attempts=%d hash=%s", in.Brand, res.Attempts, res.Hash)

			pg, err := s.WaitPage(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := pg.Navigate(ctx, srv.URL); err != nil {
				t.Fatal(err)
			}
			var got map[string]probeOut
			if err := pg.Eval(ctx, stockPageJS, &got); err != nil {
				t.Fatal(err)
			}
			cfg := inject.NewConfig(fp)
			for _, scope := range []string{"page", "srcdoc", "blank", "worker"} {
				o := got[scope]
				if o.HW != fp.HardwareThreads || o.Mem != fp.DeviceMemoryGB || o.Plat != cfg.NavigatorPlatform || o.Lang != "de-DE" {
					t.Errorf("%s: %+v does not match the profile (hw %d mem %d)", scope, o, fp.HardwareThreads, fp.DeviceMemoryGB)
				}
				if o.GPU != "" && o.GPU != fp.GPURenderer {
					t.Errorf("%s: GPU %q, want %q", scope, o.GPU, fp.GPURenderer)
				}
				if o.UA != fp.UserAgent {
					t.Errorf("%s: UA %q, want %q", scope, o.UA, fp.UserAgent)
				}
				if o.Px != got["page"].Px {
					t.Errorf("%s: canvas pixels %d differ from page %d", scope, o.Px, got["page"].Px)
				}
			}
			sent := s.SentMethods()
			for _, banned := range []string{"Runtime.enable", "Runtime.addBinding", "Console.enable", "Debugger.enable"} {
				if sent[banned] > 0 {
					t.Errorf("%s was sent %d times", banned, sent[banned])
				}
			}
			if sent["Page.addScriptToEvaluateOnNewDocument"] == 0 || sent["Runtime.runIfWaitingForDebugger"] == 0 {
				t.Errorf("expected injection and resume commands, sent %v", sent)
			}
			if errs := s.SetupErrors(); len(errs) > 0 {
				t.Errorf("setup errors: %v", errs)
			}
			b, _ := json.Marshal(got)
			t.Logf("%s", b)
		})
	}
}
