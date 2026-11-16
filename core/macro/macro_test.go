package macro

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func loadFile(t *testing.T, name string) (*Macro, error) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return Load(f)
}

func TestLoadValid(t *testing.T) {
	m, err := loadFile(t, "valid.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Steps) != 8 || len(m.Guards) != 2 || len(m.Variables) != 4 {
		t.Fatalf("unexpected shape: %d steps, %d guards, %d vars", len(m.Steps), len(m.Guards), len(m.Variables))
	}
	if got := m.Steps[1].Target.FramePath; !reflect.DeepEqual(got, []string{"iframe#login"}) {
		t.Fatalf("framePath = %v", got)
	}
	if m.Steps[0].Timeout != Duration(30e9) {
		t.Fatalf("timeout = %v", m.Steps[0].Timeout)
	}

	// Marshal -> Load must be lossless.
	out, err := Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := Load(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reflect.DeepEqual(m, m2) {
		t.Fatal("round trip changed the macro")
	}
}

func TestLoadFailures(t *testing.T) {
	// decodeErr: a plain (non-validation) error containing the substring.
	// paths: the exact set of validation problem paths.
	cases := []struct {
		file      string
		decodeErr string
		paths     []string
	}{
		{file: "invalid_unknown_field.json", decodeErr: `unknown field "colour"`},
		{file: "invalid_version_future.json", decodeErr: "newer than supported"},
		{file: "invalid_version_missing.json", decodeErr: "schemaVersion is missing"},
		{file: "invalid_trailing_data.json", decodeErr: "after top-level value"},
		{file: "invalid_duplicate_step_id.json", paths: []string{"steps[2].id"}},
		{file: "invalid_unknown_action.json", paths: []string{"steps[hover1].action"}},
		{file: "invalid_missing_fields.json", paths: []string{
			"steps[f].target", "steps[f].value", "steps[n].value", "steps[w]", "steps[as].expect",
			"steps[loc].target.locators[0].value", "steps[loc].target.locators[1].kind", "steps[loc].target.locators[2].name",
		}},
		{file: "invalid_commit_point.json", paths: []string{"steps[go].commitPoint"}},
		{file: "invalid_undeclared_variable.json", paths: []string{
			"steps[f].target.locators[0].value", "steps[f].value.expr",
		}},
		{file: "invalid_origins.json", paths: []string{
			"allowedOrigins[0]", "allowedOrigins[1]", "allowedOrigins[2]", "allowedOrigins[4]", "allowedOrigins[5]",
		}},
		{file: "invalid_navigate_outside_origin.json", paths: []string{
			"steps[evil].value.expr", "steps[bare].value.expr",
		}},
		{file: "invalid_transform.json", paths: []string{
			"steps[t].value.expr", "steps[t].value.transforms[1]", "steps[t].value.transforms[2]",
			"steps[t].value.transforms[3]", "steps[t].value.transforms[4]",
		}},
		{file: "invalid_guards.json", paths: []string{
			"guards[0].pattern", "guards[1].locator", "guards[2].pattern", "guards[2].action", "guards[3].kind",
		}},
		{file: "invalid_variables.json", paths: []string{
			"variables[1].name", "variables[2].name", "variables[3].type", "variables[4].default", "variables[5].default",
		}},
		{file: "invalid_step_limits.json", paths: []string{
			"steps[s].timeout", "steps[s].retries", "steps[s].onFail",
			"steps[s].expect[0].value", "steps[s].expect[1].value", "steps[s].expect[2].target.locators", "steps[s].expect[3].kind",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			m, err := loadFile(t, tc.file)
			if err == nil {
				t.Fatalf("expected an error, got macro %q", m.ID)
			}
			var ve *ValidationError
			isVal := errors.As(err, &ve)
			if tc.decodeErr != "" {
				if isVal {
					t.Fatalf("want decode error, got validation error: %v", err)
				}
				if !strings.Contains(err.Error(), tc.decodeErr) {
					t.Fatalf("error %q does not contain %q", err, tc.decodeErr)
				}
				return
			}
			if !isVal {
				t.Fatalf("want *ValidationError, got %T: %v", err, err)
			}
			var got []string
			for _, p := range ve.Problems {
				got = append(got, p.Path)
			}
			want := append([]string(nil), tc.paths...)
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("problem paths\n got: %q\nwant: %q\nfull: %v", got, want, err)
			}
		})
	}
}

