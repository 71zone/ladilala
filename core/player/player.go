// Package player runs a macro (schema v1) on a page of a driver.StockSession.
//
// Locating and reading happen in the player's own isolated world per frame;
// clicks and keys are real input events from CDP (Input.dispatchMouseEvent,
// Input.dispatchKeyEvent, Input.insertText). The page gets no script, no
// Runtime.enable and no property in its main world. There is no step that runs
// arbitrary JavaScript.
package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/recorder"
)

const (
	worldName      = "ladilala-player"
	defaultTimeout = 10 * time.Second
	navTimeout     = 30 * time.Second
	pollEvery      = 100 * time.Millisecond
)

// Player runs macros on one tab.
type Player struct {
	Session *driver.StockSession
	Page    *driver.Page
	// Log, when set, receives one line per step.
	Log func(format string, args ...any)
	// Hooks, when set, lets the run engine gate steps and commit points.
	Hooks Hooks

	mu        sync.Mutex
	worlds    map[worldKey]int
	committed bool // BeforeCommit returned nil for the step in progress
}

type worldKey struct{ session, frame string }

// New returns a Player for the tab pg of s.
func New(s *driver.StockSession, pg *driver.Page) *Player {
	return &Player{Session: s, Page: pg, worlds: map[worldKey]int{}}
}

func (p *Player) logf(format string, args ...any) {
	if p.Log != nil {
		p.Log(format, args...)
	}
}

// frame is one document the player acts in. base is the offset of the
// frame's process root inside the tab's viewport: zero for the tab and its
// same-process frames, the owner iframe's position for an out-of-process frame.
type frame struct {
	pg           *driver.Page
	id           string
	baseX, baseY float64
}

