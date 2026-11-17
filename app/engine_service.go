package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/engine"
	"github.com/8bu/ladilala/core/profile"
)

// Engine IDs. The three tiers of ADR 0003, 0004 and 0005:
//  1. chrome, edge: the vendor browser installed on this PC (stock),
//  2. cft: Chrome for Testing, and fpc: fingerprint-chromium (pinned, ten
//     Chromium versions old), both downloaded by Ladilala,
//  3. patched: an own patched Chromium. Not built, so never offered.
const (
	engineChrome  = "chrome"
	engineEdge    = "edge"
	engineCfT     = "cft"
	engineFPC     = "fpc"
	enginePatched = "patched"
)

const (
	engineMetaFile  = "ladilala-engine.json"
	engineStateFile = "state.json"
	engineSumsFile  = "checksums.json"
	defaultProbe    = 2 * time.Second
)

// EngineService is the Engines screen: which browsers exist, how to get
// Chrome for Testing, and whether this PC can drive them.
type EngineService struct {
	cfg      *settingsStore
	dataDir  string
	dir      string // <data>/engines/chrome-for-testing
	fpcDir   string // <data>/engines/fingerprint-chromium
	platform string
	launcher *engine.StockLauncher
	emit     func(name string, data any)

	// Seams. The defaults use the real machine; tests replace them.
	detect        func() ([]driver.Install, error)
	version       func(driver.Install) (string, error)
	probe         func(ctx context.Context, in driver.Install) (string, error)
	fetchVersions func(ctx context.Context) ([]driver.CfTBuild, error)
	fpcBuilds     []driver.FPCBuild // pinned fingerprint-chromium builds
	fpcOK         bool              // the pinned zip is a Windows build
	activeRuns    func() int
	client        *http.Client
	probeTimeout  time.Duration
	retryDelay    time.Duration

	mu      sync.Mutex // guards state.json, checksums.json and jobs
	jobs    map[string]*engineJob
	latest  []driver.CfTBuild
	sizes   map[string]int64
	major   int
	majorAt time.Time
	seq     int
	closed  bool
}

func newEngineService(b *Backend) *EngineService {
	s := &EngineService{
		cfg:          b.Settings,
		dataDir:      b.DataDir,
		dir:          filepath.Join(b.DataDir, "engines", "chrome-for-testing"),
		fpcDir:       filepath.Join(b.DataDir, "engines", "fingerprint-chromium"),
		fpcBuilds:    driver.FPCBuilds,
		fpcOK:        runtime.GOOS == "windows" && runtime.GOARCH == "amd64",
		platform:     driver.CfTPlatform(runtime.GOOS, runtime.GOARCH),
		emit:         b.Emit,
		detect:       driver.StockChrome{}.Detect,
		version:      driver.StockChrome{}.Version,
		client:       &http.Client{Timeout: 0},
		probeTimeout: defaultProbe,
		retryDelay:   time.Second,
		jobs:         map[string]*engineJob{},
		sizes:        map[string]int64{},
	}
	if b.Launcher != nil {
		s.launcher = b.Launcher
	}
	if b.Engine != nil {
		s.activeRuns = b.Engine.ActiveRuns
	}
	s.probe = s.probeStock
	s.fetchVersions = func(ctx context.Context) ([]driver.CfTBuild, error) {
		return driver.FetchCfTVersions(ctx, &http.Client{Timeout: 30 * time.Second}, "", s.platform)
	}
	s.applyDefault()
	return s
}

// ServiceStartup checks for engine updates in the background when the user
// asked for it and the last check is old.
func (s *EngineService) ServiceStartup(context.Context, application.ServiceOptions) error {
	st := s.cfg.Get().Engines
	if every := autoCheckEvery(st.AutoCheck); every > 0 {
		last, _ := time.Parse(time.RFC3339, st.LastChecked)
		if time.Since(last) > every {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				_, _ = s.CheckEngineUpdates(ctx)
			}()
		}
	}
	return nil
}

// ServiceShutdown stops the running jobs. Partial downloads stay on disk.
func (s *EngineService) ServiceShutdown() error {
	s.mu.Lock()
	s.closed = true
	for _, j := range s.jobs {
		j.cancel()
	}
	s.mu.Unlock()
	return nil
}

func autoCheckEvery(mode string) time.Duration {
	switch mode {
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	}
	return 0
}

// ---- DTOs -------------------------------------------------------------------------

