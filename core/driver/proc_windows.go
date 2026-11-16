package driver

import "os/exec"

// configureCmd does nothing: the Job object assigned after start ties the
// browser's lifetime to the app.
func configureCmd(*exec.Cmd) {}

// killTree does nothing: the Job object owns the process tree.
func killTree(int) {}
