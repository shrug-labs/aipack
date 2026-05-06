package profiles

import (
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
)

func testKeyText(text string) tea.KeyPressMsg {
	r, _ := utf8.DecodeRuneInString(text)
	return tea.KeyPressMsg(tea.Key{Text: text, Code: r})
}

func testKeyCode(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

func testTree(manifest config.PackManifest, entry config.PackEntry) treeModel {
	packs := []app.ProfilePackInfo{{Index: 0, Name: entry.Name, Root: "/tmp/pack", Manifest: manifest}}
	ct := app.BuildContentTree(packs, []config.PackEntry{entry})
	return buildTreeFromContent(ct)
}
