// Package proxy implements a per-profile loopback relay. Chrome cannot speak
// SOCKS5 with username/password, so the browser is pointed at this relay as a
// plain HTTP proxy and the relay authenticates to the real upstream.
//
// The relay fails closed: any upstream failure is reported to the client as a
// 502 and the target is never dialled directly (unless the profile has no
// proxy and the caller opted in with WithAllowDirect).
package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/8bu/ladilala/core/profile"
)

const (
	defaultCheckURL    = "https://api.ipify.org"
	defaultMaxConns    = 256
	defaultIdleTimeout = 2 * time.Minute
	defaultDialTimeout = 20 * time.Second
	headerTimeout      = 30 * time.Second
)

var (
	// ErrDirectNotAllowed is returned when the profile has no upstream proxy
	// and the relay was not explicitly allowed to connect directly.
	ErrDirectNotAllowed = errors.New("proxy: profile has no upstream proxy and direct connections are not allowed")
	// ErrUpstreamAuth means the upstream proxy answered 407.
	ErrUpstreamAuth = errors.New("proxy: upstream refused credentials")
	// ErrClosed is returned after Close.
	ErrClosed = errors.New("proxy: relay closed")
)

// Option configures a Relay.
type Option func(*Relay)

// WithAllowDirect lets a ProxyNone profile run through the relay without any
// upstream. Without it New refuses, so a missing proxy can never silently
// expose the real IP.
func WithAllowDirect() Option { return func(r *Relay) { r.allowDirect = true } }

// WithCheckURL sets the IP echo endpoint used by Check.
func WithCheckURL(u string) Option { return func(r *Relay) { r.checkURL = u } }

// WithMaxConns caps simultaneous client connections; extra ones get a 503.
func WithMaxConns(n int) Option { return func(r *Relay) { r.maxConns = n } }

// WithIdleTimeout closes a tunnel after this long without traffic in either
// direction.
func WithIdleTimeout(d time.Duration) Option { return func(r *Relay) { r.idle = d } }

// WithDialTimeout bounds connecting to and handshaking with the upstream.
func WithDialTimeout(d time.Duration) Option { return func(r *Relay) { r.dialTimeout = d } }

// WithUpstreamTLS sets the TLS config used to reach an https upstream proxy.
func WithUpstreamTLS(cfg *tls.Config) Option { return func(r *Relay) { r.tlsCfg = cfg } }

// Stats is a snapshot of relay counters.
type Stats struct {
	BytesUp     int64 // client -> upstream
	BytesDown   int64 // upstream -> client
	OpenConns   int64
	TotalConns  int64
	LastError   string // last upstream failure, empty if none
	LastErrorAt time.Time
}

// Relay is a loopback HTTP proxy that forwards through one upstream proxy.
type Relay struct {
	upstream    profile.Proxy
	allowDirect bool
	checkURL    string
	maxConns    int
	idle        time.Duration
	dialTimeout time.Duration
	tlsCfg      *tls.Config

	dial dialFunc
	ctx  context.Context
	stop context.CancelFunc
	sem  chan struct{}
	wg   sync.WaitGroup

	mu        sync.Mutex
	ln        net.Listener
	closed    bool
	conns     map[net.Conn]struct{}
	lastErr   string
	lastErrAt time.Time

	up, down, open, total atomic.Int64
}

// New validates the upstream and builds a relay. It does not listen yet.
func New(upstream profile.Proxy, opts ...Option) (*Relay, error) {
	r := &Relay{
		upstream:    upstream,
		checkURL:    defaultCheckURL,
		maxConns:    defaultMaxConns,
		idle:        defaultIdleTimeout,
		dialTimeout: defaultDialTimeout,
		conns:       make(map[net.Conn]struct{}),
	}
	for _, o := range opts {
		o(r)
	}
	if r.maxConns < 1 || r.idle <= 0 || r.dialTimeout <= 0 {
		return nil, errors.New("proxy: maxConns, idle and dial timeouts must be positive")
	}
	if upstream.Scheme == profile.ProxyNone {
		if !r.allowDirect {
			return nil, ErrDirectNotAllowed
		}
	} else if upstream.Host == "" || upstream.Port < 1 || upstream.Port > 65535 {
		return nil, fmt.Errorf("proxy: invalid upstream %s:%d", upstream.Host, upstream.Port)
	}
	d, err := r.newDialer()
	if err != nil {
		return nil, err
	}
	r.dial = d
	r.sem = make(chan struct{}, r.maxConns)
	r.ctx, r.stop = context.WithCancel(context.Background())
	return r, nil
}

