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

func TestConfigTabIsLast(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	names := m.tabNames(common.SyncStatusPending)
	want := []string{"Profiles", "Packs", "Sync", "Save", "Search", "Config"}
	if len(names) != len(want) {
		t.Fatalf("tab count = %d, want %d: %v", len(names), len(want), names)
	}
	for i, name := range want {
		if names[i] != name {
			t.Fatalf("tab %d = %q, want %q; tabs=%v", i, names[i], name, names)
		}
	}
}

func TestConfigTabSyncDefaultsMutateRootSyncConfig(t *testing.T) {
	t.Parallel()

	rm := newRootModel(context.Background(), RunConfig{ConfigDir: "/tmp/cfg"})
	rm.cfg.SyncCfg.Defaults.Harnesses = []string{"cline"}
	rm.cfg.SyncCfg.Defaults.Scope = "project"
	rm = seedProfiles(rm, profilescreen.Seed{Name: "test", IsActive: true})

	result, _ := rm.Update(configscreen.HarnessSelectMsg{})
	rm = result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogConfigHarnesses {
		t.Fatalf("expected config harness checklist dialog, got %+v", rm.dialog)
	}

	result, _ = rm.handleDialogResult(dialogResultMsg{
		ID:        dialogConfigHarnesses,
		Confirmed: true,
		Values:    []string{"codex", "opencode"},
	})
	rm = result.(rootModel)
	if got := strings.Join(rm.cfg.SyncCfg.Defaults.Harnesses, ","); got != "codex,opencode" {
		t.Fatalf("expected selected harnesses saved, got %q", got)
	}

	result, _ = rm.Update(configscreen.CycleScopeMsg{})
	rm = result.(rootModel)
	if rm.cfg.SyncCfg.Defaults.Scope != "global" {
		t.Fatalf("expected scope=global, got %q", rm.cfg.SyncCfg.Defaults.Scope)
	}
}

func TestConfigProfilePickerStartsOnDefaultProfile(t *testing.T) {
	t.Parallel()

	rm := newRootModel(context.Background(), RunConfig{ConfigDir: "/tmp/cfg"})
	rm.cfg.SyncCfg.Defaults.Profile = "beta"
	rm = seedProfiles(rm,
		profilescreen.Seed{Name: "alpha", IsActive: true},
		profilescreen.Seed{Name: "beta"},
		profilescreen.Seed{Name: "gamma"},
	)

	result, _ := rm.Update(configscreen.ProfileSelectMsg{})
	rm = result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogSyncSelectProfile {
		t.Fatalf("expected config profile dialog, got %+v", rm.dialog)
	}
	if got := rm.dialog.ListCursor; got != 1 {
		t.Fatalf("profile dialog cursor = %d, want 1", got)
	}
}

func TestConfigRefsLoadedUpdatesConfigScreen(t *testing.T) {
	t.Parallel()

	rm := newRootModel(context.Background(), RunConfig{ConfigDir: "/tmp/cfg"})
	rm = seedProfiles(rm, profilescreen.Seed{
		Name:     "work",
		IsActive: true,
		Config: config.ProfileConfig{
			Params: map[string]string{"workspace": "default"},
		},
	})

	result, _ := rm.Update(configscreen.LoadedMsg{
		ProfileName: "work",
		Refs: []app.ProfileRef{
			{Kind: "param", Name: "workspace", Status: "set", Pack: "team-tools"},
		},
	})
	rm = result.(rootModel)
	screen, _ := rm.configScreen().Update(testKeyText("j"))
	view := screen.(configscreen.Model).SetSize(120, 32).View()
	rendered := lipgloss.NewCompositor(view).Render()
	if !strings.Contains(rendered, "team-tools") {
		t.Fatalf("config screen missing loaded ref usage:\n%s", rendered)
	}
}

