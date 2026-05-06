package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	configscreen "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/config"
	profilescreen "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/profiles"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
)

func TestSyncTargetInfo_TotalChanges(t *testing.T) {
	t.Parallel()
	target := common.SyncTarget{PlanSummary: app.PlanSummary{
		NumRules:    5,
		NumSkills:   3,
		NumSettings: 2,
	}}
	if got := target.TotalChanges(); got != 10 {
		t.Fatalf("expected totalChanges=10, got %d", got)
	}
}

func TestConfigTab_HarnessSelection_EndToEnd(t *testing.T) {
	t.Parallel()
	rm := newRootModel(context.Background(), RunConfig{ConfigDir: "/tmp/cfg"})
	rm.cfg.SyncCfg.Defaults.Harnesses = []string{"cline"}
	rm = seedProfiles(rm, profilescreen.Seed{Name: "test", IsActive: true, SyncStatus: common.SyncStatusSynced})

	result, _ := rm.Update(configscreen.HarnessSelectMsg{})
	rm = result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogConfigHarnesses {
		t.Fatalf("expected harness checklist dialog, got %+v", rm.dialog)
	}

	result, _ = rm.handleDialogResult(dialogResultMsg{
		ID:        dialogConfigHarnesses,
		Confirmed: true,
		Values:    []string{"codex", "opencode"},
	})
	rm = result.(rootModel)
	if got := strings.Join(rm.cfg.SyncCfg.Defaults.Harnesses, ","); got != "codex,opencode" {
		t.Fatalf("expected selected harnesses, got %q", got)
	}
}

func TestSyncTab_ScopeToggle_EndToEnd(t *testing.T) {
	t.Parallel()
	rm := newRootModel(context.Background(), RunConfig{ConfigDir: "/tmp/cfg"})
	rm.cfg.SyncCfg.Defaults.Scope = "project"
	rm = seedProfiles(rm, profilescreen.Seed{Name: "test", IsActive: true, SyncStatus: common.SyncStatusSynced})

	result, _ := rm.Update(configscreen.CycleScopeMsg{})
	rm = result.(rootModel)
	if rm.cfg.SyncCfg.Defaults.Scope != "global" {
		t.Fatalf("expected scope=global, got %q", rm.cfg.SyncCfg.Defaults.Scope)
	}

	// Toggle back.
	result, _ = rm.Update(configscreen.CycleScopeMsg{})
	rm = result.(rootModel)
	if rm.cfg.SyncCfg.Defaults.Scope != "project" {
		t.Fatalf("expected scope=project, got %q", rm.cfg.SyncCfg.Defaults.Scope)
	}
}

func TestSyncTab_StatusDotOnTab(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m, profilescreen.Seed{Name: "test", IsActive: true})

	// Default: common.SyncStatusPending — no dot.
	names := m.tabNames(common.SyncStatusPending)
	syncIdx := 2 // Sync is tab index 2 (Profiles=0, Packs=1, Sync=2, Save=3, Search=4, Config=5)
	if names[syncIdx] != "Sync" {
		t.Fatalf("expected plain 'Sync' when pending, got %q", names[syncIdx])
	}

	// Synced — green dot.
	names = m.tabNames(common.SyncStatusSynced)
	if !strings.Contains(names[syncIdx], "●") {
		t.Fatalf("expected green dot for synced, got %q", names[syncIdx])
	}

	// Unsynced — red dot.
	names = m.tabNames(common.SyncStatusUnsynced)
	if !strings.Contains(names[syncIdx], "○") {
		t.Fatalf("expected red dot for unsynced, got %q", names[syncIdx])
	}

	// Loading — spinner.
	names = m.tabNames(common.SyncStatusLoading)
	if !strings.Contains(names[syncIdx], "⟳") {
		t.Fatalf("expected loading indicator, got %q", names[syncIdx])
	}
}

func TestSyncTab_SnapshotDerivedFromActiveProfile(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m, profilescreen.Seed{
		Name:       "test",
		IsActive:   true,
		SyncStatus: common.SyncStatusSynced,
		SyncTarget: common.SyncTarget{
			PlanSummary: app.PlanSummary{NumRules: 1},
			Harnesses:   []string{"cline"},
			Scope:       "project",
		},
	})

	snap := m.activeSyncSnapshot()
	if snap.Status != common.SyncStatusSynced {
		t.Fatalf("expected sync.StatusSynced, got %d", snap.Status)
	}
	if snap.Target.NumRules != 1 {
		t.Fatalf("expected snapshot target NumRules=1, got %d", snap.Target.NumRules)
	}

	// Error state.
	m = seedProfiles(m, profilescreen.Seed{Name: "test", IsActive: true, SyncStatus: common.SyncStatusError, SyncErrText: "boom"})
	snap = m.activeSyncSnapshot()
	if snap.Status != common.SyncStatusError {
		t.Fatalf("expected sync.StatusError, got %d", snap.Status)
	}
	if snap.ErrText != "boom" {
		t.Fatalf("expected error text 'boom', got %q", snap.ErrText)
	}
}

func TestConfigTab_ProfilesLoadedUpdatesContextThroughShell(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	result, _ := m.Update(profilescreen.LoadedFromSeeds([]profilescreen.Seed{
		{Name: "alpha", IsActive: true},
		{Name: "beta"},
	}))
	rm := result.(rootModel)
	rendered := lipgloss.NewCompositor(rm.configScreen().SetSize(100, 24).View()).Render()
	if !strings.Contains(rendered, "alpha") {
		t.Fatalf("expected config screen context to include active profile, got:\n%s", rendered)
	}
}

func TestSyncTab_SyncConfigSavedPropagates(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{ConfigDir: "/tmp/cfg"})
	m = seedProfiles(m, profilescreen.Seed{Name: "test", IsActive: true, SyncStatus: common.SyncStatusSynced})

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
