// Package recorder records user actions on a page into a macro (schema v1).
//
// It attaches to a driver.StockSession through Launcher.StockHook. In every
// page and out-of-process frame it adds one script to a named isolated world
// (Page.addScriptToEvaluateOnNewDocument with worldName) that draws the
// overlay and captures events. Events leave the page through a binding that
// exists only in that isolated context. Runtime.enable is never sent.
package recorder

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/macro"
)

//go:embed locator.js
var LocatorJS string

//go:embed recorder.js
var recorderJS string

const (
	// WorldName is the isolated world of the overlay and the recorder.
	WorldName   = "ladilala"
	bindingName = "ladiEmit"
	// navSuppress is how long after a recorded click or key press a main
	// frame navigation counts as its effect (the replay of the action repeats it).
	navSuppress = 3 * time.Second
)

// Options names the macro a Recorder produces.
type Options struct {
	ID   string
	Name string
}

// Stats are the measurements the spike reports.
type Stats struct {
	HookMs      []float64 // per attached target: time spent in the hook (CDP calls)
	OverlayMs   []float64 // per document: performance.now() when the overlay was mounted
	LatencyMs   []float64 // per step: Go receive time minus the page's Date.now() at the event
	Gaps        map[string]int
	Events      int
	BindingMode string
	NavReasons  map[string]int // Page.frameRequestedNavigation reasons seen on tabs
}

type rawLocator struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Name  string `json:"name,omitempty"`
}

type rawTarget struct {
	Locators   []rawLocator `json:"locators"`
	ShadowPath []string     `json:"shadowPath"`
	FramePath  []string     `json:"framePath"`
	CrossFrame bool         `json:"crossFrame"`
}

type rawEvent struct {
	T         string     `json:"t"`
	Action    string     `json:"action"`
	Tag       string     `json:"tag"`
	Type      string     `json:"type"`
	Target    *rawTarget `json:"target"`
	Value     string     `json:"value"`
	Sensitive bool       `json:"sensitive"`
	Name      string     `json:"name"`
	Commit    bool       `json:"commit"`
	Maybe     bool       `json:"maybe"`
	What      string     `json:"what"`
	Paused    bool       `json:"paused"`
	Msg       string     `json:"msg"`
	MS        float64    `json:"ms"`
	TS        int64      `json:"ts"`
	URL       string     `json:"url"`
	Origin    string     `json:"origin"`
	Top       bool       `json:"top"`
	Kind      string     `json:"kind"`
	// Parts is a fill value that holds data columns: text and {{column}} parts in order.
	Parts []rawPart `json:"parts"`
	// Var, To, Op, Token and Protected describe a data column event (see columns.go).
	Var       string `json:"var"`
	To        string `json:"to"`
	Op        string `json:"op"`
	Token     string `json:"token"`
	Protected bool   `json:"protected"`
}

// WarnKind says what the recorder met that it cannot record.
type WarnKind string

const (
	// WarnCanvasUnsupported: a click or drag on a <canvas>. No step is recorded.
	WarnCanvasUnsupported WarnKind = "canvas_unsupported"
	// WarnFileInputUnsupported: an input[type=file], its label or a drop zone
	// with a hidden file input. No step is recorded.
	WarnFileInputUnsupported WarnKind = "file_input_unsupported"
)

// Warning is one interaction the recorder detected but cannot record, for the
// app to show after the recording and in the editor.
type Warning struct {
	Kind WarnKind
	// StepIndex is the number of steps recorded when it happened: the position
	// in Macro().Steps where the user did something that is missing.
	StepIndex int
	// Locator is the best locator of the canvas or file input (zero when none was usable).
	Locator macro.Locator
	Message string
}

