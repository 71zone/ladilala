package rectest

import (
	"fmt"
	"strconv"
	"time"

	"github.com/8bu/ladilala/core/recorder"
)

// Recorded are the literals the scripted session types; Parameterize maps
// them to columns of the same name.
var Recorded = map[string]string{
	"first_name": "Anna", "last_name": "Lee", "email": "anna@example.com", "country": "de",
	"notes": "Hello there", "nick": "Annie", "late": "L8", "city_same": "Hue", "city_cross": "Oslo", "shadow_name": "Sh",
}

// RecordedPassword is typed into the password field and must never reach the macro.
const RecordedPassword = "pw-recorded-1"

// ExpectedActions is the step list a faithful recorder produces for Scenario.
var ExpectedActions = []string{
	// form.html
	"navigate", "fill", "fill", "fill", "press", "fill", "press", "select", "check", "check", "fill", "click",
	// nav.html -> page2.html
	"navigate", "click", "fill", "click",
	// delayed.html
	"navigate", "fill", "click",
	// frames.html: same-origin frame, cross-origin frame
	"navigate", "fill", "click", "fill", "click",
	// shadow.html
	"navigate", "fill", "click",
}

// Row returns data row i (0-based) for every column of Recorded plus password.
func Row(i int) map[string]string {
	n := strconv.Itoa(i + 1)
	names := []string{"Anna", "Ben", "Cara"}
	countries := []string{"de", "ca", "vn"}
	nm := names[i%len(names)]
	return map[string]string{
		"first_name": nm, "last_name": nm + "son", "email": nm + "@example.org", "country": countries[i%len(countries)],
		"notes": "Note " + n, "nick": nm + "ie", "late": "Late" + n, "city_same": "Same" + n, "city_cross": "Cross" + n,
		"shadow_name": "Shadow" + n, "password": "pw-row-" + n,
	}
}

// Scenario is one scripted session over the test pages: every kind of field,
// a navigation caused by a click, a delayed field, two iframes and a shadow root.
func Scenario(u *User, site *recorder.Site) error {
	steps := []func() error{
		func() error { return u.Navigate(site.URL("form.html")) },
		func() error { return u.ClickAndType("#first", Recorded["first_name"]) },
		func() error { return u.Click("input[name=last_name]") },
		func() error { return u.Type(Recorded["last_name"]) },
		func() error { return u.Click("#email") },
		func() error { return u.Type(Recorded["email"]) },
		func() error { return u.Expect("#email", Recorded["email"]) },
		func() error { return u.Key("Tab") },
		func() error { return u.Type(RecordedPassword) },
		func() error { return u.Key("Tab") },
		func() error { return u.Type("G") }, // type-ahead on the focused select: Germany
		func() error { return u.Click("input[name=newsletter]") },
		func() error { return u.Click("input[value=pro]") },
		func() error { return u.Click("#notes") },
		func() error { return u.Type(Recorded["notes"]) },
		func() error { return u.Click("button[type=submit]") },
		func() error { return u.WaitFor("document.getElementById('out').textContent.length > 0", 3*time.Second) },

		func() error { return u.Navigate(site.URL("nav.html")) },
		func() error { return u.ClickByText("a", "Go to page two") },
		func() error {
			return u.WaitFor("location.pathname === '/page2.html' && document.readyState === 'complete'", 5*time.Second)
		},
		func() error { return u.Click("input[name=nick]") },
		func() error { return u.Type(Recorded["nick"]) },
		func() error { return u.Click("button") },
		func() error { return u.WaitFor("document.getElementById('out').textContent.length > 0", 3*time.Second) },

		func() error { return u.Navigate(site.URL("delayed.html")) },
		func() error { return u.WaitFor("document.querySelector('input[name=late]')", 5*time.Second) },
		func() error { return u.Click("input[name=late]") },
		func() error { return u.Type(Recorded["late"]) },
		func() error { return u.Click("button") },
		func() error { return u.WaitFor("document.getElementById('out').textContent.length > 0", 3*time.Second) },

		func() error { return u.Navigate(site.URL("frames.html")) },
		func() error {
			return u.WaitFor("document.getElementById('same').contentDocument.readyState === 'complete'", 5*time.Second)
		},
		func() error { return u.ClickSameOriginFrame("#same", "input[name=city]") },
		func() error { return u.Type(Recorded["city_same"]) },
		func() error { return u.ClickSameOriginFrame("#same", "button") },
		func() error { return u.ClickCrossOriginFrame("#cross", "input[name=city]") },
		func() error { return u.Type(Recorded["city_cross"]) },
		func() error { return u.ClickCrossOriginFrame("#cross", "button") },
		func() error { u.Wait(400 * time.Millisecond); return nil },

		func() error { return u.Navigate(site.URL("shadow.html")) },
		func() error { return u.ClickShadow("#host", "input") },
		func() error { return u.Type(Recorded["shadow_name"]) },
		func() error { return u.ClickShadow("#host", "button") },
		func() error { u.Wait(400 * time.Millisecond); return nil },
	}
	for i, s := range steps {
		if err := s(); err != nil {
			return fmt.Errorf("scenario step %d: %w", i, err)
		}
	}
	return nil
}

// ExpectedSinks returns what the form pages must have received after a replay
// of row r: page name to field values.
func ExpectedSinks(r map[string]string) map[string]map[string]string {
	return map[string]map[string]string{
		"form": {"first_name": r["first_name"], "last_name": r["last_name"], "email": r["email"], "password": r["password"],
			"country": r["country"], "newsletter": "yes", "plan": "pro", "notes": r["notes"]},
		"page2":       {"nick": r["nick"]},
		"delayed":     {"late": r["late"]},
		"frame-same":  {"city": r["city_same"]},
		"frame-cross": {"city": r["city_cross"]},
		"shadow":      {"sname": r["shadow_name"]},
	}
}
