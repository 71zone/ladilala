package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// defaultUserDataDirs lists where Chrome, Chromium and Edge keep their
// default profile on goos. Chrome 136+ ignores debugging switches there, and
// automating it would also touch the user's real browsing data.
func defaultUserDataDirs(goos, home, localAppData, xdgConfig string) []string {
	j := filepath.Join
	switch goos {
	case "windows":
		if localAppData == "" {
			return nil
		}
		var out []string
		for _, p := range [][]string{
			{"Google", "Chrome"}, {"Google", "Chrome Beta"}, {"Google", "Chrome Dev"}, {"Google", "Chrome SxS"},
			{"Chromium"}, {"Microsoft", "Edge"}, {"Microsoft", "Edge Beta"}, {"Microsoft", "Edge Dev"}, {"Microsoft", "Edge SxS"},
		} {
			out = append(out, j(append([]string{localAppData}, append(p, "User Data")...)...))
		}
		return out
	case "darwin":
		base := j(home, "Library", "Application Support")
		return []string{j(base, "Google", "Chrome"), j(base, "Chromium"), j(base, "Microsoft Edge")}
	default:
		cfgs := []string{j(home, ".config")}
		if xdgConfig != "" {
			cfgs = append(cfgs, xdgConfig)
		}
		var out []string
		for _, c := range cfgs {
			out = append(out, j(c, "google-chrome"), j(c, "google-chrome-beta"), j(c, "google-chrome-unstable"),
				j(c, "chromium"), j(c, "microsoft-edge"))
		}
		return append(out, j(home, "snap", "chromium", "common", "chromium"))
	}
}

// validateDataDir rejects an empty, relative, or default Chrome data dir.
func validateDataDir(dir string) error {
	home, _ := os.UserHomeDir()
	defaults := defaultUserDataDirs(runtime.GOOS, home, os.Getenv("LOCALAPPDATA"), os.Getenv("XDG_CONFIG_HOME"))
	return checkDataDir(dir, defaults, caseInsensitiveFS(runtime.GOOS))
}

func checkDataDir(dir string, defaults []string, foldCase bool) error {
	if dir == "" {
		return fmt.Errorf("%w: empty", ErrInvalidDataDir)
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%w: %q is not absolute", ErrInvalidDataDir, dir)
	}
	for _, d := range pathForms(dir, foldCase) {
		for _, def := range defaults {
			for _, df := range pathForms(def, foldCase) {
				if within(df, d) {
					return fmt.Errorf("%w: %q is inside the default browser directory %q", ErrInvalidDataDir, dir, def)
				}
			}
		}
	}
	return nil
}

// pathForms returns the cleaned path and, when it resolves, its symlink-free
// form, so a link into a default directory cannot slip through.
func pathForms(p string, foldCase bool) []string {
	forms := []string{filepath.Clean(p)}
	if r, err := filepath.EvalSymlinks(p); err == nil && r != forms[0] {
		forms = append(forms, r)
	}
	if foldCase {
		for i := range forms {
			forms[i] = strings.ToLower(forms[i])
		}
	}
	return forms
}

// within reports whether path equals root or lies below it.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
