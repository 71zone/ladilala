package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/engine"
	"github.com/8bu/ladilala/core/macro"
	"github.com/8bu/ladilala/core/player"
	"github.com/8bu/ladilala/core/profile"
	"github.com/8bu/ladilala/core/store"
)

// ---- fakes -------------------------------------------------------------------

type pfBrowser struct {
	done   chan struct{}
	once   sync.Once
	closed chan struct{}
}

func (b *pfBrowser) PlayRow(context.Context, *macro.Macro, map[string]string, player.Hooks) error {
	return nil
}
func (b *pfBrowser) Recheck(context.Context, *macro.Macro) error { return nil }
func (b *pfBrowser) Done() <-chan struct{}                       { return b.done }
func (b *pfBrowser) exit()                                       { b.once.Do(func() { close(b.done) }) }
func (b *pfBrowser) Close() error {
	b.exit()
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

type pfLauncher struct {
	mu       sync.Mutex
	err      error
	launched []profile.Profile
	browsers []*pfBrowser
}

// browserFor returns the browser launched for a profile. Launch order is not
// the order of the ids, because StartProfiles starts them in parallel.
func (l *pfLauncher) browserFor(t *testing.T, id string) *pfBrowser {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, p := range l.launched {
		if p.ID == id {
			return l.browsers[i]
		}
	}
	t.Fatalf("no browser launched for %s", id)
	return nil
}

func (l *pfLauncher) Launch(_ context.Context, p profile.Profile) (engine.Browser, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	b := &pfBrowser{done: make(chan struct{}), closed: make(chan struct{})}
	l.launched = append(l.launched, p)
	l.browsers = append(l.browsers, b)
	return b, nil
}

type pfEvents struct {
	mu sync.Mutex
	ev []struct {
		name string
		data any
	}
}

func (e *pfEvents) emit(name string, data any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ev = append(e.ev, struct {
		name string
		data any
	}{name, data})
}

func (e *pfEvents) states(id string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, x := range e.ev {
		if pe, ok := x.data.(ProfileStateEvent); ok && x.name == "profile.state" && pe.ProfileID == id {
			out = append(out, pe.State+"/"+pe.Err+"/"+pe.Code)
		}
	}
	return out
}

func (e *pfEvents) all() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, _ := json.Marshal(e.ev)
	return string(b)
}

func (e *pfEvents) count(name string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, x := range e.ev {
		if x.name == name {
			n++
		}
	}
	return n
}

type pfRig struct {
	t        *testing.T
	st       *store.Store
	secrets  *store.MemorySecrets
	dir      string
	launcher *pfLauncher
	events   *pfEvents
	profiles *ProfileService
	proxies  *ProxyService
}

