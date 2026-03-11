package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
)

func TestSyncTargetInfo_TotalChanges(t *testing.T) {
	t.Parallel()
	target := syncTargetInfo{PlanSummary: app.PlanSummary{
		NumWrites:   5,
		NumCopies:   3,
		NumSettings: 2,
	}}
	if got := target.TotalChanges(); got != 10 {
		t.Fatalf("expected totalChanges=10, got %d", got)
	}
}

func TestSyncTab_CursorNavigation(t *testing.T) {
	t.Parallel()
	m := newSyncTabModel("")
	m.width = 120
	m.height = 40
	fc := m.fieldCount()

	// Navigate down through all fields.
	for i := 0; i < fc; i++ {
		if m.cursor != i {
			t.Fatalf("expected cursor=%d, got %d", i, m.cursor)
		}
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	// Should wrap back to 0.
	if m.cursor != 0 {
		t.Fatalf("expected cursor to wrap to 0, got %d", m.cursor)
	}

	// Navigate up from 0 should wrap to end.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if m.cursor != fc-1 {
		t.Fatalf("expected cursor to wrap to %d, got %d", fc-1, m.cursor)
	}
}

func TestSyncTab_HarnessToggle(t *testing.T) {
	t.Parallel()
	m := newSyncTabModel("/tmp/config")
	m.syncCfg.Defaults.Harnesses = []string{"cline"}
	m.width = 120
	m.height = 40

	// Move cursor to first harness (index 1).
	m.cursor = 1

	// Toggle harness — should emit a syncToggleHarnessMsg.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if cmd == nil {
		t.Fatal("expected command after harness toggle")
	}
	msg := cmd()
	togMsg, ok := msg.(syncToggleHarnessMsg)
	if !ok {
		t.Fatalf("expected syncToggleHarnessMsg, got %T", msg)
	}
	if togMsg.harness != m.allHarnesses[0] {
		t.Fatalf("expected harness=%q, got %q", m.allHarnesses[0], togMsg.harness)
	}
}

func TestSyncTab_HarnessToggle_EndToEnd(t *testing.T) {
	t.Parallel()
	rm := newRootModel(RunConfig{ConfigDir: "/tmp/cfg"})
	rm.cfg.SyncCfg.Defaults.Harnesses = []string{"cline"}
	rm.profiles.items = []profileItem{{name: "test", isActive: true, syncState: syncSynced}}

	// rootModel handles the intent message and mutates its SyncCfg.
	result, _ := rm.Update(syncToggleHarnessMsg{harness: "cline"})
	rm = result.(rootModel)
	for _, h := range rm.cfg.SyncCfg.Defaults.Harnesses {
		if h == "cline" {
			t.Fatal("expected cline to be removed from harnesses")
		}
	}

	// Toggle back on.
	result, _ = rm.Update(syncToggleHarnessMsg{harness: "cline"})
	rm = result.(rootModel)
	found := false
	for _, h := range rm.cfg.SyncCfg.Defaults.Harnesses {
		if h == "cline" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected cline to be added back to harnesses")
	}
}

func TestSyncTab_ScopeToggle(t *testing.T) {
	t.Parallel()
	m := newSyncTabModel("/tmp/config")
	m.syncCfg.Defaults.Scope = "project"
	m.width = 120
	m.height = 40

	// Move to scope field (last field).
	m.cursor = m.fieldCount() - 1

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if cmd == nil {
		t.Fatal("expected command after scope toggle")
	}
	msg := cmd()
	if _, ok := msg.(syncCycleScopeMsg); !ok {
		t.Fatalf("expected syncCycleScopeMsg, got %T", msg)
	}
}

func TestSyncTab_ScopeToggle_EndToEnd(t *testing.T) {
	t.Parallel()
	rm := newRootModel(RunConfig{ConfigDir: "/tmp/cfg"})
	rm.cfg.SyncCfg.Defaults.Scope = "project"
	rm.profiles.items = []profileItem{{name: "test", isActive: true, syncState: syncSynced}}

	result, _ := rm.Update(syncCycleScopeMsg{})
	rm = result.(rootModel)
	if rm.cfg.SyncCfg.Defaults.Scope != "global" {
		t.Fatalf("expected scope=global, got %q", rm.cfg.SyncCfg.Defaults.Scope)
	}

	// Toggle back.
	result, _ = rm.Update(syncCycleScopeMsg{})
	rm = result.(rootModel)
	if rm.cfg.SyncCfg.Defaults.Scope != "project" {
		t.Fatalf("expected scope=project, got %q", rm.cfg.SyncCfg.Defaults.Scope)
	}
}

func TestSyncTab_ViewShowsConfig(t *testing.T) {
	t.Parallel()
	m := newSyncTabModel("/tmp/config")
	m.syncCfg.Defaults.Profile = "myprofile"
	m.syncCfg.Defaults.Harnesses = []string{"cline", "opencode"}
	m.syncCfg.Defaults.Scope = "project"
	m.width = 120
	m.height = 40

	view := m.View()
	if !strings.Contains(view, "myprofile") {
		t.Fatalf("expected view to contain profile name, got:\n%s", view)
	}
	if !strings.Contains(view, "[x] cline") {
		t.Fatalf("expected view to show cline checked, got:\n%s", view)
	}
	if !strings.Contains(view, "[x] opencode") {
		t.Fatalf("expected view to show opencode checked, got:\n%s", view)
	}
	if !strings.Contains(view, "project") {
		t.Fatalf("expected view to show scope project, got:\n%s", view)
	}
}

