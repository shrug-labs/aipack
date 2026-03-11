package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shrug-labs/aipack/internal/app"
)

func newTestPacksModel(items []packItemDetail) packsModel {
	m := packsModel{
		items:        items,
		installedMap: map[string]int{},
	}
	for i, item := range items {
		m.installedMap[item.entry.Name] = i
	}
	m.rebuildList()
	return m
}

func TestPacksModel_EnterOpensContentPanel(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:   "test-pack",
			Path:   "/tmp/pack",
			Rules:  []string{"rule-a", "rule-b"},
			Agents: []string{"agent-a"},
		}},
	})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != packPanelContent {
		t.Fatal("expected focus=packPanelContent after enter on installed pack")
	}
	// Should have header + 2 rules + header + 1 agent = 5 items.
	if len(m.contentItems) != 5 {
		t.Fatalf("expected 5 content items, got %d", len(m.contentItems))
	}
	// Cursor should skip the header.
	if m.contentCursor != 1 {
		t.Fatalf("expected contentCursor=1 (skip header), got %d", m.contentCursor)
	}
	if m.contentItems[1].id != "rule-a" {
		t.Fatalf("expected first non-header item 'rule-a', got %q", m.contentItems[1].id)
	}
}

func TestPacksModel_ContentNavigation(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a", "rule-b"},
		}},
	})

	// Enter content panel.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.contentCursor != 1 {
		t.Fatalf("expected contentCursor=1, got %d", m.contentCursor)
	}

	// Move down.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.contentCursor != 2 {
		t.Fatalf("expected contentCursor=2 after j, got %d", m.contentCursor)
	}

	// Move up.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if m.contentCursor != 1 {
		t.Fatalf("expected contentCursor=1 after k, got %d", m.contentCursor)
	}
}

func TestPacksModel_ContentEscReturns(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != packPanelContent {
		t.Fatal("expected focus=packPanelContent")
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != packPanelList {
		t.Fatal("expected focus=packPanelList after esc")
	}
}

func TestPacksModel_ContentEnterEmitsPreview(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})

	// Enter content panel.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	// Enter on the content item should emit previewRequestMsg.
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd for preview request")
	}
	msg := cmd()
	req, ok := msg.(previewRequestMsg)
	if !ok {
		t.Fatalf("expected previewRequestMsg, got %T", msg)
	}
	if req.title != "rule-a" {
		t.Fatalf("expected title 'rule-a', got %q", req.title)
	}
	if req.packName != "test-pack" {
		t.Fatalf("expected packName 'test-pack', got %q", req.packName)
	}
	if req.category != CatRules {
		t.Fatalf("expected category %q, got %q", CatRules, req.category)
	}
}

func TestPacksModel_BuildContentItems(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:       "test-pack",
			Rules:      []string{"r1"},
			Agents:     []string{"a1", "a2"},
			Skills:     []string{"s1"},
			MCPServers: []string{},
		}},
	})

	items := m.buildContentItems(0)
	// Rules header + 1 rule + Agents header + 2 agents + Skills header + 1 skill = 7.
	if len(items) != 7 {
		t.Fatalf("expected 7 items, got %d", len(items))
	}
	if !items[0].isHeader || items[0].id != "Rules" {
		t.Fatalf("expected first item to be Rules header, got %+v", items[0])
	}
	if items[1].id != "r1" || items[1].category != CatRules {
		t.Fatalf("expected rules item 'r1', got %+v", items[1])
	}
}

func TestPacksModel_ContentFilePath(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "test-pack", Path: "/tmp/pack"}},
	})

	tests := []struct {
		ci       contentItem
		expected string
	}{
		{contentItem{category: CatRules, id: "rule-a"}, "/tmp/pack/rules/rule-a.md"},
		{contentItem{category: CatAgents, id: "agent-a"}, "/tmp/pack/agents/agent-a.md"},
		{contentItem{category: CatWorkflows, id: "wf-a"}, "/tmp/pack/workflows/wf-a.md"},
		{contentItem{category: CatSkills, id: "skill-a"}, "/tmp/pack/skills/skill-a/SKILL.md"},
	}
	for _, tt := range tests {
		got := m.contentFilePath(tt.ci)
		if got != tt.expected {
			t.Errorf("contentFilePath(%+v) = %q, want %q", tt.ci, got, tt.expected)
		}
	}
}

func TestPacksModel_EmptyPackNoContentFocus(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "empty-pack", Path: "/tmp/empty"}},
	})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != packPanelList {
		t.Fatal("expected focus=packPanelList for pack with no content")
	}
}

