package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/8bu/ladilala/core/driver"
	"github.com/8bu/ladilala/core/engine"
	"github.com/8bu/ladilala/core/profile"
	"github.com/8bu/ladilala/core/proxy"
	"github.com/8bu/ladilala/core/store"
)

// maxSeed keeps seeds inside the range a JavaScript number holds exactly.
const maxSeed = 1<<53 - 1

// profPreset is one OS choice of the editor.
type profPreset struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Platform        string `json:"platform"`
	PlatformVersion string `json:"platformVersion"`
}

var profPresets = []profPreset{
	{"windows-11", "Windows 11", profile.PlatformWindows, "15.0.0"},
	{"windows-10", "Windows 10", profile.PlatformWindows, "10.0.0"},
	{"macos-14", "macOS 14", profile.PlatformMacOS, "14.5.0"},
}

func profPresetByID(id string) (profPreset, bool) {
	for _, p := range profPresets {
		if p.ID == id {
			return p, true
		}
	}
	return profPreset{}, false
}

// profPresetOf names the preset a stored fingerprint belongs to.
func profPresetOf(fp profile.Fingerprint) string {
	switch fp.Platform {
	case profile.PlatformMacOS:
		return "macos-14"
	case profile.PlatformWindows:
		if fp.PlatformVersion == "10.0.0" {
			return "windows-10"
		}
		return "windows-11"
	}
	return fp.Platform
}

// FingerprintDTO is profile.Fingerprint plus the name of its OS preset.
type FingerprintDTO struct {
	profile.Fingerprint
	Preset string `json:"preset"`
}

// ProfileSession is one past browser session. Nothing records sessions yet,
// so ProfileDTO.Sessions is always empty.
type ProfileSession struct {
	Start string `json:"start"`
	Mins  int    `json:"minutes"`
	Pages int    `json:"pages"`
}

// ProfileDTO is the profile the UI shows. It never holds the proxy password.
type ProfileDTO struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Group       string           `json:"group"`
	Tags        []string         `json:"tags"`
	Notes       string           `json:"notes"`
	ProxyID     string           `json:"proxyId,omitempty"`
	Fingerprint FingerprintDTO   `json:"fingerprint"`
	State       string           `json:"state"` // running | stopped | error
	Err         string           `json:"err,omitempty"`
	ErrCode     string           `json:"errCode,omitempty"`
	CookieCount int              `json:"cookieCount"`
	Sessions    []ProfileSession `json:"sessions"`
	DataDir     string           `json:"dataDir"`
	CreatedAt   string           `json:"createdAt"`
	UpdatedAt   string           `json:"updatedAt"`
}

// ProfileInput is what the editor sends. Preset, Seed, Language, Timezone and
// the six modes build the fingerprint. Empty fields keep the stored value on
// update and take the default on create. Seed 0 means a new random seed on
// create and "keep" on update.
type ProfileInput struct {
	Name        string       `json:"name"`
	Group       string       `json:"group"`
	Tags        []string     `json:"tags"`
	Notes       string       `json:"notes"`
	ProxyID     string       `json:"proxyId"`
	Preset      string       `json:"preset"`
	Seed        uint64       `json:"seed"`
	Language    string       `json:"language"`
	Timezone    string       `json:"timezone"`
	Canvas      profile.Mode `json:"canvas"`
	Audio       profile.Mode `json:"audio"`
	WebGL       profile.Mode `json:"webgl"`
	Fonts       profile.Mode `json:"fonts"`
	ClientRects profile.Mode `json:"clientRects"`
	WebRTC      profile.Mode `json:"webrtc"`
}

// ProfileFilter selects profiles for ListProfiles. Empty fields match all.
type ProfileFilter struct {
	Text  string `json:"text"`
	Group string `json:"group"`
	Tag   string `json:"tag"`
	State string `json:"state"`
}

