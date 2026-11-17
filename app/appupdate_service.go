package main

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"

	"github.com/8bu/ladilala/core/driver"
)

// updateStages are the steps of an app update, in order. The Wails updater
// downloads, verifies the signature and stages the payload in one call; the
// stages follow its progress events.
var updateStages = []string{"check", "download", "verify", "prepare", "ready"}

// Phases of AppUpdateState.
const (
	phaseIdle        = "idle"
	phaseChecking    = "checking"
	phaseAvailable   = "available"
	phaseDownloading = "downloading"
	phaseReady       = "ready"
	phaseFailed      = "failed"
)

// appUpdater is the part of the Wails updater this service uses. Tests
// replace it with a fake.
type appUpdater interface {
	Check(ctx context.Context) (*updater.Release, error)
	DownloadAndInstall(ctx context.Context) error
	Restart(ctx context.Context) error
	SkipVersion(v string)
}

// AppRelease is a release the feed offers.
type AppRelease struct {
	Version     string   `json:"version"`
	Channel     string   `json:"channel"`
	Name        string   `json:"name"`
	Notes       string   `json:"notes"` // release notes text from the feed
	News        []string `json:"news"`  // metadata "news" of the manifest, when present
	Fixed       []string `json:"fixed"` // metadata "fixed" of the manifest, when present
	PublishedAt string   `json:"publishedAt"`
	SizeBytes   int64    `json:"sizeBytes"`
	// MinEngine is the lowest Chrome major version the release needs
	// (manifest metadata "minEngine"), or 0 when it states none.
	MinEngine int `json:"minEngine"`
}

// AppUpdateState is everything the Settings screen, the sidebar footer and
// the update sheet show. It arrives after every change as the event
// "appupdate:state".
type AppUpdateState struct {
	Current       string `json:"current"`
	Channel       string `json:"channel"`   // stable | beta | nightly
	AutoCheck     string `json:"autoCheck"` // daily | weekly | off
	InstallOnQuit bool   `json:"installOnQuit"`
	Skipped       string `json:"skipped"`
	LastChecked   string `json:"lastChecked"`
	// Configured is false while no feed URL is set. Then the app cannot check
	// for updates, and the UI says so.
	Configured bool `json:"configured"`
	// FeedURL is the feed in settings. FeedFromEnv is true when
	// LADILALA_UPDATE_URL replaces it.
	FeedURL     string      `json:"feedUrl"`
	FeedFromEnv bool        `json:"feedFromEnv"`
	Phase       string      `json:"phase"`
	Release     *AppRelease `json:"release"`
	Stages      []JobStage  `json:"stages"`
	BytesDone   int64       `json:"bytesDone"`
	BytesTotal  int64       `json:"bytesTotal"`
	BytesPerSec int64       `json:"bytesPerSec"`
	Failure     *APIError   `json:"failure,omitempty"`
	// WhenIdle is true while the app waits for the last run to end before it
	// restarts.
	WhenIdle   bool `json:"whenIdle"`
	ActiveRuns int  `json:"activeRuns"`
	// EngineMajor is the Chrome major version of the default engine, 0 when
	// unknown. EngineCompatible is shown only; nothing blocks the update.
	EngineMajor      int  `json:"engineMajor"`
	EngineCompatible bool `json:"engineCompatible"`
}

// AppUpdateSettings are the values the user can change.
type AppUpdateSettings struct {
	Channel       string `json:"channel"`
	AutoCheck     string `json:"autoCheck"`
	InstallOnQuit bool   `json:"installOnQuit"`
	FeedURL       string `json:"feedUrl"`
}

// AppUpdateService updates Ladilala itself over the Wails updater.
type AppUpdateService struct {
	b       *Backend
	cfg     *settingsStore
	version string
	up      func() appUpdater
	engines *EngineService
	runs    func() int
	emit    func(string, any)
	idleGap time.Duration
	quit    func() // ends the app; set by newAppUpdateService

	mu      sync.Mutex
	st      AppUpdateState
	cancel  context.CancelFunc // of the running download
	idleCh  context.CancelFunc // of the restart-when-idle wait
	started bool
	// handingOff is set once BeforeQuit passed the update to the updater.
	handingOff bool
	stop       chan struct{}
}

