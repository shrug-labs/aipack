package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	packsscreen "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/packs"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"
)

// loadPacks lists all installed packs with full details in a single pass.
func loadPacks(configDir string) tea.Cmd {
	return packsscreen.Load(configDir)
}

// runSync executes a full sync (plan + apply) for a profile.
// It delegates to app.RunSync — the single source of truth for sync orchestration.
func runSync(ctx context.Context, eng *engine.Engine, configDir, profileName, profilePath, scope, harnessFlag string, syncCfg config.SyncConfig, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		profileCfg, err := config.LoadProfile(profilePath)
		if err != nil {
			return syncDoneMsg{profileName: profileName, err: err}
		}

		cwd, err := os.Getwd()
		if err != nil {
			return syncDoneMsg{profileName: profileName, err: fmt.Errorf("resolving working directory: %w", err)}
		}
		resolved, warnings, err := app.ResolveProfile(eng, app.ResolveRequest{
			ConfigDir:   configDir,
			ProfilePath: profilePath,
			ProfileCfg:  profileCfg,
			SyncCfg:     syncCfg,
			ProjectDir:  cwd,
			Home:        config.HomeDir(),
		})
		if err != nil {
			return syncDoneMsg{profileName: profileName, warnings: warnings, err: err}
		}

		// Apply explicit overrides from dialog chain.
		if scope != "" {
			if s, ok := domain.ParseScope(scope); ok {
				resolved.Scope = s
			}
		}
		if harnessFlag != "" {
			h, herr := app.ResolveSingleSyncHarness([]string{harnessFlag})
			if herr != nil {
				return syncDoneMsg{profileName: profileName, warnings: warnings, err: herr}
			}
			resolved.Harnesses = []domain.Harness{h}
		}

		projectDir := resolved.ProjectDir
		if resolved.Scope != domain.ScopeProject {
			projectDir = ""
		}

		ts := resolved.TargetSpec
		ts.ProjectDir = projectDir

		results, syncWarnings, err := app.RunSyncEach(ctx, eng, resolved.Profile, app.SyncRequest{
			TargetSpec: ts,
			Force:      true,
			Yes:        true,
			Quiet:      true,
		}, reg, nil, nil)
		warnings = append(warnings, syncWarnings...)
		if err != nil {
			return syncDoneMsg{profileName: profileName, warnings: warnings, err: err}
		}

		total := 0
		for _, result := range results {
			total += len(result.Plan.Writes) + len(result.Plan.Copies) + len(result.Plan.Settings)
		}
		return syncDoneMsg{profileName: profileName, filesWritten: total, warnings: warnings}
	}
}

// saveSyncConfig writes sync-config to disk and returns the saved config.
func saveSyncConfig(configDir string, cfg config.SyncConfig) tea.Cmd {
	return func() tea.Msg {
		if err := app.SaveSyncConfig(configDir, cfg); err != nil {
			return syncConfigSavedMsg{err: err, syncCfg: cfg}
		}
		return syncConfigSavedMsg{syncCfg: cfg}
	}
}

func setConfigEnv(configDir, key, value string) tea.Cmd {
	return func() tea.Msg {
		return envSetMsg{key: strings.TrimSpace(key), err: app.EnvSet(configDir, key, value)}
	}
}

func unsetConfigEnv(configDir, key string) tea.Cmd {
	return func() tea.Msg {
		return envUnsetMsg{key: strings.TrimSpace(key), err: app.EnvUnset(configDir, key)}
	}
}

// activeInstall tracks an in-flight streaming pack install on the packs screen.
// Lives between packInstallReadyMsg and packInstalledMsg. Esc invokes cancel.
func installPack(ctx context.Context, configDir, input, ref string, with domain.BundledSet) tea.Cmd {
	return packsscreen.Install(ctx, configDir, input, ref, with)
}

func previewInstallPack(ctx context.Context, configDir string, sel packsscreen.ListSelection) tea.Cmd {
	return packsscreen.PreviewInstall(ctx, configDir, sel)
}

func readNextInstallEvent(eventCh <-chan app.PackInstallEvent, resultCh <-chan packInstallResult) tea.Cmd {
	return packsscreen.ReadNextInstallEvent(eventCh, resultCh)
}

var isRegistryName = packsscreen.IsRegistryName

func createPack(configDir, name string) tea.Cmd {
	return packsscreen.Create(configDir, name)
}

func removePack(configDir, name string, registry *harness.Registry) tea.Cmd {
	return packsscreen.Remove(configDir, name, registry)
}

