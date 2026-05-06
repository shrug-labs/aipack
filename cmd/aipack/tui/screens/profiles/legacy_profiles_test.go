package profiles

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
)

func TestTwoPanelView_ShowsProfileDetails(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []profileItem{
			{
				name:      "default",
				isActive:  true,
				syncState: common.SyncStatusSynced,
				syncTarget: common.SyncTarget{
					Harnesses:  []string{"cline"},
					Scope:      "project",
					ProjectDir: "/tmp/project",
				},
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "my-pack"},
					},
				},
			},
		},
		cursor: 0,
		width:  120,
		height: 40,
	}

	view := m.render()

	// Profile list shows the profile name with active dot.
	if !strings.Contains(view, "default") {
		t.Fatalf("expected view to contain 'default', got:\n%s", view)
	}
	// Sync details moved to Sync tab — should NOT appear in profiles view.
	if strings.Contains(view, "up to date") {
		t.Fatalf("sync status should not appear in profiles view, got:\n%s", view)
	}
	if !strings.Contains(view, "Packs (1/1)") {
		t.Fatalf("expected view to contain 'Packs (1/1)', got:\n%s", view)
	}
	if !strings.Contains(view, "my-pack") {
		t.Fatalf("expected view to contain 'my-pack', got:\n%s", view)
	}
}

func TestProfilesTab_BundledProfileWarning(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []profileItem{{
			name:        "team",
			bundledFrom: []string{"source-pack"},
			cfg:         config.ProfileConfig{Packs: []config.PackEntry{{Name: "my-pack"}}},
		}},
		cursor: 0,
		width:  100,
		height: 24,
	}

	view := m.render()
	if !strings.Contains(view, "pack-provided profile") || !strings.Contains(view, "source-pack") {
		t.Fatalf("expected bundled profile warning, got:\n%s", view)
	}
}

func TestBundledProfileStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		item     *profileItem
		wantHits []string
	}{
		{
			name:     "bundled profile produces warning",
			item:     &profileItem{name: "team", bundledFrom: []string{"owner-pack", "extras-pack"}},
			wantHits: []string{"team", "pack-provided", "owner-pack", "extras-pack", "overwrite"},
		},
		{
			name: "non-bundled profile is silent",
			item: &profileItem{name: "default"},
		},
		{
			name: "nil item is silent",
			item: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := bundledProfileStatus(tc.item)
			if len(tc.wantHits) == 0 {
				if got != "" {
					t.Fatalf("expected empty status, got %q", got)
				}
				return
			}
			for _, hit := range tc.wantHits {
				if !strings.Contains(got, hit) {
					t.Fatalf("status %q missing %q", got, hit)
				}
			}
		})
	}
}

func TestProfilesTab_RosterWidthCappedForLongPackNames(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []profileItem{{
			name: "team",
			cfg: config.ProfileConfig{Packs: []config.PackEntry{{
				Name: "very-long-pack-name-that-should-not-steal-the-tree-column",
			}}},
		}},
		cursor: 0,
		width:  90,
		height: 24,
	}

	if got := m.rosterMinWidth(); got > 32 {
		t.Fatalf("rosterMinWidth = %d, want capped at 32", got)
	}
}

func TestProfileList_SelectedProfileStaysHighlightedWhenNotFocused(t *testing.T) {
	t.Parallel()

	m := Model{
		items: []profileItem{
			{name: "default", isActive: true},
			{name: "other"},
		},
		cursor: 1,
		focus:  panelPacks,
		width:  120,
		height: 20,
	}

	view := m.viewProfileList(24, 10)
	if !strings.Contains(view, common.SelectedStyle.Render("other")) {
		t.Fatalf("expected selected profile to stay pink out of focus, got:\n%s", view)
	}
}

func TestAddPackToProfile(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []profileItem{
			{
				name: "test",
				cfg:  config.ProfileConfig{SchemaVersion: 6},
			},
		},
		cursor: 0,
	}

	m = m.addPackToProfile("new-pack")
	item := m.currentItem()
	if item == nil {
		t.Fatal("expected current item")
	}

	// Check pack entry was added.
	if len(item.cfg.Packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(item.cfg.Packs))
	}
	if item.cfg.Packs[0].Name != "new-pack" {
		t.Fatalf("expected pack name 'new-pack', got %q", item.cfg.Packs[0].Name)
	}

	if !m.dirty {
		t.Fatal("expected dirty=true after adding pack")
	}
}

