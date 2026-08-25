//go:build js || plan9 || wasip1

package ext

import "os/exec"

func configureCommandProcess(cmd *exec.Cmd) {}
