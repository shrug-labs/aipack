package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness/claudecode"
	"github.com/shrug-labs/aipack/internal/plugin"
	"github.com/shrug-labs/aipack/internal/util"
)

// Bump when native delivery changes without changing the rendered payload.
const nativeDeliveryVersion = 5

// Claude's catalog source controls root scanning and unnamed root-skill IDs.
// Preserve local source paths instead of changing those native semantics.
func nativePayloadPath(p domain.NativePlugin) (string, error) {
	if p.Harness == domain.HarnessClaudeCode {
		if path, ok := p.MarketplaceEntry["source"].(string); ok {
			if !strings.HasPrefix(path, "./") || !filepath.IsLocal(path) || strings.Contains(path, "\\") || slices.Contains(strings.Split(path, "/"), "..") {
				return "", fmt.Errorf("native plugin source path escapes its marketplace")
			}
			return filepath.ToSlash(filepath.Clean(path)), nil
		}
	}
	return "plugins/" + p.Name, nil
}

func claudeMarketplaceSourceOwned(entry map[string]any, record domain.NativePluginRecord) bool {
	source, _ := entry["source"].(map[string]any)
	path, _ := source["path"].(string)
	return (source["source"] == "directory" && canonicalPath(path) == canonicalPath(record.MarketplaceDir)) ||
		(source["source"] == "file" && canonicalPath(path) == canonicalPath(nativeCatalogPath(record.Harness, record.MarketplaceDir)))
}

func nativeGeneration(action domain.NativePluginAction) (string, error) {
	body, err := json.Marshal(struct {
		DeliveryVersion int
		SharedRoot      bool
		Package         domain.NativePlugin
		Files           []domain.NativePluginFile
	}{nativeDeliveryVersion, action.SharedRoot, action.Package, action.Files})
	if err != nil {
		return "", err
	}
	return util.ContentDigest(body), nil
}

func nativeSharedRootGeneration(action domain.NativePluginAction) (string, error) {
	if !action.SharedRoot {
		return "", nil
	}
	body, err := json.Marshal(action.Files)
	if err != nil {
		return "", err
	}
	return util.ContentDigest(body), nil
}

func nativePluginPlanOps(eng *engine.Engine, req SyncRequest, plan domain.Plan, ledger domain.Ledger) ([]PlanOp, error) {
	if len(plan.NativePlugins)+len(ledger.NativePlugins) == 0 {
		return nil, nil
	}
	other, err := otherNativeRecords(eng, req.ConfigDir, plan.Ledger)
	if err != nil {
		return nil, err
	}
	desired := map[string]bool{}
	var ops []PlanOp
	for _, action := range plan.NativePlugins {
		binding := action.Package.Binding()
		desired[binding] = true
		generation, err := nativeGeneration(action)
		if err != nil {
			return nil, err
		}
		previous, exists := ledger.NativePlugins[binding]
		previous, err = nativeSetupState(previous, binding, other[binding])
		if err != nil {
			return nil, err
		}
		diff := fmt.Sprintf("Install selected plugin components through %s (%d source entries).", action.Package.Harness, len(action.Files))
		if exists && previous.Generation == generation && util.PathExists(previous.CachePath) {
			installed, err := nativePluginInstalled(previous, binding)
			if err != nil {
				return nil, err
			}
			if installed && !previous.SetupPending && len(nativeSetupWarnings(previous, binding)) == 0 {
				continue
			}
			if installed {
				diff = "Record incomplete dependency setup; a later sync retries installation."
				if previous.SetupPending {
					diff = "Retry Node dependency installation with lifecycle scripts disabled; keep runtime data."
				}
			}
		}
		kind := domain.DiffCreate
		if exists {
			kind = domain.DiffManaged
		}
		payload, err := nativePayloadPath(action.Package)
		if err != nil {
			return nil, err
		}
		diff += fmt.Sprintf("\nConverter: %d; planned generation: %s", action.Package.ConverterVersion, generation)
		if exists {
			diff += "\nLast delivered generation: " + previous.Generation
		}
		ops = append(ops, PlanOp{Kind: PlanOpPlugin, Dst: filepath.Join(action.MarketplaceDir, payload), DisplayDst: binding, SourcePack: action.SourcePack, DiffKind: kind,
			Diff: diff})
	}
	for binding, record := range ledger.NativePlugins {
		if !desired[binding] {
			ops = append(ops, PlanOp{Kind: PlanOpPlugin, Dst: record.CachePath, DisplayDst: binding, SourcePack: record.SourcePack, Diff: "Remove this scope's native activation; retain runtime data."})
		}
	}
	slices.SortFunc(ops, func(a, b PlanOp) int { return strings.Compare(a.DisplayDst, b.DisplayDst) })
	return ops, nil
}

// Other scope ledgers are the ownership source of truth for the shared native
// cache. Different selections of one native identity cannot coexist in that cache.
func otherNativeRecords(eng *engine.Engine, configDir, currentLedger string) (map[string][]domain.NativePluginRecord, error) {
	records := map[string][]domain.NativePluginRecord{}
	err := filepath.WalkDir(filepath.Join(configDir, "ledger"), func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" || filepath.Clean(path) == filepath.Clean(currentLedger) {
			return nil
		}
		ledger, warnings, err := eng.LoadLedger(path)
		if err != nil {
			return err
		}
		if len(warnings) > 0 {
			return fmt.Errorf("cannot determine native ownership: %s", warnings[0].String())
		}
		for binding, record := range ledger.NativePlugins {
			if err := validateNativeRecord(configDir, binding, record); err != nil {
				return err
			}
			records[binding] = append(records[binding], record)
		}
		return nil
	})
	return records, err
}

func nativeSetupTarget(record domain.NativePluginRecord, binding string) (nativeActivationSnapshot, bool) {
	parts := strings.Split(binding, "@")
	root := filepath.Dir(filepath.Dir(filepath.Dir(record.MarketplaceDir)))
	if len(parts) == 2 && record.RootDirectoryName != "" && record.RootDirectoryName != parts[1] {
		root = filepath.Dir(root)
	}
	if record.Harness != domain.HarnessClaudeCode || len(parts) != 2 || !domain.ValidNativeRootName(record.RootDirectoryName) || !filepath.IsAbs(record.MarketplaceDir) || filepath.Clean(record.MarketplaceDir) != domain.NativeMarketplaceDir(root, record.Harness, parts[1], record.RootDirectoryName) {
		return nativeActivationSnapshot{}, false
	}
	key := util.ContentDigest([]byte(canonicalPath(record.ConfigHome) + "\x00" + binding))
	return nativeActivationSnapshot{Path: filepath.Join(root, "native-setup.json"), Section: "setups", Binding: key}, true
}

func nativeSetupCacheVersion(record domain.NativePluginRecord, binding string) string {
	parts := strings.Split(binding, "@")
	if len(parts) != 2 || record.CachePath == "" {
		return ""
	}
	base := nativeCacheRoot(record, binding)
	version, err := filepath.Rel(canonicalPath(base), canonicalPath(record.CachePath))
	if err != nil || version == "." || !filepath.IsLocal(version) {
		return ""
	}
	return filepath.ToSlash(version)
}

func nativeSetupVersions(value any) (map[string]any, error) {
	versions, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("shared native setup versions must be an object")
	}
	for version, pending := range versions {
		if _, ok := pending.(bool); !ok || version == "." || !filepath.IsLocal(filepath.FromSlash(version)) || strings.Contains(version, "\\") || filepath.ToSlash(filepath.Clean(filepath.FromSlash(version))) != version {
			return nil, fmt.Errorf("shared native setup version/result is invalid")
		}
	}
	return versions, nil
}

