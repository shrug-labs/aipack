//go:build !unix && !windows

package source

import (
	"os"
	"os/exec"
)

func prepareGitProcess(cmd *exec.Cmd) (func(), error)   { return func() {}, nil }
func gitProcessInterrupted(state *os.ProcessState) bool { return false }
