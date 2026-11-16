//go:build !windows

package driver

import (
	"os"
	"os/exec"
)

// setChildPipes hands the browser its ends of the CDP pipe as file
// descriptors 3 (commands in) and 4 (responses out), which is where
// --remote-debugging-pipe looks on Unix.
func setChildPipes(cmd *exec.Cmd, childR, childW *os.File, _ ...*os.File) error {
	cmd.ExtraFiles = []*os.File{childR, childW}
	return nil
}
