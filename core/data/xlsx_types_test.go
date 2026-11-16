package data

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func style(t *testing.T, f *excelize.File, s *excelize.Style) int {
	t.Helper()
	id, err := f.NewStyle(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func typesWorkbook(t *testing.T, date1904 bool) string {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sh := "Sheet1"
	for i, h := range []string{"d14", "dcustom", "dt", "tm", "tms", "money", "text", "f_calc", "f_bad", "after_gap", "k", "textime"} {
		c, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sh, c, h)
	}
	cus := func(code string) int { return style(t, f, &excelize.Style{CustomNumFmt: &code}) }
	n14 := 14
	sD14 := style(t, f, &excelize.Style{NumFmt: n14})
	sDC := cus("dd/mm/yyyy")
	sDT := cus("yyyy-mm-dd hh:mm")
	sT := cus("hh:mm")
	sTS := cus("hh:mm:ss")
	sM := cus(`#,##0 "₫"`)
	sTxt := style(t, f, &excelize.Style{NumFmt: 49})
	// serial 31048 = 1985-01-01 in the 1900 system.
	serial := 31048.0
	if date1904 {
		serial -= 1462
	}
	f.SetCellValue(sh, "A2", serial)
	f.SetCellStyle(sh, "A2", "A2", sD14)
	f.SetCellValue(sh, "B2", serial)
	f.SetCellStyle(sh, "B2", "B2", sDC)
	f.SetCellValue(sh, "C2", serial+14.5/24+30.0/1440-30.0/1440)
	f.SetCellStyle(sh, "C2", "C2", sDT)
	f.SetCellValue(sh, "D2", 9.25/24)
	f.SetCellStyle(sh, "D2", "D2", sT)
	f.SetCellValue(sh, "E2", 9.25/24+30.0/86400)
	f.SetCellStyle(sh, "E2", "E2", sTS)
	f.SetCellValue(sh, "F2", 1500000)
	f.SetCellStyle(sh, "F2", "F2", sM)
	f.SetCellValue(sh, "G2", "070000")
	f.SetCellStyle(sh, "G2", "G2", sTxt)
	f.SetCellValue(sh, "K2", 2)
	f.SetCellValue(sh, "L2", "1899-12-30T09:15:00Z")
	f.SetCellStyle(sh, "L2", "L2", sT)
	f.SetCellFormula(sh, "H2", "K2*21")
	f.SetCellFormula(sh, "I2", "NOSUCHFUNC(K2)")
	// row 3 is skipped on purpose: a gap must not shift cell addresses.
	f.SetCellValue(sh, "J4", "x")
	f.SetCellValue(sh, "A4", serial)
	f.SetCellStyle(sh, "A4", "A4", sD14)
	f.SetCellFormula(sh, "H4", "K2+1")
	if date1904 {
		f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: func() *bool { b := true; return &b }()})
	}
	p := filepath.Join(t.TempDir(), "types.xlsx")
	if err := f.SaveAs(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestXLSXTypedCells(t *testing.T) {
	for _, d1904 := range []bool{false, true} {
		p := typesWorkbook(t, d1904)
		s, err := NewXLSX(p, XLSXOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, rows := readAll(t, s, 0, -1)
		if len(rows) != 2 {
			t.Fatalf("1904=%v rows = %d", d1904, len(rows))
		}
		r := rows[0]
		want := map[string]string{
			"d14": "1985-01-01", "dcustom": "1985-01-01", "tm": "09:15", "textime": "09:15",
			"tms": "09:15:30", "money": "1,500,000 ₫", "text": "070000", "f_calc": "42", "f_bad": "",
		}
		for k, v := range want {
			if r[k] != v {
				t.Errorf("1904=%v %s = %q, want %q", d1904, k, r[k], v)
			}
		}
		if r["dt"] != "1985-01-01T14:30" {
			t.Errorf("dt = %q", r["dt"])
		}
		if rows[1]["d14"] != "1985-01-01" || rows[1]["f_calc"] != "3" || rows[1]["after_gap"] != "x" {
			t.Errorf("row after gap = %v", rows[1])
		}
		w := s.Warnings()
		if len(w) != 1 || w[0].Cell != "I2" || w[0].Column != "f_bad" {
			t.Errorf("warnings = %v", w)
		}
		s.Close()
	}
}

func TestXLSXDateDisplayMode(t *testing.T) {
	s, err := NewXLSX(typesWorkbook(t, false), XLSXOptions{DateFormat: DateDisplay})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, rr := readAll(t, s, 0, -1)
	r := rr[0]
	if r["dcustom"] != "01/01/1985" {
		t.Errorf("display date = %q", r["dcustom"])
	}
	if r["tm"] != "09:15" || r["dt"] != "1985-01-01T14:30" {
		t.Errorf("time and datetime stay normalized: %q %q", r["tm"], r["dt"])
	}
	if _, err := NewXLSX("testdata/sample.xlsx", XLSXOptions{DateFormat: "bogus"}); err == nil {
		t.Error("bogus date format accepted")
	}
}

func TestClassifyCustom(t *testing.T) {
	cases := []struct {
		code string
		kind cellKind
		sec  bool
	}{
		{"dd/mm/yyyy", kindDate, false}, {"mmm-yy", kindDate, false}, {"yyyy-mm-dd hh:mm:ss", kindDateTime, true},
		{"h:mm AM/PM", kindTime, false}, {"mm:ss", kindTime, true}, {"[h]:mm:ss", kindOther, false},
		{`0.00" days"`, kindOther, false}, {`#,##0 "₫"`, kindOther, false}, {"0.0%", kindOther, false}, {"General", kindOther, false},
		{`"day" 0`, kindOther, false},
	}
	for _, c := range cases {
		k, sec := classifyCustom(c.code)
		if k != c.kind || sec != c.sec {
			t.Errorf("%q = %v,%v want %v,%v", c.code, k, sec, c.kind, c.sec)
		}
	}
}
