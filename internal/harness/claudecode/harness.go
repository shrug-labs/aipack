package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/plugin"
	"github.com/shrug-labs/aipack/internal/util"
)

// Harness implements the v2 harness.Harness interface for Claude Code.
type Harness struct{}

func (Harness) ID() domain.Harness { return domain.HarnessClaudeCode }

// Layout describes Claude Code's filesystem footprint for a given scope.
func (Harness) Layout(ctx harness.CaptureContext) harness.Layout {
	scope, baseDir, home := ctx.Scope, ctx.TargetBaseDir(), ctx.Home
	configTarget := ctx.TargetConfigDir
	paths := PathsForScope(scope, configTarget)
	mcpPath := filepath.Join(baseDir, paths.MCPFile)
	settingsPath := filepath.Join(baseDir, paths.SettingsFile)
	pluginSettingsPath := filepath.Join(baseDir, ".claude", "settings.json")
	knownMarketplacesPath := filepath.Join(home, ".claude", "plugins", "known_marketplaces.json")
	if ctx.NativeConfigDir != "" {
		knownMarketplacesPath = filepath.Join(ctx.NativeConfigDir, "plugins", "known_marketplaces.json")
	}
	if configTarget {
		pluginSettingsPath = settingsPath
		knownMarketplacesPath = filepath.Join(baseDir, "plugins", "known_marketplaces.json")
	}
	pruneMCPServers := func(root map[string]any, ctx harness.EditContext) {
		harness.PruneMapKeys(root, "mcpServers", ctx.ManagedMCPServers)
	}

	ownedFiles := []harness.OwnedFile{
		{
			Path: mcpPath, Format: harness.FormatJSON,
			Strip: pruneMCPServers,
			Reset: pruneMCPServers,
		},
		{
			Path: settingsPath, Format: harness.FormatJSON,
			Strip: func(root map[string]any, ctx harness.EditContext) {
				stripManagedPermissions(root)
				stripManagedHooks(root, ctx)
				stripNativePluginState(root, settingsPath, ctx)
			},
			Reset: func(root map[string]any, ctx harness.EditContext) {
				delete(root, "permissions")
				stripManagedHooks(root, ctx)
				stripNativePluginState(root, settingsPath, ctx)
			},
		},
	}
	// At global scope the managed settings file and the plugin settings file are
	// both ~/.claude/settings.json. Registering a second OwnedFile for the same
	// path would clobber the managed strip/reset in sync's path-keyed map (last
	// entry wins), so only add the plugin OwnedFile when the paths differ. The
	// managed strip/reset already handle native plugin state, so the converged
	// single entry is correct.
	if pluginSettingsPath != settingsPath {
		ownedFiles = append(ownedFiles, harness.OwnedFile{
			Path: pluginSettingsPath, Format: harness.FormatJSON,
			Strip: func(root map[string]any, ctx harness.EditContext) {
				stripNativePluginState(root, pluginSettingsPath, ctx)
			},
			Reset: func(root map[string]any, ctx harness.EditContext) {
				stripNativePluginState(root, pluginSettingsPath, ctx)
			},
		})
	}
	ownedFiles = append(ownedFiles, harness.OwnedFile{
		Path: knownMarketplacesPath, Format: harness.FormatJSON,
		Strip: func(root map[string]any, ctx harness.EditContext) {
			stripNativePluginState(root, knownMarketplacesPath, ctx)
		},
		Reset: func(root map[string]any, ctx harness.EditContext) {
			stripNativePluginState(root, knownMarketplacesPath, ctx)
		},
	})

	l := harness.Layout{
		ValidationRoots: []string{
			filepath.Join(baseDir, paths.RulesDir),
			filepath.Join(baseDir, paths.AgentsDir),
			filepath.Join(baseDir, paths.WorkflowsDir),
			filepath.Join(baseDir, paths.SkillsDir),
			mcpPath,
			settingsPath,
			pluginSettingsPath,
			knownMarketplacesPath,
		},
		RemovePaths: []string{
			filepath.Join(baseDir, paths.RulesDir),
			filepath.Join(baseDir, paths.AgentsDir),
			filepath.Join(baseDir, paths.WorkflowsDir),
			filepath.Join(baseDir, paths.SkillsDir),
		},
		OwnedFiles: ownedFiles,
	}
	return l
}

