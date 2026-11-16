package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/8bu/ladilala/core/profile"
	"github.com/8bu/ladilala/core/proxy"
	"github.com/8bu/ladilala/core/store"
)

// ProxyDTO is the proxy the UI shows. It has hasPassword, never the password.
type ProxyDTO struct {
	ID          string   `json:"id"`
	Scheme      string   `json:"scheme"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Username    string   `json:"username"`
	HasPassword bool     `json:"hasPassword"`
	Country     string   `json:"country"`
	Timezone    string   `json:"timezone"`
	Language    string   `json:"language"`
	Status      string   `json:"status"` // ok | down | checking | untested
	LatencyMs   int      `json:"latencyMs,omitempty"`
	IP          string   `json:"ip,omitempty"`
	UsedBy      []string `json:"usedBy"`
}

// ProxyInput is the data of a new or edited proxy. Password is write-only:
// nil keeps the stored password on update, a pointer to "" removes it.
type ProxyInput struct {
	Scheme   string  `json:"scheme"`
	Host     string  `json:"host"`
	Port     int     `json:"port"`
	Username string  `json:"username"`
	Password *string `json:"password"`
}

// ProxyTestInput names a saved proxy (ID) or gives an unsaved one. Given
// fields override the saved values. An empty password uses the saved one.
type ProxyTestInput struct {
	ID       string `json:"id"`
	Scheme   string `json:"scheme"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// ProxyCheckResult is the outcome of one test. A failed test is a normal
// result (ok false, code set), not a method error.
type ProxyCheckResult struct {
	OK        bool   `json:"ok"`
	Status    string `json:"status"`
	IP        string `json:"ip,omitempty"`
	LatencyMs int    `json:"latencyMs,omitempty"`
	Country   string `json:"country,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
	Language  string `json:"language,omitempty"`
	Code      string `json:"code,omitempty"`    // proxy.down | proxy.auth
	Message   string `json:"message,omitempty"` // English, for logs
}

// ProxyImportSkip is one line the import did not take. It holds no part of
// the line, because a line can hold a password.
type ProxyImportSkip struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"` // format | scheme | host | port | duplicate
}

// ProxyImportResult is the answer of ImportProxies.
type ProxyImportResult struct {
	Added   int               `json:"added"`
	Skipped []ProxyImportSkip `json:"skipped"`
}

// ProxyStatusEvent is the payload of the proxy.status event.
type ProxyStatusEvent struct {
	ProxyID   string `json:"proxyId"`
	Status    string `json:"status"`
	LatencyMs int    `json:"latencyMs,omitempty"`
	IP        string `json:"ip,omitempty"`
	Country   string `json:"country,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
	Language  string `json:"language,omitempty"`
}

// ProxyLookup is the setting of the two public lookup services the proxy test
// uses. See docs/proxy-lookup.md for what each service sees.
type ProxyLookup struct {
	// IPURL answers with the caller IP as plain text.
	IPURL string `json:"ipUrl"`
	// GeoURL answers with JSON that holds a country code and a timezone.
	// {ip} is replaced by the exit IP. Empty turns the geo lookup off.
	GeoURL string `json:"geoUrl"`
}

const (
	defaultIPURL  = "https://api.ipify.org"
	defaultGeoURL = "https://ipwho.is/{ip}"
	lookupFile    = "proxy-lookup.json"
	statusFile    = "proxy-status.json"
)

// ---- prober ------------------------------------------------------------------

// proxyProber tests a proxy through a relay: exit IP, latency and location.
type proxyProber struct {
	dataDir string
	timeout time.Duration
}

func newProxyProber(dataDir string) *proxyProber {
	return &proxyProber{dataDir: dataDir, timeout: 25 * time.Second}
}

func (p *proxyProber) lookup() ProxyLookup {
	l := ProxyLookup{IPURL: defaultIPURL, GeoURL: defaultGeoURL}
	if b, err := os.ReadFile(filepath.Join(p.dataDir, lookupFile)); err == nil {
		var f ProxyLookup
		if json.Unmarshal(b, &f) == nil && f.IPURL != "" {
			l = f
		}
	}
	return l
}

// probe sends the checks through up. On failure it returns the partial result
// and an error that proxyToAPIError maps to proxy.auth or proxy.down.
func (p *proxyProber) probe(ctx context.Context, up profile.Proxy) (ProxyCheckResult, error) {
	cfg := p.lookup()
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	relay, err := proxy.New(up, proxy.WithCheckURL(cfg.IPURL), proxy.WithDialTimeout(p.timeout/3))
	if err != nil {
		return ProxyCheckResult{Status: "down", Code: "invalid", Message: err.Error()}, err
	}
	addr, err := relay.Start(ctx)
	if err != nil {
		return ProxyCheckResult{Status: "down", Message: err.Error()}, err
	}
	defer relay.Close()
	cr, err := relay.Check(ctx)
	res := ProxyCheckResult{IP: cr.IP, LatencyMs: int(cr.Latency / time.Millisecond)}
	if err != nil {
		res.Status = "down"
		res.Code = proxyToAPIError(err).(*APIError).Code
		if strings.Contains(relay.Stats().LastError, proxy.ErrUpstreamAuth.Error()) {
			res.Code = "proxy.auth"
			err = fmt.Errorf("%w (%v)", proxy.ErrUpstreamAuth, err)
		}
		res.Message = err.Error()
		return res, err
	}
	res.OK, res.Status = true, "ok"
	if res.LatencyMs < 1 {
		res.LatencyMs = 1
	}
	if cfg.GeoURL != "" {
		// A failed geo lookup does not fail the test: the proxy works.
		if cc, tz := geoThrough(ctx, addr, cfg.GeoURL, res.IP); cc != "" || tz != "" {
			res.Country, res.Timezone, res.Language = cc, tz, countryLanguage[cc]
		}
	}
	return res, nil
}

func geoThrough(ctx context.Context, relayAddr, tmpl, ip string) (cc, tz string) {
	target := strings.ReplaceAll(tmpl, "{ip}", url.PathEscape(ip))
	pu, _ := url.Parse("http://" + relayAddr)
	cl := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: true}, Timeout: 12 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", ""
	}
	resp, err := cl.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", ""
	}
	return parseGeo(body)
}

// parseGeo reads a country code and an IANA timezone from the JSON of the
// common lookup services (ipwho.is, ipapi.co, ip-api.com, ipinfo.io shapes).
func parseGeo(body []byte) (cc, tz string) {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return "", ""
	}
	str := func(v any) string { s, _ := v.(string); return s }
	for _, k := range []string{"country_code", "countryCode", "country"} {
		if s := strings.ToUpper(str(m[k])); len(s) == 2 {
			cc = s
			break
		}
	}
	for _, k := range []string{"timezone", "time_zone", "timezone_id"} {
		switch v := m[k].(type) {
		case string:
			tz = v
		case map[string]any:
			tz = str(v["id"])
			if tz == "" {
				tz = str(v["name"])
			}
		}
		if tz != "" {
			break
		}
	}
	if !strings.Contains(tz, "/") && tz != "UTC" {
		tz = ""
	}
	return cc, tz
}

// countryLanguage is the usual browser language of a country.
var countryLanguage = map[string]string{
	"US": "en-US", "GB": "en-GB", "CA": "en-CA", "AU": "en-AU", "NZ": "en-NZ", "IE": "en-IE", "SG": "en-SG", "IN": "en-IN", "ZA": "en-ZA", "PH": "en-PH",
	"DE": "de-DE", "AT": "de-AT", "CH": "de-CH", "FR": "fr-FR", "BE": "nl-BE", "NL": "nl-NL", "ES": "es-ES", "MX": "es-MX", "AR": "es-AR", "CO": "es-CO", "CL": "es-CL",
	"PT": "pt-PT", "BR": "pt-BR", "IT": "it-IT", "PL": "pl-PL", "CZ": "cs-CZ", "SK": "sk-SK", "HU": "hu-HU", "RO": "ro-RO", "BG": "bg-BG", "GR": "el-GR",
	"SE": "sv-SE", "NO": "nb-NO", "DK": "da-DK", "FI": "fi-FI", "RU": "ru-RU", "UA": "uk-UA", "TR": "tr-TR", "IL": "he-IL", "SA": "ar-SA", "AE": "ar-AE", "EG": "ar-EG",
	"JP": "ja-JP", "KR": "ko-KR", "CN": "zh-CN", "TW": "zh-TW", "HK": "zh-HK", "VN": "vi-VN", "TH": "th-TH", "ID": "id-ID", "MY": "ms-MY",
}

// ---- service -----------------------------------------------------------------

type proxyStatus struct {
	Status    string `json:"status"`
	LatencyMs int    `json:"latencyMs,omitempty"`
	IP        string `json:"ip,omitempty"`
	Country   string `json:"country,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
	Language  string `json:"language,omitempty"`
}

// ProxyService is the Go side of the Proxies screen.
type ProxyService struct {
	st      *store.Store
	dataDir string
	emit    func(string, any)
	prober  *proxyProber
	running func(profileID string) bool

	opMu sync.Mutex
	mu   sync.Mutex
	stat map[string]proxyStatus
}

func newProxyService(b *Backend, running func(string) bool) *ProxyService {
	return newProxyServiceWith(b.Store, b.DataDir, b.Emit, newProxyProber(b.DataDir), running)
}

func newProxyServiceWith(st *store.Store, dataDir string, emit func(string, any), pr *proxyProber, running func(string) bool) *ProxyService {
	if running == nil {
		running = func(string) bool { return false }
	}
	s := &ProxyService{st: st, dataDir: dataDir, emit: emit, prober: pr, running: running, stat: map[string]proxyStatus{}}
	if b, err := os.ReadFile(filepath.Join(dataDir, statusFile)); err == nil {
		_ = json.Unmarshal(b, &s.stat)
	}
	return s
}

// proxyToAPIError maps core errors to the codes of docs/api-contract.md.
// Every ProxyService method returns its errors through it.
func proxyToAPIError(err error) error {
	if err == nil {
		return nil
	}
	var a *APIError
	switch {
	case errors.As(err, &a):
		return err
	case errors.Is(err, store.ErrNotFound):
		return apiErr("notFound", err.Error())
	case errors.Is(err, proxy.ErrUpstreamAuth):
		return &APIError{Code: "proxy.auth", Message: err.Error()}
	case errors.Is(err, context.Canceled):
		return &APIError{Code: "internal", Message: err.Error()}
	}
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) || strings.Contains(err.Error(), "proxy:") || strings.Contains(err.Error(), "dial") || errors.Is(err, context.DeadlineExceeded) {
		return &APIError{Code: "proxy.down", Message: err.Error(), Retryable: true}
	}
	return asAPIError(err)
}

