package profiles

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Profile-screen specific styles. Adaptive styles (Dim/Selected/Error/Warning/
// Content) live in cmd/aipack/tui/common — reference common.* directly. The
// profile-local statusDot* and tree* glyphs intentionally differ from the
// shell's status dots; they read better in dense profile listings.
var (
	panelHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))

	statusDotActive   = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("*")
	statusDotInactive = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("o")
	statusDotLoading  = lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Render("~")

	treeCheckOn        = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("[x]")
	treeCheckOff       = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("[ ]")
	treeOverrideWinner = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("o")
	treeExpanded       = "v"
	treeCollapsed      = ">"
)

var (
	packColorsBright = []color.Color{
		lipgloss.Color("75"),
		lipgloss.Color("114"),
		lipgloss.Color("214"),
		lipgloss.Color("141"),
		lipgloss.Color("80"),
		lipgloss.Color("39"),
		lipgloss.Color("149"),
		lipgloss.Color("208"),
		lipgloss.Color("111"),
		lipgloss.Color("203"),
		lipgloss.Color("177"),
		lipgloss.Color("120"),
	}
)

func packColorBright(idx int) lipgloss.Style {
	if len(packColorsBright) == 0 {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(packColorsBright[idx%len(packColorsBright)])
}
