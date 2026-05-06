package common

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Screen is the contract between the app shell and extracted screens.
type Screen interface {
	Init() tea.Cmd
	Update(tea.Msg) (Screen, tea.Cmd)
	View() *lipgloss.Layer
	SetSize(width, height int) Screen
}
