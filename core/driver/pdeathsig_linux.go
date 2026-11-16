package driver

import "syscall"

// setPdeathsig replaces the default that chromedp skips when ModifyCmdFunc is
// set.
func setPdeathsig(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
