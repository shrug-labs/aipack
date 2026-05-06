package common

import "charm.land/lipgloss/v2"

// Semantic styles. These are populated once at startup from tui/styles.go's
// initStyles() — which selects color tokens based on terminal background. Do
// not initialize them here; the tui package owns adaptive selection. Reading
// before init yields the lipgloss zero value (no styling), which is harmless
// during tests that don't go through tui.init.
var (
	DimStyle      lipgloss.Style
	SelectedStyle lipgloss.Style
	ErrorStyle    lipgloss.Style
	WarningStyle  lipgloss.Style
	ContentStyle  lipgloss.Style

	StatusDotActive   string
	StatusDotInactive string
	StatusDotLoading  string
)
