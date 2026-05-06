package packs

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

func updateTestModel(t testingT, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	screen, cmd := m.Update(msg)
	next, ok := screen.(Model)
	if !ok {
		t.Fatalf("expected packs.Model, got %T", screen)
	}
	return next, cmd
}

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
}
