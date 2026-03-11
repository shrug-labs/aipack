package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
)

// testTree builds a single-pack tree for testing convenience.
func testTree(manifest config.PackManifest, entry config.PackEntry) treeModel {
	packs := []packInfo{{idx: 0, name: entry.Name, root: "/tmp/pack", manifest: manifest}}
	return buildMultiPackTree(packs, []config.PackEntry{entry})
}

func TestRootModel_TabSwitching(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})

	if m.activeTab != tabProfiles {
		t.Fatalf("expected initial tab = profiles, got %d", m.activeTab)
	}

	// Tab key is Type: tea.KeyTab.
	m.activeTab = tabProfiles
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = result.(rootModel)
	if m.activeTab != tabPacks {
		t.Fatalf("expected tab to switch to packs, got %d", m.activeTab)
	}

	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = result.(rootModel)
	if m.activeTab != tabSave {
		t.Fatalf("expected tab to switch to save, got %d", m.activeTab)
	}

	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = result.(rootModel)
	if m.activeTab != tabSync {
		t.Fatalf("expected tab to switch to sync, got %d", m.activeTab)
	}

	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = result.(rootModel)
	if m.activeTab != tabSearch {
		t.Fatalf("expected tab to switch to search, got %d", m.activeTab)
	}

	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = result.(rootModel)
	if m.activeTab != tabProfiles {
		t.Fatalf("expected tab to wrap back to profiles, got %d", m.activeTab)
	}
}

func TestRootModel_QuitKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  tea.KeyMsg
	}{
		{"q", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}},
		{"ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newRootModel(RunConfig{})
			result, cmd := m.Update(tt.key)
			rm := result.(rootModel)
			if !rm.quitting {
				t.Fatalf("expected quitting=true after %s", tt.name)
			}
			if cmd == nil {
				t.Fatalf("expected tea.Quit cmd after %s", tt.name)
			}
		})
	}
}

func TestRootModel_EscWithDirtyAutoSaves(t *testing.T) {
	t.Parallel()
	tree := testTree(
		config.PackManifest{Rules: []string{"rule-a"}},
		config.PackEntry{Name: "test-pack"},
	)
	m := newRootModel(RunConfig{})
	m.dirty = true
	m.profiles.dirty = true
	m.profiles.items = []profileItem{
		{
			name:  "test",
			path:  "/tmp/test.yaml",
			cfg:   config.ProfileConfig{Packs: []config.PackEntry{{Name: "test-pack"}}},
			tree:  &tree,
			dirty: true,
		},
	}

	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	rm := result.(rootModel)

	// Should auto-save (no dialog), pending exit.
	if rm.dialog != nil {
		t.Fatal("expected no dialog — auto-save should be implicit")
	}
	if !rm.pendingExit {
		t.Fatal("expected pendingExit=true for auto-save")
	}
	if cmd == nil {
		t.Fatal("expected save command")
	}
}

func TestRootModel_EscWithoutDirtyQuits(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.dirty = false
	// No unsynced profiles → should quit directly.
	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	rm := result.(rootModel)

	if !rm.quitting {
		t.Fatal("expected quitting=true on esc without dirty")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd")
	}
}

