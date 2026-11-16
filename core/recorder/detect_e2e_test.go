package recorder_test

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/recorder"
	"github.com/8bu/ladilala/core/recorder/rectest"
)

// moScript is a page-level MutationObserver armed before the page's own
// scripts, standing in for a page that watches its own DOM.
const moScript = `(() => {
  const log = [];
  Object.defineProperty(window, '__probeMO', { value: log, enumerable: false });
  const mo = new MutationObserver((rs) => { for (const r of rs) for (const n of r.addedNodes) log.push(n.nodeName.toLowerCase()); });
  mo.observe(document, { childList: true, subtree: true });
  Object.defineProperty(window, '__probeClicks', { value: [], enumerable: false });
  window.addEventListener('click', (e) => window.__probeClicks.push(e.target.localName), true);
})()`

const probeJS = `(() => {
  const fns = { addEventListener: EventTarget.prototype.addEventListener, querySelector: document.querySelector,
    attachShadow: Element.prototype.attachShadow, fetch: window.fetch, fnToString: Function.prototype.toString,
    MutationObserver: window.MutationObserver, createElement: document.createElement, click: HTMLElement.prototype.click,
    getComputedStyle: window.getComputedStyle, requestAnimationFrame: window.requestAnimationFrame };
  const ts = {};
  for (const k in fns) ts[k] = Function.prototype.toString.call(fns[k]);
  let bindingCall = 'no-throw';
  try { window.ladiEmit('x'); } catch (e) { bindingCall = e.constructor.name; }
  const root = document.documentElement;
  return {
    props: Object.getOwnPropertyNames(window).filter((n) => !n.startsWith('__probe')).sort(),
    docProps: Object.getOwnPropertyNames(document).sort(),
    rootChildren: Array.from(root.children).map((e) => e.localName),
    allCount: document.querySelectorAll('*').length,
    docAll: document.all.length,
    htmlLen: root.outerHTML.length,
    moAdded: window.__probeMO,
    globals: { ladiEmit: typeof window.ladiEmit, ladiRec: typeof window.__ladiRec, ladiLoc: typeof window.__ladiLoc, ladiFlush: typeof window.__ladiFlush, hasEmit: 'ladiEmit' in window },
    bindingCall,
    toStrings: ts,
    getEventListenersInPage: typeof getEventListeners,
    clicks: window.__probeClicks,
    vw: innerWidth,
  };
})()`

type probeResult struct {
	Props, DocProps, RootChildren []string
	AllCount, DocAll, HTMLLen     int
	MoAdded                       []string
	Globals                       map[string]any
	BindingCall                   string
	ToStrings                     map[string]string
	GetEventListenersInPage       string
	Clicks                        []string
	VW                            float64
}

func runProbe(t *testing.T, ctx context.Context, site *recorder.Site, withRecorder bool) (probeResult, map[string]int, map[string]any, *recorder.Recorder) {
	t.Helper()
	var rec *recorder.Recorder
	hook := func(c context.Context, pg *driver.Page) error {
		if err := pg.Call(c, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": moScript, "runImmediately": true}, nil); err != nil {
			return err
		}
		if rec != nil {
			return rec.Hook(c, pg)
		}
		return nil
	}
	if withRecorder {
		rec = recorder.New(recorder.Options{ID: "probe"})
	}
	b, err := rectest.Launch(ctx, e2eBrowser(), 31, hook)
	if err != nil {
		t.Skipf("launch: %v", err)
	}
	t.Cleanup(b.Close)
	pg, err := b.Session.WaitPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := pg.Navigate(ctx, site.URL("form.html")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	u := rectest.NewUser(ctx, b.Session, pg)
	// Hover and click around the toolbar corner: does the page see events on our host?
	for _, dx := range []float64{20, 50, 80, 110, 140} {
		_ = u.MoveTo(1000-dx, 20)
	}
	var vw float64
	_ = pg.Eval(ctx, "innerWidth", &vw)
	for _, dx := range []float64{130, 100, 60, 30} {
		_ = u.ClickAtPoint(vw-dx, 20)
	}
	var res probeResult
	if err := pg.Eval(ctx, probeJS, &res); err != nil {
		t.Fatal(err)
	}
	// getEventListeners exists only with the command-line API; ask for it the
	// way DevTools does and count the listeners it reports on window.
	var el struct {
		Result struct {
			Value map[string]int `json:"value"`
		} `json:"result"`
	}
	_ = pg.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression": "Object.fromEntries(Object.entries(getEventListeners(window)).map(([k, v]) => [k, v.length]))", "includeCommandLineAPI": true, "returnByValue": true,
	}, &el)
	extra := map[string]any{"windowListeners": el.Result.Value}
	return res, b.Session.SentMethods(), extra, rec
}

func diff(a, b []string) (onlyA, onlyB []string) {
	in := func(s []string, v string) bool {
		for _, x := range s {
			if x == v {
				return true
			}
		}
		return false
	}
	for _, v := range a {
		if !in(b, v) {
			onlyA = append(onlyA, v)
		}
	}
	for _, v := range b {
		if !in(a, v) {
			onlyB = append(onlyB, v)
		}
	}
	return
}

