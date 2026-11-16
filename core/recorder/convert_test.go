package recorder

import (
	"errors"
	"testing"

	"github.com/8bu/ladilala/core/macro"
)

func TestTargetFromRawKeepsStabilityOrder(t *testing.T) {
	raw := &rawTarget{
		Locators: []rawLocator{
			{Kind: "role", Value: "textbox", Name: "First name"},
			{Kind: "label", Value: "First name"},
			{Kind: "bogus", Value: "x"},
			{Kind: "css", Value: "  "},
			{Kind: "css", Value: "#first"},
			{Kind: "css", Value: "#first"},
			{Kind: "text", Value: "{{not a literal}}"},
			{Kind: "xpath", Value: "/html[1]/body[1]/input[1]"},
		},
		ShadowPath: []string{"#host"},
		FramePath:  []string{"iframe#inner"},
		CrossFrame: true,
	}
	got, err := targetFromRaw(raw, []string{"iframe#outer"})
	if err != nil {
		t.Fatal(err)
	}
	want := []macro.Locator{
		{Kind: macro.LocRole, Value: "textbox", Name: "First name"},
		{Kind: macro.LocLabel, Value: "First name"},
		{Kind: macro.LocCSS, Value: "#first"},
		{Kind: macro.LocText, Value: `\{{not a literal}}`},
		{Kind: macro.LocXPath, Value: "/html[1]/body[1]/input[1]"},
	}
	if len(got.Locators) != len(want) {
		t.Fatalf("locators = %+v, want %+v", got.Locators, want)
	}
	for i := range want {
		if got.Locators[i] != want[i] {
			t.Errorf("locator %d = %+v, want %+v", i, got.Locators[i], want[i])
		}
	}
	if len(got.FramePath) != 2 || got.FramePath[0] != "iframe#outer" || got.FramePath[1] != "iframe#inner" {
		t.Errorf("framePath = %v, want the owner path before the page's own chain", got.FramePath)
	}
	if len(got.ShadowPath) != 1 || got.ShadowPath[0] != "#host" {
		t.Errorf("shadowPath = %v", got.ShadowPath)
	}
	m := &macro.Macro{SchemaVersion: 1, ID: "t", Name: "t", AllowedOrigins: []string{"http://127.0.0.1:1"},
		Steps: []macro.Step{{ID: "a", Action: macro.ActionClick, Target: got}}}
	if err := macro.Validate(m); err != nil {
		t.Errorf("target is not valid schema: %v", err)
	}
}

func TestTargetFromRawWithoutUsableLocatorIsAGap(t *testing.T) {
	if _, err := targetFromRaw(&rawTarget{Locators: []rawLocator{{Kind: "css", Value: ""}}}, nil); err == nil || err.Error() != "no-locator" {
		t.Errorf("err = %v, want no-locator", err)
	}
	if _, err := targetFromRaw(nil, nil); err == nil || err.Error() != "no-target" {
		t.Errorf("err = %v, want no-target", err)
	}
}