// Recorder collects the steps of one recording session.
type Recorder struct {
	opts Options
	ctx  context.Context
	stop context.CancelFunc
	done chan struct{}
	once sync.Once

	mu         sync.Mutex
	steps      []macro.Step
	origins    []string
	cols       []colState // data columns made by typing {{name}}, in order
	dead       []string   // tokens of deleted columns: never recorded as typed text
	lang       string     // overlay language
	hint       bool       // show the first-use hint card
	hintClosed bool
	hintNever  bool
	lastSync   string // the last sync call sent to the overlay
	stats      Stats
	errs       []error
	warnings   []Warning
	lastAction time.Time
	lastEnter  time.Time  // last Enter press (recorded or held)
	held       *heldPress // Enter in a search box: kept only when a navigation follows
	paused     bool
	stopped    bool
	pages      map[string]*driver.Page // by session ID
	contexts   map[ctxKey]int          // isolated contexts, for Stop
	owners     map[string][]string     // out-of-process frame target -> framePath of its owner
	requested  map[string]time.Time    // frame ID -> page-initiated navigation request
}

// heldPress is an Enter press that may have been a new line or a composition
// commit. It becomes a step only when a navigation follows within navSuppress.
type heldPress struct {
	step macro.Step
	at   time.Time
}

type ctxKey struct {
	session, frame string
}

// New creates a Recorder. Assign its Hook to driver.Launcher.StockHook before
// launching the session.
func New(o Options) *Recorder {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Recorder{
		opts: o, ctx: ctx, stop: cancel, done: make(chan struct{}),
		lang: "en", pages: map[string]*driver.Page{}, contexts: map[ctxKey]int{},
		owners: map[string][]string{}, requested: map[string]time.Time{},
	}
	r.stats.Gaps = map[string]int{}
	r.stats.NavReasons = map[string]int{}
	r.stats.BindingMode = "Runtime.addBinding by executionContextId on the named isolated world (bindingCalled needs no Runtime.enable)"
	return r
}

// Done is closed when the user pressed Stop in the overlay or Stop was called.
func (r *Recorder) Done() <-chan struct{} { return r.done }

// Errors returns problems met while attaching or converting events.
func (r *Recorder) Errors() []error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]error(nil), r.errs...)
}

// Stats returns a copy of the measurements so far.
func (r *Recorder) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.stats
	s.HookMs = append([]float64(nil), s.HookMs...)
	s.OverlayMs = append([]float64(nil), s.OverlayMs...)
	s.LatencyMs = append([]float64(nil), s.LatencyMs...)
	s.Gaps = map[string]int{}
	for k, v := range r.stats.Gaps {
		s.Gaps[k] = v
	}
	s.NavReasons = map[string]int{}
	for k, v := range r.stats.NavReasons {
		s.NavReasons[k] = v
	}
	return s
}

// Warnings returns what the recorder detected and did not record, oldest first.
func (r *Recorder) Warnings() []Warning {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Warning(nil), r.warnings...)
}

// Steps returns a copy of the steps recorded so far, for a live view. The
// list can shrink by one when a double click replaces the click it contains.
func (r *Recorder) Steps() []macro.Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]macro.Step(nil), r.steps...)
}

// SetPaused pauses or resumes the recording from the app. While paused the
// overlay in every frame shows it and the recorder drops events and warnings.
func (r *Recorder) SetPaused(paused bool) {
	r.mu.Lock()
	r.paused = paused
	r.mu.Unlock()
	r.broadcast(fmt.Sprintf("__ladiRec.pause(%v)", paused), "")
}

