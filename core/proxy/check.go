package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// CheckResult is the outcome of one upstream health check.
type CheckResult struct {
	IP      string        `json:"ip"`      // exit IP reported by the echo endpoint
	Latency time.Duration `json:"latency"` // full request time through the upstream
	Err     string        `json:"err,omitempty"`
}

// Check requests the IP echo endpoint through the upstream (never directly,
// unless AllowDirect with no proxy) and reports the exit IP. On failure the
// returned error is non-nil and CheckResult.Err carries its message, so UI
// code can use either. The relay does not need to be started.
func (r *Relay) Check(ctx context.Context) (CheckResult, error) {
	start := time.Now()
	ip, err := r.fetchIP(ctx)
	res := CheckResult{IP: ip, Latency: time.Since(start)}
	if err != nil {
		res.Err = err.Error()
		return res, err
	}
	return res, nil
}

func (r *Relay) fetchIP(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*r.dialTimeout)
	defer cancel()
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return r.dialTarget(ctx, addr)
		},
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: r.dialTimeout,
	}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.checkURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("proxy: check endpoint returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(body))
	if _, err := netip.ParseAddr(ip); err != nil {
		return "", fmt.Errorf("proxy: check endpoint did not return an IP: %q", ip)
	}
	return ip, nil
}
