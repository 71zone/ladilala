package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Settings is the whole content of settings.json in the app data folder. It
// is not in SQLite on purpose: the app must read the data folder pointer and
// the update feed before the database opens, and a user can edit the file by
// hand when the UI does not start.
type Settings struct {
	Language       string `json:"language"` // vi | en
	Theme          string `json:"theme"`    // system | light | dark
	DefaultDelayMs int    `json:"defaultDelayMs"`
	Concurrency    int    `json:"concurrency"`
	ProxyTestURL   string `json:"proxyTestUrl"`
	LogLevel       string `json:"logLevel"` // error | warn | info | debug

	Update  UpdateSettings `json:"update"`
	Engines EngineSettings `json:"engines"`
}

// UpdateSettings belong to AppUpdateService.
type UpdateSettings struct {
	FeedURL        string `json:"feedUrl"` // empty means updates are not configured; the default is defaultFeedURL
	Channel        string `json:"channel"` // stable | beta | nightly
	AutoCheck      string `json:"autoCheck"`
	InstallOnQuit  bool   `json:"installOnQuit"`
	SkippedVersion string `json:"skippedVersion"`
	LastChecked    string `json:"lastChecked"` // RFC 3339 UTC, empty when never
}

// EngineSettings belong to EngineService.
type EngineSettings struct {
	DefaultEngine  string            `json:"defaultEngine"` // chrome | edge | cft
	Channel        string            `json:"channel"`       // Stable | Beta
	AutoCheck      string            `json:"autoCheck"`     // daily | weekly | off
	KeepOld        int               `json:"keepOld"`
	BandwidthLimit int64             `json:"bandwidthLimit"` // bytes per second, 0 = unlimited
	GroupPins      map[string]string `json:"groupPins"`
	ProfileEngines map[string]string `json:"profileEngines"`
	LastChecked    string            `json:"lastChecked"`
}

const (
	defaultProxyTestURL = "https://api.ipify.org"
	defaultFeedURL      = "https://ladilala.71.zone/manifest.json"
	settingsFile        = "settings.json"
)

func defaultSettings() Settings {
	return Settings{
		Language:       "vi",
		Theme:          "system",
		DefaultDelayMs: 0,
		Concurrency:    1,
		ProxyTestURL:   defaultProxyTestURL,
		LogLevel:       "info",
		Update:         UpdateSettings{FeedURL: defaultFeedURL, Channel: "stable", AutoCheck: "daily", InstallOnQuit: true},
		Engines: EngineSettings{
			DefaultEngine: "chrome", Channel: "Stable", AutoCheck: "daily", KeepOld: 1,
			GroupPins: map[string]string{}, ProfileEngines: map[string]string{},
		},
	}
}

// settingsStore reads and writes settings.json. All services share one.
type settingsStore struct {
	path string

	mu sync.Mutex
	s  Settings
}

// openSettings loads dir/settings.json. A missing file gives the defaults. A
// file that does not parse is an error: the app must not overwrite what the
// user may have edited by hand. Fields missing from the file keep defaults.
func openSettings(dir string) (*settingsStore, error) {
	st := &settingsStore{path: filepath.Join(dir, settingsFile), s: defaultSettings()}
	b, err := os.ReadFile(st.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return st, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(b, &st.s); err != nil {
		return nil, fmt.Errorf("read %s: %w", st.path, err)
	}
	st.s.normalize()
	return st, nil
}

// normalize replaces values that are out of range with the defaults, so a
// hand-edited file cannot put the app in a state the UI cannot show.
func (s *Settings) normalize() {
	d := defaultSettings()
	if s.Language != "vi" && s.Language != "en" {
		s.Language = d.Language
	}
	if !oneOf(s.Theme, "system", "light", "dark") {
		s.Theme = d.Theme
	}
	if s.Concurrency < 1 || s.Concurrency > maxConcurrency {
		s.Concurrency = d.Concurrency
	}
	if s.DefaultDelayMs < 0 || s.DefaultDelayMs > maxDelayMs {
		s.DefaultDelayMs = d.DefaultDelayMs
	}
	if s.ProxyTestURL == "" {
		s.ProxyTestURL = d.ProxyTestURL
	}
	if !oneOf(s.LogLevel, "error", "warn", "info", "debug") {
		s.LogLevel = d.LogLevel
	}
	if !oneOf(s.Update.Channel, "stable", "beta", "nightly") {
		s.Update.Channel = d.Update.Channel
	}
	if !oneOf(s.Update.AutoCheck, "daily", "weekly", "off") {
		s.Update.AutoCheck = d.Update.AutoCheck
	}
	if !oneOf(s.Engines.DefaultEngine, "chrome", "edge", "cft") {
		s.Engines.DefaultEngine = d.Engines.DefaultEngine
	}
	if !oneOf(s.Engines.Channel, "Stable", "Beta") {
		s.Engines.Channel = d.Engines.Channel
	}
	if !oneOf(s.Engines.AutoCheck, "daily", "weekly", "off") {
		s.Engines.AutoCheck = d.Engines.AutoCheck
	}
	if s.Engines.KeepOld < 0 || s.Engines.KeepOld > 3 {
		s.Engines.KeepOld = d.Engines.KeepOld
	}
	if s.Engines.BandwidthLimit < 0 {
		s.Engines.BandwidthLimit = 0
	}
	if s.Engines.GroupPins == nil {
		s.Engines.GroupPins = map[string]string{}
	}
	if s.Engines.ProfileEngines == nil {
		s.Engines.ProfileEngines = map[string]string{}
	}
}

const (
	maxConcurrency = 64
	maxDelayMs     = 10 * 60 * 1000
)

func oneOf(v string, list ...string) bool {
	for _, x := range list {
		if v == x {
			return true
		}
	}
	return false
}

// Get returns a deep copy.
func (st *settingsStore) Get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.clone()
}

func (s Settings) clone() Settings {
	c := s
	c.Engines.GroupPins = cloneMap(s.Engines.GroupPins)
	c.Engines.ProfileEngines = cloneMap(s.Engines.ProfileEngines)
	return c
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Update applies fn to a copy, normalizes and saves it. When fn returns an
// error or the save fails, the stored settings do not change.
func (st *settingsStore) Update(fn func(*Settings) error) (Settings, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	next := st.s.clone()
	if err := fn(&next); err != nil {
		return st.s.clone(), err
	}
	next.normalize()
	if err := st.write(next); err != nil {
		return st.s.clone(), err
	}
	st.s = next
	return st.s.clone(), nil
}

// write saves through a temporary file so a crash cannot leave half a file.
func (st *settingsStore) write(s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", st.path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, st.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
