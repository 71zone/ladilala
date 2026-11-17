package recorder_test

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/recorder"
	"github.com/8bu/ladilala/core/recorder/rectest"
)

// TestE2EOverlayLayout checks that the overlay is laid out on a light page and
// on a dark page with hostile global styles: the bar, the panel and the
// first-use card are position:fixed and their boxes sit inside the viewport.
// Run it with LADILALA_E2E_BROWSER=fpc and LADILALA_FPC_DIR for the
// fingerprint-chromium build. LADILALA_E2E_SHOTS saves a screenshot per page.
func TestE2EOverlayLayout(t *testing.T) {
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	if e2eBrowser() == "fpc" && os.Getenv("LADILALA_FPC_DIR") == "" {
		t.Skip("LADILALA_FPC_DIR not set")
	}
	site, err := recorder.NewSite()
	if err != nil {
		t.Fatal(err)
	}
	defer site.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rec := recorder.New(recorder.Options{ID: "e2e-layout", Name: "layout"})
	rec.SetOptions("en", true)
	b, err := rectest.Launch(ctx, e2eBrowser(), 91, rec.Hook)
	if err != nil {
		t.Skipf("launch: %v", err)
	}
	defer b.Close()
	pg, err := b.Session.WaitPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u := rectest.NewUser(ctx, b.Session, pg)
	for _, page := range []string{"placeholders.html", "dark.html"} {
		if err := u.Navigate(site.URL(page)); err != nil {
			t.Fatal(err)
		}
		var lay recorder.OverlayLayout
		for try := 0; try < 30; try++ {
			if lay, err = rec.OverlayLayout(ctx, pg); err == nil && lay.Card.Display == "block" {
				break
			}
			u.Wait(150 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		t.Logf("%s: %+v", page, lay)
		if dir := os.Getenv("LADILALA_E2E_SHOTS"); dir != "" {
			var res struct {
				Data string `json:"data"`
			}
			if err := pg.Call(ctx, "Page.captureScreenshot", map[string]any{"format": "png"}, &res); err == nil {
				raw, _ := base64.StdEncoding.DecodeString(res.Data)
				_ = os.WriteFile(filepath.Join(dir, "overlay-"+e2eBrowser()+"-"+page+".png"), raw, 0o644)
			}
		}
		for name, box := range map[string]recorder.OverlayBox{"bar": lay.Bar, "panel": lay.Panel, "card": lay.Card} {
			if box.Position != "fixed" {
				t.Errorf("%s: %s has position %q, want fixed", page, name, box.Position)
			}
			if box.Display == "none" {
				continue
			}
			if box.W <= 0 || box.H <= 0 || box.X < 0 || box.Y < 0 || box.X+box.W > lay.VW || box.Y+box.H > lay.VH {
				t.Errorf("%s: %s box (%.0f,%.0f %.0fx%.0f) is outside the %.0fx%.0f viewport", page, name, box.X, box.Y, box.W, box.H, lay.VW, lay.VH)
			}
		}
	}
}
