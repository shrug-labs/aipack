package tui

import (
	"image/color"
	"os"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/muesli/termenv"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
)

// ac builds an adaptive color for chromatic tokens that work on all backgrounds.
func ac(light, dark string) compat.AdaptiveColor {
	return compat.AdaptiveColor{Light: lipgloss.Color(light), Dark: lipgloss.Color(dark)}
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
	clrDim     color.Color
	clrHelpBar color.Color
	clrSubtle  color.Color
	clrHeader  color.Color
	clrSummary color.Color
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

	statusDotActive   = lipgloss.NewStyle().Foreground(clrSuccess).Render("●")
	statusDotInactive = lipgloss.NewStyle().Foreground(clrError).Render("○")
	statusDotLoading  = lipgloss.NewStyle().Foreground(clrLoading).Render("⟳")
)

// --- Dynamic styles (depend on background-detected achromatic tokens) ---

var (
	inactiveTabStyle lipgloss.Style
	tabGapStyle      lipgloss.Style
	helpBarStyle     lipgloss.Style
	panelSubtleStyle lipgloss.Style
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

	helpBarStyle = lipgloss.NewStyle().Foreground(clrHelpBar)
	panelSubtleStyle = lipgloss.NewStyle().Foreground(clrSubtle)

	// Publish semantic styles to common so overlays and screens reference one
	// adaptive set instead of hardcoding xterm-256 colors.
	common.DimStyle = lipgloss.NewStyle().Foreground(clrDim)
	common.SelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(clrAccent)
	common.ErrorStyle = lipgloss.NewStyle().Foreground(clrError)
	common.WarningStyle = lipgloss.NewStyle().Foreground(clrWarning)
	common.ContentStyle = lipgloss.NewStyle().Padding(1, 2)
	common.StatusDotActive = statusDotActive
	common.StatusDotInactive = statusDotInactive
	common.StatusDotLoading = statusDotLoading

	// Diff styles. Fixed xterm-256 — diff add/remove/hunk colors are a near-
	// universal terminal convention (green/red/cyan); adaptive selection here
	// would just produce mud on light terminals.
	common.DiffAddStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	common.DiffRemoveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	common.DiffHunkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
}

func init() {
	cat := detectBackground()
	initColors(cat)
	initStyles()
}
