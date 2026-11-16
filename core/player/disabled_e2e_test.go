package player_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/data"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/player"
	"github.com/8bu/ladilala/core/recorder/rectest"
)

// TestE2EPlayerDisabledStep records a form, disables the step that fills the
// City field and gives it a required variable the data row does not have. The
// run must start (the variable is not needed), skip the step, and the page
// must receive an empty City. The template must not list the variable either.
func TestE2EPlayerDisabledStep(t *testing.T) {
	site, ctx := e2eSetup(t, 3*time.Minute)
	m := recordWith(ctx, t, 93, func(u *rectest.User) error {
		if err := u.Navigate(site.URL("placeholders.html")); err != nil {
			return err
		}
		u.Wait(800 * time.Millisecond)
		for _, f := range [][2]string{
			{"#email", "a@example.org"}, {"#email2", "a@example.org"}, {"#fullname", "Anna Test"},
			{"#code", "abc123"}, {"#phone", "0901234567"}, {"#pw", "password-1"}, {"#city", "SKIPME"},
		} {
			if err := u.Click(f[0]); err != nil {
				return err
			}
			if err := u.Type(f[1]); err != nil {
				return err
			}
		}
		if err := u.Click("button[type=submit]"); err != nil {
			return err
		}
		u.Wait(500 * time.Millisecond)
		return nil
	})
	site.ResetSinks()

	row := map[string]string{"password": "password-1"} // the row has no city
	enabled := 0
	for i := range m.Steps {
		s := &m.Steps[i]
		if s.Value != nil && s.Value.Expr == "SKIPME" {
			s.Value.Expr = "{{city}}"
			s.Disabled = true
			continue
		}
		enabled++
	}
	if enabled != len(m.Steps)-1 {
		t.Fatalf("found %d steps to disable, want 1", len(m.Steps)-enabled)
	}
	m.Variables = append(m.Variables, macro.VariableDecl{Name: "city", Type: macro.VarString, Required: true})
	if err := macro.Validate(m); err != nil {
		t.Fatalf("macro with a disabled step must validate: %v", err)
	}
	for _, c := range data.TemplateColumns(m) {
		if c.Name == "city" {
			t.Errorf("template lists %q, a variable only a disabled step reads", c.Name)
		}
	}
	if _, err := player.Preflight(m, row); err != nil {
		t.Fatalf("preflight must ignore the disabled step: %v", err)
	}

	err := replayIn(ctx, t, 94, m, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitSinks(site, 3*time.Second, "placeholders")
	subs := site.Sinks("placeholders")
	if len(subs) != 1 {
		t.Fatalf("the page got %d submissions, want 1", len(subs))
	}
	if got := fmt.Sprint(subs[0]["city"]); got != "" {
		t.Errorf("city = %q, want empty: the disabled step was played", got)
	}
	if got := fmt.Sprint(subs[0]["fullname"]); got != "Anna Test" {
		t.Errorf("fullname = %q, enabled steps must still run", got)
	}
}
