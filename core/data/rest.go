package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PagingMode selects how a REST source asks for more rows.
type PagingMode string

const (
	PagingNone   PagingMode = ""       // one request
	PagingPage   PagingMode = "page"   // ?page=N (and ?size=M when PageSize is set)
	PagingOffset PagingMode = "offset" // ?offset=N&limit=M
	PagingLink   PagingMode = "link"   // follow a next link in the body or the Link header
)

// Limits for REST sources.
const (
	DefaultRESTTimeout = 30 * time.Second
	DefaultMaxRows     = 1_000_000
	maxPageBytes       = 64 << 20
)

// RESTAuth holds credentials. Values never appear in errors or logs.
type RESTAuth struct {
	Bearer    string
	BasicUser string
	BasicPass string
}

// RESTConfig describes a REST data source. Only GET is supported.
type RESTConfig struct {
	URL     string
	Headers map[string]string
	Auth    RESTAuth
	// Path is a JSONPath-lite route to the array of rows: "$.data.items",
	// "data.items[0].rows" or "" when the body is the array.
	Path string

	Paging      PagingMode
	PageParam   string // default "page"
	SizeParam   string // default "limit" for offset paging, "per_page" for page paging
	OffsetParam string // default "offset"
	PageSize    int    // rows per request; 0 means the server decides
	StartPage   int    // first page number; default 1
	NextPath    string // JSONPath-lite route to the next link in the body (link paging)

	MinInterval time.Duration // minimum time between two requests
	Timeout     time.Duration // per request; default 30 s
	MaxRows     int           // default 1,000,000
	Nested      NestedMode
	AllowHTTP   bool // allow plain HTTP to a host that is not loopback
}

type pageResult struct {
	items []json.RawMessage
	next  string
}

// RESTSource reads rows from a JSON REST endpoint, page by page.
type RESTSource struct {
	cfg    RESTConfig
	base   *url.URL
	client *http.Client
	segs   []pathSeg
	nsegs  []pathSeg

	mu     sync.Mutex
	last   time.Time
	first  *pageResult
	cols   []string
	closed bool
}

