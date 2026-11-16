package data

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/8bu/ladilala/core/macro"
	"github.com/xuri/excelize/v2"
)

func templateMacro() *macro.Macro {
	loc := func(kind macro.LocatorKind, v, name string) *macro.Target {
		return &macro.Target{Locators: []macro.Locator{{Kind: kind, Value: v, Name: name}}}
	}
	return &macro.Macro{
		SchemaVersion: macro.CurrentVersion, ID: "t", Name: "t", AllowedOrigins: []string{"https://example.com"},
		Variables: []macro.VariableDecl{
			{Name: "ho_ten", Type: macro.VarString, Kind: macro.KindText, Required: true},
			{Name: "email", Type: macro.VarString, Kind: macro.KindEmail, Required: true},
			{Name: "password", Type: macro.VarString, Kind: macro.KindPassword, Required: true},
		},
		Steps: []macro.Step{
			{ID: "s1", Action: macro.ActionNavigate, Value: &macro.Value{Expr: "https://example.com"}},
			{ID: "s2", Action: macro.ActionFill, Target: loc(macro.LocLabel, "Họ tên", ""), Value: &macro.Value{Expr: "Anh {{ho_ten}}", Transforms: []string{"trim"}}},
			{ID: "s3", Action: macro.ActionFill, Target: loc(macro.LocRole, "textbox", "Email"), Value: &macro.Value{Expr: "{{email}}"}},
			{ID: "s4", Action: macro.ActionFill, Target: loc(macro.LocLabel, "Confirm email", ""), Value: &macro.Value{Expr: "{{email}}"}},
			{ID: "s5", Action: macro.ActionFill, Target: loc(macro.LocCSS, "#pw", ""), Value: &macro.Value{Expr: "{{password}}"}},
		},
	}
}

func TestWriteTemplateXLSX(t *testing.T) {
	m := templateMacro()
	if err := macro.Validate(m); err != nil {
		t.Fatalf("test macro: %v", err)
	}
	path := filepath.Join(t.TempDir(), "template.xlsx")
	if err := WriteTemplate(m, path, "en"); err != nil {
		t.Fatal(err)
	}
	// The existing reader sees the Data sheet first: header in macro order, no rows.
	src, err := NewXLSX(path, XLSXOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ctx := context.Background()
	cols, err := src.Columns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ho_ten", "email", "password"}
	if !reflect.DeepEqual(cols, want) {
		t.Fatalf("header = %v, want %v", cols, want)
	}
	if n, err := src.Count(ctx); err != nil || n != 0 {
		t.Errorf("rows = %d, %v; want 0", n, err)
	}
	if src.Sheet() != "Data" {
		t.Errorf("first sheet = %q, want Data", src.Sheet())
	}
	// The mapping code binds every macro variable by exact name.
	rep := Mapping{Vars: want}.Resolve(cols)
	if !rep.OK() || len(rep.Extra) != 0 {
		t.Errorf("mapping not automatic: %+v", rep)
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := f.GetSheetList(); !reflect.DeepEqual(got, []string{"Data", "Guide"}) {
		t.Fatalf("sheets = %v", got)
	}
	rows, err := f.GetRows("Guide")
	if err != nil {
		t.Fatal(err)
	}
	wantGuide := [][]string{
		{"Column", "Field type", "Page label", "Transform", "Steps"},
		{"ho_ten", "text", "Họ tên", "trim", "1"},
		{"email", "email", "Email", "", "2"},
		{"password", "password (protected)", "#pw", "", "1"},
	}
	if !reflect.DeepEqual(rows, wantGuide) {
		t.Errorf("guide = %v\nwant    %v", rows, wantGuide)
	}
	panes, err := f.GetPanes("Data")
	if err != nil || !panes.Freeze || panes.YSplit != 1 {
		t.Errorf("row 1 is not frozen: %+v %v", panes, err)
	}
	styleID, _ := f.GetCellStyle("Data", "A1")
	st, _ := f.GetStyle(styleID)
	if st == nil || st.Font == nil || !st.Font.Bold {
		t.Errorf("header is not bold: %+v", st)
	}
	if w, _ := f.GetColWidth("Data", "A"); w < 14 {
		t.Errorf("column width %v is too small", w)
	}
}

func TestWriteTemplateCSVAndVietnamese(t *testing.T) {
	m := templateMacro()
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "t.csv")
	if err := WriteTemplate(m, csvPath, "vi"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(csvPath)
	if strings.TrimSpace(string(raw)) != "ho_ten,email,password" {
		t.Errorf("csv = %q", raw)
	}
	src, err := NewCSV(csvPath, CSVOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if cols, _ := src.Columns(context.Background()); !reflect.DeepEqual(cols, []string{"ho_ten", "email", "password"}) {
		t.Errorf("csv columns = %v", cols)
	}
	xl := filepath.Join(dir, "vi.xlsx")
	if err := WriteTemplate(m, xl, "vi"); err != nil {
		t.Fatal(err)
	}
	f, _ := excelize.OpenFile(xl)
	defer f.Close()
	if v, _ := f.GetCellValue("Guide", "A1"); v != "Cột" {
		t.Errorf("Vietnamese guide header = %q", v)
	}
	if v, _ := f.GetCellValue("Guide", "B4"); v != "mật khẩu (được bảo vệ)" {
		t.Errorf("Vietnamese password kind = %q", v)
	}
}

func TestWriteTemplateNeedsColumns(t *testing.T) {
	m := &macro.Macro{SchemaVersion: macro.CurrentVersion, ID: "t", Name: "t", Steps: []macro.Step{{ID: "s1", Action: macro.ActionNavigate, Value: &macro.Value{Expr: "https://example.com"}}}, AllowedOrigins: []string{"https://example.com"}}
	if err := WriteTemplate(m, filepath.Join(t.TempDir(), "x.xlsx"), "en"); err == nil {
		t.Error("a macro without data columns must not give an empty template")
	}
}

func TestTemplateColumnsSkipDisabledSteps(t *testing.T) {
	m := templateMacro()
	m.Steps[1].Disabled = true // the only step that reads ho_ten
	for _, c := range TemplateColumns(m) {
		if c.Name == "ho_ten" {
			t.Errorf("template lists %q, read only by a disabled step", c.Name)
		}
	}
	if got := len(TemplateColumns(m)); got != 2 {
		t.Errorf("columns = %d, want 2", got)
	}
}
