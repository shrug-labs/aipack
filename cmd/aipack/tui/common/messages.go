package common

import (
	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/internal/domain"
)

type PreviewRequestMsg struct {
	Title    string
	Category domain.PackCategory
	PackName string
	FilePath string
}

type LayerHitMsg struct {
	ID    string
	Mouse tea.MouseMsg
}

func IsLeftClick(mouse tea.MouseMsg) bool {
	click, ok := mouse.(tea.MouseClickMsg)
	return ok && click.Button == tea.MouseLeft
}

const MouseWheelRows = 3

func MouseWheelDelta(mouse tea.MouseMsg) int {
	wheel, ok := mouse.(tea.MouseWheelMsg)
	if !ok {
		return 0
	}
	switch wheel.Mouse().Button {
	case tea.MouseWheelDown:
		return 1
	case tea.MouseWheelUp:
		return -1
	default:
		return 0
	}
}

type FocusMsg struct{}
type BlurMsg struct{}