// Run executes m for one data row. The row is checked first (Preflight), so a
// missing column or a forbidden origin fails before the browser is touched.
func (p *Player) Run(ctx context.Context, m *macro.Macro, row map[string]string) error {
	vars, err := Preflight(m, row)
	if err != nil {
		return err
	}
	// Lifecycle events tell when a navigation an action caused has loaded.
	_ = p.Page.Call(ctx, "Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}, nil)
	for i := range m.Steps {
		s := &m.Steps[i]
		if s.Disabled {
			continue
		}
		if len(m.Guards) > 0 {
			if err := p.CheckGuards(ctx, m); err != nil {
				return err
			}
		}
		if p.Hooks.BeforeStep != nil {
			if err := p.Hooks.BeforeStep(ctx, i, s); err != nil {
				return err
			}
		}
		var last error
		p.committed = false
		for attempt := 0; attempt <= s.Retries; attempt++ {
			t0 := time.Now()
			last = p.runStep(ctx, m, s, vars)
			p.logf("%s %s attempt=%d err=%v (%s)", s.ID, s.Action, attempt, last, time.Since(t0).Round(time.Millisecond))
			if last == nil || p.committed || ctx.Err() != nil || errors.Is(last, ErrOrigin) || errors.Is(last, ErrMissingColumn) || errors.Is(last, ErrBadValue) {
				break
			}
		}
		if p.committed && p.Hooks.AfterCommit != nil {
			p.Hooks.AfterCommit(ctx, s, last)
		}
		if last != nil {
			if s.OnFail == macro.OnFailSkip && ctx.Err() == nil && !p.committed {
				continue
			}
			return &StepError{StepID: s.ID, Action: s.Action, Err: last}
		}
	}
	return nil
}

// commit runs the BeforeCommit hook for a commit point step. skip is true in
// a dry run: the step does nothing.
func (p *Player) commit(ctx context.Context, s *macro.Step) (skip bool, err error) {
	if !s.CommitPoint {
		return false, nil
	}
	if p.Hooks.DryRun {
		p.logf("%s dry run: commit point skipped", s.ID)
		return true, nil
	}
	if p.Hooks.BeforeCommit != nil {
		if err := p.Hooks.BeforeCommit(ctx, s); err != nil {
			return false, err
		}
	}
	p.committed = true
	return false, nil
}

func (p *Player) runStep(ctx context.Context, m *macro.Macro, s *macro.Step, vars map[string]string) error {
	timeout := time.Duration(s.Timeout)
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var value string
	if s.Value != nil {
		var err error
		if value, err = evalExpr(s.ID, *s.Value, vars); err != nil {
			return err
		}
	}
	switch s.Action {
	case macro.ActionNavigate:
		if !m.AllowsURL(value) {
			return &OriginError{URL: value, Step: s.ID}
		}
		nctx, ncancel := context.WithTimeout(ctx, navTimeout)
		defer ncancel()
		if err := p.Page.Navigate(nctx, value); err != nil {
			return err
		}
	case macro.ActionWait:
		if s.Target == nil {
			select {
			case <-time.After(timeout):
			case <-ctx.Done():
				return ctx.Err()
			}
			break
		}
		if _, _, err := p.find(sctx, m, s, vars, false); err != nil {
			return err
		}
	case macro.ActionClick:
		fr, res, err := p.find(sctx, m, s, vars, true)
		if err != nil {
			return err
		}
		if skip, err := p.commit(sctx, s); err != nil {
			return err
		} else if skip {
			return nil
		}
		if err := p.clickAt(sctx, fr, res, 1); err != nil {
			return err
		}
	case macro.ActionDblClick:
		fr, res, err := p.find(sctx, m, s, vars, true)
		if err != nil {
			return err
		}
		if err := p.clickAt(sctx, fr, res, 2); err != nil {
			return err
		}
	case macro.ActionFill:
		fr, res, err := p.find(sctx, m, s, vars, true)
		if err != nil {
			return err
		}
		if skip, err := p.commit(sctx, s); err != nil {
			return err
		} else if skip {
			return nil
		}
		if err := p.fill(sctx, fr, res, s.ID, value); err != nil {
			return err
		}
	case macro.ActionSelect:
		fr, _, err := p.find(sctx, m, s, vars, false)
		if err != nil {
			return err
		}
		if err := p.selectOption(sctx, fr, value); err != nil {
			return err
		}
	case macro.ActionCheck:
		fr, res, err := p.find(sctx, m, s, vars, true)
		if err != nil {
			return err
		}
		if err := p.check(sctx, fr, res, value != "false"); err != nil {
			return err
		}
	case macro.ActionPress:
		var fr *frame
		if s.Target != nil {
			var err error
			if fr, _, err = p.find(sctx, m, s, vars, false); err != nil {
				return err
			}
			if err := p.evalIn(sctx, fr, `(function(){globalThis.__ladiEl.focus();return true})()`, nil); err != nil {
				return err
			}
		}
		if skip, err := p.commit(sctx, s); err != nil {
			return err
		} else if skip {
			return nil
		}
		if err := p.press(sctx, value); err != nil {
			return err
		}
	case macro.ActionScroll:
		if err := p.scroll(sctx, m, s, vars, value); err != nil {
			return err
		}
	case macro.ActionManual:
		if p.Hooks.Manual == nil {
			return fmt.Errorf("%w: %s", ErrUnsupported, s.Action)
		}
		return p.Hooks.Manual(ctx, s, value)
	case macro.ActionAssert:
		// Expectations below are the whole step.
	default:
		return fmt.Errorf("%w: %s", ErrUnsupported, s.Action)
	}
	for _, e := range s.Expect {
		if err := p.expect(sctx, m, s, vars, e); err != nil {
			return err
		}
	}
	return nil
}

// ---- finding -----------------------------------------------------------------

type located struct {
	Status  string  `json:"status"`
	Index   int     `json:"index"`
	Count   int     `json:"count"`
	What    string  `json:"what"`
	Tried   []Tried `json:"tried"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Covered string  `json:"covered"`
	Tag     string  `json:"tag"`
	Origin  string  `json:"origin"`
}

// resolveTarget renders the variables inside a target's locators.
func resolveTarget(stepID string, t *macro.Target, vars map[string]string) (*macro.Target, error) {
	out := &macro.Target{FramePath: t.FramePath, ShadowPath: t.ShadowPath}
	for _, l := range t.Locators {
		v, err := evalExpr(stepID, macro.Value{Expr: l.Value}, vars)
		if err != nil {
			return nil, err
		}
		n := ""
		if l.Name != "" {
			if n, err = evalExpr(stepID, macro.Value{Expr: l.Name}, vars); err != nil {
				return nil, err
			}
		}
		out.Locators = append(out.Locators, macro.Locator{Kind: l.Kind, Value: v, Name: n})
	}
	return out, nil
}

// find waits until s.Target resolves to exactly one element (and, with
// wantPoint, one whose centre no other element covers). The element stays in
// the frame's isolated world as __ladiEl for the action that follows.
func (p *Player) find(ctx context.Context, m *macro.Macro, s *macro.Step, vars map[string]string, wantPoint bool) (*frame, *located, error) {
	if s.Target == nil {
		return nil, nil, errors.New("step has no target")
	}
	t, err := resolveTarget(s.ID, s.Target, vars)
	if err != nil {
		return nil, nil, err
	}
	js, _ := json.Marshal(map[string]any{"locators": t.Locators, "shadowPath": t.ShadowPath})
	expr := fmt.Sprintf(`(function(){const r=__ladiLoc.resolve(%s);
const o={status:r.status,index:r.index||0,count:r.count||0,what:r.what||'',tried:r.tried||[],origin:location.origin};
if(r.status!=='ok')return o;globalThis.__ladiEl=r.el;o.tag=r.el.localName;
if(%v){const c=__ladiLoc.centre(r.el);o.x=c.x;o.y=c.y;o.covered=c.covered}return o})()`, js, wantPoint)
	var last error
	for {
		fr, err := p.enter(ctx, t.FramePath)
		if err == nil {
			var res located
			if err = p.evalIn(ctx, fr, expr, &res); err == nil {
				switch {
				case res.Status == "ok" && res.Covered != "":
					last = &LocatorError{Reason: ErrObscured, Tried: res.Tried, Detail: res.Covered}
				case res.Status == "ok":
					if !m.AllowsURL(res.Origin + "/") {
						return nil, nil, &OriginError{URL: res.Origin, Step: s.ID}
					}
					return fr, &res, nil
				case res.Status == "ambiguous":
					last = &LocatorError{Reason: ErrAmbiguous, Tried: res.Tried, Count: res.Count, Detail: res.What}
				default:
					last = &LocatorError{Reason: ErrNotFound, Tried: res.Tried, Detail: res.What}
				}
			}
		}
		if err != nil {
			last = &LocatorError{Reason: ErrNotFound, Detail: err.Error()}
		}
		select {
		case <-ctx.Done():
			return nil, nil, last
		case <-time.After(pollEvery):
		}
	}
}

// Locate resolves a target once, without waiting, and reports how many
// elements each locator matched. Measurements use it.
func (p *Player) Locate(ctx context.Context, t *macro.Target) (status string, tried []Tried, err error) {
	js, _ := json.Marshal(map[string]any{"locators": t.Locators, "shadowPath": t.ShadowPath})
	fr, err := p.enter(ctx, t.FramePath)
	if err != nil {
		return "", nil, err
	}
	var res located
	expr := fmt.Sprintf(`(function(){const r=__ladiLoc.resolve(%s);return{status:r.status,tried:r.tried||[]}})()`, js)
	if err := p.evalIn(ctx, fr, expr, &res); err != nil {
		return "", nil, err
	}
	return res.Status, res.Tried, nil
}

// ---- frames and worlds --------------------------------------------------------

// enter returns the frame at the end of framePath, outermost iframe first.
func (p *Player) enter(ctx context.Context, path []string) (*frame, error) {
	fr := &frame{pg: p.Page, id: p.Page.TargetID}
	for _, sel := range path {
		q, _ := json.Marshal(sel)
		var r struct {
			N int     `json:"n"`
			X float64 `json:"x"`
			Y float64 `json:"y"`
		}
		expr := fmt.Sprintf(`(function(){const l=document.querySelectorAll(%s);if(l.length!==1)return{n:l.length};
globalThis.__ladiEl=l[0];const o=__ladiLoc.iframeOrigin(l[0]);return{n:1,x:o.x,y:o.y}})()`, q)
		if err := p.evalIn(ctx, fr, expr, &r); err != nil {
			return nil, err
		}
		if r.N != 1 {
			return nil, fmt.Errorf("frame %q matches %d elements", sel, r.N)
		}
		id, err := p.frameIDOfElement(ctx, fr)
		if err != nil {
			return nil, err
		}
		next := &frame{pg: fr.pg, id: id, baseX: fr.baseX, baseY: fr.baseY}
		for _, cand := range p.Session.Pages() {
			if cand.TargetID == id { // out-of-process frame: its own session
				next = &frame{pg: cand, id: id, baseX: fr.baseX + r.X, baseY: fr.baseY + r.Y}
				break
			}
		}
		fr = next
	}
	return fr, nil
}

func (p *Player) frameIDOfElement(ctx context.Context, fr *frame) (string, error) {
	w, err := p.world(ctx, fr, false)
	if err != nil {
		return "", err
	}
	var ev struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if err := fr.pg.Call(ctx, "Runtime.evaluate", map[string]any{"contextId": w, "expression": "globalThis.__ladiEl"}, &ev); err != nil {
		return "", err
	}
	var d struct {
		Node struct {
			FrameID string `json:"frameId"`
		} `json:"node"`
	}
	if err := fr.pg.Call(ctx, "DOM.describeNode", map[string]any{"objectId": ev.Result.ObjectID}, &d); err != nil {
		return "", err
	}
	if d.Node.FrameID == "" {
		return "", errors.New("element is not a frame owner")
	}
	return d.Node.FrameID, nil
}

// world returns the player's isolated context in fr, creating it (and loading
// the locator library) when needed or when force is set.
func (p *Player) world(ctx context.Context, fr *frame, force bool) (int, error) {
	k := worldKey{fr.pg.SessionID, fr.id}
	p.mu.Lock()
	id, ok := p.worlds[k]
	p.mu.Unlock()
	if ok && !force {
		return id, nil
	}
	var w struct {
		ExecutionContextID int `json:"executionContextId"`
	}
	if err := fr.pg.Call(ctx, "Page.createIsolatedWorld", map[string]any{"frameId": fr.id, "worldName": worldName}, &w); err != nil {
		return 0, err
	}
	if err := fr.pg.Call(ctx, "Runtime.evaluate", map[string]any{"contextId": w.ExecutionContextID, "expression": recorder.LocatorJS + ";true"}, nil); err != nil {
		return 0, err
	}
	p.mu.Lock()
	p.worlds[k] = w.ExecutionContextID
	p.mu.Unlock()
	return w.ExecutionContextID, nil
}

// evalIn runs expr in the player's world of fr and decodes the JSON result
// into out. A stale context (the frame navigated) is replaced once.
func (p *Player) evalIn(ctx context.Context, fr *frame, expr string, out any) error {
	for attempt := 0; ; attempt++ {
		w, err := p.world(ctx, fr, attempt > 0)
		if err != nil {
			return err
		}
		var res struct {
			Result struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
			ExceptionDetails *struct {
				Text      string `json:"text"`
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		err = fr.pg.Call(ctx, "Runtime.evaluate", map[string]any{
			"contextId": w, "expression": expr, "returnByValue": true, "awaitPromise": true,
		}, &res)
		if err != nil {
			if attempt == 0 && (strings.Contains(err.Error(), "context") || strings.Contains(err.Error(), "Cannot find")) {
				continue
			}
			return err
		}
		if res.ExceptionDetails != nil {
			if attempt == 0 && strings.Contains(res.ExceptionDetails.Exception.Description, "__ladi") {
				continue // library missing: the world was recreated by the page
			}
			d := res.ExceptionDetails.Exception.Description
			if d == "" {
				d = res.ExceptionDetails.Text
			}
			return errors.New("script: " + d)
		}
		if out == nil || len(res.Result.Value) == 0 {
			return nil
		}
		return json.Unmarshal(res.Result.Value, out)
	}
}

// ---- actions -------------------------------------------------------------------

func (p *Player) mouse(ctx context.Context, typ string, x, y float64, buttons, clickCount int) error {
	params := map[string]any{"type": typ, "x": x, "y": y, "button": "none", "buttons": buttons}
	if typ != "mouseMoved" {
		params["button"] = "left"
		params["clickCount"] = clickCount
	}
	return p.Page.Call(ctx, "Input.dispatchMouseEvent", params, nil)
}

// clickAt presses the left button count times at the point of res. Count 2
// is a double click: the second press carries clickCount 2, so the page sees
// mousedown, mouseup, click, mousedown, mouseup, click, dblclick, as it does
// for a person.
func (p *Player) clickAt(ctx context.Context, fr *frame, res *located, count int) error {
	w := p.watchNav()
	defer w.stop()
	x, y := res.X+fr.baseX, res.Y+fr.baseY
	if err := p.mouse(ctx, "mouseMoved", x, y, 0, 0); err != nil {
		return err
	}
	for n := 1; n <= count; n++ {
		if err := p.mouse(ctx, "mousePressed", x, y, 1, n); err != nil {
			return err
		}
		if err := p.mouse(ctx, "mouseReleased", x, y, 0, n); err != nil {
			return err
		}
	}
	return w.wait(ctx)
}

func (p *Player) fill(ctx context.Context, fr *frame, res *located, stepID, text string) error {
	var kind struct {
		Type  string `json:"type"`
		Field string `json:"field"`
	}
	if err := p.evalIn(ctx, fr, `(function(){const el=globalThis.__ladiEl;const l=el.labels&&el.labels[0];
return{type:el.localName==='input'?el.type:'',field:el.name||el.id||el.getAttribute('aria-label')||(l?l.textContent.trim():'')}})()`, &kind); err != nil {
		return err
	}
	if _, ok := dateInputWant[kind.Type]; ok {
		return p.fillDate(ctx, fr, stepID, kind.Type, kind.Field, text)
	}
	x, y := res.X+fr.baseX, res.Y+fr.baseY
	for _, step := range []struct {
		typ     string
		buttons int
	}{{"mouseMoved", 0}, {"mousePressed", 1}, {"mouseReleased", 0}} {
		if err := p.mouse(ctx, step.typ, x, y, step.buttons, 1); err != nil {
			return err
		}
	}
	// Focus (the click may have landed on a label or a wrapper) and select the
	// current content so the typed text replaces it.
	if err := p.evalIn(ctx, fr, `(function(){const el=globalThis.__ladiEl;el.focus();
if(typeof el.select==='function'&&el.localName!=='select')el.select();
else if(el.isContentEditable){const r=document.createRange();r.selectNodeContents(el);const s=getSelection();s.removeAllRanges();s.addRange(r)}
return true})()`, nil); err != nil {
		return err
	}
	if text == "" {
		if err := p.key(ctx, "Backspace"); err != nil {
			return err
		}
	} else if err := p.Page.Call(ctx, "Input.insertText", map[string]any{"text": text}, nil); err != nil {
		return err
	}
	// blur() makes the browser fire the native change event, as a user leaving
	// the field does.
	return p.evalIn(ctx, fr, `(function(){globalThis.__ladiEl.blur();return true})()`, nil)
}

func (p *Player) selectOption(ctx context.Context, fr *frame, want string) error {
	w, _ := json.Marshal(want)
	var ok bool
	expr := fmt.Sprintf(`(function(){const el=globalThis.__ladiEl;if(el.localName!=='select')return false;
const w=%s;const o=Array.from(el.options).find(o=>o.value===w)||Array.from(el.options).find(o=>o.textContent.trim()===w);
if(!o)return false;el.value=o.value;
el.dispatchEvent(new Event('input',{bubbles:true,composed:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return true})()`, w)
	if err := p.evalIn(ctx, fr, expr, &ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no option %q in the select element", want)
	}
	return nil
}

func (p *Player) check(ctx context.Context, fr *frame, res *located, want bool) error {
	var cur bool
	if err := p.evalIn(ctx, fr, `globalThis.__ladiEl.checked===true`, &cur); err != nil {
		return err
	}
	if cur == want {
		return nil
	}
	if err := p.clickAt(ctx, fr, res, 1); err != nil {
		return err
	}
	if err := p.evalIn(ctx, fr, `globalThis.__ladiEl.checked===true`, &cur); err != nil {
		return err
	}
	if cur != want {
		return fmt.Errorf("click did not set checked=%v", want)
	}
	return nil
}

type keyDef struct {
	key, code string
	vk        int
	text      string
}

var keys = map[string]keyDef{
	"Enter": {"Enter", "Enter", 13, "\r"}, "Tab": {"Tab", "Tab", 9, ""}, "Escape": {"Escape", "Escape", 27, ""},
	"Backspace": {"Backspace", "Backspace", 8, ""}, "Delete": {"Delete", "Delete", 46, ""},
	"ArrowDown": {"ArrowDown", "ArrowDown", 40, ""}, "ArrowUp": {"ArrowUp", "ArrowUp", 38, ""},
	"ArrowLeft": {"ArrowLeft", "ArrowLeft", 37, ""}, "ArrowRight": {"ArrowRight", "ArrowRight", 39, ""},
	" ": {" ", "Space", 32, " "},
}

func (p *Player) press(ctx context.Context, chord string) error {
	mods := 0
	name := chord
	if rest, ok := strings.CutPrefix(chord, "Shift+"); ok {
		mods, name = 8, rest
	}
	w := p.watchNav()
	defer w.stop()
	if err := p.keyMods(ctx, name, mods); err != nil {
		return err
	}
	return w.wait(ctx)
}

func (p *Player) key(ctx context.Context, name string) error { return p.keyMods(ctx, name, 0) }

func (p *Player) keyMods(ctx context.Context, name string, mods int) error {
	k, ok := keys[name]
	if !ok {
		if len([]rune(name)) != 1 {
			return fmt.Errorf("%w: key %q", ErrUnsupported, name)
		}
		k = keyDef{key: name, code: "", vk: int(strings.ToUpper(name)[0]), text: name}
	}
	down := "rawKeyDown"
	if k.text != "" {
		down = "keyDown"
	}
	for _, typ := range []string{down, "keyUp"} {
		params := map[string]any{"type": typ, "key": k.key, "code": k.code, "windowsVirtualKeyCode": k.vk, "modifiers": mods}
		if typ == "keyDown" {
			params["text"] = k.text
		}
		if err := p.Page.Call(ctx, "Input.dispatchKeyEvent", params, nil); err != nil {
			return err
		}
	}
	return nil
}

func (p *Player) scroll(ctx context.Context, m *macro.Macro, s *macro.Step, vars map[string]string, value string) error {
	if s.Target != nil {
		fr, _, err := p.find(ctx, m, s, vars, true) // centre() scrolls the element into view
		_ = fr
		return err
	}
	fr := &frame{pg: p.Page, id: p.Page.TargetID}
	expr := ""
	switch value {
	case "top":
		expr = `window.scrollTo(0,0)`
	case "bottom":
		expr = `window.scrollTo(0,document.documentElement.scrollHeight)`
	default:
		var dy float64
		if _, err := fmt.Sscanf(value, "%g", &dy); err != nil {
			return fmt.Errorf("scroll value %q: %w", value, err)
		}
		expr = fmt.Sprintf(`window.scrollBy(0,%g)`, dy)
	}
	return p.evalIn(ctx, fr, "(function(){"+expr+";return true})()", nil)
}

// ---- expectations ---------------------------------------------------------------

func (p *Player) expect(ctx context.Context, m *macro.Macro, s *macro.Step, vars map[string]string, e macro.Expectation) error {
	val := ""
	if e.Value != nil {
		var err error
		if val, err = evalExpr(s.ID, *e.Value, vars); err != nil {
			return err
		}
	}
	if e.Kind == macro.ExpectURLMatches {
		re, err := regexp.Compile(val)
		if err != nil {
			return err
		}
		return poll(ctx, func() error {
			var u string
			if err := p.evalIn(ctx, &frame{pg: p.Page, id: p.Page.TargetID}, `location.href`, &u); err != nil {
				return err
			}
			if !re.MatchString(u) {
				return fmt.Errorf("expect urlMatches: %q does not match %q", u, val)
			}
			return nil
		})
	}
	if e.Target == nil {
		return fmt.Errorf("expectation %s has no target", e.Kind)
	}
	probe := &macro.Step{ID: s.ID, Target: e.Target}
	if e.Kind == macro.ExpectElementAbsent {
		return poll(ctx, func() error {
			t, err := resolveTarget(s.ID, e.Target, vars)
			if err != nil {
				return err
			}
			st, _, err := p.Locate(ctx, t)
			if err == nil && st == "ok" {
				return errors.New("expect elementAbsent: element is present")
			}
			return nil
		})
	}
	fr, _, err := p.find(ctx, m, probe, vars, false)
	if err != nil {
		return err
	}
	var read string
	switch e.Kind {
	case macro.ExpectElementPresent:
		return nil
	case macro.ExpectTextContains, macro.ExpectTextEquals:
		read = `globalThis.__ladiEl.textContent`
	case macro.ExpectValueEquals:
		read = `globalThis.__ladiEl.value`
	}
	return poll(ctx, func() error {
		var got string
		if err := p.evalIn(ctx, fr, read, &got); err != nil {
			return err
		}
		ok := got == val
		if e.Kind == macro.ExpectTextContains {
			ok = strings.Contains(got, val)
		}
		if !ok {
			return fmt.Errorf("expect %s: got %q, want %q", e.Kind, got, val)
		}
		return nil
	})
}

// poll runs fn until it returns nil or ctx ends; it returns the last error.
func poll(ctx context.Context, fn func() error) error {
	for {
		err := fn()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(pollEvery):
		}
	}
}

func jsq(s string) string { b, _ := json.Marshal(s); return string(b) }
