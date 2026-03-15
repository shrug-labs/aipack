package tui

import "github.com/charmbracelet/lipgloss"

var (
	tabBorder = lipgloss.Border{
		Top:         "─",
		Bottom:      " ",
		Left:        "│",
		Right:       "│",
		TopLeft:     "╭",
		TopRight:    "╮",
		BottomLeft:  "┘",
		BottomRight: "└",
	}

	tabBorderInactive = lipgloss.Border{
		Top:         "─",
		Bottom:      "─",
		Left:        "│",
		Right:       "│",
		TopLeft:     "╭",
		TopRight:    "╮",
		BottomLeft:  "┴",
		BottomRight: "┴",
	}

	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			Border(tabBorder).
			BorderForeground(lipgloss.Color("205")).
			Padding(0, 2)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240")).
				Border(tabBorderInactive).
				BorderForeground(lipgloss.Color("240")).
				Padding(0, 2)

	tabGapStyle = lipgloss.NewStyle().
			Border(lipgloss.Border{Bottom: "─"}, false, false, true, false).
			BorderForeground(lipgloss.Color("240"))

	contentStyle = lipgloss.NewStyle().
			Padding(1, 2)

	statusDotActive   = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("●")
	statusDotInactive = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("○")
	statusDotLoading  = lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Render("⟳")

	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	helpBarStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	dialogBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("205")).
				Padding(1, 2)

	dialogTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))

	treeCheckOn  = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("[x]")
	treeCheckOff = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("[ ]")

	treeExpanded  = "▼"
	treeCollapsed = "▶"

	fileSizeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	warningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

	panelHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1)

	panelFocusedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("205")).
				Padding(0, 1)

	panelMutedHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("250"))

	panelSubtleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	badgeInstalledStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("46"))

	badgeAvailableStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("81"))

	badgeDetachedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("214"))

	badgeMetaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))

	listMetaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	categoryHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("252"))

	contentSummaryStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))

	previewBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("205")).
				Padding(0, 1)

	previewTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("205"))

	previewKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("75"))

	// Operation type styles for the sync plan view.
	opRuleStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("114")) // green
	opWorkflowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))  // blue
	opAgentStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("141")) // purple
	opSkillStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("79"))  // teal
	opSettingsStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // yellow
	opMCPStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))  // cyan
	opPruneStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203")) // red

	// Diff output styles for the plan diff viewer.
	diffAddStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("114")) // green
	diffRemoveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // red
	diffHunkStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))  // cyan

	// Pack color palette — bright for left panel, muted for right panel attribution.
	packColorsBright = []lipgloss.Color{"75", "114", "214", "141", "80", "204", "220", "109"}
	packColorsMuted  = []lipgloss.Color{"67", "65", "172", "97", "37", "131", "136", "66"}
)

// packColorBright returns a style for a pack at the given profile index.
func packColorBright(idx int) lipgloss.Style {
	c := packColorsBright[idx%len(packColorsBright)]
	return lipgloss.NewStyle().Foreground(c)
}

// packColorMuted returns a muted style for pack attribution in the tree.
func packColorMuted(idx int) lipgloss.Style {
	c := packColorsMuted[idx%len(packColorsMuted)]
	return lipgloss.NewStyle().Foreground(c).Italic(true)
}
