package driver

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckDataDir(t *testing.T) {
	root := t.TempDir()
	def := filepath.Join(root, "Google", "Chrome", "User Data")
	if err := os.MkdirAll(def, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(def, link); err != nil {
		t.Fatal(err)
	}
	defaults := []string{def}
	for name, dir := range map[string]string{
		"empty":    "",
		"relative": "profiles/a",
		"default":  def,
		"nested":   filepath.Join(def, "Profile 1"),
		"symlink":  link,
		"dotdot":   filepath.Join(root, "x", "..", "Google", "Chrome", "User Data"),
	} {
		if err := checkDataDir(dir, defaults, false); !errors.Is(err, ErrInvalidDataDir) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if err := checkDataDir(filepath.Join(root, "Google", "Chrome", "User Data 2"), defaults, false); err != nil {
		t.Errorf("sibling dir rejected: %v", err)
	}
	if err := checkDataDir(filepath.Join(root, "profiles", "a"), defaults, false); err != nil {
		t.Errorf("ordinary dir rejected: %v", err)
	}
	if err := checkDataDir(filepath.Join(root, "GOOGLE", "chrome", "user data"), defaults, true); !errors.Is(err, ErrInvalidDataDir) {
		t.Errorf("case-insensitive match missed: %v", err)
	}
}

func TestDefaultUserDataDirs(t *testing.T) {
	if d := defaultUserDataDirs("windows", "", `C:\Users\a\AppData\Local`, ""); len(d) == 0 {
		t.Fatal("no windows defaults")
	}
	got := defaultUserDataDirs("darwin", "/Users/a", "", "")
	if filepath.ToSlash(got[0]) != "/Users/a/Library/Application Support/Google/Chrome" {
		t.Fatalf("darwin default: %v", got)
	}
	if len(defaultUserDataDirs("linux", "/home/a", "", "/cfg")) < 6 {
		t.Fatal("linux defaults missing XDG_CONFIG_HOME")
	}
}
