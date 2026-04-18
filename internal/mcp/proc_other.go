//go:build !unix

package mcp

import "os/exec"

// setProcessGroup is a no-op on non-unix platforms. Proper grandchild
// cleanup on Windows requires JobObjects; that is backlog and tracked
// alongside the broader Windows support work.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup falls back to killing just the PID on non-unix
// platforms. Grandchildren may be orphaned on Windows until JobObject
// support lands; see the aipack Windows-support backlog.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
