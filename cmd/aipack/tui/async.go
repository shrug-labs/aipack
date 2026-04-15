package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
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
func checkSyncStatus(ctx context.Context, eng *engine.Engine, configDir, profileName, profilePath string, profileCfg config.ProfileConfig, syncCfg config.SyncConfig, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		cwd, err := os.Getwd()
		if err != nil {
			return syncStatusMsg{profileName: profileName, err: fmt.Errorf("resolving working directory: %w", err)}
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
			return syncStatusMsg{profileName: profileName, warnings: warnings, err: err}
		}

		summary, err := app.PlanWithDiffs(ctx, eng, resolved.Profile, app.SyncRequest{
			TargetSpec: resolved.TargetSpec,
		}, reg)
		if err != nil {
			return syncStatusMsg{profileName: profileName, warnings: warnings, err: err}
		}
		warnings = append(warnings, summary.Warnings...)

		target := planSummaryToTarget(resolved, summary)
		hasChanges := target.TotalChanges() > 0
		return syncStatusMsg{profileName: profileName, synced: !hasChanges, target: target, warnings: warnings}
	}
}

// planSummaryToTarget converts an app.PlanSummary to a TUI syncTargetInfo.
func planSummaryToTarget(ctx app.SyncContext, ps app.PlanSummary) syncTargetInfo {
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
			if h, ok := domain.ParseHarness(harnessFlag); ok {
				resolved.Harnesses = []domain.Harness{h}
			}
		}

		projectDir := resolved.ProjectDir
		if resolved.Scope != domain.ScopeProject {
			projectDir = ""
		}

		ts := resolved.TargetSpec
		ts.ProjectDir = projectDir

		result, syncWarnings, err := app.RunSync(ctx, eng, resolved.Profile, app.SyncRequest{
			TargetSpec: ts,
			Force:      true,
			Yes:        true,
			Quiet:      true,
		}, reg, io.Discard, io.Discard)
		warnings = append(warnings, syncWarnings...)
		if err != nil {
			return syncDoneMsg{profileName: profileName, warnings: warnings, err: err}
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

// installPack installs a pack from a path, URL, or registry name.
// For bare names (no path separators, not an existing path), it performs a
// registry lookup — matching the CLI's `pack install` behavior. ref is
// the user-picked git ref spec (e.g. "1.2.3" semver, "my-pack/v0.3.0"
// namespaced, or "main" branch); empty installs at the registry default
// ref. Only honored for registry-name installs — URL/path installs ignore
// ref because they take whatever ref the user already specified.
func installPack(ctx context.Context, configDir, input, ref string, with domain.BundledSet) tea.Cmd {
	return func() tea.Msg {
		req := app.PackInstallRequest{
			ConfigDir: configDir,
			With:      with,
			Ref:       ref,
		}
		if strings.Contains(input, "://") ||
			strings.HasPrefix(input, "github.com") ||
			strings.HasPrefix(input, "bitbucket.org") {
			req.URL = input
		} else if isRegistryName(input) {
			// Bare name — try registry lookup. Auto-fetch and retry once
			// on failure, matching the CLI's pack install behavior.
			regReq := app.RegistryListRequest{ConfigDir: configDir}
			entry, err := app.RegistryLookup(regReq, input)
			if err != nil {
				fetchErr := app.RegistryFetch(ctx, app.RegistryFetchRequest{ConfigDir: configDir}, io.Discard)
				if fetchErr == nil {
					entry, err = app.RegistryLookup(regReq, input)
				}
			}
			if err != nil {
				return packInstalledMsg{name: input, err: fmt.Errorf("registry lookup for %q: %w", input, err)}
			}
			req.URL = entry.Repo
			req.SubPath = entry.Path
			// User-picked ref wins over the registry's default ref.
			if req.Ref == "" {
				req.Ref = entry.Ref
			}
			req.Name = input
		} else {
			req.PackPath = input
			req.Link = true
		}
		err := app.PackInstall(ctx, req, io.Discard)
		name := req.Name
		if name == "" {
			name = filepath.Base(input)
		}
		return packInstalledMsg{name: name, err: err}
	}
}

var isRegistryName = cmdutil.IsRegistryName

// createPack scaffolds a new pack inside the packs directory and registers it.
func createPack(configDir, name string) tea.Cmd {
	return func() tea.Msg {
		if err := app.PackCreate(app.PackCreateRequest{
			Name:      name,
			ConfigDir: configDir,
			Local:     true,
		}); err != nil {
			return packCreatedMsg{name: name, err: err}
		}
		return packCreatedMsg{name: name}
	}
}

// removePack removes an installed pack.
func removePack(configDir, name string) tea.Cmd {
	return func() tea.Msg {
		result, err := app.PackDelete(configDir, name, io.Discard)
		if err != nil {
			return packRemovedMsg{name: name, err: err}
		}
		// TUI auto-removes bundled profiles without prompting — the user already
		// confirmed the pack removal via the destructive dialog.
		if len(result.BundledProfiles) > 0 {
			app.RemoveBundledProfiles(configDir, result.BundledProfiles, io.Discard)
		}
		return packRemovedMsg{name: name, err: nil}
	}
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
		err := app.PackApproveBundled(configDir, names, approved, io.Discard)
		return bundledApprovedMsg{err: err}
	}
}

