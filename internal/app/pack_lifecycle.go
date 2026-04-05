package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/util"

	"gopkg.in/yaml.v3"
)

// PackRemove uninstalls a pack and deregisters it from all profiles.
// PackRemoveResult holds post-removal metadata for callers to act on.
type PackRemoveResult struct {
	// BundledProfiles lists profile names that were bundled with this pack
	// and still exist after removal. Callers should prompt for confirmation
	// before deleting these.
	BundledProfiles []string
}

func PackRemove(configDir string, name string, stdout io.Writer) (PackRemoveResult, error) {
	var result PackRemoveResult

	if strings.TrimSpace(name) == "" {
		return result, fmt.Errorf("pack name is required")
	}
	packsDir := PacksDir(configDir)
	destDir := filepath.Join(packsDir, name)

	if _, err := os.Lstat(destDir); os.IsNotExist(err) {
		return result, fmt.Errorf("pack %q is not installed", name)
	}

	// Load manifest before removing so we know which profiles were bundled.
	if manifest, err := config.LoadPackManifest(filepath.Join(destDir, "pack.json")); err == nil {
		result.BundledProfiles = packFindBundledProfiles(configDir, manifest.Profiles)
	}

	if err := os.RemoveAll(destDir); err != nil {
		return result, fmt.Errorf("removing pack: %w", err)
	}
	fmt.Fprintf(stdout, "Removed: %s\n", destDir)

	// Best-effort origin cleanup.
	_ = packClearOrigin(configDir, name)

	// Best-effort deregister from all profiles.
	packDeregisterFromAllProfiles(configDir, name, stdout)

	return result, nil
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

// packDeregisterFromAllProfiles removes source and pack entries for a given pack
// name from every profile YAML in configDir/profiles/.
func packDeregisterFromAllProfiles(configDir, packName string, stdout io.Writer) {
	profilesDir := filepath.Join(configDir, "profiles")
	names, err := config.ListProfileNames(profilesDir)
	if err != nil {
		return
	}
	for _, name := range names {
		profilePath := filepath.Join(profilesDir, name+".yaml")
		if packDeregister(profilePath, packName) {
			fmt.Fprintf(stdout, "Deregistered pack %q from profile %q\n", packName, name)
		}
	}
}

// packDeregister removes pack entries matching packName from a single profile file.
// Returns true if the profile was modified.
func packDeregister(profilePath, packName string) bool {
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return false
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
		return false
	}

	out, err := yaml.Marshal(&cfg)
	if err != nil {
		return false
	}
	_ = util.WriteFileAtomicWithPerms(profilePath, out, 0o700, 0o600)
	return true
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

	// 3. Update sync-config.
	if err := packRenameInSyncConfig(configDir, oldName, newName); err != nil {
		rollback()
		return fmt.Errorf("updating sync-config: %w", err)
	}

	// 4. Update all profiles.
	packRenameInAllProfiles(configDir, oldName, newName, stdout)

	// 5. Update all ledger files.
	packRenameInAllLedgers(eng, configDir, oldName, newName, stdout)

	fmt.Fprintf(stdout, "Renamed pack %q → %q\n", oldName, newName)
	return nil
}

func packRenameInSyncConfig(configDir, oldName, newName string) error {
	scPath := config.SyncConfigPath(configDir)
	sc, err := config.LoadSyncConfig(scPath)
	if err != nil {
		return err
	}
	if sc.InstalledPacks == nil {
		return nil
	}
	meta, ok := sc.InstalledPacks[oldName]
	if !ok {
		return nil
	}
	delete(sc.InstalledPacks, oldName)
	sc.InstalledPacks[newName] = meta
	return config.SaveSyncConfig(scPath, sc)
}

func packRenameInAllProfiles(configDir, oldName, newName string, stdout io.Writer) {
	profilesDir := filepath.Join(configDir, "profiles")
	names, err := config.ListProfileNames(profilesDir)
	if err != nil {
		return
	}
	for _, name := range names {
		profilePath := filepath.Join(profilesDir, name+".yaml")
		if packRenameInProfile(profilePath, oldName, newName) {
			fmt.Fprintf(stdout, "Updated profile %q: %s → %s\n", name, oldName, newName)
		}
	}
}

func packRenameInProfile(profilePath, oldName, newName string) bool {
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return false
	}
	modified := false
	for i := range cfg.Packs {
		if cfg.Packs[i].Name == oldName {
			cfg.Packs[i].Name = newName
			modified = true
		}
	}
	if !modified {
		return false
	}
	out, err := yaml.Marshal(&cfg)
	if err != nil {
		return false
	}
	_ = util.WriteFileAtomicWithPerms(profilePath, out, 0o700, 0o600)
	return true
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

