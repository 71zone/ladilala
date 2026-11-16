package main

import (
	"context"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// appLogLevel is the level of the app log. SettingsService changes it.
var appLogLevel slog.LevelVar

// SettingsService is the Settings screen: language, theme, defaults for runs,
// data folder and logs. The values live in settings.json (settings_store.go).
type SettingsService struct {
	b       *Backend
	boot    string // folder that holds the data folder pointer
	openDir func(path string) error
}

func newSettingsService(b *Backend) *SettingsService {
	boot, _ := bootstrapDir()
	s := &SettingsService{b: b, boot: boot, openDir: openInFileManager}
	appLogLevel.Set(logLevelOf(b.Settings.Get().LogLevel))
	return s
}

// GeneralSettings are the values the Settings screen edits in one save. The
// update and engine groups have their own services.
type GeneralSettings struct {
	Language       string `json:"language"`
	Theme          string `json:"theme"`
	DefaultDelayMs int    `json:"defaultDelayMs"`
	Concurrency    int    `json:"concurrency"`
	ProxyTestURL   string `json:"proxyTestUrl"`
	LogLevel       string `json:"logLevel"`
	DefaultEngine  string `json:"defaultEngine"`
}

func generalOf(s Settings) GeneralSettings {
	return GeneralSettings{
		Language: s.Language, Theme: s.Theme, DefaultDelayMs: s.DefaultDelayMs, Concurrency: s.Concurrency,
		ProxyTestURL: s.ProxyTestURL, LogLevel: s.LogLevel, DefaultEngine: s.Engines.DefaultEngine,
	}
}

// GetSettings returns the saved settings, with defaults for what was never set.
func (s *SettingsService) GetSettings(context.Context) (GeneralSettings, error) {
	return generalOf(s.b.Settings.Get()), nil
}

// SaveSettings validates and saves the general settings. The default engine is
// not saved here: EngineService.SetDefaultEngine owns it, because changing it
// also changes which browser the run engine launches.
func (s *SettingsService) SaveSettings(_ context.Context, in GeneralSettings) (GeneralSettings, error) {
	var problems []map[string]string
	bad := func(path, msg string) { problems = append(problems, map[string]string{"path": path, "msg": msg}) }
	if in.Language != "vi" && in.Language != "en" {
		bad("language", "must be vi or en")
	}
	if !oneOf(in.Theme, "system", "light", "dark") {
		bad("theme", "must be system, light or dark")
	}
	if in.Concurrency < 1 || in.Concurrency > maxConcurrency {
		bad("concurrency", "must be from 1 to 64")
	}
	if in.DefaultDelayMs < 0 || in.DefaultDelayMs > maxDelayMs {
		bad("defaultDelayMs", "must be from 0 to 600000")
	}
	in.ProxyTestURL = strings.TrimSpace(in.ProxyTestURL)
	if in.ProxyTestURL == "" {
		in.ProxyTestURL = defaultProxyTestURL
	}
	if u, err := url.Parse(in.ProxyTestURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		bad("proxyTestUrl", "must be an http or https URL")
	}
	if !oneOf(in.LogLevel, "error", "warn", "info", "debug") {
		bad("logLevel", "must be error, warn, info or debug")
	}
	if len(problems) > 0 {
		return GeneralSettings{}, apiErr("invalid", "settings are not valid", "problems", problems)
	}
	next, err := s.b.Settings.Update(func(c *Settings) error {
		c.Language, c.Theme, c.DefaultDelayMs, c.Concurrency = in.Language, in.Theme, in.DefaultDelayMs, in.Concurrency
		c.ProxyTestURL, c.LogLevel = in.ProxyTestURL, in.LogLevel
		return nil
	})
	if err != nil {
		return GeneralSettings{}, asAPIError(err)
	}
	appLogLevel.Set(logLevelOf(next.LogLevel))
	return generalOf(next), nil
}

// SetLocale saves the language. The UI also keeps it in localStorage so the
// first paint is in the right language.
func (s *SettingsService) SetLocale(_ context.Context, lang string) error {
	if lang != "vi" && lang != "en" {
		return apiErr("invalid", "language must be vi or en", "field", "language")
	}
	_, err := s.b.Settings.Update(func(c *Settings) error { c.Language = lang; return nil })
	return asAPIError(err)
}

// SetTheme saves the theme: system, light or dark.
func (s *SettingsService) SetTheme(_ context.Context, theme string) error {
	if !oneOf(theme, "system", "light", "dark") {
		return apiErr("invalid", "theme must be system, light or dark", "field", "theme")
	}
	_, err := s.b.Settings.Update(func(c *Settings) error { c.Theme = theme; return nil })
	return asAPIError(err)
}

// DataFolderInfo describes the data folder.
type DataFolderInfo struct {
	// Path is the folder in use now, with the separators of this OS.
	Path string `json:"path"`
	// Pending is the folder the app uses after the next start. It is empty
	// when the user changed nothing, and equal to Default when the user reset it.
	Pending string `json:"pending"`
	Default string `json:"default"`
	// Overridden is true when LADILALA_DATA fixes the folder. Then the
	// setting has no effect.
	Overridden bool `json:"overridden"`
}

// GetDataFolder returns the folder in use and the one chosen for the next start.
func (s *SettingsService) GetDataFolder(context.Context) (DataFolderInfo, error) {
	info := DataFolderInfo{Path: filepath.Clean(s.b.DataDir), Default: filepath.Clean(s.boot), Overridden: os.Getenv("LADILALA_DATA") != ""}
	if p := readPointer(s.boot); p != "" {
		info.Pending = filepath.Clean(p)
	} else {
		info.Pending = info.Default
	}
	if info.Pending == info.Path {
		info.Pending = ""
	}
	return info, nil
}

// DataFolderUsage returns the size in bytes of the data folder. It walks the
// folder, so call it once when the screen opens, not on a timer.
func (s *SettingsService) DataFolderUsage(ctx context.Context) (int64, error) {
	var n int64
	err := filepath.WalkDir(s.b.DataDir, func(_ string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			n += fi.Size()
		}
		return nil
	})
	return n, asAPIError(err)
}

