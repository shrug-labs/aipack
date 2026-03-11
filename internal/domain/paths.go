package domain

import (
	"path/filepath"
	"strings"
)

// IsUnderAny reports whether path is under any of the given prefixes.
func IsUnderAny(path string, prefixes []string) bool {
	p := filepath.Clean(path)
	for _, pre := range prefixes {
		root := filepath.Clean(pre)
		rel, err := filepath.Rel(root, p)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return true
		}
		if !strings.HasPrefix(rel, "../") && rel != ".." {
			return true
		}
	}
	return false
}

// IsUnder reports whether path is under parent.
func IsUnder(path string, parent string) bool {
	return IsUnderAny(path, []string{parent})
}