// EngineVersion is one installed version of an engine.
type EngineVersion struct {
	Version   string `json:"version"`
	SizeBytes int64  `json:"sizeBytes"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256,omitempty"`
	Active    bool   `json:"active"`
}

// EngineDTO is one row of the Engines screen.
type EngineDTO struct {
	ID   string `json:"id"`
	Tier int    `json:"tier"` // 1 stock, 2 Chrome for Testing, 3 own patched Chromium
	Name string `json:"name"`
	// Status is ready, missing, blocked (a policy forbids remote debugging)
	// or unavailable (not built).
	Status        string          `json:"status"`
	System        bool            `json:"system"`
	Path          string          `json:"path"`
	Installed     []EngineVersion `json:"installed"`
	Active        string          `json:"active"`
	Previous      string          `json:"previous"`
	Latest        string          `json:"latest"`        // newest version of the chosen channel, from the last check
	DownloadBytes int64           `json:"downloadBytes"` // zip size when known, else 0
	Default       bool            `json:"default"`
	PolicyBlocked bool            `json:"policyBlocked"`
	NotesURL      string          `json:"notesUrl"`
	JobID         string          `json:"jobId"` // running job of this engine, or empty
	// DiskBytes is the size on disk after the unpack, when known (fpc).
	DiskBytes int64 `json:"diskBytes,omitempty"`
	// ChromiumMajor is the Chromium version the engine is built on (fpc).
	ChromiumMajor int `json:"chromiumMajor,omitempty"`
	// Notes are codes the UI writes in the user's language: old (the build is
	// many Chromium versions old and misses security fixes), unsupported
	// (fields the engine cannot set stay real).
	Notes []string `json:"notes,omitempty"`
}

// EngineSummary is what the sidebar badge needs.
type EngineSummary struct {
	Ready   int    `json:"ready"`
	Default string `json:"default"`
	Updates int    `json:"updates"` // engines with a newer version available
}

// ---- state on disk ----------------------------------------------------------------

type cftState struct {
	Active   string `json:"active"`
	Previous string `json:"previous"`
}

type cftMeta struct {
	Version     string `json:"version"`
	Platform    string `json:"platform"`
	Channel     string `json:"channel,omitempty"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"sizeBytes"`
	InstalledAt string `json:"installedAt"`
}

func (s *EngineService) versionDir(v string) string { return filepath.Join(s.dir, v) }

func (s *EngineService) readState() cftState {
	var st cftState
	if b, err := os.ReadFile(filepath.Join(s.dir, engineStateFile)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func (s *EngineService) writeState(st cftState) error {
	return writeJSON(filepath.Join(s.dir, engineStateFile), st)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *EngineService) readSums() map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(s.dir, engineSumsFile)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func sumKey(platform, version string) string { return platform + "/" + version }

// installedVersions lists the unpacked Chrome for Testing versions, newest
// first. A folder without a metadata file is a half-done unpack and is ignored.
func (s *EngineService) installedVersions() []cftMeta {
	ents, _ := os.ReadDir(s.dir)
	var out []cftMeta
	for _, e := range ents {
		if !e.IsDir() || strings.HasSuffix(e.Name(), ".staging") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, e.Name(), engineMetaFile))
		if err != nil {
			continue
		}
		var m cftMeta
		if json.Unmarshal(b, &m) == nil && m.Version == e.Name() {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].InstalledAt != out[b].InstalledAt {
			return out[a].InstalledAt > out[b].InstalledAt
		}
		return out[a].Version > out[b].Version
	})
	return out
}

// ---- detection --------------------------------------------------------------------

type stockFound struct {
	in      driver.Install
	version string
}

// detectStock returns the first Chrome and the first Edge on the machine.
func (s *EngineService) detectStock() (chrome, edge *stockFound) {
	installs, _ := s.detect()
	for _, in := range installs {
		f := &stockFound{in: in}
		f.version, _ = s.version(in)
		switch {
		case in.Brand == driver.BrandChrome && chrome == nil:
			chrome = f
		case in.Brand == driver.BrandEdge && edge == nil:
			edge = f
		}
	}
	return chrome, edge
}

