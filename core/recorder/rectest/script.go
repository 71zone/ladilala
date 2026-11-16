package rectest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/8bu/ladilala/core/driver"
)

// User is a scripted human: it finds an element with a main-world query (this
// is test code, not the product) and then acts with real CDP input events, so
// the events the page sees are trusted, like a person's.
type User struct {
	Ctx  context.Context
	S    *driver.StockSession
	Page *driver.Page
	// Pace is the pause after each action.
	Pace time.Duration
	// Clicks and Keys record when each real input event was sent, for latency.
	Sent []time.Time
}

// NewUser returns a User for the first tab of s.
func NewUser(ctx context.Context, s *driver.StockSession, pg *driver.Page) *User {
	// On Windows Chrome the first mouse press after a load can be eaten while
	// the window takes focus; a focused tab makes the scripted user reliable.
	_ = pg.Call(ctx, "Page.bringToFront", nil, nil)
	_ = pg.Call(ctx, "Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true}, nil)
	return &User{Ctx: ctx, S: s, Page: pg, Pace: 120 * time.Millisecond}
}

func (u *User) pause() { time.Sleep(u.Pace) }

// Navigate loads url like a user typing it in the address bar would.
func (u *User) Navigate(url string) error {
	defer u.pause()
	return u.Page.Navigate(u.Ctx, url)
}

type point struct {
	X, Y float64
}

// pointOf evaluates expr (a JS expression for an element) in pg's main world
// and returns its centre in that document's viewport after scrolling it in.
func (u *User) pointOf(pg *driver.Page, expr string) (point, error) {
	var p point
	js := fmt.Sprintf(`(() => { const el = %s; if (!el) return null; el.scrollIntoView({block:'center',inline:'center'});
const r = el.getBoundingClientRect(); return {X: r.left + r.width/2, Y: r.top + r.height/2}; })()`, expr)
	var raw *point
	if err := pg.Eval(u.Ctx, js, &raw); err != nil {
		return p, err
	}
	if raw == nil {
		return p, fmt.Errorf("rectest: no element for %s", expr)
	}
	return *raw, nil
}

func jsq(s string) string { b, _ := json.Marshal(s); return string(b) }

// Click clicks the element css matches in the top document.
func (u *User) Click(css string) error {
	p, err := u.pointOf(u.Page, "document.querySelector("+jsq(css)+")")
	if err != nil {
		return err
	}
	return u.clickAt(p)
}

// ClickByText clicks the first element of the tag whose text equals text.
func (u *User) ClickByText(tag, text string) error {
	p, err := u.pointOf(u.Page, fmt.Sprintf("Array.from(document.querySelectorAll(%s)).find(e => e.textContent.trim() === %s)", jsq(tag), jsq(text)))
	if err != nil {
		return err
	}
	return u.clickAt(p)
}

// ClickShadow clicks css inside the open shadow root of host.
func (u *User) ClickShadow(host, css string) error {
	p, err := u.pointOf(u.Page, fmt.Sprintf("document.querySelector(%s).shadowRoot.querySelector(%s)", jsq(host), jsq(css)))
	if err != nil {
		return err
	}
	return u.clickAt(p)
}

func (u *User) iframeOrigin(pg *driver.Page, sel string) (point, error) {
	var o *point
	js := fmt.Sprintf(`(() => { const f = document.querySelector(%s); if (!f) return null; const r = f.getBoundingClientRect(), cs = getComputedStyle(f);
return {X: r.left + f.clientLeft + parseFloat(cs.paddingLeft), Y: r.top + f.clientTop + parseFloat(cs.paddingTop)}; })()`, jsq(sel))
	if err := pg.Eval(u.Ctx, js, &o); err != nil || o == nil {
		return point{}, fmt.Errorf("rectest: iframe %s: %v", sel, err)
	}
	return *o, nil
}

// ClickSameOriginFrame clicks css inside the same-origin iframe frameSel.
func (u *User) ClickSameOriginFrame(frameSel, css string) error {
	if err := u.scrollTo(frameSel); err != nil {
		return err
	}
	p, err := u.pointOf(u.Page, fmt.Sprintf("document.querySelector(%s).contentDocument.querySelector(%s)", jsq(frameSel), jsq(css)))
	if err != nil {
		return err
	}
	o, err := u.iframeOrigin(u.Page, frameSel)
	if err != nil {
		return err
	}
	return u.clickAt(point{p.X + o.X, p.Y + o.Y})
}

func (u *User) scrollTo(sel string) error {
	return u.Page.Eval(u.Ctx, fmt.Sprintf("document.querySelector(%s).scrollIntoView({block:'center'})", jsq(sel)), nil)
}

// ClickCrossOriginFrame clicks css inside the out-of-process iframe frameSel.
func (u *User) ClickCrossOriginFrame(frameSel, css string) error {
	if err := u.scrollTo(frameSel); err != nil {
		return err
	}
	var child *driver.Page
	deadline := time.Now().Add(5 * time.Second)
	for child == nil && time.Now().Before(deadline) {
		for _, p := range u.S.Pages() {
			if p.Type == "iframe" && p.ParentSessionID == u.Page.SessionID {
				child = p
			}
		}
		if child == nil {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if child == nil {
		return fmt.Errorf("rectest: no out-of-process frame for %s", frameSel)
	}
	p, err := u.pointOf(child, "document.querySelector("+jsq(css)+")")
	if err != nil {
		return err
	}
	o, err := u.iframeOrigin(u.Page, frameSel)
	if err != nil {
		return err
	}
	return u.clickAt(point{p.X + o.X, p.Y + o.Y})
}

func (u *User) clickAt(p point) error { return u.clickCount(p, 1) }

// clickCount presses and releases the left button n times in a row at p; the
// n-th press carries clickCount n, so n=2 is a double click.
func (u *User) clickCount(p point, n int) error {
	defer u.pause()
	for i := 1; i <= n; i++ {
		for _, s := range []struct {
			typ     string
			buttons int
		}{{"mouseMoved", 0}, {"mousePressed", 1}, {"mouseReleased", 0}} {
			if s.typ == "mouseMoved" && i > 1 {
				continue
			}
			params := map[string]any{"type": s.typ, "x": p.X, "y": p.Y, "button": "none", "buttons": s.buttons}
			if s.typ != "mouseMoved" {
				params["button"] = "left"
				params["clickCount"] = i
			}
			if s.typ == "mousePressed" {
				u.Sent = append(u.Sent, time.Now())
			}
			if err := u.Page.Call(u.Ctx, "Input.dispatchMouseEvent", params, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// DoubleClick double-clicks the element css matches in the top document.
func (u *User) DoubleClick(css string) error {
	p, err := u.pointOf(u.Page, "document.querySelector("+jsq(css)+")")
	if err != nil {
		return err
	}
	return u.clickCount(p, 2)
}

// MoveTo moves the pointer to viewport position (x, y) without clicking.
func (u *User) MoveTo(x, y float64) error {
	return u.Page.Call(u.Ctx, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y, "button": "none"}, nil)
}

// ClickAtPoint clicks viewport position (x, y).
func (u *User) ClickAtPoint(x, y float64) error { return u.clickAt(point{x, y}) }

// Type types text one key at a time (keydown, input, keyup per character).
func (u *User) Type(text string) error {
	defer u.pause()
	for _, r := range text {
		ch := string(r)
		for _, typ := range []string{"keyDown", "keyUp"} {
			params := map[string]any{"type": typ, "key": ch, "unmodifiedText": ch}
			if typ == "keyDown" {
				params["text"] = ch
			}
			if err := u.Page.Call(u.Ctx, "Input.dispatchKeyEvent", params, nil); err != nil {
				return err
			}
		}
		time.Sleep(8 * time.Millisecond)
	}
	return nil
}

var keyCodes = map[string]struct {
	vk   int
	text string
}{"Tab": {9, ""}, "Enter": {13, "\r"}, "ArrowUp": {38, ""}, "ArrowRight": {39, ""}}

// Key presses a named key (Tab, Enter).
func (u *User) Key(name string) error {
	defer u.pause()
	k, ok := keyCodes[strings.TrimPrefix(name, "Shift+")]
	if !ok {
		return fmt.Errorf("rectest: unknown key %s", name)
	}
	mods := 0
	if strings.HasPrefix(name, "Shift+") {
		mods = 8
	}
	down := "rawKeyDown"
	if k.text != "" {
		down = "keyDown"
	}
	for _, typ := range []string{down, "keyUp"} {
		params := map[string]any{"type": typ, "key": strings.TrimPrefix(name, "Shift+"), "code": strings.TrimPrefix(name, "Shift+"), "windowsVirtualKeyCode": k.vk, "modifiers": mods}
		if typ == "keyDown" {
			params["text"] = k.text
		}
		u.Sent = append(u.Sent, time.Now())
		if err := u.Page.Call(u.Ctx, "Input.dispatchKeyEvent", params, nil); err != nil {
			return err
		}
	}
	return nil
}

// Wait pauses for d.
func (u *User) Wait(d time.Duration) { time.Sleep(d) }

// WaitFor polls a main-world JS condition on the tab until it is true.
func (u *User) WaitFor(cond string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var ok bool
		if err := u.Page.Eval(u.Ctx, "!!("+cond+")", &ok); err == nil && ok {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("rectest: timeout waiting for %s", cond)
}

// Focus focuses the element css matches (a date input then has its first
// segment active) without a click.
func (u *User) Focus(css string) error {
	defer time.Sleep(400 * time.Millisecond) // the segment needs a moment before it takes keys
	return u.Page.Eval(u.Ctx, "document.querySelector("+jsq(css)+").focus()", nil)
}

// Drag presses the left button on the element css matches, moves 40 px right
// in steps and releases.
func (u *User) Drag(css string) error {
	defer u.pause()
	p, err := u.pointOf(u.Page, "document.querySelector("+jsq(css)+")")
	if err != nil {
		return err
	}
	send := func(typ string, x float64, buttons int) error {
		params := map[string]any{"type": typ, "x": x, "y": p.Y, "button": "none", "buttons": buttons}
		if typ != "mouseMoved" {
			params["button"], params["clickCount"] = "left", 1
		}
		return u.Page.Call(u.Ctx, "Input.dispatchMouseEvent", params, nil)
	}
	if err := send("mouseMoved", p.X, 0); err != nil {
		return err
	}
	if err := send("mousePressed", p.X, 1); err != nil {
		return err
	}
	for dx := 10.0; dx <= 40; dx += 10 {
		if err := send("mouseMoved", p.X+dx, 1); err != nil {
			return err
		}
	}
	return send("mouseReleased", p.X+40, 0)
}

// EditDate edits a native date-like input with the keyboard: ArrowUp fills the
// focused segment, ArrowRight moves to the next one. It works whatever the
// locale's segment order, and the values come from today's date, so callers
// compare with the returned field value. segments is 3 for date, 2 for month.
func (u *User) EditDate(css string, segments int) (string, error) {
	if err := u.Focus(css); err != nil {
		return "", err
	}
	for i := 0; i < segments; i++ {
		if i > 0 {
			if err := u.Key("ArrowRight"); err != nil {
				return "", err
			}
		}
		if err := u.Key("ArrowUp"); err != nil {
			return "", err
		}
	}
	var v string
	err := u.Page.Eval(u.Ctx, "document.querySelector("+jsq(css)+").value", &v)
	return v, err
}

// Expect checks that the field css matches holds want, so a dropped keystroke
// fails the scenario instead of looking like a recorder bug.
func (u *User) Expect(css, want string) error {
	var got string
	if err := u.Page.Eval(u.Ctx, "document.querySelector("+jsq(css)+").value", &got); err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("rectest: %s holds %q after typing, want %q", css, got, want)
	}
	return nil
}

// ClickAndType clicks the field css matches and types text, and retries when
// the field stays empty. On Windows, in a browser started from an SSH session
// (no active window), the first mouse press after a launch can be swallowed
// while the window is activated; a swallowed press leaves the page untouched.
func (u *User) ClickAndType(css, text string) error {
	for try := 0; ; try++ {
		if err := u.Click(css); err != nil {
			return err
		}
		if err := u.Type(text); err != nil {
			return err
		}
		if err := u.Expect(css, text); err == nil || try == 3 {
			return err
		}
	}
}

// Center returns the viewport center of the element css matches in the top document.
func (u *User) Center(css string) (x, y float64, err error) {
	p, err := u.pointOf(u.Page, "document.querySelector("+jsq(css)+")")
	return p.X, p.Y, err
}

// DragBetween presses the left button at (x1, y1), moves to (x2, y2) in steps
// and releases there: a real mouse drag.
func (u *User) DragBetween(x1, y1, x2, y2 float64) error {
	defer u.pause()
	send := func(typ string, x, y float64, buttons int) error {
		params := map[string]any{"type": typ, "x": x, "y": y, "button": "none", "buttons": buttons}
		if typ != "mouseMoved" {
			params["button"], params["clickCount"] = "left", 1
		}
		return u.Page.Call(u.Ctx, "Input.dispatchMouseEvent", params, nil)
	}
	if err := send("mouseMoved", x1, y1, 0); err != nil {
		return err
	}
	if err := send("mousePressed", x1, y1, 1); err != nil {
		return err
	}
	const steps = 8
	for i := 1; i <= steps; i++ {
		f := float64(i) / steps
		if err := send("mouseMoved", x1+(x2-x1)*f, y1+(y2-y1)*f, 1); err != nil {
			return err
		}
		time.Sleep(15 * time.Millisecond)
	}
	return send("mouseReleased", x2, y2, 0)
}
