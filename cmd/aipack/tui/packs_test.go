package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
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

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != packPanelContent {
		t.Fatal("expected focus=packPanelContent after enter on installed pack")
	}
	if cmd == nil {
		t.Fatal("expected inline preview load command after entering content")
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

func TestPacksModel_ListSortedInstalledThenAlphabetical(t *testing.T) {
	t.Parallel()
	m := packsModel{
		items: []packItemDetail{
			{entry: app.PackShowEntry{Name: "zeta"}},
			{entry: app.PackShowEntry{Name: "alpha"}},
		},
		installedMap: map[string]int{"zeta": 0, "alpha": 1},
		registry: []registryItem{
			{name: "beta", installed: false},
			{name: "alpha", installed: true},
			{name: "aardvark", installed: false},
		},
	}
	m.rebuildList()

	got := []string{
		m.listItems[0].name,
		m.listItems[1].name,
		m.listItems[2].name,
		m.listItems[3].name,
	}
	want := []string{"alpha", "zeta", "aardvark", "beta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected sort order %v, want %v", got, want)
		}
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

func TestPacksModel_ContentRightMovesToPreviewAndScrolls(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})
	m.focus = packPanelContent
	m.previewState = asyncLoaded
	m.previewData = previewLoadedMsg{
		title:    "rule-a",
		category: domain.CategoryRules,
		filePath: "/tmp/pack/rules/rule-a.md",
		body:     "line-1\nline-2\nline-3\nline-4\nline-5\nline-6\nline-7",
	}
	m.height = 8
	m.width = 100

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.focus != packPanelPreview {
		t.Fatalf("expected focus=packPanelPreview after right, got %d", m.focus)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.previewOffset == 0 {
		t.Fatal("expected previewOffset to advance after scrolling in preview focus")
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != packPanelContent {
		t.Fatalf("expected esc from preview to return to content, got %d", m.focus)
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
	if req.category != domain.CategoryRules {
		t.Fatalf("expected category %q, got %q", domain.CategoryRules, req.category)
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
	if items[1].id != "r1" || items[1].category != domain.CategoryRules {
		t.Fatalf("expected rules item 'r1', got %+v", items[1])
	}
}

func TestPacksModel_ListSelectionDoesNotLoadInlinePreview(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
		{entry: app.PackShowEntry{
			Name:  "other-pack",
			Path:  "/tmp/other-pack",
			Rules: []string{"rule-b"},
		}},
	})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if cmd != nil {
		t.Fatal("expected no inline preview load command while focus remains in list")
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
		{contentItem{category: domain.CategoryRules, id: "rule-a"}, "/tmp/pack/rules/rule-a.md"},
		{contentItem{category: domain.CategoryAgents, id: "agent-a"}, "/tmp/pack/agents/agent-a.md"},
		{contentItem{category: domain.CategoryWorkflows, id: "wf-a"}, "/tmp/pack/workflows/wf-a.md"},
		{contentItem{category: domain.CategorySkills, id: "skill-a"}, "/tmp/pack/skills/skill-a/SKILL.md"},
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

func TestPacksModel_ViewShowsPackInfoAndInlinePreview(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:    "test-pack",
			Path:    "/tmp/pack",
			Version: "2026.03.10",
			Rules:   []string{"rule-a"},
		}},
	})
	m.width = 120
	m.height = 30
	m.previewState = asyncLoaded
	m.previewData = previewLoadedMsg{
		title:    "rule-a",
		category: domain.CategoryRules,
		packName: "test-pack",
		filePath: "/tmp/pack/rules/rule-a.md",
		body:     "# Rule A\n\nBody text",
	}

	view := m.View()
	if !strings.Contains(view, "Version") {
		t.Fatalf("expected view to contain Version label, got:\n%s", view)
	}
	if !strings.Contains(view, "Content") {
		t.Fatalf("expected view to contain Content, got:\n%s", view)
	}
	if !strings.Contains(view, "Preview") {
		t.Fatalf("expected view to contain Preview, got:\n%s", view)
	}
	if !strings.Contains(view, "# Rule A") {
		t.Fatalf("expected preview body in view, got:\n%s", view)
	}
}

