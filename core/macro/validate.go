package macro

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	maxRetries = 20
	maxTimeout = 10 * time.Minute
)

// Problem is one validation failure at a document path such as
// "steps[login].target.locators[0].kind".
type Problem struct {
	Path string
	Msg  string
}

func (p Problem) String() string { return p.Path + ": " + p.Msg }

// ValidationError carries every problem found, not just the first.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "macro invalid (%d problem", len(e.Problems))
	if len(e.Problems) != 1 {
		b.WriteByte('s')
	}
	b.WriteString(")")
	for _, p := range e.Problems {
		b.WriteString("\n  ")
		b.WriteString(p.String())
	}
	return b.String()
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type validator struct {
	vars     map[string]bool
	problems []Problem
}

func (v *validator) add(path, format string, args ...any) {
	v.problems = append(v.problems, Problem{Path: path, Msg: fmt.Sprintf(format, args...)})
}

// Validate checks m and returns a *ValidationError listing every problem, or
// nil. Static checks only: runtime facts (does the element exist, is the
// variable supplied) belong to the engine.
func Validate(m *Macro) error {
	if m == nil {
		return &ValidationError{Problems: []Problem{{Path: "$", Msg: "macro is nil"}}}
	}
	v := &validator{vars: map[string]bool{}}

	if m.SchemaVersion != CurrentVersion {
		v.add("schemaVersion", "must be %d, got %d", CurrentVersion, m.SchemaVersion)
	}
	if !idPattern.MatchString(m.ID) {
		v.add("id", "must be non-empty and match [A-Za-z0-9_-]+")
	}
	if strings.TrimSpace(m.Name) == "" {
		v.add("name", "required")
	}

	v.origins(m.AllowedOrigins)
	v.variables(m.Variables)

	if len(m.Steps) == 0 {
		v.add("steps", "at least one step required")
	}
	seen := map[string]bool{}
	for i := range m.Steps {
		v.step(m, i, seen)
	}
	for i := range m.Guards {
		v.guard(i, &m.Guards[i])
	}

	if len(v.problems) == 0 {
		return nil
	}
	return &ValidationError{Problems: v.problems}
}

func (v *validator) origins(origins []string) {
	if len(origins) == 0 {
		v.add("allowedOrigins", "at least one origin required")
	}
	seen := map[string]bool{}
	for i, o := range origins {
		path := fmt.Sprintf("allowedOrigins[%d]", i)
		if err := checkOrigin(o); err != nil {
			v.add(path, "%v", err)
			continue
		}
		if k := strings.ToLower(o); seen[k] {
			v.add(path, "duplicate origin %q", o)
		} else {
			seen[k] = true
		}
	}
}