// Shared setup survives removal of a scope. Legacy ledger failures are retained
// until the first shared result is committed through the native delivery journal.
func nativeSetupState(record domain.NativePluginRecord, binding string, others []domain.NativePluginRecord) (domain.NativePluginRecord, error) {
	if record.Harness != domain.HarnessClaudeCode {
		return record, nil
	}
	if target, managed := nativeSetupTarget(record, binding); managed {
		if !util.IsWithinDir(canonicalPath(target.Path), canonicalPath(filepath.Dir(target.Path))) {
			return record, fmt.Errorf("shared native setup state escapes config directory")
		}
		root, err := readNativeConfig(target.Path)
		if err != nil {
			return record, err
		}
		entries, ok := root[target.Section].(map[string]any)
		if root[target.Section] != nil && !ok {
			return record, fmt.Errorf("shared native setup state must be an object")
		}
		if value, exists := entries[target.Binding]; exists {
			versions, err := nativeSetupVersions(value)
			if err != nil {
				return record, err
			}
			if pending, exists := versions[nativeSetupCacheVersion(record, binding)]; exists {
				record.SetupPending = pending.(bool)
				return record, nil
			}
		}
	}
	for _, other := range others {
		if other.Harness == record.Harness && record.CachePath != "" && canonicalPath(other.CachePath) == canonicalPath(record.CachePath) && canonicalPath(other.ConfigHome) == canonicalPath(record.ConfigHome) && other.SetupPending {
			record.SetupPending = true
		}
	}
	return record, nil
}

func setNativeSetupState(record domain.NativePluginRecord, binding string, pending bool) error {
	if target, managed := nativeSetupTarget(record, binding); managed {
		if _, err := nativeSetupState(record, binding, nil); err != nil {
			return err
		}
		version := nativeSetupCacheVersion(record, binding)
		if version == "" {
			return fmt.Errorf("shared native setup requires a versioned cache")
		}
		root, err := readNativeConfig(target.Path)
		if err != nil {
			return err
		}
		versions := map[string]any{}
		entries, _ := root[target.Section].(map[string]any)
		if value, exists := entries[target.Binding]; exists {
			versions, err = nativeSetupVersions(value)
			if err != nil {
				return err
			}
		}
		if current, exists := versions[version]; exists && current == pending {
			info, err := os.Stat(target.Path)
			if err != nil {
				return err
			}
			if runtime.GOOS == "windows" || info.Mode().Perm() == 0o600 {
				return nil
			}
		}
		versions[version] = pending
		return restoreNativeConfigEntry(target.Path, target.Section, target.Binding, versions, true)
	}
	return nil
}

func canonicalPath(path string) string {
	clean := filepath.Clean(path)
	for ancestor := clean; ; ancestor = filepath.Dir(ancestor) {
		if resolved, err := filepath.EvalSymlinks(ancestor); err == nil {
			rel, err := filepath.Rel(ancestor, clean)
			if err == nil {
				return filepath.Join(resolved, rel)
			}
		}
		if filepath.Dir(ancestor) == ancestor {
			break
		}
	}
	return clean
}

func validateNativeLedgerHarness(path string, ledger domain.Ledger) error {
	hid, _ := harnessFromLedgerPath(path)
	for _, record := range ledger.NativePlugins {
		if (hid != domain.HarnessCodex && hid != domain.HarnessClaudeCode) || record.Harness != hid {
			return fmt.Errorf("native ownership does not match ledger harness: %s", path)
		}
	}
	return nil
}

