package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
)

// loadProfiles scans the profiles directory and returns a list of profile items.
func loadProfiles(configDir string, syncCfg config.SyncConfig) tea.Cmd {
	return func() tea.Msg {
		profilesDir := filepath.Join(configDir, "profiles")
		names, err := config.ListProfileNames(profilesDir)
		if err != nil {
			if os.IsNotExist(err) {
				return profilesLoadedMsg{items: nil}
			}
			return profilesLoadedMsg{err: err}
		}

		defaultProfile := syncCfg.Defaults.Profile
		if defaultProfile == "" {
			defaultProfile = "default"
		}

		var items []profileItem
		for _, name := range names {
			path := filepath.Join(profilesDir, name+".yaml")

			item := profileItem{
				name:      name,
				path:      path,
				isActive:  name == defaultProfile,
				syncState: syncPending,
			}

			// Try to load the profile config for later use.
			if cfg, err := config.LoadProfile(path); err == nil {
				item.cfg = cfg
			}

			items = append(items, item)
		}
		return profilesLoadedMsg{items: items}
	}
}

// createProfile creates a new empty profile YAML.
func createProfile(configDir, name string) tea.Cmd {
	return func() tea.Msg {
		err := app.ProfileCreate(app.ProfileCreateRequest{
			ConfigDir: configDir,
			Name:      name,
		})
		return profileCreatedMsg{name: name, err: err}
	}
}

// deleteProfile removes a profile YAML file and clears the active profile setting if needed.
func deleteProfile(configDir, name string) tea.Cmd {
	return func() tea.Msg {
		err := app.ProfileDelete(app.ProfileDeleteRequest{
			ConfigDir: configDir,
			Name:      name,
		})
		return profileDeletedMsg{name: name, err: err}
	}
}

// activateProfile sets a profile as the default in sync-config.
func activateProfile(configDir, name string) tea.Cmd {
	return func() tea.Msg {
		err := app.ProfileSet(app.ProfileSetRequest{
			ConfigDir: configDir,
			Name:      name,
		})
		return profileActivatedMsg{profileName: name, err: err}
	}
}

// saveProfile writes a profile config back to its YAML file via app.ProfileSave.
func saveProfile(configDir, name string, cfg config.ProfileConfig) tea.Cmd {
	return func() tea.Msg {
		err := app.ProfileSave(app.ProfileSaveRequest{
			ConfigDir: configDir,
			Name:      name,
			Config:    cfg,
		})
		return profileSavedMsg{profileName: name, err: err}
	}
}

// loadPacks lists all installed packs with full details in a single pass.
func loadPacks(configDir string) tea.Cmd {
	return func() tea.Msg {
		entries, err := app.PackListDetailed(configDir)
		if err != nil {
			return packsLoadedMsg{err: err}
		}

		items := make([]packItemDetail, 0, len(entries))
		for _, e := range entries {
			items = append(items, packItemDetail{
				entry:     e,
				sizeState: asyncPending,
			})
		}
		return packsLoadedMsg{items: items}
	}
}

// checkSyncStatus runs a dry-run sync plan for a profile to check if it's synced.
// It accepts the in-memory ProfileConfig so that unsaved toggles are reflected
// immediately. It delegates to app.PlanWithDiffs for all classify+filter logic.
func checkSyncStatus(configDir, profileName, profilePath string, profileCfg config.ProfileConfig, syncCfg config.SyncConfig, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		ctx, warnings, err := app.ResolveProfile(app.ResolveRequest{
			ConfigDir:   configDir,
			ProfilePath: profilePath,
			ProfileCfg:  profileCfg,
			SyncCfg:     syncCfg,
		})
		if err != nil {
			return syncStatusMsg{profileName: profileName, warnings: warnings, err: err}
		}

		summary, err := app.PlanWithDiffs(ctx.Profile, app.SyncRequest{
			TargetSpec: ctx.TargetSpec,
		}, reg)
		if err != nil {
			return syncStatusMsg{profileName: profileName, warnings: warnings, err: err}
		}
		warnings = append(warnings, summary.Warnings...)

		target := planSummaryToTarget(ctx, summary)
		hasChanges := target.TotalChanges() > 0
		return syncStatusMsg{profileName: profileName, synced: !hasChanges, target: target, warnings: warnings}
	}
}

// planSummaryToTarget converts an app.PlanSummary to a TUI syncTargetInfo.
func planSummaryToTarget(ctx app.ResolveResult, ps app.PlanSummary) syncTargetInfo {
	harnessNames := make([]string, len(ctx.Harnesses))
	for i, h := range ctx.Harnesses {
		harnessNames[i] = string(h)
	}

	return syncTargetInfo{
		PlanSummary: ps,
		harnesses:   harnessNames,
		scope:       ctx.Scope,
		projectDir:  ctx.ProjectDir,
	}
}

