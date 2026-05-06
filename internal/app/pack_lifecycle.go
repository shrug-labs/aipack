package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"

	"gopkg.in/yaml.v3"
)

// PackDeleteRequest holds inputs for deleting an installed pack.
type PackDeleteRequest struct {
	ConfigDir    string
	Name         string
	KeepRendered bool
	DryRun       bool
	Registry     *harness.Registry
}

// PackDeleteResult holds post-deletion metadata for callers to act on.
type PackDeleteResult struct {
	Name                   string `json:"name"`
	DryRun                 bool   `json:"dry_run"`
	KeepRendered           bool   `json:"keep_rendered"`
	SourceRemoved          bool   `json:"source_removed"`
	RenderedRemoved        int    `json:"rendered_removed"`
	RenderedPreserved      int    `json:"rendered_preserved"`
	SharedSettingsStripped int    `json:"shared_settings_stripped"`
	LedgerCleared          int    `json:"ledger_cleared"`
	ProfilesEdited         int    `json:"profiles_edited"`
	LockfileCleared        bool   `json:"lockfile_cleared"`

	// BundledProfiles lists profile names that were bundled with this pack
	// and still exist after removal. Callers should prompt for confirmation
	// before deleting these.
	BundledProfiles []string `json:"bundled_profiles,omitempty"`
}

// PackDelete removes a pack from disk, profiles, lockfile, ledger, and rendered
// harness output. Use PackDeleteWithOptions with KeepRendered to retain rendered
// files while clearing aipack tracking.
func PackDelete(configDir string, name string, stdout io.Writer) (PackDeleteResult, error) {
	return PackDeleteWithOptions(engine.New(nil, nil), PackDeleteRequest{
		ConfigDir: configDir,
		Name:      name,
	}, stdout)
}

// PackDeleteWithOptions deletes a pack and optionally keeps rendered files.
func PackDeleteWithOptions(eng *engine.Engine, req PackDeleteRequest, stdout io.Writer) (PackDeleteResult, error) {
	name := strings.TrimSpace(req.Name)
	result := PackDeleteResult{Name: name, DryRun: req.DryRun, KeepRendered: req.KeepRendered}
	if stdout == nil {
		stdout = io.Discard
	}
	if name == "" {
		return result, fmt.Errorf("pack name is required")
	}
	if req.ConfigDir == "" {
		return result, fmt.Errorf("config dir is required")
	}
	if eng == nil {
		eng = engine.New(nil, nil)
	}
	registry := req.Registry
	destDir := filepath.Join(PacksDir(req.ConfigDir), name)
	sourceExists := false
	if _, err := os.Lstat(destDir); err == nil {
		sourceExists = true
	} else if !os.IsNotExist(err) {
		return result, fmt.Errorf("checking pack source: %w", err)
	}

	lf, err := config.EnsureLockfileMigrated(req.ConfigDir)
	if err != nil {
		return result, fmt.Errorf("loading lockfile: %w", err)
	}
	_, inLockfile := lf.Packs[name]
	if !sourceExists && !inLockfile {
		return result, fmt.Errorf("pack %q is not installed", name)
	}

	if sourceExists {
		// Load manifest before removing so we know which profiles were bundled.
		if manifest, err := config.LoadPackManifest(filepath.Join(destDir, "pack.json")); err == nil {
			result.BundledProfiles = packFindBundledProfiles(req.ConfigDir, manifest.Profiles)
		}
	}

	ledgerResult, err := cleanOrClearPackInAllLedgers(eng, registry, req.ConfigDir, name, req.DryRun, req.KeepRendered, stdout)
	if err != nil {
		return result, fmt.Errorf("clearing ledger entries: %w", err)
	}
	result.RenderedRemoved = ledgerResult.RenderedRemoved
	result.RenderedPreserved = ledgerResult.RenderedPreserved
	result.SharedSettingsStripped = ledgerResult.SharedSettingsStripped
	result.LedgerCleared = ledgerResult.LedgerCleared

	profilesEdited, err := countOrRemovePackFromAllProfiles(req.ConfigDir, name, req.DryRun, stdout)
	if err != nil {
		return result, fmt.Errorf("updating profiles: %w", err)
	}
	result.ProfilesEdited = profilesEdited

	if req.DryRun {
		action := "delete"
		if req.KeepRendered {
			action = "delete with rendered files retained"
		}
		fmt.Fprintf(stdout, "Would %s pack %q: remove %d rendered paths, preserve %d rendered paths, strip %d shared settings files, clear %d ledger entries, edit %d profiles, remove pack source, clear lockfile entry.\n",
			action, name, result.RenderedRemoved, result.RenderedPreserved, result.SharedSettingsStripped, result.LedgerCleared, profilesEdited)
		result.SourceRemoved = sourceExists
		result.LockfileCleared = inLockfile
		return result, nil
	}

	if sourceExists {
		if err := os.RemoveAll(destDir); err != nil {
			return result, fmt.Errorf("removing pack source: %w", err)
		}
		fmt.Fprintf(stdout, "Removed: %s\n", destDir)
		result.SourceRemoved = true
	}
	if inLockfile {
		if err := packClearOrigin(req.ConfigDir, name); err != nil {
			return result, fmt.Errorf("clearing lockfile entry: %w", err)
		}
		result.LockfileCleared = true
	}
	if err := clearDeletedPackFromIndex(req.ConfigDir, name); err != nil {
		fmt.Fprintf(stdout, "Warning: failed to clear search index for pack %q: %v\n", name, err)
	}
	if req.KeepRendered {
		fmt.Fprintf(stdout, "Deleted pack %q and kept rendered files unmanaged.\n", name)
	} else {
		suffix := "s"
		if result.RenderedRemoved == 1 {
			suffix = ""
		}
		fmt.Fprintf(stdout, "Deleted pack %q and removed %d rendered path%s.\n", name, result.RenderedRemoved, suffix)
	}
	return result, nil
}

