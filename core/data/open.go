package data

import (
	"fmt"
	"path/filepath"
	"strings"
)

// OpenFile opens a data file by its extension: .csv, .tsv, .txt, .json, .xlsx.
func OpenFile(path string) (Source, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv", ".tsv", ".txt":
		return NewCSV(path, CSVOptions{})
	case ".json":
		return NewJSON(path, JSONOptions{}), nil
	case ".xlsx", ".xlsm":
		return NewXLSX(path, XLSXOptions{})
	}
	return nil, fmt.Errorf("unsupported file type %q; use csv, json or xlsx", filepath.Ext(path))
}
