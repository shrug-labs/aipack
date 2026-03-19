package tui

import "github.com/charmbracelet/lipgloss"

// ac builds an AdaptiveColor that auto-selects based on terminal background.
// Light value is used on light/white backgrounds, dark on dark backgrounds.
func ac(light, dark string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: light, Dark: dark}
}

// --- Adaptive color tokens ---
//
// Each token defines a light-bg and dark-bg variant. lipgloss detects the
// terminal background automatically (via termenv OSC 11) and picks the right
// one at render time. Users can override detection by setting COLORFGBG.
var (
	clrAccent  = ac("125", "205") // magenta — tabs, selection, borders, dialogs
	clrDim     = ac("245", "240") // dim/inactive text
	clrHelpBar = ac("244", "241") // help bar
	clrSubtle  = ac("242", "244") // subtle/panel text
	clrHeader  = ac("235", "252") // prominent headers
	clrSummary = ac("240", "246") // content summaries

	clrSuccess = ac("28", "46")   // green — active dots, checks
	clrError   = ac("160", "196") // red — errors, inactive dots
	clrWarning = ac("166", "214") // orange — warnings
	clrLoading = ac("130", "226") // yellow — loading indicator

	clrBlue   = ac("26", "75")   // workflows, preview keys, diff hunks
	clrGreen  = ac("28", "114")  // rules, diff additions
	clrPurple = ac("91", "141")  // agents
	clrTeal   = ac("30", "79")   // skills
	clrCyan   = ac("31", "81")   // MCP servers
	clrStale  = ac("124", "203") // stale items
)

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
			Foreground(clrAccent).
			Border(tabBorder).
			BorderForeground(clrAccent).
			Padding(0, 2)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(clrDim).
				Border(tabBorderInactive).
				BorderForeground(clrDim).
				Padding(0, 2)

	tabGapStyle = lipgloss.NewStyle().
			Border(lipgloss.Border{Bottom: "─"}, false, false, true, false).
			BorderForeground(clrDim)

	contentStyle = lipgloss.NewStyle().
			Padding(1, 2)

	statusDotActive   = lipgloss.NewStyle().Foreground(clrSuccess).Render("●")
	statusDotInactive = lipgloss.NewStyle().Foreground(clrError).Render("○")
	statusDotLoading  = lipgloss.NewStyle().Foreground(clrLoading).Render("⟳")

	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(clrAccent)
	dimStyle      = lipgloss.NewStyle().Foreground(clrDim)

	helpBarStyle = lipgloss.NewStyle().Foreground(clrHelpBar)

	dialogBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(clrAccent).
				Padding(1, 2)

	dialogTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(clrAccent)

	treeCheckOn  = lipgloss.NewStyle().Foreground(clrSuccess).Render("[x]")
	treeCheckOff = lipgloss.NewStyle().Foreground(clrDim).Render("[ ]")

	treeExpanded  = "▼"
	treeCollapsed = "▶"

	fileSizeStyle = lipgloss.NewStyle().Foreground(clrDim)

	errorStyle   = lipgloss.NewStyle().Foreground(clrError)
	warningStyle = lipgloss.NewStyle().Foreground(clrWarning)

	panelHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(clrHeader)

	panelSubtleStyle = lipgloss.NewStyle().Foreground(clrSubtle)

	categoryHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(clrHeader)

	contentSummaryStyle = lipgloss.NewStyle().Foreground(clrSummary)

	previewBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(clrAccent).
				Padding(0, 1)

	previewTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(clrAccent)

	previewKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(clrBlue)

	// Operation type styles for the sync plan view.
	opRuleStyle     = lipgloss.NewStyle().Foreground(clrGreen)
	opWorkflowStyle = lipgloss.NewStyle().Foreground(clrBlue)
	opAgentStyle    = lipgloss.NewStyle().Foreground(clrPurple)
	opSkillStyle    = lipgloss.NewStyle().Foreground(clrTeal)
	opSettingsStyle = lipgloss.NewStyle().Foreground(clrWarning)
	opMCPStyle      = lipgloss.NewStyle().Foreground(clrCyan)
	opStaleStyle    = lipgloss.NewStyle().Foreground(clrStale)

	// Diff output styles for the plan diff viewer.
	diffAddStyle    = lipgloss.NewStyle().Foreground(clrGreen)
	diffRemoveStyle = lipgloss.NewStyle().Foreground(clrError)
	diffHunkStyle   = lipgloss.NewStyle().Foreground(clrBlue)

	// Pack color palette — bright for left panel, muted for right panel attribution.
	// Each entry is an AdaptiveColor: darker on light backgrounds, vivid on dark.
	packColorsBright = []lipgloss.TerminalColor{
		ac("26", "75"),   // blue
		ac("28", "114"),  // green
		ac("166", "214"), // orange
		ac("91", "141"),  // purple
		ac("30", "80"),   // cyan
		ac("162", "204"), // pink
		ac("172", "220"), // gold
		ac("66", "109"),  // teal
	}
	packColorsMuted = []lipgloss.TerminalColor{
		ac("24", "67"),   // blue-gray
		ac("22", "65"),   // olive
		ac("130", "172"), // dark orange
		ac("54", "97"),   // dark purple
		ac("23", "37"),   // dark cyan
		ac("88", "131"),  // dark red
		ac("94", "136"),  // dark yellow
		ac("30", "66"),   // dark teal
	}
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
