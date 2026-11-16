package player

import (
	"errors"
	"strings"
	"testing"

	"github.com/8bu/ladilala/core/macro"
)

func testMacro() *macro.Macro {
	return &macro.Macro{
		SchemaVersion: 1, ID: "t", Name: "t", AllowedOrigins: []string{"https://app.example.com"},
		Variables: []macro.VariableDecl{
			{Name: "first_name", Type: macro.VarString, Required: true},
			{Name: "amount", Type: macro.VarNumber, Default: float64(100)},
		},
		Steps: []macro.Step{
			{ID: "open", Action: macro.ActionNavigate, Value: &macro.Value{Expr: "https://{{host}}/start"}},
			{ID: "name", Action: macro.ActionFill, Target: &macro.Target{Locators: []macro.Locator{{Kind: macro.LocCSS, Value: "#n"}}},
				Value: &macro.Value{Expr: "{{first_name}} {{amount}}"}},
		},
	}
}

func TestBuildVarsRequiredDefaultAndRow(t *testing.T) {
	m := testMacro()
	if _, err := BuildVars(m, map[string]string{}); !errors.Is(err, ErrMissingColumn) {
		t.Fatalf("err = %v, want ErrMissingColumn for the required variable", err)
	}
	var mc *MissingColumnError
	_, err := BuildVars(m, nil)
	if !errors.As(err, &mc) || mc.Column != "first_name" || mc.Step != "" {
		t.Fatalf("err = %#v, want MissingColumnError{first_name}", err)
	}
	vars, err := BuildVars(m, map[string]string{"first_name": "Anna"})
	if err != nil {
		t.Fatal(err)
	}
	if vars["amount"] != "100" {
		t.Errorf("number default = %q, want 100", vars["amount"])
	}
	vars, _ = BuildVars(m, map[string]string{"first_name": "Anna", "amount": "7"})
	if vars["amount"] != "7" {
		t.Errorf("row must override the default, got %q", vars["amount"])
	}
}

func TestPreflightSubstitutesAndNamesTheStep(t *testing.T) {
	m := testMacro()
	m.Variables = append(m.Variables, macro.VariableDecl{Name: "host", Type: macro.VarString})
	// "host" is declared but optional and absent: the navigate step needs it.
	_, err := Preflight(m, map[string]string{"first_name": "Anna"})
	var se *StepError
	var mc *MissingColumnError
	if !errors.As(err, &se) || !errors.As(err, &mc) || se.StepID != "open" || mc.Column != "host" || mc.Step != "open" {
		t.Fatalf("err = %v, want StepError(open) wrapping MissingColumnError(host)", err)
	}
	if !strings.Contains(err.Error(), "open") || !strings.Contains(err.Error(), "host") {
		t.Errorf("message %q must name the step and the column", err)
	}
	vars, err := Preflight(m, map[string]string{"first_name": "Anna", "host": "app.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := evalExpr("name", *m.Steps[1].Value, vars)
	if got != "Anna 100" {
		t.Errorf("rendered value = %q, want %q", got, "Anna 100")
	}
}

func TestPreflightIgnoresDisabledSteps(t *testing.T) {
	m := testMacro()
	m.Variables = append(m.Variables, macro.VariableDecl{Name: "host", Type: macro.VarString})
	if _, err := Preflight(m, map[string]string{"first_name": "Anna"}); err == nil {
		t.Fatal("setup: the open step needs host and must fail")
	}
	m.Steps[0].Disabled = true
	if _, err := Preflight(m, map[string]string{"first_name": "Anna"}); err != nil {
		t.Errorf("a disabled step must not be evaluated: %v", err)
	}
}

func TestPreflightRefusesForeignOriginBeforeTouchingTheBrowser(t *testing.T) {
	m := testMacro()
	m.Variables = append(m.Variables, macro.VariableDecl{Name: "host", Type: macro.VarString})
	_, err := Preflight(m, map[string]string{"first_name": "Anna", "host": "evil.example.net"})
	var oe *OriginError
	if !errors.Is(err, ErrOrigin) || !errors.As(err, &oe) || oe.URL != "https://evil.example.net/start" {
		t.Fatalf("err = %v, want OriginError for https://evil.example.net/start", err)
	}
}

func TestLocatorErrorMessagesAndSentinels(t *testing.T) {
	e := &LocatorError{Reason: ErrAmbiguous, Count: 3, Tried: []Tried{{"role", 3}, {"css", 0}}}
	if !errors.Is(e, ErrAmbiguous) || errors.Is(e, ErrNotFound) {
		t.Error("LocatorError must match its reason only")
	}
	msg := e.Error()
	for _, want := range []string{"more than one", "3 matches", "role=3", "css=0"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	wrapped := &StepError{StepID: "go", Action: macro.ActionClick, Err: &LocatorError{Reason: ErrNotFound}}
	if !errors.Is(wrapped, ErrNotFound) || !strings.Contains(wrapped.Error(), "step go (click)") {
		t.Errorf("StepError = %v", wrapped)
	}
}

func TestCheckDateValue(t *testing.T) {
	cases := []struct {
		typ, v string
		ok     bool
	}{
		{"date", "2026-01-15", true}, {"date", "2024-02-29", true}, {"date", "2026-02-29", false}, {"date", "15/01/2026", false},
		{"date", "2026-1-5", false}, {"date", "", true},
		{"time", "09:05", true}, {"time", "23:59:59", true}, {"time", "24:00", false}, {"time", "9:5", false},
		{"datetime-local", "2026-01-15T09:05", true}, {"datetime-local", "2026-01-15 09:05", false}, {"datetime-local", "2026-01-15", false},
		{"month", "2026-12", true}, {"month", "2026-13", false}, {"month", "12/2026", false},
		{"week", "2026-W03", true}, {"week", "2020-W53", true}, {"week", "2027-W53", false}, {"week", "2026-W00", false}, {"week", "2026-03", false},
	}
	for _, c := range cases {
		if got := checkDateValue(c.typ, c.v); got != c.ok {
			t.Errorf("checkDateValue(%s, %q) = %v, want %v", c.typ, c.v, got, c.ok)
		}
	}
}

func TestBadValueErrorNamesFieldAndFormat(t *testing.T) {
	err := error(&BadValueError{Step: "s1", Field: "start", Type: "date", Value: "15/01/2026", Want: "YYYY-MM-DD"})
	if !errors.Is(err, ErrBadValue) {
		t.Fatal("must wrap ErrBadValue")
	}
	for _, want := range []string{`"start"`, "15/01/2026", "expected YYYY-MM-DD", "s1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lacks %q", err, want)
		}
	}
}

func TestBuildVarsIgnoresRequiredVariableOfDisabledStepOnly(t *testing.T) {
	m := testMacro()
	m.Variables = append(m.Variables, macro.VariableDecl{Name: "host", Type: macro.VarString, Required: true})
	if _, err := BuildVars(m, map[string]string{"first_name": "Anna"}); err == nil {
		t.Fatal("setup: an enabled step reads host, it must be required")
	}
	m.Steps[0].Disabled = true
	if _, err := BuildVars(m, map[string]string{"first_name": "Anna"}); err != nil {
		t.Errorf("host is read only by a disabled step: %v", err)
	}
}
