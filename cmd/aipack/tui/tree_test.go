package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestTreeModel_Toggle(t *testing.T) {
	t.Parallel()
	tree := testTree(
		config.PackManifest{Rules: []string{"rule-a", "rule-b"}},
		config.PackEntry{Name: "test-pack"},
	)

	// Default: all enabled (nil selector = all).
	itemIdx := 1 // first item after category
	tree.cursor = itemIdx
	if !tree.nodes[itemIdx].enabled {
		t.Fatal("expected rule-a to be enabled by default")
	}

	// Toggle off.
	changed := tree.toggle()
	if !changed {
		t.Fatal("expected toggle to report change")
	}
	if tree.nodes[itemIdx].enabled {
		t.Fatal("expected rule-a to be disabled after toggle")
	}

	// Toggle back on.
	changed = tree.toggle()
	if !changed {
		t.Fatal("expected toggle to report change")
	}
	if !tree.nodes[itemIdx].enabled {
		t.Fatal("expected rule-a to be enabled after second toggle")
	}
}

func TestTreeModel_CategoryExpandCollapse(t *testing.T) {
	t.Parallel()
	tree := testTree(
		config.PackManifest{Rules: []string{"rule-a"}, Agents: []string{"agent-a"}},
		config.PackEntry{Name: "test-pack"},
	)

	// Category at index 0 should be expanded.
	if !tree.nodes[0].expanded {
		t.Fatal("expected Rules category to be expanded")
	}

	// Collapse it.
	tree.cursor = 0
	tree.toggle()
	if tree.nodes[0].expanded {
		t.Fatal("expected Rules category to be collapsed after toggle")
	}

	// Items under collapsed category should not be visible.
	if tree.isVisible(1) {
		t.Fatal("expected rule-a to be hidden when Rules is collapsed")
	}
}

func TestTreeModel_MultiPackAttribution(t *testing.T) {
	t.Parallel()
	packs := []app.ProfilePackInfo{
		{Index: 0, Name: "pack-a", Root: "/tmp/a", Manifest: config.PackManifest{Rules: []string{"rule-1"}}},
		{Index: 1, Name: "pack-b", Root: "/tmp/b", Manifest: config.PackManifest{Rules: []string{"rule-2"}}},
	}
	entries := []config.PackEntry{
		{Name: "pack-a"},
		{Name: "pack-b"},
	}
	ct := app.BuildContentTree(packs, entries)
	tree := buildTreeFromContent(ct)

	// Should have: category + 2 items.
	if len(tree.nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(tree.nodes))
	}
	if tree.nodes[1].packIdx != 0 {
		t.Fatalf("expected rule-1 packIdx=0, got %d", tree.nodes[1].packIdx)
	}
	if tree.nodes[2].packIdx != 1 {
		t.Fatalf("expected rule-2 packIdx=1, got %d", tree.nodes[2].packIdx)
	}

	// View should show pack names.
	view := tree.view(false, 80)
	if !strings.Contains(view, "pack-a") {
		t.Fatal("expected view to contain pack-a attribution")
	}
	if !strings.Contains(view, "pack-b") {
		t.Fatal("expected view to contain pack-b attribution")
	}
}

func TestTreeModel_EnterOpensPreview(t *testing.T) {
	t.Parallel()
	tree := testTree(
		config.PackManifest{Rules: []string{"rule-a", "rule-b"}},
		config.PackEntry{Name: "test-pack"},
	)

	// Move to first item (index 1, after the category header).
	tree.cursor = 1
	if !tree.nodes[1].enabled {
		t.Fatal("expected rule-a enabled by default")
	}

	pe := config.PackEntry{Name: "test-pack"}
	m := profilesModel{
		items: []profileItem{
			{
				name: "test",
				cfg:  config.ProfileConfig{Packs: []config.PackEntry{pe}},
				tree: &tree,
			},
		},
		cursor: 0,
		focus:  panelTree,
	}

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Enter should NOT toggle the item.
	if !m.items[0].tree.nodes[1].enabled {
		t.Fatal("expected rule-a to remain enabled (enter opens preview, not toggle)")
	}
	if m.dirty {
		t.Fatal("expected dirty=false (enter opens preview, not toggle)")
	}

	// Should emit a previewRequestMsg.
	if cmd == nil {
		t.Fatal("expected a preview request cmd")
	}
	msg := cmd()
	req, ok := msg.(previewRequestMsg)
	if !ok {
		t.Fatalf("expected previewRequestMsg, got %T", msg)
	}
	if req.title != "rule-a" {
		t.Fatalf("expected title 'rule-a', got %q", req.title)
	}
	if req.category != domain.CategoryRules {
		t.Fatalf("expected category %q, got %q", domain.CategoryRules, req.category)
	}
	if req.packName != "test-pack" {
		t.Fatalf("expected packName 'test-pack', got %q", req.packName)
	}
}