// PreviewInput asks for the device a preset and seed give.
type PreviewInput struct {
	Preset   string `json:"preset"`
	Seed     uint64 `json:"seed"`
	Language string `json:"language"`
	Timezone string `json:"timezone"`
}

// StartResult is the outcome of starting one profile.
type StartResult struct {
	ProfileID string    `json:"profileId"`
	OK        bool      `json:"ok"`
	Error     *APIError `json:"error,omitempty"`
}

// ProfileStateEvent is the payload of the profile.state event.
type ProfileStateEvent struct {
	ProfileID string `json:"profileId"`
	State     string `json:"state"`
	Err       string `json:"err,omitempty"`  // proxyDown | engineExit
	Code      string `json:"code,omitempty"` // API error code
}

// ProfilePresets is the answer of Presets.
type ProfilePresets struct {
	Presets []profPreset `json:"presets"`
	// EngineMajor is the Chrome major version new fingerprints use.
	EngineMajor int `json:"engineMajor"`
}

type runningProfile struct {
	browser engine.Browser
	since   time.Time
}

type profileFault struct{ err, code string }

// ProfileService is the Go side of the Profiles screen.
type ProfileService struct {
	st       *store.Store
	dataDir  string
	launcher engine.Launcher
	emit     func(string, any)
	prober   *proxyProber
	version  func() string

	opMu sync.Mutex // serialises writes (name checks need it)

	mu       sync.Mutex
	running  map[string]*runningProfile
	starting map[string]bool
	faults   map[string]profileFault
}

func newProfileService(b *Backend) *ProfileService {
	return newProfileServiceWith(b.Store, b.DataDir, b.Launcher, b.Emit, newProxyProber(b.DataDir))
}

func newProfileServiceWith(st *store.Store, dataDir string, l engine.Launcher, emit func(string, any), pr *proxyProber) *ProfileService {
	return &ProfileService{
		st: st, dataDir: dataDir, launcher: l, emit: emit, prober: pr,
		version:  detectedBrowserVersion,
		running:  map[string]*runningProfile{},
		starting: map[string]bool{},
		faults:   map[string]profileFault{},
	}
}

var (
	versionMu    sync.Mutex
	versionCache string
)

// detectedBrowserVersion is the version of the installed stock Chrome, so the
// spoofed user agent agrees with the real engine. It falls back to the
// default version of core/profile when no browser is found.
func detectedBrowserVersion() string {
	versionMu.Lock()
	defer versionMu.Unlock()
	if versionCache != "" {
		return versionCache
	}
	installs, err := driver.StockChrome{}.Detect()
	if err == nil {
		for _, in := range installs {
			if v, err := (driver.StockChrome{}).Version(in); err == nil && v != "" {
				versionCache = v
				return v
			}
		}
	}
	return profile.DefaultBrowserVersion
}

func majorOfVersion(v string) int {
	n, _ := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	return n
}

// ---- errors ------------------------------------------------------------------

// profileToAPIError maps core errors to the codes of docs/api-contract.md.
// Every ProfileService method returns its errors through it.
func profileToAPIError(err error) error {
	if err == nil {
		return nil
	}
	var a *APIError
	switch {
	case errors.As(err, &a):
		return err
	case errors.Is(err, store.ErrNotFound):
		return apiErr("notFound", err.Error())
	case errors.Is(err, driver.ErrProfileLocked):
		return apiErr("profile.locked", err.Error())
	case errors.Is(err, driver.ErrInvalidDataDir):
		return apiErr("profile.dataDir", err.Error())
	case errors.Is(err, driver.ErrBinaryNotFound):
		return apiErr("engine.notFound", err.Error())
	case errors.Is(err, driver.ErrDebuggingBlocked):
		return apiErr("engine.policyBlocked", err.Error())
	case errors.Is(err, driver.ErrCanvasHang), errors.Is(err, driver.ErrCanvasLeak):
		return apiErr("engine.hang", err.Error())
	case errors.Is(err, proxy.ErrUpstreamAuth):
		return apiErr("proxy.auth", err.Error())
	}
	return asAPIError(err)
}