func TestRootModel_HelpTextChangesWithContext(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})

	// Default: profile list (panelProfiles).
	help := m.helpText()
	if !strings.Contains(help, "enter:packs") {
		t.Fatalf("expected profile list help to mention enter:packs, got %q", help)
	}
	if !strings.Contains(help, ".:actions") {
		t.Fatalf("expected profile list help to mention .:actions, got %q", help)
	}
	if !strings.Contains(help, "s:sync") {
		t.Fatalf("expected profile list help to mention s:sync, got %q", help)
	}

	// Pack roster (panelPacks).
	m.profiles.focus = panelPacks
	help = m.helpText()
	if !strings.Contains(help, "space:toggle") {
		t.Fatalf("expected pack roster help to mention space:toggle, got %q", help)
	}
	if !strings.Contains(help, "enter:tree") {
		t.Fatalf("expected pack roster help to mention enter:tree, got %q", help)
	}
	if !strings.Contains(help, "esc:back") {
		t.Fatalf("expected pack roster help to mention esc:back, got %q", help)
	}

	// Content tree (panelTree).
	m.profiles.focus = panelTree
	help = m.helpText()
	if !strings.Contains(help, "space:toggle") {
		t.Fatalf("expected tree mode help to mention space:toggle, got %q", help)
	}
	if !strings.Contains(help, "esc:back") {
		t.Fatalf("expected tree mode help to mention esc:back, got %q", help)
	}

	// Packs tab.
	m.activeTab = tabPacks
	help = m.helpText()
	if !strings.Contains(help, "j/k:navigate") {
		t.Fatalf("expected packs tab help to mention j/k:navigate, got %q", help)
	}
	if !strings.Contains(help, ".:actions") {
		t.Fatalf("expected packs tab help to mention .:actions, got %q", help)
	}
}

func TestRootModel_PacksLoadedWhileProfilesTabActive(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	if m.activeTab != tabProfiles {
		t.Fatal("expected initial tab to be profiles")
	}

	result, _ := m.Update(packsLoadedMsg{
		items: []packItemDetail{
			{entry: app.PackShowEntry{Name: "test-pack", Version: "1.0"}},
		},
	})
	rm := result.(rootModel)
	if len(rm.packs.items) != 1 {
		t.Fatalf("expected packs to receive message, got %d items", len(rm.packs.items))
	}
	if rm.packs.items[0].entry.Name != "test-pack" {
		t.Fatalf("expected pack name 'test-pack', got %q", rm.packs.items[0].entry.Name)
	}
}

func TestRootModel_DialogResultNotSwallowed(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	d := newConfirmDialog(dialogSaveOnExit, "Save changes?")
	m.dialog = &d

	// Dialog result should be handled, not swallowed.
	result, cmd := m.Update(dialogResultMsg{id: dialogSaveOnExit, confirmed: false})
	rm := result.(rootModel)
	if rm.dialog != nil {
		t.Fatal("expected dialog to be cleared after result message")
	}
	// save-on-exit is a legacy dialog ID that just quits now.
	if !rm.quitting {
		t.Fatal("expected quitting=true")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd")
	}
}

func TestRootModel_ProfileSavedClearsDirty(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.dirty = true
	m.profiles.dirty = true
	m.profiles.items = []profileItem{
		{name: "test", path: "/tmp/test.yaml"},
	}

	result, _ := m.Update(profileSavedMsg{profileName: "test"})
	rm := result.(rootModel)
	if rm.dirty {
		t.Fatal("expected dirty=false after successful save")
	}
	if rm.profiles.dirty {
		t.Fatal("expected profiles.dirty=false after successful save")
	}

	// Verify dirty does NOT reappear when a subsequent message is processed.
	result2, _ := rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	rm2 := result2.(rootModel)
	if rm2.dirty {
		t.Fatal("dirty should remain false after subsequent key message")
	}
}

func TestRootModel_ProfileSavedClearsDirty_MultipleProfiles(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.dirty = true
	m.profiles.dirty = true
	m.profiles.items = []profileItem{
		{name: "alpha", path: "/tmp/alpha.yaml", dirty: true},
		{name: "beta", path: "/tmp/beta.yaml", dirty: true},
	}

	// Save alpha — beta is still dirty, so global dirty must stay true.
	result, _ := m.Update(profileSavedMsg{profileName: "alpha"})
	rm := result.(rootModel)
	if !rm.dirty {
		t.Fatal("expected dirty=true while beta is still unsaved")
	}
	if !rm.profiles.dirty {
		t.Fatal("expected profiles.dirty=true while beta is still unsaved")
	}
	if rm.profiles.items[0].dirty {
		t.Fatal("expected alpha.dirty=false after save")
	}

	// Save beta — now all profiles are clean.
	result2, _ := rm.Update(profileSavedMsg{profileName: "beta"})
	rm2 := result2.(rootModel)
	if rm2.dirty {
		t.Fatal("expected dirty=false after all profiles saved")
	}
	if rm2.profiles.dirty {
		t.Fatal("expected profiles.dirty=false after all profiles saved")
	}
}

