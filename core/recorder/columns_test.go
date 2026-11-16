package recorder

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/macro"
)

// The JavaScript helpers have their own tests; run them when bun is installed.
func TestPlaceholderJS(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun is not installed")
	}
	out, err := exec.Command(bun, "test", "placeholder.test.js").CombinedOutput()
	if err != nil {
		t.Fatalf("bun test: %v\n%s", err, out)
	}
}

func fillEv(css string, parts []rawPart, value string) rawEvent {
	return rawEvent{T: "step", Action: "fill", Value: value, Parts: parts, URL: "https://example.com/", Target: &rawTarget{Locators: []rawLocator{{Kind: "css", Value: css}}}}
}

func newCols(t *testing.T, names ...string) *Recorder {
	t.Helper()
	r := New(Options{ID: "u"})
	for _, n := range names {
		r.onVar(rawEvent{Var: n, Kind: "text", Token: "ladi" + n})
	}
	return r
}

func exprs(r *Recorder) []string {
	var out []string
	for _, s := range r.Steps() {
		out = append(out, s.Value.Expr)
	}
	return out
}

func TestColumnStepsAndDeclarations(t *testing.T) {
	r := New(Options{ID: "u"})
	r.onVar(rawEvent{Var: "email", Kind: "email", Token: "ladia@example.com"})
	r.onVar(rawEvent{Var: "email", Kind: "text", Token: "other"}) // a second add keeps the first token
	r.onVar(rawEvent{Var: "ho_ten", Kind: "text", Token: "ladib"})
	r.onVar(rawEvent{Var: "Bad Name", Kind: "text", Token: "x"})   // not a column name
	r.onVar(rawEvent{Var: "weird", Kind: "colour", Token: "x"})    // not a kind
	r.onVar(rawEvent{Var: "unused", Kind: "text", Token: "ladic"}) // no step reads it
	if got := r.Columns(); len(got) != 3 || got[0].Kind != macro.KindEmail || got[0].Name != "email" {
		t.Fatalf("columns = %+v", got)
	}
	r.onStep(nil, fillEv("#a", []rawPart{{L: "Hello "}, {V: "ho_ten"}, {L: " {{not a column}}"}}, "Hello ladib {{not a column}}"), time.Now())
	r.onStep(nil, fillEv("#b", []rawPart{{V: "email"}}, "ladia@example.com"), time.Now())
	r.onStep(nil, fillEv("#c", []rawPart{{V: "ghost"}}, "x"), time.Now()) // unknown column: a gap, no step
	r.onStep(nil, fillEv("#d", nil, "fixed text"), time.Now())
	want := []string{`Hello {{ho_ten}} \{{not a column}}`, "{{email}}", "fixed text"}
	if got := exprs(r); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("steps = %q, want %q", got, want)
	}
	if r.Stats().Gaps["unknown-column"] != 1 {
		t.Errorf("gaps = %v", r.Stats().Gaps)
	}
	m, err := r.Macro()
	if err != nil {
		t.Fatalf("macro: %v", err)
	}
	// Declared in order of first use, only what a step reads, as text values with the field kind.
	if len(m.Variables) != 2 || m.Variables[0].Name != "ho_ten" || m.Variables[1].Name != "email" || m.Variables[1].Kind != macro.KindEmail || m.Variables[1].Type != macro.VarString {
		t.Errorf("variables = %+v", m.Variables)
	}
}

func TestColumnRenameAndDelete(t *testing.T) {
	r := newCols(t, "a", "b")
	r.onStep(nil, fillEv("#1", []rawPart{{V: "a"}}, "ladia"), time.Now())
	r.onStep(nil, fillEv("#2", []rawPart{{V: "b"}}, "ladib"), time.Now())
	r.onStep(nil, fillEv("#3", []rawPart{{L: "x "}, {V: "a"}}, "x ladia"), time.Now())
	r.onStep(nil, fillEv("#4", nil, "plain"), time.Now())
	r.warnings = []Warning{{Kind: WarnCanvasUnsupported, StepIndex: 4}}

	r.onVarOp(rawEvent{Op: "rename", Var: "a", To: "c"})
	r.onVarOp(rawEvent{Op: "rename", Var: "b", To: "c"}) // taken: ignored
	r.onVarOp(rawEvent{Op: "rename", Var: "b", To: "Not Valid"})
	if got := exprs(r); strings.Join(got, "|") != "{{c}}|{{b}}|x {{c}}|plain" {
		t.Fatalf("after rename: %q", got)
	}
	if cols := r.Columns(); cols[0].Name != "c" || cols[1].Name != "b" {
		t.Fatalf("columns = %+v", cols)
	}

	r.onVarOp(rawEvent{Op: "delete", Var: "c"})
	if got := exprs(r); strings.Join(got, "|") != "{{b}}|plain" {
		t.Fatalf("after delete: %q", got)
	}
	for i, s := range r.Steps() {
		if want := "s00" + string(rune('1'+i)) + "-fill"; s.ID != want {
			t.Errorf("step %d ID = %s, want %s", i, s.ID, want)
		}
	}
	if w := r.Warnings(); w[0].StepIndex != 2 {
		t.Errorf("warning position = %d, want 2 (two steps before it were deleted)", w[0].StepIndex)
	}
	if len(r.Columns()) != 1 {
		t.Errorf("columns = %+v", r.Columns())
	}
	// The token of a deleted column goes to every overlay frame so it is never recorded as typed text.
	if js := func() string { r.mu.Lock(); defer r.mu.Unlock(); return r.syncJS() }(); !strings.Contains(js, `"dead":["ladia"]`) || !strings.Contains(js, `"counts":{"b":1}`) {
		t.Errorf("sync = %s", js)
	}
	// A new step after a delete gets a fresh, unique ID.
	r.onStep(nil, fillEv("#5", nil, "more"), time.Now())
	ids := map[string]bool{}
	for _, s := range r.Steps() {
		if ids[s.ID] {
			t.Errorf("duplicate step ID %s", s.ID)
		}
		ids[s.ID] = true
	}
}

func TestHintStateAndOptions(t *testing.T) {
	r := New(Options{ID: "u"})
	r.SetOptions("vi", true)
	r.mu.Lock()
	js := r.syncJS()
	r.mu.Unlock()
	if !strings.Contains(js, `"lang":"vi"`) || !strings.Contains(js, `"hint":true`) {
		t.Fatalf("sync = %s", js)
	}
	r.onBinding(nil, []byte(`{"name":"ladiEmit","payload":"{\"t\":\"hint\",\"op\":\"off\"}"}`))
	if closed, never := r.HintState(); !closed || !never {
		t.Errorf("hint state = %v %v", closed, never)
	}
	r.mu.Lock()
	js = r.syncJS()
	r.mu.Unlock()
	if !strings.Contains(js, `"hint":false`) {
		t.Errorf("sync after close = %s", js)
	}
}