func (s *ProxyService) saveStatus() {
	b, err := json.Marshal(s.stat)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(s.dataDir, statusFile), b, 0o600)
}

func (s *ProxyService) setStatus(id string, st proxyStatus) {
	s.mu.Lock()
	s.stat[id] = st
	s.saveStatus()
	s.mu.Unlock()
	if s.emit != nil {
		s.emit("proxy.status", ProxyStatusEvent{ProxyID: id, Status: st.Status, LatencyMs: st.LatencyMs, IP: st.IP, Country: st.Country, Timezone: st.Timezone, Language: st.Language})
	}
}

// usage maps a proxy ID to the IDs of the profiles that use it.
func (s *ProxyService) usage(ctx context.Context) (map[string][]string, error) {
	rows, err := s.st.DB().QueryContext(ctx, `SELECT proxy_id, id FROM profiles WHERE proxy_id IS NOT NULL ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var pid, id string
		if err := rows.Scan(&pid, &id); err != nil {
			return nil, err
		}
		out[pid] = append(out[pid], id)
	}
	return out, rows.Err()
}

func (s *ProxyService) toDTO(r store.ProxyRecord, used []string) ProxyDTO {
	s.mu.Lock()
	st, ok := s.stat[r.ID]
	s.mu.Unlock()
	if !ok || st.Status == "" || st.Status == "checking" {
		st = proxyStatus{Status: "untested"}
	}
	if used == nil {
		used = []string{}
	}
	return ProxyDTO{
		ID: r.ID, Scheme: string(r.Scheme), Host: r.Host, Port: r.Port, Username: r.Username, HasPassword: r.HasSecret,
		Country: st.Country, Timezone: st.Timezone, Language: st.Language, Status: st.Status, LatencyMs: st.LatencyMs, IP: st.IP, UsedBy: used,
	}
}

// ListProxies returns all proxies with the profiles that use them.
func (s *ProxyService) ListProxies(ctx context.Context) ([]ProxyDTO, error) {
	recs, err := s.st.Proxies.List(ctx)
	if err != nil {
		return nil, proxyToAPIError(err)
	}
	use, err := s.usage(ctx)
	if err != nil {
		return nil, proxyToAPIError(err)
	}
	out := make([]ProxyDTO, 0, len(recs))
	for _, r := range recs {
		out = append(out, s.toDTO(r, use[r.ID]))
	}
	return out, nil
}

func proxyInvalid(field, msg string) error {
	return &APIError{Code: "invalid", Message: msg, Details: map[string]any{
		"problems": []map[string]string{{"path": field, "msg": msg}},
	}}
}

func validateProxyFields(scheme, host string, port int, user string, pass *string) (profile.ProxyScheme, error) {
	sc := profile.ProxyScheme(strings.ToLower(strings.TrimSpace(scheme)))
	switch sc {
	case profile.ProxyHTTP, profile.ProxyHTTPS, profile.ProxySOCKS5:
	default:
		return "", proxyInvalid("scheme", "scheme is not http, https or socks5")
	}
	if !validProxyHost(host) {
		return "", proxyInvalid("host", "host is empty or not valid")
	}
	if port < 1 || port > 65535 {
		return "", proxyInvalid("port", "port is not between 1 and 65535")
	}
	if len(user) > 255 {
		return "", proxyInvalid("username", "username is longer than 255 bytes")
	}
	if pass != nil && len(*pass) > 1024 {
		return "", proxyInvalid("password", "password is longer than 1024 bytes")
	}
	return sc, nil
}

func (s *ProxyService) duplicate(ctx context.Context, sc profile.ProxyScheme, host string, port int, user, exceptID string) (bool, error) {
	recs, err := s.st.Proxies.List(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range recs {
		if r.ID != exceptID && r.Scheme == sc && strings.EqualFold(r.Host, host) && r.Port == port && r.Username == user {
			return true, nil
		}
	}
	return false, nil
}

var errProxyExists = &APIError{Code: "conflict", Message: "this proxy exists already", Details: map[string]any{"reason": "duplicate"}}

// CreateProxy adds a proxy. The password goes to the secret store.
func (s *ProxyService) CreateProxy(ctx context.Context, in ProxyInput) (ProxyDTO, error) {
	host := strings.TrimSpace(in.Host)
	sc, err := validateProxyFields(in.Scheme, host, in.Port, in.Username, in.Password)
	if err != nil {
		return ProxyDTO{}, err
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if dup, err := s.duplicate(ctx, sc, host, in.Port, in.Username, ""); err != nil {
		return ProxyDTO{}, proxyToAPIError(err)
	} else if dup {
		return ProxyDTO{}, errProxyExists
	}
	rec, err := s.st.Proxies.Put(ctx, store.ProxyRecord{Name: proxyName(host, in.Port), Scheme: sc, Host: host, Port: in.Port, Username: in.Username}, in.Password)
	if err != nil {
		return ProxyDTO{}, proxyToAPIError(err)
	}
	return s.toDTO(rec, nil), nil
}

// UpdateProxy edits a proxy. A nil password keeps the stored one. A changed
// endpoint clears the saved test result.
func (s *ProxyService) UpdateProxy(ctx context.Context, id string, in ProxyInput) (ProxyDTO, error) {
	host := strings.TrimSpace(in.Host)
	sc, err := validateProxyFields(in.Scheme, host, in.Port, in.Username, in.Password)
	if err != nil {
		return ProxyDTO{}, err
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	cur, err := s.st.Proxies.Get(ctx, id)
	if err != nil {
		return ProxyDTO{}, proxyToAPIError(err)
	}
	if dup, err := s.duplicate(ctx, sc, host, in.Port, in.Username, id); err != nil {
		return ProxyDTO{}, proxyToAPIError(err)
	} else if dup {
		return ProxyDTO{}, errProxyExists
	}
	moved := cur.Scheme != sc || !strings.EqualFold(cur.Host, host) || cur.Port != in.Port || cur.Username != in.Username || in.Password != nil
	cur.Scheme, cur.Host, cur.Port, cur.Username, cur.Name = sc, host, in.Port, in.Username, proxyName(host, in.Port)
	rec, err := s.st.Proxies.Put(ctx, cur, in.Password)
	if err != nil {
		return ProxyDTO{}, proxyToAPIError(err)
	}
	if moved {
		s.mu.Lock()
		delete(s.stat, id)
		s.saveStatus()
		s.mu.Unlock()
	}
	use, _ := s.usage(ctx)
	return s.toDTO(rec, use[id]), nil
}

// SetProxyPassword replaces the stored password. An empty one removes it.
func (s *ProxyService) SetProxyPassword(ctx context.Context, id, password string) error {
	if len(password) > 1024 {
		return proxyInvalid("password", "password is longer than 1024 bytes")
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	cur, err := s.st.Proxies.Get(ctx, id)
	if err != nil {
		return proxyToAPIError(err)
	}
	_, err = s.st.Proxies.Put(ctx, cur, &password)
	return proxyToAPIError(err)
}

// DeleteProxy removes a proxy and its password. Profiles that used it have no
// proxy afterwards. A running profile that uses it gives conflict.
func (s *ProxyService) DeleteProxy(ctx context.Context, id string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if _, err := s.st.Proxies.Get(ctx, id); err != nil {
		return proxyToAPIError(err)
	}
	use, err := s.usage(ctx)
	if err != nil {
		return proxyToAPIError(err)
	}
	var run []string
	for _, pid := range use[id] {
		if s.running(pid) {
			run = append(run, pid)
		}
	}
	if len(run) > 0 {
		return &APIError{Code: "conflict", Message: "a running profile uses this proxy", Details: map[string]any{"reason": "running", "ids": run}}
	}
	if err := s.st.Proxies.Delete(ctx, id); err != nil {
		return proxyToAPIError(err)
	}
	s.mu.Lock()
	delete(s.stat, id)
	s.saveStatus()
	s.mu.Unlock()
	return nil
}

// resolve builds the proxy to test from a saved record and the input.
func (s *ProxyService) resolve(ctx context.Context, in ProxyTestInput) (profile.Proxy, error) {
	p := profile.Proxy{Scheme: profile.ProxyScheme(strings.ToLower(in.Scheme)), Host: strings.TrimSpace(in.Host), Port: in.Port, Username: in.Username, Password: in.Password}
	if in.ID != "" {
		rec, err := s.st.Proxies.Get(ctx, in.ID)
		if err != nil {
			return p, err
		}
		if p.Scheme == "" {
			p.Scheme = rec.Scheme
		}
		if p.Host == "" {
			p.Host = rec.Host
		}
		if p.Port == 0 {
			p.Port = rec.Port
		}
		if p.Username == "" {
			p.Username = rec.Username
		}
		if p.Password == "" && rec.HasSecret {
			if p.Password, err = s.st.Proxies.Password(ctx, rec.ID); err != nil {
				return p, err
			}
		}
	}
	if _, err := validateProxyFields(string(p.Scheme), p.Host, p.Port, p.Username, &p.Password); err != nil {
		return p, err
	}
	return p, nil
}

// TestProxy connects through the proxy: exit IP, latency, country, timezone
// and language. A saved proxy keeps the result and fires proxy.status.
func (s *ProxyService) TestProxy(ctx context.Context, in ProxyTestInput) (ProxyCheckResult, error) {
	p, err := s.resolve(ctx, in)
	if err != nil {
		return ProxyCheckResult{}, proxyToAPIError(err)
	}
	res, err := s.prober.probe(ctx, p)
	if err != nil && res.Status == "" {
		res.Status = "down"
	}
	if err != nil {
		res.OK = false
		if res.Code == "" {
			res.Code = proxyToAPIError(err).(*APIError).Code
		}
		res.Message = err.Error()
	}
	if in.ID != "" {
		s.setStatus(in.ID, proxyStatus{Status: res.Status, LatencyMs: res.LatencyMs, IP: res.IP, Country: res.Country, Timezone: res.Timezone, Language: res.Language})
	}
	return res, nil
}

// HealthCheck tests the proxies in the background (empty ids: all) and
// returns a job ID. Each result fires proxy.status. The job ends with
// job.done.
func (s *ProxyService) HealthCheck(ctx context.Context, ids []string) (string, error) {
	if len(ids) == 0 {
		recs, err := s.st.Proxies.List(ctx)
		if err != nil {
			return "", proxyToAPIError(err)
		}
		for _, r := range recs {
			ids = append(ids, r.ID)
		}
	} else {
		for _, id := range ids {
			if _, err := s.st.Proxies.Get(ctx, id); err != nil {
				return "", proxyToAPIError(err)
			}
		}
	}
	job := store.NewID()
	for _, id := range ids {
		s.mu.Lock()
		old := s.stat[id]
		s.mu.Unlock()
		if s.emit != nil {
			s.emit("proxy.status", ProxyStatusEvent{ProxyID: id, Status: "checking", Country: old.Country, Timezone: old.Timezone, Language: old.Language})
		}
	}
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var wg sync.WaitGroup
		var doneMu sync.Mutex
		done := 0
		sem := make(chan struct{}, 6)
		for _, id := range ids {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				_, _ = s.TestProxy(bg, ProxyTestInput{ID: id})
				doneMu.Lock()
				done++
				pct := done * 100 / len(ids)
				doneMu.Unlock()
				if s.emit != nil {
					s.emit("job.progress", map[string]any{"jobId": job, "kind": "proxyHealth", "stage": "check", "pct": pct})
				}
			}()
		}
		wg.Wait()
		if s.emit != nil {
			s.emit("job.done", map[string]any{"jobId": job, "ok": true})
		}
	}()
	return job, nil
}

// ImportProxies adds the proxies in text, one per line. Accepted forms:
//
//	host:port
//	host:port:user:pass
//	[ipv6]:port  and  [ipv6]:port:user:pass
//	scheme://host:port
//	scheme://user:pass@host:port   (also user:pass@host:port)
//
// Lines that are empty or start with # are ignored. A bad line does not stop
// the import: it appears in Skipped with its line number and a reason.
func (s *ProxyService) ImportProxies(ctx context.Context, text, defaultScheme string) (ProxyImportResult, error) {
	ds := profile.ProxyScheme(strings.ToLower(strings.TrimSpace(defaultScheme)))
	if ds == "" {
		ds = profile.ProxySOCKS5
	}
	if !validProxyScheme(ds) {
		return ProxyImportResult{}, proxyInvalid("defaultScheme", "scheme is not http, https or socks5")
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	res := ProxyImportResult{Skipped: []ProxyImportSkip{}}
	for i, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pp, reason := parseProxyLine(line, ds)
		if reason == "" {
			dup, err := s.duplicate(ctx, pp.Scheme, pp.Host, pp.Port, pp.Username, "")
			if err != nil {
				return res, proxyToAPIError(err)
			}
			if dup {
				reason = "duplicate"
			}
		}
		if reason != "" {
			res.Skipped = append(res.Skipped, ProxyImportSkip{Line: i + 1, Reason: reason})
			continue
		}
		var pw *string
		if pp.Password != "" {
			pw = &pp.Password
		}
		if _, err := s.st.Proxies.Put(ctx, store.ProxyRecord{Name: proxyName(pp.Host, pp.Port), Scheme: pp.Scheme, Host: pp.Host, Port: pp.Port, Username: pp.Username}, pw); err != nil {
			return res, proxyToAPIError(err)
		}
		res.Added++
	}
	return res, nil
}

// GetProxyLookup returns the lookup service setting.
func (s *ProxyService) GetProxyLookup(ctx context.Context) (ProxyLookup, error) {
	return s.prober.lookup(), nil
}

// SetProxyLookup saves the lookup service setting. Both URLs must be http or
// https. GeoURL may be empty (no geo lookup) or hold {ip}.
func (s *ProxyService) SetProxyLookup(ctx context.Context, l ProxyLookup) (ProxyLookup, error) {
	l.IPURL, l.GeoURL = strings.TrimSpace(l.IPURL), strings.TrimSpace(l.GeoURL)
	chk := func(field, v string, empty bool) error {
		if v == "" && empty {
			return nil
		}
		u, err := url.Parse(strings.ReplaceAll(v, "{ip}", "0.0.0.0"))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return proxyInvalid(field, field+" is not an http or https URL")
		}
		return nil
	}
	if err := chk("ipUrl", l.IPURL, false); err != nil {
		return l, err
	}
	if err := chk("geoUrl", l.GeoURL, true); err != nil {
		return l, err
	}
	b, err := json.Marshal(l)
	if err != nil {
		return l, proxyToAPIError(err)
	}
	if err := os.WriteFile(filepath.Join(s.dataDir, lookupFile), b, 0o600); err != nil {
		return l, proxyToAPIError(err)
	}
	return l, nil
}

func proxyName(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }
