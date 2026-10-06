package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/plugin"
	"github.com/shrug-labs/aipack/internal/util"
)

// One pending native delivery shares the existing config mutation lock. The
// ledger's operation ID distinguishes a committed apply from a partial one.
type nativeOperation struct {
	ID, Ledger, Home string
	Ready            bool
	Before           map[string]domain.NativePluginRecord
	Touched          map[string]domain.NativePluginRecord
	Views            map[string]nativeViewSnapshot
	Caches           map[string]nativeViewSnapshot `json:",omitempty"`
	Activations      []nativeActivationSnapshot
	Markets          []nativeActivationSnapshot
	Portable         *portableOperation            `json:",omitempty"`
	Skills           map[string]nativeViewSnapshot `json:",omitempty"`
}

type nativeViewSnapshot struct {
	Exists  bool
	Digest  string
	Harness domain.Harness `json:",omitempty"`
}

func (v nativeViewSnapshot) harness() domain.Harness {
	if v.Harness == "" {
		return domain.HarnessCodex
	}
	return v.Harness
}

func nativeOperationDir(configDir string) string {
	return filepath.Join(configDir, ".tmp", "native-operation")
}

func saveNativeOperation(configDir string, op *nativeOperation) error {
	for _, snapshots := range [][]nativeActivationSnapshot{op.Activations, op.Markets} {
		for i := range snapshots {
			if snapshots[i].Present && snapshots[i].Entry != nil {
				var body []byte
				var err error
				if filepath.Ext(snapshots[i].Path) == ".json" {
					body, err = json.Marshal(snapshots[i].Entry)
					snapshots[i].EntryJSON = body
				} else {
					body, err = toml.Marshal(map[string]any{"entry": snapshots[i].Entry})
					snapshots[i].EntryTOML = body
				}
				if err != nil {
					return err
				}
			}
		}
	}
	body, err := json.Marshal(op)
	if err != nil {
		return err
	}
	return util.WriteFileAtomicWithPerms(filepath.Join(nativeOperationDir(configDir), "operation.json"), body, 0o700, 0o600)
}