func TestRootModel_ProfileSaveFailKeepsDirty(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.dirty = true
	m.profiles.dirty = true

	result, _ := m.Update(profileSavedMsg{
		profileName: "test",
		err:         fmt.Errorf("disk full"),
	})
	rm := result.(rootModel)
	if !rm.dirty {
		t.Fatal("expected dirty=true when save failed")
	}
	if !rm.profiles.dirty {
		t.Fatal("expected profiles.dirty=true when save failed")
	}
}

func TestDialogModel_ConfirmYes(t *testing.T) {
	t.Parallel()
	d := newConfirmDialog("test", "Confirm?")

	// Press y.
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	_ = d
	if cmd == nil {
		t.Fatal("expected cmd after pressing y")
	}

	msg := cmd()
	result, ok := msg.(dialogResultMsg)
	if !ok {
		t.Fatalf("expected dialogResultMsg, got %T", msg)
	}
	if !result.confirmed {
		t.Fatal("expected confirmed=true after pressing y")
	}
}

func TestDialogModel_ConfirmNo(t *testing.T) {
	t.Parallel()
	d := newConfirmDialog("test", "Confirm?")

	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	_ = d
	if cmd == nil {
		t.Fatal("expected cmd after pressing n")
	}

	msg := cmd()
	result, ok := msg.(dialogResultMsg)
	if !ok {
		t.Fatalf("expected dialogResultMsg, got %T", msg)
	}
	if result.confirmed {
		t.Fatal("expected confirmed=false after pressing n")
	}
}

func TestDialogHelpText(t *testing.T) {
	t.Parallel()

	// Confirm dialog.
	d := newConfirmDialog("test", "Confirm?")
	help := d.helpText()
	if help != "enter:confirm  esc:cancel" {
		t.Fatalf("expected default help for confirm, got %q", help)
	}

	// Plain list select dialog.
	d2 := newListSelectDialog("test", "Select:", []string{"a", "b"})
	help2 := d2.helpText()
	if !strings.Contains(help2, "enter:select") {
		t.Fatalf("expected 'enter:select' in list help, got %q", help2)
	}

	// List select dialog with actions.
	d3 := newListSelectDialog("test", "Select:", []string{"a", "b"})
	d3.listActions = []listAction{
		{key: "a", name: "activate"},
		{key: "d", name: "delete"},
	}
	help3 := d3.helpText()
	if !strings.Contains(help3, "a:activate") {
		t.Fatalf("expected 'a:activate' in help, got %q", help3)
	}
	if !strings.Contains(help3, "d:delete") {
		t.Fatalf("expected 'd:delete' in help, got %q", help3)
	}
}

func TestPromptSync_ShowsDialog(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.profiles.items = []profileItem{
		{name: "test", syncState: syncSynced},
	}

	result, _ := m.promptSync()
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected sync dialog")
	}
	if rm.dialog.id != dialogSyncOnExit {
		t.Fatalf("expected sync-on-exit dialog, got %q", rm.dialog.id)
	}
}

func TestPromptSync_UnsyncedProfileShowsPendingCount(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.profiles.items = []profileItem{
		{
			name:      "default",
			syncState: syncUnsynced,
			syncTarget: syncTargetInfo{
				PlanSummary: app.PlanSummary{NumWrites: 2, NumCopies: 1},
				harnesses:   []string{"cline"},
				scope:       "project",
			},
		},
	}

	result, _ := m.promptSync()
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected sync dialog for unsynced profile")
	}
	if rm.dialog.id != dialogSyncOnExit {
		t.Fatalf("expected sync-on-exit dialog, got %q", rm.dialog.id)
	}
	if len(rm.dialog.listItems) != 3 {
		t.Fatalf("expected 3 options, got %d", len(rm.dialog.listItems))
	}
	// First option should contain harness/scope info.
	if !strings.Contains(rm.dialog.listItems[0], "cline") {
		t.Fatalf("expected first option to mention cline, got %q", rm.dialog.listItems[0])
	}
	if rm.dialog.listItems[1] != "Customize..." {
		t.Fatalf("expected second option to be 'Customize...', got %q", rm.dialog.listItems[1])
	}
	if rm.dialog.listItems[2] != "Cancel" {
		t.Fatalf("expected third option to be 'Cancel', got %q", rm.dialog.listItems[2])
	}
	// Title should mention pending changes.
	if !strings.Contains(rm.dialog.title, "3 pending") {
		t.Fatalf("expected title to mention pending changes, got %q", rm.dialog.title)
	}
}