// installFor returns the browser binary of an engine, or nil when it has none.
func (s *EngineService) installFor(id string) *driver.Install {
	switch id {
	case engineChrome, engineEdge:
		c, e := s.detectStock()
		f := c
		if id == engineEdge {
			f = e
		}
		if f == nil {
			return nil
		}
		return &f.in
	case engineFPC:
		if !s.fpcOK {
			return nil
		}
		if _, in, ok := s.fpcInstalled(); ok {
			return &in
		}
		return nil
	case engineCfT:
		s.mu.Lock()
		act := s.readState().Active
		s.mu.Unlock()
		if act == "" {
			return nil
		}
		in := driver.CfTInstall(s.versionDir(act), s.platform)
		if st, err := os.Stat(in.Path); err != nil || !st.Mode().IsRegular() {
			return nil
		}
		return &in
	}
	return nil
}

// defaultMajor is the Chrome major version of the default engine, or 0. It
// caches the answer for a minute because it runs the browser with --version.
func (s *EngineService) defaultMajor() int {
	s.mu.Lock()
	if time.Since(s.majorAt) < time.Minute {
		m := s.major
		s.mu.Unlock()
		return m
	}
	s.mu.Unlock()
	major := 0
	if in := s.installFor(s.effectiveDefault()); in != nil {
		if v, err := s.version(*in); err == nil {
			major, _ = strconv.Atoi(strings.SplitN(v, ".", 2)[0])
		}
	}
	s.mu.Lock()
	s.major, s.majorAt = major, time.Now()
	s.mu.Unlock()
	return major
}

// effectiveDefault is the saved default when that engine is usable, else the
// first usable engine: Chrome, Edge, then Chrome for Testing.
func (s *EngineService) effectiveDefault() string {
	want := s.cfg.Get().Engines.DefaultEngine
	if s.installFor(want) != nil {
		return want
	}
	for _, id := range []string{engineChrome, engineEdge, engineCfT} {
		if s.installFor(id) != nil {
			return id
		}
	}
	return want
}

// applyDefault tells the run engine which browser to launch: the default
// engine, or the pin of a profile or its group.
func (s *EngineService) applyDefault() {
	if s.launcher == nil {
		return
	}
	id := s.effectiveDefault()
	switch id {
	case engineEdge:
		s.launcher.UseBrand(driver.BrandEdge)
		s.launcher.UseInstall(nil)
	case engineCfT, engineFPC:
		s.launcher.UseInstall(s.installFor(id))
	default:
		s.launcher.UseBrand(driver.BrandChrome)
		s.launcher.UseInstall(nil)
	}
	s.launcher.SetPicker(func(p profile.Profile) *driver.Install {
		e := s.cfg.Get().Engines
		pin := e.ProfileEngines[p.ID]
		if pin == "" && p.Group != "" {
			pin = e.GroupPins[p.Group]
		}
		if pin == "" {
			return nil
		}
		return s.installFor(pin)
	})
}

// ---- ListEngines ------------------------------------------------------------------

// ListEngines returns the three tiers: stock Chrome and Edge, Chrome for
// Testing and the own patched Chromium (shown as not available).
func (s *EngineService) ListEngines(context.Context) ([]EngineDTO, error) {
	chrome, edge := s.detectStock()
	def := s.effectiveDefault()
	cfg := s.cfg.Get().Engines
	out := []EngineDTO{
		s.stockDTO(engineChrome, "Google Chrome", chrome, def, "https://chromereleases.googleblog.com/"),
		s.stockDTO(engineEdge, "Microsoft Edge", edge, def, "https://learn.microsoft.com/deployedge/microsoft-edge-relnote-stable-channel"),
	}

	s.mu.Lock()
	st := s.readState()
	vers := s.installedVersions()
	latest := s.latestFor(cfg.Channel)
	jobID := s.runningJobLocked(engineCfT)
	var size int64
	if latest != nil {
		size = s.sizes[latest.URL]
	}
	s.mu.Unlock()
	cft := EngineDTO{
		ID: engineCfT, Tier: 2, Name: "Chrome for Testing", Status: "missing", Path: s.dir,
		Installed: []EngineVersion{}, Active: st.Active, Previous: st.Previous, Default: def == engineCfT,
		NotesURL: "https://googlechromelabs.github.io/chrome-for-testing/", JobID: jobID,
	}
	for _, m := range vers {
		cft.Installed = append(cft.Installed, EngineVersion{
			Version: m.Version, SizeBytes: dirSize(s.versionDir(m.Version)), Path: s.versionDir(m.Version),
			SHA256: m.SHA256, Active: m.Version == st.Active,
		})
	}
	if s.installFor(engineCfT) != nil {
		cft.Status = "ready"
		cft.Path = s.installFor(engineCfT).Path
	}
	if latest != nil {
		cft.Latest = latest.Version
		cft.DownloadBytes = size
	}
	out = append(out, cft, s.fpcDTO(def), EngineDTO{
		ID: enginePatched, Tier: 3, Name: "Ladilala Chromium", Status: "unavailable", Installed: []EngineVersion{},
	})
	return out, nil
}