// packRecordOrigin saves the install metadata for a pack in sync-config.
func packRecordOrigin(configDir, name string, meta config.InstalledPackMeta) error {
	scPath := config.SyncConfigPath(configDir)
	sc, err := config.LoadSyncConfig(scPath)
	if err != nil {
		return err
	}
	if sc.InstalledPacks == nil {
		sc.InstalledPacks = make(map[string]config.InstalledPackMeta)
	}
	sc.InstalledPacks[name] = meta
	return config.SaveSyncConfig(scPath, sc)
}

// packClearOrigin removes the install metadata for a pack from sync-config.
func packClearOrigin(configDir, name string) error {
	scPath := config.SyncConfigPath(configDir)
	sc, err := config.LoadSyncConfig(scPath)
	if err != nil {
		return err
	}
	if sc.InstalledPacks == nil {
		return nil
	}
	delete(sc.InstalledPacks, name)
	return config.SaveSyncConfig(scPath, sc)
}

// PackRegister adds a pack entry to the named profile.
func PackRegister(configDir string, profileName string, packName string, quiet bool, stdout io.Writer) error {
	profilePath := filepath.Join(configDir, "profiles", profileName+".yaml")

	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("profile %q does not exist at %s (run 'aipack init' first)", profileName, profilePath)
		}
		return fmt.Errorf("parsing profile: %w", err)
	}

	// Check if pack already exists; set Quiet only when explicitly requested.
	packExists := false
	for i, p := range cfg.Packs {
		if p.Name == packName {
			packExists = true
			if quiet {
				cfg.Packs[i].Quiet = true
			}
			break
		}
	}
	if !packExists {
		enabled := true
		cfg.Packs = append(cfg.Packs, config.PackEntry{
			Name:    packName,
			Enabled: &enabled,
			Quiet:   quiet,
		})
	}

	out, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshalling profile: %w", err)
	}

	if err := util.WriteFileAtomicWithPerms(profilePath, out, 0o700, 0o600); err != nil {
		return err
	}

	if !packExists {
		label := "pack"
		if quiet {
			label = "quiet pack"
		}
		fmt.Fprintf(stdout, "Registered %s %q in profile %q\n", label, packName, profileName)
	} else {
		fmt.Fprintf(stdout, "Pack %q already registered in profile %q\n", packName, profileName)
	}
	return nil
}

// PackDeregister removes a pack entry from the named profile.
func PackDeregister(configDir string, profileName string, packName string, stdout io.Writer) error {
	profilePath := filepath.Join(configDir, "profiles", profileName+".yaml")

	if packDeregister(profilePath, packName) {
		fmt.Fprintf(stdout, "Removed pack %q from profile %q\n", packName, profileName)
		return nil
	}

	// Check if profile exists at all.
	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		return fmt.Errorf("profile %q does not exist at %s", profileName, profilePath)
	}

	fmt.Fprintf(stdout, "Pack %q not found in profile %q\n", packName, profileName)
	return nil
}

// PackInstallMissingRequest holds the inputs for installing packs missing from a profile.
type PackInstallMissingRequest struct {
	ConfigDir   string
	ProfileName string

	// PackInstallFn overrides PackInstall for testing. nil = use PackInstall.
	PackInstallFn func(context.Context, PackInstallRequest, io.Writer) error
}

// PackInstallMissingResult describes the outcome for a single pack in the profile.
type PackInstallMissingResult struct {
	Pack   string // pack name from profile
	Status string // "installed", "present", "not-in-registry", "error"
	Detail string // install method or error message
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
				Pack: name, Status: "present",
			})
			continue
		}

		entry, err := RegistryLookup(regReq, name)
		if err != nil {
			fmt.Fprintf(stdout, "  %s: not in registry — install manually or check 'aipack registry list'\n", name)
			results = append(results, PackInstallMissingResult{
				Pack: name, Status: "not-in-registry", Detail: err.Error(),
			})
			continue
		}

		installReq := PackInstallRequest{
			ConfigDir:    req.ConfigDir,
			URL:          entry.Repo,
			Ref:          entry.Ref,
			SubPath:      entry.Path,
			Name:         name,
			Register:     false,
			Quiet:        entry.Quiet,
			ContentPaths: entry.ContentPaths,
		}
		if installErr := installFn(ctx, installReq, stdout); installErr != nil {
			fmt.Fprintf(stdout, "  %s: install failed: %v\n", name, installErr)
			results = append(results, PackInstallMissingResult{
				Pack: name, Status: "error", Detail: installErr.Error(),
			})
			continue
		}

		results = append(results, PackInstallMissingResult{
			Pack: name, Status: "installed",
		})
	}

	return results, nil
}
