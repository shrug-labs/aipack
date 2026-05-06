package tui

import (
	"fmt"
	"image/color"
	"math"
	"testing"
)

// sRGB-to-linear conversion per WCAG 2.x.
func srgbToLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// relativeLuminance computes WCAG relative luminance from RGB 0-255.
func relativeLuminance(r, g, b uint8) float64 {
	rl := srgbToLinear(float64(r) / 255)
	gl := srgbToLinear(float64(g) / 255)
	bl := srgbToLinear(float64(b) / 255)
	return 0.2126*rl + 0.7152*gl + 0.0722*bl
}

// contrastRatio computes the WCAG contrast ratio between two colors.
func contrastRatio(r1, g1, b1, r2, g2, b2 uint8) float64 {
	l1 := relativeLuminance(r1, g1, b1)
	l2 := relativeLuminance(r2, g2, b2)
	if l2 > l1 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// xtermGrayRGB returns the RGB value for xterm grayscale colors 232-255.
func xtermGrayRGB(n int) uint8 {
	return uint8(8 + (n-232)*10)
}

// termColorRGB extracts 8-bit RGB from a Lip Gloss v2 color.
func termColorRGB(c color.Color) (uint8, uint8, uint8, error) {
	if c == nil {
		return 0, 0, 0, fmt.Errorf("nil color")
	}
	r, g, b, _ := c.RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), nil
}

type tokenSpec struct {
	name  string
	color *color.Color // pointer to the package-level var
	minCR float64      // minimum WCAG contrast ratio required
}

func TestContrastRatios(t *testing.T) {
	// Gray palettes are the new work — strict WCAG targets.
	// Dark/light palettes are unchanged — dim/helpbar are intentionally
	// low-contrast (WCAG exempts inactive/decorative UI). We still set
	// a floor to catch regressions, and hold functional text to WCAG.
	tests := []struct {
		name   string
		cat    bgCategory
		bgFrom int
		bgTo   int
		tokens []tokenSpec
	}{
		{"bgDark", bgDark, 232, 236, []tokenSpec{
			{"clrDim", &clrDim, 1.5}, // decorative, intentionally dim
			{"clrHelpBar", &clrHelpBar, 1.5},
			{"clrSubtle", &clrSubtle, 3.0},
			{"clrSummary", &clrSummary, 3.0},
			{"clrHeader", &clrHeader, 4.5},
		}},
		{"bgGrayDark", bgGrayDark, 238, 243, []tokenSpec{
			{"clrDim", &clrDim, 3.0},
			{"clrHelpBar", &clrHelpBar, 3.0},
			{"clrSubtle", &clrSubtle, 3.0},
			{"clrSummary", &clrSummary, 3.5},
			{"clrHeader", &clrHeader, 4.5},
		}},
		{"bgGrayLight", bgGrayLight, 244, 249, []tokenSpec{
			{"clrDim", &clrDim, 3.0},
			{"clrHelpBar", &clrHelpBar, 3.0},
			{"clrSubtle", &clrSubtle, 3.0},
			{"clrSummary", &clrSummary, 3.5},
			{"clrHeader", &clrHeader, 4.5},
		}},
		{"bgLight", bgLight, 250, 255, []tokenSpec{
			{"clrDim", &clrDim, 1.5}, // decorative, intentionally dim
			{"clrHelpBar", &clrHelpBar, 1.5},
			{"clrSubtle", &clrSubtle, 2.5},
			{"clrSummary", &clrSummary, 3.0},
			{"clrHeader", &clrHeader, 4.5},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initColors(tt.cat)

			for _, tok := range tt.tokens {
				fr, fg, fb, err := termColorRGB(*tok.color)
				if err != nil {
					t.Errorf("%s: %v", tok.name, err)
					continue
				}

				for bg := tt.bgFrom; bg <= tt.bgTo; bg++ {
					v := xtermGrayRGB(bg)
					cr := contrastRatio(fr, fg, fb, v, v, v)
					if cr < tok.minCR {
						t.Errorf("%s vs bg %d (RGB %d): contrast %.2f:1, need %.1f:1",
							tok.name, bg, v, cr, tok.minCR)
					}
				}
			}
		})
	}
}

// TestColorHierarchy verifies that each palette maintains the visual hierarchy:
// dim ≤ helpbar ≤ subtle ≤ summary ≤ header (by increasing contrast ratio).
func TestColorHierarchy(t *testing.T) {
	cats := []struct {
		name string
		cat  bgCategory
		bgN  int // representative background xterm number
	}{
		{"bgDark", bgDark, 234},
		{"bgGrayDark", bgGrayDark, 240},
		{"bgGrayLight", bgGrayLight, 246},
		{"bgLight", bgLight, 253},
	}

	order := []struct {
		name  string
		color *color.Color
	}{
		{"clrDim", &clrDim},
		{"clrHelpBar", &clrHelpBar},
		{"clrSubtle", &clrSubtle},
		{"clrSummary", &clrSummary},
		{"clrHeader", &clrHeader},
	}

	for _, tt := range cats {
		t.Run(tt.name, func(t *testing.T) {
			initColors(tt.cat)

			v := xtermGrayRGB(tt.bgN)
			prevCR := 0.0
			prevName := ""

			for _, o := range order {
				fr, fg, fb, err := termColorRGB(*o.color)
				if err != nil {
					t.Errorf("%s: %v", o.name, err)
					continue
				}
				cr := contrastRatio(fr, fg, fb, v, v, v)
				if cr < prevCR-0.01 { // small epsilon for floating point
					t.Errorf("hierarchy broken: %s (%.2f:1) < %s (%.2f:1) vs bg %d",
						o.name, cr, prevName, prevCR, tt.bgN)
				}
				prevCR = cr
				prevName = o.name
			}
		})
	}
}