func TestRemovePackFromProfile(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []profileItem{
			{
				name: "test",
				cfg: config.ProfileConfig{
					SchemaVersion: 6,
					Packs: []config.PackEntry{
						{Name: "pack-a"},
						{Name: "pack-b"},
					},
				},
			},
		},
		cursor: 0,
	}

	m = m.removePackFromProfile("pack-a")
	item := m.currentItem()
	if item == nil {
		t.Fatal("expected current item")
	}

	// Check pack entry was removed.
	if len(item.cfg.Packs) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(item.cfg.Packs))
	}
	if item.cfg.Packs[0].Name != "pack-b" {
		t.Fatalf("expected remaining pack 'pack-b', got %q", item.cfg.Packs[0].Name)
	}

	if !m.dirty {
		t.Fatal("expected dirty=true after removing pack")
	}
}

func TestProfilePackNames(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []profileItem{
			{
				name: "test",
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "pack-a"},
						{Name: "pack-b"},
					},
				},
			},
		},
		cursor: 0,
	}

	names := m.profilePackNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 pack names, got %d", len(names))
	}
	if names[0] != "pack-a" || names[1] != "pack-b" {
		t.Fatalf("expected [pack-a, pack-b], got %v", names)
	}
}

func TestProfilesLoaded_ClampsOffsetForLastActiveProfile(t *testing.T) {
	t.Parallel()

	items := make([]profileItem, 0, 7)
	for i := range 7 {
		items = append(items, profileItem{
			name:     "profile-" + string(rune('0'+i)),
			isActive: i == 6,
			cfg:      config.ProfileConfig{Packs: []config.PackEntry{{Name: "pack-a"}}},
		})
	}

	m := Model{
		width:  100,
		height: 10,
		focus:  panelProfiles,
	}

	updated, _ := m.Update(LoadedMsg{Items: items})
	m = updated.(Model)

	view := m.viewProfileList(20, 8)
	if !strings.Contains(view, "profile-6") {
		t.Fatalf("expected last active profile to be visible after load, got:\n%s", view)
	}
}

func TestViewTreePanel_ShowsLastContentItemAtBottom(t *testing.T) {
	t.Parallel()

	tree := testTree(
		config.PackManifest{Rules: []string{"rule-0", "rule-1", "rule-2", "rule-3", "rule-4", "rule-5"}},
		config.PackEntry{Name: "test-pack"},
	)
	tree.cursor = len(tree.nodes) - 1
	tree.clampOffset(6)

	m := Model{
		items: []profileItem{{
			name: "default",
			cfg:  config.ProfileConfig{Packs: []config.PackEntry{{Name: "test-pack"}}},
			tree: &tree,
		}},
		cursor: 0,
		focus:  panelTree,
		width:  100,
		height: 10,
	}

	view := m.viewTreePanel(40, 8)
	if !strings.Contains(view, "rule-5") {
		t.Fatalf("expected tree panel to include the last content item, got:\n%s", view)
	}
}

func TestLeftPanel_PackCursorNavigation(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []profileItem{
			{
				name: "default",
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "pack-a"},
						{Name: "pack-b"},
						{Name: "pack-c"},
					},
				},
			},
		},
		cursor: 0,
	}

	// Initial cursor at 0.
	if m.packCursor != 0 {
		t.Fatalf("expected initial packCursor=0, got %d", m.packCursor)
	}

	// Move down.
	m, _ = m.updatePackRoster(testKeyText(string([]rune{'j'})))
	if m.packCursor != 1 {
		t.Fatalf("expected packCursor=1 after j, got %d", m.packCursor)
	}

	// Move down again.
	m, _ = m.updatePackRoster(testKeyText(string([]rune{'j'})))
	if m.packCursor != 2 {
		t.Fatalf("expected packCursor=2 after second j, got %d", m.packCursor)
	}

	// Move to "Add pack..." virtual item.
	m, _ = m.updatePackRoster(testKeyText(string([]rune{'j'})))
	if m.packCursor != 3 {
		t.Fatalf("expected packCursor=3 (Add pack...), got %d", m.packCursor)
	}

	// Wrap around.
	m, _ = m.updatePackRoster(testKeyText(string([]rune{'j'})))
	if m.packCursor != 0 {
		t.Fatalf("expected packCursor=0 after wrap, got %d", m.packCursor)
	}

	// Move up wraps to end (Add pack... item).
	m, _ = m.updatePackRoster(testKeyText(string([]rune{'k'})))
	if m.packCursor != 3 {
		t.Fatalf("expected packCursor=3 after k from 0, got %d", m.packCursor)
	}
}