func (r *Recorder) onWarn(ev rawEvent) {
	var kind WarnKind
	switch WarnKind(ev.Kind) {
	case WarnCanvasUnsupported, WarnFileInputUnsupported:
		kind = WarnKind(ev.Kind)
	default:
		return
	}
	w := Warning{Kind: kind, Message: ev.Msg}
	if t, err := targetFromRaw(ev.Target, nil); err == nil {
		w.Locator = t.Locators[0]
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped || r.paused {
		return
	}
	w.StepIndex = len(r.steps)
	if n := len(r.warnings); n > 0 && r.warnings[n-1] == w {
		return
	}
	r.warnings = append(r.warnings, w)
}

func (r *Recorder) fail(format string, args ...any) {
	r.mu.Lock()
	r.errs = append(r.errs, fmt.Errorf(format, args...))
	r.mu.Unlock()
}

// Hook is the driver.Launcher.StockHook of a recording session. It runs while
// the target is paused, so the script is in place before the page's own.
func (r *Recorder) Hook(ctx context.Context, pg *driver.Page) error {
	t0 := time.Now()
	r.mu.Lock()
	r.pages[pg.SessionID] = pg
	r.mu.Unlock()
	bound, stopBound := pg.Subscribe("Runtime.bindingCalled")
	navs, stopNavs := pg.Subscribe("Page.frameNavigated")
	reqs, stopReqs := pg.Subscribe("Page.frameRequestedNavigation")
	err := pg.Call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{
		"source": LocatorJS + ";\n" + placeholderJS + ";\n" + recorderJS, "worldName": WorldName, "runImmediately": true,
	}, nil)
	if err != nil {
		stopBound()
		stopNavs()
		stopReqs()
		return fmt.Errorf("recorder: add overlay script: %w", err)
	}
	go func() {
		defer stopBound()
		defer stopNavs()
		defer stopReqs()
		for {
			select {
			case v := <-bound:
				r.onBinding(pg, v)
			case v := <-reqs:
				r.onRequested(pg, v)
			case v := <-navs:
				// A request event precedes its commit on the wire; make sure it
				// is counted first when both are ready.
				for drained := false; !drained; {
					select {
					case rv := <-reqs:
						r.onRequested(pg, rv)
					default:
						drained = true
					}
				}
				go r.onNavigated(pg, v)
			case <-r.ctx.Done():
				return
			}
		}
	}()
	// Frames that exist before the first commit (the initial about:blank). Done
	// in the background: on a paused out-of-process frame the isolated world
	// does not exist until the target resumes, and its commit event binds it.
	go func() {
		var tree struct {
			FrameTree frameTree `json:"frameTree"`
		}
		if err := pg.Call(r.ctx, "Page.getFrameTree", nil, &tree); err != nil {
			return
		}
		for _, id := range tree.FrameTree.ids() {
			_ = r.bindFrame(r.ctx, pg, id) // the first commit binds it again if this fails
		}
	}()
	r.mu.Lock()
	r.stats.HookMs = append(r.stats.HookMs, float64(time.Since(t0).Microseconds())/1000)
	r.mu.Unlock()
	return nil
}

type frameTree struct {
	Frame struct {
		ID string `json:"id"`
	} `json:"frame"`
	Children []frameTree `json:"childFrames"`
}

func (t frameTree) ids() []string {
	out := []string{t.Frame.ID}
	for _, c := range t.Children {
		out = append(out, c.ids()...)
	}
	return out
}

// bindFrame makes the isolated context of frame the origin of events: Page.
// createIsolatedWorld with the same world name returns the context the script
// already runs in; the binding is added to that context only, so the main
// world has no such function. Queued events are flushed afterwards.
func (r *Recorder) bindFrame(ctx context.Context, pg *driver.Page, frameID string) error {
	var w struct {
		ExecutionContextID int `json:"executionContextId"`
	}
	if err := pg.Call(ctx, "Page.createIsolatedWorld", map[string]any{"frameId": frameID, "worldName": WorldName}, &w); err != nil {
		return err
	}
	// A second binding on a context that has one is an error; ignore it.
	_ = pg.Call(ctx, "Runtime.addBinding", map[string]any{"name": bindingName, "executionContextId": w.ExecutionContextID}, nil)
	r.mu.Lock()
	r.contexts[ctxKey{pg.SessionID, frameID}] = w.ExecutionContextID
	r.mu.Unlock()
	flushErr := pg.Call(ctx, "Runtime.evaluate", map[string]any{
		"contextId": w.ExecutionContextID, "expression": "typeof __ladiFlush==='function'&&__ladiFlush()",
	}, nil)
	r.mu.Lock()
	sync := r.syncJS()
	r.mu.Unlock()
	_ = pg.Call(ctx, "Runtime.evaluate", map[string]any{
		"contextId": w.ExecutionContextID, "expression": "typeof __ladiRec==='object'&&" + sync,
	}, nil)
	return flushErr
}

