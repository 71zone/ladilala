package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// pointerFile sits in the default folder and holds the data folder the user
// chose in Settings. The app reads it at start, before anything else opens.
const pointerFile = "datafolder.json"

// bootstrapDir is %LOCALAPPDATA%\Ladilala on Windows. It never moves: it holds
// the pointer file and, when the user chose no other folder, all the data.
// The uninstaller never removes it, so profiles, window state and the WebView2
// cache survive a reinstall.
func bootstrapDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "Ladilala")
	return dir, os.MkdirAll(dir, 0o700)
}

// userDataDir returns the data folder: LADILALA_DATA when set, else the folder
// in the pointer file when it is usable, else the bootstrap folder.
func userDataDir() (string, error) {
	if d := os.Getenv("LADILALA_DATA"); d != "" {
		return d, os.MkdirAll(d, 0o700)
	}
	boot, err := bootstrapDir()
	if err != nil {
		return "", err
	}
	if p := readPointer(boot); p != "" {
		if err := os.MkdirAll(p, 0o700); err == nil {
			return p, nil
		}
	}
	return boot, nil
}

func readPointer(boot string) string {
	b, err := os.ReadFile(filepath.Join(boot, pointerFile))
	if err != nil {
		return ""
	}
	var v struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(b, &v) != nil || !filepath.IsAbs(v.Path) {
		return ""
	}
	return v.Path
}

func writePointer(boot, path string) error {
	file := filepath.Join(boot, pointerFile)
	if path == "" {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	b, _ := json.Marshal(struct {
		Path string `json:"path"`
	}{path})
	return os.WriteFile(file, b, 0o600)
}