func profInvalid(field, msg string) error {
	return &APIError{Code: "invalid", Message: msg, Details: map[string]any{
		"problems": []map[string]string{{"path": field, "msg": msg}},
	}}
}

func profConflict(reason string, ids ...string) error {
	return &APIError{Code: "conflict", Message: "profile conflict: " + reason, Details: map[string]any{"reason": reason, "ids": ids}}
}

// ---- helpers -----------------------------------------------------------------

func (s *ProfileService) engineVersion() string {
	if s.version != nil {
		return s.version()
	}
	return profile.DefaultBrowserVersion
}

func newSeed() uint64 { return rand.Uint64N(maxSeed-1) + 1 }

func (s *ProfileService) stateOf(id string) (state, errKind, errCode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.running[id]; ok {
		return "running", "", ""
	}
	if f, ok := s.faults[id]; ok {
		return "error", f.err, f.code
	}
	return "stopped", "", ""
}

func (s *ProfileService) toDTO(r store.ProfileRecord) ProfileDTO {
	st, ek, ec := s.stateOf(r.ID)
	tags := r.Tags
	if tags == nil {
		tags = []string{}
	}
	return ProfileDTO{
		ID: r.ID, Name: r.Name, Group: r.Group, Tags: tags, Notes: r.Notes, ProxyID: r.ProxyID,
		Fingerprint: FingerprintDTO{Fingerprint: r.Fingerprint, Preset: profPresetOf(r.Fingerprint)},
		State:       st, Err: ek, ErrCode: ec, Sessions: []ProfileSession{}, DataDir: r.DataDir,
		CreatedAt: r.CreatedAt.Format(time.RFC3339), UpdatedAt: r.UpdatedAt.Format(time.RFC3339),
	}
}

func (s *ProfileService) isRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.running[id]
	return ok
}

func (s *ProfileService) runningAmong(ids []string) []string {
	var out []string
	for _, id := range ids {
		if s.isRunning(id) {
			out = append(out, id)
		}
	}
	return out
}

func (s *ProfileService) setState(id, state, errKind, code string) {
	if s.emit != nil {
		s.emit("profile.state", ProfileStateEvent{ProfileID: id, State: state, Err: errKind, Code: code})
	}
}

func (s *ProfileService) dataDirOf(id string) string { return filepath.Join(s.dataDir, "profiles", id) }

func cleanText(v string, max int) string {
	v = strings.TrimSpace(v)
	if r := []rune(v); len(r) > max {
		v = string(r[:max])
	}
	return v
}

func cleanTags(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range in {
		t = cleanText(t, 40)
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return out
}

// checkFields validates the plain text fields of an input.
func profCheckFields(in *ProfileInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return profInvalid("name", "name is empty")
	}
	if len([]rune(in.Name)) > 100 {
		return profInvalid("name", "name is longer than 100 characters")
	}
	in.Group = cleanText(in.Group, 60)
	in.Tags = cleanTags(in.Tags)
	if len([]rune(in.Notes)) > 5000 {
		return profInvalid("notes", "notes are longer than 5000 characters")
	}
	if in.Seed > maxSeed {
		return profInvalid("seed", "seed is larger than 2^53-1")
	}
	return nil
}

func (s *ProfileService) nameTaken(ctx context.Context, name, exceptID string) (bool, error) {
	all, err := s.st.Profiles.List(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range all {
		if p.ID != exceptID && strings.EqualFold(p.Name, name) {
			return true, nil
		}
	}
	return false, nil
}

func (s *ProfileService) checkProxy(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	if _, err := s.st.Proxies.Get(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return &APIError{Code: "notFound", Message: "proxy not found", Details: map[string]any{"proxyId": id}}
		}
		return err
	}
	return nil
}

