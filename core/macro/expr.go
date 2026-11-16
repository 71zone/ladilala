package macro

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Transform names. The set is closed so macros stay portable and the player
// never executes user-supplied code.
const (
	TransformTrim      = "trim"
	TransformUpper     = "upper"
	TransformLower     = "lower"
	TransformDigits    = "digits"    // keep only 0-9
	TransformURLEncode = "urlencode" // query-escape
	TransformDate      = "date"      // date:<layout>, tokens YYYY YY MM DD HH mm ss
)

// Transform is a parsed "name" or "name:arg" transform.
type Transform struct {
	Name string
	Arg  string
}

// Part is one segment of a parsed expression: either literal text (Var == "")
// or a variable reference with its inline transforms.
type Part struct {
	Literal    string
	Var        string
	Transforms []Transform
}

// ParseTransform parses "trim", "date:YYYY-MM-DD", etc. and rejects unknown
// names and wrong arguments.
func ParseTransform(s string) (Transform, error) {
	name, arg, hasArg := strings.Cut(strings.TrimSpace(s), ":")
	t := Transform{Name: name, Arg: arg}
	switch name {
	case TransformTrim, TransformUpper, TransformLower, TransformDigits, TransformURLEncode:
		if hasArg {
			return t, fmt.Errorf("transform %q takes no argument", name)
		}
	case TransformDate:
		if !hasArg || len(dateTokens(arg)) == 0 {
			return t, fmt.Errorf("transform %q needs a layout with at least one of YYYY, YY, MM, DD, HH, mm, ss", name)
		}
	case "":
		return t, fmt.Errorf("empty transform")
	default:
		return t, fmt.Errorf("unknown transform %q", name)
	}
	return t, nil
}

// EscapeLiteral returns s as an expression that renders back to exactly s:
// every "{{" gets a backslash, which ParseExpr reads as the escape "\{{".
func EscapeLiteral(s string) string { return strings.ReplaceAll(s, "{{", `\{{`) }

// ParseExpr splits s into literal and {{variable|transform}} parts. A
// backslash directly before "{{" escapes it: `\{{` is the literal text "{{"
// (the backslash is dropped). Any other backslash is literal.
func ParseExpr(s string) ([]Part, error) {
	var parts []Part
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, Part{Literal: lit.String()})
			lit.Reset()
		}
	}
	for s != "" {
		open := strings.Index(s, "{{")
		if open < 0 {
			lit.WriteString(s)
			break
		}
		if open > 0 && s[open-1] == '\\' {
			lit.WriteString(s[:open-1])
			lit.WriteString("{{")
			s = s[open+2:]
			continue
		}
		lit.WriteString(s[:open])
		flush()
		s = s[open+2:]
		end := strings.Index(s, "}}")
		if end < 0 {
			return nil, fmt.Errorf("unterminated {{ in expression")
		}
		body := s[:end]
		if strings.Contains(body, "{{") {
			return nil, fmt.Errorf("nested {{ in expression")
		}
		s = s[end+2:]

		fields := strings.Split(body, "|")
		name := strings.TrimSpace(fields[0])
		if !validName(name) {
			return nil, fmt.Errorf("invalid variable name %q in {{%s}}", name, body)
		}
		p := Part{Var: name}
		for _, f := range fields[1:] {
			t, err := ParseTransform(f)
			if err != nil {
				return nil, err
			}
			p.Transforms = append(p.Transforms, t)
		}
		parts = append(parts, p)
	}
	flush()
	return parts, nil
}

// Refs returns the variable names referenced by parts, in order of appearance.
func Refs(parts []Part) []string {
	var names []string
	for _, p := range parts {
		if p.Var != "" {
			names = append(names, p.Var)
		}
	}
	return names
}

// Eval renders the value: substitutes variables (inline transforms first),
// then applies the value-level Transforms to the whole result. A reference to
// a variable missing from vars is an error; vars hold already-stringified
// values (dates as ISO-8601).
func (v Value) Eval(vars map[string]string) (string, error) {
	parts, err := ParseExpr(v.Expr)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Var == "" {
			b.WriteString(p.Literal)
			continue
		}
		val, ok := vars[p.Var]
		if !ok {
			return "", fmt.Errorf("variable %q has no value", p.Var)
		}
		if val, err = applyAll(val, p.Transforms); err != nil {
			return "", fmt.Errorf("variable %q: %w", p.Var, err)
		}
		b.WriteString(val)
	}
	out := b.String()
	for _, raw := range v.Transforms {
		t, err := ParseTransform(raw)
		if err != nil {
			return "", err
		}
		if out, err = t.Apply(out); err != nil {
			return "", err
		}
	}
	return out, nil
}

func applyAll(s string, ts []Transform) (string, error) {
	var err error
	for _, t := range ts {
		if s, err = t.Apply(s); err != nil {
			return "", err
		}
	}
	return s, nil
}

// Apply runs the transform on s.
func (t Transform) Apply(s string) (string, error) {
	switch t.Name {
	case TransformTrim:
		return strings.TrimSpace(s), nil
	case TransformUpper:
		return strings.ToUpper(s), nil
	case TransformLower:
		return strings.ToLower(s), nil
	case TransformDigits:
		return strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, s), nil
	case TransformURLEncode:
		return url.QueryEscape(s), nil
	case TransformDate:
		d, err := parseDate(s)
		if err != nil {
			return "", err
		}
		return formatDate(d, t.Arg), nil
	}
	return "", fmt.Errorf("unknown transform %q", t.Name)
}

var dateInputLayouts = []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, l := range dateInputLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not an ISO-8601 date", s)
}

// dateTokenOrder lists tokens longest-first so YYYY wins over YY.
var dateTokenOrder = []string{"YYYY", "YY", "MM", "DD", "HH", "mm", "ss"}

// dateTokens returns the tokens found in layout, in order.
func dateTokens(layout string) []string {
	var out []string
	for i := 0; i < len(layout); {
		if tok := tokenAt(layout[i:]); tok != "" {
			out = append(out, tok)
			i += len(tok)
		} else {
			i++
		}
	}
	return out
}

func tokenAt(s string) string {
	for _, tok := range dateTokenOrder {
		if strings.HasPrefix(s, tok) {
			return tok
		}
	}
	return ""
}

// formatDate uses its own tokens rather than Go layouts so that literal
// digits/letters in a user's format cannot be misread as layout elements.
func formatDate(t time.Time, layout string) string {
	var b strings.Builder
	for i := 0; i < len(layout); {
		tok := tokenAt(layout[i:])
		if tok == "" {
			b.WriteByte(layout[i])
			i++
			continue
		}
		i += len(tok)
		switch tok {
		case "YYYY":
			fmt.Fprintf(&b, "%04d", t.Year())
		case "YY":
			fmt.Fprintf(&b, "%02d", t.Year()%100)
		case "MM":
			fmt.Fprintf(&b, "%02d", int(t.Month()))
		case "DD":
			fmt.Fprintf(&b, "%02d", t.Day())
		case "HH":
			fmt.Fprintf(&b, "%02d", t.Hour())
		case "mm":
			fmt.Fprintf(&b, "%02d", t.Minute())
		case "ss":
			fmt.Fprintf(&b, "%02d", t.Second())
		}
	}
	return b.String()
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