// checkOrigin accepts scheme://host[:port] with http/https and an optional
// "*." host prefix; anything with a path, query, fragment or userinfo is not
// an origin.
func checkOrigin(o string) error {
	u, err := url.Parse(o)
	if err != nil {
		return fmt.Errorf("invalid origin %q: %v", o, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("origin %q must use http or https", o)
	}
	if u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
		strings.HasSuffix(o, "?") || strings.HasSuffix(o, "#") {
		return fmt.Errorf("origin %q must be scheme://host[:port] with no path, query or credentials", o)
	}
	host := u.Hostname()
	if rest, ok := strings.CutPrefix(host, "*."); ok {
		host = rest
	}
	if host == "" || strings.Contains(host, "*") {
		return fmt.Errorf("origin %q: '*' is only allowed as a leading \"*.\" label", o)
	}
	return nil
}

func (v *validator) variables(decls []VariableDecl) {
	for i, d := range decls {
		path := fmt.Sprintf("variables[%d]", i)
		if !validName(d.Name) {
			v.add(path+".name", "must be an identifier ([A-Za-z_][A-Za-z0-9_]*), got %q", d.Name)
		} else if v.vars[d.Name] {
			v.add(path+".name", "duplicate variable %q", d.Name)
		} else {
			v.vars[d.Name] = true
		}
		switch d.Type {
		case VarString, VarNumber, VarBool, VarDate:
		default:
			v.add(path+".type", "must be string, number, bool or date, got %q", d.Type)
			continue
		}
		switch d.Kind {
		case "", KindText, KindEmail, KindPhone, KindNumber, KindURL, KindDate, KindPassword:
		default:
			v.add(path+".kind", "must be text, email, phone, number, url, date or password, got %q", d.Kind)
		}
		if d.Default != nil {
			if err := checkDefault(d.Type, d.Default); err != nil {
				v.add(path+".default", "%v", err)
			}
		}
	}
}

func checkDefault(t VarType, def any) error {
	ok := false
	switch t {
	case VarString:
		_, ok = def.(string)
	case VarNumber:
		switch def.(type) {
		case float64, float32, int, int32, int64:
			ok = true
		}
	case VarBool:
		_, ok = def.(bool)
	case VarDate:
		s, isStr := def.(string)
		if !isStr {
			return fmt.Errorf("default for a date variable must be an ISO-8601 string")
		}
		if _, err := parseDate(s); err != nil {
			return err
		}
		return nil
	}
	if !ok {
		return fmt.Errorf("default %v does not match type %s", def, t)
	}
	return nil
}

// expr checks one template string: syntax, transforms, declared variables.
// It reports the parsed parts so callers can do static checks on constants.
func (v *validator) expr(path, s string) []Part {
	parts, err := ParseExpr(s)
	if err != nil {
		v.add(path, "%v", err)
		return nil
	}
	for _, name := range Refs(parts) {
		if !v.vars[name] {
			v.add(path, "references undeclared variable %q", name)
		}
	}
	return parts
}

func isConstant(parts []Part) bool { return len(Refs(parts)) == 0 }

func (v *validator) value(path string, val *Value) []Part {
	parts := v.expr(path+".expr", val.Expr)
	for i, t := range val.Transforms {
		if _, err := ParseTransform(t); err != nil {
			v.add(fmt.Sprintf("%s.transforms[%d]", path, i), "%v", err)
		}
	}
	return parts
}

func (v *validator) locator(path string, l *Locator) {
	switch l.Kind {
	case LocRole, LocLabel, LocText, LocCSS, LocXPath, LocTestID:
	default:
		v.add(path+".kind", "must be role, label, text, css, xpath or testid, got %q", l.Kind)
		return
	}
	if strings.TrimSpace(l.Value) == "" {
		v.add(path+".value", "required")
	} else {
		v.expr(path+".value", l.Value)
	}
	if l.Name != "" {
		if l.Kind != LocRole {
			v.add(path+".name", "only allowed for role locators")
		} else {
			v.expr(path+".name", l.Name)
		}
	}
}

func (v *validator) target(path string, t *Target) {
	if len(t.Locators) == 0 {
		v.add(path+".locators", "at least one locator required")
	}
	for i := range t.Locators {
		v.locator(fmt.Sprintf("%s.locators[%d]", path, i), &t.Locators[i])
	}
	for i, s := range t.FramePath {
		if strings.TrimSpace(s) == "" {
			v.add(fmt.Sprintf("%s.framePath[%d]", path, i), "selector must not be empty")
		}
	}
	for i, s := range t.ShadowPath {
		if strings.TrimSpace(s) == "" {
			v.add(fmt.Sprintf("%s.shadowPath[%d]", path, i), "selector must not be empty")
		}
	}
}

func (v *validator) step(m *Macro, i int, seen map[string]bool) {
	s := &m.Steps[i]
	path := fmt.Sprintf("steps[%d]", i)
	switch {
	case !idPattern.MatchString(s.ID):
		v.add(path+".id", "must be non-empty and match [A-Za-z0-9_-]+")
	case seen[s.ID]:
		v.add(path+".id", "duplicate step id %q", s.ID)
	default:
		seen[s.ID] = true
		path = "steps[" + s.ID + "]"
	}

	known := true
	switch s.Action {
	case ActionNavigate, ActionClick, ActionDblClick, ActionFill, ActionSelect, ActionPress, ActionCheck,
		ActionUploadFile, ActionWait, ActionAssert, ActionScroll, ActionManual:
	default:
		known = false
		v.add(path+".action", "unknown action %q", s.Action)
	}

	if s.Target != nil {
		v.target(path+".target", s.Target)
	}
	var valueParts []Part
	if s.Value != nil {
		valueParts = v.value(path+".value", s.Value)
	}
	if known {
		v.requiredFields(m, path, s, valueParts)
	}

	if s.CommitPoint && s.Action != ActionClick && s.Action != ActionPress && s.Action != ActionFill {
		v.add(path+".commitPoint", "only allowed on click, press and fill steps, not %q", s.Action)
	}
	for j := range s.Expect {
		v.expectation(fmt.Sprintf("%s.expect[%d]", path, j), &s.Expect[j])
	}
	if s.Timeout < 0 || time.Duration(s.Timeout) > maxTimeout {
		v.add(path+".timeout", "must be between 0 and %s", maxTimeout)
	}
	if s.Retries < 0 || s.Retries > maxRetries {
		v.add(path+".retries", "must be between 0 and %d", maxRetries)
	}
	switch s.OnFail {
	case "", OnFailStop, OnFailSkip, OnFailPause:
	default:
		v.add(path+".onFail", "must be stop, skip or pause, got %q", s.OnFail)
	}
}

func (v *validator) requiredFields(m *Macro, path string, s *Step, valueParts []Part) {
	needTarget := func() {
		if s.Target == nil {
			v.add(path+".target", "required for %s", s.Action)
		}
	}
	needValue := func() {
		if s.Value == nil || strings.TrimSpace(s.Value.Expr) == "" {
			v.add(path+".value", "required for %s", s.Action)
		}
	}
	switch s.Action {
	case ActionNavigate:
		needValue()
		if s.Target != nil {
			v.add(path+".target", "not allowed for navigate")
		}
		if s.Value != nil && valueParts != nil && isConstant(valueParts) {
			// A templated URL can only be checked by the engine at run time.
			if !m.AllowsURL(s.Value.Expr) {
				v.add(path+".value.expr", "URL %q is not http(s) within allowedOrigins", s.Value.Expr)
			}
		}
	case ActionClick, ActionDblClick:
		needTarget()
	case ActionFill, ActionSelect, ActionUploadFile:
		needTarget()
		needValue()
	case ActionPress, ActionManual:
		needValue()
	case ActionCheck:
		needTarget()
	case ActionWait:
		if s.Target == nil && s.Timeout <= 0 {
			v.add(path, "wait needs a target or a positive timeout")
		}
	case ActionAssert:
		if len(s.Expect) == 0 {
			v.add(path+".expect", "assert needs at least one expectation")
		}
	case ActionScroll:
		if s.Target == nil && s.Value == nil {
			v.add(path, "scroll needs a target or a value")
		}
	}
}

func (v *validator) expectation(path string, e *Expectation) {
	needTarget := func() {
		if e.Target == nil {
			v.add(path+".target", "required for %s", e.Kind)
		}
	}
	needValue := func() {
		if e.Value == nil || e.Value.Expr == "" {
			v.add(path+".value", "required for %s", e.Kind)
		}
	}
	switch e.Kind {
	case ExpectURLMatches:
		needValue()
		if e.Target != nil {
			v.add(path+".target", "not allowed for urlMatches")
		}
	case ExpectElementPresent, ExpectElementAbsent:
		needTarget()
	case ExpectTextContains, ExpectTextEquals, ExpectValueEquals:
		needTarget()
		needValue()
	default:
		v.add(path+".kind", "unknown expectation kind %q", e.Kind)
		return
	}
	if e.Target != nil {
		v.target(path+".target", e.Target)
	}
	if e.Value != nil {
		parts := v.value(path+".value", e.Value)
		if e.Kind == ExpectURLMatches && parts != nil && isConstant(parts) {
			if _, err := regexp.Compile(e.Value.Expr); err != nil {
				v.add(path+".value.expr", "invalid regexp: %v", err)
			}
		}
	}
}

func (v *validator) guard(i int, g *Guard) {
	path := fmt.Sprintf("guards[%d]", i)
	switch g.Kind {
	case GuardURLMatches:
		if g.Pattern == "" {
			v.add(path+".pattern", "required for urlMatches")
		} else if _, err := regexp.Compile(g.Pattern); err != nil {
			v.add(path+".pattern", "invalid regexp: %v", err)
		}
		if g.Locator != nil {
			v.add(path+".locator", "not allowed for urlMatches")
		}
	case GuardElementPresent, GuardElementAbsent:
		if g.Locator == nil {
			v.add(path+".locator", "required for %s", g.Kind)
		} else {
			v.locator(path+".locator", g.Locator)
		}
		if g.Pattern != "" {
			v.add(path+".pattern", "only allowed for urlMatches")
		}
	default:
		v.add(path+".kind", "must be urlMatches, elementPresent or elementAbsent, got %q", g.Kind)
	}
	switch g.Action {
	case GuardNeedsLogin, GuardPause, GuardFail:
	default:
		v.add(path+".action", "must be needsLogin, pause or fail, got %q", g.Action)
	}
}

// AllowsURL reports whether raw is an http(s) URL whose origin matches one of
// the macro's AllowedOrigins. Wildcard origins ("https://*.example.com") match
// subdomains only, never the bare domain.
func (m *Macro) AllowsURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	for _, o := range m.AllowedOrigins {
		ou, err := url.Parse(o)
		if err != nil || ou.Scheme != u.Scheme {
			continue
		}
		oh := strings.ToLower(ou.Host)
		if rest, ok := strings.CutPrefix(oh, "*."); ok {
			if strings.HasSuffix(host, "."+rest) {
				return true
			}
		} else if host == oh {
			return true
		}
	}
	return false
}