// Start listens on a random loopback port and returns its host:port. The relay
// shuts down when ctx is cancelled or Close is called.
func (r *Relay) Start(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return "", ErrClosed
	}
	if r.ln != nil {
		return "", errors.New("proxy: relay already started")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	r.ln = ln
	r.wg.Add(1)
	go r.acceptLoop(ln)
	go func() {
		select {
		case <-ctx.Done():
			r.Close()
		case <-r.ctx.Done():
		}
	}()
	return ln.Addr().String(), nil
}

// Close stops listening, tears down every tunnel and waits for handlers.
func (r *Relay) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.stop()
	if r.ln != nil {
		r.ln.Close()
	}
	for c := range r.conns {
		c.Close()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

// Stats returns a snapshot of the counters.
func (r *Relay) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Stats{
		BytesUp:     r.up.Load(),
		BytesDown:   r.down.Load(),
		OpenConns:   r.open.Load(),
		TotalConns:  r.total.Load(),
		LastError:   r.lastErr,
		LastErrorAt: r.lastErrAt,
	}
}

func (r *Relay) noteErr(err error) {
	r.mu.Lock()
	r.lastErr, r.lastErrAt = err.Error(), time.Now()
	r.mu.Unlock()
}

// track registers c so Close can tear it down; false means the relay is
// already closed and c has been closed.
func (r *Relay) track(c net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		c.Close()
		return false
	}
	r.conns[c] = struct{}{}
	return true
}

func (r *Relay) release(c net.Conn) {
	r.mu.Lock()
	delete(r.conns, c)
	r.mu.Unlock()
	c.Close()
}

func (r *Relay) acceptLoop(ln net.Listener) {
	defer r.wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-r.ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		select {
		case r.sem <- struct{}{}:
		default:
			if !r.track(c) {
				continue
			}
			// Draining can take up to drainTimeout, so keep it off the accept loop.
			r.wg.Add(1)
			go func() {
				defer r.wg.Done()
				defer r.release(c)
				writeStatus(c, http.StatusServiceUnavailable, "too many connections")
			}()
			continue
		}
		if !r.track(c) {
			<-r.sem
			continue
		}
		r.wg.Add(1)
		r.open.Add(1)
		r.total.Add(1)
		go func() {
			defer func() {
				r.release(c)
				r.open.Add(-1)
				<-r.sem
				r.wg.Done()
			}()
			r.handle(c)
		}()
	}
}

func (r *Relay) handle(c net.Conn) {
	br := bufio.NewReader(c)
	c.SetReadDeadline(time.Now().Add(headerTimeout))
	req, err := http.ReadRequest(br)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			writeStatus(c, http.StatusBadRequest, "bad request")
		}
		return
	}
	c.SetReadDeadline(time.Time{})
	if req.Method == http.MethodConnect {
		r.connect(c, br, req)
	} else {
		r.forward(c, br, req)
	}
}

func (r *Relay) connect(c net.Conn, br *bufio.Reader, req *http.Request) {
	target, ok := normalizeHostPort(req.Host, "")
	if !ok {
		writeStatus(c, http.StatusBadRequest, "bad CONNECT target")
		return
	}
	up, err := r.dialTarget(r.ctx, target)
	if err != nil {
		writeStatus(c, http.StatusBadGateway, "upstream proxy error: "+err.Error())
		return
	}
	defer r.release(up)
	if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	r.pipe(c, br, up)
}

