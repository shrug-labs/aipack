package domain

import "strings"

// HasFrontmatterPrefix reports whether raw bytes begin with a YAML frontmatter
// marker. Unlike SplitFrontmatter, it does not require a closing delimiter.
func HasFrontmatterPrefix(b []byte) bool {
	return strings.HasPrefix(string(b), "---")
}

// SplitFrontmatter splits a markdown file into YAML frontmatter and body.
// Returns (nil, b, nil) if no frontmatter delimiter is found.
func SplitFrontmatter(b []byte) (frontmatter, body []byte, err error) {
	s := string(b)
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return nil, b, nil
	}

	// Determine opening delimiter length.
	openLen := 4 // "---\n"
	if strings.HasPrefix(s, "---\r\n") {
		openLen = 5
	}
	rest := s[openLen:]

	// Find the closing delimiter.
	idx := strings.Index(rest, "\n---\n")
	if idx >= 0 {
		return []byte(rest[:idx]), []byte(rest[idx+len("\n---\n"):]), nil
	}

	// Try \r\n variant.
	idx = strings.Index(rest, "\r\n---\r\n")
	if idx >= 0 {
		return []byte(rest[:idx]), []byte(rest[idx+len("\r\n---\r\n"):]), nil
	}

	return nil, b, nil
}