func clearDeletedPackFromIndex(configDir, name string) error {
	dbPath := filepath.Join(configDir, "index.db")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	db, err := openIndexDB(configDir, "")
	if err != nil {
		return err
	}
	defer db.Close()
	return db.DeletePack(name)
}

// packFindBundledProfiles returns profile names (without extension) for bundled
// profiles declared in the pack manifest that still exist on disk.
func packFindBundledProfiles(configDir string, manifestProfiles []string) []string {
	if len(manifestProfiles) == 0 {
		return nil
	}
	profilesDir := filepath.Join(configDir, "profiles")
	var found []string
	for _, id := range manifestProfiles {
		dest := filepath.Join(profilesDir, id+".yaml")
		if _, err := os.Stat(dest); err == nil {
			found = append(found, id)
		}
	}
	return found
}

// RemoveBundledProfiles deletes bundled profile files by name, clearing the
// active-profile setting if needed. Delegates to ProfileDelete for each profile.
func RemoveBundledProfiles(configDir string, names []string, stdout io.Writer) {
	for _, name := range names {
		if err := ProfileDelete(ProfileDeleteRequest{ConfigDir: configDir, Name: name}); err != nil {
			fmt.Fprintf(stdout, "Warning: failed to remove bundled profile %q: %v\n", name, err)
			continue
		}
		fmt.Fprintf(stdout, "Removed bundled profile %q\n", name)
	}
}

// packRemoveFromProfile removes pack entries matching packName from a single profile file.
// Returns true if the profile was modified.
func packRemoveFromProfile(profilePath, packName string) (bool, error) {
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return false, err
	}

	modified := false

	// Remove matching pack entries.
	filteredPacks := cfg.Packs[:0]
	for _, p := range cfg.Packs {
		if p.Name == packName {
			modified = true
			continue
		}
		filteredPacks = append(filteredPacks, p)
	}
	cfg.Packs = filteredPacks

	if !modified {
		return false, nil
	}

	return true, saveProfile(profilePath, &cfg)
}

type packDeleteLedgerResult struct {
	RenderedRemoved        int
	RenderedPreserved      int
	SharedSettingsStripped int
	LedgerCleared          int
}

func (r *packDeleteLedgerResult) add(other packDeleteLedgerResult) {
	r.RenderedRemoved += other.RenderedRemoved
	r.RenderedPreserved += other.RenderedPreserved
	r.SharedSettingsStripped += other.SharedSettingsStripped
	r.LedgerCleared += other.LedgerCleared
}

// cleanOrClearPackInAllLedgers walks every ledger under configDir/ledger and
// clears entries with SourcePack == packName. Unless keepRendered is true, it
// removes only unmodified rendered files and strips managed overlays from shared
// settings files.
func cleanOrClearPackInAllLedgers(eng *engine.Engine, registry *harness.Registry, configDir, packName string, dryRun, keepRendered bool, stdout io.Writer) (packDeleteLedgerResult, error) {
	var result packDeleteLedgerResult
	ledgerDir := filepath.Join(configDir, "ledger")
	if _, err := os.Stat(ledgerDir); os.IsNotExist(err) {
		return result, nil
	}
	walkErr := filepath.WalkDir(ledgerDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		ledgerResult, lerr := cleanOrClearPackInLedger(packDeleteLedgerCleanRequest{
			eng:          eng,
			registry:     registry,
			path:         path,
			packName:     packName,
			dryRun:       dryRun,
			keepRendered: keepRendered,
			stdout:       stdout,
		})
		if lerr != nil {
			rel, _ := filepath.Rel(ledgerDir, path)
			fmt.Fprintf(stdout, "Warning: ledger %s: %s\n", rel, lerr)
			return nil
		}
		result.add(ledgerResult)
		if ledgerResult.LedgerCleared > 0 && !dryRun {
			rel, _ := filepath.Rel(ledgerDir, path)
			fmt.Fprintf(stdout, "Cleared %d ledger entries from %q\n", ledgerResult.LedgerCleared, rel)
		}
		return nil
	})
	if walkErr != nil {
		return result, walkErr
	}
	return result, nil
}