func newAppUpdateService(b *Backend, eng *EngineService) *AppUpdateService {
	s := &AppUpdateService{
		b: b, cfg: b.Settings, version: version, engines: eng, emit: b.Emit,
		idleGap: 2 * time.Second, stop: make(chan struct{}),
	}
	s.up = func() appUpdater {
		if b.App == nil || b.App.Updater == nil {
			return nil
		}
		return b.App.Updater
	}
	if b.Engine != nil {
		s.runs = b.Engine.ActiveRuns
	}
	s.quit = func() {
		if b.App != nil {
			b.App.Quit()
		}
	}
	b.BeforeQuit = s.BeforeQuit
	s.st = s.baseState()
	return s
}

// ---- feed provider ----------------------------------------------------------------

// feedProvider is the one updater.Provider of the app. It reads the feed URL
// and the channel from the settings on every call, so the user can change
// them without a restart (the Wails updater accepts Init only once). With no
// feed URL it returns errUpdatesNotConfigured.
type feedProvider struct{ cfg *settingsStore }

var errUpdatesNotConfigured = errors.New("updates are not configured: no feed URL")

func newFeedProvider(cfg *settingsStore) *feedProvider { return &feedProvider{cfg: cfg} }

// feedURL returns the feed and whether the environment variable set it.
func feedURL(cfg *settingsStore) (string, bool) {
	if v := strings.TrimSpace(os.Getenv("LADILALA_UPDATE_URL")); v != "" {
		return v, true
	}
	return strings.TrimSpace(cfg.Get().Update.FeedURL), false
}

func (p *feedProvider) Name() string { return "endpoint" }

func (p *feedProvider) inner() (*endpoint.Provider, error) {
	url, _ := feedURL(p.cfg)
	if url == "" {
		return nil, errUpdatesNotConfigured
	}
	return endpoint.New(endpoint.Config{URL: url, Channel: p.cfg.Get().Update.Channel})
}

func (p *feedProvider) Check(ctx context.Context, req updater.CheckRequest) (*updater.Release, error) {
	in, err := p.inner()
	if err != nil {
		return nil, err
	}
	return in.Check(ctx, req)
}

func (p *feedProvider) Download(ctx context.Context, r *updater.Release, dst io.Writer, onProgress func(written, total int64)) error {
	in, err := p.inner()
	if err != nil {
		return err
	}
	return in.Download(ctx, r, dst, onProgress)
}

// ---- lifecycle --------------------------------------------------------------------

// ServiceStartup listens to the updater's progress events and starts the
// timer for automatic checks.
func (s *AppUpdateService) ServiceStartup(context.Context, application.ServiceOptions) error {
	if s.b.App != nil {
		s.b.App.Event.On(updater.EventDownloadProgress, func(e *application.CustomEvent) {
			if p, ok := e.Data.(updater.Progress); ok {
				s.onProgress(p.Written, p.Total, int64(p.Rate))
			}
		})
	}
	go s.autoCheckLoop()
	return nil
}

// ServiceShutdown stops the timers.
func (s *AppUpdateService) ServiceShutdown() error {
	close(s.stop)
	s.mu.Lock()
	if s.idleCh != nil {
		s.idleCh()
	}
	s.mu.Unlock()
	return nil
}

// BeforeQuit is the Wails ShouldQuit hook and the main window's close hook. When a downloaded update is ready,
// the user chose "install when I quit" and no run is active, the first quit
// request is held back and the update is handed to the Wails updater helper.
// The helper waits for this process to exit, then swaps the files. The hand-off
// cannot run in ServiceShutdown: Updater.Restart calls App.Quit, and a Quit
// inside the shutdown sequence never lets the process exit (seen on Windows:
// the helper timed out after 30 s and relaunched the old version). Restart
// runs on its own goroutine here and its Quit passes this hook a second time,
// when it answers true.
func (s *AppUpdateService) BeforeQuit() bool {
	s.mu.Lock()
	hand := s.st.Phase == phaseReady && s.cfg.Get().Update.InstallOnQuit && !s.handingOff
	if hand && s.activeRuns() == 0 && s.up() != nil {
		s.handingOff = true
		s.mu.Unlock()
		go s.handOff(s.up())
		return false
	}
	s.mu.Unlock()
	return true
}

func (s *AppUpdateService) handOff(up appUpdater) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := up.Restart(ctx); err != nil {
		log.Printf("update: install on quit failed, quitting without it: %v", err)
		s.quit()
	}
}

func (s *AppUpdateService) activeRuns() int {
	if s.runs == nil {
		return 0
	}
	return s.runs()
}