func TestPacksModel_HelpTextChangesInContentFocus(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.activeTab = tabPacks

	help := m.helpText()
	if !strings.Contains(help, "enter:content") {
		t.Fatalf("expected packs help to contain 'enter:content', got %q", help)
	}
	if !strings.Contains(help, ".:actions") {
		t.Fatalf("expected packs help to contain '.:actions', got %q", help)
	}

	m.packs.focus = packPanelContent
	help = m.helpText()
	if !strings.Contains(help, "enter:preview") {
		t.Fatalf("expected content focus help to contain 'enter:preview', got %q", help)
	}
	if !strings.Contains(help, "esc:back") {
		t.Fatalf("expected content focus help to contain 'esc:back', got %q", help)
	}
}

func TestPacksTab_AddDialogResult(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.activeTab = tabPacks

	// Simulate dialog result for pack-add.
	result, cmd := m.Update(dialogResultMsg{id: dialogPackAdd, confirmed: true, value: "/tmp/my-pack"})
	rm := result.(rootModel)
	if rm.dialog != nil {
		t.Fatal("expected dialog to be cleared after result")
	}
	if cmd == nil {
		t.Fatal("expected addPack command")
	}
	if !strings.Contains(rm.statusText, "adding") {
		t.Fatalf("expected status text to mention 'adding', got %q", rm.statusText)
	}
}

func TestPacksTab_RemoveDialogResult(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.activeTab = tabPacks
	m.packs = newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "test-pack"}},
	})

	result, cmd := m.Update(dialogResultMsg{id: dialogPackRemove, confirmed: true})
	rm := result.(rootModel)
	if rm.dialog != nil {
		t.Fatal("expected dialog to be cleared after result")
	}
	if cmd == nil {
		t.Fatal("expected removePack command")
	}
	if !strings.Contains(rm.statusText, "removing") {
		t.Fatalf("expected status text to mention 'removing', got %q", rm.statusText)
	}
}

func TestPackAddedMsg_ReloadsPacks(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})

	result, cmd := m.Update(packAddedMsg{name: "new-pack"})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "added new-pack") {
		t.Fatalf("expected status text 'added new-pack', got %q", rm.statusText)
	}
	if cmd == nil {
		t.Fatal("expected loadPacks reload command")
	}
}

func TestPackAddedMsg_Error(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})

	result, _ := m.Update(packAddedMsg{name: "bad-pack", err: fmt.Errorf("not found")})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "add error") {
		t.Fatalf("expected status text to mention 'add error', got %q", rm.statusText)
	}
}

func TestPackRemovedMsg_ReloadsPacksAndProfiles(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})

	result, cmd := m.Update(packRemovedMsg{name: "old-pack"})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "removed old-pack") {
		t.Fatalf("expected status text 'removed old-pack', got %q", rm.statusText)
	}
	if cmd == nil {
		t.Fatal("expected reload command (packs + profiles)")
	}
}

func TestPackUpdatedMsg_ShowsSummary(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})

	result, cmd := m.Update(packUpdatedMsg{
		name: "test-pack",
		results: []app.PackUpdateResult{
			{Name: "test-pack", Status: "updated"},
		},
	})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "test-pack: updated") {
		t.Fatalf("expected status text to contain update summary, got %q", rm.statusText)
	}
	if cmd == nil {
		t.Fatal("expected loadPacks reload command")
	}
}

func TestPacksTab_KeysIgnoredInContentFocus(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.activeTab = tabPacks
	m.packs = newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "test-pack", Path: "/tmp/pack", Rules: []string{"r1"}}},
	})
	m.packs.focus = packPanelContent
	m.packs.contentItems = m.packs.buildContentItems(0)
	m.packs.contentCursor = 1

	// 'a' should not open dialog when in content focus.
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	rm := result.(rootModel)
	if rm.dialog != nil {
		t.Fatal("expected 'a' key to be ignored in content focus mode")
	}
}

func TestPacksModel_RegistryLoaded(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "example-pack"}},
	})

	m, _ = m.Update(registryLoadedMsg{
		items: []registryItem{
			{name: "example-pack", description: "Example ops pack"},
			{name: "devtools", description: "Dev tools pack"},
		},
	})

	if m.registryState != asyncLoaded {
		t.Fatal("expected registryState=asyncLoaded")
	}
	if len(m.listItems) != 2 {
		t.Fatalf("expected 2 list items, got %d", len(m.listItems))
	}
	// example-pack should be marked installed.
	if !m.listItems[0].installed {
		t.Fatal("expected example-pack to be marked installed")
	}
	if m.listItems[1].installed {
		t.Fatal("expected devtools to NOT be marked installed")
	}
}

func TestPacksModel_UninstalledNoContentFocus(t *testing.T) {
	t.Parallel()
	m := packsModel{
		installedMap: map[string]int{},
	}
	m.registry = []registryItem{
		{name: "uninstalled-pack", description: "not installed"},
	}
	m.registryState = asyncLoaded
	m.rebuildList()

	// Enter on uninstalled pack should not switch to content panel.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != packPanelList {
		t.Fatal("expected focus=packPanelList for uninstalled pack")
	}
}