type packDeleteLedgerCleanRequest struct {
	eng          *engine.Engine
	registry     *harness.Registry
	path         string
	packName     string
	dryRun       bool
	keepRendered bool
	stdout       io.Writer
}

// packDeleteCtx bundles the constant context used across the pack-delete
// family of helpers (eng, hid, editor, packName, dryRun, stdout). Per-call
// variations (lg, path, entry, server-name sets) remain explicit.
type packDeleteCtx struct {
	eng      *engine.Engine
	hid      domain.Harness
	editor   harness.MCPManagedOverlayEditor
	packName string
	dryRun   bool
	stdout   io.Writer
}

func cleanOrClearPackInLedger(req packDeleteLedgerCleanRequest) (packDeleteLedgerResult, error) {
	var result packDeleteLedgerResult
	lg, warnings, err := req.eng.LoadLedger(req.path)
	if err != nil {
		return result, fmt.Errorf("load: %w", err)
	}
	if len(warnings) > 0 {
		return result, fmt.Errorf("load warning: %s", warnings[0])
	}
	hid, _ := harnessFromLedgerPath(req.path)
	editor, _ := harness.LookupMCPManagedOverlayEditor(req.registry, hid)
	ctx := packDeleteCtx{
		eng:      req.eng,
		hid:      hid,
		editor:   editor,
		packName: req.packName,
		dryRun:   req.dryRun,
		stdout:   req.stdout,
	}
	if !req.keepRendered {
		mcpServerNames := mcpServerNamesForPackInLedger(lg, req.packName)
		result.add(stripMCPSharedSettings(ctx, &lg, mcpServerNames))
	}
	ledgerDirty := !req.dryRun && result.SharedSettingsStripped > 0
	siblingMCP := siblingMCPServerNamesOutsidePackInLedger(lg, req.packName)
	for k, entry := range lg.Managed {
		if entry.SourcePack != req.packName {
			continue
		}
		deleteEntry := true
		if !req.keepRendered && !domain.IsMCPLedgerKey(k) {
			switch {
			case len(entry.ManagedOverlay) > 0:
				var stripped, preserved bool
				var stripErr error
				if len(siblingMCP) > 0 {
					var retained bool
					stripped, preserved, ledgerDirty, retained, stripErr = stripPreservingSiblingMCP(ctx, lg, k, entry, siblingMCP, ledgerDirty)
					deleteEntry = !retained
					if !retained && stripErr == nil {
						stripped, preserved, stripErr = stripSharedSettings(ctx, lg, k, entry)
					}
				} else {
					stripped, preserved, stripErr = stripSharedSettings(ctx, lg, k, entry)
				}
				if stripErr != nil {
					preserved = true
					fmt.Fprintf(ctx.stdout, "Warning: preserved shared settings path %q unmanaged: %v\n", k, stripErr)
				}
				if stripped {
					result.SharedSettingsStripped++
				}
				if preserved {
					result.RenderedPreserved++
				}
			default:
				removed, preserved, removeErr := removeRenderedPathForPackDelete(ctx.eng, k, entry, ctx.dryRun)
				if removeErr != nil {
					preserved = true
					fmt.Fprintf(ctx.stdout, "Warning: preserved rendered path %q unmanaged: %v\n", k, removeErr)
				}
				if removed {
					result.RenderedRemoved++
				}
				if preserved {
					result.RenderedPreserved++
				}
			}
		} else if req.keepRendered && !domain.IsMCPLedgerKey(k) {
			if _, statErr := ctx.eng.FS.Stat(k); statErr == nil {
				result.RenderedPreserved++
			}
		}
		if deleteEntry {
			result.LedgerCleared++
		}
		if !req.dryRun && deleteEntry {
			delete(lg.Managed, k)
			ledgerDirty = true
		}
	}
	if !ledgerDirty || req.dryRun {
		return result, nil
	}
	if err := ctx.eng.SaveLedger(req.path, lg, false); err != nil {
		return result, fmt.Errorf("save: %w", err)
	}
	return result, nil
}

func mcpServerNamesForPackInLedger(lg domain.Ledger, packName string) map[string]struct{} {
	out := map[string]struct{}{}
	for k, entry := range lg.Managed {
		if entry.SourcePack != packName || !domain.IsMCPLedgerKey(k) {
			continue
		}
		_, serverName, ok := splitMCPLedgerKey(k)
		if !ok {
			continue
		}
		out[serverName] = struct{}{}
	}
	return out
}

func siblingMCPServerNamesOutsidePackInLedger(lg domain.Ledger, packName string) map[string]struct{} {
	out := map[string]struct{}{}
	for k, entry := range lg.Managed {
		if entry.SourcePack == packName || !domain.IsMCPLedgerKey(k) {
			continue
		}
		_, serverName, ok := splitMCPLedgerKey(k)
		if !ok {
			continue
		}
		out[serverName] = struct{}{}
	}
	return out
}

func splitMCPLedgerKey(key string) (configPath string, serverName string, ok bool) {
	key = filepath.Clean(key)
	const marker = "#mcp:"
	idx := strings.LastIndex(key, marker)
	if idx < 0 || idx+len(marker) >= len(key) {
		return "", "", false
	}
	return filepath.Clean(key[:idx]), key[idx+len(marker):], true
}

