package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/8bu/ladilala/core/profile"
)

const (
	testUser = "alice"
	testPass = "s3cret"
)

// backend is the "internet" the fake upstreams connect to. It counts every
// TCP connection so tests can prove nothing reached it directly.
type backend struct {
	srv   *httptest.Server
	conns atomic.Int64
}

func newBackend(t *testing.T) *backend {
	t.Helper()
	b := &backend{}
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello from backend") })
	mux.HandleFunc("/ip", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "203.0.113.7\n") })
	b.srv = httptest.NewUnstartedServer(mux)
	b.srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			b.conns.Add(1)
		}
	}
	b.srv.Start()
	t.Cleanup(b.srv.Close)
	return b
}

func (b *backend) addr() string { return b.srv.Listener.Addr().String() }

type fakeUpstream struct {
	proxy profile.Proxy
	tls   *x509.CertPool
	close func()

	mu      sync.Mutex
	targets []string
}

func (f *fakeUpstream) record(target string) {
	f.mu.Lock()
	f.targets = append(f.targets, target)
	f.mu.Unlock()
}

func (f *fakeUpstream) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.targets...)
}

func splitProxy(t *testing.T, scheme profile.ProxyScheme, addr string) profile.Proxy {
	t.Helper()
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	return profile.Proxy{Scheme: scheme, Host: host, Port: p, Username: testUser, Password: testPass}
}

func bicopy(a net.Conn, aR io.Reader, b net.Conn) {
	done := make(chan struct{})
	go func() { io.Copy(b, aR); closeWrite(b); close(done) }()
	io.Copy(a, b)
	closeWrite(a)
	<-done
	a.Close()
	b.Close()
}

