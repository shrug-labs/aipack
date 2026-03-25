//go:build windows

package cline

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// FOLDERID_Documents is the KNOWNFOLDERID for the user's Documents folder.
var folderIDDocuments = windows.KNOWNFOLDERID{
	Data1: 0xFDD39AD0,
	Data2: 0x238F,
	Data3: 0x46AF,
	Data4: [8]byte{0xAD, 0xB4, 0x6C, 0x85, 0x48, 0x03, 0x69, 0xC7},
}

// documentsDir returns the user's Documents folder.
// On Windows this queries the shell API, which respects OneDrive redirection.
// Falls back to home\Documents if the API call fails.
func documentsDir(home string) string {
	if p, err := windows.KnownFolderPath(&folderIDDocuments, 0); err == nil && p != "" {
		return p
	}
	return filepath.Join(home, "Documents")
}