func stripMCPSharedSettings(ctx packDeleteCtx, lg *domain.Ledger, serverNames map[string]struct{}) packDeleteLedgerResult {
	var result packDeleteLedgerResult
	if len(serverNames) == 0 || ctx.editor == nil {
		return result
	}
	for sharedPath, entry := range lg.Managed {
		if domain.IsMCPLedgerKey(sharedPath) || len(entry.ManagedOverlay) == 0 || entry.SourcePack == ctx.packName {
			continue
		}
		nextOverlay, changed, err := ctx.editor.PruneMCPServersFromManagedOverlay(entry.ManagedOverlay, serverNames)
		if err != nil {
			result.RenderedPreserved++
			fmt.Fprintf(ctx.stdout, "Warning: preserved shared settings path %q unmanaged: %v\n", sharedPath, err)
			continue
		}
		if !changed {
			continue
		}
		stripped, preserved, _, err := applySharedSettingsOverlay(ctx, *lg, sharedPath, nextOverlay, entry.SourcePack)
		if err != nil {
			result.RenderedPreserved++
			fmt.Fprintf(ctx.stdout, "Warning: preserved shared settings path %q unmanaged: %v\n", sharedPath, err)
			continue
		}
		if stripped {
			result.SharedSettingsStripped++
		}
		if preserved {
			result.RenderedPreserved++
		}
	}
	return result
}

func stripPreservingSiblingMCP(ctx packDeleteCtx, lg domain.Ledger, path string, entry domain.Entry, serverNames map[string]struct{}, ledgerDirty bool) (stripped, preserved, dirty, retained bool, err error) {
	if ctx.editor == nil {
		return false, true, ledgerDirty, false, fmt.Errorf("no managed overlay editor for shared settings")
	}
	nextOverlay, err := ctx.editor.RetainMCPServersInManagedOverlay(entry.ManagedOverlay, serverNames)
	if err != nil {
		return false, true, ledgerDirty, false, err
	}
	if managedOverlayEmpty(ctx.editor, nextOverlay) {
		return false, false, ledgerDirty, false, nil
	}
	stripped, preserved, dirty, err = applySharedSettingsOverlay(ctx, lg, path, nextOverlay, "")
	return stripped, preserved, ledgerDirty || dirty, true, err
}

func managedOverlayEmpty(editor harness.MCPManagedOverlayEditor, overlay []byte) bool {
	return len(bytes.TrimSpace(overlay)) == 0 || bytes.Equal(bytes.TrimSpace(overlay), bytes.TrimSpace(editor.EmptyManagedOverlay()))
}

func applySharedSettingsOverlay(ctx packDeleteCtx, lg domain.Ledger, path string, nextOverlay []byte, sourcePack string) (stripped, preserved, dirty bool, err error) {
	if _, err := ctx.eng.FS.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, false, false, nil
		}
		return false, true, false, err
	}
	diffs, err := ctx.eng.ComputeSettingsDiffs([]domain.SettingsAction{{
		Dst:        path,
		Desired:    nextOverlay,
		Harness:    ctx.hid,
		Label:      filepath.Base(path),
		SourcePack: sourcePack,
		MergeMode:  true,
	}}, lg)
	if err != nil {
		return false, true, false, err
	}
	if len(diffs) == 0 {
		return false, false, false, nil
	}
	changedOnDisk := diffs[0].Kind != domain.DiffIdentical
	if ctx.dryRun {
		return changedOnDisk, false, false, nil
	}
	if changedOnDisk {
		if err := ctx.eng.FS.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, true, false, err
		}
		if err := ctx.eng.FS.WriteFile(path, diffs[0].Desired, 0o644); err != nil {
			return false, true, false, err
		}
	}
	lg.Record(path, diffs[0].Desired, sourcePack, diffs[0].ManagedOverlay, time.Now())
	return changedOnDisk, false, true, nil
}

func harnessFromLedgerPath(path string) (domain.Harness, bool) {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return domain.ParseHarness(name)
}

func stripSharedSettings(ctx packDeleteCtx, lg domain.Ledger, path string, entry domain.Entry) (stripped, preserved bool, err error) {
	if ctx.editor == nil {
		return false, true, fmt.Errorf("no managed overlay editor for shared settings")
	}
	stripped, preserved, _, err = applySharedSettingsOverlay(ctx, lg, path, ctx.editor.EmptyManagedOverlay(), entry.SourcePack)
	return stripped, preserved, err
}

func removeRenderedPathForPackDelete(eng *engine.Engine, path string, entry domain.Entry, dryRun bool) (removed bool, preserved bool, err error) {
	st, err := eng.FS.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, true, err
	}
	if entry.Digest == "" {
		return false, true, nil
	}
	if !isPackRenderedRemovalPath(path) {
		return false, true, nil
	}
	digest, err := eng.PathDigest(path)
	if err != nil {
		return false, true, err
	}
	if digest != entry.Digest {
		return false, true, nil
	}
	if dryRun {
		return true, false, nil
	}
	if st.IsDir() {
		if _, ok := eng.FS.(engine.OSFS); ok {
			if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
				return false, true, err
			}
			return true, false, nil
		}
	}
	if err := eng.FS.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, true, err
	}
	return true, false, nil
}

