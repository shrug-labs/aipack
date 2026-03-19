package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// ac builds an AdaptiveColor for chromatic tokens that work on all backgrounds.
func ac(light, dark string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: light, Dark: dark}
}

// --- Background detection ---
//
// Terminal backgrounds fall into four categories based on HSL lightness.
// Pure dark/light terminals use xterm-256 grays. Gray terminals use true-color
// tinted grays that provide WCAG 3:1+ contrast through luminance and hue
// separation against neutral gray backgrounds.
//
// The tint hues are chosen for perceptual effect: cool blue-gray (210°) recedes
// on dark backgrounds; warm gray (30°) grounds text on light backgrounds. Both
// use 12% saturation — noticeable enough to aid contrast, subtle enough to read
// as "gray" rather than "colored."

type bgCategory int

const (
	bgDark      bgCategory = iota // HSL lightness < 0.25
	bgGrayDark                    // HSL lightness 0.25–0.50
	bgGrayLight                   // HSL lightness 0.50–0.75
	bgLight                       // HSL lightness ≥ 0.75
)

func detectBackground() bgCategory {
	o := termenv.NewOutput(os.Stdout)
	c := termenv.ConvertToRGB(o.BackgroundColor())
	_, _, l := c.Hsl()
	switch {
	case l < 0.25:
		return bgDark
	case l < 0.50:
		return bgGrayDark
	case l < 0.75:
		return bgGrayLight
	default:
		return bgLight
	}
}

// --- Achromatic color tokens (set by initColors based on background) ---

var (
	clrDim     lipgloss.TerminalColor
	clrHelpBar lipgloss.TerminalColor
	clrSubtle  lipgloss.TerminalColor
	clrHeader  lipgloss.TerminalColor
	clrSummary lipgloss.TerminalColor
)

func initColors(cat bgCategory) {
	switch cat {
	case bgDark:
		// xterm-256 grays — good contrast against black/very dark backgrounds.
		clrDim = lipgloss.Color("240")
		clrHelpBar = lipgloss.Color("241")
		clrSubtle = lipgloss.Color("244")
		clrSummary = lipgloss.Color("246")
		clrHeader = lipgloss.Color("252")

	case bgGrayDark:
		// Cool blue-gray tints (hue 210°, sat 12%) — lighter text on dark gray.
		// WCAG 3:1+ against xterm 238–243 backgrounds.
		clrDim = lipgloss.Color("#cdd3d8")     // L≈83%, CR≥3.0
		clrHelpBar = lipgloss.Color("#cdd3d8") // same tier as dim
		clrSubtle = lipgloss.Color("#d6dadf")  // L≈86%, CR≥3.2
		clrSummary = lipgloss.Color("#dfe3e8") // L≈89%, CR≥3.5
		clrHeader = lipgloss.Color("#fefefe")  // L≈99%, CR≥4.5

	case bgGrayLight:
		// Warm gray tints (hue 30°, sat 12%) — darker text on light gray.
		// WCAG 3:1+ against xterm 244–249 backgrounds.
		clrDim = lipgloss.Color("#3d3630")     // L≈21%, CR≥3.0
		clrHelpBar = lipgloss.Color("#3d3630") // same tier as dim
		clrSubtle = lipgloss.Color("#362f2a")  // L≈19%, CR≥3.2
		clrSummary = lipgloss.Color("#302a24") // L≈17%, CR≥3.5
		clrHeader = lipgloss.Color("#1a1714")  // L≈9%, CR≥4.5

	case bgLight:
		// xterm-256 grays — good contrast against white/very light backgrounds.
		clrDim = lipgloss.Color("245")
		clrHelpBar = lipgloss.Color("244")
		clrSubtle = lipgloss.Color("242")
		clrSummary = lipgloss.Color("240")
		clrHeader = lipgloss.Color("235")
	}
}

// --- Chromatic color tokens (adaptive, work on all backgrounds) ---

var (
	clrAccent  = ac("125", "205") // magenta — tabs, selection, borders, dialogs
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

// --- Static styles (chromatic colors only, safe as var declarations) ---

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

	contentStyle = lipgloss.NewStyle().
			Padding(1, 2)

	statusDotActive   = lipgloss.NewStyle().Foreground(clrSuccess).Render("●")
	statusDotInactive = lipgloss.NewStyle().Foreground(clrError).Render("○")
	statusDotLoading  = lipgloss.NewStyle().Foreground(clrLoading).Render("⟳")

	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(clrAccent)

	dialogBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(clrAccent).
				Padding(1, 2)

	dialogTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(clrAccent)

	treeCheckOn   = lipgloss.NewStyle().Foreground(clrSuccess).Render("[x]")
	treeExpanded  = "▼"
	treeCollapsed = "▶"

	errorStyle   = lipgloss.NewStyle().Foreground(clrError)
	warningStyle = lipgloss.NewStyle().Foreground(clrWarning)

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

// --- Dynamic styles (depend on background-detected achromatic tokens) ---

var (
	inactiveTabStyle    lipgloss.Style
	tabGapStyle         lipgloss.Style
	dimStyle            lipgloss.Style
	helpBarStyle        lipgloss.Style
	treeCheckOff        string
	fileSizeStyle       lipgloss.Style
	panelHeaderStyle    lipgloss.Style
	panelSubtleStyle    lipgloss.Style
	categoryHeaderStyle lipgloss.Style
	contentSummaryStyle lipgloss.Style
)

func initStyles() {
	inactiveTabStyle = lipgloss.NewStyle().
		Foreground(clrDim).
		Border(tabBorderInactive).
		BorderForeground(clrDim).
		Padding(0, 2)

	tabGapStyle = lipgloss.NewStyle().
		Border(lipgloss.Border{Bottom: "─"}, false, false, true, false).
		BorderForeground(clrDim)

	dimStyle = lipgloss.NewStyle().Foreground(clrDim)
	helpBarStyle = lipgloss.NewStyle().Foreground(clrHelpBar)
	treeCheckOff = lipgloss.NewStyle().Foreground(clrDim).Render("[ ]")
	fileSizeStyle = lipgloss.NewStyle().Foreground(clrDim)
	panelHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(clrHeader)
	panelSubtleStyle = lipgloss.NewStyle().Foreground(clrSubtle)
	categoryHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(clrHeader)
	contentSummaryStyle = lipgloss.NewStyle().Foreground(clrSummary)
}

func init() {
	cat := detectBackground()
	initColors(cat)
	initStyles()
}

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
