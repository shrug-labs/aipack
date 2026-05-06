package packs

import "charm.land/lipgloss/v2"

var (
	clrLoading = lipgloss.Color("226")

	successStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	panelHeaderStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	panelSubtleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	categoryHeaderStyle = panelHeaderStyle
	contentSummaryStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	previewKeyStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
)