func isPackRenderedRemovalPath(path string) bool {
	p := filepath.ToSlash(filepath.Clean(path))
	dirMarkers := []string{
		"/.claude/rules/",
		"/.claude/agents/",
		"/.claude/commands/",
		"/.claude/skills/",
		"/.opencode/rules/",
		"/.opencode/agents/",
		"/.opencode/commands/",
		"/.opencode/skills/",
		"/.config/opencode/rules/",
		"/.config/opencode/agents/",
		"/.config/opencode/commands/",
		"/.config/opencode/skills/",
		"/.agents/skills/",
		"/.codex/agents/",
		"/.clinerules/",
		"/Cline/Rules/",
		"/Cline/Workflows/",
	}
	for _, marker := range dirMarkers {
		if strings.Contains(p, marker) {
			return true
		}
	}
	return strings.HasSuffix(p, "/AGENTS.override.md") ||
		strings.HasSuffix(p, "/.codex/AGENTS.override.md")
}

// countOrRemovePackFromAllProfiles iterates every profile YAML in configDir/profiles/
// and either counts profiles containing packName or removes the entry. Returns
// the count of profiles touched (counted in dry-run, removed otherwise).
func countOrRemovePackFromAllProfiles(configDir, packName string, dryRun bool, stdout io.Writer) (int, error) {
	profilesDir := filepath.Join(configDir, "profiles")
	names, err := config.ListProfileNames(profilesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, name := range names {
		profilePath := filepath.Join(profilesDir, name+".yaml")
		if dryRun {
			cfg, err := config.LoadProfile(profilePath)
			if err != nil {
				continue
			}
			for _, p := range cfg.Packs {
				if p.Name == packName {
					count++
					break
				}
			}
			continue
		}
		modified, err := packRemoveFromProfile(profilePath, packName)
		if err != nil {
			fmt.Fprintf(stdout, "Warning: failed to update profile %q: %v\n", name, err)
			continue
		}
		if modified {
			fmt.Fprintf(stdout, "Removed pack %q from profile %q\n", packName, name)
			count++
		}
	}
	return count, nil
}

// PackRename renames a pack across all config: directory, manifest, sync-config,
// profiles, and ledger files. Rolls back the directory rename on failure.
func PackRename(eng *engine.Engine, configDir, oldName, newName string, stdout io.Writer) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || newName == "" {
		return fmt.Errorf("old and new pack names are required")
	}
	if oldName == newName {
		return fmt.Errorf("old and new names are the same")
	}
	if strings.ContainsAny(newName, "/\\\x00") || strings.Contains(newName, "..") {
		return fmt.Errorf("invalid pack name %q", newName)
	}

	packsDir := PacksDir(configDir)
	oldDir := filepath.Join(packsDir, oldName)
	newDir := filepath.Join(packsDir, newName)

	if _, err := os.Lstat(oldDir); os.IsNotExist(err) {
		return fmt.Errorf("pack %q is not installed", oldName)
	}
	if _, err := os.Lstat(newDir); err == nil {
		return fmt.Errorf("pack %q already exists", newName)
	}

	// 1. Rename directory.
	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("renaming pack directory: %w", err)
	}
	rollback := func() { _ = os.Rename(newDir, oldDir) }

	// 2. Update pack.json name field.
	manifestPath := filepath.Join(newDir, "pack.json")
	manifest, err := config.LoadPackManifest(manifestPath)
	if err != nil {
		rollback()
		return fmt.Errorf("loading manifest after rename: %w", err)
	}
	manifest.Name = newName
	if err := config.SavePackManifest(manifestPath, manifest); err != nil {
		rollback()
		return fmt.Errorf("saving manifest: %w", err)
	}

	// 3. Update lockfile.
	if err := packRenameInLockfile(configDir, oldName, newName); err != nil {
		rollback()
		return fmt.Errorf("updating lockfile: %w", err)
	}

	// 4. Update all profiles.
	packRenameInAllProfiles(configDir, oldName, newName, stdout)

	// 5. Update all ledger files.
	packRenameInAllLedgers(eng, configDir, oldName, newName, stdout)

	fmt.Fprintf(stdout, "Renamed pack %q → %q\n", oldName, newName)
	return nil
}

func packRenameInLockfile(configDir, oldName, newName string) error {
	return mutateLockfile(configDir, func(lf *config.Lockfile) error {
		meta, ok := lf.Packs[oldName]
		if !ok {
			return nil
		}
		delete(lf.Packs, oldName)
		// Rebuild inventory under the new name so a subsequent sync
		// reads a fresh baseline. Content didn't change, but the
		// inventory's name key follows the rename — and if the old
		// entry had no Resolved block (pre-v0.22 install), this also
		// backfills it opportunistically.
		packDir := filepath.Join(configDir, "packs", newName)
		if inv := buildResolvedInventory(configDir, newName, packDir, meta.Ref, time.Now(), nil); inv != nil {
			meta.Resolved = inv
		}
		lf.Packs[newName] = meta
		return nil
	})
}