// Preflight is read-only, including during dry-run. It refuses unowned native
// installations and marketplace registrations instead of silently taking them over.
func preflightNativePlugins(ctx context.Context, eng *engine.Engine, plan domain.Plan, req SyncRequest) error {
	ledger, warnings, err := eng.LoadLedger(plan.Ledger)
	if err != nil {
		return err
	}
	if len(warnings) > 0 {
		for _, action := range plan.Writes {
			if action.Delivery != nil {
				return fmt.Errorf("cannot determine portable ownership: %s", warnings[0].String())
			}
		}
		for _, entry := range ledger.Managed {
			if entry.Delivery != nil {
				return fmt.Errorf("cannot determine portable ownership: %s", warnings[0].String())
			}
		}
	}
	if err := preflightPortablePlugins(eng, plan, ledger, req.ConfigDir); err != nil {
		return err
	}
	if err := preflightGenericPlugins(eng, plan, ledger, req); err != nil {
		return err
	}
	if len(plan.NativePlugins) == 0 && len(ledger.NativePlugins) == 0 {
		return nil
	}
	if len(warnings) > 0 {
		return fmt.Errorf("cannot determine native ownership: %s", warnings[0].String())
	}
	for binding, record := range ledger.NativePlugins {
		if err := validateNativeRecord(req.ConfigDir, binding, record); err != nil {
			return err
		}
	}
	other, err := otherNativeRecords(eng, req.ConfigDir, plan.Ledger)
	if err != nil {
		return err
	}
	checkedMarkets := map[string]bool{}
	for _, action := range plan.NativePlugins {
		if action.Package.Harness != domain.HarnessCodex && action.Package.Harness != domain.HarnessClaudeCode {
			return fmt.Errorf("native installer does not support %s", action.Package.Harness)
		}
		if action.Package.Harness == domain.HarnessCodex {
			if err := config.ValidateCodexMarketplacePolicy(action.Package.MarketplaceEntry); err != nil {
				return fmt.Errorf("plugin %s: %w", action.Package.Binding(), err)
			}
		}
		expected := domain.NativeMarketplaceDir(req.ConfigDir, action.Package.Harness, action.Package.Marketplace, action.Package.RootDirectoryName)
		if !domain.ValidNativeRootName(action.Package.RootDirectoryName) || (action.Package.Harness != domain.HarnessClaudeCode && action.Package.RootDirectoryName != "") || filepath.Clean(action.MarketplaceDir) != filepath.Clean(expected) || !domain.ValidNativeName(action.Package.Name) || !domain.ValidNativeName(action.Package.Marketplace) {
			return fmt.Errorf("invalid native package destination")
		}
		binding := action.Package.Binding()
		payload, err := nativePayloadPath(action.Package)
		if err != nil {
			return err
		}
		sharedRoot, err := nativeSharedRootGeneration(action)
		if err != nil {
			return err
		}
		if action.SharedRoot && (action.Package.Harness != domain.HarnessClaudeCode || action.Package.Format != plugin.Claude || action.Package.Manifest != "" || action.Package.CopiedSource || payload != ".") {
			return fmt.Errorf("invalid shared native source root")
		}
		packageDir := filepath.Join(action.MarketplaceDir, payload)
		if !util.IsWithinDir(canonicalPath(packageDir), canonicalPath(action.MarketplaceDir)) {
			return fmt.Errorf("native payload path escapes its marketplace")
		}
		catalogPath := nativeCatalogPath(action.Package.Harness, action.MarketplaceDir)
		if payload != "." && util.IsWithinDir(catalogPath, packageDir) {
			return fmt.Errorf("native payload conflicts with the generated marketplace catalog")
		}
		if payload == "." && slices.ContainsFunc(action.Files, func(file domain.NativePluginFile) bool {
			if action.SharedRoot && file.Mode.IsDir() && (file.Path == ".aipack-marketplace" || file.Path == ".aipack-marketplace/empty-skills") {
				return false
			}
			return file.Path == ".aipack-marketplace" || strings.HasPrefix(file.Path, ".aipack-marketplace/")
		}) {
			return fmt.Errorf("upstream package conflicts with the generated marketplace catalog")
		}
		for _, sibling := range plan.NativePlugins {
			if sibling.Package.Binding() == binding || sibling.MarketplaceDir != action.MarketplaceDir {
				continue
			}
			path, err := nativePayloadPath(sibling.Package)
			if err != nil {
				return err
			}
			otherDir := filepath.Join(sibling.MarketplaceDir, path)
			if util.IsWithinDir(packageDir, otherDir) || util.IsWithinDir(otherDir, packageDir) {
				if action.SharedRoot && sibling.SharedRoot && path == "." {
					otherRoot, err := nativeSharedRootGeneration(sibling)
					if err != nil || otherRoot != sharedRoot {
						return fmt.Errorf("shared native root has inconsistent selected payloads")
					}
					continue
				}
				return fmt.Errorf("native payloads for %s and %s overlap; shared-source selection is not implemented", binding, sibling.Package.Binding())
			}
		}
		for sibling, record := range ledger.NativePlugins {
			if sibling == binding || record.MarketplaceDir != action.MarketplaceDir {
				continue
			}
			otherDir := filepath.Join(record.MarketplaceDir, record.PayloadPath)
			if util.IsWithinDir(packageDir, otherDir) || util.IsWithinDir(otherDir, packageDir) {
				if action.SharedRoot && record.PayloadPath == "." {
					continue
				}
				if record.SharedRoot != "" && payload == "." && action.Package.Format == plugin.Claude && action.Package.Manifest == "" && !action.Package.CopiedSource && !slices.ContainsFunc(plan.NativePlugins, func(candidate domain.NativePluginAction) bool { return candidate.Package.Binding() == sibling }) {
					continue
				}
				return fmt.Errorf("native payload %s overlaps an owned source; clean that source before changing layout", binding)
			}
		}
		for sibling, records := range other {
			for _, record := range records {
				if sibling == binding || record.MarketplaceDir != action.MarketplaceDir {
					continue
				}
				otherDir := filepath.Join(record.MarketplaceDir, record.PayloadPath)
				if util.IsWithinDir(packageDir, otherDir) || util.IsWithinDir(otherDir, packageDir) {
					if action.SharedRoot && record.SharedRoot == sharedRoot && record.PayloadPath == "." {
						continue
					}
					return fmt.Errorf("native payload %s overlaps a source owned by another scope", binding)
				}
			}
		}
		if previous, exists := ledger.NativePlugins[binding]; exists && canonicalPath(previous.ConfigHome) != canonicalPath(action.ConfigHome) {
			return fmt.Errorf("native plugin %q is owned in %s; clean this scope before changing its native config home", binding, previous.ConfigHome)
		}
		generation, err := nativeGeneration(action)
		if err != nil {
			return err
		}
		owned := false
		marketOwned := false
		for _, record := range ledger.NativePlugins {
			if canonicalPath(record.ConfigHome) == canonicalPath(action.ConfigHome) && canonicalPath(record.MarketplaceDir) == canonicalPath(action.MarketplaceDir) {
				marketOwned = true
			}
		}
		for ownerBinding, records := range other {
			for _, record := range records {
				if strings.HasSuffix(ownerBinding, "@"+action.Package.Marketplace) && record.Harness == action.Package.Harness && canonicalPath(record.ConfigHome) == canonicalPath(action.ConfigHome) && canonicalPath(record.MarketplaceDir) != canonicalPath(action.MarketplaceDir) {
					return fmt.Errorf("native marketplace %q has a different source layout active at %s; clean that scope before changing its shared registration", action.Package.Marketplace, record.SettingsPath)
				}
				if canonicalPath(record.ConfigHome) == canonicalPath(action.ConfigHome) && canonicalPath(record.MarketplaceDir) == canonicalPath(action.MarketplaceDir) {
					marketOwned = true
				}
			}
		}
		records := append([]domain.NativePluginRecord{}, other[binding]...)
		if record, ok := ledger.NativePlugins[binding]; ok {
			records = append(records, record)
		}
		for _, record := range records {
			if canonicalPath(record.ConfigHome) == canonicalPath(action.ConfigHome) && canonicalPath(record.MarketplaceDir) == canonicalPath(action.MarketplaceDir) {
				owned = true
			}
		}
		for _, record := range other[binding] {
			if canonicalPath(record.MarketplaceDir) == canonicalPath(action.MarketplaceDir) && record.Generation != generation {
				return fmt.Errorf("native plugin %q has a different selection active at %s; disable that scope before replacing its shared native package", binding, record.SettingsPath)
			}
		}
		cache := filepath.Join(action.ConfigHome, "plugins", "cache", action.Package.Marketplace, action.Package.Name)
		if _, err := os.Stat(cache); err == nil && !owned && action.Package.Harness == domain.HarnessCodex {
			return fmt.Errorf("native plugin %q is already installed outside AIPack; remove it through Codex before activating this pack", binding)
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		record := domain.NativePluginRecord{Harness: action.Package.Harness, ConfigHome: action.ConfigHome, MarketplaceDir: action.MarketplaceDir, RootDirectoryName: action.Package.RootDirectoryName, PayloadPath: payload, SettingsPath: action.SettingsPath, CachePath: cache}
		if err := validateNativeRecord(req.ConfigDir, binding, record); err != nil {
			return err
		}
		if record.Harness == domain.HarnessClaudeCode {
			entries, err := claudeInstalledEntries(record, binding)
			if err != nil {
				return err
			}
			for _, raw := range entries {
				entry, _ := raw.(map[string]any)
				project, _ := entry["projectPath"].(string)
				claimed := slices.ContainsFunc(records, func(owner domain.NativePluginRecord) bool {
					return owner.Harness == record.Harness && canonicalPath(owner.ConfigHome) == canonicalPath(record.ConfigHome) && entry["scope"] == claudeScope(owner) && (claudeScope(owner) == "user" || canonicalPath(project) == canonicalPath(filepath.Dir(filepath.Dir(owner.SettingsPath))))
				})
				if !claimed {
					return fmt.Errorf("native plugin %q has an installation scope outside AIPack; remove that native installation before activating this pack", binding)
				}
			}
		}
		snapshots, err := snapshotNativeConfigEntries(nativeMarketplaceTargets(record, action.Package.Marketplace))
		if err != nil {
			return err
		}
		registered := false
		for _, snapshot := range snapshots {
			if !snapshot.Present {
				continue
			}
			registered = true
			entry, _ := snapshot.Entry.(map[string]any)
			source, _ := entry["source"].(string)
			local := entry["source_type"] == "local"
			if action.Package.Harness == domain.HarnessClaudeCode {
				local, source = claudeMarketplaceSourceOwned(entry, record), action.MarketplaceDir
				for ownerBinding, owner := range ledger.NativePlugins {
					if strings.HasSuffix(ownerBinding, "@"+action.Package.Marketplace) && canonicalPath(owner.ConfigHome) == canonicalPath(action.ConfigHome) && claudeMarketplaceSourceOwned(entry, owner) {
						local, source, marketOwned = true, action.MarketplaceDir, true
					}
				}
			}
			if !local || canonicalPath(source) != canonicalPath(action.MarketplaceDir) || !marketOwned {
				return fmt.Errorf("native marketplace %q is already registered outside this AIPack delivery; remove that registration before activating this pack", action.Package.Marketplace)
			}
		}
		if action.Package.Harness == domain.HarnessCodex && registered && util.PathExists(catalogPath) {
			key := action.ConfigHome + "\x00" + action.MarketplaceDir
			if !checkedMarkets[key] {
				if err := checkNativeCodexMarketplace(ctx, req.Home, action); err != nil {
					return err
				}
				checkedMarkets[key] = true
			}
		}
	}
	return nil
}

// Ordinary delivery must not expose an unmanaged copy of the imported plugin.
// A current-scope native receipt permits migration; other installations stay owned
// by their native installer and must be removed explicitly before conversion.
func preflightGenericPlugins(eng *engine.Engine, plan domain.Plan, ledger domain.Ledger, req SyncRequest) error {
	hid, _ := harnessFromLedgerPath(plan.Ledger)
	if hid != domain.HarnessClaudeCode && hid != domain.HarnessOpenCode && hid != domain.HarnessCline {
		return nil
	}
	packs := map[string]bool{}
	for _, action := range plan.Writes {
		if action.Category == domain.CategorySkills {
			packs[action.SourcePack] = true
		}
		if action.Category == domain.CategoryHooks {
			// Hook wrappers can combine handlers from several source packs.
			packs[action.SourcePack] = true
			for _, ref := range action.TraceRefs {
				packs[ref.SourcePack] = true
			}
		}
	}
	for _, server := range plan.MCPServers {
		packs[server.SourcePack] = true
	}
	// Native/portable packages also publish MCP records for trace and ledger.
	// Their existing preflight owns scope admission; these checks are ordinary-only.
	for _, action := range plan.NativePlugins {
		delete(packs, action.SourcePack)
	}
	for _, action := range plan.Writes {
		if action.Delivery != nil {
			delete(packs, action.SourcePack)
		}
	}
	delete(packs, "")
	for pack := range packs {
		manifest, err := config.LoadPackManifest(filepath.Join(req.ConfigDir, "packs", pack, "pack.json"))
		if os.IsNotExist(err) {
			continue // In-memory profiles can supply ordinary content without an installed pack.
		}
		if err != nil {
			return err
		}
		if manifest.NativePlugin == nil || manifest.NativePlugin.Harness != domain.HarnessCodex {
			continue
		}
		binding := manifest.NativePlugin.Binding()
		if hid == domain.HarnessOpenCode {
			paths := []string{filepath.Join(req.Home, ".config/opencode/opencode.json")}
			if native := planRequestForHarness(req, hid).NativeConfigDir; native != "" {
				paths = append(paths, filepath.Join(native, "opencode.json"))
			}
			if req.ProjectDir != "" {
				paths = append(paths, filepath.Join(req.ProjectDir, ".opencode/opencode.json"))
			}
			for _, path := range paths {
				if err := preflightPortableBinding(eng, binding, path, ledger); err != nil {
					return err
				}
			}
			continue
		}
		if hid == domain.HarnessCline {
			if err := preflightClinePlugin(req.Home, manifest.NativePlugin.Name); err != nil {
				return err
			}
			continue
		}
		home := planRequestForHarness(req, hid).NativeConfigDir
		if home == "" {
			home = filepath.Join(req.Home, ".claude")
		}
		entries, err := claudeInstalledEntries(domain.NativePluginRecord{ConfigHome: home}, binding)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			continue
		}
		owner := ledger.NativePlugins[binding]
		if len(entries) == 1 && owner.Harness == hid && owner.SourcePack == pack && canonicalPath(owner.ConfigHome) == canonicalPath(home) {
			entry, err := claudeInstalledEntry(owner, binding)
			if err != nil {
				return err
			}
			if entry != nil {
				continue
			}
		}
		return fmt.Errorf("native plugin %q is installed outside AIPack's current scope; remove that native installation before activating ordinary converted skills", binding)
	}
	return nil
}

