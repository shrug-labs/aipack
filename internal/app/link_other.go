//go:build !windows

package app

import "os"

// createLink creates a symbolic link from target to linkPath.
// On non-Windows platforms this is a plain symlink.
func createLink(target, linkPath string) error {
	return os.Symlink(target, linkPath)
}