func beginNativeOperation(configDir string, plan domain.Plan, req SyncRequest, prior domain.Ledger) (*nativeOperation, error) {
	if req.DryRun {
		return nil, nil
	}
	portable, err := planPortableOperation(plan, prior)
	if err != nil {
		return nil, err
	}
	if len(plan.NativePlugins)+len(prior.NativePlugins) == 0 && portable == nil {
		return nil, nil
	}
	if portable != nil && len(plan.NativePlugins)+len(prior.NativePlugins) != 0 {
		return nil, fmt.Errorf("portable and native installations require separate harness ledgers")
	}
	if runtime.GOOS == "windows" && portable == nil {
		return nil, fmt.Errorf("recoverable native plugin delivery is not supported on Windows")
	}
	if !filepath.IsAbs(req.Home) {
		return nil, fmt.Errorf("native delivery requires an absolute home directory")
	}
	op := &nativeOperation{ID: rand.Text(), Ledger: plan.Ledger, Home: req.Home,
		Before: maps.Clone(prior.NativePlugins), Touched: maps.Clone(prior.NativePlugins), Views: map[string]nativeViewSnapshot{}, Portable: portable}
	if op.Touched == nil {
		op.Touched = map[string]domain.NativePluginRecord{}
	}
	for _, action := range plan.NativePlugins {
		payload, err := nativePayloadPath(action.Package)
		if err != nil {
			return nil, err
		}
		generation, err := nativeGeneration(action)
		if err != nil {
			return nil, err
		}
		sharedRoot, err := nativeSharedRootGeneration(action)
		if err != nil {
			return nil, err
		}
		op.Touched[action.Package.Binding()] = domain.NativePluginRecord{Harness: action.Package.Harness,
			SharedRoot:        sharedRoot,
			RootDirectoryName: action.Package.RootDirectoryName,
			Namespace:         action.Namespace,
			Generation:        generation,
			ConfigHome:        action.ConfigHome, MarketplaceDir: action.MarketplaceDir, PayloadPath: payload, SettingsPath: action.SettingsPath,
			MCPPermissionServers: action.MCPPermissionServers,
			CachePath:            filepath.Join(action.ConfigHome, "plugins/cache", action.Package.Marketplace, action.Package.Name), Home: req.Home}
	}
	op.Activations, err = snapshotNativeActivations(plan, prior)
	if err != nil {
		return nil, err
	}
	var setupTargets []nativeActivationSnapshot
	for _, records := range []map[string]domain.NativePluginRecord{op.Touched, op.Before} {
		for binding, record := range records {
			if _, err := nativeSetupState(record, binding, nil); err != nil {
				return nil, err
			}
			if target, managed := nativeSetupTarget(record, binding); managed {
				setupTargets = append(setupTargets, target)
			}
		}
	}
	setups, err := snapshotNativeConfigEntries(setupTargets)
	if err != nil {
		return nil, err
	}
	op.Activations = append(op.Activations, setups...)
	var marketTargets []nativeActivationSnapshot
	for binding, record := range op.Touched {
		if err := validateNativeRecord(configDir, binding, record); err != nil {
			return nil, err
		}
		market := strings.Split(binding, "@")[1]
		op.Views[market] = nativeViewSnapshot{Harness: record.Harness}
		marketTargets = append(marketTargets, nativeMarketplaceTargets(record, market)...)
	}
	op.Markets, err = snapshotNativeConfigEntries(marketTargets)
	if err != nil {
		return nil, err
	}
	op.Skills = map[string]nativeViewSnapshot{}
	roots := operationSkillRoots(*op)
	packs := map[string]bool{}
	for _, record := range prior.NativePlugins {
		packs[record.SourcePack] = true
	}
	for _, action := range plan.NativePlugins {
		packs[action.SourcePack] = true
	}
	for _, entry := range prior.Managed {
		if entry.Delivery != nil {
			packs[entry.SourcePack] = true
		}
	}
	for _, action := range plan.Writes {
		if action.Delivery != nil {
			packs[action.SourcePack] = true
		}
	}
	delete(packs, "")
	for _, action := range plan.Writes {
		if packs[action.SourcePack] && action.Category == domain.CategorySkills && filepath.Base(action.Dst) == domain.SkillEntryFile && roots[filepath.Dir(filepath.Dir(action.Dst))] {
			op.Skills[filepath.Dir(action.Dst)] = nativeViewSnapshot{}
		}
	}
	for path, entry := range prior.Managed {
		if packs[entry.SourcePack] && filepath.Base(path) == domain.SkillEntryFile && roots[filepath.Dir(filepath.Dir(path))] {
			op.Skills[filepath.Dir(path)] = nativeViewSnapshot{}
		}
	}
	dir := nativeOperationDir(configDir)
	if err := os.Mkdir(dir, 0o700); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return nil, err
		}
		if err := os.Mkdir(dir, 0o700); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("native delivery already pending: %w", err)
	}
	if err := saveNativeOperation(configDir, op); err != nil {
		return nil, err
	}
	if err := snapshotPortableOperation(configDir, portable); err != nil {
		return nil, err
	}
	for path := range op.Skills {
		files, err := (engine.OSFS{}).ReadPackage(path)
		if _, rootErr := os.Lstat(path); os.IsNotExist(rootErr) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := (engine.OSFS{}).WritePackage(operationSkillBackup(configDir, path), files); err != nil {
			return nil, err
		}
		op.Skills[path] = nativeViewSnapshot{Exists: true, Digest: domain.SingleFileDigest(domain.PackageManifest(files))}
	}
	for market, snapshot := range op.Views {
		view := filepath.Join(configDir, "rendered-plugins", string(snapshot.harness()), market)
		before, err := snapshotNativeTree(view, filepath.Join(dir, "views", market))
		if err != nil {
			return nil, err
		}
		before.Harness = snapshot.harness()
		op.Views[market] = before
	}
	op.Caches = map[string]nativeViewSnapshot{}
	for binding, record := range op.Touched {
		before, err := snapshotNativeTree(nativeCacheRoot(record, binding), filepath.Join(dir, "caches", binding))
		if err != nil {
			return nil, err
		}
		before.Harness = record.Harness
		op.Caches[binding] = before
	}
	op.Ready = true
	if err := saveNativeOperation(configDir, op); err != nil {
		return nil, err
	}
	return op, nil
}

func nativeCacheRoot(record domain.NativePluginRecord, binding string) string {
	parts := strings.Split(binding, "@") // callers validate binding ownership first
	return filepath.Join(record.ConfigHome, "plugins/cache", parts[1], parts[0])
}

func operationSkillRoots(op nativeOperation) map[string]bool {
	roots := map[string]bool{}
	for _, records := range []map[string]domain.NativePluginRecord{op.Before, op.Touched} {
		for _, record := range records {
			if record.Harness == domain.HarnessClaudeCode {
				roots[filepath.Join(filepath.Dir(record.SettingsPath), "skills")] = true
			}
		}
	}
	if op.Portable != nil {
		for _, snapshot := range op.Portable.Payloads {
			roots[filepath.Join(filepath.Dir(snapshot.Delivery.SettingsPath), "skills")] = true
		}
	}
	return roots
}

