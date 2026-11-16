package main

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/8bu/ladilala/core/data"
	"github.com/8bu/ladilala/core/macro"
)

// TemplateResult says what ExportTemplate wrote.
type TemplateResult struct {
	Path string `json:"path"`
	// Columns is the number of data columns in row 1.
	Columns int `json:"columns"`
}

// PickTemplateSavePath opens the native save dialog for a data template.
// format is "xlsx" or "csv". It returns an empty path when the user cancels.
func (d *DataService) PickTemplateSavePath(_ context.Context, fileName, format string) (string, error) {
	if format == "csv" {
		return pickSavePath(d.b, fileName, "CSV", "*.csv")
	}
	return pickSavePath(d.b, fileName, "Excel", "*.xlsx")
}

// ExportTemplate writes an empty data file for a saved macro: one column for
// each variable its steps read, named like the variable, so a source filled
// from it maps to the macro by exact name. The extension of savePath picks the
// format: ".csv" gives a header row; any other name gives an Excel file with
// the sheets "Data" and "Guide". lang ("vi" or "en") sets the Guide text.
func (d *DataService) ExportTemplate(ctx context.Context, macroID, savePath, lang string) (TemplateResult, error) {
	m, err := d.loadMacro(ctx, macroID)
	if err != nil {
		return TemplateResult{}, toDataAPIError(err)
	}
	return writeTemplate(m, savePath, lang)
}

// ExportDraftTemplate does the same for a macro that is not saved yet: the
// review after a recording uses it, before the user saves.
func (d *DataService) ExportDraftTemplate(_ context.Context, m macro.Macro, savePath, lang string) (TemplateResult, error) {
	return writeTemplate(&m, savePath, lang)
}

func writeTemplate(m *macro.Macro, savePath, lang string) (TemplateResult, error) {
	savePath = strings.TrimSpace(savePath)
	if savePath == "" {
		return TemplateResult{}, apiErr("invalid", "the save path is empty", "problems", []ProblemDTO{{Path: "savePath", Msg: "must not be empty"}})
	}
	if filepath.Ext(savePath) == "" {
		savePath += ".xlsx"
	}
	cols := data.TemplateColumns(m)
	if len(cols) == 0 {
		return TemplateResult{}, apiErr("invalid", "the macro has no data columns", "problems", []ProblemDTO{{Path: "macro", Msg: "no step reads a variable"}})
	}
	if err := data.WriteTemplate(m, savePath, lang); err != nil {
		return TemplateResult{}, toDataAPIError(err)
	}
	return TemplateResult{Path: savePath, Columns: len(cols)}, nil
}
