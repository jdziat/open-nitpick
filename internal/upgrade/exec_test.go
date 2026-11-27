package upgrade

import "os/exec"

func newCommand(path string) *exec.Cmd { return exec.Command(path) }