func (s *EngineService) stockDTO(id, name string, f *stockFound, def, notes string) EngineDTO {
	d := EngineDTO{ID: id, Tier: 1, Name: name, System: true, Status: "missing", Installed: []EngineVersion{}, NotesURL: notes}
	if f == nil {
		return d
	}
	d.Status, d.Path, d.Active, d.Default = "ready", f.in.Path, f.version, def == id
	d.Installed = []EngineVersion{{Version: f.version, Path: f.in.Path, Active: true}}
	if driver.DebuggingBlockedByPolicy(f.in.Brand) {
		d.Status, d.PolicyBlocked = "blocked", true
	}
	return d
}

func (s *EngineService) runningJobLocked(engineID string) string {
	for id, j := range s.jobs {
		if j.snapshot().EngineID == engineID && j.running() {
			return id
		}
	}
	return ""
}

// latestFor returns the cached build of channel ("Stable" or "Beta").
func (s *EngineService) latestFor(channel string) *driver.CfTBuild {
	for i := range s.latest {
		if s.latest[i].Channel == channel {
			return &s.latest[i]
		}
	}
	return nil
}

// headSize asks for the zip size. The versions JSON carries none. A failure
// leaves the size unknown (0); it never fails the update check.
func (s *EngineService) headSize(ctx context.Context, url string) int64 {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength < 0 {
		return 0
	}
	return resp.ContentLength
}

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// ---- updates ----------------------------------------------------------------------

// CfTBuildDTO is one downloadable Chrome for Testing version.
type CfTBuildDTO struct {
	Version   string `json:"version"`
	Channel   string `json:"channel"`
	Revision  string `json:"revision"`
	URL       string `json:"url"`
	Installed bool   `json:"installed"`
}

// CheckEngineUpdates fetches the official Chrome for Testing list and returns
// the build of every channel for this platform.
func (s *EngineService) CheckEngineUpdates(ctx context.Context) ([]CfTBuildDTO, error) {
	builds, err := s.fetchVersions(ctx)
	if err != nil {
		return nil, apiErr("engine.network", err.Error(), "fixId", "retry")
	}
	want := s.cfg.Get().Engines.Channel
	size := int64(0)
	for _, b := range builds {
		if b.Channel == want {
			size = s.headSize(ctx, b.URL)
			s.mu.Lock()
			s.sizes[b.URL] = size
			s.mu.Unlock()
		}
	}
	s.mu.Lock()
	s.latest = builds
	have := map[string]bool{}
	for _, m := range s.installedVersions() {
		have[m.Version] = true
	}
	s.mu.Unlock()
	_, _ = s.cfg.Update(func(c *Settings) error { c.Engines.LastChecked = time.Now().UTC().Format(time.RFC3339); return nil })
	out := make([]CfTBuildDTO, 0, len(builds))
	for _, b := range builds {
		out = append(out, CfTBuildDTO{Version: b.Version, Channel: b.Channel, Revision: b.Revision, URL: b.URL, Installed: have[b.Version]})
	}
	return out, nil
}

// EngineUpdateSettings is the update group on the Engines screen.
type EngineUpdateSettings struct {
	Channel        string `json:"channel"`   // Stable | Beta
	AutoCheck      string `json:"autoCheck"` // daily | weekly | off
	KeepOld        int    `json:"keepOld"`   // 0 to 3
	BandwidthLimit int64  `json:"bandwidthLimit"`
	LastChecked    string `json:"lastChecked"`
}

func (s *EngineService) GetUpdateSettings(context.Context) (EngineUpdateSettings, error) {
	e := s.cfg.Get().Engines
	return EngineUpdateSettings{Channel: e.Channel, AutoCheck: e.AutoCheck, KeepOld: e.KeepOld, BandwidthLimit: e.BandwidthLimit, LastChecked: e.LastChecked}, nil
}