func TestTreeModel_EnterExpandsCategory(t *testing.T) {
	t.Parallel()
	tree := testTree(
		config.PackManifest{Rules: []string{"rule-a"}},
		config.PackEntry{Name: "test-pack"},
	)

	tree.cursor = 0
	if !tree.nodes[0].expanded {
		t.Fatal("expected category expanded initially")
	}

	pe := config.PackEntry{Name: "test-pack"}
	m := profilesModel{
		items: []profileItem{
			{
				name: "test",
				cfg:  config.ProfileConfig{Packs: []config.PackEntry{pe}},
				tree: &tree,
			},
		},
		cursor: 0,
		focus:  panelTree,
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.items[0].tree.nodes[0].expanded {
		t.Fatal("expected category collapsed after enter")
	}
	if m.dirty {
		t.Fatal("expected dirty=false after category collapse (not a content change)")
	}
}

func TestEnsureTree_NoPacks(t *testing.T) {
	t.Parallel()
	m := profilesModel{
		items: []profileItem{
			{name: "empty", cfg: config.ProfileConfig{}},
		},
		cursor: 0,
	}
	m = m.ensureTree()
	if m.items[0].treeErr == "" {
		t.Fatal("expected treeErr for profile with no packs")
	}
}

func TestFormatSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1024, "1.0 KB"},
		{2048, "2.0 KB"},
		{1048576, "1.0 MB"},
		{1572864, "1.5 MB"},
	}
	for _, tt := range tests {
		got := formatSize(tt.input)
		if got != tt.expected {
			t.Errorf("formatSize(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestEnsureTree_SkipsDisabledPacks(t *testing.T) {
	t.Parallel()
	disabled := false
	m := profilesModel{
		items: []profileItem{
			{
				name: "default",
				path: "/tmp/profiles/default.yaml",
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "pack-a", Enabled: &disabled},
						{Name: "pack-b"},
					},
				},
			},
		},
		cursor: 0,
	}

	m = m.ensureTree()

	// With pack-a disabled and pack-b source not resolvable,
	// the tree should be nil or only contain pack-b entries.
	// Since /tmp/packs/b/pack.json won't exist, tree may fail,
	// but pack-a should definitely not be attempted.
	item := m.currentItem()
	if item.tree != nil {
		// If a tree was built, it should only have pack-b entries.
		for _, p := range item.tree.packs {
			if p.Name == "pack-a" {
				t.Fatal("disabled pack-a should not appear in tree")
			}
		}
	}
}

func TestTreeModel_FilePath(t *testing.T) {
	t.Parallel()
	packs := []app.ProfilePackInfo{
		{Index: 0, Name: "test-pack", Root: "/tmp/pack", Manifest: config.PackManifest{
			Rules:     []string{"rule-a"},
			Agents:    []string{"agent-a"},
			Workflows: []string{"flow-a"},
			Skills:    []string{"skill-a"},
		}},
	}
	ct := app.BuildContentTree(packs, []config.PackEntry{{Name: "test-pack"}})
	tree := buildTreeFromContent(ct)

	// Find each category's first item and check path.
	for i, n := range tree.nodes {
		if n.kind != nodeItem {
			continue
		}
		tree.cursor = i
		fp := tree.filePath()
		switch n.category {
		case domain.CategoryRules:
			if fp != "/tmp/pack/rules/rule-a.md" {
				t.Fatalf("rules: expected /tmp/pack/rules/rule-a.md, got %q", fp)
			}
		case domain.CategoryAgents:
			if fp != "/tmp/pack/agents/agent-a.md" {
				t.Fatalf("agents: expected /tmp/pack/agents/agent-a.md, got %q", fp)
			}
		case domain.CategoryWorkflows:
			if fp != "/tmp/pack/workflows/flow-a.md" {
				t.Fatalf("workflows: expected /tmp/pack/workflows/flow-a.md, got %q", fp)
			}
		case domain.CategorySkills:
			if fp != "/tmp/pack/skills/skill-a/SKILL.md" {
				t.Fatalf("skills: expected /tmp/pack/skills/skill-a/SKILL.md, got %q", fp)
			}
		}
	}

	// Category node should return "".
	tree.cursor = 0
	if fp := tree.filePath(); fp != "" {
		t.Fatalf("expected empty path for category node, got %q", fp)
	}
}

