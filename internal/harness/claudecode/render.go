package claudecode

import (
	"encoding/json"
	"slices"
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
		name := engine.NormalizeServerName(s.Name)
		for _, tool := range tools(s) {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}
			perms = append(perms, mcpPermPrefix+name+"__"+tool)
		}
	}
	slices.Sort(perms)
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

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
