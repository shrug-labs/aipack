package tui

import (
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

func testKeyText(text string) tea.KeyPressMsg {
	r, _ := utf8.DecodeRuneInString(text)
	return tea.KeyPressMsg(tea.Key{Text: text, Code: r})
}

func testKeyCode(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

func testKeyCtrl(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Mod: tea.ModCtrl})
}
