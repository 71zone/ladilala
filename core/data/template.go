package data

import (
	"encoding/csv"
	"fmt"
	"os"
	"strings"

	"github.com/8bu/ladilala/core/macro"
	"github.com/xuri/excelize/v2"
)

// TemplateColumn is one column of a data template: a variable a step reads.
type TemplateColumn struct {
	Name string
	Kind macro.VarKind
	// Label is the page label of the first field that uses the column.
	Label string
	// Transform lists the transforms the steps apply, joined by ", ".
	Transform string
	// Steps is how many steps read the column.
	Steps int
}

// TemplateColumns lists the variables the steps read, in order of first use.
// The names are the headers of the template, so a source filled from it maps
// to the macro by exact name.
func TemplateColumns(m *macro.Macro) []TemplateColumn {
	kinds := map[string]macro.VarKind{}
	for _, v := range m.Variables {
		kinds[v.Name] = v.Kind
	}
	var out []TemplateColumn
	at := map[string]int{}
	for _, s := range m.Steps {
		if s.Value == nil || s.Disabled {
			continue
		}
		parts, err := macro.ParseExpr(s.Value.Expr)
		if err != nil {
			continue
		}
		for _, name := range macro.Refs(parts) {
			i, ok := at[name]
			if !ok {
				i = len(out)
				at[name] = i
				out = append(out, TemplateColumn{Name: name, Kind: kinds[name], Label: stepLabel(s)})
			}
			out[i].Steps++
			for _, t := range s.Value.Transforms {
				if !strings.Contains(", "+out[i].Transform+", ", ", "+t+", ") {
					if out[i].Transform != "" {
						out[i].Transform += ", "
					}
					out[i].Transform += t
				}
			}
		}
	}
	return out
}

// stepLabel is the text a person sees for the step's field: a label or a role
// name when there is one, else the first locator value.
func stepLabel(s macro.Step) string {
	if s.Target == nil || len(s.Target.Locators) == 0 {
		return ""
	}
	for _, want := range []macro.LocatorKind{macro.LocLabel, macro.LocRole, macro.LocText} {
		for _, l := range s.Target.Locators {
			if l.Kind != want {
				continue
			}
			if l.Kind == macro.LocRole && l.Name != "" {
				return l.Name
			}
			if l.Kind != macro.LocRole {
				return l.Value
			}
		}
	}
	return s.Target.Locators[0].Value
}

var guideText = map[string]struct {
	Head  [5]string
	Kinds map[macro.VarKind]string
}{
	"en": {
		Head:  [5]string{"Column", "Field type", "Page label", "Transform", "Steps"},
		Kinds: map[macro.VarKind]string{macro.KindText: "text", macro.KindEmail: "email", macro.KindPhone: "phone", macro.KindNumber: "number", macro.KindURL: "url", macro.KindDate: "date", macro.KindPassword: "password (protected)"},
	},
	"vi": {
		Head:  [5]string{"Cột", "Loại ô", "Nhãn trên trang", "Biến đổi", "Số bước"},
		Kinds: map[macro.VarKind]string{macro.KindText: "văn bản", macro.KindEmail: "email", macro.KindPhone: "điện thoại", macro.KindNumber: "số", macro.KindURL: "url", macro.KindDate: "ngày", macro.KindPassword: "mật khẩu (được bảo vệ)"},
	},
}

// WriteTemplate writes an empty data file for the macro. The format follows the
// extension of path: ".csv" gives one header row; anything else gives an Excel
// file with the sheet "Data" (row 1 = the columns, bold and frozen, row 2 empty)
// and the sheet "Guide" (each column, its field type, page label and transform).
// lang is "vi" or "en" and only changes the text of the Guide sheet.
func WriteTemplate(m *macro.Macro, path, lang string) error {
	cols := TemplateColumns(m)
	if len(cols) == 0 {
		return fmt.Errorf("the macro has no data columns")
	}
	if strings.EqualFold(pathExt(path), ".csv") {
		return writeCSVTemplate(cols, path)
	}
	if lang != "vi" {
		lang = "en"
	}
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", "Data"); err != nil {
		return err
	}
	bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return err
	}
	for i, c := range cols {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue("Data", cell, c.Name); err != nil {
			return err
		}
		col, _ := excelize.ColumnNumberToName(i + 1)
		if err := f.SetColWidth("Data", col, col, float64(max(len([]rune(c.Name))+4, 14))); err != nil {
			return err
		}
	}
	last, _ := excelize.CoordinatesToCellName(len(cols), 1)
	if err := f.SetCellStyle("Data", "A1", last, bold); err != nil {
		return err
	}
	if err := f.SetPanes("Data", &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return err
	}
	if _, err := f.NewSheet("Guide"); err != nil {
		return err
	}
	g := guideText[lang]
	for i, h := range g.Head {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue("Guide", cell, h)
	}
	_ = f.SetCellStyle("Guide", "A1", "E1", bold)
	for r, c := range cols {
		kind := g.Kinds[c.Kind]
		if kind == "" {
			kind = g.Kinds[macro.KindText]
		}
		for i, v := range []any{c.Name, kind, c.Label, c.Transform, c.Steps} {
			cell, _ := excelize.CoordinatesToCellName(i+1, r+2)
			if err := f.SetCellValue("Guide", cell, v); err != nil {
				return err
			}
		}
	}
	for i, w := range []float64{22, 24, 30, 18, 10} {
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetColWidth("Guide", col, col, w)
	}
	f.SetActiveSheet(0)
	return f.SaveAs(path)
}

func pathExt(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 && !strings.ContainsAny(p[i:], `/\`) {
		return p[i:]
	}
	return ""
}

func writeCSVTemplate(cols []TemplateColumn, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	if err := w.Write(names); err != nil {
		f.Close()
		return err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
