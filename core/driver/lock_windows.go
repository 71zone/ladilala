package driver

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// profileLockedOnDisk probes Chrome's "lockfile". A running Chrome holds it
// open exclusively, so opening it fails with a sharing violation.
func profileLockedOnDisk(dir string) bool {
	f, err := os.OpenFile(filepath.Join(dir, "lockfile"), os.O_RDWR, 0)
	if err == nil {
		f.Close()
		return false
	}
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