func (s *AppUpdateService) autoCheckLoop() {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for first := true; ; first = false {
		if !first {
			select {
			case <-s.stop:
				return
			case <-t.C:
			}
		}
		u := s.cfg.Get().Update
		if every := autoCheckEvery(u.AutoCheck); every > 0 && s.configured() {
			last, _ := time.Parse(time.RFC3339, u.LastChecked)
			if time.Since(last) > every {
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				_, _ = s.check(ctx, false)
				cancel()
			}
		}
	}
}

func (s *AppUpdateService) configured() bool {
	url, _ := feedURL(s.cfg)
	return url != ""
}

// ---- state ------------------------------------------------------------------------

func newStages() []JobStage {
	out := make([]JobStage, len(updateStages))
	for i, k := range updateStages {
		out[i] = JobStage{Key: k, State: "pending"}
	}
	return out
}

// baseState fills the fields that come from settings and the machine.
func (s *AppUpdateService) baseState() AppUpdateState {
	u := s.cfg.Get().Update
	url, env := feedURL(s.cfg)
	st := AppUpdateState{
		Current: s.version, Channel: u.Channel, AutoCheck: u.AutoCheck, InstallOnQuit: u.InstallOnQuit,
		Skipped: u.SkippedVersion, LastChecked: u.LastChecked, Configured: url != "",
		FeedURL: u.FeedURL, FeedFromEnv: env, Phase: phaseIdle, Stages: newStages(),
	}
	if st.FeedFromEnv {
		st.FeedURL = url
	}
	return st
}

// refreshLocked updates the parts of the state that settings and the machine
// own, keeping phase, release and stages.
func (s *AppUpdateService) refreshLocked() {
	b := s.baseState()
	b.Phase, b.Release, b.Stages, b.Failure = s.st.Phase, s.st.Release, s.st.Stages, s.st.Failure
	b.BytesDone, b.BytesTotal, b.BytesPerSec, b.WhenIdle = s.st.BytesDone, s.st.BytesTotal, s.st.BytesPerSec, s.st.WhenIdle
	if !b.Configured && b.Phase == phaseIdle {
		b.Release = nil
	}
	b.ActiveRuns = s.activeRuns()
	if s.engines != nil {
		b.EngineMajor = s.engines.defaultMajor()
	}
	b.EngineCompatible = b.Release == nil || b.Release.MinEngine == 0 || b.EngineMajor >= b.Release.MinEngine
	s.st = b
}

func (s *AppUpdateService) snapshotLocked() AppUpdateState {
	s.refreshLocked()
	d := s.st
	d.Stages = append([]JobStage(nil), d.Stages...)
	return d
}

func (s *AppUpdateService) publish() {
	s.mu.Lock()
	d := s.snapshotLocked()
	s.mu.Unlock()
	if s.emit != nil {
		s.emit("appupdate:state", d)
	}
}

func (s *AppUpdateService) mutate(fn func(*AppUpdateState)) AppUpdateState {
	s.mu.Lock()
	fn(&s.st)
	d := s.snapshotLocked()
	s.mu.Unlock()
	if s.emit != nil {
		s.emit("appupdate:state", d)
	}
	return d
}

func (st *AppUpdateState) setStage(key, state, code string, params map[string]any) {
	for i := range st.Stages {
		if st.Stages[i].Key == key {
			st.Stages[i].State, st.Stages[i].Code, st.Stages[i].Params = state, code, params
		}
	}
}

// GetAppUpdateState returns the state for the Settings screen and the sidebar.
func (s *AppUpdateService) GetAppUpdateState(context.Context) (AppUpdateState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked(), nil
}

