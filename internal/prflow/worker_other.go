//go:build !unix

package prflow

import "os/exec"

func configureWorkerProcess(_ *exec.Cmd) {}

func killWorkerProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
