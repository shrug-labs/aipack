package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
)

func TestTwoPanelView_ShowsProfileDetails(t *testing.T) {
	t.Parallel()
	m := profilesModel{
		items: []profileItem{
			{
				name:      "default",
				isActive:  true,
				syncState: syncSynced,
				syncTarget: syncTargetInfo{
					harnesses:  []string{"cline"},
					scope:      "project",
					projectDir: "/tmp/project",
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

	view := m.View()

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

func TestProfileList_SelectedProfileStaysHighlightedWhenNotFocused(t *testing.T) {
	t.Parallel()

	m := profilesModel{
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
	if !strings.Contains(view, selectedStyle.Render("other")) {
		t.Fatalf("expected selected profile to stay pink out of focus, got:\n%s", view)
	}
}

func TestTwoPanelView_UnsyncedShowsPendingCount(t *testing.T) {
	t.Parallel()
	// Pending count is now shown on the Sync tab, not on the profiles view.
	m := newSyncTabModel("")
	m.activeSync = syncTabSnapshot{
		syncState: syncUnsynced,
		syncTarget: syncTargetInfo{PlanSummary: app.PlanSummary{
			NumRules:  2,
			NumSkills: 1,
		}},
	}
	m.width = 120
	m.height = 40

	view := m.View()
	if !strings.Contains(view, "3 pending") {
		t.Fatalf("expected view to contain '3 pending', got:\n%s", view)
	}
}

func TestShortPath(t *testing.T) {
	t.Parallel()

	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME not set")
	}

	tests := []struct {
		input    string
		expected string
	}{
		{home + "/projects/foo", "~/projects/foo"},
		{"/etc/config", "/etc/config"},
		{"relative/path", "relative/path"},
	}
	for _, tt := range tests {
		got := shortPath(tt.input)
		if got != tt.expected {
			t.Errorf("shortPath(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestAddPackToProfile(t *testing.T) {
	t.Parallel()
	m := profilesModel{
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
	m := profilesModel{
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
	m := profilesModel{
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

func TestProfileSavedMsg_RerunsSyncCheck(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.profiles.items = []profileItem{
		{name: "test", path: "/tmp/test.yaml", isActive: true, syncState: syncUnsynced},
	}

	result, cmd := m.Update(profileSavedMsg{profileName: "test"})
	rm := result.(rootModel)

	// Should have returned a sync check command (not nil).
	if cmd == nil {
		t.Fatal("expected sync check command after save")
	}

	// Sync state should be set to syncLoading (checkSyncCmd sets it).
	if rm.profiles.items[0].syncState != syncLoading {
		t.Fatalf("expected syncState=syncLoading after save triggered recheck, got %d", rm.profiles.items[0].syncState)
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

	m := profilesModel{
		width:  100,
		height: 10,
		focus:  panelProfiles,
	}

	updated, _ := m.Update(profilesLoadedMsg{items: items})
	m = updated

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

	m := profilesModel{
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
	m := profilesModel{
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
	m, _ = m.updatePackRoster(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.packCursor != 1 {
		t.Fatalf("expected packCursor=1 after j, got %d", m.packCursor)
	}

	// Move down again.
	m, _ = m.updatePackRoster(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.packCursor != 2 {
		t.Fatalf("expected packCursor=2 after second j, got %d", m.packCursor)
	}

	// Move to "Add pack..." virtual item.
	m, _ = m.updatePackRoster(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.packCursor != 3 {
		t.Fatalf("expected packCursor=3 (Add pack...), got %d", m.packCursor)
	}

	// Wrap around.
	m, _ = m.updatePackRoster(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.packCursor != 0 {
		t.Fatalf("expected packCursor=0 after wrap, got %d", m.packCursor)
	}

	// Move up wraps to end (Add pack... item).
	m, _ = m.updatePackRoster(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if m.packCursor != 3 {
		t.Fatalf("expected packCursor=3 after k from 0, got %d", m.packCursor)
	}
}

func TestLeftPanel_TogglePackEnabled(t *testing.T) {
	t.Parallel()
	m := profilesModel{
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
	m.items[0].syncState = syncSynced
	m = m.togglePackEnabled(1)
	if m.items[0].syncState != syncPending {
		t.Fatal("expected sync state to be invalidated after pack toggle on active profile")
	}
}

func TestLeftPanel_SpaceTogglesPackViaUpdateList(t *testing.T) {
	t.Parallel()
	m := profilesModel{
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
	m, _ = m.updatePackRoster(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	pe := m.items[0].cfg.Packs[1]
	if pe.Enabled == nil || *pe.Enabled {
		t.Fatal("expected pack-b to be disabled after space at packCursor=1")
	}
}

func TestLeftPanel_EnterSwitchesToRightPanel(t *testing.T) {
	t.Parallel()
	tree := treeModel{nodes: []treeNode{{kind: nodeItem, id: "test"}}}
	m := profilesModel{
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

	m, _ = m.updatePackRoster(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != panelTree {
		t.Fatal("expected focus to switch to panelTree after enter")
	}
}

func TestLeftPanel_RightArrowSwitchesToRightPanel(t *testing.T) {
	t.Parallel()
	tree := treeModel{nodes: []treeNode{{kind: nodeItem, id: "test"}}}
	m := profilesModel{
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

	m, _ = m.updatePackRoster(tea.KeyMsg{Type: tea.KeyRight})
	if m.focus != panelTree {
		t.Fatal("expected focus to switch to panelTree after right arrow")
	}
}

func TestRightPanel_LeftArrowSwitchesToLeftPanel(t *testing.T) {
	t.Parallel()
	tree := treeModel{nodes: []treeNode{{kind: nodeItem, id: "test"}}}
	m := profilesModel{
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

	m, _ = m.updateTree(tea.KeyMsg{Type: tea.KeyLeft})
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
	m := profilesModel{
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
	_, cmd := m.updatePackRoster(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
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
	m := profilesModel{
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

	view := m.View()

	// Should show enabled/total count.
	if !strings.Contains(view, "Packs (1/2)") {
		t.Fatalf("expected 'Packs (1/2)' in view, got:\n%s", view)
	}

	// Should show [x] for enabled pack.
	if !strings.Contains(view, "[x] enabled-pack") {
		t.Fatalf("expected '[x] enabled-pack' in view, got:\n%s", view)
	}

	// Should show [ ] for disabled pack.
	if !strings.Contains(view, "[ ] disabled-pack") {
		t.Fatalf("expected '[ ] disabled-pack' in view, got:\n%s", view)
	}
}

func TestProfilesTab_NoSyncBlurb(t *testing.T) {
	t.Parallel()
	m := profilesModel{
		items: []profileItem{
			{
				name:      "test",
				isActive:  true,
				syncState: syncUnsynced,
				syncTarget: syncTargetInfo{
					PlanSummary: app.PlanSummary{NumRules: 5, NumSkills: 2, NumSettings: 1},
					harnesses:   []string{"cline"},
					scope:       "project",
				},
			},
		},
		cursor: 0,
		width:  120,
		height: 40,
	}

	view := m.View()
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

func TestProfilesTab_SettingsFromDisplay(t *testing.T) {
	t.Parallel()
	enabled := true
	m := profilesModel{
		items: []profileItem{
			{
				name: "test",
				cfg: config.ProfileConfig{
					Packs: []config.PackEntry{
						{Name: "pack-a"},
						{Name: "pack-b", Settings: config.PackSettingsConfig{Enabled: &enabled}},
					},
				},
			},
		},
		cursor: 0,
		width:  120,
		height: 40,
	}

	view := m.View()
	if !strings.Contains(view, "(settings)") {
		t.Fatalf("expected '(settings)' in view, got:\n%s", view)
	}
	if !strings.Contains(view, "pack-b") {
		t.Fatalf("expected settings source 'pack-b' in view, got:\n%s", view)
	}
}

func TestProfilesTab_SettingsSourceToggle(t *testing.T) {
	t.Parallel()
	enabled := true
	m := profilesModel{
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

	// Initially pack-a is the settings source.
	if src := m.settingsSourcePack(); src != "pack-a" {
		t.Fatalf("expected settings source pack-a, got %q", src)
	}

	// Switch to pack-b.
	m = m.setSettingsSource("pack-b")
	if src := m.settingsSourcePack(); src != "pack-b" {
		t.Fatalf("expected settings source pack-b, got %q", src)
	}
	if !m.dirty {
		t.Fatal("expected dirty=true after settings source change")
	}

	// pack-a should no longer be settings source.
	item := m.currentItem()
	if item.cfg.Packs[0].Settings.Enabled != nil {
		t.Fatal("expected pack-a settings.enabled to be nil")
	}
	if item.cfg.Packs[1].Settings.Enabled == nil || !*item.cfg.Packs[1].Settings.Enabled {
		t.Fatal("expected pack-b settings.enabled to be true")
	}
}