func packRenameInAllProfiles(configDir, oldName, newName string, stdout io.Writer) {
	profilesDir := filepath.Join(configDir, "profiles")
	names, err := config.ListProfileNames(profilesDir)
	if err != nil {
		return
	}
	for _, name := range names {
		profilePath := filepath.Join(profilesDir, name+".yaml")
		modified, err := packRenameInProfile(profilePath, oldName, newName)
		if err != nil {
			fmt.Fprintf(stdout, "Warning: failed to update profile %q: %v\n", name, err)
			continue
		}
		if modified {
			fmt.Fprintf(stdout, "Updated profile %q: %s → %s\n", name, oldName, newName)
		}
	}
}

func packRenameInProfile(profilePath, oldName, newName string) (bool, error) {
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return false, err
	}
	modified := false
	for i := range cfg.Packs {
		if cfg.Packs[i].Name == oldName {
			cfg.Packs[i].Name = newName
			modified = true
		}
	}
	if !modified {
		return false, nil
	}
	return true, saveProfile(profilePath, &cfg)
}

func packRenameInAllLedgers(eng *engine.Engine, configDir, oldName, newName string, stdout io.Writer) {
	ledgerDir := filepath.Join(configDir, "ledger")
	entries, err := os.ReadDir(ledgerDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(ledgerDir, e.Name())
		updated, lerr := packRenameInLedger(eng, path, oldName, newName)
		if lerr != nil {
			fmt.Fprintf(stdout, "Warning: ledger %s: %s\n", e.Name(), lerr)
		} else if updated {
			fmt.Fprintf(stdout, "Updated ledger %q: %s → %s\n", e.Name(), oldName, newName)
		}
	}
}

func packRenameInLedger(eng *engine.Engine, path, oldName, newName string) (bool, error) {
	lg, warnings, err := eng.LoadLedger(path)
	if err != nil {
		return false, fmt.Errorf("load: %w", err)
	}
	if len(warnings) > 0 {
		return false, fmt.Errorf("load warning: %s", warnings[0])
	}
	modified := false
	for k, entry := range lg.Managed {
		if entry.SourcePack == oldName {
			entry.SourcePack = newName
			lg.Managed[k] = entry
			modified = true
		}
	}
	if !modified {
		return false, nil
	}
	if err := eng.SaveLedger(path, lg, false); err != nil {
		return false, fmt.Errorf("save: %w", err)
	}
	return true, nil
}

// boolPtrIf returns &true when v is true, else nil. Used to convert a
// registry-entry `Quiet bool` hint into the PackInstallRequest tri-state
// override — a false hint means "no opinion," so nil propagates and the
// lockfile inheritance rule applies.
func boolPtrIf(v bool) *bool {
	if !v {
		return nil
	}
	return &v
}

// packRecordOrigin saves the install metadata for a pack in the lockfile.
func packRecordOrigin(configDir, name string, meta config.InstalledPackMeta) error {
	return mutateLockfile(configDir, func(lf *config.Lockfile) error {
		lf.Packs[name] = meta
		return nil
	})
}

// buildResolvedInventory is a best-effort wrapper around
// engine.BuildPackInventory used at install/update/rename time to keep the
// lockfile's Resolved baseline populated without waiting for the first
// sync. Emits a warning on failure and returns nil — the caller should
// record install metadata either way; a missing Resolved block just
// degrades drift detection and doctor broken_refs until the next sync
// rebuilds it. Stamps CapturedAtRef so drift reports can show the
// version transition on the next run.
func buildResolvedInventory(configDir, name, packRoot, ref string, now time.Time, stdout io.Writer) *domain.PackInventory {
	inv, err := engine.BuildPackInventory(configDir, domain.Pack{Name: name, Root: packRoot}, now)
	if err != nil {
		if stdout != nil {
			fmt.Fprintf(stdout, "Warning: failed to build inventory for %s: %v\n", name, err)
		}
		return nil
	}
	inv.CapturedAtRef = ref
	return &inv
}

// packClearOrigin removes the install metadata for a pack from the lockfile.
func packClearOrigin(configDir, name string) error {
	return mutateLockfile(configDir, func(lf *config.Lockfile) error {
		delete(lf.Packs, name)
		return nil
	})
}

// mutateLockfile loads the lockfile (running migration first), applies fn,
// and saves it. If fn returns an error, the lockfile is not written.
func mutateLockfile(configDir string, fn func(*config.Lockfile) error) error {
	lf, err := config.EnsureLockfileMigrated(configDir)
	if err != nil {
		return err
	}
	if err := fn(&lf); err != nil {
		return err
	}
	return config.SaveLockfile(config.LockfilePath(configDir), lf)
}

// saveProfile marshals and atomically writes a profile config.
func saveProfile(profilePath string, cfg *config.ProfileConfig) error {
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling profile: %w", err)
	}
	return util.WriteFileAtomicWithPerms(profilePath, out, 0o700, 0o600)
}