// loadPackDrift runs app.DetectPackDrift over every installed pack and
// delivers the drifted entries as a packDriftLoadedMsg. Used by the packs
// tab to decorate list rows with a drift indicator on tab open. Errors
// loading the lockfile collapse to an empty drift set — drift is a
// passive UX hint, not a hard signal worth surfacing as an error.
func loadPackDrift(ctx context.Context, configDir string) tea.Cmd {
	return func() tea.Msg {
		lf, err := config.EnsureLockfileMigrated(configDir)
		if err != nil {
			return packDriftLoadedMsg{err: err}
		}
		drifted := app.DetectPackDrift(ctx, configDir, lf.Packs, nil)
		return packDriftLoadedMsg{drifted: drifted}
	}
}

// loadPackVersions queries the remote tags for a pack via app.PackListVersions
// and delivers the result as a packVersionsLoadedMsg. Used by the packs tab to
// populate the inline version list in the details panel and warm the cache for
// the install-time version picker.
//
// The interactive TUI budget is intentionally tight (tuiVersionLoadTimeout):
// a miss is cosmetic (no version badge), while a long wait blocks the render
// loop. gitLsRemoteRaw honors the ctx deadline we set here, so unreachable or
// credential-gated remotes fail fast instead of applying the 30s CLI default.
// A failed lookup is reported back to the cache as an error entry, not as a
// TUI-wide error.
func loadPackVersions(ctx context.Context, configDir, name string) tea.Cmd {
	return func() tea.Msg {
		tctx, cancel := context.WithTimeout(ctx, tuiVersionLoadTimeout)
		defer cancel()
		result, err := app.PackListVersions(tctx, app.PackListVersionsRequest{
			ConfigDir: configDir,
			Name:      name,
		})
		if err != nil {
			return packVersionsLoadedMsg{packName: name, err: err}
		}
		return packVersionsLoadedMsg{packName: name, versions: result.Versions}
	}
}

// tuiVersionLoadTimeout is the upper bound for a single pack's ls-remote tag
// probe when driven by the TUI. Five seconds is enough for a healthy fetch
// over a typical corporate VPN but short enough that a credential-gated or
// unreachable remote doesn't stall the render loop.
const tuiVersionLoadTimeout = 5 * time.Second

// updatePack updates one or all installed packs. ref is the value passed
// to PackUpdateRequest.Ref: empty preserves the current pin (default
// update behavior), any ref spec moves the pin (semver, commit, branch,
// namespaced tag), and "latest" clears the pin entirely (the unpin
// sentinel honored by app.PackUpdate).
func updatePack(ctx context.Context, configDir, name string, all bool, ref string, with domain.BundledSet) tea.Cmd {
	return func() tea.Msg {
		req := app.PackUpdateRequest{
			ConfigDir: configDir,
			Name:      name,
			All:       all,
			Ref:       ref,
			With:      with,
		}
		results, err := app.PackUpdate(ctx, req, io.Discard)
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

		for _, cat := range packTabCategories() {
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
func runSavePlan(ctx context.Context, eng *engine.Engine, configDir, profileName string, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		result, warnings, err := app.SaveRoundTripPlan(ctx, eng, configDir, reg)
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

// installFromSearch installs a pack by registry name and adds it to the
// active profile so it's immediately usable after the next sync.
func installFromSearch(ctx context.Context, configDir, name, profile string) tea.Cmd {
	return func() tea.Msg {
		entry, err := app.RegistryLookup(app.RegistryListRequest{ConfigDir: configDir}, name)
		if err != nil {
			return searchInstallMsg{name: name, err: fmt.Errorf("registry lookup for %q: %w", name, err)}
		}
		req := app.PackInstallRequest{
			ConfigDir: configDir,
			URL:       entry.Repo,
			SubPath:   entry.Path,
			Ref:       entry.Ref,
			Name:      name,
			Add:       true,
			Profile:   profile,
		}
		err = app.PackInstall(ctx, req, io.Discard)
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
func discoverVectors(ctx context.Context, harnessID domain.Harness, projectDir, home string, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		vectors, err := app.DiscoverContentVectorsAllScopes(ctx, harnessID, projectDir, home, reg)
		return vectorsDiscoveredMsg{vectors: vectors, err: err}
	}
}

// discoverSaveFiles runs capture + classification for one harness filtered to categories.
// Merges results from both project and global scopes.
func discoverSaveFiles(ctx context.Context, eng *engine.Engine, req app.DiscoverSaveRequest, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		candidates, warnings, err := app.DiscoverSaveFilesAllScopes(ctx, eng, req, reg)
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
func executeSavePipeline(eng *engine.Engine, req app.SavePipelineRequest, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		result, err := app.RunSavePipeline(eng, req, reg)
		return savePipelineDoneMsg{result: &result, err: err}
	}
}
