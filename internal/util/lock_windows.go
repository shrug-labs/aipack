package util

import (
	"context"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
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
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		file.Close()
		return nil, fmt.Errorf("another AIPack mutation is running: %w", err)
	}
	return file, nil
}

func unlockConfig(file *os.File) error { return file.Close() }

func InheritConfigLock(ctx context.Context, cmd *exec.Cmd) error {
	if configLockFile(ctx) != nil {
		return fmt.Errorf("recoverable native delivery requires inherited config locks; unsupported on Windows")
	}
	return nil
}