func TestMarshalRejectsInvalid(t *testing.T) {
	if _, err := Marshal(&Macro{}); err == nil {
		t.Fatal("expected validation error for an empty macro")
	}
}

func TestMigrate(t *testing.T) {
	in := []byte(`{"schemaVersion":1}`)
	out, err := Migrate(in)
	if err != nil || !bytes.Equal(in, out) {
		t.Fatalf("v1 must pass through unchanged: %q, %v", out, err)
	}
	for _, bad := range []string{`{"schemaVersion":0}`, `{"schemaVersion":2}`, `{}`, `[`} {
		if _, err := Migrate([]byte(bad)); err == nil {
			t.Errorf("Migrate(%s) should fail", bad)
		}
	}
}

func TestCommitPointActions(t *testing.T) {
	steps := map[Action]Step{
		ActionClick:  {Target: &Target{Locators: []Locator{{Kind: LocCSS, Value: "b"}}}},
		ActionFill:   {Target: &Target{Locators: []Locator{{Kind: LocCSS, Value: "b"}}}, Value: &Value{Expr: "x"}},
		ActionPress:  {Value: &Value{Expr: "Enter"}},
		ActionSelect: {Target: &Target{Locators: []Locator{{Kind: LocCSS, Value: "b"}}}, Value: &Value{Expr: "x"}},
		ActionManual: {Value: &Value{Expr: "do it"}},
	}
	for action, s := range steps {
		s.ID, s.Action, s.CommitPoint = "s", action, true
		m := &Macro{SchemaVersion: 1, ID: "m", Name: "M", AllowedOrigins: []string{"https://a.com"}, Steps: []Step{s}}
		err := Validate(m)
		allowed := action == ActionClick || action == ActionFill || action == ActionPress
		if allowed && err != nil {
			t.Errorf("%s: unexpected error %v", action, err)
		}
		if !allowed && (err == nil || !strings.Contains(err.Error(), "steps[s].commitPoint")) {
			t.Errorf("%s: want commitPoint error, got %v", action, err)
		}
	}
}

func TestEval(t *testing.T) {
	vars := map[string]string{"name": "  Ada Lovelace ", "day": "2026-03-07", "phone": "+84 (90) 123-4567", "q": "a b&c"}
	cases := []struct {
		v    Value
		want string
	}{
		{Value{Expr: "plain"}, "plain"},
		{Value{Expr: "{{name}}"}, "  Ada Lovelace "},
		{Value{Expr: "{{name}}", Transforms: []string{"trim", "upper"}}, "ADA LOVELACE"},
		{Value{Expr: "Hi {{ name | trim | lower }}!"}, "Hi ada lovelace!"},
		{Value{Expr: "{{day|date:DD/MM/YYYY}}"}, "07/03/2026"},
		{Value{Expr: "{{day|date:YY.M.D-HH:mm:ss}}"}, "26.M.D-00:00:00"},
		{Value{Expr: "{{phone|digits}}"}, "84901234567"},
		{Value{Expr: "q={{q|urlencode}}"}, "q=a+b%26c"},
		{Value{Expr: "{{day}}", Transforms: []string{"date:YYYY"}}, "2026"},
	}
	for _, tc := range cases {
		got, err := tc.v.Eval(vars)
		if err != nil || got != tc.want {
			t.Errorf("Eval(%+v) = %q, %v; want %q", tc.v, got, err, tc.want)
		}
	}
	for _, bad := range []Value{
		{Expr: "{{nope}}"},
		{Expr: "{{name"},
		{Expr: "{{a{{b}}}}"},
		{Expr: "{{name|bogus}}"},
		{Expr: "{{name|date:YYYY}}"}, // not a date
		{Expr: "{{1x}}"},
	} {
		if got, err := bad.Eval(vars); err == nil {
			t.Errorf("Eval(%+v) = %q, want error", bad, got)
		}
	}
}