func applyModes(fp *profile.Fingerprint, in ProfileInput) {
	set := func(dst *profile.Mode, v profile.Mode) {
		if v != "" {
			*dst = v
		}
	}
	set(&fp.Canvas, in.Canvas)
	set(&fp.Audio, in.Audio)
	set(&fp.WebGL, in.WebGL)
	set(&fp.Fonts, in.Fonts)
	set(&fp.ClientRects, in.ClientRects)
	set(&fp.WebRTC, in.WebRTC)
}

// buildFingerprint makes the device of a preset and a seed.
func (s *ProfileService) buildFingerprint(presetID string, seed uint64, lang, tz string) (profile.Fingerprint, error) {
	p, ok := profPresetByID(presetID)
	if !ok {
		return profile.Fingerprint{}, profInvalid("preset", "unknown OS preset "+presetID)
	}
	fp := profile.NewFingerprint(seed, profile.Options{Platform: p.Platform, Language: lang, Timezone: tz, BrowserVersion: s.engineVersion()})
	fp.PlatformVersion = p.PlatformVersion
	return fp, nil
}

func profValidate(fp profile.Fingerprint) error {
	problems := profile.Validate(fp)
	if len(problems) == 0 {
		return nil
	}
	list := make([]map[string]string, 0, len(problems))
	for _, p := range problems {
		list = append(list, map[string]string{"path": "fingerprint", "msg": p})
	}
	return &APIError{Code: "invalid", Message: "fingerprint is not coherent", Details: map[string]any{"problems": list}}
}

// ---- reads -------------------------------------------------------------------

// Presets lists the OS presets and the engine major version.
func (s *ProfileService) Presets(ctx context.Context) (ProfilePresets, error) {
	return ProfilePresets{Presets: profPresets, EngineMajor: majorOfVersion(s.engineVersion())}, nil
}

// PreviewFingerprint returns the device a preset and seed give, so the editor
// can show it before the user saves.
func (s *ProfileService) PreviewFingerprint(ctx context.Context, in PreviewInput) (FingerprintDTO, error) {
	if in.Seed == 0 {
		in.Seed = newSeed()
	}
	if in.Seed > maxSeed {
		return FingerprintDTO{}, profInvalid("seed", "seed is larger than 2^53-1")
	}
	fp, err := s.buildFingerprint(in.Preset, in.Seed, in.Language, in.Timezone)
	if err != nil {
		return FingerprintDTO{}, profileToAPIError(err)
	}
	return FingerprintDTO{Fingerprint: fp, Preset: in.Preset}, nil
}