// pageInitiated are the Page.frameRequestedNavigation reasons of navigations a
// page action causes. Replaying that action repeats them, so they are not steps.
var pageInitiated = map[string]bool{
	"anchorClick": true, "formSubmissionGet": true, "formSubmissionPost": true, "scriptInitiated": true,
	"metaTagRefresh": true, "httpHeaderRefresh": true,
}

func (r *Recorder) onRequested(pg *driver.Page, params []byte) {
	var ev struct {
		FrameID string `json:"frameId"`
		Reason  string `json:"reason"`
		URL     string `json:"url"`
	}
	if json.Unmarshal(params, &ev) != nil || pg.Type != "page" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats.NavReasons[ev.Reason]++
	if pageInitiated[ev.Reason] {
		r.requested[ev.FrameID] = time.Now()
	}
}

func (r *Recorder) onNavigated(pg *driver.Page, params []byte) {
	var ev struct {
		Frame struct {
			ID       string `json:"id"`
			ParentID string `json:"parentId"`
			URL      string `json:"url"`
		} `json:"frame"`
	}
	if json.Unmarshal(params, &ev) != nil {
		return
	}
	if err := r.bindFrame(r.ctx, pg, ev.Frame.ID); err != nil && r.ctx.Err() == nil {
		time.Sleep(50 * time.Millisecond)
		if err := r.bindFrame(r.ctx, pg, ev.Frame.ID); err != nil && r.ctx.Err() == nil && !strings.Contains(err.Error(), "No frame for given id") {
			r.fail("bind frame %s: %v", ev.Frame.URL, err)
		}
	}
	if pg.Type != "page" || ev.Frame.ParentID != "" {
		return
	}
	u := ev.Frame.URL
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped || r.paused {
		return
	}
	caused := false
	if at, ok := r.requested[ev.Frame.ID]; ok {
		delete(r.requested, ev.Frame.ID)
		caused = time.Since(at) < navSuppress && !r.lastAction.IsZero() && at.Sub(r.lastAction) < navSuppress
	}
	// A script that changes location after the key, or a redirect that commits
	// later, has no request event of a page action: the Enter still caused it.
	if !caused && !r.lastEnter.IsZero() && time.Since(r.lastEnter) < navSuppress {
		caused = true
	}
	if caused {
		r.commitEnter()
		r.addOrigin(u)
		return // the click or key press that caused it replays the navigation
	}
	if n := len(r.steps); n > 0 && r.steps[n-1].Action == macro.ActionNavigate && r.steps[n-1].Value.Expr == u {
		return
	}
	r.addOrigin(u)
	r.append(macro.Step{Action: macro.ActionNavigate, Value: &macro.Value{Expr: u}})
}

// commitEnter runs when a navigation was caused by an Enter: a held press
// becomes a step, and a recorded press that ends the steps is a commit point.
// The caller holds r.mu.
func (r *Recorder) commitEnter() {
	if h := r.held; h != nil {
		r.held = nil
		if time.Since(h.at) < navSuppress {
			h.step.CommitPoint = true
			r.append(h.step)
			return
		}
	}
	if n := len(r.steps); n > 0 && r.steps[n-1].Action == macro.ActionPress && !r.lastEnter.IsZero() &&
		r.steps[n-1].Value != nil && r.steps[n-1].Value.Expr == "Enter" {
		r.steps[n-1].CommitPoint = true
	}
}

// append assigns the step ID; the caller holds r.mu.
func (r *Recorder) append(s macro.Step) {
	s.ID = fmt.Sprintf("s%03d-%s", len(r.steps)+1, s.Action)
	r.steps = append(r.steps, s)
}

func (r *Recorder) addOrigin(raw string) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return
	}
	o := u.Scheme + "://" + u.Host
	for _, have := range r.origins {
		if have == o {
			return
		}
	}
	r.origins = append(r.origins, o)
}

