package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/net/proxy"

	"github.com/8bu/ladilala/core/profile"
)

type dialFunc func(ctx context.Context, addr string) (net.Conn, error)

func (r *Relay) proxyAddr() string {
	return net.JoinHostPort(r.upstream.Host, strconv.Itoa(r.upstream.Port))
}

func (r *Relay) newDialer() (dialFunc, error) {
	nd := &net.Dialer{Timeout: r.dialTimeout}
	switch r.upstream.Scheme {
	case profile.ProxyNone:
		return func(ctx context.Context, addr string) (net.Conn, error) {
			return nd.DialContext(ctx, "tcp", addr)
		}, nil
	case profile.ProxySOCKS5:
		var auth *proxy.Auth
		if r.upstream.Username != "" {
			auth = &proxy.Auth{User: r.upstream.Username, Password: r.upstream.Password}
		}
		d, err := proxy.SOCKS5("tcp", r.proxyAddr(), auth, nd)
		if err != nil {
			return nil, err
		}
		cd, ok := d.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("proxy: socks5 dialer lacks context support")
		}
		// The library sends non-IP hosts as SOCKS5 domain names (ATYP 3), so the
		// proxy resolves them.
		return func(ctx context.Context, addr string) (net.Conn, error) {
			return cd.DialContext(ctx, "tcp", addr)
		}, nil
	case profile.ProxyHTTP, profile.ProxyHTTPS:
		return func(ctx context.Context, addr string) (net.Conn, error) {
			c, err := r.dialProxy(ctx)
			if err != nil {
				return nil, err
			}
			tc, err := r.connectHandshake(ctx, c, addr)
			if err != nil {
				c.Close()
				return nil, err
			}
			return tc, nil
		}, nil
	default:
		return nil, fmt.Errorf("proxy: unsupported upstream scheme %q", r.upstream.Scheme)
	}
}

// dialProxy connects to the HTTP(S) upstream proxy itself.
func (r *Relay) dialProxy(ctx context.Context) (net.Conn, error) {
	nd := &net.Dialer{Timeout: r.dialTimeout}
	if r.upstream.Scheme == profile.ProxyHTTPS {
		cfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if r.tlsCfg != nil {
			cfg = r.tlsCfg.Clone()
		}
		return (&tls.Dialer{NetDialer: nd, Config: cfg}).DialContext(ctx, "tcp", r.proxyAddr())
	}
	return nd.DialContext(ctx, "tcp", r.proxyAddr())
}

func basicAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// connectHandshake asks an HTTP upstream to tunnel to addr.
func (r *Relay) connectHandshake(ctx context.Context, c net.Conn, addr string) (net.Conn, error) {
	if d, ok := ctx.Deadline(); ok {
		c.SetDeadline(d)
	}
	stop := context.AfterFunc(ctx, func() { c.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	hdr := "CONNECT " + addr + " HTTP/1.1\r\nHost: " + addr + "\r\n"
	if r.upstream.Username != "" {
		hdr += "Proxy-Authorization: " + basicAuth(r.upstream.Username, r.upstream.Password) + "\r\n"
	}
	if _, err := io.WriteString(c, hdr+"\r\n"); err != nil {
		return nil, ctxOr(ctx, err)
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, ctxOr(ctx, err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusProxyAuthRequired {
		return nil, ErrUpstreamAuth
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("proxy: upstream CONNECT to %s failed: %s", addr, resp.Status)
	}
	if !stop() {
		return nil, ctxOr(ctx, io.ErrUnexpectedEOF)
	}
	c.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufConn{Conn: c, r: br}, nil
	}
	return c, nil
}

func ctxOr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// bufConn keeps bytes the handshake reader already pulled off the socket.
type bufConn struct {
	net.Conn
	r io.Reader
}

func (b *bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func (b *bufConn) CloseWrite() error { return closeWrite(b.Conn) }
