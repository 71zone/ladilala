package driver

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const hostOS = runtime.GOOS

// binaryNames are the executable file names a Chromium build ships on goos.
func binaryNames(goos string) []string {
	switch goos {
	case "windows":
		return []string{"chrome.exe"}
	case "darwin":
		return []string{"Chromium", "chrome"}
	default:
		return []string{"chrome", "chromium"}
	}
}

// scanDir finds files named like names under root, at most maxDepth levels
// down. A release zip unpacks into one versioned subfolder, so a shallow walk
// finds the binary wherever the user extracted it. A missing root is not an
// error: the engine simply is not installed yet.
func scanDir(root string, names []string, maxDepth int) ([]string, error) {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	}
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		depth := 0
		if rel != "." {
			depth = strings.Count(rel, string(filepath.Separator)) + 1
		}
		if d.IsDir() {
			if depth >= maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		for _, n := range names {
			if d.Name() == n && d.Type().IsRegular() {
				found = append(found, path)
			}
		}
		return nil
	})
	return found, err
}
