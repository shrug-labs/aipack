package claudecode

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"
)

// Harness implements the v2 harness.Harness interface for Claude Code.
type Harness struct{}

func (Harness) ID() domain.Harness { return domain.HarnessClaudeCode }

func (Harness) PackRelativePaths() []string {
	return []string{"claudecode/settings.local.json"}
}

func (Harness) SettingsPaths(scope domain.Scope, baseDir, _ string) []string {
	if scope == domain.ScopeProject {
		return []string{SettingsProjectPath(baseDir)}
	}
	return []string{SettingsGlobalPath(baseDir)}
}

func (Harness) ManagedRoots(scope domain.Scope, baseDir, _ string) []string {
	if scope == domain.ScopeProject {
		return ManagedRootsProject(baseDir)
	}
	return ManagedRootsGlobal(baseDir)
}

func (Harness) StrictExtraDirs(_ domain.Scope, _, _ string) []string { return nil }

// Plan produces a Fragment from typed content. Handles both project and global scope.
func (Harness) Plan(ctx engine.SyncContext) (domain.Fragment, error) {
	var f domain.Fragment

	if err := planContent(&f, ctx.TargetDir, ctx.Profile); err != nil {
		return domain.Fragment{}, err
	}
	if err := planMCPAndSettings(&f, ctx); err != nil {
		return domain.Fragment{}, err
	}

	return f, nil
}