func preflightClinePlugin(home, name string) error {
	root := filepath.Join(home, ".agents/plugins")
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		packageRoot := filepath.Join(root, entry.Name())
		info, err := os.Stat(packageRoot)
		if os.IsNotExist(err) || err == nil && !info.IsDir() {
			continue
		}
		if err != nil {
			return err
		}
		path := filepath.Join(packageRoot, "plugin.json")
		info, err = os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || !util.IsWithinDir(canonicalPath(path), canonicalPath(packageRoot)) {
			continue // The native loader rejects non-files and escaping manifest links.
		}
		body, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		var manifest map[string]any
		if json.Unmarshal(body, &manifest) == nil && manifest["$schema"] == "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json" && manifest["name"] == name {
			return fmt.Errorf("native Cline Agent Plugin %q is installed outside AIPack; remove that native installation before activating converted content", name)
		}
	}
	return nil
}

func validateNativeRecord(configDir, binding string, record domain.NativePluginRecord) error {
	parts := strings.Split(binding, "@")
	if len(parts) != 2 || !domain.ValidNativeName(parts[0]) || !domain.ValidNativeName(parts[1]) || (record.Harness != domain.HarnessCodex && record.Harness != domain.HarnessClaudeCode) || !filepath.IsAbs(record.ConfigHome) || !filepath.IsAbs(record.SettingsPath) || (record.Home != "" && !filepath.IsAbs(record.Home)) {
		return fmt.Errorf("invalid native ownership record for %s", binding)
	}
	if !domain.ValidNativeRootName(record.RootDirectoryName) || (record.Harness != domain.HarnessClaudeCode && record.RootDirectoryName != "") || canonicalPath(record.MarketplaceDir) != canonicalPath(domain.NativeMarketplaceDir(configDir, record.Harness, parts[1], record.RootDirectoryName)) || !util.IsWithinDir(canonicalPath(record.CachePath), canonicalPath(filepath.Join(record.ConfigHome, "plugins", "cache", parts[1], parts[0]))) {
		return fmt.Errorf("native ownership paths escape their package for %s", binding)
	}
	if !filepath.IsLocal(record.PayloadPath) || strings.Contains(record.PayloadPath, "\\") || filepath.ToSlash(filepath.Clean(record.PayloadPath)) != record.PayloadPath {
		return fmt.Errorf("native payload ownership escapes its marketplace for %s", binding)
	}
	if !util.IsWithinDir(canonicalPath(filepath.Join(record.MarketplaceDir, record.PayloadPath)), canonicalPath(record.MarketplaceDir)) {
		return fmt.Errorf("native payload ownership escapes its marketplace for %s", binding)
	}
	projectSettings := ".codex/config.toml"
	if record.Harness == domain.HarnessClaudeCode {
		projectSettings = ".claude/settings.local.json"
	}
	if canonicalPath(record.SettingsPath) != canonicalPath(nativeUserConfig(record)) && filepath.Clean(record.SettingsPath) != filepath.Join(filepath.Dir(filepath.Dir(record.SettingsPath)), projectSettings) {
		return fmt.Errorf("invalid native scope path for %s", binding)
	}
	namespace := record.Namespace
	if namespace == "" {
		namespace = parts[0] // older ownership records used the install name
	} else if record.Harness != domain.HarnessClaudeCode || !domain.ValidNativeName(namespace) {
		return fmt.Errorf("invalid native component namespace for %s", binding)
	}
	for _, server := range record.MCPPermissionServers {
		prefix := claudecode.MCPPermissionName("plugin_" + namespace + "_")
		if record.Harness != domain.HarnessClaudeCode || !strings.HasPrefix(server, prefix) || len(server) == len(prefix) {
			return fmt.Errorf("invalid native MCP permission ownership for %s", binding)
		}
	}
	if record.SetupPending && record.Harness != domain.HarnessClaudeCode {
		return fmt.Errorf("invalid native package setup state for %s", binding)
	}
	if record.SharedRoot != "" {
		if _, err := hex.DecodeString(record.SharedRoot); err != nil || len(record.SharedRoot) != 64 || record.Harness != domain.HarnessClaudeCode || record.PayloadPath != "." {
			return fmt.Errorf("invalid shared native source ownership for %s", binding)
		}
	}
	return nil
}

func readNativeConfig(path string) (map[string]any, error) {
	body, exists, err := util.ReadFileIfExists(path)
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string]any{}, nil
	}
	root := map[string]any{}
	if filepath.Ext(path) == ".json" {
		err = util.UnmarshalJSON(body, &root)
	} else {
		err = toml.Unmarshal(body, &root)
	}
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, fmt.Errorf("native config %s must be an object", path)
	}
	return root, nil
}

func removeNativeMarketplace(ctx context.Context, home string, record domain.NativePluginRecord, market string) error {
	target := nativeMarketplaceTargets(record, market)[0]
	root, err := readNativeConfig(target.Path)
	if err != nil {
		return err
	}
	markets := root
	if target.Section != "" {
		markets, _ = root[target.Section].(map[string]any)
	}
	if markets[market] == nil {
		return nil
	}
	args := []string{"marketplace", "remove", market, "--json"}
	if record.Harness == domain.HarnessClaudeCode {
		inUse, err := claudeMarketplaceInUse(record, market)
		if err != nil || inUse {
			return err
		}
		entry, _ := markets[market].(map[string]any)
		if !claudeMarketplaceSourceOwned(entry, record) {
			return fmt.Errorf("native marketplace %q changed after delivery; retain its registration", market)
		}
		record.SettingsPath = nativeUserConfig(record)
		args = []string{"marketplace", "remove", market}
	} else if record.Harness == domain.HarnessCodex {
		entry, _ := markets[market].(map[string]any)
		source, _ := entry["source"].(string)
		if entry["source_type"] != "local" || canonicalPath(source) != canonicalPath(record.MarketplaceDir) {
			return fmt.Errorf("native marketplace %q changed after delivery; retain its registration", market)
		}
	}
	_, err = runNativePlugin(ctx, home, record, args...)
	return err
}

