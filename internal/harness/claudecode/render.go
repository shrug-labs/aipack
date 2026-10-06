package claudecode

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	harnesspkg "github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"
)

// mcpEntry is the Claude Code .mcp.json server format.
type mcpEntry struct {
	Type    string            `json:"type,omitempty"`    // omit for stdio (default)
	Command string            `json:"command,omitempty"` // stdio only
	Args    []string          `json:"args,omitempty"`    // stdio only
	URL     string            `json:"url,omitempty"`     // sse / streamable-http
	Headers map[string]string `json:"headers,omitempty"` // sse / streamable-http
	Env     map[string]string `json:"env,omitempty"`     // stdio only
}

// mcpRoot is the top-level .mcp.json structure expected by Claude Code.
type mcpRoot struct {
	MCPServers map[string]mcpEntry `json:"mcpServers"`
}

const mcpPermPrefix = "mcp__"

const claudeTransportHTTP = "http"

var claudeMCPTransport = harnesspkg.MCPTransportCodec{StreamableHTTP: claudeTransportHTTP}

// filterOutMCPPerms removes mcp__* entries from a JSON-unmarshalled
// permissions slice, preserving non-MCP entries. Accepts any; returns
// nil if the input is not []any.
func filterOutMCPPerms(v any) []any {
	perms, ok := v.([]any)
	if !ok {
		return nil
	}
	var kept []any
	for _, v := range perms {
		if s, ok := v.(string); ok && strings.HasPrefix(s, mcpPermPrefix) {
			continue
		}
		kept = append(kept, v)
	}
	return kept
}

// RenderMCPBytesFromTyped produces .mcp.json content from typed MCPServer structs.
// Globals are already expanded at profile resolution time.
func RenderMCPBytesFromTyped(servers []domain.MCPServer) ([]byte, []domain.Warning, error) {
	expanded, warnings := engine.ExpandMCPServers(servers)
	mcp := map[string]mcpEntry{}
	for _, s := range expanded {
		var entry mcpEntry
		if s.IsStdio() {
			if len(s.Command) == 0 {
				continue
			}
			entry.Command = s.Command[0]
			entry.Args = s.Command[1:]
			if len(s.Env) > 0 {
				entry.Env = s.Env
			}
		} else {
			entry.Type = claudeMCPTransport.ToNative(s.Transport)
			entry.URL = s.URL
			if len(s.Headers) > 0 {
				entry.Headers = s.Headers
			}
		}
		mcp[s.Name] = entry
	}
	out, err := json.MarshalIndent(mcpRoot{MCPServers: mcp}, "", "  ")
	if err != nil {
		return nil, warnings, err
	}
	return append(out, '\n'), warnings, nil
}

// RenderPermissions generates Claude Code permission.allow patterns.
// Format: mcp__<servername>__<toolname>. Claude Code has no visibility gate
// for MCP tools — permissions.allow is already auto-approve semantics — so
// AllowedTools and AlwaysAllowedTools are unioned into the same output.
func RenderPermissions(servers []domain.MCPServer) []string {
	return renderPermPatterns(servers, func(s domain.MCPServer) []string {
		return engine.UnionToolLists(s.AllowedTools, s.AlwaysAllowedTools)
	})
}

// RenderDenyPermissions generates Claude Code permissions.deny patterns.
func RenderDenyPermissions(servers []domain.MCPServer) []string {
	return renderPermPatterns(servers, func(s domain.MCPServer) []string { return s.DisabledTools })
}

// renderPermPatterns builds sorted mcp__<server>__<tool> patterns from the
// tools returned by the selector function for each server.
func renderPermPatterns(servers []domain.MCPServer, tools func(domain.MCPServer) []string) []string {
	var perms []string
	for _, s := range servers {
		name := MCPPermissionName(strings.TrimSpace(s.Name))
		for _, tool := range tools(s) {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}
			parts := strings.Split(tool, "*")
			for i := range parts {
				parts[i] = MCPPermissionName(parts[i])
			}
			perms = append(perms, mcpPermPrefix+name+"__"+strings.Join(parts, "*"))
		}
	}
	slices.Sort(perms)
	return perms
}

// MCPPermissionName matches Claude's case-sensitive MCP tool namespace.
func MCPPermissionName(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, name)
}

