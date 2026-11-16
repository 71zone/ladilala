package recorder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/recorder"
	"github.com/8bu/ladilala/core/recorder/rectest"
)

func e2eBrowser() string {
	if b := os.Getenv("LADILALA_E2E_BROWSER"); b != "" {
		return b
	}
	return "chrome"
}

// TestE2ERecord records the scripted session (real CDP input events) on the
// local test pages and checks the macro: step sequence, locators, password
// placeholder, frame and shadow paths, and that it loads with macro.Load.
func TestE2ERecord(t *testing.T) {
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
	rec := recorder.New(recorder.Options{ID: "e2e-signup", Name: "E2E sign-up"})
	b, err := rectest.Launch(ctx, e2eBrowser(), 11, rec.Hook)
	if err != nil {
		t.Skipf("launch: %v", err)
	}
	defer b.Close()
	pg, err := b.Session.WaitPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u := rectest.NewUser(ctx, b.Session, pg)
	if err := rectest.Scenario(u, site); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	rec.Stop()
	m, err := rec.Macro()
	if err != nil {
		t.Fatalf("macro invalid: %v", err)
	}
	var got []string
	for _, s := range m.Steps {
		got = append(got, string(s.Action))
	}
	if strings.Join(got, ",") != strings.Join(rectest.ExpectedActions, ",") {
		t.Errorf("recorded actions differ\n got  %v\n want %v", got, rectest.ExpectedActions)
	}
	raw, err := macro.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(rectest.RecordedPassword)) {
		t.Error("the typed password is in the macro")
	}
	if _, err := macro.Load(bytes.NewReader(raw)); err != nil {
		t.Errorf("macro.Load rejects the recording: %v", err)
	}
	t.Logf("macro (%d steps, origins %v):\n%s", len(m.Steps), m.AllowedOrigins, raw)
	st := rec.Stats()
	sent := b.Session.SentMethods()
	t.Logf("stats: events=%d hookMs=%v overlayMs=%v latencyMs=%v gaps=%v navReasons=%v", st.Events, st.HookMs, st.OverlayMs, st.LatencyMs, st.Gaps, st.NavReasons)
	sj, _ := json.Marshal(sent)
	t.Logf("sent methods: %s", sj)
	if sent["Runtime.enable"] != 0 {
		t.Errorf("Runtime.enable was sent %d times", sent["Runtime.enable"])
	}
	if errs := rec.Errors(); len(errs) > 0 {
		t.Errorf("recorder errors: %v", errs)
	}
	if errs := b.Session.SetupErrors(); len(errs) > 0 {
		t.Errorf("setup errors: %v", errs)
	}
}

// recordWith records what f does on the test site and returns the recorder.
func recordWith(t *testing.T, seed uint64, f func(ctx context.Context, u *rectest.User, site *recorder.Site) error) *recorder.Recorder {
	t.Helper()
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
	rec := recorder.New(recorder.Options{ID: "e2e", Name: "e2e"})
	b, err := rectest.Launch(ctx, e2eBrowser(), seed, rec.Hook)
	if err != nil {
		t.Skipf("launch: %v", err)
	}
	defer b.Close()
	pg, err := b.Session.WaitPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u := rectest.NewUser(ctx, b.Session, pg)
	if err := f(ctx, u, site); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	rec.Stop()
	if sent := b.Session.SentMethods(); sent["Runtime.enable"] != 0 {
		t.Errorf("Runtime.enable was sent %d times", sent["Runtime.enable"])
	}
	if errs := rec.Errors(); len(errs) > 0 {
		t.Errorf("recorder errors: %v", errs)
	}
	return rec
}

func actionsOf(m *macro.Macro) string {
	var a []string
	for _, s := range m.Steps {
		a = append(a, string(s.Action))
	}
	return strings.Join(a, ",")
}

// TestE2ERecordDblClick: a real double click is one dblclick step, not two clicks.
func TestE2ERecordDblClick(t *testing.T) {
	var log []string
	rec := recordWith(t, 61, func(ctx context.Context, u *rectest.User, site *recorder.Site) error {
		for _, f := range []func() error{
			func() error { return u.Navigate(site.URL("dblclick.html")) },
			func() error { return u.Click("#single") },
			func() error { return u.DoubleClick("#dbl") },
			func() error { u.Wait(300 * time.Millisecond); return u.Page.Eval(ctx, "window.log", &log) },
		} {
			if err := f(); err != nil {
				return err
			}
		}
		return nil
	})
	m, err := rec.Macro()
	if err != nil {
		t.Fatal(err)
	}
	if got := actionsOf(m); got != "navigate,click,dblclick" {
		t.Errorf("actions = %s, want navigate,click,dblclick", got)
	}
	t.Logf("page saw while recording: %v", log)
}