// Native installation enables a plugin in user config. Project delivery restores
// that entry afterward, while retaining the native marketplace registration.
func restoreNativePluginEntry(record domain.NativePluginRecord, binding string, previous any, present bool) error {
	target := nativeActivationTargets(record, binding)[1]
	return restoreNativeConfigEntry(target.Path, target.Section, binding, previous, present)
}

func restoreNativeConfigEntry(path, section, key string, previous any, present bool) error {
	root, err := readNativeConfig(path)
	if err != nil {
		return err
	}
	plugins := root
	if section != "" {
		if raw, exists := root[section]; exists {
			var ok bool
			plugins, ok = raw.(map[string]any)
			if !ok {
				return fmt.Errorf("native config %s section %q must be an object", path, section)
			}
		} else {
			plugins = map[string]any{}
		}
	}
	if plugins == nil {
		plugins = map[string]any{}
	}
	if present {
		plugins[key] = previous
	} else {
		delete(plugins, key)
	}
	if section != "" {
		if len(plugins) == 0 && !(section == "plugins" && filepath.Base(path) == "installed_plugins.json") {
			delete(root, section)
		} else {
			root[section] = plugins
		}
	}
	var body []byte
	if filepath.Ext(path) == ".json" {
		body, err = json.MarshalIndent(root, "", "  ")
	} else {
		body, err = toml.Marshal(root)
	}
	if err != nil {
		return err
	}
	return util.WriteFileAtomicWithPerms(path, body, 0o700, 0o600)
}

type nativeActivationSnapshot struct {
	Path, Binding     string
	Section           string `json:",omitempty"`
	Entry             any    `json:"-"`
	EntryTOML         []byte `json:",omitempty"`
	EntryJSON         []byte `json:",omitempty"`
	Present           bool
	PermissionServers []string `json:",omitempty"`
}

func (s nativeActivationSnapshot) section(fallback string) string {
	if s.Section != "" || filepath.Ext(s.Path) == ".json" {
		return s.Section
	}
	return fallback // journals written before JSON support have no Section
}

func (s nativeActivationSnapshot) entry() (any, error) {
	if !s.Present || s.Entry != nil {
		return s.Entry, nil
	}
	if filepath.Ext(s.Path) == ".json" {
		var entry any
		if err := util.UnmarshalJSON(s.EntryJSON, &entry); err != nil {
			return nil, err
		}
		if entry == nil {
			return nil, fmt.Errorf("native JSON snapshot is missing its entry")
		}
		return entry, nil
	}
	var root map[string]any
	if err := toml.Unmarshal(s.EntryTOML, &root); err != nil {
		return nil, err
	}
	if root["entry"] == nil {
		return nil, fmt.Errorf("native TOML snapshot is missing its entry")
	}
	return root["entry"], nil
}

// Capture owned bindings before the engine merges activation settings. Restore
// only those entries after a failed apply, preserving unrelated config edits.
func snapshotNativeActivations(plan domain.Plan, ledger domain.Ledger) ([]nativeActivationSnapshot, error) {
	var targets []nativeActivationSnapshot
	packs := map[string]bool{}
	for _, action := range plan.NativePlugins {
		record := domain.NativePluginRecord{Harness: action.Package.Harness, ConfigHome: action.ConfigHome, SettingsPath: action.SettingsPath, MCPPermissionServers: action.MCPPermissionServers}
		targets = append(targets, nativeActivationTargets(record, action.Package.Binding())...)
		if record.Harness == domain.HarnessClaudeCode {
			packs[action.SourcePack] = true
		}
	}
	for binding, record := range ledger.NativePlugins {
		targets = append(targets, nativeActivationTargets(record, binding)...)
		if record.Harness == domain.HarnessClaudeCode {
			packs[record.SourcePack] = true
		}
	}
	delete(packs, "")
	// A route transition can add or remove ordinary servers independently of
	// native activation. Keep exact IDs from the plan/ledger, not sanitized names.
	for _, server := range plan.MCPServers {
		if server.Harness == domain.HarnessClaudeCode && packs[server.SourcePack] {
			targets = append(targets, nativeActivationSnapshot{Path: server.ConfigPath, Binding: server.Name, Section: "mcpServers"})
		}
	}
	for key, entry := range ledger.Managed {
		path, name, ok := domain.SplitMCPLedgerKey(key)
		if ok && packs[entry.SourcePack] {
			targets = append(targets, nativeActivationSnapshot{Path: path, Binding: name, Section: "mcpServers"})
		}
	}
	return snapshotNativeConfigEntries(targets)
}

func restoreNativeActivations(snapshots []nativeActivationSnapshot) error {
	var errs []error
	for _, snapshot := range snapshots {
		entry, err := snapshot.entry()
		if err != nil {
			return err
		}
		if snapshot.Section == "permissions" {
			root, err := readNativeConfig(snapshot.Path)
			if err != nil {
				return err
			}
			perms, ok := root["permissions"].(map[string]any)
			if root["permissions"] != nil && !ok {
				return fmt.Errorf("native permissions must be an object")
			}
			kept, err := filterNativePermissions(perms[snapshot.Binding], snapshot.PermissionServers, false)
			if err != nil {
				return err
			}
			prior, err := filterNativePermissions(entry, snapshot.PermissionServers, true)
			if err != nil {
				return err
			}
			items := append(kept, prior...)
			errs = append(errs, restoreNativeConfigEntry(snapshot.Path, "permissions", snapshot.Binding, items, snapshot.Binding == "allow" || len(items) > 0))
			continue
		}
		errs = append(errs, restoreNativeConfigEntry(snapshot.Path, snapshot.section("plugins"), snapshot.Binding, entry, snapshot.Present))
	}
	return errors.Join(errs...)
}

