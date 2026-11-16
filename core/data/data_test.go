package data

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

var wantCols = []string{"name", "zip", "birthday", "note"}

var wantRows = []Row{
	{"name": "Nguyễn Văn An", "zip": "070000", "birthday": "2024-03-15", "note": "Khách quen"},
	{"name": "Trần Thị Bích", "zip": "100000", "birthday": "1990-12-01", "note": ""},
	{"name": "Lê Hoàng Đức", "zip": "084000", "birthday": "2001-07-09", "note": "Dòng một\nDòng hai"},
	{"name": "Phạm Thị Ơn", "zip": "550000", "birthday": "1985-01-31", "note": `Có "ngoặc kép", dấu phẩy`},
}

// fixture returns the path of a sample file. The UTF-8 BOM sample is built in a
// temporary folder: the BOM is the first bytes of the file, and tools that
// normalise text can strip it from a committed copy. The committed
// sample-bom.csv holds the same rows without the BOM.
func fixture(t *testing.T, name string) string {
	t.Helper()
	if name != "sample-bom.csv" {
		return filepath.Join("testdata", name)
	}
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, append([]byte("\xef\xbb\xbf"), body...), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

var sampleFiles = []string{
	"sample.csv", "sample-bom.csv", "sample-semicolon.csv", "sample-tab.csv",
	"sample-1258.csv", "sample-utf16.csv", "sample.json", "sample-rows.json", "sample.xlsx",
}

func readAll(t *testing.T, src Source, from, to int) (idx []int, rows []Row) {
	t.Helper()
	ctx := context.Background()
	rs, err := src.Open(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	for {
		i, r, ok, err := rs.Next(ctx)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !ok {
			return
		}
		idx = append(idx, i)
		rows = append(rows, r)
	}
}

func TestEveryFormatGivesSameRows(t *testing.T) {
	ctx := context.Background()
	for _, name := range sampleFiles {
		t.Run(name, func(t *testing.T) {
			src, err := OpenFile(fixture(t, name))
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()
			cols, err := src.Columns(ctx)
			if err != nil || !reflect.DeepEqual(cols, wantCols) {
				t.Fatalf("columns = %q, %v", cols, err)
			}
			n, err := src.Count(ctx)
			if err != nil || n != len(wantRows) {
				t.Fatalf("count = %d, %v", n, err)
			}
			idx, rows := readAll(t, src, 0, -1)
			if !reflect.DeepEqual(rows, wantRows) {
				t.Fatalf("rows = %q", rows)
			}
			if !reflect.DeepEqual(idx, []int{0, 1, 2, 3}) {
				t.Fatalf("indexes = %v", idx)
			}
			// Range.
			idx, rows = readAll(t, src, 1, 3)
			if !reflect.DeepEqual(idx, []int{1, 2}) || !reflect.DeepEqual(rows, wantRows[1:3]) {
				t.Fatalf("range gave %v %q", idx, rows)
			}
			idx, _ = readAll(t, src, 3, 99)
			if !reflect.DeepEqual(idx, []int{3}) {
				t.Fatalf("tail range gave %v", idx)
			}
			idx, _ = readAll(t, src, 4, -1)
			if len(idx) != 0 {
				t.Fatalf("past the end gave %v", idx)
			}
		})
	}
}

func TestSniff(t *testing.T) {
	cases := map[string]struct {
		enc   string
		delim rune
	}{
		"sample.csv":           {EncUTF8, ','},
		"sample-bom.csv":       {EncUTF8BOM, ','},
		"sample-semicolon.csv": {EncUTF8, ';'},
		"sample-tab.csv":       {EncUTF8, '\t'},
		"sample-1258.csv":      {EncWindows1258, ','},
		"sample-utf16.csv":     {EncUTF16LE, ','},
	}
	for name, want := range cases {
		enc, d, err := Sniff(fixture(t, name))
		if err != nil || enc != want.enc || d != want.delim {
			t.Errorf("%s: got %s %q %v, want %s %q", name, enc, d, err, want.enc, want.delim)
		}
	}
}

func TestWindows1252Fallback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "w.csv")
	// "café;ñ" in Windows-1252.
	if err := os.WriteFile(p, []byte("a;b\r\ncaf\xe9;\xf1\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewCSV(p, CSVOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Encoding() != EncWindows1252 || s.Delimiter() != ';' {
		t.Fatalf("got %s %q", s.Encoding(), s.Delimiter())
	}
	_, rows := readAll(t, s, 0, -1)
	if rows[0]["a"] != "café" || rows[0]["b"] != "ñ" {
		t.Fatalf("rows = %q", rows)
	}
}

func TestBOMNotInHeader(t *testing.T) {
	s, err := NewCSV(fixture(t, "sample-bom.csv"), CSVOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cols, _ := s.Columns(context.Background())
	if cols[0] != "name" {
		t.Fatalf("first column = %q", cols[0])
	}
}

func TestRaggedRows(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.csv")
	if err := os.WriteFile(p, []byte("a,b,c\n1,2,3\n4,5\n6,7,8,9\n10,11,12\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewCSV(p, CSVOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rs, _ := s.Open(ctx, 0, -1)
	defer rs.Close()
	var got []string
	for {
		i, row, ok, err := rs.Next(ctx)
		if err == nil && !ok {
			break
		}
		if err != nil {
			var ra *RaggedRowError
			if !errors.As(err, &ra) || ra.RowIndex() != i {
				t.Fatalf("error type: %v", err)
			}
			got = append(got, fmt.Sprintf("err%d:%s", i, err))
			continue
		}
		got = append(got, fmt.Sprintf("ok%d:%s", i, row["a"]))
	}
	want := []string{
		"ok0:1",
		"err1:row 2 has 2 cells but the header has 3",
		"err2:row 3 has 4 cells but the header has 3",
		"ok3:10",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	// A range that skips the bad rows does not see them.
	_, rows := readAll(t, s, 3, -1)
	if len(rows) != 1 || rows[0]["a"] != "10" {
		t.Fatalf("rows = %q", rows)
	}
}

func TestJSONNested(t *testing.T) {
	p := filepath.Join(t.TempDir(), "n.json")
	body := `[{"a":"1","b":2,"c":true,"d":null},{"a":"2","b":{"x":1}},{"a":"3","e":"new"}]`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s := NewJSON(p, JSONOptions{})
	cols, err := s.Columns(ctx)
	if err != nil || !reflect.DeepEqual(cols, []string{"a", "b", "c", "d", "e"}) {
		t.Fatalf("cols = %q %v", cols, err)
	}
	rs, _ := s.Open(ctx, 0, -1)
	_, r0, ok, err := rs.Next(ctx)
	if !ok || err != nil || r0["b"] != "2" || r0["c"] != "true" || r0["d"] != "" || r0["e"] != "" {
		t.Fatalf("row 0 = %q %v", r0, err)
	}
	_, _, ok, err = rs.Next(ctx)
	var ne *NestedValueError
	if ok || !errors.As(err, &ne) || ne.Index != 1 || ne.Column != "b" {
		t.Fatalf("want nested error, got %v", err)
	}
	if _, r2, ok, err := rs.Next(ctx); !ok || err != nil || r2["e"] != "new" {
		t.Fatalf("row 2 = %q %v", r2, err)
	}
	rs.Close()

	s2 := NewJSON(p, JSONOptions{Nested: NestedJSON})
	_, rows := readAll(t, s2, 1, 2)
	if rows[0]["b"] != `{"x":1}` {
		t.Fatalf("flattened = %q", rows[0]["b"])
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(`{"data": []}`), 0o644)
	if _, err := NewJSON(bad, JSONOptions{}).Columns(ctx); err == nil || !strings.Contains(err.Error(), `"rows"`) {
		t.Fatalf("want rows error, got %v", err)
	}
}

func TestXLSXSheets(t *testing.T) {
	ctx := context.Background()
	s, err := NewXLSX("testdata/sample.xlsx", XLSXOptions{Sheet: "Khac"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, rows := readAll(t, s, 0, -1)
	if len(rows) != 1 || rows[0]["other"] != "x" {
		t.Fatalf("rows = %q", rows)
	}
	s2, err := NewXLSX("testdata/sample.xlsx", XLSXOptions{SheetIndex: 1})
	if err != nil || s2.Sheet() != "Khac" {
		t.Fatalf("index pick: %v", err)
	}
	s2.Close()
	if _, err := NewXLSX("testdata/sample.xlsx", XLSXOptions{Sheet: "Nope"}); err == nil || !strings.Contains(err.Error(), "Khach, Khac") {
		t.Fatalf("want sheet list in error, got %v", err)
	}
	if _, err := NewXLSX("testdata/sample.xlsx", XLSXOptions{SheetIndex: 9}); err == nil {
		t.Fatal("want range error")
	}
	_ = ctx
}

func TestContextCancelStopsNext(t *testing.T) {
	for _, name := range []string{"sample.csv", "sample.json", "sample.xlsx"} {
		src, err := OpenFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		rs, err := src.Open(ctx, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, ok, err := rs.Next(ctx); !ok || err != nil {
			t.Fatalf("%s: first Next: %v", name, err)
		}
		cancel()
		if _, _, ok, err := rs.Next(ctx); ok || !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: after cancel got ok=%v err=%v", name, ok, err)
		}
		rs.Close()
		src.Close()
	}
}

func TestCSV200kStreams(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.csv")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, "id,name,zip,note")
	const n = 200_000
	for i := 0; i < n; i++ {
		fmt.Fprintf(f, "%d,Nguyễn Văn %d,%06d,\"ghi chú, số %d với một chuỗi khá dài để file lớn hơn\"\n", i, i, i, i)
	}
	f.Close()
	st, _ := os.Stat(p)

	ctx := context.Background()
	s, err := NewCSV(p, CSVOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cnt, err := s.Count(ctx)
	if err != nil || cnt != n {
		t.Fatalf("count = %d %v", cnt, err)
	}
	runtime.GC()
	var base, ms runtime.MemStats
	runtime.ReadMemStats(&base)
	rs, err := s.Open(ctx, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var peak uint64
	got := 0
	for {
		i, row, ok, err := rs.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		if i != got || row["zip"] != fmt.Sprintf("%06d", i) {
			t.Fatalf("row %d wrong: %q", i, row)
		}
		got++
		if got%10000 == 0 {
			runtime.GC()
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > base.HeapAlloc && ms.HeapAlloc-base.HeapAlloc > peak {
				peak = ms.HeapAlloc - base.HeapAlloc
			}
		}
	}
	if got != n {
		t.Fatalf("read %d rows", got)
	}
	t.Logf("file %.1f MB, live heap growth while streaming %d rows: %.2f MB", float64(st.Size())/1e6, n, float64(peak)/1e6)
	if peak > 8<<20 {
		t.Fatalf("heap grew by %d bytes; the reader keeps rows", peak)
	}
}

// ---- REST ----

const secret = "s3cr3t-token-value"

func restServer(t *testing.T, total int) *httptest.Server {
	t.Helper()
	item := func(i int) string {
		return fmt.Sprintf(`{"id":%d,"name":"Nguyễn %d","zip":"%06d"}`, i, i, i)
	}
	items := func(from, to int) string {
		var parts []string
		for i := from; i < to && i < total; i++ {
			parts = append(parts, item(i))
		}
		return strings.Join(parts, ",")
	}
	atoi := func(s string, d int) int {
		var n int
		if _, err := fmt.Sscan(s, &n); err != nil {
			return d
		}
		return n
	}
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "nope "+r.Header.Get("Authorization"), http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		p, size := atoi(r.URL.Query().Get("page"), 1), atoi(r.URL.Query().Get("per_page"), 10)
		fmt.Fprintf(w, `{"data":{"items":[%s]}}`, items((p-1)*size, p*size))
	})
	mux.HandleFunc("/offset", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		o, l := atoi(r.URL.Query().Get("offset"), 0), atoi(r.URL.Query().Get("limit"), 10)
		fmt.Fprintf(w, `[%s]`, items(o, o+l))
	})
	mux.HandleFunc("/link", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		c := atoi(r.URL.Query().Get("cursor"), 0)
		next := ""
		if c+7 < total {
			next = fmt.Sprintf("/link?cursor=%d", c+7)
		}
		fmt.Fprintf(w, `{"rows":[%s],"meta":{"next":%q}}`, items(c, c+7), next)
	})
	mux.HandleFunc("/linkhdr", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		c := atoi(r.URL.Query().Get("cursor"), 0)
		if c+7 < total {
			w.Header().Set("Link", fmt.Sprintf(`</linkhdr?cursor=%d>; rel="next"`, c+7))
		}
		fmt.Fprintf(w, `[%s]`, items(c, c+7))
	})
	mux.HandleFunc("/single", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		fmt.Fprintf(w, `[%s]`, items(0, total))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func collectIDs(t *testing.T, s Source, from, to int) []string {
	t.Helper()
	_, rows := readAll(t, s, from, to)
	var ids []string
	for _, r := range rows {
		ids = append(ids, r["id"])
	}
	return ids
}

func seq(from, to int) []string {
	var out []string
	for i := from; i < to; i++ {
		out = append(out, fmt.Sprint(i))
	}
	return out
}

func TestRESTPaging(t *testing.T) {
	srv := restServer(t, 25)
	auth := RESTAuth{Bearer: secret}
	cases := map[string]RESTConfig{
		"page":   {URL: srv.URL + "/page", Auth: auth, Path: "$.data.items", Paging: PagingPage, PageSize: 10},
		"offset": {URL: srv.URL + "/offset", Auth: auth, Paging: PagingOffset, PageSize: 10},
		"link":   {URL: srv.URL + "/link", Auth: auth, Path: "rows", Paging: PagingLink, NextPath: "meta.next"},
		"header": {URL: srv.URL + "/linkhdr", Auth: auth, Paging: PagingLink},
		"none":   {URL: srv.URL + "/single", Auth: auth},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := NewREST(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			cols, err := s.Columns(context.Background())
			if err != nil || !reflect.DeepEqual(cols, []string{"id", "name", "zip"}) {
				t.Fatalf("cols = %q %v", cols, err)
			}
			if n, _ := s.Count(context.Background()); n != -1 {
				t.Fatalf("count = %d", n)
			}
			if got := collectIDs(t, s, 0, -1); !reflect.DeepEqual(got, seq(0, 25)) {
				t.Fatalf("ids = %v", got)
			}
			if got := collectIDs(t, s, 12, 17); !reflect.DeepEqual(got, seq(12, 17)) {
				t.Fatalf("range ids = %v", got)
			}
		})
	}
}

func TestRESTMaxRowsAndRate(t *testing.T) {
	srv := restServer(t, 25)
	s, err := NewREST(RESTConfig{
		URL: srv.URL + "/offset", Auth: RESTAuth{Bearer: secret}, Paging: PagingOffset, PageSize: 10,
		MaxRows: 14, MinInterval: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if got := collectIDs(t, s, 0, -1); !reflect.DeepEqual(got, seq(0, 14)) {
		t.Fatalf("ids = %v", got)
	}
	// Two pages are needed for 14 rows, so at least one gap of 40 ms.
	if d := time.Since(start); d < 40*time.Millisecond {
		t.Fatalf("rate limit ignored: %v", d)
	}
}

func TestRESTAuthHeadersAndNoSecretInErrors(t *testing.T) {
	var gotBasic, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		gotBasic = fmt.Sprint(ok, u, p)
		gotKey = r.Header.Get("X-Api-Key")
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer srv.Close()
	s, err := NewREST(RESTConfig{
		URL:     srv.URL + "/x?token=" + secret,
		Headers: map[string]string{"X-Api-Key": secret},
		Auth:    RESTAuth{BasicUser: "bob", BasicPass: secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Columns(context.Background())
	if err == nil {
		t.Fatal("want HTTP error")
	}
	if gotBasic != "truebob"+secret || gotKey != secret {
		t.Fatalf("auth not sent: %q %q", gotBasic, gotKey)
	}
	if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("bad error text: %v", err)
	}
	// Connection error: the URL query must not leak either.
	srv.Close()
	s2, _ := NewREST(RESTConfig{URL: srv.URL + "/x?token=" + secret, Auth: RESTAuth{Bearer: secret}, Timeout: time.Second})
	if _, err := s2.Columns(context.Background()); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("bad error text: %v", err)
	}
}

func TestRESTRefusals(t *testing.T) {
	if _, err := NewREST(RESTConfig{URL: "http://example.com/api"}); err == nil {
		t.Fatal("plain HTTP to a remote host must be refused")
	}
	if _, err := NewREST(RESTConfig{URL: "http://example.com/api", AllowHTTP: true}); err != nil {
		t.Fatal(err)
	}
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer redir.Close()
	s, _ := NewREST(RESTConfig{URL: redir.URL})
	if _, err := s.Columns(context.Background()); err == nil || !strings.Contains(err.Error(), "another host") {
		t.Fatalf("want redirect refusal, got %v", err)
	}
}

func TestRESTContextCancel(t *testing.T) {
	srv := restServer(t, 100)
	s, _ := NewREST(RESTConfig{URL: srv.URL + "/offset", Auth: RESTAuth{Bearer: secret}, Paging: PagingOffset, PageSize: 10})
	ctx, cancel := context.WithCancel(context.Background())
	rs, err := s.Open(ctx, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := rs.Next(ctx); !ok || err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, _, ok, err := rs.Next(ctx); ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("got ok=%v err=%v", ok, err)
	}
}

// ---- Mapping ----

func TestMapping(t *testing.T) {
	m := Mapping{
		Vars:      []string{"Name", "zip_code", "email", "Phone"},
		Overrides: map[string]string{"zip_code": "ZIP"},
		Required:  []string{"Name", "zip_code"},
	}
	rep := m.Resolve([]string{" name ", "ZIP", "birthday", "Zip", "note"})
	if !reflect.DeepEqual(rep.Bound, map[string]string{"Name": " name ", "zip_code": "ZIP"}) {
		t.Fatalf("bound = %q", rep.Bound)
	}
	if !reflect.DeepEqual(rep.Missing, []string{"email", "Phone"}) {
		t.Fatalf("missing = %q", rep.Missing)
	}
	if !reflect.DeepEqual(rep.Extra, []string{"birthday", "note"}) {
		t.Fatalf("extra = %q", rep.Extra)
	}
	if !reflect.DeepEqual(rep.Duplicates, []string{"Zip"}) {
		t.Fatalf("duplicates = %q", rep.Duplicates)
	}
	if rep.OK() {
		t.Fatal("report must not be OK")
	}
	if !(Mapping{Vars: []string{"NAME"}}).Resolve([]string{"name"}).OK() {
		t.Fatal("case-insensitive match must be OK")
	}
	// An override that points at a missing column leaves the variable missing.
	r2 := Mapping{Vars: []string{"a"}, Overrides: map[string]string{"a": "zzz"}}.Resolve([]string{"a"})
	if !reflect.DeepEqual(r2.Missing, []string{"a"}) {
		t.Fatalf("missing = %q", r2.Missing)
	}
}

func TestRequiredValidator(t *testing.T) {
	ctx := context.Background()
	src, _ := OpenFile("testdata/sample.csv")
	m := Mapping{Vars: []string{"name", "note"}, Required: []string{"note"}}
	rep, err := m.ResolveSource(ctx, src)
	if err != nil || !rep.OK() {
		t.Fatalf("%v %+v", err, rep)
	}
	raw, _ := src.Open(ctx, 0, -1)
	rs := Validated(raw, m.RequiredValidator(rep))
	defer rs.Close()
	var okIdx, badIdx []int
	for {
		i, row, ok, err := rs.Next(ctx)
		if err == nil && !ok {
			break
		}
		if err != nil {
			var ve *RowValidationError
			if !errors.As(err, &ve) || ve.Variable != "note" || ve.Column != "note" {
				t.Fatalf("err = %v", err)
			}
			badIdx = append(badIdx, i)
			continue
		}
		if rep.Apply(row)["name"] == "" {
			t.Fatal("apply lost the name")
		}
		okIdx = append(okIdx, i)
	}
	if !reflect.DeepEqual(okIdx, []int{0, 2, 3}) || !reflect.DeepEqual(badIdx, []int{1}) {
		t.Fatalf("ok=%v bad=%v", okIdx, badIdx)
	}
}
