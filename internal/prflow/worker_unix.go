//go:build aix || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris

package prflow

import (
	"os/exec"
	"syscall"
)

func configureWorkerProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killWorkerProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// Kill the process group first so shell wrappers cannot leave a compiler or
	// package driver behind after the parent deadline fires.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