// SetAppUpdateSettings saves channel, check interval, install-on-quit and the
// feed URL. A new channel or feed drops an offered release: it may not exist
// in the new feed.
func (s *AppUpdateService) SetAppUpdateSettings(_ context.Context, in AppUpdateSettings) (AppUpdateState, error) {
	var problems []map[string]string
	if !oneOf(in.Channel, "stable", "beta", "nightly") {
		problems = append(problems, map[string]string{"path": "channel", "msg": "must be stable, beta or nightly"})
	}
	if !oneOf(in.AutoCheck, "daily", "weekly", "off") {
		problems = append(problems, map[string]string{"path": "autoCheck", "msg": "must be daily, weekly or off"})
	}
	in.FeedURL = strings.TrimSpace(in.FeedURL)
	if in.FeedURL != "" && !strings.HasPrefix(in.FeedURL, "https://") && !strings.HasPrefix(in.FeedURL, "http://127.0.0.1") && !strings.HasPrefix(in.FeedURL, "http://localhost") {
		problems = append(problems, map[string]string{"path": "feedUrl", "msg": "must be an https URL"})
	}
	if len(problems) > 0 {
		return AppUpdateState{}, apiErr("invalid", "update settings are not valid", "problems", problems)
	}
	prev := s.cfg.Get().Update
	if _, err := s.cfg.Update(func(c *Settings) error {
		c.Update.Channel, c.Update.AutoCheck, c.Update.InstallOnQuit, c.Update.FeedURL = in.Channel, in.AutoCheck, in.InstallOnQuit, in.FeedURL
		return nil
	}); err != nil {
		return AppUpdateState{}, asAPIError(err)
	}
	changed := prev.Channel != in.Channel || prev.FeedURL != in.FeedURL
	return s.mutate(func(st *AppUpdateState) {
		if changed && st.Phase != phaseDownloading && st.Phase != phaseReady {
			st.Release, st.Phase, st.Failure, st.Stages = nil, phaseIdle, nil, newStages()
		}
	}), nil
}

// ---- check ------------------------------------------------------------------------

// CheckAppUpdate asks the feed for a newer version. The Check button passes
// force=true: it shows a release the user skipped before and clears the skip.
func (s *AppUpdateService) CheckAppUpdate(ctx context.Context, force bool) (AppUpdateState, error) {
	return s.check(ctx, force)
}

func (s *AppUpdateService) check(ctx context.Context, force bool) (AppUpdateState, error) {
	if !s.configured() {
		return s.mutate(func(st *AppUpdateState) { st.Phase, st.Release, st.Failure = phaseIdle, nil, nil }),
			apiErr("update.notConfigured", "no update feed is set")
	}
	up := s.up()
	if up == nil {
		return AppUpdateState{}, apiErr("update.notConfigured", "the updater is not running")
	}
	s.mu.Lock()
	busy := s.st.Phase == phaseChecking || s.st.Phase == phaseDownloading || s.st.Phase == phaseReady
	s.mu.Unlock()
	if busy {
		return s.GetAppUpdateState(ctx)
	}
	if force {
		up.SkipVersion("")
		_, _ = s.cfg.Update(func(c *Settings) error { c.Update.SkippedVersion = ""; return nil })
	}
	s.mutate(func(st *AppUpdateState) { st.Phase, st.Failure = phaseChecking, nil })
	rel, err := up.Check(ctx)
	now := time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		ae := toUpdateError(err)
		s.mutate(func(st *AppUpdateState) { st.Phase, st.Failure = phaseFailed, ae })
		return s.GetAppUpdateState(ctx)
	}
	_, _ = s.cfg.Update(func(c *Settings) error { c.Update.LastChecked = now; return nil })
	return s.mutate(func(st *AppUpdateState) {
		st.Failure, st.Stages = nil, newStages()
		if rel == nil {
			st.Phase, st.Release = phaseIdle, nil
			return
		}
		st.Phase, st.Release = phaseAvailable, releaseOf(rel)
		st.setStage("check", "done", "latest", map[string]any{"version": rel.Version})
	}), nil
}

func releaseOf(r *updater.Release) *AppRelease {
	out := &AppRelease{
		Version: r.Version, Channel: r.Channel, Name: r.Name, Notes: r.Notes, SizeBytes: r.Artifact.Size,
		News: stringList(r.Metadata["news"]), Fixed: stringList(r.Metadata["fixed"]),
	}
	if !r.PublishedAt.IsZero() {
		out.PublishedAt = r.PublishedAt.UTC().Format(time.RFC3339)
	}
	switch v := r.Metadata["minEngine"].(type) {
	case float64:
		out.MinEngine = int(v)
	case int:
		out.MinEngine = v
	case string:
		out.MinEngine, _ = strconv.Atoi(v)
	}
	return out
}

func stringList(v any) []string {
	out := []string{}
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func toUpdateError(err error) *APIError {
	e := &APIError{Code: "update.network", Message: err.Error(), Retryable: true, Details: map[string]any{"fixId": "retry"}}
	m := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, errUpdatesNotConfigured):
		e.Code, e.Retryable, e.Details = "update.notConfigured", false, nil
	case driver.IsDiskFull(err):
		e.Code, e.Details = "update.disk", map[string]any{"fixId": "retry"}
	case strings.Contains(m, "signature") || strings.Contains(m, "digest") || strings.Contains(m, "verif"):
		e.Code, e.Retryable, e.Details = "update.signature", false, map[string]any{"fixId": "redownload"}
	case errors.Is(err, updater.ErrNoPendingRelease):
		e.Code, e.Details = "update.noRelease", map[string]any{"fixId": "retry"}
	}
	return e
}

