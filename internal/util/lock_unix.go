//go:build !windows

package util

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// ponytail: one config lock; finer locks only if mutation throughput needs it.
func lockConfig(configDir string) (*os.File, error) {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(configDir, ".mutation.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another AIPack mutation is running: %w", err)
	}
	return file, nil
}

// InheritConfigLock protects a CommandContext through process loss and cancels
// its process group before the caller releases the mutation lock.
func InheritConfigLock(ctx context.Context, cmd *exec.Cmd) error {
	if file := configLockFile(ctx); file != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, file)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			err := unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
			if errors.Is(err, unix.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
	}
	return nil
}

func unlockConfig(file *os.File) error {
	// A concurrent fork can briefly retain the descriptor before exec.
	// Unlock explicitly so those copies cannot prolong our mutation lock.
	return errors.Join(unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close())
}