// ListProfiles returns the profiles that match the filter, by name.
func (s *ProfileService) ListProfiles(ctx context.Context, f ProfileFilter) ([]ProfileDTO, error) {
	all, err := s.st.Profiles.List(ctx)
	if err != nil {
		return nil, profileToAPIError(err)
	}
	q := strings.ToLower(strings.TrimSpace(f.Text))
	out := make([]ProfileDTO, 0, len(all))
	for _, r := range all {
		d := s.toDTO(r)
		if q != "" && !strings.Contains(strings.ToLower(r.Name+" "+r.Group+" "+strings.Join(r.Tags, " ")), q) {
			continue
		}
		if f.Group != "" && r.Group != f.Group {
			continue
		}
		if f.Tag != "" && !containsFold(r.Tags, f.Tag) {
			continue
		}
		if f.State != "" && d.State != f.State {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// GetProfile returns one profile.
func (s *ProfileService) GetProfile(ctx context.Context, id string) (ProfileDTO, error) {
	r, err := s.st.Profiles.Get(ctx, id)
	if err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	}
	return s.toDTO(r), nil
}

// ListGroups returns the distinct groups in use, sorted.
func (s *ProfileService) ListGroups(ctx context.Context) ([]string, error) {
	return s.distinct(ctx, func(r store.ProfileRecord) []string { return []string{r.Group} })
}

// ListTags returns the distinct tags in use, sorted.
func (s *ProfileService) ListTags(ctx context.Context) ([]string, error) {
	return s.distinct(ctx, func(r store.ProfileRecord) []string { return r.Tags })
}

func (s *ProfileService) distinct(ctx context.Context, pick func(store.ProfileRecord) []string) ([]string, error) {
	all, err := s.st.Profiles.List(ctx)
	if err != nil {
		return nil, profileToAPIError(err)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, r := range all {
		for _, v := range pick(r) {
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// ---- writes ------------------------------------------------------------------

// CreateProfile makes a profile with a coherent device and an empty data
// folder under the app data folder.
func (s *ProfileService) CreateProfile(ctx context.Context, in ProfileInput) (ProfileDTO, error) {
	if err := profCheckFields(&in); err != nil {
		return ProfileDTO{}, err
	}
	if in.Preset == "" {
		in.Preset = "windows-11"
	}
	if in.Seed == 0 {
		in.Seed = newSeed()
	}
	fp, err := s.buildFingerprint(in.Preset, in.Seed, in.Language, in.Timezone)
	if err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	}
	applyModes(&fp, in)
	if err := profValidate(fp); err != nil {
		return ProfileDTO{}, err
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if taken, err := s.nameTaken(ctx, in.Name, ""); err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	} else if taken {
		return ProfileDTO{}, &APIError{Code: "conflict", Message: "name is taken", Details: map[string]any{"reason": "name", "field": "name"}}
	}
	if err := s.checkProxy(ctx, in.ProxyID); err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	}
	id := store.NewID()
	rec, err := s.st.Profiles.Put(ctx, store.ProfileRecord{
		Profile: profile.Profile{ID: id, Name: in.Name, Group: in.Group, Tags: in.Tags, Notes: in.Notes, Fingerprint: fp, DataDir: s.dataDirOf(id)},
		ProxyID: in.ProxyID,
	})
	if err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	}
	return s.GetProfile(ctx, rec.ID)
}

// UpdateProfile edits a profile. While the browser runs only name, group,
// tags and notes may change: a changed device, language, modes or proxy gives
// code conflict.
func (s *ProfileService) UpdateProfile(ctx context.Context, id string, in ProfileInput) (ProfileDTO, error) {
	if err := profCheckFields(&in); err != nil {
		return ProfileDTO{}, err
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	cur, err := s.st.Profiles.Get(ctx, id)
	if err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	}
	if taken, err := s.nameTaken(ctx, in.Name, id); err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	} else if taken {
		return ProfileDTO{}, &APIError{Code: "conflict", Message: "name is taken", Details: map[string]any{"reason": "name", "field": "name"}}
	}
	fp := cur.Fingerprint
	preset := profPresetOf(fp)
	if in.Preset != "" && in.Preset != preset || in.Seed != 0 && in.Seed != fp.Seed {
		if in.Preset == "" {
			in.Preset = preset
		}
		seed := in.Seed
		if seed == 0 {
			seed = fp.Seed
		}
		lang, tz := fp.Language, fp.Timezone
		nfp, err := s.buildFingerprint(in.Preset, seed, lang, tz)
		if err != nil {
			return ProfileDTO{}, profileToAPIError(err)
		}
		nfp.Canvas, nfp.Audio, nfp.WebGL, nfp.Fonts, nfp.ClientRects, nfp.WebRTC = fp.Canvas, fp.Audio, fp.WebGL, fp.Fonts, fp.ClientRects, fp.WebRTC
		fp = nfp
	}
	fp = profile.ApplyGeo(fp, in.Timezone, in.Language)
	applyModes(&fp, in)
	if err := profValidate(fp); err != nil {
		return ProfileDTO{}, err
	}
	if err := s.checkProxy(ctx, in.ProxyID); err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	}
	if s.isRunning(id) && (fp != cur.Fingerprint || in.ProxyID != cur.ProxyID) {
		return ProfileDTO{}, profConflict("running", id)
	}
	cur.Name, cur.Group, cur.Tags, cur.Notes, cur.Fingerprint, cur.ProxyID = in.Name, in.Group, in.Tags, in.Notes, fp, in.ProxyID
	if _, err := s.st.Profiles.Put(ctx, cur); err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	}
	return s.GetProfile(ctx, id)
}

