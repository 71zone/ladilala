package main

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/8bu/ladilala/core/data"
	"github.com/8bu/ladilala/core/macro"
)

func templateTestMacro(name string) macro.Macro {
	return macro.Macro{
		SchemaVersion: 1, Name: name, AllowedOrigins: []string{"https://shop.example"},
		Variables: []macro.VariableDecl{
			{Name: "ho_ten", Type: macro.VarString, Kind: macro.KindText, Required: true},
			{Name: "email", Type: macro.VarString, Kind: macro.KindEmail, Required: true},
		},
		Steps: []macro.Step{
			{ID: "open", Action: macro.ActionNavigate, Value: &macro.Value{Expr: "https://shop.example/signup"}},
			fillStep("a", "#name", "{{ho_ten}}"),
			fillStep("b", "#mail", "{{email}}"),
		},
	}
}

func TestExportTemplateOfSavedAndDraftMacro(t *testing.T) {
	e := newSvcmEnv(t)
	ctx := context.Background()
	saved := e.save(t, templateTestMacro("signup"))
	dir := t.TempDir()

	res, err := e.data.ExportTemplate(ctx, saved.Macro.ID, filepath.Join(dir, "saved.xlsx"), "vi")
	if err != nil {
		t.Fatal(err)
	}
	if res.Columns != 2 {
		t.Errorf("columns = %d, want 2", res.Columns)
	}
	src, err := data.NewXLSX(res.Path, data.XLSXOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	cols, _ := src.Columns(ctx)
	if !reflect.DeepEqual(cols, []string{"ho_ten", "email"}) {
		t.Errorf("header = %v", cols)
	}

	// A draft that is not saved gives the same columns, and a path without an extension becomes .xlsx.
	m := templateTestMacro("draft")
	res, err = e.data.ExportDraftTemplate(ctx, m, filepath.Join(dir, "draft"), "en")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(res.Path) != ".xlsx" {
		t.Errorf("path = %s", res.Path)
	}
	csv, err := e.data.ExportDraftTemplate(ctx, m, filepath.Join(dir, "draft.csv"), "en")
	if err != nil || csv.Columns != 2 {
		t.Fatalf("csv: %+v %v", csv, err)
	}

	// Linked later, the filled file maps by exact name, without any manual step.
	rep := data.Mapping{Vars: []string{"ho_ten", "email"}}.Resolve(cols)
	if !rep.OK() || len(rep.Extra) != 0 {
		t.Errorf("mapping = %+v", rep)
	}
}

func TestExportTemplateErrors(t *testing.T) {
	e := newSvcmEnv(t)
	ctx := context.Background()
	if _, err := e.data.ExportTemplate(ctx, "nope", filepath.Join(t.TempDir(), "x.xlsx"), "en"); apiCode(t, err) != "notFound" {
		t.Errorf("unknown macro: %v", err)
	}
	m := templateTestMacro("empty")
	m.Steps = m.Steps[:1]
	m.Variables = nil
	if _, err := e.data.ExportDraftTemplate(ctx, m, filepath.Join(t.TempDir(), "x.xlsx"), "en"); apiCode(t, err) != "invalid" {
		t.Errorf("no columns: %v", err)
	}
	if _, err := e.data.ExportDraftTemplate(ctx, templateTestMacro("x"), " ", "en"); apiCode(t, err) != "invalid" {
		t.Errorf("empty path: %v", err)
	}
}
