package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

func nativeUserConfig(record domain.NativePluginRecord) string {
	if record.Harness == domain.HarnessClaudeCode {
		return filepath.Join(record.ConfigHome, "settings.json")
	}
	return filepath.Join(record.ConfigHome, "config.toml")
}

func nativeCatalogPath(hid domain.Harness, marketDir string) string {
	if hid == domain.HarnessClaudeCode {
		return filepath.Join(marketDir, ".aipack-marketplace/marketplace.json")
	}
	return filepath.Join(marketDir, ".agents/plugins/marketplace.json")
}

func nativeRecordCatalogPath(record domain.NativePluginRecord) string {
	return nativeCatalogPath(record.Harness, record.MarketplaceDir)
}

func nativeActivationTargets(record domain.NativePluginRecord, binding string) []nativeActivationSnapshot {
	section := "plugins"
	if record.Harness == domain.HarnessClaudeCode {
		section = "enabledPlugins"
	}
	targets := []nativeActivationSnapshot{{Path: record.SettingsPath, Binding: binding, Section: section}, {Path: nativeUserConfig(record), Binding: binding, Section: section}}
	if record.Harness == domain.HarnessClaudeCode {
		targets = append(targets, nativeActivationSnapshot{Path: filepath.Join(record.ConfigHome, "plugins/installed_plugins.json"), Binding: binding, Section: "plugins"})
		if len(record.MCPPermissionServers) > 0 {
			for _, key := range []string{"allow", "deny"} {
				targets = append(targets, nativeActivationSnapshot{Path: record.SettingsPath, Binding: key, Section: "permissions", PermissionServers: record.MCPPermissionServers})
			}
		}
	}
	return targets
}

func nativeMarketplaceTargets(record domain.NativePluginRecord, market string) []nativeActivationSnapshot {
	if record.Harness == domain.HarnessClaudeCode {
		return []nativeActivationSnapshot{{Path: filepath.Join(record.ConfigHome, "plugins/known_marketplaces.json"), Binding: market}, {Path: nativeUserConfig(record), Binding: market, Section: "extraKnownMarketplaces"}}
	}
	return []nativeActivationSnapshot{{Path: nativeUserConfig(record), Binding: market, Section: "marketplaces"}}
}

func snapshotNativeConfigEntries(targets []nativeActivationSnapshot) ([]nativeActivationSnapshot, error) {
	seen := map[string]int{}
	var snapshots []nativeActivationSnapshot
	for _, target := range targets {
		key := nativeSnapshotKey(target, target.Section)
		if index, exists := seen[key]; exists {
			snapshots[index].PermissionServers = append(snapshots[index].PermissionServers, target.PermissionServers...)
			continue
		}
		seen[key] = len(snapshots)
		target.PermissionServers = slices.Clone(target.PermissionServers)
		snapshots = append(snapshots, target)
	}
	roots := map[string]map[string]any{}
	for i := range snapshots {
		target := &snapshots[i]
		root, loaded := roots[target.Path]
		var err error
		if !loaded {
			root, err = readNativeConfig(target.Path)
			if err != nil {
				return nil, err
			}
			roots[target.Path] = root
		}
		entries := root
		if target.Section != "" {
			if raw, exists := root[target.Section]; exists {
				var ok bool
				entries, ok = raw.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("native config %s section %q must be an object", target.Path, target.Section)
				}
			} else {
				entries = nil
			}
		}
		target.Entry, target.Present = entries[target.Binding]
		if target.Section == "permissions" {
			slices.Sort(target.PermissionServers)
			target.PermissionServers = slices.Compact(target.PermissionServers)
			target.Entry, err = filterNativePermissions(target.Entry, target.PermissionServers, true)
			if err != nil {
				return nil, err
			}
		}
	}
	return snapshots, nil
}

func filterNativePermissions(value any, servers []string, retain bool) ([]any, error) {
	items, ok := value.([]any)
	if value != nil && !ok {
		return nil, fmt.Errorf("native permission rules must be an array")
	}
	out := []any{}
	for _, item := range items {
		rule, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("native permission rule must be a string")
		}
		owned := slices.ContainsFunc(servers, func(server string) bool { return strings.HasPrefix(rule, "mcp__"+server+"__") })
		if owned == retain {
			out = append(out, item)
		}
	}
	return out, nil
}

func nativeSnapshotKey(s nativeActivationSnapshot, fallback string) string {
	return canonicalPath(s.Path) + "\x00" + s.section(fallback) + "\x00" + s.Binding
}

