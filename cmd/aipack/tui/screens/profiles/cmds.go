package profiles

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

func Load(configDir string, syncCfg config.SyncConfig) tea.Cmd {
	return func() tea.Msg {
		results, err := app.ProfileListItems(configDir, syncCfg)
		if err != nil {
			if os.IsNotExist(err) {
				return LoadedMsg{Items: nil}
			}
			return LoadedMsg{Err: err}
		}

		var items []profileItem
		for _, r := range results {
			item := profileItem{
				name:        r.Name,
				path:        r.Path,
				isActive:    r.IsActive,
				syncState:   common.SyncStatusPending,
				bundledFrom: r.BundledFrom,
			}
			if r.LoadErr == nil {
				item.cfg = r.Config
			}
			items = append(items, item)
		}
		return LoadedMsg{Items: items}
	}
}

func Create(configDir, name string) tea.Cmd {
	return func() tea.Msg {
		err := app.ProfileCreate(app.ProfileCreateRequest{
			ConfigDir: configDir,
			Name:      name,
		})
		return CreatedMsg{Name: name, Err: err}
	}
}

func Delete(configDir, name string) tea.Cmd {
	return func() tea.Msg {
		err := app.ProfileDelete(app.ProfileDeleteRequest{
			ConfigDir: configDir,
			Name:      name,
		})
		return DeletedMsg{Name: name, Err: err}
	}
}

func Activate(configDir, name string) tea.Cmd {
	return func() tea.Msg {
		err := app.ProfileSet(app.ProfileSetRequest{
			ConfigDir: configDir,
			Name:      name,
		})
		return ActivatedMsg{ProfileName: name, Err: err}
	}
}

func Save(configDir, name string, cfg config.ProfileConfig) tea.Cmd {
	return func() tea.Msg {
		err := app.ProfileSave(app.ProfileSaveRequest{
			ConfigDir: configDir,
			Name:      name,
			Config:    cfg,
		})
		return SavedMsg{ProfileName: name, Err: err}
	}
}

func Duplicate(configDir, srcName, newName string) tea.Cmd {
	return func() tea.Msg {
		err := app.ProfileDuplicate(app.ProfileDuplicateRequest{
			ConfigDir: configDir,
			SrcName:   srcName,
			DstName:   newName,
		})
		return CreatedMsg{Name: newName, Err: err}
	}
}

func CheckSyncStatus(ctx context.Context, eng *engine.Engine, configDir, profileName, profilePath string, profileCfg config.ProfileConfig, syncCfg config.SyncConfig, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		cwd, err := os.Getwd()
		if err != nil {
			return SyncStatusMsg{ProfileName: profileName, Err: fmt.Errorf("resolving working directory: %w", err)}
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
			return SyncStatusMsg{ProfileName: profileName, Warnings: warnings, Err: err}
		}
		// Preview the default sync, which targets all configured harnesses,
		// each synced independently. Counts and operations aggregate across them.
		summary, err := app.PlanWithDiffsEach(ctx, eng, resolved.Profile, app.SyncRequest{
			TargetSpec: resolved.TargetSpec,
		}, reg)
		if err != nil {
			return SyncStatusMsg{ProfileName: profileName, Warnings: warnings, Err: err}
		}
		warnings = append(warnings, summary.Warnings...)

		target := planSummaryToTarget(resolved, summary)
		return SyncStatusMsg{ProfileName: profileName, Synced: target.TotalChanges() == 0, Target: target, Warnings: warnings}
	}
}

func planSummaryToTarget(ctx app.SyncContext, ps app.PlanSummary) common.SyncTarget {
	harnessNames := make([]string, len(ctx.Harnesses))
	for i, h := range ctx.Harnesses {
		harnessNames[i] = string(h)
	}

	return common.SyncTarget{
		PlanSummary: ps,
		Harnesses:   harnessNames,
		Scope:       ctx.Scope,
		ProjectDir:  ctx.ProjectDir,
	}
}
