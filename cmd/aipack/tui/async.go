package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
)

// loadProfiles scans the profiles directory and returns a list of profile items.
func loadProfiles(configDir string, syncCfg config.SyncConfig) tea.Cmd {
	return func() tea.Msg {
		results, err := app.ProfileListItems(configDir, syncCfg)
		if err != nil {
			if os.IsNotExist(err) {
				return profilesLoadedMsg{items: nil}
			}
			return profilesLoadedMsg{err: err}
		}

		var items []profileItem
		for _, r := range results {
			item := profileItem{
				name:      r.Name,
				path:      r.Path,
				isActive:  r.IsActive,
				syncState: syncPending,
			}
			if r.LoadErr == nil {
				item.cfg = r.Config
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
func runSync(configDir, profileName, profilePath, scope, harnessFlag string, prune bool, syncCfg config.SyncConfig, reg *harness.Registry) tea.Cmd {
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
			if s, ok := domain.ParseScope(scope); ok {
				ctx.Scope = s
			}
		}
		if harnessFlag != "" {
			if h, ok := domain.ParseHarness(harnessFlag); ok {
				ctx.Harnesses = []domain.Harness{h}
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
			Prune:      prune,
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
		if err := app.SaveSyncConfig(configDir, cfg); err != nil {
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

// isRegistryName checks if a string looks like a registry pack name.
var isRegistryName = app.IsRegistryName

// createPack scaffolds a new pack inside the packs directory and registers it.
func createPack(configDir, name string) tea.Cmd {
	return func() tea.Msg {
		packDir := filepath.Join(configDir, "packs", name)
		if err := app.PackCreate(app.PackCreateRequest{Dir: packDir, Name: name}); err != nil {
			return packCreatedMsg{name: name, err: err}
		}
		// Register in sync-config so it's immediately available for profiles and save.
		scPath := config.SyncConfigPath(configDir)
		sc, err := config.LoadSyncConfig(scPath)
		if err != nil {
			return packCreatedMsg{name: name, err: fmt.Errorf("register: %w", err)}
		}
		if sc.InstalledPacks == nil {
			sc.InstalledPacks = map[string]config.InstalledPackMeta{}
		}
		sc.InstalledPacks[name] = config.InstalledPackMeta{
			Origin:      packDir,
			Method:      config.MethodLocal,
			InstalledAt: time.Now().UTC().Format(time.RFC3339),
		}
		if err := config.SaveSyncConfig(scPath, sc); err != nil {
			return packCreatedMsg{name: name, err: fmt.Errorf("register: %w", err)}
		}
		return packCreatedMsg{name: name}
	}
}

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

		for _, cat := range domain.AllPackCategories() {
			for _, id := range entry.ContentIDs(cat) {
				sz := app.PackContentSize(entry.Path, cat, id)
				sizes[cat.DirName()+"/"+id] = sz
				if sz > 0 {
					total += sz
				}
			}
		}

		sizes["total"] = total
		return packSizesMsg{packName: entry.Name, sizes: sizes}
	}
}

// runSavePlan runs a dry-run round-trip save and returns plan entries for preview.
func runSavePlan(configDir, profileName string, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		result, warnings, err := app.SaveRoundTripPlan(configDir, reg)
		if err != nil {
			return savePlanMsg{profileName: profileName, warnings: warnings, err: err}
		}

		var ops []app.PlanOp
		for _, sf := range result.SavedFiles {
			ops = append(ops, app.PlanOp{
				Kind:       app.PlanOpSkill, // save copies are content; default to skill
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
			ops:         ops,
			warnings:    warnings,
		}
	}
}

// runSave executes a round-trip save (harness → source packs) for a profile.
func runSave(configDir, profileName string, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		result, warnings, err := app.SaveRoundTrip(configDir, true, reg)
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
func moveContentToPack(configDir, id string, category domain.PackCategory, fromPack, toPack string) tea.Cmd {
	return func() tea.Msg {
		err := app.MoveContent(app.MoveContentRequest{
			ConfigDir: configDir,
			ID:        id,
			Category:  category,
			FromPack:  fromPack,
			ToPack:    toPack,
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

// ---------------------------------------------------------------------------
// Save pipeline async commands
// ---------------------------------------------------------------------------

// detectHarnesses returns all registered harnesses so the user can pick any
// target, regardless of sync-config or on-disk content.
func detectHarnesses(reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		all := reg.All()
		ids := make([]domain.Harness, len(all))
		for i, h := range all {
			ids[i] = h.ID()
		}
		return harnessDetectedMsg{harnesses: ids}
	}
}

// discoverVectors runs capture on one harness and returns available content vectors.
// Merges results from both project and global scopes.
func discoverVectors(harnessID domain.Harness, configDir string, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		res, _, err := app.ResolveActiveProfile(configDir)
		if err != nil {
			return vectorsDiscoveredMsg{err: err}
		}
		vectors, err := app.DiscoverContentVectorsAllScopes(harnessID, res.TargetSpec.ProjectDir, res.TargetSpec.Home, reg)
		return vectorsDiscoveredMsg{vectors: vectors, err: err}
	}
}

// discoverSaveFiles runs capture + classification for one harness filtered to categories.
// Merges results from both project and global scopes.
func discoverSaveFiles(req app.DiscoverSaveRequest, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		candidates, warnings, err := app.DiscoverSaveFilesAllScopes(req, reg)
		return saveFilesDiscoveredMsg{candidates: candidates, warnings: warnings, err: err}
	}
}

// deleteSaveFile removes a harness file from disk.
func deleteSaveFile(path string) tea.Cmd {
	return func() tea.Msg {
		err := os.RemoveAll(path)
		return saveFileDeletedMsg{path: path, err: err}
	}
}

// executeSavePipeline runs the pipeline to copy selected files to a pack.
func executeSavePipeline(req app.SavePipelineRequest, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		result, err := app.RunSavePipeline(req, reg)
		return savePipelineDoneMsg{result: &result, err: err}
	}
}