// runSync executes a full sync (plan + apply) for a profile.
// It delegates to app.RunSync — the single source of truth for sync orchestration.
func runSync(configDir, profileName, profilePath, scope, harnessFlag string, syncCfg config.SyncConfig, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		profileCfg, err := config.LoadProfile(profilePath)
		if err != nil {
			return syncDoneMsg{profileName: profileName, err: err}
		}

		ctx, warnings, err := app.ResolveProfile(app.ResolveRequest{
			ConfigDir:   configDir,
			ProfilePath: profilePath,
			ProfileCfg:  profileCfg,
			SyncCfg:     syncCfg,
		})
		if err != nil {
			return syncDoneMsg{profileName: profileName, warnings: warnings, err: err}
		}

		// Apply explicit overrides from dialog chain.
		if scope != "" {
			if s, serr := cmdutil.NormalizeScope(scope); serr == nil {
				ctx.Scope = s
			}
		}
		if harnessFlag != "" {
			if hs, herr := cmdutil.ResolveHarnesses([]string{harnessFlag}); herr == nil {
				ctx.Harnesses = hs
			}
		}

		projectDir := ctx.ProjectDir
		if ctx.Scope != domain.ScopeProject {
			projectDir = ""
		}

		ts := ctx.TargetSpec
		ts.ProjectDir = projectDir

		result, err := app.RunSync(ctx.Profile, app.SyncRequest{
			TargetSpec: ts,
			Force:      true,
			Yes:        true,
			Quiet:      true,
		}, reg, io.Discard, io.Discard)
		if err != nil {
			return syncDoneMsg{profileName: profileName, err: err}
		}

		total := len(result.Plan.Writes) + len(result.Plan.Copies) + len(result.Plan.Settings)
		return syncDoneMsg{profileName: profileName, filesWritten: total, warnings: warnings}
	}
}

// saveSyncConfig writes sync-config to disk and returns the saved config.
func saveSyncConfig(configDir string, cfg config.SyncConfig) tea.Cmd {
	return func() tea.Msg {
		path := config.SyncConfigPath(configDir)
		if err := config.SaveSyncConfig(path, cfg); err != nil {
			return syncConfigSavedMsg{err: err, syncCfg: cfg}
		}
		return syncConfigSavedMsg{syncCfg: cfg}
	}
}

// addPack installs a pack from a path, URL, or registry name.
// For bare names (no path separators, not an existing path), it performs a
// registry lookup — matching the CLI's `pack install` behavior.
func addPack(configDir, input string) tea.Cmd {
	return func() tea.Msg {
		req := app.PackAddRequest{
			ConfigDir: configDir,
		}
		if strings.Contains(input, "://") ||
			strings.HasPrefix(input, "github.com") ||
			strings.HasPrefix(input, "bitbucket.org") {
			req.URL = input
		} else if isRegistryName(input) {
			// Bare name — try registry lookup.
			entry, err := app.RegistryLookup(app.RegistryListRequest{ConfigDir: configDir}, input)
			if err != nil {
				return packAddedMsg{name: input, err: fmt.Errorf("registry lookup for %q: %w", input, err)}
			}
			req.URL = entry.Repo
			req.SubPath = entry.Path
			req.Ref = entry.Ref
			req.Name = input
		} else {
			req.PackPath = input
			req.Link = true
		}
		err := app.PackAdd(req, io.Discard)
		name := req.Name
		if name == "" {
			name = filepath.Base(input)
		}
		return packAddedMsg{name: name, err: err}
	}
}

// isRegistryName delegates to cmdutil.IsRegistryName.
var isRegistryName = cmdutil.IsRegistryName

// removePack removes an installed pack.
func removePack(configDir, name string) tea.Cmd {
	return func() tea.Msg {
		err := app.PackRemove(configDir, name, io.Discard)
		return packRemovedMsg{name: name, err: err}
	}
}

// updatePack updates one or all installed packs.
func updatePack(configDir, name string, all bool) tea.Cmd {
	return func() tea.Msg {
		req := app.PackUpdateRequest{
			ConfigDir: configDir,
			Name:      name,
			All:       all,
		}
		results, err := app.PackUpdate(req, io.Discard)
		return packUpdatedMsg{name: name, results: results, err: err}
	}
}

// loadRegistry loads the pack registry asynchronously.
func loadRegistry(configDir string) tea.Cmd {
	return func() tea.Msg {
		results, err := app.RegistryList(app.RegistryListRequest{
			ConfigDir: configDir,
		})
		if err != nil {
			return registryLoadedMsg{err: err}
		}
		items := make([]registryItem, 0, len(results))
		for _, r := range results {
			items = append(items, registryItem{
				name:        r.Name,
				description: r.Description,
				repo:        r.Repo,
				path:        r.Path,
				ref:         r.Ref,
				owner:       r.Owner,
			})
		}
		return registryLoadedMsg{items: items}
	}
}