func TestTreeModel_FilePathMCP(t *testing.T) {
	t.Parallel()
	packs := []app.ProfilePackInfo{
		{Index: 0, Name: "test-pack", Root: "/tmp/pack", Manifest: config.PackManifest{
			MCP: config.MCPPack{Servers: map[string]config.MCPDefaults{"srv": {}}},
		}},
	}
	ct := app.BuildContentTree(packs, []config.PackEntry{{Name: "test-pack"}})
	tree := buildTreeFromContent(ct)
	for i, n := range tree.nodes {
		if n.kind == nodeItem && n.category == domain.CategoryMCP {
			tree.cursor = i
			fp := tree.filePath()
			if fp != "/tmp/pack/mcp/srv.json" {
				t.Fatalf("expected /tmp/pack/mcp/srv.json, got %q", fp)
			}
			return
		}
	}
	t.Fatal("expected to find an MCP item node")
}

func TestTreeModel_EnterOnMCPItem_OpensPreview(t *testing.T) {
	t.Parallel()
	packs := []app.ProfilePackInfo{
		{Index: 0, Name: "test-pack", Root: "/tmp/pack", Manifest: config.PackManifest{
			MCP: config.MCPPack{Servers: map[string]config.MCPDefaults{"srv": {}}},
		}},
	}
	ct := app.BuildContentTree(packs, []config.PackEntry{{Name: "test-pack"}})
	tree := buildTreeFromContent(ct)

	// Expand the MCP category.
	tree.cursor = 0
	tree.toggle()

	// Move to the MCP item.
	for i, n := range tree.nodes {
		if n.kind == nodeItem && n.category == domain.CategoryMCP {
			tree.cursor = i
			break
		}
	}

	pe := config.PackEntry{Name: "test-pack"}
	m := profilesModel{
		items: []profileItem{
			{
				name: "test",
				cfg:  config.ProfileConfig{Packs: []config.PackEntry{pe}},
				tree: &tree,
			},
		},
		cursor: 0,
		focus:  panelTree,
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected preview cmd for enter on MCP item")
	}
	msg := cmd()
	req, ok := msg.(previewRequestMsg)
	if !ok {
		t.Fatalf("expected previewRequestMsg, got %T", msg)
	}
	if req.category != domain.CategoryMCP || req.title != "srv" {
		t.Fatalf("expected MCP preview for 'srv', got category=%q title=%q", req.category, req.title)
	}
}

func TestTreeToggle_InvalidatesSyncStatusForActiveProfile(t *testing.T) {
	t.Parallel()
	tree := testTree(
		config.PackManifest{Rules: []string{"rule-a", "rule-b"}},
		config.PackEntry{Name: "test-pack"},
	)
	tree.cursor = 1 // first item

	pe := config.PackEntry{Name: "test-pack"}
	m := profilesModel{
		items: []profileItem{
			{
				name:      "test",
				isActive:  true,
				syncState: syncSynced,
				cfg:       config.ProfileConfig{Packs: []config.PackEntry{pe}},
				tree:      &tree,
			},
		},
		cursor: 0,
		focus:  panelTree,
	}

	// Toggle an item — should invalidate sync status.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if m.items[0].syncState != syncPending {
		t.Fatalf("expected syncState=syncPending after toggle, got %d", m.items[0].syncState)
	}
	if !m.dirty {
		t.Fatal("expected dirty=true after toggle")
	}
}

func TestTreeToggle_DoesNotInvalidateSyncForInactiveProfile(t *testing.T) {
	t.Parallel()
	tree := testTree(
		config.PackManifest{Rules: []string{"rule-a"}},
		config.PackEntry{Name: "test-pack"},
	)
	tree.cursor = 1

	pe := config.PackEntry{Name: "test-pack"}
	m := profilesModel{
		items: []profileItem{
			{
				name:      "test",
				isActive:  false,
				syncState: syncSynced,
				cfg:       config.ProfileConfig{Packs: []config.PackEntry{pe}},
				tree:      &tree,
			},
		},
		cursor: 0,
		focus:  panelTree,
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	// Non-active profile: sync state should NOT be invalidated.
	if m.items[0].syncState != syncSynced {
		t.Fatalf("expected syncState=syncSynced for inactive profile, got %d", m.items[0].syncState)
	}
}