func applyNativePlugins(ctx context.Context, eng *engine.Engine, plan domain.Plan, req SyncRequest, ledger *domain.Ledger, retained map[string]domain.NativePluginRecord) (func(bool) error, error) {
	other, err := otherNativeRecords(eng, req.ConfigDir, plan.Ledger)
	if err != nil {
		return nil, err
	}
	// Targeted deletion retains bindings from this ledger as well as other scopes.
	for binding, record := range retained {
		if err := validateNativeRecord(req.ConfigDir, binding, record); err != nil {
			return nil, err
		}
		other[binding] = append(other[binding], record)
	}
	if ledger.NativePlugins == nil {
		ledger.NativePlugins = map[string]domain.NativePluginRecord{}
	}
	before := maps.Clone(ledger.NativePlugins)
	desired := map[string]bool{}
	var finalizers []func(bool) error
	finish := func(commit bool) error {
		var errs []error
		for i := len(finalizers) - 1; i >= 0; i-- {
			errs = append(errs, finalizers[i](commit))
		}
		if commit {
			errs = append(errs, cleanupNativePayloadChanges(eng, req.ConfigDir, plan.Ledger, before, ledger.NativePlugins))
		}
		return errors.Join(errs...)
	}
	fail := func(err error) (func(bool) error, error) { return nil, errors.Join(err, finish(false)) }
	for _, action := range plan.NativePlugins {
		binding := action.Package.Binding()
		desired[binding] = true
		generation, err := nativeGeneration(action)
		if err != nil {
			return fail(err)
		}
		previous, exists := ledger.NativePlugins[binding]
		previous, err = nativeSetupState(previous, binding, other[binding])
		if err != nil {
			return fail(err)
		}
		if exists && previous.Generation == generation && util.PathExists(previous.CachePath) {
			installed, err := nativePluginInstalled(previous, binding)
			if err != nil {
				return fail(err)
			}
			if installed {
				previous, err = completeNativeSetup(ctx, req.Home, previous, binding)
				if err != nil {
					return fail(err)
				}
				previous.MCPPermissionServers = slices.Clone(action.MCPPermissionServers)
				previous.Namespace = action.Namespace
				ledger.NativePlugins[binding] = previous
				continue
			}
		}
		payload, err := nativePayloadPath(action.Package)
		if err != nil {
			return fail(err)
		}
		record := domain.NativePluginRecord{Generation: generation, Harness: action.Package.Harness, Home: req.Home, ConfigHome: action.ConfigHome, MarketplaceDir: action.MarketplaceDir, PayloadPath: payload, SettingsPath: action.SettingsPath, SourcePack: action.SourcePack,
			RootDirectoryName:    action.Package.RootDirectoryName,
			Namespace:            action.Namespace,
			MCPPermissionServers: slices.Clone(action.MCPPermissionServers),
			CachePath:            filepath.Join(action.ConfigHome, "plugins/cache", action.Package.Marketplace, action.Package.Name)}
		record.SharedRoot, err = nativeSharedRootGeneration(action)
		if err != nil {
			return fail(err)
		}
		// A different scope may already have delivered this exact generation.
		shared := false
		for _, record := range other[binding] {
			if record.Harness == domain.HarnessCodex && record.Generation == generation && canonicalPath(record.ConfigHome) == canonicalPath(action.ConfigHome) && util.PathExists(record.CachePath) {
				installed, err := nativePluginInstalled(record, binding)
				if err != nil {
					return fail(err)
				}
				if !installed {
					continue
				}
				record.SettingsPath, record.SourcePack = action.SettingsPath, action.SourcePack
				ledger.NativePlugins[binding] = record
				shared = true
				break
			}
		}
		if shared {
			continue
		}
		if err := os.MkdirAll(action.MarketplaceDir, 0o700); err != nil {
			return fail(err)
		}
		manifestPath := nativeCatalogPath(record.Harness, action.MarketplaceDir)
		catalog := maps.Clone(action.Package.MarketplaceMetadata)
		if catalog == nil {
			catalog = map[string]any{}
		}
		catalog["name"], catalog["plugins"] = action.Package.Marketplace, []map[string]any{}
		catalogBefore, catalogErr := os.ReadFile(manifestPath)
		if catalogErr == nil {
			entries, err := plugin.CatalogEntries(catalogBefore)
			if err != nil {
				return fail(err)
			}
			catalog["plugins"] = entries
		} else if !errors.Is(catalogErr, fs.ErrNotExist) {
			return fail(catalogErr)
		}
		config, err := readNativeConfig(nativeUserConfig(record))
		if err != nil {
			return fail(err)
		}
		activation := nativeActivationTargets(record, binding)[1]
		plugins, _ := config[activation.Section].(map[string]any)
		oldEntry, present := plugins[binding]
		marketSnapshots, err := snapshotNativeConfigEntries(nativeMarketplaceTargets(record, action.Package.Marketplace))
		if err != nil {
			return fail(err)
		}
		marketPreviouslyRegistered := marketSnapshots[0].Present
		finalizers = append(finalizers, func(commit bool) error {
			if commit {
				return nil
			}
			var errs []error
			if catalogErr == nil {
				errs = append(errs, util.WriteFileAtomic(manifestPath, catalogBefore))
			} else if err := os.Remove(manifestPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
			if !marketPreviouslyRegistered {
				errs = append(errs, removeNativeMarketplace(context.WithoutCancel(ctx), req.Home, record, action.Package.Marketplace))
			}
			for _, snapshot := range marketSnapshots {
				errs = append(errs, restoreNativeConfigEntry(snapshot.Path, snapshot.Section, snapshot.Binding, snapshot.Entry, snapshot.Present))
			}
			errs = append(errs, restoreNativePluginEntry(record, binding, oldEntry, present))
			return errors.Join(errs...)
		})
		// Keep sibling packs in the same native marketplace.
		entries := catalog["plugins"].([]map[string]any)
		entries = slices.DeleteFunc(entries, func(entry map[string]any) bool { return entry["name"] == action.Package.Name })
		entry := maps.Clone(action.Package.MarketplaceEntry)
		if entry == nil {
			entry = map[string]any{}
		}
		entry["name"], entry["source"] = action.Package.Name, "./"+payload
		if payload == "." {
			entry["source"] = "./"
		}
		entries = append(entries, entry)
		catalog["plugins"] = entries
		body, err := json.MarshalIndent(catalog, "", "  ")
		if err != nil {
			return fail(err)
		}
		packageDir := filepath.Join(action.MarketplaceDir, payload)
		if err := os.MkdirAll(filepath.Dir(packageDir), 0o700); err != nil {
			return fail(err)
		}
		stage, err := os.MkdirTemp(filepath.Dir(packageDir), ".native-*")
		if err != nil {
			return fail(err)
		}
		defer util.RemoveOwnedTree(stage)
		if err := plugin.WriteFiles(stage, action.Files); err != nil {
			_ = util.RemoveOwnedTree(stage)
			return fail(err)
		}
		if payload == "." {
			if !action.SharedRoot && util.PathExists(filepath.Join(stage, ".aipack-marketplace")) {
				return fail(fmt.Errorf("upstream package conflicts with the generated marketplace catalog"))
			}
			if err := util.WriteFileAtomicWithPerms(nativeCatalogPath(action.Package.Harness, stage), body, 0o700, 0o600); err != nil {
				return fail(err)
			}
		}
		backup := ""
		if req.nativeOperation == nil {
			backup, err = util.ReplaceDirKeepingBackup(packageDir, stage)
		} else {
			if _, statErr := os.Lstat(packageDir); statErr == nil {
				backup = filepath.Join(nativeOperationDir(req.ConfigDir), "live-backups", action.Package.Marketplace, action.Package.Name)
				if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
					return fail(err)
				}
			} else if !errors.Is(statErr, fs.ErrNotExist) {
				return fail(statErr)
			}
			err = util.ReplaceDirWithBackup(packageDir, stage, backup)
		}
		if err != nil {
			_ = util.RemoveOwnedTree(stage)
			return fail(err)
		}
		attemptedInstall := false
		finalizers = append(finalizers, func(commit bool) error {
			if commit {
				if backup != "" {
					return util.RemoveOwnedTree(backup)
				}
				return nil
			}
			if err := util.RemoveOwnedTree(packageDir); err != nil {
				return err
			}
			if backup == "" {
				if attemptedInstall && util.PathExists(record.CachePath) {
					return uninstallNativePlugin(context.WithoutCancel(ctx), req.Home, record, binding)
				}
				return nil
			}
			if err := os.Rename(backup, packageDir); err != nil {
				return err
			}
			_, err := installNativePlugin(context.WithoutCancel(ctx), req.Home, record, binding)
			return err
		})
		if payload != "." {
			if err := util.WriteFileAtomic(manifestPath, body); err != nil {
				return fail(err)
			}
		}
		if action.Package.Harness == domain.HarnessClaudeCode && action.Package.CopiedSource {
			finish, err := writeNativeCopiedSource(ctx, action, req)
			if err != nil {
				return fail(err)
			}
			finalizers = append(finalizers, finish)
			entry["source"] = map[string]any{"source": "url", "url": "file://" + filepath.Join(filepath.Dir(manifestPath), action.Package.Name+".git")}
			body, err = json.MarshalIndent(catalog, "", "  ")
			if err != nil {
				return fail(err)
			}
			if err := util.WriteFileAtomic(manifestPath, body); err != nil {
				return fail(err)
			}
		}
		// The native runtime reads the canonical catalog beneath installLocation,
		// including when the installer accepted a separately registered file.
		if record.Harness == domain.HarnessClaudeCode && (payload != "." || action.Package.CopiedSource) {
			if err := util.WriteFileAtomic(filepath.Join(record.MarketplaceDir, ".claude-plugin/marketplace.json"), body); err != nil {
				return fail(err)
			}
		}
		if err := addNativeMarketplace(ctx, req.Home, record); err != nil {
			return fail(err)
		}
		attemptedInstall = true
		cacheBefore, err := os.ReadDir(record.CachePath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fail(err)
		}
		cachePath, installErr := installNativePlugin(ctx, req.Home, record, binding)
		if action.Scope == domain.ScopeProject || present {
			installErr = errors.Join(installErr, restoreNativePluginEntry(record, binding, oldEntry, present))
		}
		if installErr != nil {
			return fail(installErr)
		}
		freshCache := !slices.ContainsFunc(cacheBefore, func(item fs.DirEntry) bool {
			return filepath.Clean(cachePath) == filepath.Join(record.CachePath, item.Name())
		})
		record.CachePath = cachePath
		if err := validateNativeRecord(req.ConfigDir, binding, record); err != nil {
			return fail(fmt.Errorf("native installer returned unexpected identity/path for %s", binding))
		}
		if freshCache {
			if err := setNativeSetupState(record, binding, false); err != nil {
				return fail(err)
			}
		}
		record, err = nativeSetupState(record, binding, other[binding])
		if err != nil {
			return fail(err)
		}
		record, err = completeNativeSetup(ctx, req.Home, record, binding)
		if err != nil {
			return fail(err)
		}
		ledger.NativePlugins[binding] = record
	}
	removedMarkets := map[string]bool{}
	for binding, record := range ledger.NativePlugins {
		if desired[binding] {
			continue
		}
		record, err = nativeSetupState(record, binding, other[binding])
		if err != nil {
			return fail(err)
		}
		if err := setNativeSetupState(record, binding, record.SetupPending); err != nil {
			return fail(err)
		}
		retained := false
		viewRetained := false
		for _, owner := range other[binding] {
			if canonicalPath(owner.ConfigHome) == canonicalPath(record.ConfigHome) {
				retained = true
			}
			if canonicalPath(owner.MarketplaceDir) == canonicalPath(record.MarketplaceDir) {
				viewRetained = true
			}
		}
		if !retained || record.Harness == domain.HarnessClaudeCode {
			if err := validateNativeRecord(req.ConfigDir, binding, record); err != nil {
				return fail(err)
			}
			global, err := readNativeConfig(nativeUserConfig(record))
			if err != nil {
				return fail(err)
			}
			activation := nativeActivationTargets(record, binding)[1]
			plugins, _ := global[activation.Section].(map[string]any)
			oldEntry, present := plugins[binding]
			finalizers = append(finalizers, func(commit bool) error {
				if commit {
					if viewRetained {
						return nil
					}
					return removeNativeView(binding, record)
				}
				_, err := installNativePlugin(context.WithoutCancel(ctx), req.Home, record, binding)
				return errors.Join(err, restoreNativePluginEntry(record, binding, oldEntry, present))
			})
			if util.PathExists(record.CachePath) || record.Harness == domain.HarnessClaudeCode {
				if err := uninstallNativePlugin(ctx, req.Home, record, binding); err != nil {
					return fail(err)
				}
			}
			if record.Harness == domain.HarnessClaudeCode {
				entries, err := claudeInstalledEntries(record, binding)
				if err != nil {
					return fail(err)
				}
				viewRetained = viewRetained || len(entries) > 0
			}
			marketRetained := false
			for otherBinding, owner := range ledger.NativePlugins {
				if desired[otherBinding] && canonicalPath(owner.ConfigHome) == canonicalPath(record.ConfigHome) && canonicalPath(owner.MarketplaceDir) == canonicalPath(record.MarketplaceDir) {
					marketRetained = true
				}
			}
			for _, records := range other {
				for _, owner := range records {
					if canonicalPath(owner.ConfigHome) == canonicalPath(record.ConfigHome) && canonicalPath(owner.MarketplaceDir) == canonicalPath(record.MarketplaceDir) {
						marketRetained = true
					}
				}
			}
			marketKey := canonicalPath(record.ConfigHome) + "\x00" + canonicalPath(record.MarketplaceDir)
			if !marketRetained && !removedMarkets[marketKey] {
				removedMarkets[marketKey] = true
				finalizers = append(finalizers, func(commit bool) error {
					if !commit {
						return nil
					}
					return removeNativeMarketplace(ctx, req.Home, record, strings.Split(binding, "@")[1])
				})
			}
		}
		delete(ledger.NativePlugins, binding)
	}
	return finish, nil
}