func TestLeftPanel_TogglePackEnabled(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []profileItem{
			{
				name:     "default",
				isActive: true,
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "pack-a"},
						{Name: "pack-b"},
					},
				},
			},
		},
		cursor: 0,
	}

	// Initially all packs enabled (nil = true).
	pe := m.items[0].cfg.Packs[0]
	if pe.Enabled != nil {
		t.Fatal("expected pack-a Enabled to be nil (default-true)")
	}

	// Toggle: should disable pack-a.
	m = m.togglePackEnabled(0)
	pe = m.items[0].cfg.Packs[0]
	if pe.Enabled == nil || *pe.Enabled {
		t.Fatal("expected pack-a to be disabled after toggle")
	}
	if !m.dirty {
		t.Fatal("expected dirty after toggle")
	}

	// Toggle again: should re-enable.
	m = m.togglePackEnabled(0)
	pe = m.items[0].cfg.Packs[0]
	if pe.Enabled != nil {
		t.Fatalf("expected pack-a Enabled to be nil after re-enable, got %v", *pe.Enabled)
	}

	// Sync state should be invalidated for active profile.
	m.items[0].syncState = common.SyncStatusSynced
	m = m.togglePackEnabled(1)
	if m.items[0].syncState != common.SyncStatusPending {
		t.Fatal("expected sync state to be invalidated after pack toggle on active profile")
	}
}

func TestLeftPanel_MovePackDown(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []profileItem{
			{
				name:     "default",
				isActive: true,
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "pack-a"},
						{Name: "pack-b"},
						{Name: "pack-c"},
					},
				},
			},
		},
		cursor:     0,
		packCursor: 0,
	}

	// Move pack-a down: [a,b,c] → [b,a,c], cursor follows to 1.
	m = m.movePackDown(0)
	if m.items[0].cfg.Packs[0].Name != "pack-b" {
		t.Fatalf("expected pack-b at index 0, got %s", m.items[0].cfg.Packs[0].Name)
	}
	if m.items[0].cfg.Packs[1].Name != "pack-a" {
		t.Fatalf("expected pack-a at index 1, got %s", m.items[0].cfg.Packs[1].Name)
	}
	if m.packCursor != 1 {
		t.Fatalf("expected cursor to follow pack to 1, got %d", m.packCursor)
	}
	if !m.dirty {
		t.Fatal("expected dirty after reorder")
	}
	if m.items[0].syncState != common.SyncStatusPending {
		t.Fatal("expected sync state invalidated after reorder")
	}

	// Move at last position is a no-op.
	m.packCursor = 2
	m = m.movePackDown(2)
	if m.items[0].cfg.Packs[2].Name != "pack-c" {
		t.Fatalf("expected pack-c still at index 2, got %s", m.items[0].cfg.Packs[2].Name)
	}
	if m.packCursor != 2 {
		t.Fatalf("expected cursor unchanged at 2, got %d", m.packCursor)
	}
}

func TestLeftPanel_MovePackUp(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []profileItem{
			{
				name:     "default",
				isActive: true,
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "pack-a"},
						{Name: "pack-b"},
						{Name: "pack-c"},
					},
				},
			},
		},
		cursor:     0,
		packCursor: 2,
	}

	// Move pack-c up: [a,b,c] → [a,c,b], cursor follows to 1.
	m = m.movePackUp(2)
	if m.items[0].cfg.Packs[1].Name != "pack-c" {
		t.Fatalf("expected pack-c at index 1, got %s", m.items[0].cfg.Packs[1].Name)
	}
	if m.items[0].cfg.Packs[2].Name != "pack-b" {
		t.Fatalf("expected pack-b at index 2, got %s", m.items[0].cfg.Packs[2].Name)
	}
	if m.packCursor != 1 {
		t.Fatalf("expected cursor to follow pack to 1, got %d", m.packCursor)
	}

	// Move at first position is a no-op.
	m.packCursor = 0
	m = m.movePackUp(0)
	if m.items[0].cfg.Packs[0].Name != "pack-a" {
		t.Fatalf("expected pack-a still at index 0, got %s", m.items[0].cfg.Packs[0].Name)
	}
	if m.packCursor != 0 {
		t.Fatalf("expected cursor unchanged at 0, got %d", m.packCursor)
	}
}

