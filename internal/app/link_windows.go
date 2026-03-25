//go:build windows

package app

import (
	"os"
	"os/exec"
)

// createLink creates a symbolic link from target to linkPath.
// On Windows, os.Symlink requires Developer Mode or admin privileges.
// When it fails, we fall back to a directory junction (mklink /J) which
// works without elevated privileges and is transparent to Go's filepath
// operations.
func createLink(target, linkPath string) error {
	if err := os.Symlink(target, linkPath); err == nil {
		return nil
	}
	// Fallback: directory junction via mklink /J.
	// Build the command string with quoted paths to handle spaces and
	// special characters safely. exec.Command on Windows joins args
	// after /C into a single string for cmd.exe.
	cmd := `mklink /J "` + linkPath + `" "` + target + `"`
	return exec.Command("cmd", "/C", cmd).Run()
}