func TestConvertStepNeverKeepsSecretsOrBrokenTemplates(t *testing.T) {
	tgt := &rawTarget{Locators: []rawLocator{{Kind: "css", Value: "#pw"}}}
	st, err := convertStep(rawEvent{Action: "fill", Target: tgt, Value: "hunter2", Sensitive: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Value.Expr != "{{password}}" {
		t.Errorf("password step value = %q, want the placeholder", st.Value.Expr)
	}
	st, err = convertStep(rawEvent{Action: "fill", Target: &rawTarget{Locators: []rawLocator{{Kind: "text", Value: "{{x}}"}}}, Value: "a {{b}} c"}, nil)
	if err != nil {
		t.Fatalf("braces must be escaped, not dropped: %v", err)
	}
	if got, _ := st.Value.Eval(nil); got != "a {{b}} c" {
		t.Errorf("escaped value renders %q", got)
	}
	if got, _ := (macro.Value{Expr: st.Target.Locators[0].Value}).Eval(nil); got != "{{x}}" {
		t.Errorf("escaped locator renders %q", got)
	}
	if _, err := convertStep(rawEvent{Action: "teleport", Target: tgt}, nil); err == nil {
		t.Error("unknown action should be a gap")
	}
	if st, err := convertStep(rawEvent{Action: "dblclick", Target: tgt}, nil); err != nil || st.Action != macro.ActionDblClick {
		t.Errorf("dblclick: %+v %v", st, err)
	}
}

func TestParameterizeReplacesLiteralsAndDeclaresColumns(t *testing.T) {
	m := &macro.Macro{SchemaVersion: 1, ID: "p", Name: "p", AllowedOrigins: []string{"http://127.0.0.1:1"},
		Steps: []macro.Step{
			{ID: "a", Action: macro.ActionFill, Target: &macro.Target{Locators: []macro.Locator{{Kind: macro.LocCSS, Value: "#a"}}}, Value: &macro.Value{Expr: "Anna"}},
			{ID: "b", Action: macro.ActionSelect, Target: &macro.Target{Locators: []macro.Locator{{Kind: macro.LocCSS, Value: "#b"}}}, Value: &macro.Value{Expr: "de"}},
			{ID: "c", Action: macro.ActionFill, Target: &macro.Target{Locators: []macro.Locator{{Kind: macro.LocCSS, Value: "#c"}}}, Value: &macro.Value{Expr: "Anna"}},
		}}
	n, err := Parameterize(m, map[string]string{"first_name": "Anna", "country": "de"})
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v, want 3 rewrites", n, err)
	}
	if m.Steps[0].Value.Expr != "{{first_name}}" || m.Steps[1].Value.Expr != "{{country}}" || m.Steps[2].Value.Expr != "{{first_name}}" {
		t.Errorf("values = %q %q %q", m.Steps[0].Value.Expr, m.Steps[1].Value.Expr, m.Steps[2].Value.Expr)
	}
	if len(m.Variables) != 2 {
		t.Errorf("variables = %+v, want both columns declared once", m.Variables)
	}
	if err := macro.Validate(m); err != nil {
		t.Errorf("parameterized macro invalid: %v", err)
	}
	got, _ := m.Steps[0].Value.Eval(map[string]string{"first_name": "Ben"})
	if got != "Ben" {
		t.Errorf("eval = %q, want Ben", got)
	}
}

func TestParameterizeUnknownLiteralChangesNothing(t *testing.T) {
	m := &macro.Macro{SchemaVersion: 1, ID: "p", Name: "p", AllowedOrigins: []string{"http://127.0.0.1:1"},
		Steps: []macro.Step{{ID: "a", Action: macro.ActionFill, Target: &macro.Target{Locators: []macro.Locator{{Kind: macro.LocCSS, Value: "#a"}}}, Value: &macro.Value{Expr: "Anna"}}}}
	_, err := Parameterize(m, map[string]string{"first_name": "Anna", "city": "Oslo"})
	var cu *ColumnUnusedError
	if !errors.As(err, &cu) || cu.Column != "city" {
		t.Fatalf("err = %v, want ColumnUnusedError for city", err)
	}
	if m.Steps[0].Value.Expr != "Anna" || len(m.Variables) != 0 {
		t.Error("macro was changed although Parameterize failed")
	}
}

func TestWarningsCarryKindIndexLocatorAndNoStep(t *testing.T) {
	r := New(Options{})
	tgt := &rawTarget{Locators: []rawLocator{{Kind: "css", Value: "#cv"}}}
	r.onWarn(rawEvent{Kind: "canvas_unsupported", Msg: "canvas", Target: tgt})
	r.onWarn(rawEvent{Kind: "canvas_unsupported", Msg: "canvas", Target: tgt}) // repeat at the same position
	r.append(macro.Step{Action: macro.ActionClick, Target: &macro.Target{Locators: []macro.Locator{{Kind: macro.LocCSS, Value: "#b"}}}})
	r.onWarn(rawEvent{Kind: "file_input_unsupported", Msg: "file", Target: &rawTarget{Locators: []rawLocator{{Kind: "label", Value: "Resume"}}}})
	r.onWarn(rawEvent{Kind: "bogus", Msg: "x"})
	want := []Warning{
		{Kind: WarnCanvasUnsupported, StepIndex: 0, Locator: macro.Locator{Kind: macro.LocCSS, Value: "#cv"}, Message: "canvas"},
		{Kind: WarnFileInputUnsupported, StepIndex: 1, Locator: macro.Locator{Kind: macro.LocLabel, Value: "Resume"}, Message: "file"},
	}
	got := r.Warnings()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("warnings = %+v, want %+v", got, want)
	}
	if len(r.steps) != 1 {
		t.Errorf("a warning must not record a step, steps = %d", len(r.steps))
	}
}