func stripNativePluginState(root map[string]any, configPath string, ctx harness.EditContext) {
	bindings, markets := map[string]struct{}{}, map[string]struct{}{}
	for binding, record := range ctx.NativePlugins {
		if record.Harness != domain.HarnessClaudeCode {
			continue
		}
		if filepath.Clean(record.SettingsPath) == filepath.Clean(configPath) {
			bindings[binding] = struct{}{}
		}
		if filepath.Clean(filepath.Join(record.ConfigHome, "settings.json")) == filepath.Clean(configPath) || filepath.Clean(filepath.Join(record.ConfigHome, "plugins/known_marketplaces.json")) == filepath.Clean(configPath) {
			if parts := strings.Split(binding, "@"); len(parts) == 2 {
				markets[parts[1]] = struct{}{}
			}
		}
	}
	harness.PruneMapKeys(root, "enabledPlugins", bindings)
	if ctx.PreserveNativeMarketplaces {
		return
	}
	if filepath.Base(configPath) == "known_marketplaces.json" {
		for market := range markets {
			delete(root, market)
		}
	} else {
		harness.PruneMapKeys(root, "extraKnownMarketplaces", markets)
	}
}

// stripManagedPermissions removes mcp__* entries from permissions.allow and
// permissions.deny, preserving non-MCP permission entries.
func stripManagedPermissions(root map[string]any) {
	perms, ok := root["permissions"].(map[string]any)
	if !ok {
		return
	}
	allow := filterOutMCPPerms(perms["allow"])
	if allow == nil {
		allow = []any{}
	}
	perms["allow"] = allow
	if kept := filterOutMCPPerms(perms["deny"]); len(kept) > 0 {
		perms["deny"] = kept
	} else {
		delete(perms, "deny")
	}
}

// Plan produces a Fragment from typed content. Handles both project and global scope.
func (Harness) Plan(_ context.Context, ctx engine.SyncContext) (domain.Fragment, error) {
	var f domain.Fragment

	if err := planContent(&f, ctx.TargetDir, PathsForScope(ctx.Scope, ctx.TargetConfigDir), ctx.Profile, ctx.Namespaced); err != nil {
		return domain.Fragment{}, err
	}
	if err := planMCPAndSettings(&f, ctx); err != nil {
		return domain.Fragment{}, err
	}

	return f, nil
}

func planContent(f *domain.Fragment, baseDir string, paths Paths, p domain.Profile, namespaced bool) error {
	return harness.PlanStandardContent(f, p, harness.ContentDirs{
		Rules:     filepath.Join(baseDir, paths.RulesDir),
		Agents:    filepath.Join(baseDir, paths.AgentsDir),
		Workflows: filepath.Join(baseDir, paths.WorkflowsDir),
		Skills:    filepath.Join(baseDir, paths.SkillsDir),
	}, namespaced, func(a domain.Agent) (domain.Agent, error) {
		transformed, err := TransformAgent(a)
		if err != nil {
			return domain.Agent{}, fmt.Errorf("transform agent %s: %w", a.Name, err)
		}
		a.Raw = transformed
		return a, nil
	})
}