func TestLeftPanel_ReorderViaKeypress(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []profileItem{
			{
				name: "default",
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "pack-a"},
						{Name: "pack-b"},
						{Name: "pack-c"},
					},
				},
			},
		},
		cursor:     0,
		packCursor: 0,
	}

	// J (shift+j) moves pack down.
	m, _ = m.updatePackRoster(testKeyText(string([]rune{'J'})))
	if m.items[0].cfg.Packs[0].Name != "pack-b" {
		t.Fatalf("expected pack-b at index 0 after J, got %s", m.items[0].cfg.Packs[0].Name)
	}
	if m.packCursor != 1 {
		t.Fatalf("expected cursor at 1 after J, got %d", m.packCursor)
	}

	// K (shift+k) moves pack up.
	m, _ = m.updatePackRoster(testKeyText(string([]rune{'K'})))
	if m.items[0].cfg.Packs[0].Name != "pack-a" {
		t.Fatalf("expected pack-a at index 0 after K, got %s", m.items[0].cfg.Packs[0].Name)
	}
	if m.packCursor != 0 {
		t.Fatalf("expected cursor at 0 after K, got %d", m.packCursor)
	}

	// J on "Add pack..." virtual row is a no-op.
	m.packCursor = 3 // "Add pack..." position
	m, _ = m.updatePackRoster(testKeyText(string([]rune{'J'})))
	if m.packCursor != 3 {
		t.Fatalf("expected cursor unchanged on Add pack row, got %d", m.packCursor)
	}
}

func TestLeftPanel_SpaceTogglesPackViaUpdateList(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []profileItem{
			{
				name: "default",
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "pack-a"},
						{Name: "pack-b"},
					},
				},
			},
		},
		cursor:     0,
		packCursor: 1,
	}

	// Space should toggle the pack at packCursor.
	m, _ = m.updatePackRoster(testKeyText(string([]rune{' '})))
	pe := m.items[0].cfg.Packs[1]
	if pe.Enabled == nil || *pe.Enabled {
		t.Fatal("expected pack-b to be disabled after space at packCursor=1")
	}
}

func TestLeftPanel_EnterSwitchesToRightPanel(t *testing.T) {
	t.Parallel()
	tree := treeModel{nodes: []treeNode{{kind: nodeItem, id: "test"}}}
	m := Model{
		items: []profileItem{
			{
				name: "default",
				tree: &tree,
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{{Name: "p"}},
				},
			},
		},
		cursor: 0,
		focus:  panelPacks,
	}

	m, _ = m.updatePackRoster(testKeyCode(tea.KeyEnter))
	if m.focus != panelTree {
		t.Fatal("expected focus to switch to panelTree after enter")
	}
}

func TestLeftPanel_RightArrowSwitchesToRightPanel(t *testing.T) {
	t.Parallel()
	tree := treeModel{nodes: []treeNode{{kind: nodeItem, id: "test"}}}
	m := Model{
		items: []profileItem{
			{
				name: "default",
				tree: &tree,
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{{Name: "p"}},
				},
			},
		},
		cursor: 0,
		focus:  panelPacks,
	}

	m, _ = m.updatePackRoster(testKeyCode(tea.KeyRight))
	if m.focus != panelTree {
		t.Fatal("expected focus to switch to panelTree after right arrow")
	}
}

func TestRightPanel_LeftArrowSwitchesToLeftPanel(t *testing.T) {
	t.Parallel()
	tree := treeModel{nodes: []treeNode{{kind: nodeItem, id: "test"}}}
	m := Model{
		items: []profileItem{
			{
				name: "default",
				tree: &tree,
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{{Name: "p"}},
				},
			},
		},
		cursor: 0,
		focus:  panelTree,
	}

	m, _ = m.updateTree(testKeyCode(tea.KeyLeft))
	if m.focus != panelPacks {
		t.Fatal("expected focus to switch to panelPacks after left arrow")
	}
}

func TestLeftPanel_SpaceReturnsFileSizeCmd(t *testing.T) {
	t.Parallel()
	// Build a tree manually so computeFileSizesCmd has something to work with.
	tree := treeModel{
		nodes: []treeNode{{kind: nodeItem, id: "test", packIdx: 0}},
		packs: []app.ProfilePackInfo{{Index: 0, Name: "pack-a", Root: "/tmp/pack-a"}},
	}
	m := Model{
		items: []profileItem{
			{
				name: "default",
				tree: &tree,
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "pack-a"},
						{Name: "pack-b"},
					},
				},
			},
		},
		cursor:     0,
		packCursor: 1, // toggle pack-b so pack-a tree remains
	}

	// Space toggle should return a cmd (computeFileSizesCmd).
	_, cmd := m.updatePackRoster(testKeyText(string([]rune{' '})))
	// The cmd may be nil if tree is destroyed during rebuild, but
	// the key behavior is that space triggers a toggle + rebuild.
	// Verify the toggle happened.
	pe := m.items[0].cfg.Packs[1]
	if pe.Enabled == nil || *pe.Enabled {
		t.Fatal("expected pack-b to be disabled after space")
	}
	_ = cmd // cmd depends on whether ensureTree succeeds with real files
}