// PackAdd adds a pack entry to the named profile.
//
// quietOverride controls whether the entry is marked quiet:
//
//   - nil — inherit from the lockfile's InstallQuiet. New entries take the
//     lockfile value; existing entries are not touched.
//   - *true — force quiet. New entries are created quiet; existing entries
//     are promoted to quiet.
//   - *false — force non-quiet. New entries are non-quiet; existing entries
//     are demoted from quiet. Surfaced by `--no-quiet` on `pack add` /
//     `pack install --add` when the user wants to override a pack that
//     was installed quietly.
//
// Inheriting from the lockfile is the feature that makes `-q` at install
// time persist: once the lockfile records InstallQuiet=true, every profile
// add defaults to quiet without re-specifying the flag.
func PackAdd(configDir string, profileName string, packName string, quietOverride *bool, stdout io.Writer) error {
	profilePath := filepath.Join(configDir, "profiles", profileName+".yaml")

	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("profile %q does not exist at %s (run 'aipack init' first)", profileName, profilePath)
		}
		return fmt.Errorf("parsing profile: %w", err)
	}

	// Compute effective quiet for a new entry: explicit override wins,
	// otherwise inherit the install-time InstallQuiet from the lockfile.
	effectiveQuiet := false
	if quietOverride != nil {
		effectiveQuiet = *quietOverride
	} else if lf, lfErr := config.EnsureLockfileMigrated(configDir); lfErr == nil {
		if meta, ok := lf.Packs[packName]; ok {
			effectiveQuiet = meta.InstallQuiet
		}
	}

	packExists := false
	modified := false
	for i, p := range cfg.Packs {
		if p.Name == packName {
			packExists = true
			if quietOverride != nil && cfg.Packs[i].Quiet != *quietOverride {
				cfg.Packs[i].Quiet = *quietOverride
				modified = true
			}
			break
		}
	}
	if !packExists {
		enabled := true
		cfg.Packs = append(cfg.Packs, config.PackEntry{
			Name:    packName,
			Enabled: &enabled,
			Quiet:   effectiveQuiet,
		})
		modified = true
	}

	if modified {
		warnBundledProfileEdit(reqConfigDirFromProfilePath(profilePath), profileName, stdout)
		if err := saveProfile(profilePath, &cfg); err != nil {
			return err
		}
	}

	var detail string
	if !packExists {
		label := "pack"
		if effectiveQuiet {
			label = "quiet pack"
		}
		detail = fmt.Sprintf("Added %s %q to profile %q\n", label, packName, profileName)
	} else {
		detail = fmt.Sprintf("Pack %q already in profile %q\n", packName, profileName)
	}
	if stdout != nil {
		fmt.Fprint(stdout, detail)
	}
	return nil
}

// PackRemove removes a pack entry from the named profile.
func PackRemove(configDir string, profileName string, packName string, stdout io.Writer) error {
	profilePath := filepath.Join(configDir, "profiles", profileName+".yaml")

	modified, err := packRemoveFromProfile(profilePath, packName)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("profile %q does not exist at %s", profileName, profilePath)
		}
		return fmt.Errorf("updating profile: %w", err)
	}
	if modified {
		warnBundledProfileEdit(filepath.Dir(filepath.Dir(profilePath)), profileName, stdout)
		fmt.Fprintf(stdout, "Removed pack %q from profile %q\n", packName, profileName)
		return nil
	}

	fmt.Fprintf(stdout, "Pack %q not found in profile %q\n", packName, profileName)
	return nil
}

// PackEnable sets enabled: true on an existing pack entry in the named profile.
func PackEnable(configDir string, profileName string, packName string, stdout io.Writer) error {
	profilePath := filepath.Join(configDir, "profiles", profileName+".yaml")
	return packSetEnabled(profilePath, profileName, packName, true, stdout)
}

// PackDisable sets enabled: false on an existing pack entry in the named profile.
func PackDisable(configDir string, profileName string, packName string, stdout io.Writer) error {
	profilePath := filepath.Join(configDir, "profiles", profileName+".yaml")
	return packSetEnabled(profilePath, profileName, packName, false, stdout)
}

// packSetEnabled toggles the enabled field on an existing pack entry in a profile.
func packSetEnabled(profilePath, profileName, packName string, enabled bool, stdout io.Writer) error {
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("profile %q does not exist at %s", profileName, profilePath)
		}
		return fmt.Errorf("parsing profile: %w", err)
	}

	found := false
	for i, p := range cfg.Packs {
		if p.Name == packName {
			e := enabled
			cfg.Packs[i].Enabled = &e
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("pack %q not found in profile %q (use 'aipack pack add' first)", packName, profileName)
	}

	if err := saveProfile(profilePath, &cfg); err != nil {
		return err
	}

	warnBundledProfileEdit(filepath.Dir(filepath.Dir(profilePath)), profileName, stdout)
	verb := "Enabled"
	if !enabled {
		verb = "Disabled"
	}
	fmt.Fprintf(stdout, "%s pack %q in profile %q\n", verb, packName, profileName)
	return nil
}

