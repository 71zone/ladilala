package player

import (
	"fmt"
	"strconv"

	"github.com/8bu/ladilala/core/macro"
)

// BuildVars returns the variable values of one run: declared defaults, then
// the data row on top. A required variable with neither is a
// *MissingColumnError.
func BuildVars(m *macro.Macro, row map[string]string) (map[string]string, error) {
	vars := map[string]string{}
	for _, d := range m.Variables {
		if d.Default != nil {
			vars[d.Name] = stringify(d.Default)
		}
	}
	for k, v := range row {
		vars[k] = v
	}
	onlyDisabled := disabledOnlyVars(m)
	for _, d := range m.Variables {
		if _, ok := vars[d.Name]; !ok && d.Required && !onlyDisabled[d.Name] {
			return nil, &MissingColumnError{Column: d.Name}
		}
	}
	return vars, nil
}

// disabledOnlyVars names the variables that a disabled step reads and no
// enabled step reads. The player skips disabled steps, so such a variable
// needs no value even when it is declared required.
func disabledOnlyVars(m *macro.Macro) map[string]bool {
	refs := func(s *macro.Step) []string {
		var out []string
		vals := []*macro.Value{s.Value}
		for _, e := range s.Expect {
			vals = append(vals, e.Value)
		}
		for _, v := range vals {
			if v == nil {
				continue
			}
			if parts, err := macro.ParseExpr(v.Expr); err == nil {
				out = append(out, macro.Refs(parts)...)
			}
		}
		return out
	}
	used, off := map[string]bool{}, map[string]bool{}
	for i := range m.Steps {
		for _, n := range refs(&m.Steps[i]) {
			if m.Steps[i].Disabled {
				off[n] = true
			} else {
				used[n] = true
			}
		}
	}
	for n := range used {
		delete(off, n)
	}
	return off
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return fmt.Sprint(v)
}

// evalExpr renders one expression for a step; a reference to a variable with
// no value is a *MissingColumnError.
func evalExpr(stepID string, v macro.Value, vars map[string]string) (string, error) {
	parts, err := macro.ParseExpr(v.Expr)
	if err != nil {
		return "", err
	}
	for _, name := range macro.Refs(parts) {
		if _, ok := vars[name]; !ok {
			return "", &MissingColumnError{Column: name, Step: stepID}
		}
	}
	return v.Eval(vars)
}

// Preflight checks a run before any browser action: every variable has a
// value, every value expression renders, and every navigate URL is inside
// allowedOrigins. It returns the variables Run will use.
func Preflight(m *macro.Macro, row map[string]string) (map[string]string, error) {
	vars, err := BuildVars(m, row)
	if err != nil {
		return nil, err
	}
	for _, s := range m.Steps {
		if s.Value == nil || s.Disabled {
			continue
		}
		out, err := evalExpr(s.ID, *s.Value, vars)
		if err != nil {
			return nil, &StepError{StepID: s.ID, Action: s.Action, Err: err}
		}
		if s.Action == macro.ActionNavigate && !m.AllowsURL(out) {
			return nil, &StepError{StepID: s.ID, Action: s.Action, Err: &OriginError{URL: out, Step: s.ID}}
		}
	}
	return vars, nil
}