// ---- download ---------------------------------------------------------------------

func (s *AppUpdateService) onProgress(written, total, rate int64) {
	s.mu.Lock()
	if s.st.Phase != phaseDownloading {
		s.mu.Unlock()
		return
	}
	s.st.BytesDone, s.st.BytesTotal, s.st.BytesPerSec = written, total, rate
	d := s.snapshotLocked()
	s.mu.Unlock()
	if total > 0 && written >= total {
		s.mutate(func(st *AppUpdateState) {
			st.setStage("download", "done", "downloaded", map[string]any{"bytes": total})
			st.setStage("verify", "active", "", nil)
		})
		return
	}
	if s.emit != nil {
		s.emit("appupdate:state", d)
	}
}

// DownloadAppUpdate downloads, verifies and stages the offered release in the
// background. Watch the event "appupdate:state". The install itself happens at
// RestartNow, RestartWhenIdle or, when the setting is on, when the app quits.
// The engine version needed by the release is shown but does not block it.
func (s *AppUpdateService) DownloadAppUpdate(ctx context.Context) (AppUpdateState, error) {
	up := s.up()
	s.mu.Lock()
	if s.st.Release == nil || (s.st.Phase != phaseAvailable && s.st.Phase != phaseFailed) {
		s.mu.Unlock()
		return AppUpdateState{}, apiErr("conflict", "there is no release to download", "reason", "noRelease")
	}
	if up == nil {
		s.mu.Unlock()
		return AppUpdateState{}, apiErr("update.notConfigured", "the updater is not running")
	}
	dctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.st.Phase, s.st.Failure, s.st.Stages = phaseDownloading, nil, newStages()
	s.st.setStage("check", "done", "latest", map[string]any{"version": s.st.Release.Version})
	s.st.setStage("download", "active", "", nil)
	s.st.BytesDone, s.st.BytesTotal, s.st.BytesPerSec = 0, s.st.Release.SizeBytes, 0
	s.mu.Unlock()
	s.publish()
	go s.download(dctx, up)
	return s.GetAppUpdateState(ctx)
}

func (s *AppUpdateService) download(ctx context.Context, up appUpdater) {
	err := up.DownloadAndInstall(ctx)
	s.mu.Lock()
	s.cancel = nil
	s.mu.Unlock()
	switch {
	case errors.Is(err, context.Canceled):
		s.mutate(func(st *AppUpdateState) {
			st.Phase, st.Stages, st.Failure = phaseAvailable, newStages(), nil
			st.setStage("check", "done", "latest", map[string]any{"version": st.Release.Version})
		})
	case err != nil:
		ae := toUpdateError(err)
		s.mutate(func(st *AppUpdateState) {
			st.Phase, st.Failure = phaseFailed, ae
			failed := false
			for i := range st.Stages {
				if st.Stages[i].State == "active" {
					st.Stages[i].State, st.Stages[i].Code, failed = "fail", ae.Code, true
				}
			}
			if !failed {
				st.setStage("download", "fail", ae.Code, nil)
			}
		})
	default:
		s.mutate(func(st *AppUpdateState) {
			for _, k := range []string{"download", "verify", "prepare", "ready"} {
				st.setStage(k, "done", map[string]string{"verify": "signed", "prepare": "staged"}[k], nil)
			}
			st.Phase = phaseReady
			if st.Release != nil {
				st.BytesDone = st.BytesTotal
			}
		})
	}
}

// CancelAppUpdate stops a running download. The offered release stays.
func (s *AppUpdateService) CancelAppUpdate(ctx context.Context) (AppUpdateState, error) {
	s.mu.Lock()
	c := s.cancel
	s.mu.Unlock()
	if c != nil {
		c()
	}
	return s.GetAppUpdateState(ctx)
}