func planMCPAndSettings(f *domain.Fragment, ctx engine.SyncContext) error {
	sp := ctx.Profile.SettingsPackName(domain.HarnessClaudeCode)

	paths := PathsForScope(ctx.Scope, ctx.TargetConfigDir)
	mcpPath := filepath.Join(ctx.TargetDir, paths.MCPFile)
	settingsPath := filepath.Join(ctx.TargetDir, paths.SettingsFile)
	home := ctx.Home
	if home == "" {
		home = ctx.TargetDir
	}
	configHome := filepath.Join(home, ".claude")
	if ctx.NativeConfigDir != "" {
		configHome = ctx.NativeConfigDir
	}
	if ctx.Scope == domain.ScopeGlobal && ctx.TargetConfigDir {
		configHome = ctx.TargetDir
	}
	var nativePlugins []string
	permissionServers := slices.Clone(ctx.Profile.MCPServers)
	serverOwners := map[string]string{}
	for _, server := range permissionServers {
		name := MCPPermissionName(server.Name)
		if owner, exists := serverOwners[name]; exists {
			return fmt.Errorf("claude MCP namespace %q collides between packs %q and %q", name, owner, server.SourcePack)
		}
		serverOwners[name] = server.SourcePack
	}
	for _, pack := range ctx.Profile.Packs {
		if pack.NativePlugin == nil {
			continue
		}
		namespace, err := plugin.ClaudeNamespace(*pack.NativePlugin)
		if err != nil {
			return fmt.Errorf("native plugin pack %q: %w", pack.Name, err)
		}
		var nativeServers []string
		for server, policy := range pack.NativePlugin.MCPPolicy {
			name := MCPPermissionName("plugin_" + namespace + "_" + server)
			if owner, exists := serverOwners[name]; exists {
				return fmt.Errorf("native Claude MCP namespace %q collides between packs %q and %q", name, owner, pack.Name)
			}
			serverOwners[name] = pack.Name
			nativeServers = append(nativeServers, name)
			permissionServers = append(permissionServers, domain.MCPServer{Name: name, AllowedTools: policy.AllowedTools,
				AlwaysAllowedTools: policy.AlwaysAllowedTools, DisabledTools: policy.DisabledTools, SourcePack: pack.Name})
		}
		slices.Sort(nativeServers)
		pkg := pack.NativePlugin.Package
		var files []domain.NativePluginFile
		if pkg.Harness == domain.HarnessCodex {
			files, pkg, err = plugin.RenderCodexForClaude(*pack.NativePlugin)
		} else {
			var entry map[string]any
			files, entry, err = plugin.RenderClaudePackage(*pack.NativePlugin)
			pkg.MarketplaceEntry = entry
		}
		if err != nil {
			return fmt.Errorf("native plugin pack %q: %w", pack.Name, err)
		}
		f.NativePlugins = append(f.NativePlugins, domain.NativePluginAction{
			Package: pkg, Selection: pack.NativePlugin, Namespace: namespace, Files: files, SourcePack: pack.Name, Scope: ctx.Scope, MCPPolicy: pack.NativePlugin.MCPPolicy,
			MCPPermissionServers: nativeServers,
			ConfigHome:           configHome, SettingsPath: settingsPath,
			MarketplaceDir: domain.NativeMarketplaceDir(ctx.ConfigDir, domain.HarnessClaudeCode, pkg.Marketplace, pkg.RootDirectoryName),
		})
		nativePlugins = append(nativePlugins, pack.NativePlugin.Package.Binding())
	}
	if err := plugin.RenderClaudeSharedRoots(f.NativePlugins); err != nil {
		return err
	}

	if len(ctx.Profile.MCPServers) > 0 {
		mcpBytes, _, err := RenderMCPBytesFromTyped(ctx.Profile.MCPServers)
		if err != nil {
			return fmt.Errorf("render MCP bytes: %w", err)
		}
		planned := map[string]domain.MCPServer{}
		parseMCPJSON(planned, mcpBytes, true)
		mcpActions, err := domain.BuildMCPActions(
			mcpPath,
			domain.HarnessClaudeCode,
			harness.PlannedMCPServers(ctx.Profile.MCPServers, planned),
			false,
		)
		if err != nil {
			return fmt.Errorf("build MCP actions: %w", err)
		}
		mcpLabel := ".mcp.json"
		if ctx.Scope == domain.ScopeGlobal {
			mcpLabel = ".claude.json"
		}
		f.MCP = append(f.MCP, domain.SettingsAction{
			Dst:        mcpPath,
			Desired:    mcpBytes,
			Harness:    domain.HarnessClaudeCode,
			Label:      mcpLabel,
			SourcePack: sp,
			MergeMode:  true,
		})
		f.MCPServers = append(f.MCPServers, mcpActions...)
		f.Desired = append(f.Desired, filepath.Clean(mcpPath))
	}

	base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessClaudeCode, "settings.local.json")
	hasMCP := len(permissionServers) > 0
	hooks := ctx.Profile.AllHooks()
	renderedHooks, hookSourcePack, err := RenderHooks(hooks)
	if err != nil {
		return fmt.Errorf("render Claude hooks: %w", err)
	}
	hasHooks := len(renderedHooks) > 0
	if sp == "" && hasHooks {
		sp = hookSourcePack
	}
	hookTraceRefs := domain.TraceRefsForHooks(hooks)
	if sp == "" && len(nativePlugins) > 0 {
		sp = compositePluginSourcePack(f.NativePlugins)
	}
	hasManagedKeys := hasMCP || hasHooks || len(nativePlugins) > 0
	decision := engine.ClassifySettings(hasManagedKeys, len(base) > 0, ctx.SkipSettings)

	settingsLabel := filepath.Base(paths.SettingsFile)

	if decision.EmitSettings {
		out, err := RenderSettingsBytesWithRenderedHooks(base, permissionServers, renderedHooks)
		if err != nil {
			return fmt.Errorf("render settings bytes: %w", err)
		}
		if len(nativePlugins) > 0 {
			if out, err = InjectEnabledPlugins(out, nativePlugins); err != nil {
				return fmt.Errorf("inject enabledPlugins: %w", err)
			}
		}
		f.Settings = append(f.Settings, domain.SettingsAction{
			Dst: settingsPath, Desired: out, Harness: domain.HarnessClaudeCode,
			Label: settingsLabel, SourcePack: sp, MergeMode: true, TraceRefs: hookTraceRefs,
		})
		f.Desired = append(f.Desired, filepath.Clean(settingsPath))
	} else if decision.EmitMCP {
		// Skip base template, render only managed keys (MCP permissions).
		out, err := RenderSettingsBytesWithRenderedHooks(nil, permissionServers, renderedHooks)
		if err != nil {
			return fmt.Errorf("render managed settings bytes: %w", err)
		}
		if len(nativePlugins) > 0 {
			if out, err = InjectEnabledPlugins(out, nativePlugins); err != nil {
				return fmt.Errorf("inject enabledPlugins: %w", err)
			}
		}
		f.MCP = append(f.MCP, domain.SettingsAction{
			Dst: settingsPath, Desired: out, Harness: domain.HarnessClaudeCode,
			Label: settingsLabel + " (managed keys)", SourcePack: sp, MergeMode: true, TraceRefs: hookTraceRefs,
		})
		f.Desired = append(f.Desired, filepath.Clean(settingsPath))
	}
	return nil
}

