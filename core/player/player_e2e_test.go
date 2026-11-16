package player_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/player"
	"github.com/8bu/ladilala/core/recorder"
	"github.com/8bu/ladilala/core/recorder/rectest"
)

func e2eBrowser() string {
	if b := os.Getenv("LADILALA_E2E_BROWSER"); b != "" {
		return b
	}
	return "chrome"
}

// recordMacro records the scripted session and turns the typed literals into columns.
func recordMacro(ctx context.Context, site *recorder.Site) (*macro.Macro, error) {
	rec := recorder.New(recorder.Options{ID: "e2e-signup", Name: "E2E sign-up"})
	b, err := rectest.Launch(ctx, e2eBrowser(), 21, rec.Hook)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	pg, err := b.Session.WaitPage(ctx)
	if err != nil {
		return nil, err
	}
	if err := rectest.Scenario(rectest.NewUser(ctx, b.Session, pg), site); err != nil {
		return nil, err
	}
	time.Sleep(400 * time.Millisecond)
	rec.Stop()
	m, err := rec.Macro()
	if err != nil {
		return nil, err
	}
	if _, err := recorder.Parameterize(m, rectest.Recorded); err != nil {
		return nil, err
	}
	raw, err := macro.Marshal(m)
	if err != nil {
		return nil, err
	}
	return macro.Load(bytes.NewReader(raw))
}