// ApplyUpdateFix runs the fix of a failed update. retry checks the feed again
// and downloads again, redownload does the same after the updater discards its
// staged file.
func (s *AppUpdateService) ApplyUpdateFix(ctx context.Context, fixID string) (AppUpdateState, error) {
	if fixID != "retry" && fixID != "redownload" {
		return AppUpdateState{}, apiErr("invalid", "unknown fix", "fixId", fixID)
	}
	s.mu.Lock()
	failed := s.st.Phase == phaseFailed
	hadRelease := s.st.Release != nil
	s.mu.Unlock()
	if !failed {
		return AppUpdateState{}, apiErr("conflict", "nothing failed", "reason", "notFailed")
	}
	if hadRelease && s.stageFailed("download", "verify", "prepare") {
		return s.DownloadAppUpdate(ctx)
	}
	s.mutate(func(st *AppUpdateState) { st.Phase = phaseIdle })
	st, err := s.check(ctx, true)
	if err != nil || st.Phase != phaseAvailable {
		return st, err
	}
	return s.DownloadAppUpdate(ctx)
}

func (s *AppUpdateService) stageFailed(keys ...string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.st.Stages {
		for _, k := range keys {
			if st.Key == k && st.State == "fail" {
				return true
			}
		}
	}
	return false
}

// ---- install ----------------------------------------------------------------------

// RestartNow swaps the files and restarts the app. It refuses while a run is
// active: restarting would kill the run's browsers.
func (s *AppUpdateService) RestartNow(ctx context.Context) error {
	s.mu.Lock()
	ready := s.st.Phase == phaseReady
	s.mu.Unlock()
	if !ready {
		return apiErr("conflict", "no update is ready", "reason", "notReady")
	}
	if n := s.activeRuns(); n > 0 {
		return apiErr("conflict", "a run is active", "reason", "runActive", "runs", n)
	}
	up := s.up()
	if up == nil {
		return apiErr("update.notConfigured", "the updater is not running")
	}
	if err := up.Restart(ctx); err != nil {
		return apiErr("update.restart", err.Error(), "fixId", "retry")
	}
	return nil
}

// RestartWhenIdle waits until no run is active, then restarts. A second call
// does nothing. CancelRestartWhenIdle stops the wait.
func (s *AppUpdateService) RestartWhenIdle(ctx context.Context) (AppUpdateState, error) {
	s.mu.Lock()
	if s.st.Phase != phaseReady {
		s.mu.Unlock()
		return AppUpdateState{}, apiErr("conflict", "no update is ready", "reason", "notReady")
	}
	if s.st.WhenIdle {
		d := s.snapshotLocked()
		s.mu.Unlock()
		return d, nil
	}
	wctx, cancel := context.WithCancel(context.Background())
	s.idleCh = cancel
	s.st.WhenIdle = true
	s.mu.Unlock()
	s.publish()
	go func() {
		t := time.NewTicker(s.idleGap)
		defer t.Stop()
		for {
			if s.activeRuns() == 0 {
				break
			}
			select {
			case <-wctx.Done():
				return
			case <-t.C:
			}
		}
		if err := s.RestartNow(wctx); err != nil {
			ae := asAPIError(err).(*APIError)
			s.mutate(func(st *AppUpdateState) { st.WhenIdle, st.Failure = false, ae })
		}
	}()
	return s.GetAppUpdateState(ctx)
}

// CancelRestartWhenIdle stops waiting for the runs to end.
func (s *AppUpdateService) CancelRestartWhenIdle(ctx context.Context) (AppUpdateState, error) {
	s.mu.Lock()
	if s.idleCh != nil {
		s.idleCh()
		s.idleCh = nil
	}
	s.st.WhenIdle = false
	s.mu.Unlock()
	s.publish()
	return s.GetAppUpdateState(ctx)
}

// SkipVersion hides a release until the user clicks Check Now.
func (s *AppUpdateService) SkipVersion(ctx context.Context, v string) (AppUpdateState, error) {
	if v == "" {
		return AppUpdateState{}, apiErr("invalid", "version is empty", "field", "version")
	}
	if up := s.up(); up != nil {
		up.SkipVersion(v)
	}
	if _, err := s.cfg.Update(func(c *Settings) error { c.Update.SkippedVersion = v; return nil }); err != nil {
		return AppUpdateState{}, asAPIError(err)
	}
	return s.mutate(func(st *AppUpdateState) {
		if st.Release != nil && st.Release.Version == v && st.Phase == phaseAvailable {
			st.Release, st.Phase, st.Stages = nil, phaseIdle, newStages()
		}
	}), nil
}

// GetReleaseNotes returns the offered release when its version is v.
func (s *AppUpdateService) GetReleaseNotes(_ context.Context, v string) (AppRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Release == nil || s.st.Release.Version != v {
		return AppRelease{}, apiErr("notFound", "the feed offers no release with this version", "version", v)
	}
	return *s.st.Release, nil
}