func compositePluginSourcePack(plugins []domain.NativePluginAction) string {
	if len(plugins) == 0 {
		return ""
	}
	first := plugins[0].SourcePack
	for _, p := range plugins[1:] {
		if p.SourcePack != first {
			return "(composite)"
		}
	}
	return first
}

// Render produces a Fragment for pack rendering.
func (Harness) Render(_ context.Context, ctx harness.RenderContext) (domain.Fragment, error) {
	base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessClaudeCode, "settings.local.json")
	out, err := RenderSettingsBytesWithHooks(base, ctx.Profile.MCPServers, ctx.Profile.AllHooks())
	if err != nil {
		return domain.Fragment{}, err
	}
	p := filepath.Join(ctx.OutDir, "claudecode", "settings.local.json")
	return domain.Fragment{
		Writes:  []domain.WriteAction{{Dst: p, Content: out}},
		Desired: []string{p},
	}, nil
}

// Capture extracts Claude Code content for round-trip save.
func (Harness) Capture(_ context.Context, ctx harness.CaptureContext) (harness.CaptureResult, error) {
	res := harness.NewCaptureResult()

	paths := PathsForScope(ctx.Scope, ctx.TargetConfigDir)
	baseDir := ctx.TargetBaseDir()
	mcpPath := filepath.Join(baseDir, paths.MCPFile)
	settingsPath := filepath.Join(baseDir, paths.SettingsFile)

	captureContent(&res, baseDir, paths, ctx.KnownPacks)

	if err := captureMCPAndSettings(&res, mcpPath, settingsPath); err != nil {
		return res, err
	}

	return res, nil
}

// captureContent captures rules, agents, commands, and skills from baseDir/.claude/.
func captureContent(res *harness.CaptureResult, baseDir string, paths Paths, knownPacks map[string]struct{}) {
	harness.CaptureContent(res, harness.ContentDirs{
		Rules:     filepath.Join(baseDir, paths.RulesDir),
		Agents:    filepath.Join(baseDir, paths.AgentsDir),
		Workflows: filepath.Join(baseDir, paths.WorkflowsDir),
		Skills:    filepath.Join(baseDir, paths.SkillsDir),
	}, knownPacks, func(raw []byte, _ string, src string) (domain.Agent, error) {
		return ReverseTransformAgent(raw, filepath.Base(src))
	})
}