func warnBundledProfileEdit(configDir, profileName string, stdout io.Writer) {
	if stdout == nil || configDir == "" {
		return
	}
	owners := ProfileBundledOwners(configDir, profileName)
	if len(owners) == 0 {
		return
	}
	fmt.Fprintf(stdout, "Warning: %q is a pack-provided profile from %s; duplicate it before editing because pack update can overwrite it.\n", profileName, strings.Join(owners, ", "))
}

func reqConfigDirFromProfilePath(profilePath string) string {
	return filepath.Dir(filepath.Dir(profilePath))
}

// PackInstallMissingRequest holds the inputs for installing packs missing from a profile.
type PackInstallMissingRequest struct {
	ConfigDir   string
	ProfileName string

	// PackInstallFn overrides PackInstall for testing. nil = use PackInstall.
	PackInstallFn func(context.Context, PackInstallRequest, io.Writer) error
}

// MissingStatus is the outcome of reconciling a single profile-referenced
// pack against the on-disk packs directory and the merged registry.
type MissingStatus string

const (
	MissingStatusInstalled     MissingStatus = "installed"
	MissingStatusPresent       MissingStatus = "present"
	MissingStatusNotInRegistry MissingStatus = "not-in-registry"
	MissingStatusError         MissingStatus = "error"
)

// PackInstallMissingResult describes the outcome for a single pack in the profile.
type PackInstallMissingResult struct {
	Pack   string
	Status MissingStatus
	Detail string // error message for MissingStatusError / MissingStatusNotInRegistry
}

// ProfileMissingPacks returns the names of enabled packs in a profile whose
// directories do not exist under configDir/packs/.
func ProfileMissingPacks(configDir, profileName string) ([]string, error) {
	profilePath := filepath.Join(configDir, "profiles", profileName+".yaml")
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return nil, fmt.Errorf("loading profile %q: %w", profileName, err)
	}

	packsDir := PacksDir(configDir)
	var missing []string
	for _, pe := range cfg.Packs {
		name := strings.TrimSpace(pe.Name)
		if name == "" {
			continue
		}
		if pe.Enabled != nil && !*pe.Enabled {
			continue
		}
		packDir := filepath.Join(packsDir, name)
		if _, err := os.Stat(packDir); os.IsNotExist(err) {
			missing = append(missing, name)
		}
	}
	return missing, nil
}

// PackInstallMissing installs packs that a profile declares but that are not
// present on disk. Each missing pack is looked up in the merged registry and
// installed via PackInstall. Packs not found in the registry are reported but
// do not cause a failure.
func PackInstallMissing(ctx context.Context, req PackInstallMissingRequest, stdout io.Writer) ([]PackInstallMissingResult, error) {
	profilePath := filepath.Join(req.ConfigDir, "profiles", req.ProfileName+".yaml")
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return nil, fmt.Errorf("loading profile %q: %w", req.ProfileName, err)
	}

	packsDir := PacksDir(req.ConfigDir)
	regReq := RegistryListRequest{ConfigDir: req.ConfigDir}
	installFn := req.PackInstallFn
	if installFn == nil {
		installFn = PackInstall
	}

	var (
		reg       config.Registry
		regErr    error
		regLoaded bool
	)
	loadRegistry := func() (config.Registry, error) {
		if !regLoaded {
			reg, regErr = loadRegistryForRequest(regReq)
			regLoaded = true
		}
		return reg, regErr
	}

	var results []PackInstallMissingResult
	for _, pe := range cfg.Packs {
		name := strings.TrimSpace(pe.Name)
		if name == "" {
			continue
		}
		if pe.Enabled != nil && !*pe.Enabled {
			continue
		}

		packDir := filepath.Join(packsDir, name)
		if _, err := os.Stat(packDir); err == nil {
			results = append(results, PackInstallMissingResult{
				Pack: name, Status: MissingStatusPresent,
			})
			continue
		}

		reg, regErr := loadRegistry()
		if regErr != nil {
			results = append(results, PackInstallMissingResult{
				Pack: name, Status: MissingStatusNotInRegistry, Detail: regErr.Error(),
			})
			continue
		}
		entry, ok := reg.Packs[name]
		if !ok {
			detail := fmt.Sprintf("pack %q not found in registry", name)
			if hint := missingRegistryPackHint(regReq, name); hint != "" {
				detail = fmt.Sprintf("%s. %s", detail, hint)
			}
			results = append(results, PackInstallMissingResult{
				Pack: name, Status: MissingStatusNotInRegistry, Detail: detail,
			})
			continue
		}

		installReq := PackInstallRequestFromRegistryEntry(req.ConfigDir, name, entry)
		if installErr := installFn(ctx, installReq, stdout); installErr != nil {
			results = append(results, PackInstallMissingResult{
				Pack: name, Status: MissingStatusError, Detail: installErr.Error(),
			})
			continue
		}

		results = append(results, PackInstallMissingResult{
			Pack: name, Status: MissingStatusInstalled,
		})
	}

	return results, nil
}