// CloneProfile copies the settings under a new name with a new seed and an
// empty data folder. An empty name gives "<name> (copy)".
func (s *ProfileService) CloneProfile(ctx context.Context, id, name string) (ProfileDTO, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	src, err := s.st.Profiles.Get(ctx, id)
	if err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		base := src.Name + " (copy)"
		name = base
		for i := 2; ; i++ {
			taken, err := s.nameTaken(ctx, name, "")
			if err != nil {
				return ProfileDTO{}, profileToAPIError(err)
			}
			if !taken {
				break
			}
			name = fmt.Sprintf("%s %d", base, i)
		}
	} else if taken, err := s.nameTaken(ctx, name, ""); err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	} else if taken {
		return ProfileDTO{}, &APIError{Code: "conflict", Message: "name is taken", Details: map[string]any{"reason": "name", "field": "name"}}
	}
	nfp := profile.Shuffle(src.Fingerprint, newSeed())
	nfp.PlatformVersion = presetVersion(profPresetOf(src.Fingerprint), nfp.PlatformVersion)
	nid := store.NewID()
	rec, err := s.st.Profiles.Put(ctx, store.ProfileRecord{
		Profile: profile.Profile{ID: nid, Name: name, Group: src.Group, Tags: src.Tags, Notes: src.Notes, Fingerprint: nfp, DataDir: s.dataDirOf(nid)},
		ProxyID: src.ProxyID,
	})
	if err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	}
	return s.GetProfile(ctx, rec.ID)
}

func presetVersion(presetID, fallback string) string {
	if p, ok := profPresetByID(presetID); ok {
		return p.PlatformVersion
	}
	return fallback
}

// ShuffleFingerprint gives each profile a new seed and so a new device. The
// OS, language, timezone and modes stay. A running profile gives conflict.
func (s *ProfileService) ShuffleFingerprint(ctx context.Context, ids []string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	recs, err := s.loadAll(ctx, ids)
	if err != nil {
		return profileToAPIError(err)
	}
	if run := s.runningAmong(ids); len(run) > 0 {
		return profConflict("running", run...)
	}
	for _, r := range recs {
		nfp := profile.Shuffle(r.Fingerprint, newSeed())
		nfp.PlatformVersion = presetVersion(profPresetOf(r.Fingerprint), nfp.PlatformVersion)
		if err := profValidate(nfp); err != nil {
			return err
		}
		r.Fingerprint = nfp
		if _, err := s.st.Profiles.Put(ctx, r); err != nil {
			return profileToAPIError(err)
		}
	}
	return nil
}

