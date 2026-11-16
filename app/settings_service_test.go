package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newSettingsFixture(t *testing.T) (*SettingsService, string, string) {
	t.Helper()
	dir, boot := t.TempDir(), t.TempDir()
	cfg, err := openSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := newSettingsService(&Backend{DataDir: dir, Settings: cfg})
	s.boot = boot
	return s, dir, boot
}

func TestSettingsDefaultsAndRoundTrip(t *testing.T) {
	s, dir, _ := newSettingsFixture(t)
	ctx := context.Background()
	got, _ := s.GetSettings(ctx)
	if got.Language != "vi" || got.Theme != "system" || got.Concurrency != 1 || got.ProxyTestURL != defaultProxyTestURL || got.LogLevel != "info" || got.DefaultEngine != "chrome" {
		t.Fatalf("defaults %+v", got)
	}
	in := GeneralSettings{Language: "en", Theme: "dark", DefaultDelayMs: 750, Concurrency: 4, ProxyTestURL: "https://ip.example/test", LogLevel: "debug"}
	if _, err := s.SaveSettings(ctx, in); err != nil {
		t.Fatal(err)
	}
	// A new process reads the same file.
	cfg2, err := openSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	got = generalOf(cfg2.Get())
	in.DefaultEngine = "chrome"
	if got != in {
		t.Fatalf("round trip\n got %+v\nwant %+v", got, in)
	}
	if appLogLevel.Level().String() != "DEBUG" {
		t.Errorf("log level not applied: %v", appLogLevel.Level())
	}
	if err := s.SetLocale(ctx, "vi"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTheme(ctx, "light"); err != nil {
		t.Fatal(err)
	}
	if g := s.b.Settings.Get(); g.Language != "vi" || g.Theme != "light" || g.Concurrency != 4 {
		t.Errorf("setters changed other fields: %+v", g)
	}
}

func TestSettingsRejectBadInput(t *testing.T) {
	s, _, _ := newSettingsFixture(t)
	ctx := context.Background()
	_, err := s.SaveSettings(ctx, GeneralSettings{Language: "fr", Theme: "pink", DefaultDelayMs: -1, Concurrency: 0, ProxyTestURL: "ftp://x", LogLevel: "loud"})
	ae, ok := err.(*APIError)
	if !ok || ae.Code != "invalid" {
		t.Fatalf("got %v", err)
	}
	if n := len(ae.Details["problems"].([]map[string]string)); n != 6 {
		t.Errorf("want 6 problems, got %d: %v", n, ae.Details)
	}
	if g, _ := s.GetSettings(ctx); g.Language != "vi" {
		t.Errorf("a rejected save changed the settings: %+v", g)
	}
	if err := s.SetLocale(ctx, "xx"); err == nil {
		t.Error("bad locale accepted")
	}
	// An empty test URL means the default.
	g, err := s.SaveSettings(ctx, GeneralSettings{Language: "vi", Theme: "system", Concurrency: 1, LogLevel: "info"})
	if err != nil || g.ProxyTestURL != defaultProxyTestURL {
		t.Errorf("empty url: %+v %v", g, err)
	}
}

func TestSettingsFileHandEdited(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, settingsFile), []byte(`{"language":"en","concurrency":9999,"theme":"neon","update":{"channel":"stable","feedUrl":"https://u.example/m.json"}}`), 0o600)
	cfg, err := openSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := cfg.Get()
	if g.Language != "en" || g.Concurrency != 1 || g.Theme != "system" || g.Update.FeedURL != "https://u.example/m.json" || g.Engines.KeepOld != 1 {
		t.Errorf("normalized %+v", g)
	}
	// A broken file is an error, not a silent reset.
	os.WriteFile(filepath.Join(dir, settingsFile), []byte(`{not json`), 0o600)
	if _, err := openSettings(dir); err == nil {
		t.Error("broken settings.json must fail")
	}
}

func TestSettingsDataFolder(t *testing.T) {
	s, dir, boot := newSettingsFixture(t)
	ctx := context.Background()
	info, _ := s.GetDataFolder(ctx)
	// The fixture runs from a folder that is not the default one, as after an
	// earlier change; with no pointer the next start uses the default folder.
	if info.Path != filepath.Clean(dir) || info.Pending != filepath.Clean(boot) || info.Default != filepath.Clean(boot) {
		t.Fatalf("info %+v", info)
	}
	if _, err := s.ChangeDataFolder(ctx, "relative/path"); !isCode(err, "invalid") {
		t.Errorf("relative path: %v", err)
	}
	target := filepath.Join(t.TempDir(), "ladilala data")
	os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o600)
	info, err := s.ChangeDataFolder(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Pending != target || info.Path != filepath.Clean(dir) {
		t.Errorf("after change %+v", info)
	}
	if got := readPointer(boot); got != target {
		t.Errorf("pointer %q", got)
	}
	// Nothing moved: the old folder still holds its files.
	if _, err := os.Stat(filepath.Join(dir, "marker")); err != nil {
		t.Errorf("old data should stay: %v", err)
	}
	info, _ = s.ChangeDataFolder(ctx, "")
	if info.Pending != filepath.Clean(boot) || readPointer(boot) != "" {
		t.Errorf("reset %+v", info)
	}
	os.WriteFile(filepath.Join(dir, "a.bin"), make([]byte, 1000), 0o600)
	if n, _ := s.DataFolderUsage(ctx); n < 1000 {
		t.Errorf("usage %d", n)
	}
	var opened string
	s.openDir = func(p string) error { opened = p; return nil }
	s.OpenDataFolder(ctx)
	if opened != dir {
		t.Errorf("opened %q", opened)
	}
	s.OpenLogsFolder(ctx)
	if opened != filepath.Join(dir, "logs") {
		t.Errorf("logs %q", opened)
	}
}

func TestUserDataDirPointer(t *testing.T) {
	boot := t.TempDir()
	if p := readPointer(boot); p != "" {
		t.Fatal(p)
	}
	target := filepath.Join(t.TempDir(), "d")
	if err := writePointer(boot, target); err != nil {
		t.Fatal(err)
	}
	if readPointer(boot) != target {
		t.Fatal("pointer lost")
	}
	os.WriteFile(filepath.Join(boot, pointerFile), []byte(`{"path":"relative"}`), 0o600)
	if readPointer(boot) != "" {
		t.Fatal("a relative pointer must be ignored")
	}
}