// captureMCPAndSettings captures MCP servers and settings from the given paths.
func captureMCPAndSettings(res *harness.CaptureResult, mcpPath, settingsPath string) error {
	if b, ok, err := util.ReadFileIfExists(mcpPath); err != nil {
		return fmt.Errorf("capture claudecode MCP config: %w", err)
	} else if ok {
		res.Warnings = append(res.Warnings, parseMCPJSON(res.MCPServers, b, filepath.Base(mcpPath) != ".claude.json")...)
	}

	if b, ok, err := util.ReadFileIfExists(settingsPath); err != nil {
		return fmt.Errorf("capture claudecode settings: %w", err)
	} else if ok {
		res.Writes = append(res.Writes, domain.WriteAction{
			Dst: filepath.Join("configs", "claudecode", "settings.local.json"), Content: b, Src: settingsPath,
		})
		res.Warnings = append(res.Warnings, parseSettingsPermissions(res.MCPServers, res.AllowedTools, b)...)
	}
	res.MaterializeCapturedMCP(mcpPath)
	return nil
}

func parseMCPJSON(servers map[string]domain.MCPServer, b []byte, allowFlat bool) []domain.Warning {
	var warnings []domain.Warning

	// Claude Code .mcp.json wraps servers in {"mcpServers": {...}}.
	// Global .claude.json is native state: an absent envelope means no MCP
	// servers. Project MCP files also accept flat declarations for tolerance.
	var envelope struct {
		MCPServers json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return []domain.Warning{{Message: fmt.Sprintf("failed to parse .mcp.json: %v", err)}}
	}
	serverBytes := b
	if envelope.MCPServers != nil {
		serverBytes = envelope.MCPServers
	} else if !allowFlat {
		return nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(serverBytes, &raw); err != nil {
		return []domain.Warning{{Message: fmt.Sprintf("failed to parse .mcp.json servers: %v", err)}}
	}
	names := slices.Sorted(maps.Keys(raw))
	for _, name := range names {
		var entry struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Env     map[string]string `json:"env"`
		}
		if err := json.Unmarshal(raw[name], &entry); err != nil {
			warnings = append(warnings, domain.Warning{Field: "mcp." + name, Message: fmt.Sprintf("invalid JSON: %v", err)})
			continue
		}
		transport := claudeMCPTransport.ToCanonical(entry.Type)
		srv := domain.MCPServer{Name: name, Transport: transport}
		switch transport {
		case domain.TransportStdio:
			if entry.Command == "" {
				continue
			}
			srv.Command = append([]string{entry.Command}, entry.Args...)
			srv.Env = entry.Env
			if srv.Env == nil {
				srv.Env = map[string]string{}
			}
		case domain.TransportSSE, domain.TransportStreamableHTTP:
			if entry.URL == "" {
				continue
			}
			srv.URL = entry.URL
			srv.Headers = entry.Headers
		default:
			continue
		}
		servers[name] = srv
	}
	return warnings
}

func parseSettingsPermissions(servers map[string]domain.MCPServer, allowed map[string][]string, b []byte) []domain.Warning {
	var root settingsRoot
	if err := json.Unmarshal(b, &root); err != nil {
		return []domain.Warning{{Message: fmt.Sprintf("failed to parse Claude Code settings.local.json: %v", err)}}
	}
	if root.Permissions == nil {
		return nil
	}
	connectionNames := map[string]string{}
	for name := range servers {
		connectionNames[MCPPermissionName(name)] = name
	}
	for _, perm := range root.Permissions.Allow {
		serverName, toolName, ok := parseMCPPermission(perm)
		if !ok {
			continue
		}
		serverName, ok = connectionNames[serverName]
		if !ok {
			continue
		}
		allowed[serverName] = append(allowed[serverName], toolName)
	}
	for _, name := range slices.Sorted(maps.Keys(allowed)) {
		slices.Sort(allowed[name])
	}
	for _, perm := range root.Permissions.Deny {
		serverName, toolName, ok := parseMCPPermission(perm)
		if !ok {
			continue
		}
		serverName, ok = connectionNames[serverName]
		if !ok {
			continue
		}
		srv := servers[serverName]
		srv.Name = serverName
		srv.DisabledTools = append(srv.DisabledTools, toolName)
		slices.Sort(srv.DisabledTools)
		servers[serverName] = srv
	}
	return nil
}

func parseMCPPermission(perm string) (string, string, bool) {
	if !strings.HasPrefix(perm, mcpPermPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(perm, mcpPermPrefix)
	parts := strings.SplitN(rest, "__", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	serverName := strings.TrimSpace(parts[0])
	toolName := strings.TrimSpace(parts[1])
	if serverName == "" || toolName == "" {
		return "", "", false
	}
	return serverName, toolName, true
}