func (s *EngineService) SetUpdateSettings(_ context.Context, in EngineUpdateSettings) (EngineUpdateSettings, error) {
	var problems []map[string]string
	if !oneOf(in.Channel, "Stable", "Beta") {
		problems = append(problems, map[string]string{"path": "channel", "msg": "must be Stable or Beta"})
	}
	if !oneOf(in.AutoCheck, "daily", "weekly", "off") {
		problems = append(problems, map[string]string{"path": "autoCheck", "msg": "must be daily, weekly or off"})
	}
	if in.KeepOld < 0 || in.KeepOld > 3 {
		problems = append(problems, map[string]string{"path": "keepOld", "msg": "must be from 0 to 3"})
	}
	if in.BandwidthLimit < 0 {
		problems = append(problems, map[string]string{"path": "bandwidthLimit", "msg": "must not be negative"})
	}
	if len(problems) > 0 {
		return EngineUpdateSettings{}, apiErr("invalid", "update settings are not valid", "problems", problems)
	}
	_, err := s.cfg.Update(func(c *Settings) error {
		c.Engines.Channel, c.Engines.AutoCheck, c.Engines.KeepOld, c.Engines.BandwidthLimit = in.Channel, in.AutoCheck, in.KeepOld, in.BandwidthLimit
		return nil
	})
	if err != nil {
		return EngineUpdateSettings{}, asAPIError(err)
	}
	return s.GetUpdateSettings(context.Background())
}

// ---- default, pins, rollback, remove ----------------------------------------------

// SetDefaultEngine makes id the engine for every profile without a pin. The
// engine must be installed and not blocked.
func (s *EngineService) SetDefaultEngine(_ context.Context, id string) error {
	if id == enginePatched {
		return apiErr("engine.unavailable", "the own Chromium is not available")
	}
	if !oneOf(id, engineChrome, engineEdge, engineCfT, engineFPC) {
		return apiErr("notFound", "unknown engine", "id", id)
	}
	in := s.installFor(id)
	if in == nil {
		return apiErr("engine.missing", "engine is not installed", "id", id)
	}
	if driver.DebuggingBlockedByPolicy(in.Brand) {
		return apiErr("engine.policy", "a policy blocks remote debugging for this browser", "id", id)
	}
	if _, err := s.cfg.Update(func(c *Settings) error { c.Engines.DefaultEngine = id; return nil }); err != nil {
		return asAPIError(err)
	}
	s.applyDefault()
	return nil
}

// EnginePins are the engines pinned to profile groups and to single profiles.
type EnginePins struct {
	Groups   map[string]string `json:"groups"`
	Profiles map[string]string `json:"profiles"`
}

func (s *EngineService) GetPins(context.Context) (EnginePins, error) {
	e := s.cfg.Get().Engines
	return EnginePins{Groups: e.GroupPins, Profiles: e.ProfileEngines}, nil
}

// SetGroupPin pins a profile group to an engine. An empty engine id removes the pin.
func (s *EngineService) SetGroupPin(_ context.Context, group, engineID string) error {
	return s.setPin(group, engineID, func(c *Settings) map[string]string { return c.Engines.GroupPins })
}

// SetProfileEngine pins one profile to an engine. An empty engine id removes
// the pin, and the profile follows its group pin or the default.
func (s *EngineService) SetProfileEngine(_ context.Context, profileID, engineID string) error {
	return s.setPin(profileID, engineID, func(c *Settings) map[string]string { return c.Engines.ProfileEngines })
}

func (s *EngineService) setPin(key, engineID string, m func(*Settings) map[string]string) error {
	if key == "" {
		return apiErr("invalid", "pin key is empty", "field", "key")
	}
	if engineID != "" && s.installFor(engineID) == nil {
		return apiErr("engine.missing", "engine is not installed", "id", engineID)
	}
	_, err := s.cfg.Update(func(c *Settings) error {
		if engineID == "" {
			delete(m(c), key)
		} else {
			m(c)[key] = engineID
		}
		return nil
	})
	return asAPIError(err)
}