func operationSkillBackup(configDir, path string) string {
	return filepath.Join(nativeOperationDir(configDir), "skills", util.ContentDigest([]byte(path)))
}

// Native receipts bound the ordinary MCP config paths for route rollback.
// Global Claude config can use HOME or an explicit CLAUDE_CONFIG_DIR.
func operationMCPPaths(op nativeOperation) map[string]bool {
	paths := map[string]bool{}
	for _, records := range []map[string]domain.NativePluginRecord{op.Before, op.Touched} {
		for _, record := range records {
			if record.Harness != domain.HarnessClaudeCode {
				continue
			}
			if claudeScope(record) == "local" {
				paths[filepath.Join(filepath.Dir(filepath.Dir(record.SettingsPath)), ".mcp.json")] = true
			} else {
				paths[filepath.Join(record.ConfigHome, ".claude.json")] = true
				home := record.Home
				if home == "" {
					home = op.Home
				}
				paths[filepath.Join(home, ".claude.json")] = true
			}
		}
	}
	return paths
}

// Ordinary entrypoints participate in native delivery rollback. Keep complete
// skill trees so returning to ordinary delivery also restores their assets.
func validateOperationSkills(configDir string, op nativeOperation) error {
	roots, fs := operationSkillRoots(op), engine.OSFS{}
	for path, before := range op.Skills {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || !roots[filepath.Dir(path)] || canonicalPath(path) != filepath.Join(canonicalPath(filepath.Dir(path)), filepath.Base(path)) {
			return fmt.Errorf("native recovery contains an unowned ordinary skill: %s", path)
		}
		if before.Exists {
			files, err := fs.ReadPackage(operationSkillBackup(configDir, path))
			if err != nil || domain.SingleFileDigest(domain.PackageManifest(files)) != before.Digest {
				return fmt.Errorf("ordinary skill recovery snapshot changed; both copies retained: %s", path)
			}
		}
		if _, err := fs.ReadPackage(path); err != nil {
			if _, rootErr := os.Lstat(path); !os.IsNotExist(rootErr) {
				return fmt.Errorf("cannot recover ordinary skill %s: %w", path, err)
			}
		}
	}
	return nil
}

func restoreOperationSkills(configDir string, op nativeOperation) error {
	fs := engine.OSFS{}
	for path, before := range op.Skills {
		files, err := fs.ReadPackage(path)
		missing := false
		if err != nil {
			_, rootErr := os.Lstat(path)
			missing = os.IsNotExist(rootErr)
			if !missing {
				return err
			}
		}
		if !missing && before.Exists && domain.SingleFileDigest(domain.PackageManifest(files)) == before.Digest {
			continue
		}
		if !missing {
			retained := filepath.Join(nativeOperationDir(configDir), "interrupted-skills", util.ContentDigest([]byte(path)))
			if err := os.MkdirAll(filepath.Dir(retained), 0o700); err != nil {
				return err
			}
			if _, err := os.Lstat(retained); !os.IsNotExist(err) {
				return fmt.Errorf("ordinary skill changed again during recovery; both copies retained: %s", path)
			}
			if err := os.Rename(path, retained); err != nil {
				return err
			}
		}
		if before.Exists {
			files, err := fs.ReadPackage(operationSkillBackup(configDir, path))
			if err != nil {
				return err
			}
			if err := fs.WritePackage(path, files); err != nil {
				return err
			}
		}
	}
	return nil
}

func snapshotNativeTree(source, backup string) (nativeViewSnapshot, error) {
	info, err := os.Lstat(source)
	if errors.Is(err, fs.ErrNotExist) {
		return nativeViewSnapshot{}, nil
	}
	if err != nil {
		return nativeViewSnapshot{}, err
	}
	if !info.IsDir() {
		return nativeViewSnapshot{}, fmt.Errorf("native snapshot source is not a directory: %s", source)
	}
	files, err := plugin.ReadPayloadFiles(source)
	if err != nil {
		return nativeViewSnapshot{}, err
	}
	// ponytail: complete tree copies; use rename-backed snapshots if large
	// payloads or repeated recovery attempts make copying material.
	if err := os.MkdirAll(backup, 0o700); err != nil {
		return nativeViewSnapshot{}, err
	}
	if err := plugin.WriteFiles(backup, files); err != nil {
		return nativeViewSnapshot{}, err
	}
	digest, err := packTreeDigest(backup)
	return nativeViewSnapshot{Exists: true, Digest: digest}, err
}

