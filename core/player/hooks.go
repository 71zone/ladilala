package player

import (
	"context"
	"fmt"
	"regexp"

	"github.com/8bu/ladilala/core/macro"
)

// Hooks let the run engine watch and gate a row. Every field is optional.
type Hooks struct {
	// BeforeStep runs before each step, after the guards were checked. It may
	// block (pause, takeover). An error stops the row with that error.
	BeforeStep func(ctx context.Context, index int, s *macro.Step) error
	// BeforeCommit runs right before the input of a commit point step reaches
	// the page. When it returns an error nothing was sent. After it returned
	// nil the step is never retried.
	BeforeCommit func(ctx context.Context, s *macro.Step) error
	// AfterCommit runs when the commit point step ended. err is nil when the
	// action and its expectations passed.
	AfterCommit func(ctx context.Context, s *macro.Step, err error)
	// Manual runs for a manual step. It returns when the person is done.
	Manual func(ctx context.Context, s *macro.Step, instruction string) error
	// DryRun skips every commit point step.
	DryRun bool
}

// ErrGuard is matched by errors.Is for a *GuardError.
var ErrGuard = fmt.Errorf("a macro guard fired")

// GuardError says which guard fired on the current page.
type GuardError struct {
	Index int
	Guard macro.Guard
	Why   string
}

func (e *GuardError) Error() string {
	return fmt.Sprintf("guard %d (%s) fired, action %s: %s", e.Index, e.Guard.Kind, e.Guard.Action, e.Why)
}
func (e *GuardError) Unwrap() error { return ErrGuard }

// CheckGuards evaluates the guards of m against the current page, in order.
// It returns a *GuardError for the first guard that fires and nil when none
// does. A page that cannot be read yet (no document) fires nothing.
func (p *Player) CheckGuards(ctx context.Context, m *macro.Macro) error {
	for i := range m.Guards {
		g := &m.Guards[i]
		switch g.Kind {
		case macro.GuardURLMatches:
			re, err := regexp.Compile(g.Pattern)
			if err != nil {
				return err
			}
			var u string
			if err := p.evalIn(ctx, &frame{pg: p.Page, id: p.Page.TargetID}, `location.href`, &u); err != nil {
				continue
			}
			if re.MatchString(u) {
				return &GuardError{Index: i, Guard: *g, Why: "url " + u + " matches " + g.Pattern}
			}
		case macro.GuardElementPresent, macro.GuardElementAbsent:
			if g.Locator == nil {
				continue
			}
			st, _, err := p.Locate(ctx, &macro.Target{Locators: []macro.Locator{*g.Locator}})
			if err != nil {
				continue
			}
			present := st == "ok" || st == "ambiguous"
			if present == (g.Kind == macro.GuardElementPresent) {
				return &GuardError{Index: i, Guard: *g, Why: fmt.Sprintf("locator %s=%q is %s", g.Locator.Kind, g.Locator.Value, map[bool]string{true: "present", false: "absent"}[present])}
			}
		}
	}
	return nil
}