func (s *ProfileService) loadAll(ctx context.Context, ids []string) ([]store.ProfileRecord, error) {
	out := make([]store.ProfileRecord, 0, len(ids))
	for _, id := range ids {
		r, err := s.st.Profiles.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// DeleteProfiles removes profiles. All of them must exist and none may run.
// With deleteData the data folder goes too, but only when it lies inside the
// app data folder.
func (s *ProfileService) DeleteProfiles(ctx context.Context, ids []string, deleteData bool) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	recs, err := s.loadAll(ctx, ids)
	if err != nil {
		return profileToAPIError(err)
	}
	if run := s.runningAmong(ids); len(run) > 0 {
		return profConflict("running", run...)
	}
	root := filepath.Join(s.dataDir, "profiles") + string(os.PathSeparator)
	for _, r := range recs {
		if err := s.st.Profiles.Delete(ctx, r.ID); err != nil {
			return profileToAPIError(err)
		}
		s.mu.Lock()
		delete(s.faults, r.ID)
		s.mu.Unlock()
		if deleteData && strings.HasPrefix(filepath.Clean(r.DataDir)+string(os.PathSeparator), root) {
			if err := os.RemoveAll(r.DataDir); err != nil {
				return profileToAPIError(fmt.Errorf("delete data folder: %w", err))
			}
		}
	}
	return nil
}

// AssignProxy sets or clears (empty proxyId) the proxy of the profiles. A
// running profile gives conflict.
func (s *ProfileService) AssignProxy(ctx context.Context, ids []string, proxyID string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.checkProxy(ctx, proxyID); err != nil {
		return profileToAPIError(err)
	}
	recs, err := s.loadAll(ctx, ids)
	if err != nil {
		return profileToAPIError(err)
	}
	if run := s.runningAmong(ids); len(run) > 0 {
		return profConflict("running", run...)
	}
	for _, r := range recs {
		r.ProxyID = proxyID
		if _, err := s.st.Profiles.Put(ctx, r); err != nil {
			return profileToAPIError(err)
		}
	}
	return nil
}

// AssignProxyRoundRobin gives the profiles the proxies in turn.
func (s *ProfileService) AssignProxyRoundRobin(ctx context.Context, ids, proxyIDs []string) error {
	if len(proxyIDs) == 0 {
		return profInvalid("proxyIds", "no proxy given")
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	for _, p := range proxyIDs {
		if err := s.checkProxy(ctx, p); err != nil {
			return profileToAPIError(err)
		}
	}
	recs, err := s.loadAll(ctx, ids)
	if err != nil {
		return profileToAPIError(err)
	}
	if run := s.runningAmong(ids); len(run) > 0 {
		return profConflict("running", run...)
	}
	for i, r := range recs {
		r.ProxyID = proxyIDs[i%len(proxyIDs)]
		if _, err := s.st.Profiles.Put(ctx, r); err != nil {
			return profileToAPIError(err)
		}
	}
	return nil
}

// BulkSetGroup moves the profiles to a group. It works while they run.
func (s *ProfileService) BulkSetGroup(ctx context.Context, ids []string, group string) error {
	group = cleanText(group, 60)
	s.opMu.Lock()
	defer s.opMu.Unlock()
	recs, err := s.loadAll(ctx, ids)
	if err != nil {
		return profileToAPIError(err)
	}
	for _, r := range recs {
		r.Group = group
		if _, err := s.st.Profiles.Put(ctx, r); err != nil {
			return profileToAPIError(err)
		}
	}
	return nil
}

// BulkSetTags adds and removes tags on the profiles. It works while they run.
func (s *ProfileService) BulkSetTags(ctx context.Context, ids, add, remove []string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	recs, err := s.loadAll(ctx, ids)
	if err != nil {
		return profileToAPIError(err)
	}
	for _, r := range recs {
		var tags []string
		for _, t := range r.Tags {
			if !containsFold(remove, t) {
				tags = append(tags, t)
			}
		}
		r.Tags = cleanTags(append(tags, add...))
		if _, err := s.st.Profiles.Put(ctx, r); err != nil {
			return profileToAPIError(err)
		}
	}
	return nil
}

// ApplyProxyGeo sets timezone and language from the exit IP of the profile's
// proxy. It makes one request through the proxy (see docs/proxy-lookup.md).
func (s *ProfileService) ApplyProxyGeo(ctx context.Context, id string) (ProfileDTO, error) {
	r, err := s.st.Profiles.Get(ctx, id)
	if err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	}
	if r.ProxyID == "" {
		return ProfileDTO{}, profInvalid("proxyId", "the profile has no proxy")
	}
	if s.isRunning(id) {
		return ProfileDTO{}, profConflict("running", id)
	}
	res, err := s.prober.probe(ctx, r.Proxy)
	if err != nil {
		return ProfileDTO{}, proxyToAPIError(err)
	}
	if res.Timezone == "" && res.Language == "" {
		return ProfileDTO{}, &APIError{Code: "network", Message: "the lookup service gave no location", Retryable: true}
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	r, err = s.st.Profiles.Get(ctx, id)
	if err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	}
	r.Fingerprint = profile.ApplyGeo(r.Fingerprint, res.Timezone, res.Language)
	if err := profValidate(r.Fingerprint); err != nil {
		return ProfileDTO{}, err
	}
	if _, err := s.st.Profiles.Put(ctx, r); err != nil {
		return ProfileDTO{}, profileToAPIError(err)
	}
	return s.GetProfile(ctx, id)
}

// ---- run state ---------------------------------------------------------------

// StartProfiles launches the browsers. Profiles start at the same time. Each
// result says ok or gives the error code, so one bad profile does not stop
// the others. A running profile counts as ok.
func (s *ProfileService) StartProfiles(ctx context.Context, ids []string) ([]StartResult, error) {
	out := make([]StartResult, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = StartResult{ProfileID: id, OK: true}
			if err := s.startOne(ctx, id); err != nil {
				ae := profileToAPIError(err).(*APIError)
				out[i] = StartResult{ProfileID: id, Error: ae}
			}
		}()
	}
	wg.Wait()
	return out, nil
}