func TestConfigActionMenuOffersAddForParamsAndEnv(t *testing.T) {
	t.Parallel()

	rm := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	rm.activeTab = tabConfig
	rm = seedProfiles(rm, profilescreen.Seed{
		Name:     "work",
		IsActive: true,
		Config:   config.ProfileConfig{Params: map[string]string{"workspace": "default"}},
	})
	rm, _ = rm.syncConfigScreenContext(false)
	result, _ := rm.Update(configscreen.LoadedMsg{
		ProfileName: "work",
		Env:         []app.EnvEntry{{Key: "API_TOKEN", Length: 6}},
	})
	rm = result.(rootModel)

	result, _ = rm.Update(testKeyText("j"))
	rm = result.(rootModel)
	result, _ = rm.Update(testKeyText("."))
	rm = result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogActionConfig {
		t.Fatalf("expected config action dialog for params, got %+v", rm.dialog)
	}
	if got, want := rm.dialog.ListItems, []string{actAddParam, actEditParam, actDeleteParam}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("param actions = %v, want %v", got, want)
	}

	rm.dialog = nil
	result, _ = rm.Update(testKeyText("j"))
	rm = result.(rootModel)
	result, _ = rm.Update(testKeyText("."))
	rm = result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogActionConfig {
		t.Fatalf("expected config action dialog for env, got %+v", rm.dialog)
	}
	if got, want := rm.dialog.ListItems, []string{actAddEnv, actEditEnv, actDeleteEnv}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("env actions = %v, want %v", got, want)
	}
}

func TestConfigActionMenuHidesDeleteForUnsetParamRefs(t *testing.T) {
	t.Parallel()

	rm := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	rm.activeTab = tabConfig
	rm = seedProfiles(rm, profilescreen.Seed{Name: "work", IsActive: true})
	rm, _ = rm.syncConfigScreenContext(false)
	result, _ := rm.Update(configscreen.LoadedMsg{
		ProfileName: "work",
		Refs: []app.ProfileRef{{
			Kind:    "param",
			Name:    "workspace",
			Status:  "defaulted",
			Display: "{params.workspace:-default}",
		}},
	})
	rm = result.(rootModel)

	result, _ = rm.Update(testKeyText("j"))
	rm = result.(rootModel)
	result, _ = rm.Update(testKeyText("."))
	rm = result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogActionConfig {
		t.Fatalf("expected config action dialog for params, got %+v", rm.dialog)
	}
	if got, want := rm.dialog.ListItems, []string{actAddParam, actEditParam}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("param actions = %v, want %v", got, want)
	}
}

func TestConfigActionMenuAddEnvWritesDotEnv(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	rm := newRootModel(context.Background(), RunConfig{ConfigDir: configDir})
	rm.activeTab = tabConfig
	rm = seedProfiles(rm, profilescreen.Seed{Name: "work", IsActive: true})

	result, cmd := rm.handleDialogResult(dialogResultMsg{
		ID:        dialogConfigEnvAdd,
		Confirmed: true,
		Value:     "API_TOKEN=abc123",
	})
	rm = result.(rootModel)
	if cmd == nil {
		t.Fatal("expected env set command")
	}
	result, _ = rm.Update(cmd())
	rm = result.(rootModel)
	if !strings.Contains(rm.statusText, "set API_TOKEN") {
		t.Fatalf("statusText = %q, want set API_TOKEN", rm.statusText)
	}
	value, ok, err := app.EnvGet(configDir, "API_TOKEN")
	if err != nil {
		t.Fatalf("EnvGet: %v", err)
	}
	if !ok || value != "abc123" {
		t.Fatalf("API_TOKEN = %q, %v; want abc123, true", value, ok)
	}

	result, _ = rm.Update(configscreen.LoadedMsg{
		ProfileName: "work",
		Env:         []app.EnvEntry{{Key: "API_TOKEN", Length: 6}},
	})
	rm = result.(rootModel)
	if sel, ok := rm.configScreen().CurrentEnvSelection(); !ok || sel.Key != "API_TOKEN" {
		t.Fatalf("current env = %+v, %v; want API_TOKEN", sel, ok)
	}
}

