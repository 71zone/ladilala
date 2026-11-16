// Package driver launches isolated Chrome-family browser processes for
// profiles and hands back a chromedp context bound to each one.
package driver

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/8bu/ladilala/core/profile"
)

// Brand identifies the vendor of an installed browser. Policy keys and
// default user-data directories depend on it.
const (
	BrandChrome   = "chrome"
	BrandEdge     = "edge"
	BrandChromium = "chromium"
)

// Install is one browser binary found on disk.
type Install struct {
	Path  string
	Brand string
}

// Typed launch failures. Callers match them with errors.Is to show an
// actionable message instead of a raw chromedp error.
var (
	// ErrBinaryNotFound means the browser executable does not exist.
	ErrBinaryNotFound = errors.New("driver: browser binary not found")
	// ErrDebuggingBlocked means an enterprise policy forbids remote debugging,
	// so no automation can attach to this browser.
	ErrDebuggingBlocked = errors.New("driver: remote debugging is blocked by policy")
	// ErrProfileLocked means another browser process already uses the profile's
	// data directory.
	ErrProfileLocked = errors.New("driver: profile data directory is in use")
	// ErrInvalidDataDir means the data directory is empty, relative, or one of
	// the default Chrome user-data directories.
	ErrInvalidDataDir = errors.New("driver: invalid profile data directory")
	// ErrProxyRequired means the profile has a proxy but no relay address was
	// given; launching would silently leak the real IP.
	ErrProxyRequired = errors.New("driver: profile has a proxy but no relay address was given")
	// ErrNoFingerprintSeed means the profile fingerprint was never generated.
	ErrNoFingerprintSeed = errors.New("driver: profile fingerprint has no seed")
)

// BrowserEngine is one browser build the launcher can drive.
type BrowserEngine interface {
	Name() string
	// Detect lists the usable binaries of this engine, best first.
	Detect() ([]Install, error)
	// Args returns the engine-specific command-line flags for p, including the
	// proxy flag when proxyAddr is set. Generic launch flags (data dir,
	// first-run suppression) are added by the Launcher.
	Args(p profile.Profile, proxyAddr string) ([]string, error)
	// Version returns the four-part product version of the binary.
	Version(Install) (string, error)
}

// proxyFlag builds --proxy-server for the local relay. Chrome cannot send
// proxy credentials from a flag, so the relay must handle upstream auth and
// userinfo here would be silently ignored.
func proxyFlag(addr string) ([]string, error) {
	if addr == "" {
		return nil, nil
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("driver: proxy address %q: %w", addr, err)
	}
	if u.User != nil {
		return nil, fmt.Errorf("driver: proxy address %q must not carry credentials", addr)
	}
	if _, _, err := net.SplitHostPort(u.Host); err != nil {
		return nil, fmt.Errorf("driver: proxy address %q needs host:port: %w", addr, err)
	}
	return []string{"--proxy-server=" + u.Scheme + "://" + u.Host}, nil
}

// requireProxy rejects a configured proxy with no relay address.
func requireProxy(p profile.Profile, proxyAddr string) error {
	if p.Proxy.Scheme != profile.ProxyNone && proxyAddr == "" {
		return ErrProxyRequired
	}
	return nil
}