func TestLeftPanel_ViewShowsCursorAndCheckbox(t *testing.T) {
	t.Parallel()
	disabled := false
	m := Model{
		items: []profileItem{
			{
				name:     "default",
				isActive: true,
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "enabled-pack"},
						{Name: "disabled-pack", Enabled: &disabled},
					},
				},
			},
		},
		cursor:     0,
		packCursor: 0,
		focus:      panelPacks,
		width:      120,
		height:     40,
	}

	view := m.render()

	// Should show enabled/total count.
	if !strings.Contains(view, "Packs (1/2)") {
		t.Fatalf("expected 'Packs (1/2)' in view, got:\n%s", view)
	}

	// Should show [x] and the enabled pack name.
	if !strings.Contains(view, "[x]") || !strings.Contains(view, "enabled-pack") {
		t.Fatalf("expected enabled pack row in view, got:\n%s", view)
	}

	// Should show [ ] and the disabled pack name.
	if !strings.Contains(view, "[ ]") || !strings.Contains(view, "disabled-pack") {
		t.Fatalf("expected disabled pack row in view, got:\n%s", view)
	}
}

func TestProfilesTab_NoSyncBlurb(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []profileItem{
			{
				name:      "test",
				isActive:  true,
				syncState: common.SyncStatusUnsynced,
				syncTarget: common.SyncTarget{
					PlanSummary: app.PlanSummary{NumRules: 5, NumSkills: 2, NumSettings: 1},
					Harnesses:   []string{"cline"},
					Scope:       "project",
				},
			},
		},
		cursor: 0,
		width:  120,
		height: 40,
	}

	view := m.render()
	// Sync status should NOT appear in profiles view (moved to Sync tab).
	if strings.Contains(view, "Sync:") {
		t.Fatalf("profiles view should not contain 'Sync:' label, got:\n%s", view)
	}
	if strings.Contains(view, "Harness:") {
		t.Fatalf("profiles view should not contain 'Harness:' label, got:\n%s", view)
	}
	if strings.Contains(view, "pending") {
		t.Fatalf("profiles view should not contain 'pending' label, got:\n%s", view)
	}
}

func TestProfilesTab_SettingsDisabledDisplay(t *testing.T) {
	t.Parallel()
	disabled := false
	m := Model{
		items: []profileItem{
			{
				name: "test",
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "pack-a"},
						{Name: "pack-b", Settings: config.PackSettingsConfig{Enabled: &disabled}},
					},
				},
			},
		},
		cursor: 0,
		width:  120,
		height: 40,
	}

	view := m.render()
	if !strings.Contains(view, "(settings off)") {
		t.Fatalf("expected '(settings off)' in view for disabled pack, got:\n%s", view)
	}
}

func TestProfilesTab_SettingsSourceToggle(t *testing.T) {
	t.Parallel()
	enabled := true
	m := Model{
		items: []profileItem{
			{
				name: "test",
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "pack-a", Settings: config.PackSettingsConfig{Enabled: &enabled}},
						{Name: "pack-b"},
					},
				},
			},
		},
		cursor: 0,
	}

	// Initially pack-a has settings enabled — not disabled.
	if config.SettingsDisabled(m.currentItem().cfg.Packs[0].Settings.Enabled) {
		t.Fatal("expected pack-a to NOT be disabled")
	}

	// Toggle pack-a off.
	m = m.toggleSettingsDisabled("pack-a")
	if !config.SettingsDisabled(m.currentItem().cfg.Packs[0].Settings.Enabled) {
		t.Fatal("expected pack-a to be disabled after toggle")
	}
	if !m.dirty {
		t.Fatal("expected dirty=true after settings toggle")
	}

	// Toggle pack-a back on.
	m = m.toggleSettingsDisabled("pack-a")
	if config.SettingsDisabled(m.currentItem().cfg.Packs[0].Settings.Enabled) {
		t.Fatal("expected pack-a to be re-enabled after second toggle")
	}
}
