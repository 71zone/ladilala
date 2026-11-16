package recorder

import (
	"fmt"
	"sort"

	"github.com/8bu/ladilala/core/macro"
)

// ColumnUnusedError says a column maps to a literal no fill or select step holds.
type ColumnUnusedError struct {
	Column, Literal string
}

func (e *ColumnUnusedError) Error() string {
	return fmt.Sprintf("recorder: no fill or select step has the recorded value %q (column %q)", e.Literal, e.Column)
}

// Parameterize replaces recorded literals with {{column}} references. columns
// maps a column name to the literal that was recorded (for example first_name
// to "Anna"). Every fill and select step whose value equals the literal is
// rewritten and the column is declared as a required string variable. It
// returns the number of steps rewritten. A column that matches no step is a
// *ColumnUnusedError and the macro is left unchanged.
func Parameterize(m *macro.Macro, columns map[string]string) (int, error) {
	names := make([]string, 0, len(columns))
	for c := range columns {
		names = append(names, c)
	}
	sort.Strings(names)
	hits := map[string][]int{}
	for _, c := range names {
		for i, s := range m.Steps {
			if (s.Action == macro.ActionFill || s.Action == macro.ActionSelect) && s.Value != nil && s.Value.Expr == columns[c] {
				hits[c] = append(hits[c], i)
			}
		}
		if len(hits[c]) == 0 {
			return 0, &ColumnUnusedError{Column: c, Literal: columns[c]}
		}
	}
	n := 0
	for _, c := range names {
		for _, i := range hits[c] {
			m.Steps[i].Value = &macro.Value{Expr: "{{" + c + "}}", Transforms: m.Steps[i].Value.Transforms}
			n++
		}
		declared := false
		for _, v := range m.Variables {
			declared = declared || v.Name == c
		}
		if !declared {
			m.Variables = append(m.Variables, macro.VariableDecl{Name: c, Type: macro.VarString, Required: true})
		}
	}
	return n, nil
}