func retainNativeCaches(configDir string, op nativeOperation) error {
	for binding, before := range op.Caches {
		cache := nativeCacheRoot(op.Touched[binding], binding)
		current, err := packTreeDigest(cache)
		if errors.Is(err, fs.ErrNotExist) || err == nil && before.Exists && current == before.Digest {
			continue
		}
		if err != nil {
			return err
		}
		retained := filepath.Join(nativeOperationDir(configDir), "interrupted-caches")
		if err := os.MkdirAll(retained, 0o700); err != nil {
			return err
		}
		backup, err := os.MkdirTemp(retained, binding+"-")
		if err != nil {
			return err
		}
		snapshot, err := snapshotNativeTree(cache, backup)
		if err != nil {
			return err
		}
		after, err := packTreeDigest(cache)
		if err != nil {
			return fmt.Errorf("retaining interrupted native cache for %s: %w", binding, err)
		}
		if snapshot.Digest != current || after != current {
			return fmt.Errorf("native cache changed while retaining interrupted output for %s", binding)
		}
	}
	return nil
}

func restoreNativeCaches(configDir string, op nativeOperation) error {
	for binding, before := range op.Caches {
		cache := nativeCacheRoot(op.Touched[binding], binding)
		if current, err := packTreeDigest(cache); before.Exists && err == nil && current == before.Digest {
			continue
		}
		var files []plugin.File
		if before.Exists {
			var err error
			files, err = plugin.ReadPayloadFiles(filepath.Join(nativeOperationDir(configDir), "caches", binding))
			if err != nil {
				return err
			}
		}
		if err := util.RemoveOwnedTree(cache); err != nil {
			return err
		}
		if before.Exists {
			if err := os.MkdirAll(cache, 0o700); err != nil {
				return err
			}
			if err := plugin.WriteFiles(cache, files); err != nil {
				return err
			}
		}
	}
	return nil
}