// computePackSizes computes file sizes for a single installed pack.
func computePackSizes(entry app.PackShowEntry) tea.Cmd {
	return func() tea.Msg {
		sizes := map[string]int64{}
		var total int64

		addSizes := func(category string, ids []string) {
			for _, id := range ids {
				fp := contentPath(entry.Path, category, id)
				if fp == "" {
					continue
				}
				var sz int64
				if category == CatSkills {
					sz = dirSize(filepath.Dir(fp))
				} else {
					sz = fileSize(fp)
				}
				sizes[category+"/"+id] = sz
				if sz > 0 {
					total += sz
				}
			}
		}

		addSizes(CatRules, entry.Rules)
		addSizes(CatAgents, entry.Agents)
		addSizes(CatWorkflows, entry.Workflows)
		addSizes(CatSkills, entry.Skills)

		for _, name := range entry.MCPServers {
			fp := filepath.Join(entry.Path, "mcp", name+".json")
			sz := fileSize(fp)
			sizes[CatMCP+"/"+name] = sz
			if sz > 0 {
				total += sz
			}
		}

		sizes["total"] = total
		return packSizesMsg{packName: entry.Name, sizes: sizes}
	}
}

// runSavePlan runs a dry-run round-trip save and returns plan entries for preview.
func runSavePlan(configDir, profileName, profilePath string, syncCfg config.SyncConfig, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		result, warnings, err := resolveAndRunRoundTrip(configDir, profilePath, syncCfg, true, reg)
		if err != nil {
			return savePlanMsg{profileName: profileName, profilePath: profilePath, warnings: warnings, err: err}
		}

		var ops []app.PlanOp
		for _, sf := range result.SavedFiles {
			ops = append(ops, app.PlanOp{
				Kind:       app.PlanOpCopy,
				Dst:        sf.PackPath,
				Src:        sf.HarnessPath,
				SourcePack: sf.PackName,
			})
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
			profilePath: profilePath,
			ops:         ops,
			warnings:    warnings,
		}
	}
}

// runSave executes a round-trip save (harness → source packs) for a profile.
func runSave(configDir, profileName, profilePath string, syncCfg config.SyncConfig, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		result, warnings, err := resolveAndRunRoundTrip(configDir, profilePath, syncCfg, false, reg)
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

// resolveAndRunRoundTrip loads a profile, resolves save parameters from
// syncCfg, and runs RunRoundTrip. Shared by runSavePlan and runSave.
func resolveAndRunRoundTrip(configDir, profilePath string, syncCfg config.SyncConfig, dryRun bool, reg *harness.Registry) (app.RoundTripResult, []domain.Warning, error) {
	profileCfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return app.RoundTripResult{}, nil, err
	}

	ctx, warnings, err := app.ResolveProfile(app.ResolveRequest{
		ConfigDir:   configDir,
		ProfilePath: profilePath,
		ProfileCfg:  profileCfg,
		SyncCfg:     syncCfg,
	})
	if err != nil {
		return app.RoundTripResult{}, warnings, err
	}

	packRoots := map[string]string{}
	for _, p := range ctx.Profile.Packs {
		packRoots[p.Name] = p.Root
	}

	result, err := app.RunRoundTrip(app.RoundTripRequest{
		TargetSpec: ctx.TargetSpec,
		PackRoots:  packRoots,
		DryRun:     dryRun,
		Force:      true,
	}, reg)
	if err != nil {
		return result, warnings, err
	}
	warnings = append(warnings, result.CaptureWarnings...)
	return result, warnings, nil
}

// inspectHarness runs an async harness inspection for the Save tab.
func inspectHarness(configDir, profilePath string, syncCfg config.SyncConfig, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		profileCfg, err := config.LoadProfile(profilePath)
		if err != nil {
			return inspectResultMsg{err: err}
		}

		ctx, _, err := app.ResolveProfile(app.ResolveRequest{
			ConfigDir:   configDir,
			ProfilePath: profilePath,
			ProfileCfg:  profileCfg,
			SyncCfg:     syncCfg,
		})
		if err != nil {
			return inspectResultMsg{err: err}
		}

		packRoots := map[string]string{}
		for _, p := range ctx.Profile.Packs {
			packRoots[p.Name] = p.Root
		}

		result, err := app.InspectHarness(app.InspectRequest{
			TargetSpec: ctx.TargetSpec,
			PackRoots:  packRoots,
		}, reg)
		return inspectResultMsg{result: result, err: err}
	}
}