// Remove obsolete payload paths only after persistence. Keep every live scope's
// source tree and the generated catalog, including descendants of the old root.
func completeNativeSetup(ctx context.Context, home string, record domain.NativePluginRecord, binding string) (domain.NativePluginRecord, error) {
	if record.SetupPending {
		if err := retryNativeNodeSetup(ctx, home, record, binding); err != nil {
			return record, fmt.Errorf("native plugin %s setup is incomplete: %w", binding, err)
		}
		record.SetupPending = false
	}
	record.SetupPending = len(nativeNodeSetupWarnings(ctx, home, record, binding)) > 0
	return record, setNativeSetupState(record, binding, record.SetupPending)
}

func cleanupNativePayloadChanges(eng *engine.Engine, configDir, ledgerPath string, before, after map[string]domain.NativePluginRecord) error {
	other, err := otherNativeRecords(eng, configDir, ledgerPath)
	if err != nil {
		return err
	}
	for binding, prior := range before {
		current, present := after[binding]
		if !present || (prior.MarketplaceDir == current.MarketplaceDir && prior.PayloadPath == current.PayloadPath) {
			continue
		}
		if err := validateNativeRecord(configDir, binding, prior); err != nil {
			return err
		}
		if err := validateNativeRecord(configDir, binding, current); err != nil {
			return err
		}
		keep := []string{filepath.Dir(nativeCatalogPath(current.Harness, current.MarketplaceDir))}
		if current.Harness == domain.HarnessClaudeCode {
			keep = append(keep, filepath.Join(current.MarketplaceDir, ".claude-plugin/marketplace.json"))
		}
		for _, record := range after {
			keep = append(keep, filepath.Join(record.MarketplaceDir, record.PayloadPath), filepath.Dir(nativeCatalogPath(record.Harness, record.MarketplaceDir)))
		}
		for _, records := range other {
			for _, record := range records {
				keep = append(keep, filepath.Join(record.MarketplaceDir, record.PayloadPath), filepath.Dir(nativeCatalogPath(record.Harness, record.MarketplaceDir)))
			}
		}
		oldRoot := filepath.Join(prior.MarketplaceDir, prior.PayloadPath)
		if err := filepath.WalkDir(oldRoot, func(path string, entry fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			for _, kept := range keep {
				if util.IsWithinDir(path, kept) {
					if entry.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if util.IsWithinDir(kept, path) {
					return nil
				}
			}
			if err := util.RemoveOwnedTree(path); err != nil {
				return err
			}
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func removeNativeView(binding string, record domain.NativePluginRecord) error {
	parts := strings.Split(binding, "@")
	manifestPath := nativeRecordCatalogPath(record)
	body, err := os.ReadFile(manifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var catalog map[string]any
	if err := util.UnmarshalJSON(body, &catalog); err != nil {
		return err
	}
	entries, err := plugin.CatalogEntries(body)
	if err != nil {
		return err
	}
	entries = slices.DeleteFunc(entries, func(entry map[string]any) bool { return entry["name"] == parts[0] })
	payload := record.PayloadPath
	if payload == "." && len(entries) != 0 && record.SharedRoot == "" {
		return fmt.Errorf("cannot remove a root payload while its marketplace has sibling entries")
	}
	catalog["plugins"] = entries
	body, err = json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	if err := util.WriteFileAtomic(manifestPath, body); err != nil {
		return err
	}
	if payload == "." && record.SharedRoot != "" && len(entries) != 0 {
		return util.WriteFileAtomic(filepath.Join(record.MarketplaceDir, ".claude-plugin/marketplace.json"), body)
	}
	if record.Harness == domain.HarnessClaudeCode && payload != "." && manifestPath != filepath.Join(record.MarketplaceDir, ".claude-plugin/marketplace.json") {
		if err := util.WriteFileAtomic(filepath.Join(record.MarketplaceDir, ".claude-plugin/marketplace.json"), body); err != nil {
			return err
		}
	}
	if err := util.RemoveOwnedTree(filepath.Join(record.MarketplaceDir, payload)); err != nil {
		return err
	}
	if record.Harness == domain.HarnessClaudeCode && payload != "." {
		return util.RemoveOwnedTree(filepath.Join(filepath.Dir(manifestPath), parts[0]+".git"))
	}
	return nil
}

// Git-source plugins must retain copied loading: the native host installs Node
// output in its cache, while relative local sources load from the rendered tree.
// A bare local repository lets that host own copying and package installation.
func writeNativeCopiedSource(ctx context.Context, action domain.NativePluginAction, req SyncRequest) (func(bool) error, error) {
	root := filepath.Dir(nativeCatalogPath(action.Package.Harness, action.MarketplaceDir))
	stage, err := os.MkdirTemp(root, ".repository-*")
	if err != nil {
		return nil, err
	}
	defer util.RemoveOwnedTree(stage)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	run := func(input []byte, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"--git-dir=" + stage}, args...)...)
		if err := util.InheritConfigLock(ctx, cmd); err != nil {
			return "", err
		}
		cmd.WaitDelay, cmd.Stdin = time.Second, strings.NewReader(string(input))
		cmd.Env = slices.DeleteFunc(os.Environ(), func(value string) bool { return strings.HasPrefix(value, "GIT_") })
		cmd.Env = append(cmd.Env, "GIT_AUTHOR_NAME=AIPack", "GIT_AUTHOR_EMAIL=aipack@example.invalid", "GIT_COMMITTER_NAME=AIPack", "GIT_COMMITTER_EMAIL=aipack@example.invalid", "GIT_AUTHOR_DATE=@0 +0000", "GIT_COMMITTER_DATE=@0 +0000")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("building native copied source: git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	if _, err := run(nil, "init", "--bare", "--template="); err != nil {
		return nil, err
	}
	var index strings.Builder
	// ponytail: one hash process per file; batch object creation if large imports
	// make this measured delivery overhead significant.
	for _, file := range action.Files {
		if file.Mode.IsDir() {
			continue
		}
		mode, content := "100644", file.Content
		if file.Mode&fs.ModeSymlink != 0 {
			mode, content = "120000", []byte(file.Link)
		} else if file.Mode&0o111 != 0 {
			mode = "100755"
		}
		hash, err := run(content, "hash-object", "--no-filters", "-w", "--stdin")
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&index, "%s %s\t%s%c", mode, hash, file.Path, 0)
	}
	if _, err := run([]byte(index.String()), "update-index", "-z", "--index-info"); err != nil {
		return nil, err
	}
	tree, err := run(nil, "write-tree")
	if err != nil {
		return nil, err
	}
	commit, err := run(nil, "commit-tree", tree, "-m", "AIPack native package")
	if err != nil {
		return nil, err
	}
	if _, err := run(nil, "update-ref", "HEAD", commit); err != nil {
		return nil, err
	}
	path := filepath.Join(root, action.Package.Name+".git")
	backup := ""
	if req.nativeOperation == nil {
		backup, err = util.ReplaceDirKeepingBackup(path, stage)
	} else {
		if util.PathExists(path) {
			backup = filepath.Join(nativeOperationDir(req.ConfigDir), "repository-backups", action.Package.Marketplace, action.Package.Name)
			if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
				return nil, err
			}
		}
		err = util.ReplaceDirWithBackup(path, stage, backup)
	}
	if err != nil {
		return nil, err
	}
	return func(commit bool) error {
		if commit {
			return util.RemoveOwnedTree(backup)
		}
		if err := util.RemoveOwnedTree(path); err != nil {
			return err
		}
		if backup != "" {
			return os.Rename(backup, path)
		}
		return nil
	}, nil
}

// Targeted deletion uses the same native installer and ledger persistence
// boundary as sync. Sibling packs and other scopes keep their own claims.
func removePackNativePlugins(req packDeleteLedgerCleanRequest, ledger *domain.Ledger) (func(bool) error, int, error) {
	ctx := req.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateNativeLedgerHarness(req.path, *ledger); err != nil {
		return nil, 0, err
	}
	targets := domain.NewLedger()
	targets.NativePlugins = map[string]domain.NativePluginRecord{}
	for binding, record := range ledger.NativePlugins {
		if req.packName == "" || record.SourcePack == req.packName {
			targets.NativePlugins[binding] = record
		}
	}
	count := len(targets.NativePlugins)
	if count == 0 || req.dryRun {
		return nil, count, nil
	}
	snapshots, err := snapshotNativeActivations(domain.Plan{}, targets)
	if err != nil {
		return nil, count, err
	}
	var finish func(bool) error
	var operation *nativeOperation
	if !req.keepRendered {
		home := ""
		for _, record := range targets.NativePlugins {
			home = record.Home
			break
		}
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return nil, count, err
			}
		}
		syncReq := SyncRequest{TargetSpec: TargetSpec{ConfigDir: req.configDir, Home: home}}
		var err error
		operation, err = beginNativeOperation(req.configDir, domain.Plan{Ledger: req.path}, syncReq, targets)
		if err != nil {
			return nil, count, err
		}
		syncReq.nativeOperation = operation
		retained := maps.Clone(ledger.NativePlugins)
		for binding := range targets.NativePlugins {
			delete(retained, binding)
		}
		finish, err = applyNativePlugins(ctx, req.eng, domain.Plan{Ledger: req.path}, syncReq, &targets, retained)
		if err != nil {
			return nil, count, errors.Join(err, recoverNativeOperationContext(context.WithoutCancel(ctx), req.configDir))
		}
		ledger.NativeOperation = operation.ID
	}
	nativeFinish := finish
	finish = func(commit bool) error {
		var err error
		if nativeFinish != nil {
			err = nativeFinish(commit)
		}
		if !commit && !req.keepRendered {
			err = errors.Join(err, restoreNativeActivations(snapshots))
		}
		if operation != nil {
			if !commit {
				err = errors.Join(err, recoverNativeOperationContext(context.WithoutCancel(ctx), req.configDir))
			} else if err == nil {
				err = util.RemoveOwnedTree(nativeOperationDir(req.configDir))
			}
		}
		return err
	}
	// Native activation also appears in a shared managed settings overlay.
	// Prune only this pack's bindings through the ordinary three-way merge.
	for settingsPath, entry := range ledger.Managed {
		if len(entry.ManagedOverlay) == 0 {
			continue
		}
		var overlay map[string]any
		hid, section := domain.HarnessCodex, "plugins"
		var err error
		if filepath.Ext(settingsPath) == ".json" {
			hid, section = domain.HarnessClaudeCode, "enabledPlugins"
			err = util.UnmarshalJSON(entry.ManagedOverlay, &overlay)
		} else {
			err = toml.Unmarshal(entry.ManagedOverlay, &overlay)
		}
		if err != nil {
			continue
		}
		plugins, _ := overlay[section].(map[string]any)
		changed := false
		for binding, record := range ledger.NativePlugins {
			if (req.packName == "" || record.SourcePack == req.packName) && filepath.Clean(record.SettingsPath) == filepath.Clean(settingsPath) && plugins[binding] != nil {
				delete(plugins, binding)
				changed = true
			}
		}
		if !changed || req.keepRendered {
			continue
		}
		if len(plugins) == 0 {
			delete(overlay, section)
		}
		var body []byte
		if hid == domain.HarnessClaudeCode {
			body, err = json.MarshalIndent(overlay, "", "  ")
		} else {
			body, err = toml.Marshal(overlay)
		}
		if err == nil {
			_, _, _, err = applySharedSettingsOverlay(packDeleteCtx{eng: req.eng, hid: hid, stdout: io.Discard}, *ledger, settingsPath, body, entry.SourcePack)
		}
		if err != nil {
			return nil, count, errors.Join(err, finish(false))
		}
	}
	for binding, record := range ledger.NativePlugins {
		if req.packName == "" || record.SourcePack == req.packName {
			delete(ledger.NativePlugins, binding)
		}
	}
	return finish, count, nil
}