// Interrupted renderings are retained rather than discarded during rollback.
// Native runtime data remains outside these owned views and native uninstall.
func restoreNativeViews(configDir string, op nativeOperation) error {
	dir := nativeOperationDir(configDir)
	for market, before := range op.Views {
		if !domain.ValidNativeName(market) {
			return fmt.Errorf("invalid recovery marketplace")
		}
		view := filepath.Join(configDir, "rendered-plugins", string(before.harness()), market)
		backup := filepath.Join(dir, "views", market)
		if before.Exists {
			digest, err := packTreeDigest(backup)
			if err != nil {
				return fmt.Errorf("cannot verify native recovery snapshot for %s: %w", market, err)
			}
			if digest != before.Digest {
				return fmt.Errorf("native recovery snapshot changed for %s; both renderings retained", market)
			}
			if current, err := packTreeDigest(view); err == nil && current == before.Digest {
				continue
			}
		}
		if _, err := os.Lstat(view); err == nil {
			retained := filepath.Join(dir, "interrupted", market)
			if util.PathExists(retained) {
				return fmt.Errorf("native view changed again during recovery; both renderings retained for %s", market)
			}
			if err := os.MkdirAll(filepath.Dir(retained), 0o700); err != nil {
				return err
			}
			if err := os.Rename(view, retained); err != nil {
				return err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if !before.Exists {
			continue
		}
		files, err := plugin.ReadPayloadFiles(backup)
		if err != nil {
			return err
		}
		stage := filepath.Join(dir, "restore", market)
		if err := util.RemoveOwnedTree(stage); err != nil {
			return err
		}
		if err := os.MkdirAll(stage, 0o700); err != nil {
			return err
		}
		if err := plugin.WriteFiles(stage, files); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(view), 0o700); err != nil {
			return err
		}
		if err := os.Rename(stage, view); err != nil {
			return err
		}
	}
	return nil
}

func recoverNativeOperation(configDir string) error {
	return recoverNativeOperationContext(context.Background(), configDir)
}

func recoverNativeOperationContext(ctx context.Context, configDir string) error {
	dir := nativeOperationDir(configDir)
	body, err := os.ReadFile(filepath.Join(dir, "operation.json"))
	if errors.Is(err, fs.ErrNotExist) {
		if util.PathExists(dir) {
			retained := filepath.Join(configDir, ".tmp/native-recoveries", "unprepared-"+rand.Text())
			if err := os.MkdirAll(filepath.Dir(retained), 0o700); err != nil {
				return err
			}
			return os.Rename(dir, retained)
		}
		return nil
	}
	if err != nil {
		return err
	}
	var op nativeOperation
	if err := json.Unmarshal(body, &op); err != nil {
		return fmt.Errorf("reading pending native delivery: %w", err)
	}
	if !op.Ready {
		return util.RemoveOwnedTree(dir) // no native writes precede the ready record
	}
	ledgerName := filepath.Base(op.Ledger)
	if len(op.ID) != 26 || !domain.ValidNativeName(op.ID) || !filepath.IsAbs(op.Home) || !util.IsWithinDir(canonicalPath(op.Ledger), canonicalPath(filepath.Join(configDir, "ledger"))) || (ledgerName != "codex.json" && ledgerName != "claudecode.json" && ledgerName != "opencode.json") {
		return fmt.Errorf("invalid pending native delivery identity")
	}
	if op.Portable != nil {
		if ledgerName != "opencode.json" || len(op.Before)+len(op.Touched)+len(op.Views)+len(op.Caches)+len(op.Activations)+len(op.Markets) != 0 {
			return fmt.Errorf("invalid portable recovery ownership")
		}
		ledger, warnings, err := engine.New(nil, nil).LoadLedger(op.Ledger)
		if err != nil || len(warnings) > 0 {
			return fmt.Errorf("cannot determine interrupted portable delivery commit: %v: %v", err, warnings)
		}
		return recoverPortableOperation(configDir, op, ledger)
	}
	if ledgerName == "opencode.json" {
		return fmt.Errorf("portable recovery is missing its package ownership")
	}
	allowedActivations, allowedMarkets := map[string]bool{}, map[string]bool{}
	allowedPermissionServers := map[string][]string{}
	knownViews := map[string]domain.Harness{}
	for _, records := range []map[string]domain.NativePluginRecord{op.Touched, op.Before} {
		for binding, record := range records {
			if err := validateNativeRecord(configDir, binding, record); err != nil {
				return err
			}
			if ledgerName != string(record.Harness)+".json" {
				return fmt.Errorf("native recovery harness does not match its ledger")
			}
			market := strings.Split(binding, "@")[1]
			knownViews[market] = record.Harness
			for _, target := range nativeMarketplaceTargets(record, market) {
				allowedMarkets[nativeSnapshotKey(target, target.Section)] = true
			}
			for _, target := range nativeActivationTargets(record, binding) {
				key := nativeSnapshotKey(target, target.Section)
				allowedActivations[key] = true
				allowedPermissionServers[key] = append(allowedPermissionServers[key], target.PermissionServers...)
			}
			if target, managed := nativeSetupTarget(record, binding); managed {
				if _, err := nativeSetupState(record, binding, nil); err != nil {
					return err
				}
				allowedActivations[nativeSnapshotKey(target, target.Section)] = true
			}
		}
	}
	for market, view := range op.Views {
		if knownViews[market] != view.harness() {
			return fmt.Errorf("native recovery contains an unowned view")
		}
	}
	for binding, before := range op.Caches {
		record, owned := op.Touched[binding]
		if !owned || before.harness() != record.Harness {
			return fmt.Errorf("native recovery contains an unowned cache")
		}
		if before.Exists {
			digest, err := packTreeDigest(filepath.Join(dir, "caches", binding))
			if err != nil {
				return fmt.Errorf("cannot verify native cache recovery snapshot for %s: %w", binding, err)
			}
			if digest != before.Digest {
				return fmt.Errorf("native cache recovery snapshot changed for %s; both copies retained", binding)
			}
		}
	}
	mcpPaths := operationMCPPaths(op)
	for _, group := range []struct {
		snapshots []nativeActivationSnapshot
		allowed   map[string]bool
		section   string
	}{{op.Activations, allowedActivations, "plugins"}, {op.Markets, allowedMarkets, "marketplaces"}} {
		for _, snapshot := range group.snapshots {
			key := nativeSnapshotKey(snapshot, group.section)
			if snapshot.Section == "mcpServers" && group.section == "plugins" && mcpPaths[snapshot.Path] && domain.ValidNativeName(snapshot.Binding) && canonicalPath(snapshot.Path) == filepath.Join(canonicalPath(filepath.Dir(snapshot.Path)), filepath.Base(snapshot.Path)) {
				allowedActivations[key] = true
			}
			if !group.allowed[key] {
				return fmt.Errorf("native recovery contains an unowned config key")
			}
			servers := allowedPermissionServers[key]
			slices.Sort(servers)
			servers = slices.Compact(servers)
			if !slices.Equal(servers, snapshot.PermissionServers) {
				return fmt.Errorf("native recovery contains unowned permission servers")
			}
			if snapshot.Section == "permissions" {
				entry, err := snapshot.entry()
				if err != nil {
					return err
				}
				items, err := filterNativePermissions(entry, servers, false)
				if err != nil || len(items) != 0 {
					return fmt.Errorf("native recovery contains unowned permission rules")
				}
			}
			if snapshot.Section == "setups" && snapshot.Present {
				entry, err := snapshot.entry()
				if err != nil {
					return err
				}
				if _, err := nativeSetupVersions(entry); err != nil {
					return fmt.Errorf("native recovery contains an invalid setup result")
				}
			}
			if snapshot.Present {
				if _, err := snapshot.entry(); err != nil {
					return fmt.Errorf("invalid native config recovery snapshot: %w", err)
				}
			}
		}
	}
	eng := engine.New(nil, nil)
	ledger, warnings, err := eng.LoadLedger(op.Ledger)
	if err != nil {
		return fmt.Errorf("cannot determine interrupted native delivery commit: %w", err)
	}
	if len(warnings) > 0 {
		return fmt.Errorf("cannot determine interrupted native delivery commit: %s", warnings[0])
	}
	if err := validateOperationSkills(configDir, op); err != nil {
		return err
	}
	if ledger.NativeOperation == op.ID {
		removed := domain.NewLedger()
		removed.NativePlugins = maps.Clone(op.Before)
		for binding := range ledger.NativePlugins {
			delete(removed.NativePlugins, binding)
		}
		// Include the committed ledger in shared ownership checks. The
		// temporary ledger is only an input to idempotent removal cleanup.
		finish, err := applyNativePlugins(ctx, eng, domain.Plan{Ledger: op.Ledger + ".recovery"}, SyncRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: op.Home}}, &removed, nil)
		if err != nil {
			return err
		}
		if err := finish(true); err != nil {
			return err
		}
		if err := cleanupNativePayloadChanges(eng, configDir, op.Ledger, op.Before, ledger.NativePlugins); err != nil {
			return err
		}
		return util.RemoveOwnedTree(dir)
	}
	if err := restoreOperationSkills(configDir, op); err != nil {
		return err
	}
	if err := retainNativeCaches(configDir, op); err != nil {
		return err
	}
	if err := restoreNativeViews(configDir, op); err != nil {
		return err
	}
	for binding, record := range op.Touched {
		if prior, exists := op.Before[binding]; exists {
			if err := addNativeMarketplace(ctx, op.Home, prior); err != nil {
				return err
			}
			if _, err := installNativePlugin(ctx, op.Home, prior, binding); err != nil {
				return err
			}
		} else if util.PathExists(record.CachePath) || record.Harness == domain.HarnessClaudeCode {
			if err := uninstallNativePlugin(ctx, op.Home, record, binding); err != nil {
				return err
			}
		}
	}
	if err := restoreNativeCaches(configDir, op); err != nil {
		return err
	}
	for _, snapshot := range op.Markets {
		entry, err := snapshot.entry()
		if err != nil {
			return err
		}
		if !snapshot.Present {
			for _, record := range op.Touched {
				for _, target := range nativeMarketplaceTargets(record, snapshot.Binding) {
					if nativeSnapshotKey(target, target.Section) == nativeSnapshotKey(snapshot, "marketplaces") {
						if err := removeNativeMarketplace(ctx, op.Home, record, snapshot.Binding); err != nil {
							return err
						}
					}
				}
			}
		}
		if err := restoreNativeConfigEntry(snapshot.Path, snapshot.section("marketplaces"), snapshot.Binding, entry, snapshot.Present); err != nil {
			return err
		}
	}
	if err := restoreNativeActivations(op.Activations); err != nil {
		return err
	}
	if !util.PathExists(filepath.Join(dir, "interrupted")) && !util.PathExists(filepath.Join(dir, "interrupted-caches")) && !util.PathExists(filepath.Join(dir, "interrupted-skills")) {
		return util.RemoveOwnedTree(dir)
	}
	retained := filepath.Join(configDir, ".tmp", "native-recoveries", op.ID)
	if err := os.MkdirAll(filepath.Dir(retained), 0o700); err != nil {
		return err
	}
	return os.Rename(dir, retained)
}
