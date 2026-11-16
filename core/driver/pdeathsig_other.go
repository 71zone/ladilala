//go:build !linux && !windows

package driver

import "syscall"

// setPdeathsig does nothing: only Linux has a parent-death signal.
func setPdeathsig(*syscall.SysProcAttr) {}