// resolveActiveTargetSpec loads the active profile from sync-config and resolves
// it into a TargetSpec. Shared by saveFileToPack and moveContentToPack.
func resolveActiveTargetSpec(configDir string, syncCfg config.SyncConfig) (app.TargetSpec, error) {
	profileName := syncCfg.Defaults.Profile
	if profileName == "" {
		profileName = "default"
	}
	profilePath := filepath.Join(configDir, "profiles", profileName+".yaml")
	profileCfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return app.TargetSpec{}, err
	}
	ctx, _, err := app.ResolveProfile(app.ResolveRequest{
		ConfigDir:   configDir,
		ProfilePath: profilePath,
		ProfileCfg:  profileCfg,
		SyncCfg:     syncCfg,
	})
	if err != nil {
		return app.TargetSpec{}, err
	}
	return ctx.TargetSpec, nil
}

// saveFileToPack adopts a single untracked harness file into a named pack.
func saveFileToPack(configDir, harnessPath, category, relPath, packName string, syncCfg config.SyncConfig) tea.Cmd {
	return func() tea.Msg {
		ts, err := resolveActiveTargetSpec(configDir, syncCfg)
		if err != nil {
			return saveToPackMsg{harnessPath: harnessPath, packName: packName, err: err}
		}

		err = app.AdoptFile(app.AdoptFileRequest{
			TargetSpec:  ts,
			ConfigDir:   configDir,
			PackName:    packName,
			HarnessPath: harnessPath,
			Category:    category,
			RelPath:     relPath,
		})
		return saveToPackMsg{harnessPath: harnessPath, packName: packName, err: err}
	}
}

// moveContentToPack moves a content item from one pack to another.
func moveContentToPack(configDir, id, category, fromPack, toPack string, syncCfg config.SyncConfig) tea.Cmd {
	return func() tea.Msg {
		ts, err := resolveActiveTargetSpec(configDir, syncCfg)
		if err != nil {
			return moveToPackMsg{id: id, category: category, fromPack: fromPack, toPack: toPack, err: err}
		}

		// Build the harness path from the source pack so we can read the file content.
		srcPackRoot := filepath.Join(configDir, "packs", fromPack)
		srcManifestPath := filepath.Join(srcPackRoot, "pack.json")
		srcManifest, err := config.LoadPackManifest(srcManifestPath)
		if err != nil {
			return moveToPackMsg{id: id, category: category, fromPack: fromPack, toPack: toPack, err: err}
		}
		srcResolvedRoot := app.ResolvePackRootWithFallback(srcManifestPath, srcManifest, srcPackRoot)

		harnessPath := filepath.Join(srcResolvedRoot, category, id+app.CategoryExt(category))

		err = app.MoveFile(app.MoveFileRequest{
			AdoptFileRequest: app.AdoptFileRequest{
				TargetSpec:  ts,
				ConfigDir:   configDir,
				PackName:    toPack,
				HarnessPath: harnessPath,
				Category:    category,
				RelPath:     id,
			},
			FromPackName: fromPack,
		})
		return moveToPackMsg{id: id, category: category, fromPack: fromPack, toPack: toPack, err: err}
	}
}

// runSearch queries the pack index asynchronously.
func runSearch(configDir, query, kind, category, installed string) tea.Cmd {
	return func() tea.Msg {
		req := app.IndexSearchRequest{
			ConfigDir: configDir,
			Terms:     query,
			Kind:      kind,
			Category:  category,
		}
		switch installed {
		case "installed":
			t := true
			req.Installed = &t
		case "available":
			f := false
			req.Installed = &f
		}
		results, err := app.RunIndexSearch(req)
		if err != nil {
			return searchResultsMsg{err: err}
		}
		return searchResultsMsg{results: results}
	}
}

// installFromSearch installs a pack by registry name and registers it in the
// active profile so it's immediately usable after the next sync.
func installFromSearch(configDir, name, profile string) tea.Cmd {
	return func() tea.Msg {
		entry, err := app.RegistryLookup(app.RegistryListRequest{ConfigDir: configDir}, name)
		if err != nil {
			return searchInstallMsg{name: name, err: fmt.Errorf("registry lookup for %q: %w", name, err)}
		}
		req := app.PackAddRequest{
			ConfigDir: configDir,
			URL:       entry.Repo,
			SubPath:   entry.Path,
			Ref:       entry.Ref,
			Name:      name,
			Register:  true,
			Profile:   profile,
		}
		err = app.PackAdd(req, io.Discard)
		return searchInstallMsg{name: name, err: err}
	}
}

// duplicateProfile copies a profile to a new name via app.ProfileDuplicate.
func duplicateProfile(configDir, srcName, newName string) tea.Cmd {
	return func() tea.Msg {
		err := app.ProfileDuplicate(app.ProfileDuplicateRequest{
			ConfigDir: configDir,
			SrcName:   srcName,
			DstName:   newName,
		})
		return profileCreatedMsg{name: newName, err: err}
	}
}