func TestConfigActionMenuAddParamUpdatesActiveProfile(t *testing.T) {
	t.Parallel()

	rm := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	rm.activeTab = tabConfig
	rm = seedProfiles(rm, profilescreen.Seed{Name: "work", IsActive: true})

	result, _ := rm.handleDialogResult(dialogResultMsg{
		ID:        dialogActionConfig,
		Confirmed: true,
		Value:     actAddParam,
	})
	rm = result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogProfileParamAdd {
		t.Fatalf("expected add-param input dialog, got %+v", rm.dialog)
	}

	result, cmd := rm.handleDialogResult(dialogResultMsg{
		ID:        dialogProfileParamAdd,
		Confirmed: true,
		Value:     "workspace=default",
	})
	rm = result.(rootModel)
	if cmd == nil {
		t.Fatal("expected profile save command")
	}
	item, ok := rm.profilesScreen().CurrentProfile()
	if !ok {
		t.Fatal("expected current profile")
	}
	if got := item.Config.Params["workspace"]; got != "default" {
		t.Fatalf("workspace = %q, want default", got)
	}
}

func TestConfigActionMenuAddParamSelectsNewParam(t *testing.T) {
	t.Parallel()

	rm := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	rm.activeTab = tabConfig
	rm = seedProfiles(rm, profilescreen.Seed{
		Name:     "work",
		IsActive: true,
		Config:   config.ProfileConfig{Params: map[string]string{"alpha": "one"}},
	})
	rm, _ = rm.syncConfigScreenContext(false)
	result, _ := rm.Update(testKeyText("j"))
	rm = result.(rootModel)

	result, _ = rm.handleDialogResult(dialogResultMsg{
		ID:        dialogProfileParamAdd,
		Confirmed: true,
		Value:     "zebra=two",
	})
	rm = result.(rootModel)

	if key, ok := rm.configScreen().CurrentParamKey(); !ok || key != "zebra" {
		t.Fatalf("current param = %q, %v; want zebra", key, ok)
	}

	result, _ = rm.Update(configscreen.LoadedMsg{
		ProfileName: "work",
		Refs:        []app.ProfileRef{{Kind: "param", Name: "aardvark", Status: "defaulted"}},
	})
	rm = result.(rootModel)
	if key, ok := rm.configScreen().CurrentParamKey(); !ok || key != "zebra" {
		t.Fatalf("current param after refs refresh = %q, %v; want zebra", key, ok)
	}
}

func TestConfigActionMenuEditParamOpensCurrentParam(t *testing.T) {
	t.Parallel()

	rm := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	rm.activeTab = tabConfig
	rm = seedProfiles(rm, profilescreen.Seed{
		Name:     "work",
		IsActive: true,
		Config:   config.ProfileConfig{Params: map[string]string{"workspace": "default"}},
	})
	rm, _ = rm.syncConfigScreenContext(false)
	result, _ := rm.Update(testKeyText("j"))
	rm = result.(rootModel)

	result, _ = rm.handleDialogResult(dialogResultMsg{
		ID:        dialogActionConfig,
		Confirmed: true,
		Value:     actEditParam,
	})
	rm = result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogProfileParamEdit {
		t.Fatalf("expected edit-param input dialog, got %+v", rm.dialog)
	}
	if rm.dialog.TextValue != "default" {
		t.Fatalf("textValue = %q, want default", rm.dialog.TextValue)
	}
}

func TestConfigActionMenuDeleteParamRemovesActiveProfileParam(t *testing.T) {
	t.Parallel()

	rm := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	rm.activeTab = tabConfig
	rm = seedProfiles(rm, profilescreen.Seed{
		Name:     "work",
		IsActive: true,
		Config:   config.ProfileConfig{Params: map[string]string{"workspace": "default"}},
	})
	rm, _ = rm.syncConfigScreenContext(false)
	result, _ := rm.Update(testKeyText("j"))
	rm = result.(rootModel)

	result, _ = rm.handleDialogResult(dialogResultMsg{
		ID:        dialogActionConfig,
		Confirmed: true,
		Value:     actDeleteParam,
	})
	rm = result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogProfileParamDelete {
		t.Fatalf("expected delete-param confirm dialog, got %+v", rm.dialog)
	}

	result, cmd := rm.handleDialogResult(dialogResultMsg{
		ID:        dialogProfileParamDelete,
		Confirmed: true,
	})
	rm = result.(rootModel)
	if cmd == nil {
		t.Fatal("expected profile save command")
	}
	item, ok := rm.profilesScreen().CurrentProfile()
	if !ok {
		t.Fatal("expected current profile")
	}
	if _, ok := item.Config.Params["workspace"]; ok {
		t.Fatal("workspace param should have been removed")
	}
	if !strings.Contains(rm.statusText, "removed workspace") {
		t.Fatalf("statusText = %q, want removed workspace", rm.statusText)
	}
}