// NewREST validates the config and builds a source. It sends no request.
func NewREST(cfg RESTConfig) (*RESTSource, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Host == "" {
		return nil, errors.New("the REST URL is not valid")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("the REST URL scheme %q is not supported; use https", u.Scheme)
	}
	if u.Scheme == "http" && !cfg.AllowHTTP && !isLoopback(u.Hostname()) {
		return nil, errors.New("plain HTTP is refused for this host; use https or allow HTTP explicitly")
	}
	segs, err := parsePath(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("rows path: %w", err)
	}
	nsegs, err := parsePath(cfg.NextPath)
	if err != nil {
		return nil, fmt.Errorf("next path: %w", err)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultRESTTimeout
	}
	if cfg.MaxRows <= 0 {
		cfg.MaxRows = DefaultMaxRows
	}
	if cfg.StartPage == 0 {
		cfg.StartPage = 1
	}
	if cfg.PageParam == "" {
		cfg.PageParam = "page"
	}
	if cfg.OffsetParam == "" {
		cfg.OffsetParam = "offset"
	}
	if cfg.SizeParam == "" {
		if cfg.Paging == PagingOffset {
			cfg.SizeParam = "limit"
		} else {
			cfg.SizeParam = "per_page"
		}
	}
	host := u.Host
	client := &http.Client{
		Timeout: cfg.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != host {
				return errors.New("the server redirected to another host; this is refused")
			}
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
	return &RESTSource{cfg: cfg, base: u, client: client, segs: segs, nsegs: nsegs}, nil
}

func isLoopback(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// safeURL drops the query and the user info, which may hold secrets.
func safeURL(u *url.URL) string {
	return u.Scheme + "://" + u.Host + u.Path
}

func (s *RESTSource) wait(ctx context.Context) error {
	if s.cfg.MinInterval <= 0 {
		return ctx.Err()
	}
	s.mu.Lock()
	d := time.Until(s.last.Add(s.cfg.MinInterval))
	s.last = time.Now()
	if d > 0 {
		s.last = s.last.Add(d)
	}
	s.mu.Unlock()
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// fetch requests one page.
func (s *RESTSource) fetch(ctx context.Context, u *url.URL) (*pageResult, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("cannot build the REST request")
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range s.cfg.Headers {
		req.Header.Set(k, v)
	}
	switch {
	case s.cfg.Auth.Bearer != "":
		req.Header.Set("Authorization", "Bearer "+s.cfg.Auth.Bearer)
	case s.cfg.Auth.BasicUser != "" || s.cfg.Auth.BasicPass != "":
		req.SetBasicAuth(s.cfg.Auth.BasicUser, s.cfg.Auth.BasicPass)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("REST request to %s failed: %w", safeURL(u), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("REST request to %s failed: HTTP %d", safeURL(u), resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read the REST response from %s: %w", safeURL(u), err)
	}
	if len(body) > maxPageBytes {
		return nil, fmt.Errorf("the REST response from %s is larger than %d MB", safeURL(u), maxPageBytes>>20)
	}
	body = bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF})
	arr, err := navigate(body, s.segs)
	if err != nil {
		return nil, fmt.Errorf("REST response from %s: rows path %q: %w", safeURL(u), s.cfg.Path, err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(arr, &items); err != nil {
		return nil, fmt.Errorf("REST response from %s: the value at rows path %q is not an array", safeURL(u), s.cfg.Path)
	}
	res := &pageResult{items: items}
	if s.cfg.Paging == PagingLink {
		res.next = s.nextLink(u, resp.Header, body)
	}
	return res, nil
}

// nextLink finds the next URL in the body or in the Link header.
// A link to another host is dropped so credentials stay on one host.
func (s *RESTSource) nextLink(cur *url.URL, h http.Header, body []byte) string {
	var link string
	if len(s.nsegs) > 0 {
		if raw, err := navigate(body, s.nsegs); err == nil {
			_ = json.Unmarshal(raw, &link)
		}
	} else {
		link = linkHeaderNext(h.Values("Link"))
	}
	if link == "" {
		return ""
	}
	nu, err := cur.Parse(link)
	if err != nil || nu.Host != s.base.Host || nu.String() == cur.String() {
		return ""
	}
	return nu.String()
}

func linkHeaderNext(values []string) string {
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			bits := strings.Split(part, ";")
			if len(bits) < 2 {
				continue
			}
			for _, b := range bits[1:] {
				b = strings.ReplaceAll(strings.TrimSpace(b), " ", "")
				if b == `rel="next"` || b == "rel=next" {
					t := strings.TrimSpace(bits[0])
					return strings.TrimSuffix(strings.TrimPrefix(t, "<"), ">")
				}
			}
		}
	}
	return ""
}

func (s *RESTSource) pageURL(page, offset int) *url.URL {
	u := *s.base
	q := u.Query()
	switch s.cfg.Paging {
	case PagingPage:
		q.Set(s.cfg.PageParam, strconv.Itoa(page))
		if s.cfg.PageSize > 0 {
			q.Set(s.cfg.SizeParam, strconv.Itoa(s.cfg.PageSize))
		}
	case PagingOffset:
		q.Set(s.cfg.OffsetParam, strconv.Itoa(offset))
		if s.cfg.PageSize > 0 {
			q.Set(s.cfg.SizeParam, strconv.Itoa(s.cfg.PageSize))
		}
	case PagingLink:
		if s.cfg.PageSize > 0 {
			q.Set(s.cfg.SizeParam, strconv.Itoa(s.cfg.PageSize))
		}
	}
	u.RawQuery = q.Encode()
	return &u
}

func (s *RESTSource) ensureFirst(ctx context.Context) (*pageResult, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if s.first != nil {
		defer s.mu.Unlock()
		return s.first, nil
	}
	s.mu.Unlock()
	res, err := s.fetch(ctx, s.pageURL(s.cfg.StartPage, 0))
	if err != nil {
		return nil, err
	}
	var cols columnSet
	for i, raw := range res.items {
		obj, err := parseObject(raw)
		if err != nil {
			continue
		}
		_, _ = objectToRow(i, obj, s.cfg.Nested, &cols)
	}
	s.mu.Lock()
	s.first, s.cols = res, cols.order
	s.mu.Unlock()
	return res, nil
}

// Columns returns the keys of the objects on the first page, in order of first appearance.
func (s *RESTSource) Columns(ctx context.Context) ([]string, error) {
	if _, err := s.ensureFirst(ctx); err != nil {
		return nil, err
	}
	return append([]string(nil), s.cols...), nil
}

// Count returns -1: a REST source does not tell the total.
func (s *RESTSource) Count(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrClosed
	}
	return -1, nil
}

type restIter struct {
	s       *RESTSource
	cur     *pageResult
	pos     int
	page    int
	offset  int
	index   int
	started bool
	done    bool
}

func (it *restIter) advance(ctx context.Context) error {
	s := it.s
	if !it.started {
		first, err := s.ensureFirst(ctx)
		if err != nil {
			return err
		}
		it.started, it.cur, it.pos = true, first, 0
		return it.afterPage()
	}
	var u *url.URL
	if s.cfg.Paging == PagingLink {
		nu, err := url.Parse(it.cur.next)
		if err != nil {
			return errors.New("the next link is not valid")
		}
		u = nu
	} else {
		u = s.pageURL(it.page, it.offset)
	}
	res, err := s.fetch(ctx, u)
	if err != nil {
		return err
	}
	it.cur, it.pos = res, 0
	return it.afterPage()
}

// afterPage decides whether another page follows the one just loaded.
func (it *restIter) afterPage() error {
	cfg := it.s.cfg
	n := len(it.cur.items)
	switch cfg.Paging {
	case PagingNone:
		it.done = true
	case PagingPage:
		it.page = pageAfter(it.page, cfg.StartPage)
		if n == 0 || (cfg.PageSize > 0 && n < cfg.PageSize) {
			it.done = true
		}
	case PagingOffset:
		it.offset += n
		if n == 0 || (cfg.PageSize > 0 && n < cfg.PageSize) {
			it.done = true
		}
	case PagingLink:
		if it.cur.next == "" {
			it.done = true
		}
	}
	return nil
}

func pageAfter(cur, start int) int {
	if cur < start {
		return start + 1
	}
	return cur + 1
}

func (it *restIter) next(ctx context.Context) (int, Row, bool, error) {
	for it.cur == nil || it.pos >= len(it.cur.items) {
		if it.started && it.done {
			return 0, nil, false, nil
		}
		if err := it.advance(ctx); err != nil {
			return 0, nil, false, err
		}
	}
	if it.index >= it.s.cfg.MaxRows {
		it.done = true
		it.cur = &pageResult{}
		return 0, nil, false, nil
	}
	raw := it.cur.items[it.pos]
	it.pos++
	idx := it.index
	it.index++
	obj, err := parseObject(raw)
	if err != nil {
		if errors.Is(err, errNotObject) {
			return idx, nil, false, &NotObjectError{Index: idx}
		}
		return 0, nil, false, fmt.Errorf("REST row %d is not valid JSON: %v", idx+1, err)
	}
	var cols columnSet
	row, rerr := objectToRow(idx, obj, it.s.cfg.Nested, &cols)
	if rerr != nil {
		return idx, nil, false, rerr
	}
	for _, c := range it.s.cols {
		if _, ok := row[c]; !ok {
			row[c] = ""
		}
	}
	return idx, row, true, nil
}

func (it *restIter) close() error { return nil }

// Open returns a cursor over rows [from,to). Rows before from are fetched and dropped.
func (s *RESTSource) Open(ctx context.Context, from, to int) (Rows, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	it := &restIter{s: s, page: s.cfg.StartPage}
	return newRange(it, from, to), nil
}

// Close marks the source closed.
func (s *RESTSource) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

// ---- JSONPath-lite ----

type pathSeg struct {
	name  string
	index int // -1 for a name
}

// parsePath reads "$.a.b[0].c". An empty path or "$" is the root.
func parsePath(p string) ([]pathSeg, error) {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "$")
	p = strings.TrimPrefix(p, ".")
	if p == "" {
		return nil, nil
	}
	var segs []pathSeg
	for _, part := range strings.Split(p, ".") {
		if part == "" {
			return nil, errors.New("the path has an empty segment")
		}
		name := part
		var idxs []int
		if i := strings.IndexByte(part, '['); i >= 0 {
			name = part[:i]
			rest := part[i:]
			for rest != "" {
				end := strings.IndexByte(rest, ']')
				if rest[0] != '[' || end < 0 {
					return nil, fmt.Errorf("bad index in %q", part)
				}
				n, err := strconv.Atoi(rest[1:end])
				if err != nil || n < 0 {
					return nil, fmt.Errorf("bad index in %q", part)
				}
				idxs = append(idxs, n)
				rest = rest[end+1:]
			}
		}
		if name != "" {
			segs = append(segs, pathSeg{name: name, index: -1})
		}
		for _, n := range idxs {
			segs = append(segs, pathSeg{index: n})
		}
	}
	return segs, nil
}

// navigate follows segs inside a JSON document and returns the raw value.
func navigate(doc []byte, segs []pathSeg) (json.RawMessage, error) {
	cur := json.RawMessage(bytes.TrimSpace(doc))
	if len(cur) == 0 {
		return nil, errors.New("the response is empty")
	}
	for _, sg := range segs {
		if sg.index < 0 {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(cur, &obj); err != nil {
				return nil, fmt.Errorf("expected an object before %q", sg.name)
			}
			v, ok := obj[sg.name]
			if !ok {
				return nil, fmt.Errorf("key %q not found", sg.name)
			}
			cur = v
			continue
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(cur, &arr); err != nil {
			return nil, fmt.Errorf("expected an array before [%d]", sg.index)
		}
		if sg.index >= len(arr) {
			return nil, fmt.Errorf("index [%d] is out of range", sg.index)
		}
		cur = arr[sg.index]
	}
	return cur, nil
}
