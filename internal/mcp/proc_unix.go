//go:build unix

package mcp

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the MCP server subprocess in its own process group so
// we can signal every descendant on cleanup. MCP servers frequently spawn
// worker subprocesses (node pools, Python multiprocessing); killing only
// the direct child leaks grandchildren as orphans when the parent times
// out or crashes mid-probe.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup sends SIGKILL to the entire process group created by
// setProcessGroup. Falls back to killing the PID alone if the process is
// not (or no longer) the leader of a group — that case is unlikely but
// harmless.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		// Fall back to per-PID kill if the group signal fails (e.g. the
		// child detached from our group or already exited).
		_ = cmd.Process.Kill()
	}
}