func TestSyncOnExitDialog_CancelReturnsToTUI(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.pendingExit = true
	m.profiles.items = []profileItem{
		{name: "test", syncState: syncUnsynced},
	}

	result, cmd := m.handleDialogResult(dialogResultMsg{
		id: dialogSyncOnExit, confirmed: true, value: "Cancel",
	})
	rm := result.(rootModel)
	if rm.quitting {
		t.Fatal("expected quitting=false after cancel")
	}
	if rm.pendingExit {
		t.Fatal("expected pendingExit=false after cancel")
	}
	if cmd != nil {
		t.Fatal("expected no cmd after cancel")
	}
	if rm.runResult.SyncRequested {
		t.Fatal("expected SyncRequested=false after cancel")
	}
}

func TestSyncOnExitDialog_DefaultSyncFiresCmd(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.profiles.items = []profileItem{
		{
			name: "default",
			path: "/tmp/default.yaml",
			syncTarget: syncTargetInfo{
				harnesses: []string{"cline"},
				scope:     "project",
			},
		},
	}

	result, cmd := m.handleDialogResult(dialogResultMsg{
		id: dialogSyncOnExit, confirmed: true, value: "Sync (cline, project)",
	})
	rm := result.(rootModel)
	if rm.quitting {
		t.Fatal("expected quitting=false — sync runs inline")
	}
	if cmd == nil {
		t.Fatal("expected async sync cmd")
	}
	// Sync status should be loading.
	if rm.profiles.items[0].syncState != syncLoading {
		t.Fatalf("expected syncState=syncLoading, got %d", rm.profiles.items[0].syncState)
	}
}

func TestSyncOnExitDialog_CustomizeChainsScopeDialog(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})

	result, _ := m.handleDialogResult(dialogResultMsg{
		id: dialogSyncOnExit, confirmed: true, value: "Customize...",
	})
	rm := result.(rootModel)
	if rm.quitting {
		t.Fatal("expected not quitting after Customize")
	}
	if rm.dialog == nil {
		t.Fatal("expected sync-scope dialog")
	}
	if rm.dialog.id != dialogSyncScope {
		t.Fatalf("expected sync-scope dialog, got %q", rm.dialog.id)
	}
}

func TestSyncScopeDialog_ChainsHarnessDialog(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})

	result, _ := m.handleDialogResult(dialogResultMsg{
		id: dialogSyncScope, confirmed: true, value: "project",
	})
	rm := result.(rootModel)
	if rm.exitSyncScope != "project" {
		t.Fatalf("expected exitSyncScope=project, got %q", rm.exitSyncScope)
	}
	if rm.dialog == nil {
		t.Fatal("expected sync-harness dialog")
	}
	if rm.dialog.id != dialogSyncHarness {
		t.Fatalf("expected sync-harness dialog, got %q", rm.dialog.id)
	}
}

func TestSyncHarnessDialog_FiresSyncCmd(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.exitSyncScope = "global"
	m.profiles.items = []profileItem{
		{name: "custom", path: "/tmp/custom.yaml"},
	}

	result, cmd := m.handleDialogResult(dialogResultMsg{
		id: dialogSyncHarness, confirmed: true, value: "opencode",
	})
	rm := result.(rootModel)
	if rm.quitting {
		t.Fatal("expected quitting=false — sync runs inline")
	}
	if cmd == nil {
		t.Fatal("expected async sync cmd")
	}
	if rm.profiles.items[0].syncState != syncLoading {
		t.Fatalf("expected syncState=syncLoading, got %d", rm.profiles.items[0].syncState)
	}
}

