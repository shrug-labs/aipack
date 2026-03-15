package claudecode

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
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
			entry.Type = s.Transport
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
// Format: mcp__<servername>__<toolname>.
func RenderPermissions(servers []domain.MCPServer) []string {
	var perms []string
	for _, s := range servers {
		name := engine.NormalizeServerName(s.Name)
		for _, tool := range s.AllowedTools {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}
			perms = append(perms, mcpPermPrefix+name+"__"+tool)
		}
	}
	sort.Strings(perms)
	return perms
}

// RenderDenyPermissions generates Claude Code permissions.deny patterns.
func RenderDenyPermissions(servers []domain.MCPServer) []string {
	var perms []string
	for _, s := range servers {
		name := engine.NormalizeServerName(s.Name)
		for _, tool := range s.DisabledTools {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}
			perms = append(perms, mcpPermPrefix+name+"__"+tool)
		}
	}
	sort.Strings(perms)
	return perms
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
	root := map[string]any{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &root); err != nil {
			return nil, err
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
	var allow []any
	if baseAllow, ok := perms["allow"].([]any); ok {
		for _, v := range baseAllow {
			if s, ok := v.(string); !ok || !strings.HasPrefix(s, mcpPermPrefix) {
				allow = append(allow, v)
			}
		}
	}
	for _, p := range mcpAllow {
		allow = append(allow, p)
	}
	if allow == nil {
		allow = []any{}
	}
	perms["allow"] = allow

	// Build deny: base non-MCP entries + fresh MCP entries.
	var deny []any
	if baseDeny, ok := perms["deny"].([]any); ok {
		for _, v := range baseDeny {
			if s, ok := v.(string); !ok || !strings.HasPrefix(s, mcpPermPrefix) {
				deny = append(deny, v)
			}
		}
	}
	for _, p := range mcpDeny {
		deny = append(deny, p)
	}
	if len(deny) > 0 {
		perms["deny"] = deny
	} else {
		delete(perms, "deny")
	}

	root["permissions"] = perms

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// StripManagedPermissions removes mcp__* entries from permissions.allow and
// permissions.deny.
func StripManagedPermissions(rendered []byte) ([]byte, error) {
	var root settingsRoot
	if err := json.Unmarshal(rendered, &root); err != nil {
		return nil, err
	}

	if root.Permissions != nil {
		var keptAllow []string
		for _, p := range root.Permissions.Allow {
			if !strings.HasPrefix(p, mcpPermPrefix) {
				keptAllow = append(keptAllow, p)
			}
		}
		root.Permissions.Allow = keptAllow

		var keptDeny []string
		for _, p := range root.Permissions.Deny {
			if !strings.HasPrefix(p, mcpPermPrefix) {
				keptDeny = append(keptDeny, p)
			}
		}
		if len(keptDeny) > 0 {
			root.Permissions.Deny = keptDeny
		} else {
			root.Permissions.Deny = nil
		}
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
