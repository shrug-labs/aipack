package common

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Diff styles. Populated by tui/styles.go alongside the other semantic styles
// so the colors stay consistent across the save and sync screens.
var (
	DiffAddStyle    lipgloss.Style
	DiffRemoveStyle lipgloss.Style
	DiffHunkStyle   lipgloss.Style
)

// RenderUnifiedDiff writes a colorized unified diff (or a "+ "-prefixed full
// body for new files) into sb. Header lines, the rule, the diff body, and the
// "(no changes)" placeholder are emitted in this order. Caller controls the
// rule character — save uses '-' for the legacy ASCII look, planview uses '─'.
func RenderUnifiedDiff(sb *strings.Builder, title, dst, diffText string, isNew bool, newBody string, maxRuleWidth int, ruleChar string) {
	RenderUnifiedDiffWithNote(sb, title, dst, diffText, isNew, newBody, "", maxRuleWidth, ruleChar)
}

func RenderUnifiedDiffWithNote(sb *strings.Builder, title, dst, diffText string, isNew bool, newBody, note string, maxRuleWidth int, ruleChar string) {
	if isNew {
		sb.WriteString(SelectedStyle.Render("New file: " + title))
	} else {
		sb.WriteString(SelectedStyle.Render("Diff: " + title))
	}
	sb.WriteString("\n")
	sb.WriteString(DimStyle.Render(dst))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat(ruleChar, min(maxRuleWidth, 80)))
	sb.WriteString("\n\n")

	if note != "" {
		sb.WriteString(DimStyle.Render(note))
		sb.WriteString("\n\n")
	}

	switch {
	case isNew:
		for line := range strings.SplitSeq(newBody, "\n") {
			sb.WriteString(DiffAddStyle.Render("+ " + line))
			sb.WriteString("\n")
		}
	case diffText == "":
		sb.WriteString(DimStyle.Render("(no changes — content is identical)"))
	default:
		for line := range strings.SplitSeq(diffText, "\n") {
			if line == "" {
				sb.WriteString("\n")
				continue
			}
			switch {
			case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
				sb.WriteString(DimStyle.Bold(true).Render(line))
			case strings.HasPrefix(line, "@@"):
				sb.WriteString(DiffHunkStyle.Render(line))
			case strings.HasPrefix(line, "+"):
				sb.WriteString(DiffAddStyle.Render(line))
			case strings.HasPrefix(line, "-"):
				sb.WriteString(DiffRemoveStyle.Render(line))
			default:
				sb.WriteString(line)
			}
			sb.WriteString("\n")
		}
	}
}
