//go:build !windows

package cline

import "path/filepath"

// documentsDir returns the user's Documents folder.
// On non-Windows platforms this is simply home/Documents.
func documentsDir(home string) string {
	return filepath.Join(home, "Documents")
}
