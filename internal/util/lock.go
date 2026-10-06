package util

import (
	"context"
	"os"
)

type configLockKey struct{}

// LockConfig serializes mutations and releases automatically on process exit.
func LockConfig(configDir string) (func() error, error) {
	_, unlock, err := LockConfigContext(context.Background(), configDir)
	return unlock, err
}

// LockConfigContext also supplies the lock to native subprocesses. An orphaned
// installer retains ownership until it exits; recovery cannot race its writes.
func LockConfigContext(ctx context.Context, configDir string) (context.Context, func() error, error) {
	file, err := lockConfig(configDir)
	if err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, configLockKey{}, file), func() error { return unlockConfig(file) }, nil
}

func configLockFile(ctx context.Context) *os.File {
	file, _ := ctx.Value(configLockKey{}).(*os.File)
	return file
}
