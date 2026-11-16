package player_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/data"
	"github.com/8bu/ladilala/core/player"
	"github.com/8bu/ladilala/core/recorder"
	"github.com/8bu/ladilala/core/recorder/rectest"
	"github.com/xuri/excelize/v2"
)

// TestE2EPlayerPlaceholders records a form by typing {{placeholders}}, writes the
// Excel template of the macro, fills two rows in it, links it by column name and
// replays the macro for both rows. The page must receive the row values, not
// the test tokens.
func TestE2EPlayerPlaceholders(t *testing.T) {
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

	rec := recorder.New(recorder.Options{ID: "e2e-ph", Name: "placeholders"})
	b, err := rectest.Launch(ctx, e2eBrowser(), 91, rec.Hook)
	if err != nil {
		t.Skipf("launch: %v", err)
	}
	pg, err := b.Session.WaitPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u := rectest.NewUser(ctx, b.Session, pg)
	if err := u.Navigate(site.URL("placeholders.html")); err != nil {
		t.Fatal(err)
	}
	u.Wait(800 * time.Millisecond)
	for _, f := range [][2]string{
		{"#email", "{{email}}"}, {"#email2", "{{email}}"}, {"#fullname", "{{Họ tên}}"}, {"#code", "{{code}}"},
		{"#phone", "{{phone}}"}, {"#pw", "{{password}}"}, {"#notes", "Hello {{Họ tên}}"},
	} {
		if err := u.Click(f[0]); err != nil {
			t.Fatal(err)
		}
		if err := u.Type(f[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := u.Click("button[type=submit]"); err != nil {
		t.Fatal(err)
	}
	u.Wait(500 * time.Millisecond)
	if got := len(site.Sinks("placeholders")); got != 1 {
		t.Fatalf("recording session: the page got %d submissions, want 1", got)
	}
	rec.Stop()
	m, err := rec.Macro()
	b.Close()
	if err != nil {
		t.Fatal(err)
	}

	// The template: header = the columns in macro order.
	dir := t.TempDir()
	tpl := filepath.Join(dir, "template.xlsx")
	if err := data.WriteTemplate(m, tpl, "en"); err != nil {
		t.Fatal(err)
	}
	want := []string{"email", "ho_ten", "code", "phone", "password"}
	rows := [][]string{
		{"anna@example.org", "Nguyễn An", "abc123", "0901234567", "pw-anna-1"},
		{"ben@example.org", "Trần Bảo", "xyz789", "0912345678", "pw-ben-22"},
	}
	f, err := excelize.OpenFile(tpl)
	if err != nil {
		t.Fatal(err)
	}
	for r, row := range rows {
		for c, v := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
			_ = f.SetCellValue("Data", cell, v)
		}
	}
	filled := filepath.Join(dir, "filled.xlsx")
	if err := f.SaveAs(filled); err != nil {
		t.Fatal(err)
	}
	f.Close()
	src, err := data.NewXLSX(filled, data.XLSXOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	cols, _ := src.Columns(ctx)
	if !reflect.DeepEqual(cols, want) {
		t.Fatalf("template columns = %v, want %v", cols, want)
	}
	var vars []string
	for _, v := range m.Variables {
		vars = append(vars, v.Name)
	}
	rep := data.Mapping{Vars: vars}.Resolve(cols)
	if !rep.OK() || len(rep.Extra) != 0 {
		t.Fatalf("mapping by name is not automatic: %+v", rep)
	}
	it, err := src.Open(ctx, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()

	for i := 0; ; i++ {
		_, row, ok, err := it.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			if i != len(rows) {
				t.Fatalf("read %d rows, want %d", i, len(rows))
			}
			break
		}
		vals := rep.Apply(row)
		t.Run(fmt.Sprintf("row%d", i+1), func(t *testing.T) {
			site.ResetSinks()
			rb, err := rectest.Launch(ctx, e2eBrowser(), uint64(300+i), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer rb.Close()
			rpg, err := rb.Session.WaitPage(ctx)
			if err != nil {
				t.Fatal(err)
			}
			p := player.New(rb.Session, rpg)
			if err := p.Run(ctx, m, vals); err != nil {
				t.Fatalf("replay: %v", err)
			}
			time.Sleep(300 * time.Millisecond)
			subs := site.Sinks("placeholders")
			if len(subs) != 1 {
				t.Fatalf("the page got %d submissions, want 1", len(subs))
			}
			got := subs[0]
			wantSub := map[string]string{
				"email": vals["email"], "email2": vals["email"], "fullname": vals["ho_ten"], "code": vals["code"],
				"phone": vals["phone"], "pw": vals["password"], "notes": "Hello " + vals["ho_ten"],
			}
			for k, v := range wantSub {
				if fmt.Sprint(got[k]) != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
