// Package testutil provides shared test helpers.
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// Symlink creates a symbolic link from oldname to newname.
// On platforms where symlinks require elevated privileges (Windows without
// Developer Mode), the calling test is skipped instead of failing.
func Symlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}
}

// SkipWithoutSymlinks skips the calling test if symlinks are not available.
// Use this at the top of tests that create symlinks inside closures or
// helper functions where Symlink's t.Skip would be awkward.
func SkipWithoutSymlinks(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Skipf("symlink probe failed: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}
}
