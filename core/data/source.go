// Package data reads rows of text from files and services so the run engine
// can fill forms with them.
package data

import (
	"context"
	"errors"
	"fmt"
)

// Row is one data row. Keys are header names, values are the text of the cell.
type Row map[string]string

// Source is a table of rows.
type Source interface {
	Columns(ctx context.Context) ([]string, error)        // header names in order
	Count(ctx context.Context) (int, error)               // total data rows, -1 if unknown
	Open(ctx context.Context, from, to int) (Rows, error) // 0-based row range [from,to), to<0 means to the end
	Close() error
}

// Rows is a forward-only cursor over a Source.
type Rows interface {
	Next(ctx context.Context) (index int, row Row, ok bool, err error) // index = 0-based row number in the source
	Close() error
}

// RowError is an error that belongs to one row. After a RowError, Next may be
// called again and moves to the next row. Any other error ends the cursor.
type RowError interface {
	error
	RowIndex() int
}

// RaggedRowError reports a row with a different number of cells than the header.
type RaggedRowError struct {
	Index int
	Want  int
	Got   int
}

func (e *RaggedRowError) Error() string {
	return fmt.Sprintf("row %d has %d cells but the header has %d", e.Index+1, e.Got, e.Want)
}

// RowIndex returns the 0-based row number.
func (e *RaggedRowError) RowIndex() int { return e.Index }

// NestedValueError reports a JSON value that is an object or an array.
type NestedValueError struct {
	Index  int
	Column string
}

func (e *NestedValueError) Error() string {
	return fmt.Sprintf("row %d, column %q holds an object or an array; flat values only", e.Index+1, e.Column)
}

// RowIndex returns the 0-based row number.
func (e *NestedValueError) RowIndex() int { return e.Index }

// RowValidationError reports a row that failed the required-column check.
type RowValidationError struct {
	Index    int
	Variable string
	Column   string
}

func (e *RowValidationError) Error() string {
	return fmt.Sprintf("row %d: column %q for variable %q is empty", e.Index+1, e.Column, e.Variable)
}

// RowIndex returns the 0-based row number.
func (e *RowValidationError) RowIndex() int { return e.Index }

// NotObjectError reports a JSON row that is not an object.
type NotObjectError struct{ Index int }

func (e *NotObjectError) Error() string {
	return fmt.Sprintf("row %d is not a JSON object", e.Index+1)
}

// RowIndex returns the 0-based row number.
func (e *NotObjectError) RowIndex() int { return e.Index }

// ErrEmpty means the source has no header row.
var ErrEmpty = errors.New("the source is empty: no header row")

// ErrClosed means the source was closed.
var ErrClosed = errors.New("the source is closed")

// isRowError reports whether err is a per-row error.
func isRowError(err error) bool {
	var re RowError
	return errors.As(err, &re)
}

// iter is the internal cursor. It yields every row from index 0 and reports
// per-row errors with RowError, so a range wrapper can skip them.
type iter interface {
	next(ctx context.Context) (index int, row Row, ok bool, err error)
	close() error
}

// rangeRows cuts an iter to [from,to).
type rangeRows struct {
	in       iter
	from, to int
	done     bool
}

func newRange(in iter, from, to int) Rows {
	if from < 0 {
		from = 0
	}
	return &rangeRows{in: in, from: from, to: to}
}

func (r *rangeRows) Next(ctx context.Context) (int, Row, bool, error) {
	if r.done {
		return 0, nil, false, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, nil, false, err
		}
		idx, row, ok, err := r.in.next(ctx)
		if err != nil && !isRowError(err) {
			r.done = true
			return 0, nil, false, err
		}
		if err == nil && !ok {
			r.done = true
			return 0, nil, false, nil
		}
		if idx < r.from {
			continue
		}
		if r.to >= 0 && idx >= r.to {
			r.done = true
			return 0, nil, false, nil
		}
		if err != nil {
			return idx, nil, false, err
		}
		return idx, row, true, nil
	}
}

func (r *rangeRows) Close() error {
	r.done = true
	return r.in.close()
}

// buildRow maps a record to a Row. With duplicate header names the first wins.
func buildRow(header, rec []string) Row {
	row := make(Row, len(header))
	for i, h := range header {
		if _, dup := row[h]; dup {
			continue
		}
		if i < len(rec) {
			row[h] = rec[i]
		} else {
			row[h] = ""
		}
	}
	return row
}
