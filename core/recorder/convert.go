package recorder

import (
	"errors"
	"fmt"
	"strings"

	"github.com/8bu/ladilala/core/macro"
)

var validKinds = map[string]macro.LocatorKind{
	"role": macro.LocRole, "label": macro.LocLabel, "text": macro.LocText,
	"css": macro.LocCSS, "xpath": macro.LocXPath, "testid": macro.LocTestID,
}

// targetFromRaw turns the locators the page built into a schema target. The
// page order is kept (it is the stability order); unknown kinds, empty values
// and duplicates are dropped. framePrefix is the path of the iframe that holds
// a cross-origin frame, put before the page's own same-origin frame chain.
func targetFromRaw(raw *rawTarget, framePrefix []string) (*macro.Target, error) {
	if raw == nil {
		return nil, errors.New("no-target")
	}
	t := &macro.Target{}
	seen := map[macro.Locator]bool{}
	for _, l := range raw.Locators {
		kind, ok := validKinds[l.Kind]
		if !ok || strings.TrimSpace(l.Value) == "" {
			continue
		}
		loc := macro.Locator{Kind: kind, Value: macro.EscapeLiteral(l.Value)}
		if kind == macro.LocRole {
			loc.Name = macro.EscapeLiteral(l.Name)
		}
		if !seen[loc] {
			seen[loc] = true
			t.Locators = append(t.Locators, loc)
		}
	}
	if len(t.Locators) == 0 {
		return nil, errors.New("no-locator")
	}
	t.ShadowPath = append(t.ShadowPath, raw.ShadowPath...)
	if raw.CrossFrame {
		t.FramePath = append(t.FramePath, framePrefix...)
	}
	t.FramePath = append(t.FramePath, raw.FramePath...)
	return t, nil
}

// convertStep maps one page event to a schema step. The error text is the gap
// name counted in the report.
func convertStep(ev rawEvent, framePrefix []string) (macro.Step, error) {
	var st macro.Step
	switch ev.Action {
	case "click":
		st.Action = macro.ActionClick
	case "dblclick":
		st.Action = macro.ActionDblClick
	case "fill":
		st.Action = macro.ActionFill
	case "select":
		st.Action = macro.ActionSelect
	case "check":
		st.Action = macro.ActionCheck
	case "press":
		st.Action = macro.ActionPress
	default:
		return st, fmt.Errorf("unknown-action-%s", ev.Action)
	}
	tgt, err := targetFromRaw(ev.Target, framePrefix)
	if err != nil {
		return st, err
	}
	st.Target = tgt
	switch st.Action {
	case macro.ActionFill, macro.ActionSelect, macro.ActionCheck, macro.ActionPress:
		v := ev.Value
		if ev.Sensitive {
			v = "{{" + sensitiveVar + "}}"
		} else {
			v = macro.EscapeLiteral(v)
		}
		st.Value = &macro.Value{Expr: v}
	}
	if ev.Commit {
		st.CommitPoint = true
	}
	return st, nil
}
