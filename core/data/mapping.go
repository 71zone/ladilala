package data

import (
	"context"
	"strings"
)

// Mapping links macro variables to source columns.
// A variable matches a column by name, ignoring case and outer spaces.
// Overrides give the column name to use for a variable. Required lists the
// variables that must not be empty in any row.
type Mapping struct {
	Vars      []string
	Overrides map[string]string // variable -> column name
	Required  []string
}

// MappingReport is the result of matching a Mapping to real columns.
type MappingReport struct {
	Bound      map[string]string // variable -> column name as written in the source
	Missing    []string          // variables with no column
	Extra      []string          // columns that no variable uses
	Duplicates []string          // column names that appear more than once, ignoring case
}

// OK reports whether every variable has a column and no header is duplicated.
func (r *MappingReport) OK() bool {
	return len(r.Missing) == 0 && len(r.Duplicates) == 0
}

func normName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Resolve matches the variables to the given columns.
func (m Mapping) Resolve(columns []string) *MappingReport {
	rep := &MappingReport{Bound: map[string]string{}}
	first := map[string]string{}
	seen := map[string]int{}
	for _, c := range columns {
		n := normName(c)
		seen[n]++
		if _, ok := first[n]; !ok {
			first[n] = c
		}
		if seen[n] == 2 {
			rep.Duplicates = append(rep.Duplicates, c)
		}
	}
	used := map[string]bool{}
	for _, v := range m.Vars {
		name := v
		if o, ok := m.Overrides[v]; ok {
			name = o
		}
		if col, ok := first[normName(name)]; ok {
			rep.Bound[v] = col
			used[normName(col)] = true
		} else {
			rep.Missing = append(rep.Missing, v)
		}
	}
	for _, c := range columns {
		n := normName(c)
		if !used[n] {
			rep.Extra = append(rep.Extra, c)
			used[n] = true
		}
	}
	return rep
}

// ResolveSource reads the columns of a source and matches them.
func (m Mapping) ResolveSource(ctx context.Context, src Source) (*MappingReport, error) {
	cols, err := src.Columns(ctx)
	if err != nil {
		return nil, err
	}
	return m.Resolve(cols), nil
}

// Apply turns a source row into a map from variable to value.
// Missing variables get an empty value.
func (r *MappingReport) Apply(row Row) map[string]string {
	out := make(map[string]string, len(r.Bound))
	for v, col := range r.Bound {
		out[v] = row[col]
	}
	return out
}

// RowValidator checks one row. It returns a RowError for a bad row.
type RowValidator func(index int, row Row) error

// RequiredValidator fails when a required variable is empty or has no column.
func (m Mapping) RequiredValidator(rep *MappingReport) RowValidator {
	return func(index int, row Row) error {
		for _, v := range m.Required {
			col, ok := rep.Bound[v]
			if !ok {
				return &RowValidationError{Index: index, Variable: v, Column: ""}
			}
			if strings.TrimSpace(row[col]) == "" {
				return &RowValidationError{Index: index, Variable: v, Column: col}
			}
		}
		return nil
	}
}

type validatedRows struct {
	in Rows
	v  RowValidator
}

// Validated wraps a cursor. Rows that fail v come back as row errors.
func Validated(in Rows, v RowValidator) Rows { return &validatedRows{in: in, v: v} }

func (r *validatedRows) Next(ctx context.Context) (int, Row, bool, error) {
	idx, row, ok, err := r.in.Next(ctx)
	if err != nil || !ok {
		return idx, row, ok, err
	}
	if verr := r.v(idx, row); verr != nil {
		return idx, nil, false, verr
	}
	return idx, row, true, nil
}

func (r *validatedRows) Close() error { return r.in.Close() }