// forward handles absolute-URI requests (plain http://). HTTP upstreams get the
// request as-is so CONNECT port restrictions do not apply; SOCKS5 gets a
// tunnel and an origin-form request.
func (r *Relay) forward(c net.Conn, br *bufio.Reader, req *http.Request) {
	if !req.URL.IsAbs() || req.URL.Scheme != "http" {
		writeStatus(c, http.StatusBadRequest, "proxy requests must use an absolute http:// URI or CONNECT")
		return
	}
	target, ok := normalizeHostPort(req.URL.Host, "80")
	if !ok {
		writeStatus(c, http.StatusBadRequest, "bad request target")
		return
	}
	upgrade := req.Header.Get("Upgrade") != ""
	req.Header.Del("Proxy-Connection")
	req.Header.Del("Proxy-Authorization")
	if !upgrade {
		req.Header.Del("Connection")
		req.Close = true
	}

	var up net.Conn
	var err error
	viaHTTPProxy := r.upstream.Scheme == profile.ProxyHTTP || r.upstream.Scheme == profile.ProxyHTTPS
	if viaHTTPProxy {
		up, err = r.dialProxyTracked(r.ctx)
		if err == nil && r.upstream.Username != "" {
			req.Header.Set("Proxy-Authorization", basicAuth(r.upstream.Username, r.upstream.Password))
		}
	} else {
		up, err = r.dialTarget(r.ctx, target)
	}
	if err != nil {
		writeStatus(c, http.StatusBadGateway, "upstream proxy error: "+err.Error())
		return
	}
	defer r.release(up)

	up.SetWriteDeadline(time.Now().Add(r.idle))
	if viaHTTPProxy {
		err = req.WriteProxy(up)
	} else {
		err = req.Write(up)
	}
	up.SetWriteDeadline(time.Time{})
	if err != nil {
		r.noteErr(err)
		writeStatus(c, http.StatusBadGateway, "upstream proxy error: "+err.Error())
		return
	}
	if viaHTTPProxy {
		// Never relay the upstream's 407 to the browser: it would pop a login
		// prompt for a proxy the user cannot see.
		ur := bufio.NewReader(up)
		up.SetReadDeadline(time.Now().Add(r.idle))
		if head, _ := ur.Peek(12); bytes.HasPrefix(head, []byte("HTTP/1.1 407")) || bytes.HasPrefix(head, []byte("HTTP/1.0 407")) {
			r.noteErr(ErrUpstreamAuth)
			writeStatus(c, http.StatusBadGateway, "upstream proxy error: "+ErrUpstreamAuth.Error())
			return
		}
		up.SetReadDeadline(time.Time{})
		up = &bufConn{Conn: up, r: ur}
	}
	if upgrade {
		r.pipe(c, br, up)
		return
	}
	// Connection: close was requested, so the upstream ends the response.
	t := newTunnel(c, up, r.idle)
	r.copyCounted(t, c, up, &r.down)
}

func (r *Relay) dialProxyTracked(ctx context.Context) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, r.dialTimeout)
	defer cancel()
	c, err := r.dialProxy(ctx)
	if err != nil {
		r.noteErr(err)
		return nil, err
	}
	if !r.track(c) {
		return nil, ErrClosed
	}
	return c, nil
}

// dialTarget opens a tunnel to addr (host:port) through the upstream. Hostnames
// are passed through unresolved so DNS happens on the upstream side.
func (r *Relay) dialTarget(ctx context.Context, addr string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, r.dialTimeout)
	defer cancel()
	c, err := r.dial(ctx, addr)
	if err != nil {
		r.noteErr(err)
		return nil, err
	}
	if !r.track(c) {
		return nil, ErrClosed
	}
	return c, nil
}

func normalizeHostPort(hostport, defaultPort string) (string, bool) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		if defaultPort == "" {
			return "", false
		}
		host, port = hostport, defaultPort
		if len(host) > 1 && host[0] == '[' && host[len(host)-1] == ']' {
			host = host[1 : len(host)-1]
		}
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || host == "" {
		return "", false
	}
	return net.JoinHostPort(host, port), true
}

// writeStatus sends a final error response and then shuts the connection down
// gracefully. Closing a socket that still has unread request bytes makes
// Windows (and sometimes Linux) send RST, which discards the response the
// client has not read yet. So: half-close, discard what the client has queued
// (bounded in time and size), and let the caller's close follow.
func writeStatus(c net.Conn, code int, msg string) {
	c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(c, "HTTP/1.1 %d %s\r\nConnection: close\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\n\r\n%s",
		code, http.StatusText(code), len(msg), msg)
	closeWrite(c)
	c.SetReadDeadline(time.Now().Add(drainTimeout))
	io.Copy(io.Discard, io.LimitReader(c, drainLimit))
}

const (
	drainTimeout = time.Second
	drainLimit   = 1 << 20
)