// ChangeDataFolder chooses the data folder for the next start. It does not
// move any data: the new folder starts empty and the old one stays on disk.
// An empty path goes back to the default folder.
func (s *SettingsService) ChangeDataFolder(ctx context.Context, path string) (DataFolderInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		if err := writePointer(s.boot, ""); err != nil {
			return DataFolderInfo{}, asAPIError(err)
		}
		return s.GetDataFolder(ctx)
	}
	if !filepath.IsAbs(path) {
		return DataFolderInfo{}, apiErr("invalid", "data folder must be an absolute path", "field", "path")
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return DataFolderInfo{}, apiErr("settings.dataFolder", err.Error(), "path", path)
	}
	probe, err := os.CreateTemp(path, ".ladilala-write-*")
	if err != nil {
		return DataFolderInfo{}, apiErr("settings.dataFolder", err.Error(), "path", path)
	}
	probe.Close()
	_ = os.Remove(probe.Name())
	if err := writePointer(s.boot, path); err != nil {
		return DataFolderInfo{}, asAPIError(err)
	}
	return s.GetDataFolder(ctx)
}

// PickDataFolder opens the native folder dialog. A cancelled dialog gives an
// empty path and no error. Pass the result to ChangeDataFolder.
func (s *SettingsService) PickDataFolder(context.Context) (string, error) {
	if s.b.App == nil {
		return "", apiErr("internal", "the window is not ready")
	}
	p, err := s.b.App.Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(true).PromptForSingleSelection()
	if err != nil {
		return "", apiErr("internal", err.Error())
	}
	return p, nil
}

// OpenDataFolder shows the data folder in the file manager.
func (s *SettingsService) OpenDataFolder(context.Context) error {
	return asAPIError(s.openDir(s.b.DataDir))
}

// OpenLogsFolder shows the logs folder in the file manager.
func (s *SettingsService) OpenLogsFolder(context.Context) error {
	dir := filepath.Join(s.b.DataDir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return asAPIError(err)
	}
	return asAPIError(s.openDir(dir))
}

func openInFileManager(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // explorer exits with status 1 even on success
	return nil
}

func logLevelOf(name string) slog.Level {
	switch name {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}