func TestAllowsURL(t *testing.T) {
	m := &Macro{AllowedOrigins: []string{"https://a.com", "http://localhost:8080", "https://*.b.com"}}
	for url, want := range map[string]bool{
		"https://a.com/x?y=1":     true,
		"https://A.com":           true,
		"http://a.com/":           false, // scheme mismatch
		"https://a.com:444/":      false, // port mismatch
		"http://localhost:8080/p": true,
		"https://x.b.com/":        true,
		"https://x.y.b.com/":      true,
		"https://b.com/":          false,
		"https://evilb.com/":      false,
		"https://a.com@evil.com/": false,
		"javascript:alert(1)":     false,
		"not a url":               false,
	} {
		if got := m.AllowsURL(url); got != want {
			t.Errorf("AllowsURL(%q) = %v, want %v", url, got, want)
		}
	}
}

// The package must stay independent of the runtime, profile and driver code.
func TestStdlibOnly(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if first, _, _ := strings.Cut(p, "/"); strings.Contains(first, ".") {
				t.Errorf("%s imports non-stdlib package %s", f, p)
			}
		}
	}
}

func TestDblClickStepAndBackwardCompat(t *testing.T) {
	doc := `{"schemaVersion":1,"id":"d","name":"d","allowedOrigins":["https://a.com"],"steps":[
{"id":"s1","action":"dblclick","target":{"locators":[{"kind":"css","value":"#b"}]}},
{"id":"s2","action":"click","target":{"locators":[{"kind":"css","value":"#b"}]}}]}`
	m, err := Load(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if m.Steps[0].Action != ActionDblClick {
		t.Fatalf("action = %q", m.Steps[0].Action)
	}
	if _, err := Load(strings.NewReader(strings.Replace(doc, `"action":"dblclick",`, `"action":"dblclick","point":{"x":0.5},`, 1))); err == nil {
		t.Error("unknown field must still be rejected")
	}
	m.Steps[0].Target = nil
	var ve *ValidationError
	if err := Validate(m); !errors.As(err, &ve) || ve.Problems[0].Path != "steps[s1].target" {
		t.Errorf("dblclick without target: err = %v", err)
	}
	if _, err := loadFile(t, "valid.json"); err != nil {
		t.Errorf("earlier v1 macro no longer loads: %v", err)
	}
}

func TestEscapedBraces(t *testing.T) {
	for _, lit := range []string{"{{", "a {{b}} c", `x\{{y`, "{{{{", "{{{x", `plain \ text`, ""} {
		got, err := Value{Expr: EscapeLiteral(lit)}.Eval(nil)
		if err != nil || got != lit {
			t.Errorf("round trip of %q = %q, %v", lit, got, err)
		}
	}
	got, err := Value{Expr: `\{{name}} is {{name}}`}.Eval(map[string]string{"name": "Ann"})
	if err != nil || got != "{{name}} is Ann" {
		t.Errorf("mixed = %q, %v", got, err)
	}
	m := &Macro{SchemaVersion: 1, ID: "m", Name: "m", AllowedOrigins: []string{"https://a.com"}, Steps: []Step{
		{ID: "s", Action: ActionClick, Target: &Target{Locators: []Locator{{Kind: LocText, Value: `\{{literal}}`}}}}}}
	if err := Validate(m); err != nil {
		t.Errorf("escaped braces in a locator must validate: %v", err)
	}
}
