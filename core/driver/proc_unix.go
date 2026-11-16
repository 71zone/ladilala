//go:build !windows

package driver

import (
	"os/exec"
	"syscall"
)

// configureCmd puts the browser in its own process group so killTree can end
// the helper processes with it, and (on Linux) asks the kernel to kill it if
// the app dies.
func configureCmd(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = new(syscall.SysProcAttr)
	}
	cmd.SysProcAttr.Setpgid = true
	setPdeathsig(cmd.SysProcAttr)
}

// killTree kills the browser's process group. Errors are ignored: the group
// is usually already gone after a graceful close.
func killTree(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
