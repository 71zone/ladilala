package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
)

// CfTVersionsURL lists the Chrome for Testing channels with their download
// URLs. The JSON carries no checksums: verify a download by HTTPS, the
// Content-Length of the zip, and a SHA-256 recorded on first download.
const CfTVersionsURL = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"

// CfTBuild is one downloadable Chrome for Testing build.
type CfTBuild struct {
	Version  string // four-part, e.g. "157.0.8103.78"
	Revision string
	Channel  string // "Stable", "Beta", "Dev" or "Canary"; set by ParseCfTVersions
	Platform string // "win64", "mac-arm64", "linux64"
	URL      string // zip with the chrome binary
}

// CfTPlatform is the Chrome for Testing platform name for a Go OS and arch.
func CfTPlatform(goos, goarch string) string {
	switch {
	case goos == "windows" && goarch == "386":
		return "win32"
	case goos == "windows":
		return "win64"
	case goos == "darwin" && goarch == "arm64":
		return "mac-arm64"
	case goos == "darwin":
		return "mac-x64"
	case goos == "linux":
		return "linux64"
	}
	return ""
}

// ParseCfTStable reads last-known-good-versions-with-downloads.json and
// returns the Stable chrome build for platform.
func ParseCfTStable(r io.Reader, platform string) (CfTBuild, error) {
	var doc struct {
		Channels map[string]struct {
			Version   string `json:"version"`
			Revision  string `json:"revision"`
			Downloads struct {
				Chrome []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chrome"`
			} `json:"downloads"`
		} `json:"channels"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return CfTBuild{}, fmt.Errorf("driver: read Chrome for Testing versions: %w", err)
	}
	st, ok := doc.Channels["Stable"]
	if !ok || st.Version == "" {
		return CfTBuild{}, fmt.Errorf("driver: Chrome for Testing versions have no Stable channel")
	}
	for _, d := range st.Downloads.Chrome {
		if d.Platform == platform {
			return CfTBuild{Version: st.Version, Revision: st.Revision, Platform: platform, URL: d.URL}, nil
		}
	}
	return CfTBuild{}, fmt.Errorf("driver: Chrome for Testing %s has no chrome download for %q", st.Version, platform)
}

// ParseCfTVersions reads last-known-good-versions-with-downloads.json and
// returns the chrome build of every channel (Stable, Beta, Dev, Canary) that
// has a download for platform. Stable comes first, then the others by name.
func ParseCfTVersions(r io.Reader, platform string) ([]CfTBuild, error) {
	var doc struct {
		Channels map[string]struct {
			Version   string `json:"version"`
			Revision  string `json:"revision"`
			Downloads struct {
				Chrome []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chrome"`
			} `json:"downloads"`
		} `json:"channels"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("driver: read Chrome for Testing versions: %w", err)
	}
	var names []string
	for n := range doc.Channels {
		names = append(names, n)
	}
	sort.Slice(names, func(a, b int) bool {
		if (names[a] == "Stable") != (names[b] == "Stable") {
			return names[a] == "Stable"
		}
		return names[a] < names[b]
	})
	var out []CfTBuild
	for _, n := range names {
		ch := doc.Channels[n]
		for _, d := range ch.Downloads.Chrome {
			if d.Platform == platform && ch.Version != "" {
				out = append(out, CfTBuild{Version: ch.Version, Revision: ch.Revision, Platform: platform, Channel: n, URL: d.URL})
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("driver: Chrome for Testing has no chrome download for %q", platform)
	}
	return out, nil
}

// FetchCfTVersions downloads the versions JSON from url and returns the build
// of every channel for platform. An empty url means CfTVersionsURL.
func FetchCfTVersions(ctx context.Context, client *http.Client, url, platform string) ([]CfTBuild, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if url == "" {
		url = CfTVersionsURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("driver: %s: %s", url, resp.Status)
	}
	return ParseCfTVersions(resp.Body, platform)
}

// FetchCfTStable downloads the versions JSON and returns the Stable build.
func FetchCfTStable(ctx context.Context, client *http.Client, platform string) (CfTBuild, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CfTVersionsURL, nil)
	if err != nil {
		return CfTBuild{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return CfTBuild{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CfTBuild{}, fmt.Errorf("driver: %s: %s", CfTVersionsURL, resp.Status)
	}
	return ParseCfTStable(resp.Body, platform)
}

// CfTInstall returns the Install for a build unzipped into dir (the folder
// that holds chrome-win64 or the platform's equivalent). CfT is a Chromium
// brand for policy purposes: it ignores Google's policy keys.
func CfTInstall(dir, platform string) Install {
	root := filepath.Join(dir, "chrome-"+platform)
	switch platform {
	case "win64", "win32":
		return Install{Path: filepath.Join(root, "chrome.exe"), Brand: BrandChromium}
	case "mac-arm64", "mac-x64":
		return Install{Path: filepath.Join(root, "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing"), Brand: BrandChromium}
	}
	return Install{Path: filepath.Join(root, "chrome"), Brand: BrandChromium}
}