func (r *Recorder) onBinding(pg *driver.Page, params []byte) {
	recv := time.Now()
	var b struct {
		Name    string `json:"name"`
		Payload string `json:"payload"`
	}
	if json.Unmarshal(params, &b) != nil || b.Name != bindingName {
		return
	}
	var ev rawEvent
	if err := json.Unmarshal([]byte(b.Payload), &ev); err != nil {
		r.fail("bad event payload: %v", err)
		return
	}
	r.mu.Lock()
	r.stats.Events++
	r.mu.Unlock()
	switch ev.T {
	case "overlay":
		r.mu.Lock()
		r.stats.OverlayMs = append(r.stats.OverlayMs, ev.MS)
		r.mu.Unlock()
	case "gap":
		r.mu.Lock()
		r.stats.Gaps[ev.What]++
		r.mu.Unlock()
	case "error":
		r.fail("page script: %s", ev.Msg)
	case "pause":
		r.mu.Lock()
		r.paused = ev.Paused
		r.mu.Unlock()
		r.broadcast(fmt.Sprintf("__ladiRec.pause(%v)", ev.Paused), pg.SessionID)
	case "warn":
		r.onWarn(ev)
	case "notice":
		q, _ := json.Marshal(ev.Msg)
		r.broadcast("__ladiRec.notice("+string(q)+")", "")
	case "stop":
		r.finish()
	case "var":
		r.onVar(ev)
	case "varop":
		r.onVarOp(ev)
	case "hint":
		r.mu.Lock()
		r.hintClosed = true
		r.hintNever = r.hintNever || ev.Op == "off"
		r.mu.Unlock()
		r.pushSync()
	case "step":
		r.onStep(pg, ev, recv)
		r.pushSync()
	}
}

func (r *Recorder) onStep(pg *driver.Page, ev rawEvent, recv time.Time) {
	r.mu.Lock()
	if r.stopped || r.paused {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	var owner []string
	if ev.Target != nil && ev.Target.CrossFrame {
		var err error
		if owner, err = r.ownerPath(pg); err != nil {
			r.fail("frame path of %s: %v", ev.URL, err)
			r.mu.Lock()
			r.stats.Gaps["cross-origin-frame-path"]++
			r.mu.Unlock()
			return
		}
	}
	st, err := convertStep(ev, owner)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil && len(ev.Parts) > 0 && st.Action == macro.ActionFill {
		expr, ok := r.partsExpr(ev.Parts)
		if !ok || ev.Sensitive {
			err = errors.New("unknown-column")
		} else {
			st.Value = &macro.Value{Expr: expr}
		}
	}
	if err != nil {
		r.stats.Gaps[err.Error()]++
		return
	}
	r.addOrigin(ev.URL)
	r.lastAction = recv
	r.stats.LatencyMs = append(r.stats.LatencyMs, float64(recv.UnixMilli()-ev.TS))
	if st.Action == macro.ActionDblClick {
		// The first click of the pair was already recorded; the double click replaces it.
		if n := len(r.steps); n > 0 && r.steps[n-1].Action == macro.ActionClick && sameTarget(r.steps[n-1].Target, st.Target) {
			r.steps = r.steps[:n-1]
		}
	}
	if st.Action == macro.ActionFill {
		// An Enter that was only a new line flushed the field early: the later fill holds the whole text.
		if n := len(r.steps); n > 0 && r.steps[n-1].Action == macro.ActionFill && sameTarget(r.steps[n-1].Target, st.Target) {
			r.steps = r.steps[:n-1]
		}
	}
	r.held = nil // any later step means the held Enter did not submit
	if st.Action == macro.ActionPress && ev.Value == "Enter" {
		r.lastEnter = recv
		if ev.Maybe {
			r.held = &heldPress{step: st, at: recv}
			return
		}
	}
	r.append(st)
}

const sensitiveVar = "password"

func sameTarget(a, b *macro.Target) bool {
	if a == nil || b == nil || len(a.Locators) == 0 || len(a.Locators) != len(b.Locators) {
		return false
	}
	for i := range a.Locators {
		if a.Locators[i] != b.Locators[i] {
			return false
		}
	}
	return strings.Join(a.FramePath, "\x00") == strings.Join(b.FramePath, "\x00") && strings.Join(a.ShadowPath, "\x00") == strings.Join(b.ShadowPath, "\x00")
}

// ownerPath finds the framePath of the iframe element that embeds pg, a
// cross-origin frame running in its own process. The target ID of such a frame
// is its frame ID; the embedding page resolves it with DOM.getFrameOwner.
func (r *Recorder) ownerPath(pg *driver.Page) ([]string, error) {
	r.mu.Lock()
	if p, ok := r.owners[pg.TargetID]; ok {
		r.mu.Unlock()
		return p, nil
	}
	parent := r.pages[pg.ParentSessionID]
	r.mu.Unlock()
	if parent == nil {
		return nil, errors.New("embedding page is unknown")
	}
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	var own struct {
		BackendNodeID int `json:"backendNodeId"`
	}
	if err := parent.Call(ctx, "DOM.getFrameOwner", map[string]any{"frameId": pg.TargetID}, &own); err != nil {
		return nil, err
	}
	var node struct {
		Node struct {
			Attributes []string `json:"attributes"`
		} `json:"node"`
	}
	if err := parent.Call(ctx, "DOM.describeNode", map[string]any{"backendNodeId": own.BackendNodeID}, &node); err != nil {
		return nil, err
	}
	attrs := map[string]string{}
	for i := 0; i+1 < len(node.Node.Attributes); i += 2 {
		attrs[node.Node.Attributes[i]] = node.Node.Attributes[i+1]
	}
	sel := ""
	switch {
	case attrs["id"] != "":
		sel = "iframe#" + attrs["id"]
	case attrs["name"] != "":
		sel = `iframe[name="` + attrs["name"] + `"]`
	case attrs["src"] != "":
		sel = `iframe[src="` + attrs["src"] + `"]`
	default:
		return nil, errors.New("embedding iframe has no id, name or src")
	}
	path := []string{sel}
	if parent.Type == "iframe" {
		up, err := r.ownerPath(parent)
		if err != nil {
			return nil, err
		}
		path = append(append([]string(nil), up...), path...)
	}
	r.mu.Lock()
	r.owners[pg.TargetID] = path
	r.mu.Unlock()
	return path, nil
}

// broadcast runs expr in every known isolated context except one session's.
func (r *Recorder) broadcast(expr, exceptSession string) {
	r.mu.Lock()
	type job struct {
		pg  *driver.Page
		ctx int
	}
	var jobs []job
	for k, id := range r.contexts {
		if k.session != exceptSession {
			jobs = append(jobs, job{r.pages[k.session], id})
		}
	}
	r.mu.Unlock()
	for _, j := range jobs {
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = j.pg.Call(cctx, "Runtime.evaluate", map[string]any{"contextId": j.ctx, "expression": "typeof __ladiRec==='object'&&" + expr}, nil)
		cancel()
	}
}

func (r *Recorder) finish() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	r.once.Do(func() { close(r.done) })
}