func TestPacksModel_ContentPanelShowsCategoryCounts(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:      "test-pack",
			Path:      "/tmp/pack",
			Rules:     []string{"rule-a", "rule-b"},
			Workflows: []string{"wf-a"},
		}},
	})
	m.width = 120
	m.height = 24

	view := m.viewContentPanel(36, 20)
	if !strings.Contains(view, "Rules (2)") {
		t.Fatalf("expected Rules count in content panel, got:\n%s", view)
	}
	if !strings.Contains(view, "Workflows (1)") {
		t.Fatalf("expected Workflows count in content panel, got:\n%s", view)
	}
}

func TestPacksModel_PackInfoIsCompact(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:        "test-pack",
			Path:        "/tmp/pack",
			Origin:      "/very/long/source/path/to/test-pack",
			Version:     "2026.03.10",
			Method:      "link",
			InstalledAt: "2026-03-12T21:18:52Z",
			Rules:       []string{"rule-a"},
		}},
	})
	m.width = 120
	m.height = 24
	m.registry = []registryItem{{
		name:        "test-pack",
		description: "A longish description for compact rendering.",
		repo:        "ssh://git/example/repo.git",
		ref:         "main",
		owner:       "ocm",
	}}
	m.registryState = asyncLoaded
	m.rebuildList()
	m.items[0].fileSizes = map[string]int64{"total": 4096}

	view := m.viewPackInfoPanel(40, 18)
	if strings.Contains(view, "Registry:") {
		t.Fatalf("expected compact pack info without verbose Registry section, got:\n%s", view)
	}
	if strings.Contains(view, "Installed:") {
		t.Fatalf("expected compact pack info without Installed field label, got:\n%s", view)
	}
	if !strings.Contains(view, "v2026.03.10") {
		t.Fatalf("expected compact version badge, got:\n%s", view)
	}
	if !strings.Contains(view, "A longish description") {
		t.Fatalf("expected compact description, got:\n%s", view)
	}
	if strings.Contains(view, "1 rules") {
		t.Fatalf("expected content counts to move out of pack info, got:\n%s", view)
	}
}

func TestPacksModel_ListPanelOmitsStatusBadges(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "alpha"}},
	})
	m.registry = []registryItem{{
		name:  "beta",
		repo:  "ssh://git/example/repo.git",
		owner: "ocm",
	}}
	m.registryState = asyncLoaded
	m.rebuildList()
	m.listCursor = 0

	view := m.viewListPanel(24, 10)
	if !strings.Contains(view, selectedStyle.Render("alpha")) {
		t.Fatalf("expected selected pack name to stay pink, got:\n%s", view)
	}
	if strings.Contains(view, selectedStyle.Render("alpha")+"  installed") {
		t.Fatalf("expected installed badge removed from selected row, got:\n%s", view)
	}
	if strings.Contains(view, "beta  registry") {
		t.Fatalf("expected registry badge removed from pack rows, got:\n%s", view)
	}
	if strings.Contains(view, "ssh://git/example/repo.git") {
		t.Fatalf("expected list panel to avoid duplicated repo metadata, got:\n%s", view)
	}
}

func TestPacksModel_ContentSelectionStaysHighlightedInPreviewFocus(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a", "rule-b"},
		}},
	})
	m.focus = packPanelPreview
	m.contentCursor = 2
	m.items[0].fileSizes = map[string]int64{
		"rules/rule-a": 1024,
		"rules/rule-b": 2048,
	}

	view := m.viewContentPanel(40, 12)
	if !strings.Contains(view, selectedStyle.Render("rule-b")) {
		t.Fatalf("expected selected content item to stay highlighted in preview focus, got:\n%s", view)
	}
}

func TestPacksModel_ContentPanelDoesNotInsertGapAfterCategoryHeader(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a", "rule-b"},
		}},
	})
	m.width = 120
	m.height = 20

	view := m.viewContentPanel(40, 16)
	if strings.Contains(view, "Rules (2)\n\n") {
		t.Fatalf("expected first content item immediately after category header, got:\n%s", view)
	}
}

