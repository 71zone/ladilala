package recorder_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/recorder"
	"github.com/8bu/ladilala/core/recorder/rectest"
)

// TestE2ERecordPlaceholders types {{placeholders}} into real fields with real CDP
// key events. Each one must become a test token the page accepts (checkValidity),
// the steps must hold the columns, the main world must see nothing new, and the
// overlay panel must rename and delete columns.
func TestE2ERecordPlaceholders(t *testing.T) {
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
	rec := recorder.New(recorder.Options{ID: "e2e-ph", Name: "placeholders"})
	rec.SetOptions("en", true)
	b, err := rectest.Launch(ctx, e2eBrowser(), 81, rec.Hook)
	if err != nil {
		t.Skipf("launch: %v", err)
	}
	defer b.Close()
	pg, err := b.Session.WaitPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u := rectest.NewUser(ctx, b.Session, pg)
	if err := u.Navigate(site.URL("placeholders.html")); err != nil {
		t.Fatal(err)
	}
	u.Wait(800 * time.Millisecond)

	shot := func(name string) {
		dir := os.Getenv("LADILALA_E2E_SHOTS")
		if dir == "" {
			return
		}
		var res struct {
			Data string `json:"data"`
		}
		if err := pg.Call(ctx, "Page.captureScreenshot", map[string]any{"format": "png"}, &res); err != nil {
			t.Logf("screenshot: %v", err)
			return
		}
		raw, _ := base64.StdEncoding.DecodeString(res.Data)
		_ = os.WriteFile(filepath.Join(dir, name), raw, 0o644)
	}
	eval := func(js string, out any) {
		t.Helper()
		if err := pg.Eval(ctx, js, out); err != nil {
			t.Fatal(err)
		}
	}
	value := func(css string) string {
		var v string
		eval("document.querySelector("+`"`+css+`"`+").value", &v)
		return v
	}
	valid := func(css string) bool {
		var v bool
		eval("document.querySelector("+`"`+css+`"`+").checkValidity()", &v)
		return v
	}
	// type puts the text into a field the way a person does: click, then keys.
	typeInto := func(css, text string) {
		t.Helper()
		if err := u.Click(css); err != nil {
			t.Fatal(err)
		}
		if err := u.Type(text); err != nil {
			t.Fatal(err)
		}
	}
	// Names of every property of window before the first placeholder: later there must be none more.
	var before []string
	eval("Object.getOwnPropertyNames(window)", &before)

	// The first-use hint card shows in the page at the start.
	var hx, hy float64
	for try := 0; try < 20; try++ {
		if hx, hy, err = rec.OverlayPoint(ctx, pg, "hint-chip"); err == nil {
			break
		}
		u.Wait(150 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("hint card is not shown: %v", err)
	}
	shot("e-hint.png")
	_ = hx
	_ = hy

	// 1. Email: no replacement while typing, a token that passes type=email when }} closes.
	if err := u.Click("#email"); err != nil {
		t.Fatal(err)
	}
	if err := u.Type("{{emai"); err != nil {
		t.Fatal(err)
	}
	if got := value("#email"); got != "{{emai" {
		t.Errorf("value while typing = %q, want it untouched", got)
	}
	if err := u.Type("l}}"); err != nil {
		t.Fatal(err)
	}
	emailTok := value("#email")
	if !regexp.MustCompile(`^ladi[0-9a-z]{6}@example\.com$`).MatchString(emailTok) || !valid("#email") {
		t.Errorf("email token = %q (valid %v)", emailTok, valid("#email"))
	}
	// 2. Pattern field with a two-word name.
	typeInto("#code", "{{Order Code}}")
	codeTok := value("#code")
	if !regexp.MustCompile(`^[a-z0-9]{6,12}$`).MatchString(codeTok) || !valid("#code") {
		t.Errorf("code token = %q", codeTok)
	}
	// 3. Vietnamese name.
	typeInto("#fullname", "{{Họ tên}}")
	nameTok := value("#fullname")
	if !strings.HasPrefix(nameTok, "ladi") || !valid("#fullname") {
		t.Errorf("name token = %q", nameTok)
	}
	// 4. Phone, password.
	typeInto("#phone", "{{Phone}}")
	if tok := value("#phone"); !regexp.MustCompile(`^0[0-9]{9}$`).MatchString(tok) || !valid("#phone") {
		t.Errorf("phone token = %q", tok)
	}
	typeInto("#pw", "{{pw}}")
	if !valid("#pw") || len(value("#pw")) < 8 {
		t.Errorf("password token %q is not valid", value("#pw"))
	}
	// 5. Text around a placeholder; the same name again reuses the token.
	typeInto("#notes", "Hello {{ho tên}}!")
	if got := value("#notes"); got != "Hello "+nameTok+"!" {
		t.Errorf("notes = %q, want %q", got, "Hello "+nameTok+"!")
	}
	typeInto("#email2", "{{EMAIL}}")
	if got := value("#email2"); got != emailTok {
		t.Errorf("same column typed again gave %q, want the same token %q", got, emailTok)
	}
	// 6. Text inserted in one piece (paste, IME commit): same result.
	if err := u.Click("#city"); err != nil {
		t.Fatal(err)
	}
	if err := pg.Call(ctx, "Input.insertText", map[string]any{"text": "{{city}}"}, nil); err != nil {
		t.Fatal(err)
	}
	u.Wait(200 * time.Millisecond)
	if got := value("#city"); !strings.HasPrefix(got, "ladi") {
		t.Errorf("inserted placeholder gave %q", got)
	}
	// 7. A select and a checkbox keep the real choice.
	if err := u.Key("Tab"); err != nil {
		t.Fatal(err)
	}
	if err := u.Type("V"); err != nil { // type-ahead on the focused select: Vietnam
		t.Fatal(err)
	}
	if err := u.Click("input[name=news]"); err != nil {
		t.Fatal(err)
	}
	u.Wait(200 * time.Millisecond)

	// Tokens are all different.
	seen := map[string]bool{}
	for _, tok := range []string{emailTok, codeTok, nameTok, value("#phone"), value("#pw"), value("#city")} {
		if seen[tok] {
			t.Errorf("token %q is used for two columns", tok)
		}
		seen[tok] = true
	}
	shot("e-overlay.png")

	// 8. The overlay panel lists the columns; rename code, delete phone.
	click := func(what string) {
		t.Helper()
		var x, y float64
		var err error
		for try := 0; try < 20; try++ {
			if x, y, err = rec.OverlayPoint(ctx, pg, what); err == nil {
				break
			}
			u.Wait(150 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("overlay control %q: %v", what, err)
		}
		if err := u.ClickAtPoint(x, y); err != nil {
			t.Fatal(err)
		}
		u.Wait(300 * time.Millisecond)
	}
	if _, _, err := rec.OverlayPoint(ctx, pg, "rename:order_code"); err != nil {
		click("cols") // the panel opened by itself at the first column; this is the fallback
	}
	if err := waitCount(rec, "email", 2); err != nil {
		t.Error(err)
	}
	click("rename:order_code")
	if err := u.Type("code"); err != nil {
		t.Fatal(err)
	}
	if err := u.Key("Enter"); err != nil {
		t.Fatal(err)
	}
	u.Wait(400 * time.Millisecond)
	click("delete:phone")
	shot("e-delete.png")
	click("confirm")
	u.Wait(400 * time.Millisecond)

	// 9. The page accepts the form: submit it and look at what the page received.
	site.ResetSinks()
	if err := u.Click("button[type=submit]"); err != nil {
		t.Fatal(err)
	}
	u.Wait(500 * time.Millisecond)
	subs := site.Sinks("placeholders")
	if len(subs) != 1 {
		t.Fatalf("the page got %d submissions, want 1 (its validation must pass with the tokens)", len(subs))
	}
	if subs[0]["email"] != emailTok || subs[0]["fullname"] != nameTok {
		t.Errorf("submission %v does not hold the tokens", subs[0])
	}

	// 10. Nothing new in the main world.
	var after []string
	eval("Object.getOwnPropertyNames(window)", &after)
	old := map[string]bool{}
	for _, n := range before {
		old[n] = true
	}
	for _, n := range after {
		if !old[n] {
			t.Errorf("new global in the page: %s", n)
		}
	}
	var leak map[string]string
	eval(`({ladiEmit: typeof window.ladiEmit, rec: typeof window.__ladiRec, loc: typeof window.__ladiLoc, flush: typeof window.__ladiFlush, ph: typeof window.__ladiPh})`, &leak)
	for k, v := range leak {
		if v != "undefined" {
			t.Errorf("main world has %s", k)
		}
	}

	time.Sleep(300 * time.Millisecond)
	rec.Stop()
	m, err := rec.Macro()
	if err != nil {
		t.Fatalf("macro invalid: %v", err)
	}
	var got []string
	for _, s := range m.Steps {
		v := ""
		if s.Value != nil {
			v = s.Value.Expr
		}
		if strings.Contains(v, "ladi") {
			t.Errorf("a token was recorded as typed text: %s", v)
		}
		got = append(got, string(s.Action)+"="+v)
	}
	want := []string{
		"navigate=" + site.URL("placeholders.html"), "fill={{email}}", "fill={{code}}", "fill={{ho_ten}}", "fill={{pw}}", "fill=Hello {{ho_ten}}!",
		"fill={{email}}", "fill={{city}}", "press=Tab", "select=vn", "check=true", "click=",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("steps\n got  %v\n want %v", got, want)
	}
	for i, s := range m.Steps {
		if wantID := fmt.Sprintf("s%03d-%s", i+1, s.Action); s.ID != wantID {
			t.Errorf("step %d has ID %s after the delete, want %s", i, s.ID, wantID)
		}
	}
	kinds := map[string]macro.VarKind{}
	for _, v := range m.Variables {
		kinds[v.Name] = v.Kind
	}
	wantKinds := map[string]macro.VarKind{"email": macro.KindEmail, "code": macro.KindText, "ho_ten": macro.KindText, "pw": macro.KindPassword, "city": macro.KindText}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Errorf("variables = %v, want %v", kinds, wantKinds)
	}
	for _, c := range rec.Columns() {
		if c.Name == "phone" || c.Name == "order_code" {
			t.Errorf("column %s is still listed", c.Name)
		}
		if c.Name == "pw" && !c.Protected {
			t.Error("a password column must be protected")
		}
	}
	if closed, _ := rec.HintState(); !closed {
		t.Error("the hint card should close at the first column")
	}
	if sent := b.Session.SentMethods(); sent["Runtime.enable"] != 0 {
		t.Errorf("Runtime.enable was sent %d times", sent["Runtime.enable"])
	}
	if errs := rec.Errors(); len(errs) > 0 {
		t.Errorf("recorder errors: %v", errs)
	}
}

func waitCount(rec *recorder.Recorder, col string, n int) error {
	for try := 0; try < 30; try++ {
		cnt := 0
		for _, s := range rec.Steps() {
			if s.Value != nil && strings.Contains(s.Value.Expr, "{{"+col+"}}") {
				cnt++
			}
		}
		if cnt >= n {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("column %s is not used by %d steps", col, n)
}