// newFakeHTTP is an HTTP(S) proxy requiring Basic auth. Every target is mapped
// to the backend; the requested host:port is recorded as received.
func newFakeHTTP(t *testing.T, useTLS bool, be *backend) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probe := &http.Request{Header: http.Header{"Authorization": {r.Header.Get("Proxy-Authorization")}}}
		if u, p, ok := probe.BasicAuth(); !ok || u != testUser || p != testPass {
			w.Header().Set("Proxy-Authenticate", `Basic realm="x"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		if r.Method == http.MethodConnect {
			f.record(r.Host)
			up, err := net.Dial("tcp", be.addr())
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			c, brw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				up.Close()
				return
			}
			io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
			bicopy(c, brw.Reader, up)
			return
		}
		f.record(r.URL.Host)
		out := r.Clone(r.Context())
		out.URL.Scheme, out.URL.Host, out.RequestURI = "http", be.addr(), ""
		out.Header.Del("Proxy-Authorization")
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	scheme := profile.ProxyHTTP
	if useTLS {
		srv.StartTLS()
		scheme = profile.ProxyHTTPS
		f.tls = x509.NewCertPool()
		f.tls.AddCert(srv.Certificate())
	} else {
		srv.Start()
	}
	f.close = srv.Close
	f.proxy = splitProxy(t, scheme, srv.Listener.Addr().String())
	t.Cleanup(srv.Close)
	return f
}

// newFakeSOCKS is a minimal RFC1928/1929 server (username/password only).
func newFakeSOCKS(t *testing.T, be *backend) *fakeUpstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeUpstream{close: func() { ln.Close() }}
	f.proxy = splitProxy(t, profile.ProxySOCKS5, ln.Addr().String())
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serveSOCKS(c, be)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeUpstream) serveSOCKS(c net.Conn, be *backend) {
	defer c.Close()
	br := bufio.NewReader(c)
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	hasUserPass := false
	for _, m := range methods {
		hasUserPass = hasUserPass || m == 0x02
	}
	if !hasUserPass {
		c.Write([]byte{5, 0xff})
		return
	}
	c.Write([]byte{5, 2})
	vl := make([]byte, 2)
	if _, err := io.ReadFull(br, vl[:2]); err != nil {
		return
	}
	user := make([]byte, vl[1])
	io.ReadFull(br, user)
	pl, _ := br.ReadByte()
	pass := make([]byte, pl)
	io.ReadFull(br, pass)
	if string(user) != testUser || string(pass) != testPass {
		c.Write([]byte{1, 1})
		return
	}
	c.Write([]byte{1, 0})

	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return
	}
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		io.ReadFull(br, ip)
		host = net.IP(ip).String()
	case 3:
		n, _ := br.ReadByte()
		name := make([]byte, n)
		io.ReadFull(br, name)
		host = string(name)
	case 4:
		ip := make([]byte, 16)
		io.ReadFull(br, ip)
		host = net.IP(ip).String()
	}
	pb := make([]byte, 2)
	io.ReadFull(br, pb)
	f.record(net.JoinHostPort(host, strconv.Itoa(int(pb[0])<<8|int(pb[1]))))
	up, err := net.Dial("tcp", be.addr())
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	bicopy(c, br, up)
}

type upstreamKind struct {
	name  string
	start func(*testing.T, *backend) *fakeUpstream
}

var kinds = []upstreamKind{
	{"http", func(t *testing.T, b *backend) *fakeUpstream { return newFakeHTTP(t, false, b) }},
	{"https", func(t *testing.T, b *backend) *fakeUpstream { return newFakeHTTP(t, true, b) }},
	{"socks5", newFakeSOCKS},
}

func tlsConfig(pool *x509.CertPool) *tls.Config { return &tls.Config{RootCAs: pool} }

func startRelay(t *testing.T, f *fakeUpstream, opts ...Option) (*Relay, string) {
	t.Helper()
	if f.tls != nil {
		opts = append(opts, WithUpstreamTLS(tlsConfig(f.tls)))
	}
	return startRelayFor(t, f.proxy, opts...)
}

func startRelayFor(t *testing.T, p profile.Proxy, opts ...Option) (*Relay, string) {
	t.Helper()
	r, err := New(p, opts...)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := r.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r, addr
}

// connectVia sends CONNECT through the relay and returns the status code and
// the connection positioned after the response headers.
func connectVia(t *testing.T, relayAddr, target string) (net.Conn, *bufio.Reader, int) {
	t.Helper()
	c, err := net.Dial("tcp", relayAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	return c, br, resp.StatusCode
}

func getThroughTunnel(t *testing.T, c net.Conn, br *bufio.Reader, path string) string {
	t.Helper()
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: service.test\r\nConnection: close\r\n\r\n", path)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestConnectSuccessHostnameUnresolved(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			be := newBackend(t)
			f := k.start(t, be)
			r, addr := startRelay(t, f)

			c, br, status := connectVia(t, addr, "service.test:443")
			if status != 200 {
				t.Fatalf("status = %d", status)
			}
			if got := getThroughTunnel(t, c, br, "/hello"); got != "hello from backend" {
				t.Fatalf("body = %q", got)
			}
			// The upstream must have received the name, not a locally resolved IP.
			if got := f.seen(); len(got) != 1 || got[0] != "service.test:443" {
				t.Fatalf("upstream saw %v", got)
			}
			c.Close()
			eventually(t, "stats", func() bool {
				s := r.Stats()
				return s.OpenConns == 0 && s.BytesUp > 0 && s.BytesDown > 0 && s.TotalConns == 1
			})
		})
	}
}

func TestPlainHTTPForward(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			be := newBackend(t)
			f := k.start(t, be)
			_, addr := startRelay(t, f)
			pu, _ := url.Parse("http://" + addr)
			client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 10 * time.Second}
			resp, err := client.Get("http://service.test/hello")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || string(body) != "hello from backend" {
				t.Fatalf("got %d %q", resp.StatusCode, body)
			}
			if got := f.seen(); len(got) != 1 || got[0] != "service.test:80" && got[0] != "service.test" {
				t.Fatalf("upstream saw %v", got)
			}
		})
	}
}

func TestBadCredentialsFailClosed(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			be := newBackend(t)
			f := k.start(t, be)
			f.proxy.Password = "wrong"
			r, addr := startRelay(t, f)

			_, _, status := connectVia(t, addr, be.addr())
			if status != http.StatusBadGateway {
				t.Fatalf("CONNECT status = %d, want 502", status)
			}
			pu, _ := url.Parse("http://" + addr)
			client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 10 * time.Second}
			resp, err := client.Get("http://" + be.addr() + "/hello")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("GET status = %d, want 502", resp.StatusCode)
			}
			if r.Stats().LastError == "" {
				t.Fatal("LastError not recorded")
			}
			time.Sleep(100 * time.Millisecond)
			if n := be.conns.Load(); n != 0 {
				t.Fatalf("backend got %d connections despite refused auth", n)
			}
			if _, err := r.Check(context.Background()); err == nil {
				t.Fatal("Check succeeded with bad credentials")
			}
		})
	}
}

func TestUpstreamDownNeverDialsDirect(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			be := newBackend(t)
			f := k.start(t, be)
			f.close() // upstream is gone; its port now refuses connections
			r, addr := startRelay(t, f)

			// The target is the real, reachable backend: a direct dial would succeed.
			_, _, status := connectVia(t, addr, be.addr())
			if status != http.StatusBadGateway {
				t.Fatalf("CONNECT status = %d, want 502", status)
			}
			pu, _ := url.Parse("http://" + addr)
			client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 10 * time.Second}
			resp, err := client.Get("http://" + be.addr() + "/hello")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("GET status = %d, want 502", resp.StatusCode)
			}
			time.Sleep(100 * time.Millisecond)
			if n := be.conns.Load(); n != 0 {
				t.Fatalf("backend received %d direct connections", n)
			}
			if r.Stats().LastError == "" {
				t.Fatal("LastError not recorded")
			}
		})
	}
}

func TestCheck(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			be := newBackend(t)
			f := k.start(t, be)
			r, _ := startRelay(t, f, WithCheckURL("http://echo.test:8080/ip"))
			res, err := r.Check(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if res.IP != "203.0.113.7" || res.Latency < 0 || res.Err != "" {
				t.Fatalf("result = %+v", res)
			}
			if got := f.seen(); len(got) != 1 || got[0] != "echo.test:8080" {
				t.Fatalf("upstream saw %v", got)
			}
		})
	}
}

func TestCleanShutdown(t *testing.T) {
	be := newBackend(t)
	f := newFakeSOCKS(t, be)
	r, addr := startRelay(t, f)
	c, br, status := connectVia(t, addr, "service.test:443")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if got := getThroughTunnel(t, c, br, "/hello"); got == "" {
		t.Fatal("empty body")
	}
	// Idle open tunnel must be torn down by Close.
	c2, br2, _ := connectVia(t, addr, "service.test:443")

	done := make(chan struct{})
	go func() { r.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung")
	}
	c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := br2.ReadByte(); err == nil {
		t.Fatal("tunnel still readable after Close")
	}
	if s := r.Stats(); s.OpenConns != 0 {
		t.Fatalf("OpenConns = %d", s.OpenConns)
	}
	if _, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		t.Fatal("listener still accepting after Close")
	}
	r.Close() // idempotent
	if _, err := r.Start(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Start after Close = %v", err)
	}
}

func TestContextCancelShutsDown(t *testing.T) {
	be := newBackend(t)
	f := newFakeSOCKS(t, be)
	r, err := New(f.proxy)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	addr, err := r.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, br, _ := connectVia(t, addr, "service.test:443")
	cancel()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := br.ReadByte(); err == nil {
		t.Fatal("tunnel survived context cancel")
	}
	eventually(t, "listener closed", func() bool {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
		}
		return err != nil
	})
}

func TestDirectRequiresOptIn(t *testing.T) {
	if _, err := New(profile.Proxy{}); !errors.Is(err, ErrDirectNotAllowed) {
		t.Fatalf("New(no proxy) = %v, want ErrDirectNotAllowed", err)
	}
	be := newBackend(t)
	r, addr := startRelayFor(t, profile.Proxy{}, WithAllowDirect(), WithCheckURL(be.srv.URL+"/ip"))
	c, br, status := connectVia(t, addr, be.addr())
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if got := getThroughTunnel(t, c, br, "/hello"); got != "hello from backend" {
		t.Fatalf("body = %q", got)
	}
	if res, err := r.Check(context.Background()); err != nil || res.IP != "203.0.113.7" {
		t.Fatalf("Check = %+v, %v", res, err)
	}
}

func TestNewValidation(t *testing.T) {
	for _, p := range []profile.Proxy{
		{Scheme: profile.ProxySOCKS5, Port: 1080},
		{Scheme: profile.ProxyHTTP, Host: "h", Port: 0},
		{Scheme: "ftp", Host: "h", Port: 21},
	} {
		if _, err := New(p); err == nil {
			t.Errorf("New(%+v) succeeded", p)
		}
	}
}

func TestMaxConns(t *testing.T) {
	be := newBackend(t)
	f := newFakeSOCKS(t, be)
	_, addr := startRelay(t, f, WithMaxConns(1))
	_, _, status := connectVia(t, addr, "service.test:443")
	if status != 200 {
		t.Fatalf("first status = %d", status)
	}
	_, _, status = connectVia(t, addr, "service.test:443")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want 503", status)
	}
}

func TestIdleTimeout(t *testing.T) {
	be := newBackend(t)
	f := newFakeSOCKS(t, be)
	_, addr := startRelay(t, f, WithIdleTimeout(150*time.Millisecond))
	c, br, status := connectVia(t, addr, "service.test:443")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err := br.ReadByte()
	if err == nil {
		t.Fatal("unexpected data on idle tunnel")
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("client timed out before relay closed idle tunnel")
	}
}

func TestRejectsNonProxyRequest(t *testing.T) {
	be := newBackend(t)
	f := newFakeSOCKS(t, be)
	_, addr := startRelay(t, f)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "GET /hello HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if be.conns.Load() != 0 {
		t.Fatal("origin-form request reached the backend")
	}
}