func claudeScope(record domain.NativePluginRecord) string {
	if canonicalPath(record.SettingsPath) == canonicalPath(nativeUserConfig(record)) {
		return "user"
	}
	return "local"
}

func runNativePlugin(ctx context.Context, home string, record domain.NativePluginRecord, args ...string) ([]byte, error) {
	if err := os.MkdirAll(record.ConfigHome, 0o700); err != nil {
		return nil, fmt.Errorf("creating native config home: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd, err := nativeHostCommand(ctx, home, record, append([]string{"plugin"}, args...)...)
	if err != nil {
		return nil, err
	}
	var diagnostics strings.Builder
	cmd.Stderr = &diagnostics
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s plugin %s: %w: %s %s", filepath.Base(cmd.Path), strings.Join(args, " "), err, strings.TrimSpace(diagnostics.String()), strings.TrimSpace(string(out)))
	}
	return out, nil
}

func nativeHostCommand(ctx context.Context, home string, record domain.NativePluginRecord, args ...string) (*exec.Cmd, error) {
	binary, envName := "codex", "CODEX_HOME"
	if record.Harness == domain.HarnessClaudeCode {
		binary, envName = "claude", "CLAUDE_CONFIG_DIR"
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	if err := util.InheritConfigLock(ctx, cmd); err != nil {
		return nil, err
	}
	cmd.WaitDelay = time.Second
	if record.Home != "" {
		home = record.Home
	}
	if record.Harness == domain.HarnessClaudeCode {
		cmd.Dir = home
		if claudeScope(record) == "local" {
			cmd.Dir = filepath.Dir(filepath.Dir(record.SettingsPath))
		}
	}
	env := slices.DeleteFunc(os.Environ(), func(value string) bool {
		return strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, envName+"=")
	})
	cmd.Env = append(env, "HOME="+home, envName+"="+record.ConfigHome)
	return cmd, nil
}

func addNativeMarketplace(ctx context.Context, home string, record domain.NativePluginRecord) error {
	args := []string{"marketplace", "add", record.MarketplaceDir, "--json"}
	if record.Harness == domain.HarnessClaudeCode {
		record.SettingsPath = nativeUserConfig(record)
		catalog := nativeCatalogPath(record.Harness, record.MarketplaceDir)
		if !util.PathExists(catalog) {
			catalog = record.MarketplaceDir
		}
		args = []string{"marketplace", "add", catalog, "--scope", "user"}
	}
	_, err := runNativePlugin(ctx, home, record, args...)
	if err != nil && record.Harness == domain.HarnessCodex && strings.Contains(err.Error(), "not allowed by requirements") {
		return fmt.Errorf("%w\n%s", err, codexMarketplaceApproval(record.MarketplaceDir, filepath.Base(record.MarketplaceDir)))
	}
	return err
}

func installNativePlugin(ctx context.Context, home string, record domain.NativePluginRecord, binding string) (string, error) {
	args := []string{"add", binding, "--json"}
	if record.Harness == domain.HarnessClaudeCode {
		args = []string{"install", binding, "--json", "--scope", claudeScope(record)}
		entry, err := claudeInstalledEntry(record, binding)
		if err != nil {
			return "", err
		}
		if entry != nil {
			args[0] = "update"
		}
	}
	out, err := runNativePlugin(ctx, home, record, args...)
	if err != nil {
		return "", err
	}
	var result struct {
		PluginID, InstalledPath, Outcome, UpdateOutcome string
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return "", err
	}
	if result.PluginID != binding || record.Harness == domain.HarnessClaudeCode && result.Outcome != "ok" {
		return "", fmt.Errorf("native installer did not confirm installation for %s: %s", binding, out)
	}
	if _, managed := nativeSetupTarget(record, binding); managed && result.UpdateOutcome == "up_to_date" {
		// Claude skips same-version payload changes. Reinstall the owned scope
		// through its native API, retaining data and the upstream version.
		if err := uninstallNativePlugin(ctx, home, record, binding); err != nil {
			return "", err
		}
		entry, err := claudeInstalledEntry(record, binding)
		if err != nil {
			return "", err
		}
		if entry != nil {
			return "", fmt.Errorf("native installer retained the scope after uninstalling %s", binding)
		}
		return installNativePlugin(ctx, home, record, binding)
	}
	if record.Harness == domain.HarnessClaudeCode {
		entry, err := claudeInstalledEntry(record, binding)
		if err != nil {
			return "", err
		}
		result.InstalledPath, _ = entry["installPath"].(string)
	}
	if result.InstalledPath == "" {
		return "", fmt.Errorf("native installer returned no cache path for %s", binding)
	}
	return result.InstalledPath, nil
}

// Claude 2.1.284 can report a successful install after npm ci fails.
// A binding exists independently of whether its locked packages are present.
// Development/optional packages may be omitted by npm; inspect required runtime
// output only. Presence is not a general runtime readiness check.
func nativeSetupWarnings(record domain.NativePluginRecord, binding string) []domain.Warning {
	var err error
	record, err = nativeSetupState(record, binding, nil)
	if err != nil {
		return []domain.Warning{warningf("plugin-setup", "native plugin %s setup state cannot be inspected: %v", binding, err)}
	}
	if record.SetupPending {
		return []domain.Warning{warningf("plugin-setup", "plugin %s dependency setup is incomplete; run sync again to retry", binding)}
	}
	if record.Harness != domain.HarnessClaudeCode || !util.PathExists(filepath.Join(record.CachePath, "package-lock.json")) {
		return nil
	}
	body, err := os.ReadFile(filepath.Join(record.CachePath, "package.json"))
	var pkg struct {
		Dependencies, OptionalDependencies map[string]json.RawMessage
	}
	if err == nil {
		err = json.Unmarshal(body, &pkg)
	}
	if err != nil {
		return []domain.Warning{warningf("plugin-setup", "native plugin %s package setup cannot be inspected: %v", binding, err)}
	}
	missing := false
	for name := range pkg.Dependencies {
		if _, optional := pkg.OptionalDependencies[name]; optional {
			continue
		}
		if !filepath.IsLocal(name) {
			return []domain.Warning{warningf("plugin-setup", "native plugin %s has an invalid Node package name", binding)}
		}
		info, err := os.Stat(filepath.Join(record.CachePath, "node_modules", filepath.FromSlash(name), "package.json"))
		missing = missing || err != nil || !info.Mode().IsRegular()
	}
	if !missing {
		return nil
	}
	return []domain.Warning{warningf("plugin-setup", "native plugin %s is installed but its locked Node packages are absent; setup is incomplete", binding)}
}

// Retry only an already-incomplete installation. npm owns lockfile resolution;
// AIPack does not infer requirements or execute package lifecycle scripts.
func retryNativeNodeSetup(ctx context.Context, home string, record domain.NativePluginRecord, binding string) error {
	if _, err := runNativeNodeCommand(ctx, home, record, "ci", "--ignore-scripts"); err != nil {
		return fmt.Errorf("retry native Node setup: %w", err)
	}
	return setNativeSetupState(record, binding, false)
}

// npm validates its own installed tree without fetching packages or running
// scripts. This is bounded installation evidence, not runtime readiness.
func nativeNodeSetupWarnings(ctx context.Context, home string, record domain.NativePluginRecord, binding string) []domain.Warning {
	if warnings := nativeSetupWarnings(record, binding); len(warnings) > 0 {
		return warnings
	}
	if record.Harness != domain.HarnessClaudeCode || !util.PathExists(filepath.Join(record.CachePath, "package-lock.json")) {
		return nil
	}
	_, err := runNativeNodeCommand(ctx, home, record, "ls", "--all", "--depth=Infinity", "--json", "--offline", "--ignore-scripts", "--logs-max=0", "--global=false", "--package-lock-only=false", "--prefix="+record.CachePath, "--include=prod", "--omit=dev", "--omit=optional")
	if err != nil {
		return []domain.Warning{warningf("plugin-setup", "native plugin %s installed Node package tree cannot be validated; setup is incomplete: %v", binding, err)}
	}
	return nil
}

func runNativeNodeCommand(ctx context.Context, home string, record domain.NativePluginRecord, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npm", args...)
	if err := util.InheritConfigLock(ctx, cmd); err != nil {
		return nil, err
	}
	cmd.WaitDelay, cmd.Dir = time.Second, record.CachePath
	env := slices.DeleteFunc(os.Environ(), func(value string) bool { return strings.HasPrefix(value, "HOME=") })
	if record.Home != "" {
		home = record.Home
	}
	cmd.Env = append(env, "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("npm %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func uninstallNativePlugin(ctx context.Context, home string, record domain.NativePluginRecord, binding string) error {
	args := []string{"remove", binding, "--json"}
	if record.Harness == domain.HarnessClaudeCode {
		entry, err := claudeInstalledEntry(record, binding)
		if err != nil || entry == nil {
			return err
		}
		args = []string{"uninstall", binding, "--json", "--scope", claudeScope(record), "--keep-data"}
	}
	_, err := runNativePlugin(ctx, home, record, args...)
	return err
}

func claudeInstalledEntries(record domain.NativePluginRecord, binding string) ([]any, error) {
	plugins, err := claudeInstalledPlugins(record)
	if err != nil {
		return nil, err
	}
	if _, exists := plugins[binding]; !exists {
		return nil, nil
	}
	entries, ok := plugins[binding].([]any)
	if !ok {
		return nil, fmt.Errorf("native Claude installation state for %s must be an array", binding)
	}
	return entries, nil
}

func claudeInstalledPlugins(record domain.NativePluginRecord) (map[string]any, error) {
	root, err := readNativeConfig(filepath.Join(record.ConfigHome, "plugins/installed_plugins.json"))
	if err != nil {
		return nil, err
	}
	if _, exists := root["plugins"]; !exists {
		return nil, nil
	}
	plugins, ok := root["plugins"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("native Claude installation state plugins must be an object")
	}
	return plugins, nil
}

func claudeMarketplaceInUse(record domain.NativePluginRecord, market string) (bool, error) {
	plugins, err := claudeInstalledPlugins(record)
	if err != nil {
		return false, err
	}
	for binding, raw := range plugins {
		if !strings.HasSuffix(binding, "@"+market) {
			continue
		}
		entries, ok := raw.([]any)
		if !ok {
			return false, fmt.Errorf("native Claude installation state for %s must be an array", binding)
		}
		if len(entries) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func claudeInstalledEntry(record domain.NativePluginRecord, binding string) (map[string]any, error) {
	entries, err := claudeInstalledEntries(record, binding)
	if err != nil {
		return nil, err
	}
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("native Claude installation entry for %s must be an object", binding)
		}
		project, _ := entry["projectPath"].(string)
		if entry["scope"] == claudeScope(record) && (claudeScope(record) == "user" || canonicalPath(project) == canonicalPath(filepath.Dir(filepath.Dir(record.SettingsPath)))) {
			return entry, nil
		}
	}
	return nil, nil
}

func nativePluginInstalled(record domain.NativePluginRecord, binding string) (bool, error) {
	if record.Harness == domain.HarnessClaudeCode {
		entry, err := claudeInstalledEntry(record, binding)
		return entry != nil, err
	}
	_, err := os.Stat(record.CachePath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, marketplace, ok := strings.Cut(binding, "@")
	if !ok {
		return false, fmt.Errorf("invalid native plugin binding %q", binding)
	}
	root, err := readNativeConfig(nativeUserConfig(record))
	if err != nil {
		return false, err
	}
	markets, _ := root["marketplaces"].(map[string]any)
	entry, _ := markets[marketplace].(map[string]any)
	source, _ := entry["source"].(string)
	return entry["source_type"] == "local" && canonicalPath(source) == canonicalPath(record.MarketplaceDir), nil
}

// Ask the host to evaluate its complete effective requirements and registration.
// A retained cache alone does not prove that the host admits its source.
func checkNativeCodexMarketplace(ctx context.Context, home string, action domain.NativePluginAction) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	record := domain.NativePluginRecord{Harness: domain.HarnessCodex, ConfigHome: action.ConfigHome}
	cmd, err := nativeHostCommand(ctx, home, record, "plugin", "marketplace", "list", "--json")
	if err != nil {
		return err
	}
	cmd.Dir = home
	if action.Scope == domain.ScopeProject {
		cmd.Dir = filepath.Dir(filepath.Dir(action.SettingsPath))
	}
	var diagnostics strings.Builder
	cmd.Stderr = &diagnostics
	body, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("checking native marketplace admission: %w: %s", err, strings.TrimSpace(diagnostics.String()))
	}
	var listing struct{ Marketplaces []struct{ Name, Root string } }
	if err := json.Unmarshal(body, &listing); err != nil {
		return fmt.Errorf("reading native marketplace admission: %w", err)
	}
	for _, market := range listing.Marketplaces {
		if market.Name == action.Package.Marketplace && canonicalPath(market.Root) == canonicalPath(action.MarketplaceDir) {
			return nil
		}
	}
	return fmt.Errorf("native Codex does not admit local marketplace %q at %s; check its registration and native marketplace diagnostics", action.Package.Marketplace, action.MarketplaceDir)
}

func codexMarketplaceApproval(root, name string) string {
	return fmt.Sprintf("For restricted Codex hosts, ask your administrator to allow this exact local source in the effective requirements:\n[marketplaces.allowed_sources.%q]\nsource = \"local\"\npath = %q\nAn original repository or parent-directory allow rule does not authorize this generated marketplace.", "aipack-"+name, root)
}