func TestConfigActionMenuEditEnvWritesDotEnv(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	if err := app.EnvSet(configDir, "API_TOKEN", "old"); err != nil {
		t.Fatal(err)
	}
	rm := newRootModel(context.Background(), RunConfig{ConfigDir: configDir})
	rm.activeTab = tabConfig
	rm = seedProfiles(rm, profilescreen.Seed{Name: "work", IsActive: true})
	rm, _ = rm.syncConfigScreenContext(false)
	result, _ := rm.Update(configscreen.LoadedMsg{
		ProfileName: "work",
		Env:         []app.EnvEntry{{Key: "API_TOKEN", Length: 3}},
	})
	rm = result.(rootModel)
	result, _ = rm.Update(testKeyText("j"))
	rm = result.(rootModel)
	result, _ = rm.Update(testKeyText("j"))
	rm = result.(rootModel)

	result, _ = rm.handleDialogResult(dialogResultMsg{
		ID:        dialogActionConfig,
		Confirmed: true,
		Value:     actEditEnv,
	})
	rm = result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogConfigEnvEdit {
		t.Fatalf("expected edit-env input dialog, got %+v", rm.dialog)
	}
	if rm.dialog.TextValue != "old" {
		t.Fatalf("textValue = %q, want old", rm.dialog.TextValue)
	}

	result, cmd := rm.handleDialogResult(dialogResultMsg{
		ID:        dialogConfigEnvEdit,
		Confirmed: true,
		Value:     "new",
	})
	rm = result.(rootModel)
	if cmd == nil {
		t.Fatal("expected env set command")
	}
	result, _ = rm.Update(cmd())
	rm = result.(rootModel)
	if !strings.Contains(rm.statusText, "set API_TOKEN") {
		t.Fatalf("statusText = %q, want set API_TOKEN", rm.statusText)
	}
	value, ok, err := app.EnvGet(configDir, "API_TOKEN")
	if err != nil {
		t.Fatalf("EnvGet: %v", err)
	}
	if !ok || value != "new" {
		t.Fatalf("API_TOKEN = %q, %v; want new, true", value, ok)
	}
}

func TestConfigActionMenuDeleteEnvRemovesDotEnvEntry(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	if err := app.EnvSet(configDir, "API_TOKEN", "abc123"); err != nil {
		t.Fatal(err)
	}
	rm := newRootModel(context.Background(), RunConfig{ConfigDir: configDir})
	rm.activeTab = tabConfig
	rm = seedProfiles(rm, profilescreen.Seed{Name: "work", IsActive: true})
	rm, _ = rm.syncConfigScreenContext(false)
	result, _ := rm.Update(configscreen.LoadedMsg{
		ProfileName: "work",
		Env:         []app.EnvEntry{{Key: "API_TOKEN", Length: 6}},
	})
	rm = result.(rootModel)
	result, _ = rm.Update(testKeyText("j"))
	rm = result.(rootModel)
	result, _ = rm.Update(testKeyText("j"))
	rm = result.(rootModel)

	result, _ = rm.handleDialogResult(dialogResultMsg{
		ID:        dialogActionConfig,
		Confirmed: true,
		Value:     actDeleteEnv,
	})
	rm = result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogConfigEnvDelete {
		t.Fatalf("expected delete-env confirm dialog, got %+v", rm.dialog)
	}

	result, cmd := rm.handleDialogResult(dialogResultMsg{
		ID:        dialogConfigEnvDelete,
		Confirmed: true,
	})
	rm = result.(rootModel)
	if cmd == nil {
		t.Fatal("expected env unset command")
	}
	result, _ = rm.Update(cmd())
	rm = result.(rootModel)
	if !strings.Contains(rm.statusText, "removed API_TOKEN") {
		t.Fatalf("statusText = %q, want removed API_TOKEN", rm.statusText)
	}
	if _, ok, err := app.EnvGet(configDir, "API_TOKEN"); err != nil {
		t.Fatalf("EnvGet: %v", err)
	} else if ok {
		t.Fatal("API_TOKEN should have been removed")
	}
}