// approveBundled applies user-approved bundled content categories to updated packs.
func approveBundled(configDir string, results []app.PackUpdateResult, approved domain.BundledSet) tea.Cmd {
	return func() tea.Msg {
		var names []string
		for _, r := range results {
			if r.BundledCandidates != nil {
				names = append(names, r.Name)
			}
		}
		err := app.PackApproveBundled(configDir, names, approved, nil)
		return bundledApprovedMsg{err: err}
	}
}

func updatePack(ctx context.Context, configDir, name string, all bool, ref string, with domain.BundledSet) tea.Cmd {
	return packsscreen.Update(ctx, configDir, name, all, ref, with)
}

func previewUpdatePack(ctx context.Context, configDir, name string) tea.Cmd {
	return packsscreen.PreviewUpdate(ctx, configDir, name)
}

func readNextEvent(eventCh <-chan app.PackUpdateEvent, resultCh <-chan packUpdateResult, packName string) tea.Cmd {
	return packsscreen.ReadNextEvent(eventCh, resultCh, packName)
}

func loadRegistry(configDir string) tea.Cmd {
	return packsscreen.LoadRegistry(configDir)
}

// runSavePlan runs a dry-run round-trip save and returns plan entries for preview.
func runSavePlan(ctx context.Context, eng *engine.Engine, configDir, profileName string, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		result, warnings, err := app.SaveRoundTripPlan(ctx, eng, configDir, reg)
		if err != nil {
			return savePlanMsg{profileName: profileName, warnings: warnings, err: err}
		}

		ops, expandErr := savedFilePlanOps(result.SavedFiles)
		if expandErr != nil {
			return savePlanMsg{profileName: profileName, warnings: warnings, err: expandErr}
		}
		for _, ps := range result.PendingSettings {
			ops = append(ops, app.PlanOp{
				Kind:       app.PlanOpSettings,
				Dst:        ps.PackPath,
				Src:        ps.HarnessPath,
				Content:    ps.Stripped,
				Size:       len(ps.Stripped),
				SourcePack: ps.PackName,
			})
		}

		return savePlanMsg{
			profileName: profileName,
			ops:         ops,
			warnings:    warnings,
		}
	}
}

func savedFilePlanOps(files []app.SavedFile) ([]app.PlanOp, error) {
	var ops []app.PlanOp
	for _, sf := range files {
		info, err := os.Stat(sf.HarnessPath)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			ops = append(ops, savedFilePlanOp(sf.PackPath, sf.HarnessPath, sf.PackName))
			continue
		}
		err = filepath.WalkDir(sf.HarnessPath, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if util.IgnoredName(d.Name()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(sf.HarnessPath, path)
			if err != nil {
				return err
			}
			ops = append(ops, savedFilePlanOp(
				filepath.Join(sf.PackPath, rel),
				path,
				sf.PackName,
			))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return ops, nil
}

func savedFilePlanOp(dst, src, packName string) app.PlanOp {
	return app.PlanOp{
		Kind:       planKindForPackPath(dst),
		Dst:        dst,
		Src:        src,
		SourcePack: packName,
	}
}

func planKindForPackPath(path string) app.PlanOpKind {
	for dir := filepath.Dir(path); dir != "." && dir != string(filepath.Separator); dir = filepath.Dir(dir) {
		switch strings.ToLower(filepath.Base(dir)) {
		case "rules", ".clinerules":
			return app.PlanOpRule
		case "commands", "workflows":
			return app.PlanOpWorkflow
		case "agents":
			return app.PlanOpAgent
		case "skills":
			return app.PlanOpSkill
		case "hooks":
			return app.PlanOpHook
		}
	}
	return app.PlanOpSkill
}

// runSave executes a round-trip save (harness → source packs) for a profile.
func runSave(ctx context.Context, eng *engine.Engine, configDir, profileName string, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		result, warnings, err := app.SaveRoundTrip(ctx, eng, configDir, true, reg)
		if err != nil {
			return saveDoneMsg{profileName: profileName, warnings: warnings, err: err}
		}

		return saveDoneMsg{
			profileName: profileName,
			saved:       len(result.SavedFiles),
			unchanged:   result.UnchangedCount,
			warnings:    warnings,
		}
	}
}

// moveContentToPack moves a content item from one pack to another.
func moveContentToPack(eng *engine.Engine, configDir, id string, category domain.PackCategory, fromPack, toPack string) tea.Cmd {
	return func() tea.Msg {
		err := app.MoveContent(eng, app.MoveContentRequest{
			ConfigDir: configDir,
			ID:        id,
			Category:  category,
			FromPack:  fromPack,
			ToPack:    toPack,
		})
		return moveToPackMsg{id: id, category: category, fromPack: fromPack, toPack: toPack, err: err}
	}
}