func TestPacksModel_PreviewPanelShowsPlaceholderInListFocus(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})

	view := m.viewPreviewPanel(40, 12)
	if !strings.Contains(view, "Open content to load a preview") {
		t.Fatalf("expected list-focus preview placeholder, got:\n%s", view)
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

	m.packs.focus = packPanelPreview
	help = m.helpText()
	if !strings.Contains(help, "j/k:scroll") {
		t.Fatalf("expected preview focus help to contain 'j/k:scroll', got %q", help)
	}
	if !strings.Contains(help, "e:edit") {
		t.Fatalf("expected preview focus help to contain 'e:edit', got %q", help)
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

func TestPacksTab_CreatePackDialogChain(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.activeTab = tabPacks

	// Step 1: Action menu opens with "Create pack" option.
	result, _ := m.Update(dialogResultMsg{id: dialogActionPackTab, confirmed: true, value: actCreatePack})
	rm := result.(rootModel)
	if rm.dialog == nil || rm.dialog.id != dialogCreatePack {
		t.Fatalf("expected dialogCreatePack, got %v", rm.dialog)
	}

	// Step 2: User enters a pack name.
	result, cmd := rm.Update(dialogResultMsg{id: dialogCreatePack, confirmed: true, value: "my-new-pack"})
	rm = result.(rootModel)
	if rm.dialog != nil {
		t.Fatal("expected dialog to be cleared after create-pack confirm")
	}
	if cmd == nil {
		t.Fatal("expected createPack command")
	}
	if !strings.Contains(rm.statusText, "creating my-new-pack") {
		t.Fatalf("expected status 'creating my-new-pack', got %q", rm.statusText)
	}
}

func TestPackCreatedMsg_ReloadsPacksAndProfiles(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})

	result, cmd := m.Update(packCreatedMsg{name: "fresh-pack"})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "created fresh-pack") {
		t.Fatalf("expected status 'created fresh-pack', got %q", rm.statusText)
	}
	if cmd == nil {
		t.Fatal("expected reload command")
	}
}

func TestPackCreatedMsg_Error(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})

	result, _ := m.Update(packCreatedMsg{name: "bad", err: fmt.Errorf("already exists")})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "create error") {
		t.Fatalf("expected status to mention 'create error', got %q", rm.statusText)
	}
}

func TestCreatePack_ScaffoldsAndRegisters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Set up minimal sync-config.
	scPath := config.SyncConfigPath(dir)
	if err := config.SaveSyncConfig(scPath, config.SyncConfig{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}

	cmd := createPack(dir, "test-created")
	msg := cmd()
	created, ok := msg.(packCreatedMsg)
	if !ok {
		t.Fatalf("expected packCreatedMsg, got %T", msg)
	}
	if created.err != nil {
		t.Fatalf("unexpected error: %v", created.err)
	}
	if created.name != "test-created" {
		t.Fatalf("expected name 'test-created', got %q", created.name)
	}

	// Verify pack was scaffolded.
	packDir := filepath.Join(dir, "packs", "test-created")
	if _, err := os.Stat(filepath.Join(packDir, "pack.json")); err != nil {
		t.Fatalf("expected pack.json to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(packDir, "rules")); err != nil {
		t.Fatalf("expected rules/ dir to exist: %v", err)
	}

	// Verify registered in sync-config.
	sc, err := config.LoadSyncConfig(scPath)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := sc.InstalledPacks["test-created"]
	if !ok {
		t.Fatal("expected pack to be registered in sync-config")
	}
	if meta.Method != config.MethodLocal {
		t.Fatalf("expected method %q, got %q", config.MethodLocal, meta.Method)
	}
}

func TestCreatePack_DuplicateErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	scPath := config.SyncConfigPath(dir)
	if err := config.SaveSyncConfig(scPath, config.SyncConfig{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}

	// First create succeeds.
	cmd := createPack(dir, "dup-pack")
	msg := cmd().(packCreatedMsg)
	if msg.err != nil {
		t.Fatalf("first create failed: %v", msg.err)
	}

	// Second create should fail (pack.json already exists).
	cmd = createPack(dir, "dup-pack")
	msg = cmd().(packCreatedMsg)
	if msg.err == nil {
		t.Fatal("expected error on duplicate pack create")
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
