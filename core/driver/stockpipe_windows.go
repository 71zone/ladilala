//go:build windows

package driver

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"syscall"
)

// setChildPipes passes the browser its two pipe handles. Windows has no fd 3
// and 4, so Chromium takes them from --remote-debugging-io-pipes=<read>,<write>
// (the handle it reads commands from, then the one it writes responses to; a
// handle keeps its value in the child). parents are the ends this process
// keeps: they are made non-inheritable so no other child can get them.
func setChildPipes(cmd *exec.Cmd, childR, childW *os.File, parents ...*os.File) error {
	for _, f := range parents {
		if err := syscall.SetHandleInformation(syscall.Handle(f.Fd()), syscall.HANDLE_FLAG_INHERIT, 0); err != nil {
			return fmt.Errorf("driver: make pipe end private: %w", err)
		}
	}
	hr, hw := syscall.Handle(childR.Fd()), syscall.Handle(childW.Fd())
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = new(syscall.SysProcAttr)
	}
	cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles, hr, hw)
	// The switch goes first so every parser reads it as a switch.
	cmd.Args = slices.Insert(cmd.Args, 1, "--remote-debugging-io-pipes="+strconv.FormatUint(uint64(hr), 10)+","+strconv.FormatUint(uint64(hw), 10))
	return nil
}
