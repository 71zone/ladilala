package driver

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/8bu/ladilala/core/profile"
)

// SystemChrome drives the Chrome or Edge installed on the machine. A stock
// browser has no fingerprint flags, so Args only sets the language and proxy;
// the user agent, client hints, screen, hardware threads and timezone are
// applied afterwards over CDP by EmulationPlan, once per target.
type SystemChrome struct{}

func (SystemChrome) Name() string { return "system-chrome" }

// Detect lists Chrome installs first, then Edge.
func (SystemChrome) Detect() ([]Install, error) {
	cands := systemCandidates(runtime.GOOS, os.Getenv, homeDir())
	cands = append(registryInstalls(), cands...)
	found := existingInstalls(cands, caseInsensitiveFS(runtime.GOOS))
	// Registry hits arrive first; keep Chrome ahead of Edge regardless.
	sort.SliceStable(found, func(a, b int) bool {
		return found[a].Brand == BrandChrome && found[b].Brand != BrandChrome
	})
	return found, nil
}

func (SystemChrome) Version(i Install) (string, error) { return binaryVersion(i.Path) }

func (SystemChrome) Args(p profile.Profile, proxyAddr string) ([]string, error) {
	if err := requireProxy(p, proxyAddr); err != nil {
		return nil, err
	}
	// Chrome sets navigator.webdriver while a debugger is attached unless the
	// AutomationControlled blink feature is off.
	args := []string{"--disable-blink-features=AutomationControlled"}
	args = appendIf(args, "--lang=", p.Fingerprint.Language)
	proxy, err := proxyFlag(proxyAddr)
	if err != nil {
		return nil, err
	}
	return append(args, proxy...), nil
}

// systemCandidates lists the standard install paths for goos, Chrome before
// Edge. env and home are injected so the lists can be tested without the OS.
func systemCandidates(goos string, env func(string) string, home string) []Install {
	var out []Install
	add := func(brand string, parts ...string) {
		out = append(out, Install{Path: filepath.Join(parts...), Brand: brand})
	}
	switch goos {
	case "windows":
		roots := []string{env("PROGRAMFILES"), env("PROGRAMFILES(X86)"), env("LOCALAPPDATA")}
		for _, r := range roots {
			if r != "" {
				add(BrandChrome, r, "Google", "Chrome", "Application", "chrome.exe")
			}
		}
		for _, r := range roots {
			if r != "" {
				add(BrandEdge, r, "Microsoft", "Edge", "Application", "msedge.exe")
			}
		}
	case "darwin":
		for _, root := range []string{"/Applications", filepath.Join(home, "Applications")} {
			add(BrandChrome, root, "Google Chrome.app", "Contents", "MacOS", "Google Chrome")
		}
		for _, root := range []string{"/Applications", filepath.Join(home, "Applications")} {
			add(BrandEdge, root, "Microsoft Edge.app", "Contents", "MacOS", "Microsoft Edge")
		}
	default:
		for _, p := range []string{"/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/opt/google/chrome/chrome"} {
			add(BrandChrome, p)
		}
		for _, p := range []string{"/usr/bin/microsoft-edge", "/usr/bin/microsoft-edge-stable", "/opt/microsoft/msedge/msedge"} {
			add(BrandEdge, p)
		}
	}
	return out
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func caseInsensitiveFS(goos string) bool { return goos == "windows" || goos == "darwin" }

// existingInstalls keeps the candidates that are regular files and drops
// duplicates, so a registry hit and a Program Files hit count once.
func existingInstalls(cands []Install, foldCase bool) []Install {
	var out []Install
	for _, c := range cands {
		if st, err := os.Stat(c.Path); err == nil && st.Mode().IsRegular() {
			out = append(out, c)
		}
	}
	return dedupeFold(out, foldCase)
}

func dedupe(in []Install) []Install { return dedupeFold(in, caseInsensitiveFS(runtime.GOOS)) }

func dedupeFold(in []Install, foldCase bool) []Install {
	seen := map[string]bool{}
	var out []Install
	for _, i := range in {
		k := filepath.Clean(i.Path)
		if foldCase {
			k = strings.ToLower(k)
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, i)
		}
	}
	return out
}