// TestE2EPlayer records a session, then replays it for three data rows, each in
// a fresh profile, and checks what the pages received. It ends with the typed
// errors against a live page.
func TestE2EPlayer(t *testing.T) {
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	site, err := recorder.NewSite()
	if err != nil {
		t.Fatal(err)
	}
	defer site.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	m, err := recordMacro(ctx, site)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	// The recording's origins hold the ports of this run; the replay uses the same site.
	t.Logf("macro: %d steps, %d variables, origins %v", len(m.Steps), len(m.Variables), m.AllowedOrigins)

	for i := 0; i < 3; i++ {
		row := rectest.Row(i)
		t.Run(fmt.Sprintf("row%d", i+1), func(t *testing.T) {
			site.ResetSinks()
			b, err := rectest.Launch(ctx, e2eBrowser(), uint64(100+i), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			pg, err := b.Session.WaitPage(ctx)
			if err != nil {
				t.Fatal(err)
			}
			p := player.New(b.Session, pg)
			p.Log = func(f string, a ...any) { t.Logf(f, a...) }
			t0 := time.Now()
			if err := p.Run(ctx, m, row); err != nil {
				t.Fatalf("replay: %v", err)
			}
			t.Logf("replay of %d steps took %s", len(m.Steps), time.Since(t0).Round(time.Millisecond))
			time.Sleep(300 * time.Millisecond)
			for page, want := range rectest.ExpectedSinks(row) {
				subs := site.Sinks(page)
				if len(subs) != 1 {
					t.Errorf("%s: %d submissions, want 1", page, len(subs))
					continue
				}
				for k, v := range want {
					if got := fmt.Sprint(subs[0][k]); got != v {
						t.Errorf("%s.%s = %q, want %q", page, k, got, v)
					}
				}
			}
			if sent := b.Session.SentMethods(); sent["Runtime.enable"] != 0 {
				t.Errorf("Runtime.enable was sent %d times", sent["Runtime.enable"])
			}
		})
	}

	t.Run("typedErrors", func(t *testing.T) {
		b, err := rectest.Launch(ctx, e2eBrowser(), 200, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		pg, _ := b.Session.WaitPage(ctx)
		p := player.New(b.Session, pg)
		mk := func(origins []string, steps ...macro.Step) *macro.Macro {
			return &macro.Macro{SchemaVersion: 1, ID: "err", Name: "err", AllowedOrigins: origins, Steps: steps}
		}
		open := macro.Step{ID: "open", Action: macro.ActionNavigate, Value: &macro.Value{Expr: site.URL("form.html")}}
		click := func(id, css string) macro.Step {
			return macro.Step{ID: id, Action: macro.ActionClick, Timeout: macro.Duration(1200 * time.Millisecond),
				Target: &macro.Target{Locators: []macro.Locator{{Kind: macro.LocCSS, Value: css}}}}
		}
		main := []string{site.Main.URL}
		if err := p.Run(ctx, mk(main, open, click("many", "input")), nil); !errors.Is(err, player.ErrAmbiguous) {
			t.Errorf("ambiguous locator: err = %v", err)
		} else {
			t.Logf("ambiguous: %v", err)
		}
		if err := p.Run(ctx, mk(main, open, click("none", "#nope")), nil); !errors.Is(err, player.ErrNotFound) {
			t.Errorf("missing locator: err = %v", err)
		} else {
			t.Logf("missing: %v", err)
		}
		cross := macro.Step{ID: "cross", Action: macro.ActionFill, Timeout: macro.Duration(2 * time.Second), Value: &macro.Value{Expr: "x"},
			Target: &macro.Target{Locators: []macro.Locator{{Kind: macro.LocCSS, Value: "input"}}, FramePath: []string{"#cross"}}}
		frames := macro.Step{ID: "frames", Action: macro.ActionNavigate, Value: &macro.Value{Expr: site.URL("frames.html")}}
		if err := p.Run(ctx, mk(main, frames, cross), nil); !errors.Is(err, player.ErrOrigin) {
			t.Errorf("acting in a cross-origin frame: err = %v", err)
		} else {
			t.Logf("origin (frame): %v", err)
		}
		away := macro.Step{ID: "away", Action: macro.ActionNavigate, Value: &macro.Value{Expr: site.CrossOrigin() + "/form.html"}}
		if err := p.Run(ctx, mk(main, away), nil); !errors.Is(err, player.ErrOrigin) {
			t.Errorf("navigating to a foreign origin: err = %v", err)
		}
		withVar := mk(main, macro.Step{ID: "v", Action: macro.ActionNavigate, Value: &macro.Value{Expr: site.URL("{{page}}")}})
		withVar.Variables = []macro.VariableDecl{{Name: "page", Type: macro.VarString, Required: true}}
		if err := p.Run(ctx, withVar, map[string]string{}); !errors.Is(err, player.ErrMissingColumn) {
			t.Errorf("missing column: err = %v", err)
		}
	})
}

// TestE2EPlayerLocatorStability records on markup v1 of a page, then asks the
// player how each recorded locator fares on markup v2 (a redeploy with wrapper
// elements, a banner in front and generated ids). It reports, per locator kind,
// how many recorded targets are still found by exactly one element.
func TestE2EPlayerLocatorStability(t *testing.T) {
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	site, err := recorder.NewSite()
	if err != nil {
		t.Fatal(err)
	}
	defer site.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	rec := recorder.New(recorder.Options{ID: "stability"})
	b, err := rectest.Launch(ctx, e2eBrowser(), 41, rec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	pg, _ := b.Session.WaitPage(ctx)
	u := rectest.NewUser(ctx, b.Session, pg)
	for _, f := range []func() error{
		func() error { return u.Navigate(site.URL("rerender.html?v=1")) },
		func() error { return u.Click("#q") },
		func() error { return u.Type("shoes") },
		func() error { return u.Click("#exact") },
		func() error { return u.Click("button") },
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	rec.Stop()
	m, err := rec.Macro()
	if err != nil {
		t.Fatal(err)
	}
	b.Close()

	b2, err := rectest.Launch(ctx, e2eBrowser(), 42, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.Close()
	pg2, _ := b2.Session.WaitPage(ctx)
	if err := pg2.Navigate(ctx, site.URL("rerender.html?v=2")); err != nil {
		t.Fatal(err)
	}
	p := player.New(b2.Session, pg2)
	survived, total := map[string]int{}, map[string]int{}
	whole := 0
	targets := 0
	for _, s := range m.Steps {
		if s.Target == nil {
			continue
		}
		targets++
		status, _, err := p.Locate(ctx, s.Target)
		if err != nil {
			t.Fatal(err)
		}
		if status == "ok" {
			whole++
		}
		for _, l := range s.Target.Locators {
			one := &macro.Target{Locators: []macro.Locator{l}}
			st, _, err := p.Locate(ctx, one)
			if err != nil {
				t.Fatal(err)
			}
			key := string(l.Kind)
			if l.Kind == macro.LocCSS {
				key = "css(" + cssClass(l.Value) + ")"
			}
			total[key]++
			if st == "ok" {
				survived[key]++
			}
		}
		t.Logf("%s: locator list resolves on v2: %s", s.ID, status)
	}
	t.Logf("targets resolved on v2 with the whole list: %d of %d", whole, targets)
	for k, n := range total {
		t.Logf("locator %-14s still unique on v2: %d of %d", k, survived[k], n)
	}
}

func cssClass(v string) string {
	switch {
	case len(v) > 0 && v[0] == '#':
		return "id"
	case strings.Contains(v, "[name="):
		return "name"
	}
	return "path"
}

// e2eSetup starts the test site and a context for one test.
func e2eSetup(t *testing.T, d time.Duration) (*recorder.Site, context.Context) {
	t.Helper()
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	site, err := recorder.NewSite()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(site.Close)
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return site, ctx
}

// recordWith records what f does in a recording browser and returns the macro.
func recordWith(ctx context.Context, t *testing.T, seed uint64, f func(u *rectest.User) error) *macro.Macro {
	t.Helper()
	rec := recorder.New(recorder.Options{ID: "e2e", Name: "e2e"})
	b, err := rectest.Launch(ctx, e2eBrowser(), seed, rec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	pg, err := b.Session.WaitPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f(rectest.NewUser(ctx, b.Session, pg)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	rec.Stop()
	m, err := rec.Macro()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// replayIn runs m in a fresh browser, then calls check with the tab.
func replayIn(ctx context.Context, t *testing.T, seed uint64, m *macro.Macro, row map[string]string, check func(pg *driver.Page)) error {
	t.Helper()
	b, err := rectest.Launch(ctx, e2eBrowser(), seed, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	pg, err := b.Session.WaitPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := player.New(b.Session, pg)
	p.Log = func(f string, a ...any) { t.Logf(f, a...) }
	t0 := time.Now()
	runErr := p.Run(ctx, m, row)
	t.Logf("replay of %d steps took %s", len(m.Steps), time.Since(t0).Round(time.Millisecond))
	if check != nil {
		check(pg)
	}
	if sent := b.Session.SentMethods(); sent["Runtime.enable"] != 0 {
		t.Errorf("Runtime.enable was sent %d times", sent["Runtime.enable"])
	}
	return runErr
}

// TestE2EPlayerDblClick records a real double click and replays it: the page
// must see the same events in the same order, with the same click counts.
func TestE2EPlayerDblClick(t *testing.T) {
	site, ctx := e2eSetup(t, 3*time.Minute)
	var recorded []string
	m := recordWith(ctx, t, 71, func(u *rectest.User) error {
		for _, f := range []func() error{
			func() error { return u.Navigate(site.URL("dblclick.html")) },
			func() error { return u.Click("#single") }, // the first press after a load can be eaten by window focus
			func() error { return u.DoubleClick("#dbl") },
			func() error { u.Wait(300 * time.Millisecond); return u.Page.Eval(ctx, "window.log", &recorded) },
		} {
			if err := f(); err != nil {
				return err
			}
		}
		return nil
	})
	var acts []string
	for _, s := range m.Steps {
		acts = append(acts, string(s.Action))
	}
	if strings.Join(acts, ",") != "navigate,click,dblclick" {
		t.Fatalf("recorded %v, want navigate,click,dblclick", acts)
	}
	var replayed []string
	err := replayIn(ctx, t, 72, m, nil, func(pg *driver.Page) {
		time.Sleep(300 * time.Millisecond)
		_ = pg.Eval(ctx, "window.log", &replayed)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("recording saw %v", recorded)
	t.Logf("replay saw    %v", replayed)
	want := "mousedown:1 mouseup:1 click:1 mousedown:2 mouseup:2 click:2 dblclick:2"
	if strings.Join(recorded, " ") != want || strings.Join(replayed, " ") != want {
		t.Errorf("event sequence differs: recorded %v, replayed %v, want %s", recorded, replayed, want)
	}
}

var dateTypes = []string{"date", "time", "datetime-local", "month", "week"}

var dateRows = []map[string]string{
	{"date": "2026-01-15", "time": "09:05", "datetime-local": "2026-01-15T09:05", "month": "2026-01", "week": "2026-W03"},
	{"date": "2027-12-31", "time": "23:59", "datetime-local": "2027-12-31T23:59", "month": "2027-12", "week": "2027-W52"},
	{"date": "2024-02-29", "time": "00:00", "datetime-local": "2024-02-29T00:00:30", "month": "2024-02", "week": "2020-W53"},
}

func cssTarget(sel string, frame, shadow []string) *macro.Target {
	return &macro.Target{Locators: []macro.Locator{{Kind: macro.LocCSS, Value: sel}}, FramePath: frame, ShadowPath: shadow}
}

// TestE2EPlayerDates fills every native date-like input type from a data
// column for three rows: plain, controlled (React-style), inside a same-origin
// iframe and inside an open shadow root. Then min/max and format rejection.
func TestE2EPlayerDates(t *testing.T) {
	site, ctx := e2eSetup(t, 6*time.Minute)
	var steps []macro.Step
	add := func(s macro.Step) { s.ID = fmt.Sprintf("s%02d", len(steps)+1); steps = append(steps, s) }
	add(macro.Step{Action: macro.ActionNavigate, Value: &macro.Value{Expr: site.URL("dates.html")}})
	// A harmless first press: the first press in a freshly launched window can be swallowed (Windows, SSH session).
	add(macro.Step{Action: macro.ActionClick, Target: cssTarget("h1", nil, nil)})
	var vars []macro.VariableDecl
	for _, ty := range dateTypes {
		vars = append(vars, macro.VariableDecl{Name: "v_" + strings.ReplaceAll(ty, "-", "_"), Type: macro.VarString, Required: true})
		ref := "{{v_" + strings.ReplaceAll(ty, "-", "_") + "}}"
		add(macro.Step{Action: macro.ActionFill, Target: cssTarget("input[name=plain_"+ty+"]", nil, nil), Value: &macro.Value{Expr: ref}})
		add(macro.Step{Action: macro.ActionFill, Target: cssTarget("input[name=c_"+ty+"]", nil, nil), Value: &macro.Value{Expr: ref}})
	}
	add(macro.Step{Action: macro.ActionFill, Target: cssTarget("input[name=sdate]", nil, []string{"#host"}), Value: &macro.Value{Expr: "{{v_date}}"}})
	add(macro.Step{Action: macro.ActionClick, Target: cssTarget("button", nil, []string{"#host"})})
	add(macro.Step{Action: macro.ActionFill, Target: cssTarget("input[name=fdate]", []string{"#dframe"}, nil), Value: &macro.Value{Expr: "{{v_date}}"}})
	add(macro.Step{Action: macro.ActionClick, Target: cssTarget("button", []string{"#dframe"}, nil)})
	add(macro.Step{Action: macro.ActionClick, Target: cssTarget("button[type=submit]", nil, nil)})
	m := &macro.Macro{SchemaVersion: 1, ID: "dates", Name: "dates", AllowedOrigins: []string{site.Main.URL}, Variables: vars, Steps: steps}
	if err := macro.Validate(m); err != nil {
		t.Fatal(err)
	}
	for i, data := range dateRows {
		row := map[string]string{}
		for ty, v := range data {
			row["v_"+strings.ReplaceAll(ty, "-", "_")] = v
		}
		t.Run(fmt.Sprintf("row%d", i+1), func(t *testing.T) {
			site.ResetSinks()
			if err := replayIn(ctx, t, uint64(300+i), m, row, nil); err != nil {
				t.Fatal(err)
			}
			waitSinks(site, 3*time.Second, "dates", "dates-shadow", "dates-frame")
			one := func(page string) map[string]any {
				s := site.Sinks(page)
				if len(s) != 1 {
					t.Errorf("%s: %d submissions, want 1", page, len(s))
					return map[string]any{}
				}
				return s[0]
			}
			form := one("dates")
			for ty, want := range data {
				if got := fmt.Sprint(form["plain_"+ty]); !sameDate(got, want) {
					t.Errorf("plain %s = %q, want %q", ty, got, want)
				}
				if got := fmt.Sprint(form["c_"+ty]); !sameDate(got, want) {
					t.Errorf("controlled %s input = %q, want %q", ty, got, want)
				}
				if got := fmt.Sprint(form["state_"+ty]); !sameDate(got, want) {
					t.Errorf("controlled %s state = %q, want %q (the page's framework did not see the change)", ty, got, want)
				}
			}
			if got := fmt.Sprint(one("dates-shadow")["sdate"]); got != data["date"] {
				t.Errorf("shadow date = %q, want %q", got, data["date"])
			}
			if got := fmt.Sprint(one("dates-frame")["fdate"]); got != data["date"] {
				t.Errorf("frame date = %q, want %q", got, data["date"])
			}
		})
	}

	t.Run("rejections", func(t *testing.T) {
		mk := func(name, sel, val string) *macro.Macro {
			return &macro.Macro{SchemaVersion: 1, ID: "r", Name: "r", AllowedOrigins: []string{site.Main.URL}, Steps: []macro.Step{
				{ID: "open", Action: macro.ActionNavigate, Value: &macro.Value{Expr: site.URL("dates.html")}},
				{ID: name, Action: macro.ActionFill, Target: cssTarget(sel, nil, nil), Value: &macro.Value{Expr: val}, Timeout: macro.Duration(3 * time.Second), Retries: 2},
			}}
		}
		cases := []struct{ name, sel, val, field, want string }{
			{"below", "input[name=mm_date]", "2026-01-31", "mm_date", "below min 2026-02-01"},
			{"above", "input[name=mm_date]", "2026-03-01", "mm_date", "above max 2026-02-28"},
			{"early", "input[name=mm_time]", "08:59", "mm_time", "below min 09:00"},
			{"format", "input[name=plain_date]", "15/01/2026", "plain_date", "expected YYYY-MM-DD"},
			{"impossible", "input[name=plain_date]", "2026-02-30", "plain_date", "expected YYYY-MM-DD"},
			{"week", "input[name=plain_week]", "2026-W54", "plain_week", "expected YYYY-Www"},
			{"time", "input[name=plain_time]", "9:5", "plain_time", "expected HH:MM"},
		}
		for _, c := range cases {
			var after string
			err := replayIn(ctx, t, 400, mk(c.name, c.sel, c.val), nil, func(pg *driver.Page) {
				_ = pg.Eval(ctx, "document.querySelector('"+c.sel+"').value", &after)
			})
			var bv *player.BadValueError
			if !errors.Is(err, player.ErrBadValue) || !errors.As(err, &bv) {
				t.Errorf("%s: err = %v, want ErrBadValue", c.name, err)
				continue
			}
			if bv.Field != c.field || bv.Value != c.val || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s: %v; want field %s and %q", c.name, err, c.field, c.want)
			}
			if after != "" {
				t.Errorf("%s: a rejected value must leave the field empty, it holds %q", c.name, after)
			}
			t.Logf("%s: %v", c.name, err)
		}
		// Edges of the range are accepted.
		if err := replayIn(ctx, t, 401, mk("edge", "input[name=mm_date]", "2026-02-28"), nil, nil); err != nil {
			t.Errorf("max edge: %v", err)
		}
	})

	t.Run("recorded", func(t *testing.T) {
		// Record real segment edits, parameterize them, replay other values.
		var recDate, recMonth string
		rm := recordWith(ctx, t, 73, func(u *rectest.User) error {
			if err := u.Navigate(site.URL("dates.html")); err != nil {
				return err
			}
			var err error
			if recDate, err = u.EditDate("#p_date", 3); err != nil {
				return err
			}
			if recMonth, err = u.EditDate("#p_month", 2); err != nil {
				return err
			}
			return u.Click("button[type=submit]")
		})
		warm := macro.Step{ID: "warm", Action: macro.ActionClick, Target: cssTarget("h1", nil, nil)}
		rm.Steps = append([]macro.Step{rm.Steps[0], warm}, rm.Steps[1:]...)
		if _, err := recorder.Parameterize(rm, map[string]string{"when": recDate, "mon": recMonth}); err != nil {
			t.Fatal(err)
		}
		site.ResetSinks()
		raw, _ := macro.Marshal(rm)
		t.Logf("recorded macro: %s", raw)
		if err := replayIn(ctx, t, 74, rm, map[string]string{"when": "2031-07-04", "mon": "2031-07"}, func(pg *driver.Page) {
			var info string
			_ = pg.Eval(ctx, "location.href + ' valid=' + document.getElementById('f').checkValidity() + ' out=' + document.getElementById('out').textContent + ' date=' + document.getElementById('p_date').value", &info)
			t.Logf("page after replay: %s", info)
		}); err != nil {
			t.Fatal(err)
		}
		waitSinks(site, 3*time.Second, "dates")
		s := site.Sinks("dates")
		if len(s) != 1 || fmt.Sprint(s[0]["plain_date"]) != "2031-07-04" || fmt.Sprint(s[0]["plain_month"]) != "2031-07" {
			t.Errorf("submission = %v", s)
		}
	})
}

// sameDate compares a submitted value with the intended one ignoring a zero
// seconds part, which browsers drop from time values.
func sameDate(got, want string) bool {
	return got == want || strings.TrimSuffix(want, ":00") == got
}

// waitSinks waits until every page has received a submission, or d passes.
func waitSinks(site *recorder.Site, d time.Duration, pages ...string) {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		all := true
		for _, pg := range pages {
			all = all && len(site.Sinks(pg)) > 0
		}
		if all {
			return
		}
	}
}

// TestE2EPlayerSearchEnter records a search in a script-driven search box (the
// Enter is a press step, the results navigation is not) and replays it for 3
// rows: each row must search for its own text.
func TestE2EPlayerSearchEnter(t *testing.T) {
	site, ctx := e2eSetup(t, 4*time.Minute)
	for _, page := range []string{"searchjs.html", "searchget.html"} {
		m := recordWith(ctx, t, 91, func(u *rectest.User) error {
			for _, f := range []func() error{
				func() error { return u.Navigate(site.URL(page)) },
				func() error { return u.Click("#q") },
				func() error { return u.Type("Xin chao") },
				func() error { return u.Key("Enter") },
				func() error { return u.WaitFor("location.pathname === '/results.html'", 5*time.Second) },
				func() error { u.Wait(600 * time.Millisecond); return nil },
			} {
				if err := f(); err != nil {
					return err
				}
			}
			return nil
		})
		var acts []string
		for _, s := range m.Steps {
			acts = append(acts, string(s.Action))
		}
		if strings.Join(acts, ",") != "navigate,fill,press" {
			t.Fatalf("%s: recorded %v, want navigate,fill,press", page, acts)
		}
		if _, err := recorder.Parameterize(m, map[string]string{"q": "Xin chao"}); err != nil {
			t.Fatal(err)
		}
		for i, want := range []string{"alpha one", "beta two", "gamma 3"} {
			site.ResetSinks()
			if err := replayIn(ctx, t, uint64(92+i), m, map[string]string{"q": want}, func(pg *driver.Page) {
				waitSinks(site, 5*time.Second, "results")
			}); err != nil {
				t.Fatalf("%s row %d: %v", page, i, err)
			}
			s := site.Sinks("results")
			if len(s) == 0 || s[len(s)-1]["q"] != want {
				t.Errorf("%s row %d: results sink %v, want q=%q", page, i, s, want)
			}
		}
	}
}
