package data

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/xuri/excelize/v2"
)

// XLSXOptions picks the sheet. Sheet wins over SheetIndex. SheetIndex is 0-based.
// DateFormat says how date-only cells are read.
type DateFormat string

const (
	// DateISO reads date-only cells as yyyy-mm-dd, whatever the Excel format is.
	// A web date input needs this form. It is the default.
	DateISO DateFormat = "iso"
	// DateDisplay reads date-only cells as shown in Excel, for example 10/03/2026.
	DateDisplay DateFormat = "display"
)

// XLSXOptions picks the sheet and the date mode.
// DateFormat applies to date-only cells. Date-time cells read as
// yyyy-mm-ddThh:mm and time-only cells as hh:mm in both modes.
type XLSXOptions struct {
	Sheet      string
	SheetIndex int
	DateFormat DateFormat
}

// FormulaWarning reports a formula cell that has no cached value and that the
// calculator could not evaluate. The cell reads as empty.
type FormulaWarning struct {
	Row     int // 0-based data row index
	Column  string
	Cell    string // for example "C4"
	Formula string
	Err     error
}

func (w FormulaWarning) String() string {
	return fmt.Sprintf("row %d, cell %s: formula %q has no cached value and cannot be calculated: %v", w.Row+1, w.Cell, w.Formula, w.Err)
}

// XLSXSource reads one sheet of an Excel file. Row 1 is the header.
// Cells show the text the user sees in Excel. Formulas give cached values.
type XLSXSource struct {
	f        *excelize.File
	sheet    string
	dateFmt  DateFormat
	date1904 bool

	styleMu  sync.Mutex
	styleKnd map[int]cellKind
	warnMu   sync.Mutex
	warnings []FormulaWarning

	mu      sync.Mutex
	header  []string
	count   int
	counted bool
	closed  bool
}

// NewXLSX opens an xlsx file and selects a sheet.
func NewXLSX(path string, opts XLSXOptions) (*XLSXSource, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open Excel file: %w", err)
	}
	names := f.GetSheetList()
	sheet := opts.Sheet
	switch {
	case sheet != "":
		found := false
		for _, n := range names {
			if n == sheet {
				found = true
			}
		}
		if !found {
			f.Close()
			return nil, fmt.Errorf("sheet %q not found; sheets are: %s", sheet, strings.Join(names, ", "))
		}
	case opts.SheetIndex >= 0 && opts.SheetIndex < len(names):
		sheet = names[opts.SheetIndex]
	default:
		f.Close()
		return nil, fmt.Errorf("sheet index %d is out of range; the file has %d sheets", opts.SheetIndex, len(names))
	}
	if opts.DateFormat != "" && opts.DateFormat != DateISO && opts.DateFormat != DateDisplay {
		f.Close()
		return nil, fmt.Errorf("unknown date format %q; use %q or %q", opts.DateFormat, DateISO, DateDisplay)
	}
	s := &XLSXSource{f: f, sheet: sheet, dateFmt: opts.DateFormat, styleKnd: map[int]cellKind{}}
	if s.dateFmt == "" {
		s.dateFmt = DateISO
	}
	if props, err := f.GetWorkbookProps(); err == nil && props.Date1904 != nil {
		s.date1904 = *props.Date1904
	}
	return s, nil
}

// Warnings returns the formula warnings seen so far while reading rows.
func (s *XLSXSource) Warnings() []FormulaWarning {
	s.warnMu.Lock()
	defer s.warnMu.Unlock()
	return append([]FormulaWarning(nil), s.warnings...)
}

// Sheet returns the selected sheet name.
func (s *XLSXSource) Sheet() string { return s.sheet }

// Sheets lists the sheet names of the file in workbook order.
func (s *XLSXSource) Sheets() []string { return s.f.GetSheetList() }

type xlsxIter struct {
	src    *XLSXSource
	rows   *excelize.Rows
	header []string
	index  int
	rowNum int // 1-based sheet row of the current row
}

func (s *XLSXSource) openIter() (*xlsxIter, error) {
	rows, err := s.f.Rows(s.sheet)
	if err != nil {
		return nil, fmt.Errorf("cannot read sheet %q: %w", s.sheet, err)
	}
	return &xlsxIter{src: s, rows: rows}, nil
}

func trimTrailingEmpty(cells []string) []string {
	n := len(cells)
	for n > 0 && cells[n-1] == "" {
		n--
	}
	return cells[:n]
}

// nextCells returns the next row that has at least one value.
func (it *xlsxIter) nextCells() ([]string, bool, error) {
	for it.rows.Next() {
		it.rowNum++
		cells, err := it.rows.Columns()
		if err != nil {
			return nil, false, fmt.Errorf("cannot read Excel row: %w", err)
		}
		it.src.fixCells(it, cells)
		cells = trimTrailingEmpty(cells)
		if len(cells) == 0 {
			continue
		}
		return cells, true, nil
	}
	return nil, false, it.rows.Error()
}

func (it *xlsxIter) readHeader() ([]string, error) {
	cells, ok, err := it.nextCells()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrEmpty
	}
	h := make([]string, len(cells))
	for i, c := range cells {
		h[i] = strings.TrimSpace(c)
	}
	return h, nil
}

func (it *xlsxIter) next(ctx context.Context) (int, Row, bool, error) {
	cells, ok, err := it.nextCells()
	if err != nil {
		return 0, nil, false, err
	}
	if !ok {
		return 0, nil, false, nil
	}
	idx := it.index
	it.index++
	if len(cells) > len(it.header) {
		return idx, nil, false, &RaggedRowError{Index: idx, Want: len(it.header), Got: len(cells)}
	}
	return idx, buildRow(it.header, cells), true, nil
}

func (it *xlsxIter) close() error { return it.rows.Close() }

func (s *XLSXSource) checkOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return nil
}

// Columns returns the header names.
func (s *XLSXSource) Columns(ctx context.Context) ([]string, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.header != nil {
		return append([]string(nil), s.header...), nil
	}
	it, err := s.openIter()
	if err != nil {
		return nil, err
	}
	defer it.close()
	h, err := it.readHeader()
	if err != nil {
		return nil, err
	}
	s.header = h
	return append([]string(nil), h...), nil
}

// Count counts data rows. Fully empty rows are skipped.
func (s *XLSXSource) Count(ctx context.Context) (int, error) {
	if err := s.checkOpen(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	if s.counted {
		defer s.mu.Unlock()
		return s.count, nil
	}
	s.mu.Unlock()
	it, err := s.openIter()
	if err != nil {
		return 0, err
	}
	defer it.close()
	if _, err := it.readHeader(); err != nil {
		return 0, err
	}
	n := 0
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		_, ok, err := it.nextCells()
		if err != nil {
			return 0, err
		}
		if !ok {
			break
		}
		n++
	}
	s.mu.Lock()
	s.count, s.counted = n, true
	s.mu.Unlock()
	return n, nil
}

// Open returns a cursor over rows [from,to).
func (s *XLSXSource) Open(ctx context.Context, from, to int) (Rows, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	it, err := s.openIter()
	if err != nil {
		return nil, err
	}
	h, err := it.readHeader()
	if err != nil {
		it.close()
		return nil, err
	}
	it.header = h
	return newRange(it, from, to), nil
}

// Close releases the file.
func (s *XLSXSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.f.Close()
}
