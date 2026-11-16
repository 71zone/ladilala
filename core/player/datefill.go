package player

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dateInputWant maps the type of a native date-like input to the ISO format
// the player requires for it.
var dateInputWant = map[string]string{
	"date":           "YYYY-MM-DD",
	"time":           "HH:MM or HH:MM:SS",
	"datetime-local": "YYYY-MM-DDTHH:MM or YYYY-MM-DDTHH:MM:SS",
	"month":          "YYYY-MM",
	"week":           "YYYY-Www",
}

var weekRe = regexp.MustCompile(`^(\d{4})-W(\d{2})$`)

// checkDateValue reports whether v is a valid ISO value for an input of the
// given type. An empty value is valid: it clears the field.
func checkDateValue(typ, v string) bool {
	if v == "" {
		return true
	}
	ok := func(layouts ...string) bool {
		for _, l := range layouts {
			if _, err := time.Parse(l, v); err == nil && len(v) == len(l) {
				return true
			}
		}
		return false
	}
	switch typ {
	case "date":
		return ok("2006-01-02")
	case "time":
		return ok("15:04", "15:04:05")
	case "datetime-local":
		return ok("2006-01-02T15:04", "2006-01-02T15:04:05")
	case "month":
		return ok("2006-01")
	case "week":
		m := weekRe.FindStringSubmatch(v)
		if m == nil {
			return false
		}
		y, _ := strconv.Atoi(m[1])
		w, _ := strconv.Atoi(m[2])
		_, last := time.Date(y, 12, 28, 0, 0, 0, 0, time.UTC).ISOWeek()
		return w >= 1 && w <= last
	}
	return false
}

// sameInputValue compares two values of a date-like input ignoring a zero
// seconds part, which browsers add or drop.
func sameInputValue(a, b string) bool {
	trim := func(s string) string {
		s = strings.TrimSuffix(s, ".000")
		return strings.TrimSuffix(s, ":00")
	}
	return a == b || (strings.Count(a, ":") != strings.Count(b, ":") && trim(a) == trim(b))
}

// fillDate sets a date-like input without the keyboard: through the native
// value setter in the player's isolated world (so a framework's own value
// tracker on the page object does not swallow the change), then input and
// change events. It checks min and max and reads the value back.
func (p *Player) fillDate(ctx context.Context, fr *frame, stepID, typ, field, want string) error {
	bad := func(reason string) error {
		return &BadValueError{Step: stepID, Field: field, Type: typ, Value: want, Want: dateInputWant[typ], Reason: reason}
	}
	if !checkDateValue(typ, want) {
		return bad("")
	}
	js := fmt.Sprintf(`(async function(v){const el=globalThis.__ladiEl;const prev=el.value;
const set=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;
set.call(el,v);const o={set:el.value};
if(v!==''&&(el.validity.rangeUnderflow||el.validity.rangeOverflow)){
 o.range=el.validity.rangeUnderflow?'below min '+el.min:'above max '+el.max;set.call(el,prev);return o}
el.dispatchEvent(new Event('input',{bubbles:true,composed:true}));
el.dispatchEvent(new Event('change',{bubbles:true,composed:true}));
await new Promise(r=>setTimeout(r,30));o.final=el.value;return o})(%s)`, jsq(want))
	var r struct {
		Set   string `json:"set"`
		Range string `json:"range"`
		Final string `json:"final"`
	}
	if err := p.evalIn(ctx, fr, js, &r); err != nil {
		return err
	}
	switch {
	case r.Range != "":
		return bad("the value is " + r.Range)
	case !sameInputValue(r.Set, want):
		return bad(fmt.Sprintf("the browser kept %q", r.Set))
	case !sameInputValue(r.Final, want):
		return bad(fmt.Sprintf("the page changed the value to %q", r.Final))
	}
	return nil
}
