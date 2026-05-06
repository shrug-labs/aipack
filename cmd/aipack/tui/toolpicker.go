package tui

import (
	pickeroverlay "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/overlays/picker"
	"github.com/shrug-labs/aipack/internal/mcp"
)

type triState = pickeroverlay.TriState
type toolPickerItem = pickeroverlay.Item
type toolPicker = pickeroverlay.Model

const (
	triOff  = pickeroverlay.TriOff
	triAsk  = pickeroverlay.TriAsk
	triAuto = pickeroverlay.TriAuto
)

func pickerMaxVisible(termHeight int) int {
	return pickeroverlay.MaxVisible(termHeight)
}

func newToolPicker(id, title string, items []toolPickerItem) toolPicker {
	return pickeroverlay.New(id, title, items)
}

func probePhaseLabel(phase mcp.ProbePhase, transport string) string {
	return pickeroverlay.ProbePhaseLabel(phase, transport)
}
