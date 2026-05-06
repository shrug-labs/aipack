package common

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ClampOffset keeps cursor visible within a window of vis rows.
func ClampOffset(cursor, offset, vis int) int {
	if cursor < offset {
		return cursor
	}
	if cursor >= offset+vis {
		return cursor - vis + 1
	}
	return offset
}

// WriteScrollWindow writes the visible slice of lines and an optional overflow row.
func WriteScrollWindow(sb *strings.Builder, lines []string, offset, visibleH int, overflow func(remaining int) string) {
	start := min(offset, len(lines))
	end := min(start+visibleH, len(lines))
	for _, line := range lines[start:end] {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	if end < len(lines) && overflow != nil {
		sb.WriteString(overflow(len(lines) - end))
		sb.WriteString("\n")
	}
}

// TruncateLines truncates each line to fit a display width.
func TruncateLines(lines []string, width int) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, TruncateText(line, width))
	}
	return out
}

// TruncateText truncates a string to a display width and appends an ellipsis.
// Iterates with lipgloss.Width per candidate so wide chars and ANSI escape
// sequences are accounted for — naive rune-slicing under-counts wide chars
// and over-counts styled prefixes.
func TruncateText(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	const ellipsis = "…"
	if lipgloss.Width(ellipsis) > width {
		return ""
	}
	return ansi.Truncate(s, width, ellipsis)
}
