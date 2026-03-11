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
	opWriteStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("114")) // green
	opCopyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))  // blue
	opSettingsStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // yellow
	opPluginStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("141")) // purple
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