// TestE2ERecordDate edits native date and month inputs with the keyboard
// and expects one fill step per field holding the ISO value.
func TestE2ERecordDate(t *testing.T) {
	var date, month string
	rec := recordWith(t, 62, func(ctx context.Context, u *rectest.User, site *recorder.Site) error {
		if err := u.Navigate(site.URL("dates.html")); err != nil {
			return err
		}
		var err error
		if date, err = u.EditDate("#p_date", 3); err != nil {
			return err
		}
		if month, err = u.EditDate("#p_month", 2); err != nil {
			return err
		}
		return u.Click("button[type=submit]")
	})
	m, err := rec.Macro()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("date=%q month=%q steps=%s", date, month, actionsOf(m))
	for _, st := range m.Steps {
		if st.Value != nil {
			t.Logf("step %s value %q", st.ID, st.Value.Expr)
		}
	}
	if got := actionsOf(m); got != "navigate,fill,fill,click" {
		t.Fatalf("actions = %s, want navigate,fill,fill,click (one fill per field)", got)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(date) || !regexp.MustCompile(`^\d{4}-\d{2}$`).MatchString(month) {
		t.Fatalf("the page holds date=%q month=%q, not ISO values", date, month)
	}
	if got := m.Steps[1].Value.Expr; got != date {
		t.Errorf("date fill = %q, want %q", got, date)
	}
	if got := m.Steps[2].Value.Expr; got != month {
		t.Errorf("month fill = %q, want %q", got, month)
	}
}

// TestE2ERecordUnsupported: canvas and file inputs (also in an iframe, in a
// shadow root, behind a label and as a drop zone) raise typed warnings and
// record no step.
func TestE2ERecordUnsupported(t *testing.T) {
	rec := recordWith(t, 63, func(ctx context.Context, u *rectest.User, site *recorder.Site) error {
		for _, f := range []func() error{
			func() error { return u.Navigate(site.URL("unsupported.html")) },
			func() error {
				return u.WaitFor("document.getElementById('cf').contentDocument.readyState === 'complete'", 5*time.Second)
			},
			func() error { return u.Click("#cv") },
			func() error { u.Wait(900 * time.Millisecond); return u.Drag("#cv") },
			func() error { return u.ClickSameOriginFrame("#cf", "#cvf") },
			func() error { return u.ClickShadow("#host", "#cvs") },
			func() error { return u.Click("#f1") },
			func() error { return u.Click("label[for=f2]") },
			func() error { return u.Click("#zone") },
		} {
			if err := f(); err != nil {
				return err
			}
		}
		return nil
	})
	m, err := rec.Macro()
	if err != nil {
		t.Fatal(err)
	}
	if got := actionsOf(m); got != "navigate" {
		t.Errorf("actions = %s, want only navigate: unsupported interactions record no step", got)
	}
	seen := map[string]bool{}
	for _, w := range rec.Warnings() {
		if w.Message == "" || w.StepIndex != 1 {
			t.Errorf("warning %+v: want a message and step index 1", w)
		}
		seen[string(w.Kind)+" "+w.Locator.Value] = true
	}
	for _, want := range []string{
		"canvas_unsupported #cv", "canvas_unsupported #cvf", "canvas_unsupported #cvs",
		"file_input_unsupported Resume file", "file_input_unsupported Photo", "file_input_unsupported #f3",
	} {
		if !seen[want] {
			t.Errorf("missing warning %q; got %v", want, seen)
		}
	}
	t.Logf("warnings: %+v", rec.Warnings())
}

// searchSession records a search on page: typing then Enter, waits for
// the results page, then a typed URL after the suppression window.
func searchSession(t *testing.T, seed uint64, page string) *macro.Macro {
	t.Helper()
	rec := recordWith(t, seed, func(ctx context.Context, u *rectest.User, site *recorder.Site) error {
		for _, f := range []func() error{
			func() error { return u.Navigate(site.URL(page)) },
			func() error { return u.Click("#q") },
			func() error {
				if err := u.Type("Xin chao"); err != nil {
					return err
				}
				return u.Key("Enter")
			},
			func() error { return u.WaitFor("location.pathname === '/results.html'", 5*time.Second) },
			func() error { u.Wait(3500 * time.Millisecond); return u.Navigate(site.URL("page2.html")) },
		} {
			if err := f(); err != nil {
				return err
			}
		}
		return nil
	})
	m, err := rec.Macro()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func checkSearch(t *testing.T, m *macro.Macro) {
	t.Helper()
	// The results URL (and any redirect hop) is no step; the typed URL later is.
	if got := actionsOf(m); got != "navigate,fill,press,navigate" {
		t.Fatalf("actions = %s, want navigate,fill,press,navigate", got)
	}
	if v := m.Steps[1].Value.Expr; v != "Xin chao" {
		t.Errorf("fill = %q", v)
	}
	p := m.Steps[2]
	if p.Value.Expr != "Enter" || !p.CommitPoint || p.Target == nil {
		t.Errorf("press = %+v, want Enter with target and commitPoint", p)
	}
	if strings.Contains(m.Steps[3].Value.Expr, "results") {
		t.Errorf("last navigate = %s, want the typed page2 URL", m.Steps[3].Value.Expr)
	}
}

// TestE2ERecordSearchScript: Enter in a combobox textarea whose script changes
// location after 150 ms records a press, not a navigate to the results.
func TestE2ERecordSearchScript(t *testing.T) {
	checkSearch(t, searchSession(t, 81, "searchjs.html"))
}

// TestE2ERecordSearchGetRedirect: a native GET form submit with a 302 hop.
func TestE2ERecordSearchGetRedirect(t *testing.T) {
	checkSearch(t, searchSession(t, 82, "searchget.html"))
}

// TestE2ERecordTextareaNewline: Enter in a plain textarea is a new line, no step.
func TestE2ERecordTextareaNewline(t *testing.T) {
	rec := recordWith(t, 84, func(ctx context.Context, u *rectest.User, site *recorder.Site) error {
		for _, f := range []func() error{
			func() error { return u.Navigate(site.URL("form.html")) },
			func() error { return u.Click("#notes") },
			func() error { return u.Type("a") },
			func() error { return u.Key("Enter") },
			func() error { return u.Type("b") },
			func() error { u.Wait(900 * time.Millisecond); return nil },
		} {
			if err := f(); err != nil {
				return err
			}
		}
		return nil
	})
	m, err := rec.Macro()
	if err != nil {
		t.Fatal(err)
	}
	if got := actionsOf(m); got != "navigate,fill" {
		t.Errorf("actions = %s, want navigate,fill", got)
	}
}