// TestE2ERecordDetectability compares a page in a browser with the recorder
// overlay against the same page in a control browser (same injector, no
// recorder), from the main world, and reports what the page can see.
func TestE2ERecordDetectability(t *testing.T) {
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	site, err := recorder.NewSite()
	if err != nil {
		t.Fatal(err)
	}
	defer site.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ctrl, ctrlSent, ctrlExtra, _ := runProbe(t, ctx, site, false)
	got, sent, extra, rec := runProbe(t, ctx, site, true)

	report := func(name string, pass bool, detail string) {
		verdict := "PASS"
		if !pass {
			verdict = "SEEN"
		}
		t.Logf("%-34s %s  %s", name, verdict, detail)
	}
	oc, nw := diff(ctrl.Props, got.Props)
	report("window own properties unchanged", len(oc)+len(nw) == 0, "removed="+strings.Join(oc, ",")+" added="+strings.Join(nw, ","))
	if len(oc)+len(nw) != 0 {
		t.Errorf("window properties differ: -%v +%v", oc, nw)
	}
	od, nd := diff(ctrl.DocProps, got.DocProps)
	report("document own properties unchanged", len(od)+len(nd) == 0, "removed="+strings.Join(od, ",")+" added="+strings.Join(nd, ","))
	if len(od)+len(nd) != 0 {
		t.Errorf("document properties differ: -%v +%v", od, nd)
	}
	same := true
	for k, v := range ctrl.ToStrings {
		if got.ToStrings[k] != v || !strings.Contains(v, "[native code]") {
			same = false
			t.Errorf("Function.prototype.toString of %s changed or not native: %q", k, got.ToStrings[k])
		}
	}
	report("native toString unchanged", same, "10 natives compared with the control")
	g := got.Globals
	noGlobals := g["ladiEmit"] == "undefined" && g["ladiRec"] == "undefined" && g["ladiLoc"] == "undefined" && g["ladiFlush"] == "undefined" && g["hasEmit"] == false
	report("no recorder globals in main world", noGlobals, "")
	if !noGlobals {
		t.Errorf("globals visible: %v", g)
	}
	report("page cannot call the binding", got.BindingCall == "TypeError", "window.ladiEmit('x') -> "+got.BindingCall)
	if got.BindingCall != "TypeError" {
		t.Errorf("page could call the binding: %s", got.BindingCall)
	}
	report("getEventListeners not in page", got.GetEventListenersInPage == "undefined", "typeof getEventListeners = "+got.GetEventListenersInPage)
	lc, _ := json.Marshal(ctrlExtra["windowListeners"])
	lr, _ := json.Marshal(extra["windowListeners"])
	report("DevTools getEventListeners(window)", string(lc) == string(lr), "control="+string(lc)+" recorder="+string(lr)+" (console API, not callable by a page)")

	// What a page CAN see: the overlay host element.
	extraChildren, _ := diff(got.RootChildren, ctrl.RootChildren)
	report("host element in <html> children", len(extraChildren) == 0, "extra children of <html>: "+strings.Join(extraChildren, ",")+
		"; querySelectorAll('*') "+itoa(ctrl.AllCount)+" -> "+itoa(got.AllCount)+"; document.all "+itoa(ctrl.DocAll)+" -> "+itoa(got.DocAll)+"; outerHTML +"+itoa(got.HTMLLen-ctrl.HTMLLen)+" chars")
	moNew, _ := diff(got.MoAdded, ctrl.MoAdded)
	report("MutationObserver on document", len(moNew) == 0, "added nodes seen by a page observer, not in control: "+strings.Join(moNew, ","))
	sort.Strings(got.Clicks)
	hostClicks := 0
	for _, c := range got.Clicks {
		if strings.HasPrefix(c, "ladi-") {
			hostClicks++
		}
	}
	report("page click listener sees toolbar", hostClicks == 0, "clicks with target=host element: "+itoa(hostClicks)+" of 4 clicks aimed at the toolbar corner")

	report("Runtime.enable never sent", sent["Runtime.enable"] == 0, "")
	if sent["Runtime.enable"] != 0 || ctrlSent["Runtime.enable"] != 0 {
		t.Errorf("Runtime.enable was sent")
	}
	banned := []string{"Console.enable", "Debugger.enable", "Log.enable"}
	for _, m := range banned {
		if sent[m] != 0 {
			t.Errorf("%s was sent", m)
		}
	}
	stopped := false
	select {
	case <-rec.Done():
		stopped = true
	case <-time.After(500 * time.Millisecond):
	}
	t.Logf("toolbar buttons: a click aimed at the toolbar reached Go as Stop: %v", stopped)
	sj, _ := json.Marshal(sent)
	t.Logf("recorder session methods: %s", sj)
	if errs := rec.Errors(); len(errs) > 0 {
		t.Errorf("recorder errors: %v", errs)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
