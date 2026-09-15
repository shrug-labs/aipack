//go:build unix

package source

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// A separate process group lets cancellation stop Git and its SSH/credential
// children. When stdin is the controlling terminal, give that group foreground
// ownership so native /dev/tty prompts and Ctrl-C work, then restore ownership.
func prepareGitProcess(cmd *exec.Cmd) (func(), error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	restore := func() {}
	if terminal, ok := cmd.Stdin.(*os.File); ok {
		fd := int(terminal.Fd())
		if pgrp, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP); err == nil {
			cmd.SysProcAttr.Foreground = true
			cmd.SysProcAttr.Ctty = fd
			restore = func() {
				// The parent is briefly in the background while reclaiming its
				// terminal. SIGTTOU would otherwise stop it during this ioctl.
				ignored := signal.Ignored(syscall.SIGTTOU)
				signal.Ignore(syscall.SIGTTOU)
				_ = unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, pgrp)
				if !ignored {
					signal.Reset(syscall.SIGTTOU)
				}
			}
		}
	}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if err == syscall.ESRCH {
				return os.ErrProcessDone
			}
			return fmt.Errorf("stopping Git process group: %w", err)
		}
		return nil
	}
	return restore, nil
}

func gitProcessInterrupted(state *os.ProcessState) bool {
	if state == nil {
		return false
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && (status.Signal() == syscall.SIGINT || status.Signal() == syscall.SIGTERM)
}