// Stop ends the recording: the overlay in every frame shows "Stopped" and
// later events are dropped. It does not close the browser.
func (r *Recorder) Stop() {
	r.broadcast("__ladiRec.stop()", "")
	time.Sleep(100 * time.Millisecond) // let the flush of a pending fill arrive
	r.finish()
	r.stop()
}

// Macro returns the recorded macro. It is validated, so it loads with macro.Load.
func (r *Recorder) Macro() (*macro.Macro, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := &macro.Macro{
		SchemaVersion: macro.CurrentVersion, ID: r.opts.ID, Name: r.opts.Name,
		AllowedOrigins: append([]string(nil), r.origins...),
		Steps:          append([]macro.Step(nil), r.steps...),
	}
	if m.ID == "" {
		m.ID = "recording"
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	// Every variable a step reads is declared once, in order of first use. Column
	// values are text in a run: the kind only tells the user what the field takes.
	seen := map[string]bool{}
	for _, s := range r.steps {
		for _, name := range stepRefs(s) {
			if seen[name] {
				continue
			}
			seen[name] = true
			d := macro.VariableDecl{Name: name, Type: macro.VarString, Required: true}
			if i := r.col(name); i >= 0 {
				d.Kind = r.cols[i].Kind
			} else if name == sensitiveVar {
				d.Kind = macro.KindPassword
			}
			m.Variables = append(m.Variables, d)
		}
	}
	if err := macro.Validate(m); err != nil {
		return m, err
	}
	return m, nil
}
