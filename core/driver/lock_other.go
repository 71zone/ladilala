//go:build !windows

package driver

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// profileLockedOnDisk reads Chrome's SingletonLock symlink ("host-pid") and
// reports whether its owner is still alive. A dead owner is a stale lock that
// Chrome itself takes over.
func profileLockedOnDisk(dir string) bool {
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return false
	}
	i := strings.LastIndex(target, "-")
	if i < 0 {
		return true
	}
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil || pid <= 0 {
		return true
	}
	err = syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