func TestProfileSavedMsg_MarksUnsynced(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.profiles.items = []profileItem{
		{name: "test", path: "/tmp/test.yaml", syncState: syncSynced},
	}

	result, _ := m.Update(profileSavedMsg{profileName: "test"})
	rm := result.(rootModel)
	if rm.profiles.items[0].syncState != syncUnsynced {
		t.Fatalf("expected syncState=syncUnsynced after save, got %d", rm.profiles.items[0].syncState)
	}
}

func TestPendingExitFlow_AutoSaveThenQuit(t *testing.T) {
	t.Parallel()
	tree := testTree(
		config.PackManifest{Rules: []string{"rule-a"}},
		config.PackEntry{Name: "test-pack"},
	)

	m := newRootModel(RunConfig{})
	m.dirty = true
	m.profiles.dirty = true
	m.profiles.items = []profileItem{
		{
			name:      "test",
			path:      "/tmp/test.yaml",
			syncState: syncSynced,
			cfg:       config.ProfileConfig{Packs: []config.PackEntry{{Name: "test-pack"}}},
			tree:      &tree,
			dirty:     true,
		},
	}

	// startExit with dirty state → auto-save + pending exit.
	result, cmd := m.startExit()
	rm := result.(rootModel)
	if !rm.pendingExit {
		t.Fatal("expected pendingExit=true after startExit with dirty state")
	}
	if cmd == nil {
		t.Fatal("expected save command")
	}

	// Simulate profileSavedMsg arriving → should quit (no sync prompt on exit).
	result2, cmd2 := rm.Update(profileSavedMsg{profileName: "test"})
	rm2 := result2.(rootModel)
	if !rm2.quitting {
		t.Fatal("expected quitting=true after save completes during exit")
	}
	if cmd2 == nil {
		t.Fatal("expected tea.Quit cmd")
	}
}

func TestSyncFlow_SaveThenPrompt(t *testing.T) {
	t.Parallel()
	tree := testTree(
		config.PackManifest{Rules: []string{"rule-a"}},
		config.PackEntry{Name: "test-pack"},
	)

	m := newRootModel(RunConfig{})
	m.dirty = true
	m.profiles.dirty = true
	m.profiles.items = []profileItem{
		{
			name:      "test",
			path:      "/tmp/test.yaml",
			syncState: syncUnsynced,
			cfg:       config.ProfileConfig{Packs: []config.PackEntry{{Name: "test-pack"}}},
			tree:      &tree,
			dirty:     true,
			syncTarget: syncTargetInfo{
				PlanSummary: app.PlanSummary{NumWrites: 3},
				harnesses:   []string{"cline"},
				scope:       "project",
			},
		},
	}

	// startSync with dirty state → auto-save + pending sync.
	result, cmd := m.startSync()
	rm := result.(rootModel)
	if !rm.pendingExit {
		t.Fatal("expected pendingExit=true")
	}
	if !rm.pendingSync {
		t.Fatal("expected pendingSync=true")
	}
	if cmd == nil {
		t.Fatal("expected save command")
	}

	// Simulate profileSavedMsg → should show sync prompt (not quit).
	result2, _ := rm.Update(profileSavedMsg{profileName: "test"})
	rm2 := result2.(rootModel)
	if rm2.quitting {
		t.Fatal("expected quitting=false, should show sync dialog")
	}
	if rm2.dialog == nil {
		t.Fatal("expected sync dialog after save")
	}
	if rm2.dialog.id != dialogSyncOnExit {
		t.Fatalf("expected sync-on-exit dialog, got %q", rm2.dialog.id)
	}
}