const launchTimeout = 2 * time.Minute

func (s *ProfileService) startOne(ctx context.Context, id string) error {
	s.mu.Lock()
	if _, ok := s.running[id]; ok {
		s.mu.Unlock()
		return nil
	}
	if s.starting[id] {
		s.mu.Unlock()
		return profConflict("starting", id)
	}
	s.starting[id] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.starting, id)
		s.mu.Unlock()
	}()

	rec, err := s.st.Profiles.Get(ctx, id)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		ae := profileToAPIError(err).(*APIError)
		kind := "engineExit"
		if strings.HasPrefix(ae.Code, "proxy.") {
			kind = "proxyDown"
		}
		s.mu.Lock()
		s.faults[id] = profileFault{err: kind, code: ae.Code}
		s.mu.Unlock()
		s.setState(id, "error", kind, ae.Code)
		return ae
	}
	// A profile with a proxy never starts without it: fail closed.
	if rec.Proxy.Scheme != profile.ProxyNone {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(rec.Proxy.Host, strconv.Itoa(rec.Proxy.Port)), 8*time.Second)
		if err != nil {
			return fail(&APIError{Code: "proxy.down", Message: "the proxy is not reachable: " + err.Error(), Details: map[string]any{"proxyId": rec.ProxyID}, Retryable: true})
		}
		_ = c.Close()
	}
	lctx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()
	b, err := s.launcher.Launch(lctx, rec.Profile)
	if err != nil {
		return fail(err)
	}
	rp := &runningProfile{browser: b, since: time.Now()}
	s.mu.Lock()
	s.running[id] = rp
	delete(s.faults, id)
	s.mu.Unlock()
	s.setState(id, "running", "", "")
	go s.watch(id, rp)
	return nil
}

// watch turns a browser that ended by itself (the user closed the window)
// into state stopped and frees its relay.
func (s *ProfileService) watch(id string, rp *runningProfile) {
	<-rp.browser.Done()
	s.mu.Lock()
	cur, ok := s.running[id]
	if ok && cur == rp {
		delete(s.running, id)
	}
	s.mu.Unlock()
	if ok && cur == rp {
		_ = rp.browser.Close()
		s.setState(id, "stopped", "", "")
	}
}

// StopProfiles closes the browsers and their relays. A profile that does not
// run is ignored.
func (s *ProfileService) StopProfiles(ctx context.Context, ids []string) error {
	var first error
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, id := range ids {
		s.mu.Lock()
		rp, ok := s.running[id]
		if ok {
			delete(s.running, id)
		}
		delete(s.faults, id)
		s.mu.Unlock()
		if !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := rp.browser.Close()
			s.setState(id, "stopped", "", "")
			if err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return profileToAPIError(first)
}

// StopAll stops every running profile.
func (s *ProfileService) StopAll(ctx context.Context) error {
	s.mu.Lock()
	ids := make([]string, 0, len(s.running))
	for id := range s.running {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	return s.StopProfiles(ctx, ids)
}