func planContent(f *domain.Fragment, baseDir string, p domain.Profile) error {
	base := filepath.Join(baseDir, ".claude")
	return harness.PlanStandardContent(f, p, harness.ContentDirs{
		Rules:     filepath.Join(base, "rules"),
		Agents:    filepath.Join(base, "agents"),
		Workflows: filepath.Join(base, "commands"),
		Skills:    filepath.Join(base, "skills"),
	}, func(a domain.Agent) (domain.Agent, error) {
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

	// Resolve paths based on scope.
	mcpPath := MCPProjectPath(ctx.TargetDir)
	settingsPath := SettingsProjectPath(ctx.TargetDir)
	if ctx.Scope == domain.ScopeGlobal {
		mcpPath = MCPGlobalPath(ctx.TargetDir)
		settingsPath = SettingsGlobalPath(ctx.TargetDir)
	}

	if len(ctx.Profile.MCPServers) > 0 {
		mcpBytes, _, err := RenderMCPBytesFromTyped(ctx.Profile.MCPServers)
		if err != nil {
			return fmt.Errorf("render MCP bytes: %w", err)
		}
		planned := map[string]domain.MCPServer{}
		parseMCPJSON(planned, mcpBytes)
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
	hasMCP := len(ctx.Profile.MCPServers) > 0
	hasManagedContent := hasMCP || len(base) > 0
	decision := engine.ClassifySettings(hasMCP, hasManagedContent, ctx.SkipSettings)
	if decision.EmitSettings || decision.EmitMCP {
		out, err := RenderSettingsBytes(base, ctx.Profile.MCPServers)
		if err != nil {
			return fmt.Errorf("render settings bytes: %w", err)
		}
		action := domain.SettingsAction{
			Dst:        settingsPath,
			Desired:    out,
			Harness:    domain.HarnessClaudeCode,
			Label:      "settings.local.json",
			SourcePack: sp,
			MergeMode:  true,
		}
		if decision.EmitSettings {
			f.Settings = append(f.Settings, action)
		} else {
			action.Label = "settings.local.json (managed keys)"
			f.MCP = append(f.MCP, action)
		}
		f.Desired = append(f.Desired, filepath.Clean(settingsPath))
	}
	return nil
}

// Render produces a Fragment for pack rendering.
func (Harness) Render(ctx harness.RenderContext) (domain.Fragment, error) {
	base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessClaudeCode, "settings.local.json")
	out, err := RenderSettingsBytes(base, ctx.Profile.MCPServers)
	if err != nil {
		return domain.Fragment{}, err
	}
	p := filepath.Join(ctx.OutDir, "claudecode", "settings.local.json")
	return domain.Fragment{
		Writes:  []domain.WriteAction{{Dst: p, Content: out}},
		Desired: []string{p},
	}, nil
}

// StripManagedSettings removes mcp__* entries from permissions.
func (Harness) StripManagedSettings(rendered []byte, _ string) ([]byte, error) {
	return StripManagedPermissions(rendered)
}

// Capture extracts Claude Code content for round-trip save.
func (Harness) Capture(ctx harness.CaptureContext) (harness.CaptureResult, error) {
	res := harness.NewCaptureResult()

	var baseDir, mcpPath, settingsPath string
	if ctx.Scope == domain.ScopeProject {
		baseDir = ctx.ProjectDir
		mcpPath = MCPProjectPath(baseDir)
		settingsPath = SettingsProjectPath(baseDir)
	} else {
		baseDir = ctx.Home
		mcpPath = MCPGlobalPath(ctx.Home)
		settingsPath = SettingsGlobalPath(ctx.Home)
	}

	captureContent(&res, baseDir)

	if err := captureMCPAndSettings(&res, mcpPath, settingsPath); err != nil {
		return res, err
	}

	return res, nil
}

// CleanActions returns operations to reset Claude Code managed state.
func (Harness) CleanActions(scope domain.Scope, baseDir, home string) []harness.CleanAction {
	base := filepath.Join(baseDir, ".claude")
	actions := []harness.CleanAction{
		{Path: filepath.Join(base, "rules")},
		{Path: filepath.Join(base, "agents")},
		{Path: filepath.Join(base, "commands")},
		{Path: filepath.Join(base, "skills")},
	}
	if scope == domain.ScopeProject {
		actions = append(actions,
			harness.CleanAction{
				Path:   MCPProjectPath(baseDir),
				Format: harness.CleanJSON,
				Edit:   func(root map[string]any) { root["mcpServers"] = map[string]any{} },
			},
			harness.CleanAction{
				Path:   SettingsProjectPath(baseDir),
				Format: harness.CleanJSON,
				Edit:   func(root map[string]any) { delete(root, "permissions") },
			},
		)
	} else if home != "" {
		actions = append(actions,
			harness.CleanAction{
				Path:   MCPGlobalPath(home),
				Format: harness.CleanJSON,
				Edit:   func(root map[string]any) { root["mcpServers"] = map[string]any{} },
			},
			harness.CleanAction{
				Path:   SettingsGlobalPath(home),
				Format: harness.CleanJSON,
				Edit:   func(root map[string]any) { delete(root, "permissions") },
			},
		)
	}
	return actions
}

// captureContent captures rules, agents, commands, and skills from baseDir/.claude/.
func captureContent(res *harness.CaptureResult, baseDir string) {
	base := filepath.Join(baseDir, ".claude")
	harness.CaptureContent(res, harness.ContentDirs{
		Rules:     filepath.Join(base, "rules"),
		Agents:    filepath.Join(base, "agents"),
		Workflows: filepath.Join(base, "commands"),
		Skills:    filepath.Join(base, "skills"),
	}, func(raw []byte, _ string, src string) (domain.Agent, error) {
		return ReverseTransformAgent(raw, filepath.Base(src))
	})
}

// captureMCPAndSettings captures MCP servers and settings from the given paths.
func captureMCPAndSettings(res *harness.CaptureResult, mcpPath, settingsPath string) error {
	if b, ok, err := util.ReadFileIfExists(mcpPath); err != nil {
		return fmt.Errorf("capture claudecode MCP config: %w", err)
	} else if ok {
		res.Warnings = append(res.Warnings, parseMCPJSON(res.MCPServers, b)...)
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

func parseMCPJSON(servers map[string]domain.MCPServer, b []byte) []domain.Warning {
	var warnings []domain.Warning

	// Claude Code .mcp.json wraps servers in {"mcpServers": {...}}.
	// Unwrap the envelope; fall back to flat format for tolerance.
	var envelope struct {
		MCPServers json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return []domain.Warning{{Message: fmt.Sprintf("failed to parse .mcp.json: %v", err)}}
	}
	serverBytes := b
	if envelope.MCPServers != nil {
		serverBytes = envelope.MCPServers
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(serverBytes, &raw); err != nil {
		return []domain.Warning{{Message: fmt.Sprintf("failed to parse .mcp.json servers: %v", err)}}
	}
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
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
		transport := entry.Type
		if transport == "" {
			transport = domain.TransportStdio
		}
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
	for _, perm := range root.Permissions.Allow {
		serverName, toolName, ok := parseMCPPermission(perm)
		if !ok {
			continue
		}
		allowed[serverName] = append(allowed[serverName], toolName)
	}
	for _, name := range sortedPermissionServerKeys(allowed) {
		sort.Strings(allowed[name])
	}
	for _, perm := range root.Permissions.Deny {
		serverName, toolName, ok := parseMCPPermission(perm)
		if !ok {
			continue
		}
		srv := servers[serverName]
		srv.Name = serverName
		srv.DisabledTools = append(srv.DisabledTools, toolName)
		sort.Strings(srv.DisabledTools)
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
	serverName := engine.NormalizeServerName(parts[0])
	toolName := strings.TrimSpace(parts[1])
	if serverName == "" || toolName == "" {
		return "", "", false
	}
	return serverName, toolName, true
}

func sortedPermissionServerKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