func TestEnrichedSyncStatus(t *testing.T) {
	t.Parallel()
	m := profilesModel{
		items: []profileItem{
			{name: "test"},
		},
		cursor: 0,
	}

	target := syncTargetInfo{
		PlanSummary: app.PlanSummary{NumWrites: 5, NumCopies: 3, NumSettings: 2},
		harnesses:   []string{"cline", "opencode"},
		scope:       "project",
		projectDir:  "/tmp/project",
	}

	m, _ = m.Update(syncStatusMsg{
		profileName: "test",
		synced:      false,
		target:      target,
	})

	if m.items[0].syncState != syncUnsynced {
		t.Fatalf("expected syncUnsynced, got %d", m.items[0].syncState)
	}
	if m.items[0].syncTarget.TotalChanges() != 10 {
		t.Fatalf("expected 10 total changes, got %d", m.items[0].syncTarget.TotalChanges())
	}
	if len(m.items[0].syncTarget.harnesses) != 2 {
		t.Fatalf("expected 2 harnesses, got %d", len(m.items[0].syncTarget.harnesses))
	}
}

func TestCountPendingSaves(t *testing.T) {
	t.Parallel()

	m := newRootModel(RunConfig{})
	m.profiles.items = []profileItem{
		{name: "a", dirty: true},
		{name: "b"},
		{name: "c", dirty: true},
	}

	count := m.countPendingSaves()
	if count != 2 {
		t.Fatalf("expected 2 pending saves, got %d", count)
	}
}

func TestRunResult_DefaultValues(t *testing.T) {
	t.Parallel()
	r := RunResult{}
	if r.SyncRequested {
		t.Fatal("expected SyncRequested=false by default")
	}
	if r.ProfileName != "" {
		t.Fatal("expected empty ProfileName by default")
	}
}

func TestActionMenu_ProfileActions(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.profiles.items = []profileItem{
		{name: "default", isActive: true},
		{name: "staging", isActive: false},
	}
	m.profiles.cursor = 0

	// Press "." on profiles tab → should open action menu.
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(".")})
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected action dialog")
	}
	if rm.dialog.id != dialogActionProfile {
		t.Fatalf("expected %s dialog, got %q", dialogActionProfile, rm.dialog.id)
	}
	// Active profile should not have "Activate" option.
	for _, item := range rm.dialog.listItems {
		if item == actActivate {
			t.Fatal("expected no 'Activate' option for already-active profile")
		}
	}
}

func TestActionMenu_ProfileActionsInactive(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.profiles.items = []profileItem{
		{name: "default", isActive: true},
		{name: "staging", isActive: false},
	}
	m.profiles.cursor = 1 // inactive profile

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(".")})
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected action dialog")
	}
	// Inactive profile should have "Activate" option.
	found := false
	for _, item := range rm.dialog.listItems {
		if item == actActivate {
			found = true
		}
	}
	if !found {
		t.Fatal("expected 'Activate' option for inactive profile")
	}
}

func TestActionMenu_NewProfileChain(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.profiles.items = []profileItem{{name: "default"}}

	// Simulate selecting "New profile" from the action menu.
	result, _ := m.handleDialogResult(dialogResultMsg{
		id: dialogActionProfile, confirmed: true, value: actNewProfile,
	})
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected new-profile text input dialog")
	}
	if rm.dialog.id != dialogNewProfile {
		t.Fatalf("expected %s dialog, got %q", dialogNewProfile, rm.dialog.id)
	}
}

func TestActionMenu_DeleteChain(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.profiles.items = []profileItem{
		{name: "staging", path: "/tmp/staging.yaml"},
	}

	result, _ := m.handleDialogResult(dialogResultMsg{
		id: dialogActionProfile, confirmed: true, value: actDelete,
	})
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected delete confirm dialog")
	}
	if rm.dialog.id != dialogDeleteProfile {
		t.Fatalf("expected %s dialog, got %q", dialogDeleteProfile, rm.dialog.id)
	}
}

func TestSyncErrorDisplay(t *testing.T) {
	t.Parallel()
	// Sync error is now displayed on the Sync tab, not the profiles view.
	m := newSyncTabModel("")
	m.activeSync = syncTabSnapshot{
		syncState:   syncError,
		syncErrText: "harness not found",
	}
	m.width = 120
	m.height = 40

	view := m.View()
	if !strings.Contains(view, "error") {
		t.Fatalf("expected view to contain 'error', got:\n%s", view)
	}
	if !strings.Contains(view, "harness not found") {
		t.Fatalf("expected view to contain error text 'harness not found', got:\n%s", view)
	}
}