// RollbackEngine makes the previous Chrome for Testing version the active one.
func (s *EngineService) RollbackEngine(_ context.Context, id string) error {
	if id != engineCfT {
		return apiErr("engine.notRollback", "only Chrome for Testing keeps old versions", "id", id)
	}
	if s.runsActive() {
		return apiErr("conflict", "a run is using the browser", "reason", "runActive")
	}
	s.mu.Lock()
	st := s.readState()
	if st.Previous == "" || s.metaOf(st.Previous) == nil {
		s.mu.Unlock()
		return apiErr("engine.noPrevious", "no previous version is kept")
	}
	st.Active, st.Previous = st.Previous, st.Active
	err := s.writeState(st)
	s.mu.Unlock()
	if err != nil {
		return asAPIError(err)
	}
	s.applyDefault()
	return nil
}

func (s *EngineService) metaOf(v string) *cftMeta {
	for _, m := range s.installedVersions() {
		if m.Version == v {
			return &m
		}
	}
	return nil
}

func (s *EngineService) runsActive() bool { return s.activeRuns != nil && s.activeRuns() > 0 }

// RemoveEngine deletes one installed version of Chrome for Testing, or every
// version when version is empty. Stock browsers belong to the OS and cannot be
// removed here.
func (s *EngineService) RemoveEngine(_ context.Context, id, version string) error {
	if id == engineFPC {
		if err := s.removeFPC(); err != nil {
			return err
		}
		s.forgetEngine(engineFPC)
		return nil
	}
	if id != engineCfT {
		return apiErr("engine.system", "this engine is managed by Windows", "id", id)
	}
	s.mu.Lock()
	if s.runningJobLocked(engineCfT) != "" {
		s.mu.Unlock()
		return apiErr("conflict", "an install is running", "reason", "jobRunning")
	}
	st := s.readState()
	if s.runsActive() && (version == "" || version == st.Active) {
		s.mu.Unlock()
		return apiErr("conflict", "a run is using the browser", "reason", "runActive")
	}
	var err error
	if version == "" {
		for _, m := range s.installedVersions() {
			err = errors.Join(err, os.RemoveAll(s.versionDir(m.Version)))
		}
		st = cftState{}
	} else {
		if !validVersion(version) || s.metaOf(version) == nil {
			s.mu.Unlock()
			return apiErr("notFound", "version is not installed", "version", version)
		}
		err = os.RemoveAll(s.versionDir(version))
		if st.Previous == version {
			st.Previous = ""
		}
		if st.Active == version {
			st.Active, st.Previous = st.Previous, ""
		}
	}
	if werr := s.writeState(st); werr != nil {
		err = errors.Join(err, werr)
	}
	s.mu.Unlock()
	if err != nil {
		return apiErr("engine.inUse", err.Error())
	}
	// A pin or default that pointed at the removed engine falls back.
	if st.Active == "" {
		s.forgetEngine(engineCfT)
	}
	return nil
}

// forgetEngine moves a default or a pin that pointed at a removed engine back
// to the default, then tells the launcher.
func (s *EngineService) forgetEngine(id string) {
	_, _ = s.cfg.Update(func(c *Settings) error {
		if c.Engines.DefaultEngine == id {
			c.Engines.DefaultEngine = engineChrome
		}
		for _, m := range []map[string]string{c.Engines.GroupPins, c.Engines.ProfileEngines} {
			for k, v := range m {
				if v == id {
					delete(m, k)
				}
			}
		}
		return nil
	})
	s.applyDefault()
}

// validVersion accepts only dotted digits, so a version can never be a path.
func validVersion(v string) bool {
	if v == "" || len(v) > 32 {
		return false
	}
	for _, r := range v {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return !strings.HasPrefix(v, ".") && !strings.Contains(v, "..")
}

// OpenEngineFolder shows the folder of a downloaded engine (cft or fpc) in
// the file manager.
func (s *EngineService) OpenEngineFolder(_ context.Context, id string) error {
	dir := s.dir
	switch id {
	case engineCfT:
	case engineFPC:
		dir = s.fpcDir
	default:
		return apiErr("engine.system", "this engine is managed by Windows", "id", id)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return asAPIError(err)
	}
	return asAPIError(openInFileManager(dir))
}

// EngineSummary counts what the sidebar badge shows.
func (s *EngineService) Summary(ctx context.Context) (EngineSummary, error) {
	list, _ := s.ListEngines(ctx)
	var sum EngineSummary
	for _, e := range list {
		if e.Status == "ready" {
			sum.Ready++
		}
		if e.Default {
			sum.Default = e.ID
		}
		if e.ID == engineCfT && e.Latest != "" && e.Active != "" && e.Latest != e.Active {
			sum.Updates++
		}
	}
	return sum, nil
}