func TestSyncTab_ViewShowsStatus(t *testing.T) {
	t.Parallel()
	m := newSyncTabModel("")
	m.activeSync = syncTabSnapshot{
		syncState: syncSynced,
		syncTarget: syncTargetInfo{
			PlanSummary: app.PlanSummary{
				NumWrites:   2,
				NumCopies:   1,
				LedgerPath:  "/tmp/.aipack/ledger.json",
				LedgerFiles: 42,
			},
			harnesses:  []string{"cline"},
			scope:      "project",
			ledgerTime: 1740787815,
		},
	}
	m.width = 120
	m.height = 40

	view := m.View()
	if !strings.Contains(view, "up to date") {
		t.Fatalf("expected 'up to date' status, got:\n%s", view)
	}
	if !strings.Contains(view, "Writes:    2") {
		t.Fatalf("expected 'Writes:    2', got:\n%s", view)
	}
	if !strings.Contains(view, "Copies:    1") {
		t.Fatalf("expected 'Copies:    1', got:\n%s", view)
	}
	if !strings.Contains(view, "Total:     3") {
		t.Fatalf("expected 'Total:     3', got:\n%s", view)
	}
	if !strings.Contains(view, "42 managed") {
		t.Fatalf("expected '42 managed', got:\n%s", view)
	}
	if !strings.Contains(view, "Ledger:") {
		t.Fatalf("expected 'Ledger:' section, got:\n%s", view)
	}
}

func TestSyncTab_StatusDotOnTab(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.profiles.items = []profileItem{{name: "test", isActive: true}}

	// Default: syncPending — no dot.
	names := m.tabNames(syncPending)
	syncIdx := 3 // Sync is tab index 3 (Profiles=0, Packs=1, Save=2, Sync=3, Search=4)
	if names[syncIdx] != "Sync" {
		t.Fatalf("expected plain 'Sync' when pending, got %q", names[syncIdx])
	}

	// Synced — green dot.
	names = m.tabNames(syncSynced)
	if !strings.Contains(names[syncIdx], "●") {
		t.Fatalf("expected green dot for synced, got %q", names[syncIdx])
	}

	// Unsynced — red dot.
	names = m.tabNames(syncUnsynced)
	if !strings.Contains(names[syncIdx], "○") {
		t.Fatalf("expected red dot for unsynced, got %q", names[syncIdx])
	}

	// Loading — spinner.
	names = m.tabNames(syncLoading)
	if !strings.Contains(names[syncIdx], "⟳") {
		t.Fatalf("expected loading indicator, got %q", names[syncIdx])
	}
}

func TestSyncTab_ProfilesLoadedUpdatesNames(t *testing.T) {
	t.Parallel()
	m := newSyncTabModel("")

	msg := profilesLoadedMsg{
		items: []profileItem{
			{name: "alpha"},
			{name: "beta"},
		},
	}
	m, _ = m.Update(msg)

	if len(m.profileNames) != 2 {
		t.Fatalf("expected 2 profile names, got %d", len(m.profileNames))
	}
	if m.profileNames[0] != "alpha" || m.profileNames[1] != "beta" {
		t.Fatalf("expected [alpha, beta], got %v", m.profileNames)
	}
}

func TestSyncTab_SnapshotDerivedFromActiveProfile(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.profiles.items = []profileItem{
		{name: "test", isActive: true, syncState: syncSynced, syncTarget: syncTargetInfo{
			harnesses: []string{"cline"},
			scope:     "project",
		}},
	}

	snap := m.activeSyncSnapshot()
	if snap.syncState != syncSynced {
		t.Fatalf("expected syncSynced, got %d", snap.syncState)
	}
	if len(snap.syncTarget.harnesses) != 1 || snap.syncTarget.harnesses[0] != "cline" {
		t.Fatalf("expected harnesses=[cline], got %v", snap.syncTarget.harnesses)
	}

	// Error state.
	m.profiles.items[0].syncState = syncError
	m.profiles.items[0].syncErrText = "boom"
	snap = m.activeSyncSnapshot()
	if snap.syncState != syncError {
		t.Fatalf("expected syncError, got %d", snap.syncState)
	}
	if snap.syncErrText != "boom" {
		t.Fatalf("expected error text 'boom', got %q", snap.syncErrText)
	}
}

func TestSyncTab_SyncConfigSavedPropagates(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{ConfigDir: "/tmp/cfg"})
	m.profiles.items = []profileItem{
		{name: "test", isActive: true, syncState: syncSynced},
	}

	// Simulate saving sync config with updated harnesses.
	updatedCfg := config.SyncConfig{}
	updatedCfg.Defaults.Harnesses = []string{"codex"}
	updatedCfg.Defaults.Scope = "global"

	result, _ := m.Update(syncConfigSavedMsg{syncCfg: updatedCfg})
	rm := result.(rootModel)

	// Root config should have the updated values.
	if len(rm.cfg.SyncCfg.Defaults.Harnesses) != 1 || rm.cfg.SyncCfg.Defaults.Harnesses[0] != "codex" {
		t.Fatalf("expected root cfg harnesses=[codex], got %v", rm.cfg.SyncCfg.Defaults.Harnesses)
	}
	if rm.cfg.SyncCfg.Defaults.Scope != "global" {
		t.Fatalf("expected root cfg scope=global, got %q", rm.cfg.SyncCfg.Defaults.Scope)
	}
}