func TestDeleteCancel_ClosesDialog(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.profiles.items = []profileItem{
		{name: "default", path: "/tmp/default.yaml"},
	}

	result, _ := m.handleDialogResult(dialogResultMsg{
		id: dialogDeleteProfile, confirmed: false,
	})
	rm := result.(rootModel)

	if rm.dialog != nil {
		t.Fatal("expected no dialog after cancel")
	}
}

func TestNewCancel_ClosesDialog(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})

	result, _ := m.handleDialogResult(dialogResultMsg{
		id: dialogNewProfile, confirmed: false,
	})
	rm := result.(rootModel)

	if rm.dialog != nil {
		t.Fatal("expected no dialog after cancel")
	}
}

func TestRootModel_PreviewRequestOpensOverlay(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.width = 120
	m.height = 40

	result, cmd := m.Update(previewRequestMsg{
		title:    "rule-a",
		category: CatRules,
		packName: "test-pack",
		filePath: "/tmp/pack/rules/rule-a.md",
	})
	rm := result.(rootModel)
	if rm.preview == nil {
		t.Fatal("expected preview to be non-nil")
	}
	if cmd == nil {
		t.Fatal("expected loadPreview cmd")
	}
}

func TestRootModel_EscClosesPreview(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.width = 120
	m.height = 40
	p := newPreviewModel(120, 40)
	p.setContent(previewLoadedMsg{
		title:    "rule-a",
		category: CatRules,
		filePath: "/tmp/pack/rules/rule-a.md",
		body:     "test body",
	})
	m.preview = &p

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	rm := result.(rootModel)
	if rm.preview != nil {
		t.Fatal("expected preview to be nil after esc")
	}
	if rm.quitting {
		t.Fatal("expected quitting=false — esc from preview should close preview, not quit")
	}
}

func TestRootModel_QClosesPreview(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.width = 120
	m.height = 40
	p := newPreviewModel(120, 40)
	p.setContent(previewLoadedMsg{
		title: "rule-a", category: CatRules,
		filePath: "/tmp/pack/rules/rule-a.md", body: "test",
	})
	m.preview = &p

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	rm := result.(rootModel)
	if rm.preview != nil {
		t.Fatal("expected preview to be nil after q")
	}
	if rm.quitting {
		t.Fatal("expected quitting=false — q from preview should close preview, not quit")
	}
}

func TestRootModel_PreviewViewTakesOver(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.width = 80
	m.height = 40
	p := newPreviewModel(80, 40)
	p.setContent(previewLoadedMsg{
		title:    "rule-a",
		category: CatRules,
		filePath: "/tmp/pack/rules/rule-a.md",
		body:     "# Hello World",
	})
	m.preview = &p

	view := m.View()
	// Preview should take over — no tab bar.
	if strings.Contains(view, "Profiles") {
		t.Fatal("expected preview to replace tab bar")
	}
	if !strings.Contains(view, "Hello World") {
		t.Fatal("expected preview body in view")
	}
	if !strings.Contains(view, "e:edit") {
		t.Fatal("expected preview help text in view")
	}
}

func TestHelpText_VPlanOnProfilesAndSync(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})

	m.activeTab = tabProfiles
	help := m.helpText()
	if !strings.Contains(help, "v:plan") {
		t.Fatalf("expected profiles help to contain 'v:plan', got %q", help)
	}

	m.activeTab = tabSync
	help = m.helpText()
	if !strings.Contains(help, "v:plan") {
		t.Fatalf("expected sync help to contain 'v:plan', got %q", help)
	}

	m.activeTab = tabPacks
	help = m.helpText()
	if strings.Contains(help, "v:plan") {
		t.Fatalf("expected packs help NOT to contain 'v:plan', got %q", help)
	}
}
