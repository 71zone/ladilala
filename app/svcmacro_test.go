package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/8bu/ladilala/core/engine"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/store"
)

// svcmEnv is a real SQLite store in a temp dir with in-memory secrets.
type svcmEnv struct {
	b       *Backend
	secrets *store.MemorySecrets
	macros  *MacroService
	data    *DataService
}

func newSvcmEnv(t *testing.T) *svcmEnv {
	t.Helper()
	dir := t.TempDir()
	sec := &store.MemorySecrets{}
	st, err := store.Open(context.Background(), dir, sec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	b := &Backend{DataDir: dir, Store: st, Launcher: &engine.StockLauncher{Unverified: true}}
	return &svcmEnv{b: b, secrets: sec, macros: newMacroService(b), data: newDataService(b)}
}

func apiCode(t *testing.T, err error) string {
	t.Helper()
	var a *APIError
	if !errors.As(err, &a) {
		t.Fatalf("error %v is not an APIError", err)
	}
	return a.Code
}

func apiProblems(t *testing.T, err error) []ProblemDTO {
	t.Helper()
	var a *APIError
	if !errors.As(err, &a) {
		t.Fatalf("error %v is not an APIError", err)
	}
	ps, _ := a.Details["problems"].([]ProblemDTO)
	return ps
}

func fillStep(id, css, literal string) macro.Step {
	return macro.Step{ID: id, Action: macro.ActionFill, Target: &macro.Target{Locators: []macro.Locator{{Kind: macro.LocCSS, Value: css}}}, Value: &macro.Value{Expr: literal}}
}

// formMacro has two fill steps with the same literal and one select step.
func formMacro(name string) macro.Macro {
	return macro.Macro{
		SchemaVersion: 1, Name: name, AllowedOrigins: []string{"https://shop.example"},
		Variables: []macro.VariableDecl{{Name: "name", Type: macro.VarString, Required: true}, {Name: "zip", Type: macro.VarString}, {Name: "email", Type: macro.VarString, Required: true}},
		Steps: []macro.Step{
			{ID: "open", Action: macro.ActionNavigate, Value: &macro.Value{Expr: "https://shop.example/signup"}},
			fillStep("first", "#first", "Anna"),
			fillStep("second", "#second", "Anna"),
			{ID: "go", Action: macro.ActionClick, Target: &macro.Target{Locators: []macro.Locator{{Kind: macro.LocCSS, Value: "button"}}}, CommitPoint: true},
		},
	}
}

func (e *svcmEnv) save(t *testing.T, m macro.Macro) MacroDTO {
	t.Helper()
	d, err := e.macros.SaveMacro(context.Background(), SaveMacroInput{Macro: m})
	if err != nil {
		t.Fatalf("SaveMacro: %v", err)
	}
	return d
}

func TestMacroSaveGetVersionAndName(t *testing.T) {
	e := newSvcmEnv(t)
	ctx := context.Background()
	d := e.save(t, formMacro("Signup"))
	if d.Macro.ID == "" || d.Version != 1 || d.Hash == "" {
		t.Fatalf("saved = %+v", d)
	}
	m := d.Macro
	m.Steps[1].Value.Expr = "Ben"
	d2, err := e.macros.SaveMacro(ctx, SaveMacroInput{Macro: m, Description: "fills the form"})
	if err != nil || d2.Version != 2 || d2.Hash == d.Hash || d2.Description != "fills the form" {
		t.Fatalf("update = %+v, %v", d2, err)
	}
	got, err := e.macros.GetMacro(ctx, d.Macro.ID)
	if err != nil || got.Macro.Steps[1].Value.Expr != "Ben" || got.Description != "fills the form" {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if _, err := e.macros.SaveMacro(ctx, SaveMacroInput{Macro: formMacro(" signup ")}); apiCode(t, err) != "conflict" {
		t.Errorf("a taken name must give conflict, got %v", err)
	}
	if _, err := e.macros.GetMacro(ctx, "nope"); apiCode(t, err) != "notFound" {
		t.Errorf("unknown id: %v", err)
	}
	list, err := e.macros.ListMacros(ctx)
	if err != nil || len(list) != 1 || list[0].StepCount != 4 || len(list[0].Variables) != 3 {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestMacroValidationErrorsHavePaths(t *testing.T) {
	e := newSvcmEnv(t)
	ctx := context.Background()
	m := formMacro("Bad")
	m.Steps[1].Value.Expr = "{{undeclared}}"
	m.AllowedOrigins = nil
	_, err := e.macros.SaveMacro(ctx, SaveMacroInput{Macro: m})
	if apiCode(t, err) != "invalid" {
		t.Fatalf("want invalid, got %v", err)
	}
	var paths []string
	for _, p := range apiProblems(t, err) {
		paths = append(paths, p.Path)
	}
	if got := strings.Join(paths, ","); !strings.Contains(got, "allowedOrigins") || strings.Contains(got, "steps[first]") {
		t.Errorf("problem paths = %v; allowedOrigins must fail and the new variable in steps[first] must not", paths)
	}
	if len(m.Variables) != 3 {
		t.Errorf("ValidateMacro and SaveMacro must not change the caller's macro: %+v", m.Variables)
	}
	live, err := e.macros.ValidateMacro(ctx, m)
	if err != nil || len(live) != len(paths) {
		t.Errorf("ValidateMacro = %v, %v; want %d problems", live, err, len(paths))
	}
	ok, _ := e.macros.ValidateMacro(ctx, formMacro("Fine"))
	if len(ok) != 0 {
		t.Errorf("valid macro reported %v", ok)
	}
}

func TestMacroSaveAutoDeclaresVariables(t *testing.T) {
	e := newSvcmEnv(t)
	m := formMacro("Auto")
	m.Steps[1].Value.Expr = "{{undeclared}}"
	if p, err := e.macros.ValidateMacro(context.Background(), m); err != nil || len(p) != 0 {
		t.Fatalf("ValidateMacro = %v, %v", p, err)
	}
	d := e.save(t, m)
	found := false
	for _, v := range d.Macro.Variables {
		if v.Name == "undeclared" && v.Type == macro.VarString && !v.Required {
			found = true
		}
	}
	if !found {
		t.Errorf("saved macro must list the auto-declared variable: %+v", d.Macro.Variables)
	}
}

func TestMacroImportExportRoundTrip(t *testing.T) {
	e := newSvcmEnv(t)
	ctx := context.Background()
	d := e.save(t, formMacro("Signup"))
	path := filepath.Join(t.TempDir(), "signup.json")
	if err := e.macros.ExportMacro(ctx, d.Macro.ID, path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := macro.Load(strings.NewReader(string(raw))); err != nil {
		t.Fatalf("export is not a loadable macro: %v", err)
	}
	rev, err := e.macros.ImportMacro(ctx, string(raw))
	if err != nil || rev.ReviewID == "" || len(rev.Problems) != 0 {
		t.Fatalf("review = %+v, %v", rev, err)
	}
	if len(rev.AllowedOrigins) != 1 || rev.AllowedOrigins[0] != "https://shop.example" || len(rev.CommitPointSteps) != 1 || rev.CommitPointSteps[0] != "go" {
		t.Errorf("review must show origins and commit points: %+v", rev)
	}
	if list, _ := e.macros.ListMacros(ctx); len(list) != 1 {
		t.Fatalf("an import review must save nothing, list has %d", len(list))
	}
	got, err := e.macros.ConfirmImport(ctx, rev.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Macro.ID == d.Macro.ID || got.Macro.Name != "Signup (2)" || len(got.Macro.Steps) != 4 {
		t.Errorf("imported = id %s name %q steps %d", got.Macro.ID, got.Macro.Name, len(got.Macro.Steps))
	}
	if _, err := e.macros.ConfirmImport(ctx, rev.ReviewID); apiCode(t, err) != "notFound" {
		t.Errorf("a review can be confirmed once: %v", err)
	}
}

func TestMacroImportReportsProblemsWithPaths(t *testing.T) {
	e := newSvcmEnv(t)
	ctx := context.Background()
	raw, err := os.ReadFile("../core/macro/testdata/invalid_undeclared_variable.json")
	if err != nil {
		t.Fatal(err)
	}
	rev, err := e.macros.ImportMacro(ctx, string(raw))
	if err != nil || rev.ReviewID != "" || len(rev.Problems) == 0 || rev.Problems[0].Path == "" || rev.Problems[0].Path == "$" {
		t.Fatalf("validation problems must carry document paths: %+v, %v", rev, err)
	}
	for _, name := range []string{"invalid_trailing_data.json", "invalid_unknown_field.json", "invalid_version_future.json"} {
		raw, _ := os.ReadFile("../core/macro/testdata/" + name)
		rev, err := e.macros.ImportMacro(ctx, string(raw))
		if err != nil || rev.ReviewID != "" || len(rev.Problems) == 0 {
			t.Errorf("%s must be rejected by the strict loader: %+v, %v", name, rev, err)
		}
	}
	if _, err := e.macros.ConfirmImport(ctx, ""); apiCode(t, err) != "notFound" {
		t.Errorf("empty review id: %v", err)
	}
}

func TestMacroDuplicateAndDelete(t *testing.T) {
	e := newSvcmEnv(t)
	ctx := context.Background()
	d := e.save(t, formMacro("Signup"))
	if err := e.macros.SetMacroSource(ctx, d.Macro.ID, ""); err != nil {
		t.Fatal(err)
	}
	cp, err := e.macros.DuplicateMacro(ctx, d.Macro.ID, "Signup copy")
	if err != nil || cp.Macro.ID == d.Macro.ID || cp.Macro.Name != "Signup copy" || len(cp.Macro.Steps) != 4 {
		t.Fatalf("duplicate = %+v, %v", cp, err)
	}
	cp2, err := e.macros.DuplicateMacro(ctx, d.Macro.ID, "Signup copy")
	if err != nil || cp2.Macro.Name != "Signup copy (2)" {
		t.Fatalf("second duplicate = %q, %v", cp2.Macro.Name, err)
	}

	body, _ := macro.Marshal(&d.Macro)
	run, err := e.b.Store.Runs.Create(ctx, store.RunRecord{MacroID: d.Macro.ID, MacroName: d.Macro.Name, MacroVersion: d.Version, MacroHash: d.Hash, MacroBody: body, State: "running", ProfileIDs: []string{"p1"}, RowTo: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = e.macros.DeleteMacro(ctx, d.Macro.ID)
	if apiCode(t, err) != "conflict" {
		t.Fatalf("delete while a run uses the macro must give conflict, got %v", err)
	}
	if _, err := e.macros.GetMacro(ctx, d.Macro.ID); err != nil {
		t.Errorf("the macro must still exist: %v", err)
	}
	if _, err := e.b.Store.DB().ExecContext(ctx, `UPDATE runs SET state='finished' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.macros.DeleteMacro(ctx, d.Macro.ID); err != nil {
		t.Fatalf("a finished run keeps its own copy and must not block: %v", err)
	}
	if err := e.macros.DeleteMacro(ctx, d.Macro.ID); apiCode(t, err) != "notFound" {
		t.Errorf("second delete: %v", err)
	}
}

func TestMapVariableChangesOnlyTheChosenStep(t *testing.T) {
	e := newSvcmEnv(t)
	ctx := context.Background()
	d := e.save(t, formMacro("Signup"))
	out, err := e.macros.MapVariable(ctx, d.Macro.ID, "first", "first_name")
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Macro.Steps[1].Value.Expr; got != "{{first_name}}" {
		t.Errorf("mapped step = %q", got)
	}
	if got := out.Macro.Steps[2].Value.Expr; got != "Anna" {
		t.Errorf("the other step with the same text must keep it, got %q", got)
	}
	declared := false
	for _, v := range out.Macro.Variables {
		if v.Name == "first_name" && v.Required && v.Type == macro.VarString {
			declared = true
		}
	}
	if !declared {
		t.Errorf("first_name is not declared: %+v", out.Macro.Variables)
	}
	for _, c := range []struct{ step, col, code string }{{"go", "x", "invalid"}, {"nope", "x", "notFound"}, {"second", " ", "invalid"}, {"second", "bad name", "invalid"}} {
		if _, err := e.macros.MapVariable(ctx, d.Macro.ID, c.step, c.col); apiCode(t, err) != c.code {
			t.Errorf("MapVariable(%s,%q) = %v, want %s", c.step, c.col, err, c.code)
		}
	}
	if got, _ := e.macros.GetMacro(ctx, d.Macro.ID); got.Macro.Steps[2].Value.Expr != "Anna" {
		t.Errorf("a refused call must change nothing")
	}
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDataPreviewOfTheThreeFormats(t *testing.T) {
	e := newSvcmEnv(t)
	ctx := context.Background()
	for _, f := range []struct {
		file, kind string
		total      int
	}{{"sample.csv", "csv", 4}, {"sample.json", "json", 4}, {"sample.xlsx", "excel", 4}} {
		p, err := e.data.InspectFile(ctx, FileSourceInput{Path: "../core/data/testdata/" + f.file})
		if err != nil {
			t.Fatalf("%s: %v", f.file, err)
		}
		if p.Kind != f.kind || len(p.Columns) != 4 || p.Columns[0] != "name" || len(p.Rows) == 0 || p.Rows[0][0] != "Nguyễn Văn An" {
			t.Errorf("%s: preview = %+v", f.file, p)
		}
		if p.Total < 3 {
			t.Errorf("%s: total = %d", f.file, p.Total)
		}
		src, err := e.data.CreateSourceFromFile(ctx, FileSourceInput{Path: "../core/data/testdata/" + f.file})
		if err != nil || src.Kind != f.kind || src.Name != f.file {
			t.Fatalf("%s: create = %+v, %v", f.file, src, err)
		}
		pv, err := e.data.PreviewSource(ctx, src.ID, 1, 2)
		if err != nil || len(pv.Rows) != 2 || pv.From != 1 || pv.Rows[0][1] != "100000" {
			t.Errorf("%s: PreviewSource(1,2) = %+v, %v", f.file, pv, err)
		}
		got, err := e.data.GetSource(ctx, src.ID)
		if err != nil || got.RowCount != p.Total || len(got.Columns) != 4 {
			t.Errorf("%s: GetSource = %+v, %v", f.file, got, err)
		}
	}
	x, _ := e.data.InspectFile(ctx, FileSourceInput{Path: "../core/data/testdata/sample.xlsx"})
	if len(x.Sheets) == 0 || x.Sheet == "" {
		t.Errorf("xlsx preview must name the sheets: %+v", x)
	}
	enc, err := e.data.InspectFile(ctx, FileSourceInput{Path: "../core/data/testdata/sample-1258.csv"})
	if err != nil || enc.Encoding != "windows-1258" || enc.Rows[0][0] != "Nguyễn Văn An" {
		t.Errorf("1258 detection = %q %v %v", enc.Encoding, enc.Rows, err)
	}
	semi, err := e.data.InspectFile(ctx, FileSourceInput{Path: "../core/data/testdata/sample-semicolon.csv", Delimiter: ";"})
	if err != nil || semi.Delimiter != ";" {
		t.Errorf("forced delimiter: %+v %v", semi, err)
	}
	if _, err := e.data.InspectFile(ctx, FileSourceInput{Path: writeFile(t, "a.pdf", "x")}); apiCode(t, err) != "data.format" {
		t.Errorf("unsupported extension: %v", err)
	}
	if _, err := e.data.InspectFile(ctx, FileSourceInput{Path: filepath.Join(t.TempDir(), "none.csv")}); apiCode(t, err) != "data.format" {
		t.Errorf("missing file: %v", err)
	}
	if _, err := e.data.InspectFile(ctx, FileSourceInput{Path: "../core/data/testdata/sample.xlsx", Sheet: "No such"}); apiCode(t, err) != "data.format" {
		t.Errorf("unknown sheet: %v", err)
	}
	if _, err := e.data.CreateSheetsSource(ctx, "https://docs.google.com/x"); err == nil {
		t.Errorf("Sheets must stay unavailable")
	}
}

func TestDataMappingReportCases(t *testing.T) {
	e := newSvcmEnv(t)
	ctx := context.Background()
	m := formMacro("Signup")
	m.Variables = []macro.VariableDecl{{Name: "name", Type: macro.VarString, Required: true}, {Name: "zip", Type: macro.VarString}, {Name: "Birth_day", Type: macro.VarString}, {Name: "email", Type: macro.VarString, Required: true}}
	m.Steps = append(m.Steps[:3:3], macro.Step{ID: "b", Action: macro.ActionFill, Target: &macro.Target{Locators: []macro.Locator{{Kind: macro.LocCSS, Value: "#b"}}}, Value: &macro.Value{Expr: "{{Birth_day}} {{email}} {{zip}}"}}, m.Steps[3])
	md := e.save(t, m)
	src, err := e.data.CreateSourceFromFile(ctx, FileSourceInput{Path: "../core/data/testdata/sample.csv"})
	if err != nil {
		t.Fatal(err)
	}
	mp, err := e.data.ValidateMapping(ctx, md.Macro.ID, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(mp.Missing, ",") != "Birth_day,email" || strings.Join(mp.Extra, ",") != "birthday,note" || len(mp.Duplicates) != 0 || mp.OK {
		t.Errorf("missing=%v extra=%v dup=%v ok=%v", mp.Missing, mp.Extra, mp.Duplicates, mp.OK)
	}
	var sugg string
	for _, v := range mp.Variables {
		if v.Name == "Birth_day" {
			sugg = v.Suggestion
		}
		if v.Name == "name" && v.Column != "name" {
			t.Errorf("name must bind by itself: %+v", v)
		}
	}
	if sugg != "birthday" {
		t.Errorf("suggestion = %q, want birthday", sugg)
	}
	if _, err := e.data.SetMapping(ctx, md.Macro.ID, src.ID, map[string]string{"email": "nope"}); apiCode(t, err) != "invalid" {
		t.Errorf("override to an unknown column: %v", err)
	}
	if _, err := e.data.SetMapping(ctx, md.Macro.ID, src.ID, map[string]string{"ghost": "note"}); apiCode(t, err) != "invalid" {
		t.Errorf("override of an unknown variable: %v", err)
	}
	mp, err = e.data.SetMapping(ctx, md.Macro.ID, src.ID, map[string]string{"Birth_day": "birthday", "email": "note"})
	if err != nil || strings.Join(mp.Missing, ",") != "" || !mp.OK || mp.Overrides["email"] != "note" {
		t.Fatalf("after override: %+v, %v", mp, err)
	}
	again, _ := e.data.ValidateMapping(ctx, md.Macro.ID, src.ID)
	if !again.OK || again.Overrides["Birth_day"] != "birthday" {
		t.Errorf("the override must persist: %+v", again)
	}
	if list, _ := e.macros.ListMacros(ctx); list[0].SourceID != src.ID {
		t.Errorf("SetMapping must link the source: %+v", list[0])
	}

	// A row seen through the mapping has the variable names.
	pr, err := e.macros.PreviewRow(ctx, md.Macro.ID, src.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	var rendered string
	for _, p := range pr {
		if p.StepID == "b" {
			rendered = p.Value
		}
	}
	if rendered != "1990-12-01  100000" {
		t.Errorf("PreviewRow step b = %q", rendered)
	}
	ms, err := openMappedSource(ctx, e.b, md.Macro.ID, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	rows, _ := ms.Open(ctx, 0, 1)
	_, row, _, _ := rows.Next(ctx)
	rows.Close()
	if row["email"] != "Khách quen" || row["name"] != "Nguyễn Văn An" {
		t.Errorf("mapped row = %v", row)
	}

	// Duplicate headers show up in the report.
	dup := writeFile(t, "dup.csv", "Name,name,zip\nA,B,1\n")
	ds, err := e.data.CreateSourceFromFile(ctx, FileSourceInput{Path: dup})
	if err != nil {
		t.Fatal(err)
	}
	mp, _ = e.data.ValidateMapping(ctx, md.Macro.ID, ds.ID)
	if len(mp.Duplicates) != 1 || mp.OK {
		t.Errorf("duplicates = %v ok=%v", mp.Duplicates, mp.OK)
	}
}

func TestDataValidateRows(t *testing.T) {
	e := newSvcmEnv(t)
	ctx := context.Background()
	m := formMacro("Signup")
	m.Variables = []macro.VariableDecl{{Name: "name", Type: macro.VarString, Required: true}, {Name: "email", Type: macro.VarString, Required: true}}
	m.Steps = []macro.Step{m.Steps[0], fillStep("n", "#n", "{{name}}"), fillStep("e", "#e", "{{email}}")}
	md := e.save(t, m)
	csv := writeFile(t, "rows.csv", "name,email\nAnna,a@x.test\nBen,\nCara,c@x.test,extra\nDan,d@x.test\n")
	src, err := e.data.CreateSourceFromFile(ctx, FileSourceInput{Path: csv})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.data.ValidateRows(ctx, md.Macro.ID, src.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[int]string{}
	for _, is := range res.Issues {
		codes[is.RowIndex] = is.Code
	}
	if res.Checked != 4 || len(res.Issues) != 2 || codes[1] != "emptyRequired" || codes[2] != "ragged" {
		t.Errorf("checked=%d issues=%+v", res.Checked, res.Issues)
	}
	part, _ := e.data.ValidateRows(ctx, md.Macro.ID, src.ID, 3, -1)
	if part.Checked != 1 || len(part.Issues) != 0 {
		t.Errorf("range [3,end): %+v", part)
	}
	// A required variable with no column is reported on every row.
	nc := writeFile(t, "nocol.csv", "name\nA\nB\n")
	ns, _ := e.data.CreateSourceFromFile(ctx, FileSourceInput{Path: nc})
	res, _ = e.data.ValidateRows(ctx, md.Macro.ID, ns.ID, 0, -1)
	if len(res.Issues) != 2 || res.Issues[0].Code != "noColumn" {
		t.Errorf("no column: %+v", res.Issues)
	}
}

func TestRESTSourceSecretsNeverLeave(t *testing.T) {
	const token, hdr = "tok-secret-123", "key-secret-456"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("X-Api-Key") != hdr {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"items":[{"name":"Anna","zip":"1"},{"name":"Ben","zip":"2"}]}}`))
	}))
	defer srv.Close()
	e := newSvcmEnv(t)
	ctx := context.Background()
	in := RESTSourceInput{Name: "Customers", URL: srv.URL + "/customers", Headers: map[string]string{"X-Api-Key": hdr}, AuthKind: "bearer", BearerToken: token, RowsPath: "$.data.items"}
	bad := in
	bad.BearerToken = "wrong"
	if _, err := e.data.TestRESTSource(ctx, bad); apiCode(t, err) != "data.auth" {
		t.Errorf("a refused token must give data.auth, got %v", err)
	}
	pv, err := e.data.TestRESTSource(ctx, in)
	if err != nil || len(pv.Columns) != 2 || len(pv.Rows) != 2 || pv.Total != -1 {
		t.Fatalf("test = %+v, %v", pv, err)
	}
	if n := len(e.secrets.Keys()); n != 0 {
		t.Fatalf("testing a source must store nothing, found %d secrets", n)
	}
	src, err := e.data.CreateRESTSource(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !src.HasAuth || src.AuthKind != "bearer" || len(src.HeaderNames) != 1 || src.HeaderNames[0] != "X-Api-Key" {
		t.Errorf("dto = %+v", src)
	}
	got, err := e.data.GetSource(ctx, src.ID)
	if err != nil || len(got.Columns) != 2 || got.RowCount != -1 {
		t.Fatalf("GetSource = %+v, %v", got, err)
	}
	list, _ := e.data.ListSources(ctx)
	rec, _ := e.b.Store.Sources.Get(ctx, src.ID)
	all, _ := json.Marshal([]any{src, got, list, pv})
	for name, blob := range map[string]string{"DTOs": string(all), "stored config": string(rec.Config)} {
		if strings.Contains(blob, token) || strings.Contains(blob, hdr) {
			t.Errorf("a secret is in the %s: %s", name, blob)
		}
	}
	if len(e.secrets.Keys()) != 2 {
		t.Errorf("token and headers belong in the secret store: %v", e.secrets.Keys())
	}
	if pv2, err := e.data.PreviewSource(ctx, src.ID, 0, 5); err != nil || len(pv2.Rows) != 2 {
		t.Errorf("a saved REST source must read its secrets: %+v, %v", pv2, err)
	}

	// A running run blocks the delete; afterwards the secrets go too.
	run, err := e.b.Store.Runs.Create(ctx, store.RunRecord{MacroID: "m", MacroName: "m", MacroVersion: 1, MacroHash: "h", MacroBody: []byte(`{}`), SourceID: src.ID, State: "paused", ProfileIDs: []string{"p"}, RowTo: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.data.DeleteSource(ctx, src.ID); apiCode(t, err) != "conflict" {
		t.Fatalf("delete while in use: %v", err)
	}
	_, _ = e.b.Store.DB().ExecContext(ctx, `UPDATE runs SET state='finished' WHERE id=?`, run.ID)
	if err := e.data.DeleteSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	if k := e.secrets.Keys(); len(k) != 0 {
		t.Errorf("secrets left after delete: %v", k)
	}
	if _, err := e.data.GetSource(ctx, src.ID); apiCode(t, err) != "notFound" {
		t.Errorf("deleted source: %v", err)
	}
}

func TestBasicAuthPasswordNeverInDTO(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "bob" || p != "pw-basic-789" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`[{"a":"1"}]`))
	}))
	defer srv.Close()
	e := newSvcmEnv(t)
	ctx := context.Background()
	src, err := e.data.CreateRESTSource(ctx, RESTSourceInput{Name: "B", URL: srv.URL, AuthKind: "basic", BasicUser: "bob", BasicPassword: "pw-basic-789"})
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(src)
	if strings.Contains(string(blob), "pw-basic-789") || src.BasicUser != "bob" {
		t.Errorf("dto = %s", blob)
	}
	if _, err := e.data.PreviewSource(ctx, src.ID, 0, 5); err != nil {
		t.Errorf("basic auth read: %v", err)
	}
}