func newPfRig(t *testing.T) *pfRig {
	t.Helper()
	dir := t.TempDir()
	sec := &store.MemorySecrets{}
	st, err := store.Open(context.Background(), filepath.Join(dir, "db"), sec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := &pfRig{t: t, st: st, secrets: sec, dir: dir, launcher: &pfLauncher{}, events: &pfEvents{}}
	pr := newProxyProber(dir)
	pr.timeout = 6 * time.Second
	r.profiles = newProfileServiceWith(st, dir, r.launcher, r.events.emit, pr)
	r.profiles.version = func() string { return "150.0.7871.186" }
	r.proxies = newProxyServiceWith(st, dir, r.events.emit, pr, r.profiles.isRunning)
	return r
}

func (r *pfRig) profile(name string) ProfileDTO {
	r.t.Helper()
	p, err := r.profiles.CreateProfile(context.Background(), ProfileInput{Name: name, Group: "G"})
	if err != nil {
		r.t.Fatal(err)
	}
	return p
}

func (r *pfRig) proxy(host string, port int, pass string) ProxyDTO {
	r.t.Helper()
	in := ProxyInput{Scheme: "http", Host: host, Port: port, Username: "u"}
	if pass != "" {
		in.Password = &pass
	}
	p, err := r.proxies.CreateProxy(context.Background(), in)
	if err != nil {
		r.t.Fatal(err)
	}
	return p
}

func pfCode(err error) string {
	var a *APIError
	if errors.As(err, &a) {
		return a.Code
	}
	return "not-api-error:" + pfErrString(err)
}

func pfErrString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func pfEventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

// ---- fake HTTP proxy and target ---------------------------------------------

// pfStartProxy runs an HTTP forward proxy on loopback. With user set it
// answers 407 until the request carries those credentials.
func pfStartProxy(t *testing.T, user, pass string) (host string, port int) {
	t.Helper()
	return pfStartProxyHosts(t, user, pass, nil)
}

// pfStartProxyHosts is pfStartProxy that also resolves the names in hosts
// ("name:80" to "127.0.0.1:port"), so a browser can reach a loopback server
// through a name that Chrome does not bypass the proxy for.
func pfStartProxyHosts(t *testing.T, user, pass string, hosts map[string]string) (host string, port int) {
	t.Helper()
	resolve := func(hp string) string {
		if m, ok := hosts[hp]; ok {
			return m
		}
		return hp
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user != "" && r.Header.Get("Proxy-Authorization") != want {
			w.Header().Set("Proxy-Authenticate", `Basic realm="x"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		if r.Method == http.MethodConnect {
			up, err := net.Dial("tcp", resolve(r.Host))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			c, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
			go func() { _, _ = io.Copy(up, c); up.Close() }()
			go func() { _, _ = io.Copy(c, up); c.Close() }()
			return
		}
		out := r.Clone(r.Context())
		out.RequestURI = ""
		if m, ok := hosts[out.URL.Host]; ok {
			out.URL.Host = m
		} else if m, ok := hosts[out.URL.Host+":80"]; ok {
			out.URL.Host = m
		}
		out.Header.Del("Proxy-Authorization")
		resp, err := (&http.Transport{}).RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	p, _ := strconv.Atoi(u.Port())
	return u.Hostname(), p
}

// pfStartLookup serves the two lookup endpoints and points the setting at them.
func pfStartLookup(t *testing.T, r *pfRig, ip, cc, tz string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ip", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, ip+"\n") })
	mux.HandleFunc("/geo/", func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasSuffix(req.URL.Path, ip) {
			http.NotFound(w, req)
			return
		}
		io.WriteString(w, `{"success":true,"ip":"`+ip+`","country_code":"`+cc+`","timezone":{"id":"`+tz+`"}}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if _, err := r.proxies.SetProxyLookup(context.Background(), ProxyLookup{IPURL: srv.URL + "/ip", GeoURL: srv.URL + "/geo/{ip}"}); err != nil {
		t.Fatal(err)
	}
}

// ---- profile tests -----------------------------------------------------------

func TestProfileCRUDRoundTrip(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	p, err := r.profiles.CreateProfile(ctx, ProfileInput{Name: " Shop 1 ", Group: "Ads", Tags: []string{"a", "A", " b "}, Notes: "n", Preset: "windows-10", Language: "de-DE", Timezone: "Europe/Berlin", WebRTC: profile.ModeReal})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Shop 1" || len(p.Tags) != 2 || p.State != "stopped" || p.Fingerprint.Preset != "windows-10" {
		t.Fatalf("created: %+v", p)
	}
	fp := p.Fingerprint
	if fp.Platform != "windows" || fp.PlatformVersion != "10.0.0" || fp.Language != "de-DE" || fp.Timezone != "Europe/Berlin" || fp.WebRTC != profile.ModeReal || fp.Canvas != profile.ModeNoise {
		t.Fatalf("fingerprint: %+v", fp)
	}
	if fp.Seed == 0 || fp.Seed > maxSeed {
		t.Fatalf("seed %d", fp.Seed)
	}
	if problems := profile.Validate(fp.Fingerprint); len(problems) > 0 {
		t.Fatalf("incoherent: %v", problems)
	}
	if !strings.HasPrefix(p.DataDir, filepath.Join(r.dir, "profiles")) {
		t.Fatalf("data dir %s", p.DataDir)
	}

	got, err := r.profiles.GetProfile(ctx, p.ID)
	if err != nil || got.ID != p.ID || got.Fingerprint.Seed != fp.Seed {
		t.Fatalf("get: %+v %v", got, err)
	}
	up, err := r.profiles.UpdateProfile(ctx, p.ID, ProfileInput{Name: "Shop one", Group: "Shop", Tags: []string{"x"}, Notes: "m", Canvas: profile.ModeOff})
	if err != nil {
		t.Fatal(err)
	}
	if up.Name != "Shop one" || up.Group != "Shop" || up.Fingerprint.Canvas != profile.ModeOff || up.Fingerprint.Seed != fp.Seed || up.Fingerprint.GPURenderer != fp.GPURenderer || up.Fingerprint.Language != "de-DE" {
		t.Fatalf("update changed the device or lost fields: %+v", up)
	}
	// A new preset and seed rebuild the device and keep modes and language.
	up, err = r.profiles.UpdateProfile(ctx, p.ID, ProfileInput{Name: "Shop one", Group: "Shop", Preset: "macos-14", Seed: 77})
	if err != nil {
		t.Fatal(err)
	}
	if up.Fingerprint.Platform != "macos" || up.Fingerprint.Seed != 77 || up.Fingerprint.Canvas != profile.ModeOff || up.Fingerprint.Language != "de-DE" {
		t.Fatalf("rebuild: %+v", up.Fingerprint)
	}
	if problems := profile.Validate(up.Fingerprint.Fingerprint); len(problems) > 0 {
		t.Fatalf("incoherent: %v", problems)
	}
	list, err := r.profiles.ListProfiles(ctx, ProfileFilter{Text: "shop"})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	for _, f := range []ProfileFilter{{Group: "nope"}, {Tag: "nope"}, {State: "running"}} {
		if l, _ := r.profiles.ListProfiles(ctx, f); len(l) != 0 {
			t.Fatalf("filter %+v matched", f)
		}
	}
	if g, _ := r.profiles.ListGroups(ctx); len(g) != 1 || g[0] != "Shop" {
		t.Fatalf("groups %v", g)
	}
	if err := r.profiles.DeleteProfiles(ctx, []string{p.ID}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.profiles.GetProfile(ctx, p.ID); pfCode(err) != "notFound" {
		t.Fatalf("after delete: %v", err)
	}
}

func TestProfileErrorCodes(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	a := r.profile("A")
	if _, err := r.profiles.CreateProfile(ctx, ProfileInput{Name: "a"}); pfCode(err) != "conflict" {
		t.Fatalf("duplicate name (case-insensitive): %v", err)
	}
	if _, err := r.profiles.CreateProfile(ctx, ProfileInput{Name: ""}); pfCode(err) != "invalid" {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := r.profiles.CreateProfile(ctx, ProfileInput{Name: "B", Preset: "amiga"}); pfCode(err) != "invalid" {
		t.Fatalf("preset: %v", err)
	}
	_, err := r.profiles.CreateProfile(ctx, ProfileInput{Name: "B", Language: "english"})
	if pfCode(err) != "invalid" {
		t.Fatalf("language: %v", err)
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.Details["problems"] == nil {
		t.Fatalf("invalid has no problems: %v", err)
	}
	if _, err := r.profiles.CreateProfile(ctx, ProfileInput{Name: "B", ProxyID: "ghost"}); pfCode(err) != "notFound" {
		t.Fatalf("ghost proxy: %v", err)
	}
	if _, err := r.profiles.GetProfile(ctx, "ghost"); pfCode(err) != "notFound" {
		t.Fatalf("get ghost: %v", err)
	}
	if err := r.profiles.DeleteProfiles(ctx, []string{a.ID, "ghost"}, false); pfCode(err) != "notFound" {
		t.Fatalf("delete ghost: %v", err)
	}
	if _, err := r.profiles.GetProfile(ctx, a.ID); err != nil {
		t.Fatal("a failed bulk delete must delete nothing")
	}
	b := r.profile("B")
	if _, err := r.profiles.UpdateProfile(ctx, b.ID, ProfileInput{Name: "A"}); pfCode(err) != "conflict" {
		t.Fatalf("rename onto A: %v", err)
	}
	if got := profileToAPIError(driver.ErrProfileLocked); pfCode(got) != "profile.locked" {
		t.Fatalf("locked: %v", got)
	}
	for err, want := range map[error]string{
		driver.ErrInvalidDataDir: "profile.dataDir", driver.ErrBinaryNotFound: "engine.notFound",
		driver.ErrDebuggingBlocked: "engine.policyBlocked", driver.ErrCanvasHang: "engine.hang", driver.ErrCanvasLeak: "engine.hang",
		errors.New("boom"): "internal",
	} {
		if got := pfCode(profileToAPIError(err)); got != want {
			t.Errorf("%v maps to %s, want %s", err, got, want)
		}
	}
}

func TestCloneProfile(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	px := r.proxy("127.0.0.1", 9, "")
	src, err := r.profiles.CreateProfile(ctx, ProfileInput{Name: "Src", Group: "G", Tags: []string{"t"}, Notes: "keep", Preset: "macos-14", Language: "fr-FR", Timezone: "Europe/Paris", ProxyID: px.ID, Canvas: profile.ModeReal})
	if err != nil {
		t.Fatal(err)
	}
	c1, err := r.profiles.CloneProfile(ctx, src.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := r.profiles.CloneProfile(ctx, src.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if c1.Name != "Src (copy)" || c2.Name != "Src (copy) 2" {
		t.Fatalf("names %q %q", c1.Name, c2.Name)
	}
	if c1.ID == src.ID || c1.Fingerprint.Seed == src.Fingerprint.Seed || c1.Fingerprint.Seed == c2.Fingerprint.Seed {
		t.Fatalf("seeds: %d %d %d", src.Fingerprint.Seed, c1.Fingerprint.Seed, c2.Fingerprint.Seed)
	}
	if c1.DataDir == src.DataDir || c1.Group != "G" || c1.Notes != "keep" || c1.ProxyID != px.ID || len(c1.Tags) != 1 {
		t.Fatalf("clone fields: %+v", c1)
	}
	f := c1.Fingerprint
	if f.Platform != "macos" || f.PlatformVersion != "14.5.0" || f.Language != "fr-FR" || f.Timezone != "Europe/Paris" || f.Canvas != profile.ModeReal {
		t.Fatalf("clone fingerprint: %+v", f)
	}
	if problems := profile.Validate(f.Fingerprint); len(problems) > 0 {
		t.Fatalf("incoherent: %v", problems)
	}
	if _, err := r.profiles.CloneProfile(ctx, src.ID, "Src"); pfCode(err) != "conflict" {
		t.Fatalf("clone onto taken name: %v", err)
	}
	if _, err := os.Stat(c1.DataDir); !os.IsNotExist(err) {
		t.Fatalf("clone data dir must start empty and absent, got %v", err)
	}
}

func TestStartStopEventsAndConflicts(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	a, b := r.profile("A"), r.profile("B")
	res, err := r.profiles.StartProfiles(ctx, []string{a.ID, b.ID, "ghost"})
	if err != nil {
		t.Fatal(err)
	}
	if !res[0].OK || !res[1].OK || res[2].OK || res[2].Error == nil || res[2].Error.Code != "notFound" {
		t.Fatalf("results: %+v", res)
	}
	if len(r.launcher.launched) != 2 {
		t.Fatalf("launched %d", len(r.launcher.launched))
	}
	if got := r.launcher.launched[0]; got.DataDir == "" || got.Fingerprint.Seed == 0 {
		t.Fatalf("launcher got an incomplete profile: %+v", got)
	}
	if s := r.events.states(a.ID); len(s) != 1 || s[0] != "running//" {
		t.Fatalf("events %v", s)
	}
	if got, _ := r.profiles.GetProfile(ctx, a.ID); got.State != "running" {
		t.Fatalf("state %s", got.State)
	}
	// Starting again is a no-op that reports ok.
	if res, _ := r.profiles.StartProfiles(ctx, []string{a.ID}); !res[0].OK || len(r.launcher.launched) != 2 {
		t.Fatalf("second start: %+v launched=%d", res, len(r.launcher.launched))
	}
	// Edits that matter, and delete, are refused while it runs. Name edits pass.
	if _, err := r.profiles.UpdateProfile(ctx, a.ID, ProfileInput{Name: "A", Seed: 5}); pfCode(err) != "conflict" {
		t.Fatalf("edit fingerprint while running: %v", err)
	}
	if _, err := r.profiles.UpdateProfile(ctx, a.ID, ProfileInput{Name: "A2", Group: "Z"}); err != nil {
		t.Fatalf("rename while running: %v", err)
	}
	if err := r.profiles.DeleteProfiles(ctx, []string{b.ID, a.ID}, false); pfCode(err) != "conflict" {
		t.Fatalf("delete while running: %v", err)
	}
	if err := r.profiles.AssignProxy(ctx, []string{a.ID}, ""); pfCode(err) != "conflict" {
		t.Fatalf("assign while running: %v", err)
	}
	if err := r.profiles.ShuffleFingerprint(ctx, []string{a.ID}); pfCode(err) != "conflict" {
		t.Fatalf("shuffle while running: %v", err)
	}
	if list, _ := r.profiles.ListProfiles(ctx, ProfileFilter{State: "running"}); len(list) != 2 {
		t.Fatalf("running filter: %d", len(list))
	}

	if err := r.profiles.StopProfiles(ctx, []string{a.ID, "not-running"}); err != nil {
		t.Fatal(err)
	}
	<-r.launcher.browserFor(t, a.ID).closed
	if s := r.events.states(a.ID); len(s) != 2 || s[1] != "stopped//" {
		t.Fatalf("events %v", s)
	}
	// The user closes the window of B: the service notices and frees the relay.
	r.launcher.browserFor(t, b.ID).exit()
	pfEventually(t, "B stopped", func() bool { s := r.events.states(b.ID); return len(s) == 2 && s[1] == "stopped//" })
	<-r.launcher.browserFor(t, b.ID).closed
	if err := r.profiles.DeleteProfiles(ctx, []string{a.ID, b.ID}, true); err != nil {
		t.Fatal(err)
	}
	// StopAll
	c, d := r.profile("C"), r.profile("D")
	_, _ = r.profiles.StartProfiles(ctx, []string{c.ID, d.ID})
	if err := r.profiles.StopAll(ctx); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.profiles.ListProfiles(ctx, ProfileFilter{State: "running"}); len(list) != 0 {
		t.Fatal("StopAll left a profile running")
	}
}

func TestStartFailuresMapToCodesAndState(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	a := r.profile("A")
	r.launcher.err = driver.ErrProfileLocked
	res, _ := r.profiles.StartProfiles(ctx, []string{a.ID})
	if res[0].OK || res[0].Error.Code != "profile.locked" {
		t.Fatalf("locked: %+v", res[0])
	}
	if got, _ := r.profiles.GetProfile(ctx, a.ID); got.State != "error" || got.Err != "engineExit" || got.ErrCode != "profile.locked" {
		t.Fatalf("dto after failure: %+v", got)
	}
	if s := r.events.states(a.ID); len(s) != 1 || s[0] != "error/engineExit/profile.locked" {
		t.Fatalf("events %v", s)
	}
	// A later good start clears the error.
	r.launcher.err = nil
	if res, _ := r.profiles.StartProfiles(ctx, []string{a.ID}); !res[0].OK {
		t.Fatalf("retry: %+v", res[0])
	}
	if got, _ := r.profiles.GetProfile(ctx, a.ID); got.State != "running" || got.Err != "" {
		t.Fatalf("after retry: %+v", got)
	}
}

func TestStartRefusedWhenProxyIsDown(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nothing listens there now
	px := r.proxy("127.0.0.1", port, "")
	p, err := r.profiles.CreateProfile(ctx, ProfileInput{Name: "P", ProxyID: px.ID})
	if err != nil {
		t.Fatal(err)
	}
	res, _ := r.profiles.StartProfiles(ctx, []string{p.ID})
	if res[0].OK || res[0].Error.Code != "proxy.down" || !res[0].Error.Retryable {
		t.Fatalf("result: %+v", res[0])
	}
	if len(r.launcher.launched) != 0 {
		t.Fatal("the browser must not start without its proxy")
	}
	if got, _ := r.profiles.GetProfile(ctx, p.ID); got.State != "error" || got.Err != "proxyDown" {
		t.Fatalf("dto: %+v", got)
	}
}

func TestBulkOperationsAndProxyDelete(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	a, b, c := r.profile("A"), r.profile("B"), r.profile("C")
	ids := []string{a.ID, b.ID, c.ID}
	if err := r.profiles.BulkSetGroup(ctx, ids, "Moved"); err != nil {
		t.Fatal(err)
	}
	if err := r.profiles.BulkSetTags(ctx, ids[:2], []string{"x", "y"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.profiles.BulkSetTags(ctx, ids[:1], []string{"z"}, []string{"X"}); err != nil {
		t.Fatal(err)
	}
	ga, _ := r.profiles.GetProfile(ctx, a.ID)
	gb, _ := r.profiles.GetProfile(ctx, b.ID)
	if ga.Group != "Moved" || strings.Join(ga.Tags, ",") != "y,z" || strings.Join(gb.Tags, ",") != "x,y" {
		t.Fatalf("tags: %v %v", ga.Tags, gb.Tags)
	}
	p1, p2 := r.proxy("h1.example", 1080, ""), r.proxy("h2.example", 1080, "")
	if err := r.profiles.AssignProxy(ctx, ids, p1.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.profiles.AssignProxyRoundRobin(ctx, ids, []string{p1.ID, p2.ID}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, id := range ids {
		g, _ := r.profiles.GetProfile(ctx, id)
		got = append(got, g.ProxyID)
	}
	if got[0] != p1.ID || got[1] != p2.ID || got[2] != p1.ID {
		t.Fatalf("round robin: %v", got)
	}
	if err := r.profiles.AssignProxy(ctx, ids, "ghost"); pfCode(err) != "notFound" {
		t.Fatalf("ghost proxy: %v", err)
	}
	list, _ := r.proxies.ListProxies(ctx)
	if len(list) != 2 || len(list[0].UsedBy)+len(list[1].UsedBy) != 3 {
		t.Fatalf("usedBy: %+v", list)
	}
	// Deleting a proxy frees the profiles.
	if err := r.proxies.DeleteProxy(ctx, p1.ID); err != nil {
		t.Fatal(err)
	}
	if g, _ := r.profiles.GetProfile(ctx, a.ID); g.ProxyID != "" {
		t.Fatalf("profile kept a deleted proxy: %q", g.ProxyID)
	}
	// Shuffle: new seed, same OS, language, modes.
	before, _ := r.profiles.GetProfile(ctx, b.ID)
	if err := r.profiles.ShuffleFingerprint(ctx, []string{b.ID}); err != nil {
		t.Fatal(err)
	}
	after, _ := r.profiles.GetProfile(ctx, b.ID)
	if after.Fingerprint.Seed == before.Fingerprint.Seed || after.Fingerprint.Preset != before.Fingerprint.Preset || after.Fingerprint.Language != before.Fingerprint.Language {
		t.Fatalf("shuffle: %+v -> %+v", before.Fingerprint, after.Fingerprint)
	}
	if problems := profile.Validate(after.Fingerprint.Fingerprint); len(problems) > 0 {
		t.Fatalf("incoherent: %v", problems)
	}
}

func TestPresetsAndPreview(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	pr, _ := r.profiles.Presets(ctx)
	if len(pr.Presets) != 3 || pr.EngineMajor != 150 {
		t.Fatalf("%+v", pr)
	}
	for _, p := range pr.Presets {
		fp, err := r.profiles.PreviewFingerprint(ctx, PreviewInput{Preset: p.ID, Seed: 42, Language: "en-GB", Timezone: "Europe/London"})
		if err != nil {
			t.Fatal(err)
		}
		again, _ := r.profiles.PreviewFingerprint(ctx, PreviewInput{Preset: p.ID, Seed: 42, Language: "en-GB", Timezone: "Europe/London"})
		if fp.Fingerprint != again.Fingerprint || fp.Platform != p.Platform || fp.PlatformVersion != p.PlatformVersion || fp.Language != "en-GB" {
			t.Fatalf("preview %s: %+v", p.ID, fp)
		}
		if problems := profile.Validate(fp.Fingerprint); len(problems) > 0 {
			t.Fatalf("%s incoherent: %v", p.ID, problems)
		}
	}
	if list, _ := r.profiles.ListProfiles(ctx, ProfileFilter{}); len(list) != 0 {
		t.Fatal("preview must not save")
	}
}

// ---- secrets -----------------------------------------------------------------

func TestPasswordNeverInDTOsEventsOrErrors(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	const secret = "s3cret-Pa55-w0rd"
	px := r.proxy("127.0.0.1", 9, secret)
	if !px.HasPassword {
		t.Fatal("hasPassword must be true")
	}
	p, err := r.profiles.CreateProfile(ctx, ProfileInput{Name: "P", ProxyID: px.ID})
	if err != nil {
		t.Fatal(err)
	}
	var dump []string
	add := func(v any, err error) {
		if err != nil {
			dump = append(dump, err.Error())
		}
		b, _ := json.Marshal(v)
		dump = append(dump, string(b))
	}
	add(r.proxies.ListProxies(ctx))
	add(r.profiles.ListProfiles(ctx, ProfileFilter{}))
	add(r.profiles.GetProfile(ctx, p.ID))
	add(r.proxies.UpdateProxy(ctx, px.ID, ProxyInput{Scheme: "http", Host: "127.0.0.1", Port: 9, Username: "u"}))
	add(r.proxies.TestProxy(ctx, ProxyTestInput{ID: px.ID}))
	add(r.profiles.StartProfiles(ctx, []string{p.ID})) // proxy port 9 is closed: error path
	add(r.proxies.ImportProxies(ctx, "h.example:1:u:"+secret+"\nbad:99999:u:"+secret, "http"))
	dump = append(dump, r.events.all())
	for _, d := range dump {
		if strings.Contains(d, secret) {
			t.Fatalf("the password leaked: %s", d)
		}
	}
	// The password sits in the secret store only.
	if v, err := r.secrets.Get(ctx, store.ProxySecretKey(px.ID)); err != nil || v != secret {
		t.Fatalf("secret store: %q %v", v, err)
	}
	var raw string
	rows, _ := r.st.DB().Query(`SELECT group_concat(id||name||scheme||host||username, '|') FROM proxies`)
	for rows.Next() {
		_ = rows.Scan(&raw)
	}
	rows.Close()
	if strings.Contains(raw, secret) {
		t.Fatal("the password is in SQLite")
	}
}

// ---- proxy tests -------------------------------------------------------------

func TestProxyCRUDAndPassword(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	px := r.proxy("a.example", 8080, "pw1")
	if _, err := r.proxies.CreateProxy(ctx, ProxyInput{Scheme: "http", Host: "A.example", Port: 8080, Username: "u"}); pfCode(err) != "conflict" {
		t.Fatalf("duplicate: %v", err)
	}
	for _, in := range []ProxyInput{
		{Scheme: "ftp", Host: "x", Port: 1}, {Scheme: "http", Host: "", Port: 1}, {Scheme: "http", Host: "a b", Port: 1},
		{Scheme: "http", Host: "x", Port: 0}, {Scheme: "http", Host: "x", Port: 70000},
	} {
		if _, err := r.proxies.CreateProxy(ctx, in); pfCode(err) != "invalid" {
			t.Errorf("%+v: %v", in, err)
		}
	}
	// nil password keeps it, "" removes it.
	up, err := r.proxies.UpdateProxy(ctx, px.ID, ProxyInput{Scheme: "socks5", Host: "a.example", Port: 1081, Username: "v"})
	if err != nil || up.Scheme != "socks5" || up.Port != 1081 || !up.HasPassword {
		t.Fatalf("update: %+v %v", up, err)
	}
	if v, _ := r.secrets.Get(ctx, store.ProxySecretKey(px.ID)); v != "pw1" {
		t.Fatalf("kept password %q", v)
	}
	if err := r.proxies.SetProxyPassword(ctx, px.ID, "pw2"); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.secrets.Get(ctx, store.ProxySecretKey(px.ID)); v != "pw2" {
		t.Fatalf("new password %q", v)
	}
	if err := r.proxies.SetProxyPassword(ctx, px.ID, ""); err != nil {
		t.Fatal(err)
	}
	list, _ := r.proxies.ListProxies(ctx)
	if len(list) != 1 || list[0].HasPassword || list[0].Status != "untested" || list[0].Host != "a.example" {
		t.Fatalf("list: %+v", list)
	}
	if err := r.proxies.SetProxyPassword(ctx, "ghost", "x"); pfCode(err) != "notFound" {
		t.Fatalf("ghost: %v", err)
	}
	if err := r.proxies.DeleteProxy(ctx, px.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.secrets.Get(ctx, store.ProxySecretKey(px.ID)); err == nil {
		t.Fatal("delete must remove the password")
	}
	if err := r.proxies.DeleteProxy(ctx, px.ID); pfCode(err) != "notFound" {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestDeleteProxyRefusedWhileProfileRuns(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	host, port := pfStartProxy(t, "", "")
	px := r.proxy(host, port, "")
	p, _ := r.profiles.CreateProfile(ctx, ProfileInput{Name: "P", ProxyID: px.ID})
	if res, _ := r.profiles.StartProfiles(ctx, []string{p.ID}); !res[0].OK {
		t.Fatalf("start: %+v", res[0])
	}
	if err := r.proxies.DeleteProxy(ctx, px.ID); pfCode(err) != "conflict" {
		t.Fatalf("delete used proxy: %v", err)
	}
	_ = r.profiles.StopAll(ctx)
	if err := r.proxies.DeleteProxy(ctx, px.ID); err != nil {
		t.Fatalf("delete after stop: %v", err)
	}
	if g, _ := r.profiles.GetProfile(ctx, p.ID); g.ProxyID != "" {
		t.Fatal("profile kept the deleted proxy")
	}
}

func TestParseProxyLine(t *testing.T) {
	type want struct {
		scheme, host string
		port         int
		user, pass   string
		reason       string
	}
	cases := []struct {
		line string
		want want
	}{
		{"1.2.3.4:8080", want{"socks5", "1.2.3.4", 8080, "", "", ""}},
		{"gw.example.net:1080:alice:secret", want{"socks5", "gw.example.net", 1080, "alice", "secret", ""}},
		{"gw.example.net:1080:alice:pa:ss:word", want{"socks5", "gw.example.net", 1080, "alice", "pa:ss:word", ""}},
		{"http://bob:hunter2@10.0.0.1:3128", want{"http", "10.0.0.1", 3128, "bob", "hunter2", ""}},
		{"HTTPS://bob@10.0.0.1:3128", want{"https", "10.0.0.1", 3128, "bob", "", ""}},
		{"socks5://host.example:1080", want{"socks5", "host.example", 1080, "", "", ""}},
		{"socks5://host.example:1080/", want{"socks5", "host.example", 1080, "", "", ""}},
		{"bob:p%40ss@host.example:1080", want{"socks5", "host.example", 1080, "bob", "p@ss", ""}},
		{"user:pa:ss@host.example:1080", want{"socks5", "host.example", 1080, "user", "pa:ss", ""}},
		{"[2001:db8::1]:1080", want{"socks5", "2001:db8::1", 1080, "", "", ""}},
		{"[2001:db8::1]:1080:u:p", want{"socks5", "2001:db8::1", 1080, "u", "p", ""}},
		{"http://u:p@[2001:db8::1]:8080", want{"http", "2001:db8::1", 8080, "u", "p", ""}},
		{"host.example:0", want{reason: "port"}},
		{"host.example:70000", want{reason: "port"}},
		{"host.example:http", want{reason: "port"}},
		{"host.example", want{reason: "format"}},
		{"host.example:1080:onlyuser", want{reason: "format"}},
		{"ftp://host.example:21", want{reason: "scheme"}},
		{"[2001:db8::1]1080", want{reason: "format"}},
		{"[2001:db8::1]:1080:u", want{reason: "format"}},
		{"[not-ip]:1080", want{reason: "host"}},
		{"2001:db8::1:1080", want{reason: "port"}},
		{"bad host:1080", want{reason: "host"}},
		{":1080", want{reason: "host"}},
		{"http://u:p@:1080", want{reason: "host"}},
		{"http://u:%zz@h.example:1080", want{reason: "format"}},
		{"bad line here", want{reason: "format"}},
	}
	for _, c := range cases {
		got, reason := parseProxyLine(c.line, profile.ProxySOCKS5)
		if reason != c.want.reason {
			t.Errorf("%q: reason %q, want %q", c.line, reason, c.want.reason)
			continue
		}
		if reason != "" {
			continue
		}
		if string(got.Scheme) != c.want.scheme || got.Host != c.want.host || got.Port != c.want.port || got.Username != c.want.user || got.Password != c.want.pass {
			t.Errorf("%q: got %+v, want %+v", c.line, got, c.want)
		}
	}
}

func TestImportProxiesReportsEachBadLine(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	text := "gw.example.net:1080:alice:secret\r\n" + // 1 ok
		"\n# comment\n" + // 2,3 ignored
		"185.44.12.9:8080:bob:hunter2\n" + // 4 ok
		"bad line here\n" + // 5
		"host.example:99999\n" + // 6
		"gw.example.net:1080:alice:other\n" + // 7 duplicate of 1
		"http://u:p@[2001:db8::1]:8080\n" + // 8 ok
		"ftp://x.example:21\n" // 9
	res, err := r.proxies.ImportProxies(ctx, text, "socks5")
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]string{}
	for _, s := range res.Skipped {
		got[s.Line] = s.Reason
	}
	want := map[int]string{5: "format", 6: "port", 7: "duplicate", 9: "scheme"}
	if res.Added != 3 || len(got) != len(want) {
		t.Fatalf("added %d skipped %v", res.Added, got)
	}
	for l, reason := range want {
		if got[l] != reason {
			t.Errorf("line %d: %q, want %q", l, got[l], reason)
		}
	}
	list, _ := r.proxies.ListProxies(ctx)
	if len(list) != 3 {
		t.Fatalf("%d proxies", len(list))
	}
	for _, p := range list {
		if p.Host == "gw.example.net" && (p.Scheme != "socks5" || p.Username != "alice" || !p.HasPassword) {
			t.Errorf("%+v", p)
		}
		if p.Host == "2001:db8::1" && (p.Scheme != "http" || p.Port != 8080) {
			t.Errorf("%+v", p)
		}
	}
	if _, err := r.proxies.ImportProxies(ctx, "x:1", "gopher"); pfCode(err) != "invalid" {
		t.Fatalf("bad default scheme: %v", err)
	}
}

func TestTestProxyReadsExitIPAndLocation(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	pfStartLookup(t, r, "203.0.113.9", "DE", "Europe/Berlin")
	host, port := pfStartProxy(t, "alice", "pw")
	px := r.proxy(host, port, "pw")
	if _, err := r.proxies.UpdateProxy(ctx, px.ID, ProxyInput{Scheme: "http", Host: host, Port: port, Username: "alice"}); err != nil {
		t.Fatal(err)
	}
	res, err := r.proxies.TestProxy(ctx, ProxyTestInput{ID: px.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.IP != "203.0.113.9" || res.Country != "DE" || res.Timezone != "Europe/Berlin" || res.Language != "de-DE" || res.LatencyMs < 1 {
		t.Fatalf("result: %+v", res)
	}
	list, _ := r.proxies.ListProxies(ctx)
	if list[0].Status != "ok" || list[0].IP != "203.0.113.9" || list[0].Country != "DE" {
		t.Fatalf("status was not kept: %+v", list[0])
	}
	if r.events.count("proxy.status") != 1 {
		t.Fatalf("proxy.status events: %d", r.events.count("proxy.status"))
	}

	// A wrong password gives proxy.auth, a dead port gives proxy.down.
	bad, _ := r.proxies.TestProxy(ctx, ProxyTestInput{Scheme: "http", Host: host, Port: port, Username: "alice", Password: "wrong"})
	if bad.OK || bad.Code != "proxy.auth" || bad.Status != "down" {
		t.Fatalf("wrong password: %+v", bad)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	down, _ := r.proxies.TestProxy(ctx, ProxyTestInput{Scheme: "http", Host: "127.0.0.1", Port: dead})
	if down.OK || down.Code != "proxy.down" {
		t.Fatalf("dead port: %+v", down)
	}
	if _, err := r.proxies.TestProxy(ctx, ProxyTestInput{ID: "ghost"}); pfCode(err) != "notFound" {
		t.Fatalf("ghost: %v", err)
	}
	if _, err := r.proxies.TestProxy(ctx, ProxyTestInput{Scheme: "http", Host: "x", Port: 0}); pfCode(err) != "invalid" {
		t.Fatalf("bad input: %v", err)
	}
	// An unsaved test keeps nothing.
	if l, _ := r.proxies.ListProxies(ctx); len(l) != 1 {
		t.Fatal("test must not save")
	}
}

func TestApplyProxyGeoSetsTimezoneAndLanguage(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	pfStartLookup(t, r, "198.51.100.4", "FR", "Europe/Paris")
	host, port := pfStartProxy(t, "", "")
	px := r.proxy(host, port, "")
	if _, err := r.proxies.UpdateProxy(ctx, px.ID, ProxyInput{Scheme: "http", Host: host, Port: port}); err != nil {
		t.Fatal(err)
	}
	p, _ := r.profiles.CreateProfile(ctx, ProfileInput{Name: "P", ProxyID: px.ID})
	if p.Fingerprint.Timezone != "America/New_York" {
		t.Fatalf("default timezone %s", p.Fingerprint.Timezone)
	}
	got, err := r.profiles.ApplyProxyGeo(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Fingerprint.Timezone != "Europe/Paris" || got.Fingerprint.Language != "fr-FR" || got.Fingerprint.Seed != p.Fingerprint.Seed {
		t.Fatalf("%+v", got.Fingerprint)
	}
	noProxy := r.profile("NoProxy")
	if _, err := r.profiles.ApplyProxyGeo(ctx, noProxy.ID); pfCode(err) != "invalid" {
		t.Fatalf("no proxy: %v", err)
	}
	_, _ = r.profiles.StartProfiles(ctx, []string{p.ID})
	if _, err := r.profiles.ApplyProxyGeo(ctx, p.ID); pfCode(err) != "conflict" {
		t.Fatalf("while running: %v", err)
	}
}

func TestHealthCheckFiresEventsAndJobDone(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	pfStartLookup(t, r, "203.0.113.9", "US", "America/New_York")
	host, port := pfStartProxy(t, "", "")
	good := r.proxy(host, port, "")
	r.proxy("127.0.0.1", 1, "") // refuses connections
	job, err := r.proxies.HealthCheck(ctx, nil)
	if err != nil || job == "" {
		t.Fatalf("%q %v", job, err)
	}
	pfEventually(t, "job.done", func() bool { return r.events.count("job.done") == 1 })
	list, _ := r.proxies.ListProxies(ctx)
	byID := map[string]ProxyDTO{}
	for _, p := range list {
		byID[p.ID] = p
	}
	if byID[good.ID].Status != "ok" {
		t.Fatalf("good proxy: %+v", byID[good.ID])
	}
	down := 0
	for _, p := range list {
		if p.Status == "down" {
			down++
		}
	}
	if down != 1 {
		t.Fatalf("down: %d of %+v", down, list)
	}
	// two "checking" and two result events
	if n := r.events.count("proxy.status"); n != 4 {
		t.Fatalf("proxy.status events: %d", n)
	}
	if _, err := r.proxies.HealthCheck(ctx, []string{"ghost"}); pfCode(err) != "notFound" {
		t.Fatalf("ghost: %v", err)
	}
}

func TestLookupSettingValidation(t *testing.T) {
	r := newPfRig(t)
	ctx := context.Background()
	def, _ := r.proxies.GetProxyLookup(ctx)
	if def.IPURL != defaultIPURL || def.GeoURL != defaultGeoURL {
		t.Fatalf("defaults: %+v", def)
	}
	for _, bad := range []ProxyLookup{{IPURL: ""}, {IPURL: "ftp://x"}, {IPURL: "https://ok.example", GeoURL: "nonsense"}} {
		if _, err := r.proxies.SetProxyLookup(ctx, bad); pfCode(err) != "invalid" {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if _, err := r.proxies.SetProxyLookup(ctx, ProxyLookup{IPURL: "https://ip.example/", GeoURL: ""}); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.proxies.GetProxyLookup(ctx); got.GeoURL != "" || got.IPURL != "https://ip.example/" {
		t.Fatalf("saved: %+v", got)
	}
}

func TestParseGeoShapes(t *testing.T) {
	for in, want := range map[string][2]string{
		`{"country_code":"US","timezone":{"id":"America/Chicago"}}`: {"US", "America/Chicago"},
		`{"countryCode":"gb","timezone":"Europe/London"}`:           {"GB", "Europe/London"},
		`{"country":"JP","time_zone":{"name":"Asia/Tokyo"}}`:        {"JP", "Asia/Tokyo"},
		`{"country":"Germany","timezone":"+01:00"}`:                 {"", ""},
		`not json`: {"", ""},
	} {
		cc, tz := parseGeo([]byte(in))
		if cc != want[0] || tz != want[1] {
			t.Errorf("%s: %q %q", in, cc, tz)
		}
	}
}