// settingsRoot is the structure of settings.local.json.
type settingsRoot struct {
	Permissions *settingsPermissions `json:"permissions,omitempty"`
}

type settingsPermissions struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny,omitempty"`
}

// RenderSettingsBytes renders managed settings.local.json content.
// If base is non-empty, it is parsed as a JSON template and MCP permissions
// are merged into it (base non-MCP permissions are preserved, MCP entries
// replaced with fresh values). Non-permission keys from base are preserved.
func RenderSettingsBytes(base []byte, servers []domain.MCPServer) ([]byte, error) {
	return RenderSettingsBytesWithHooks(base, servers, nil)
}

// RenderSettingsBytesWithHooks renders managed settings.local.json content,
// including optional native Claude hook groups.
func RenderSettingsBytesWithHooks(base []byte, servers []domain.MCPServer, hooks []domain.Hook) ([]byte, error) {
	renderedHooks, _, err := RenderHooks(hooks)
	if err != nil {
		return nil, err
	}
	return RenderSettingsBytesWithRenderedHooks(base, servers, renderedHooks)
}

func RenderSettingsBytesWithRenderedHooks(base []byte, servers []domain.MCPServer, renderedHooks map[string][]any) ([]byte, error) {
	root := map[string]any{}
	if len(base) > 0 {
		if err := util.UnmarshalJSON(base, &root); err != nil {
			return nil, err
		}
		if root == nil {
			return nil, fmt.Errorf("settings JSON must be an object")
		}
	}

	mcpAllow := RenderPermissions(servers)
	mcpDeny := RenderDenyPermissions(servers)

	// Get or create permissions object from base.
	perms, _ := root["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}

	// Build allow: base non-MCP entries + fresh MCP entries.
	allow := filterOutMCPPerms(perms["allow"])
	for _, p := range mcpAllow {
		allow = append(allow, p)
	}
	if allow == nil {
		allow = []any{}
	}
	perms["allow"] = allow

	// Build deny: base non-MCP entries + fresh MCP entries.
	deny := filterOutMCPPerms(perms["deny"])
	for _, p := range mcpDeny {
		deny = append(deny, p)
	}
	if len(deny) > 0 {
		perms["deny"] = deny
	} else {
		delete(perms, "deny")
	}

	root["permissions"] = perms

	if len(renderedHooks) > 0 {
		mergeClaudeHooks(root, renderedHooks)
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func mergeClaudeHooks(root map[string]any, rendered map[string][]any) {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for event, groups := range rendered {
		existing, _ := hooks[event].([]any)
		// Dedup managed groups against anything already present in the base
		// hooks (often the pack's BaseSettings). Without this, a pack that
		// declares the same hook in both configs/harness_settings/.../settings.local.json
		// and a HOOK.yaml would render duplicate entries and Claude Code would
		// fire the hook twice.
		seen := map[string]struct{}{}
		for _, g := range existing {
			if key, ok := canonicalHookGroupKey(g); ok {
				seen[key] = struct{}{}
			}
		}
		merged := existing
		for _, g := range groups {
			if key, ok := canonicalHookGroupKey(g); ok {
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
			}
			merged = append(merged, g)
		}
		hooks[event] = merged
	}
	root["hooks"] = hooks
}

// InjectEnabledPlugins merges enabledPlugins entries into already-rendered
// settings JSON, preserving every other key. Used at global scope, where Claude
// Code exposes a single user settings file (~/.claude/settings.json): managed
// keys (hooks, permissions) and enabledPlugins must share it rather than collide
// as two same-destination merge actions.
func InjectEnabledPlugins(settings []byte, plugins []string) ([]byte, error) {
	if len(plugins) == 0 {
		return settings, nil
	}
	root := map[string]any{}
	if len(settings) > 0 {
		if err := util.UnmarshalJSON(settings, &root); err != nil {
			return nil, err
		}
		if root == nil {
			return nil, fmt.Errorf("settings JSON must be an object")
		}
	}
	enabled, _ := root["enabledPlugins"].(map[string]any)
	if enabled == nil {
		enabled = map[string]any{}
	}
	for _, binding := range plugins {
		enabled[binding] = true
	}
	root["enabledPlugins"] = enabled
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
