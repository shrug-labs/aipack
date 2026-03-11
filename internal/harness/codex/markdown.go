package codex

import (
	"strings"
)

// BuildManagedContent generates the managed markdown structure used by Codex.
// Contains only rules — agents and workflows are promoted to skills via promote.go.
func BuildManagedContent(rulesHeading, flattenedRules string) string {
	var buf strings.Builder
	buf.WriteString("<!-- aipack managed; DO NOT EDIT by hand -->\n")
	if strings.TrimSpace(flattenedRules) != "" {
		buf.WriteString(rulesHeading + "\n\n")
		buf.WriteString(strings.TrimRight(flattenedRules, "\n"))
		buf.WriteString("\n")
	}
	return buf.String()
}
