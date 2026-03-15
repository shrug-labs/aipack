package engine

import (
	"fmt"
	"sort"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// buildMCPServers assembles typed MCPServer structs from inventory data and
// per-pack permissions. Params ({params.*}) and env refs ({env:VAR}) in command,
// URL, env, and headers are expanded. Servers with unresolvable refs are skipped.
func buildMCPServers(params map[string]string, packs []config.ResolvedPack, inventory map[string]domain.MCPServer) ([]domain.MCPServer, []domain.Warning) {
	servers := enabledServers(packs)
	out := make([]domain.MCPServer, 0, len(servers))
	var warnings []domain.Warning

	for _, name := range servers {
		inv, ok := inventory[name]
		if !ok {
			warnings = append(warnings, domain.Warning{
				Field:   "mcp." + name,
				Message: fmt.Sprintf("MCP server %q not found in any pack inventory — skipping", name),
			})
			continue
		}

		var expandDetail string
		warnFn := func(msg string) { expandDetail = msg }
		expanded, err := expandMCPServer(params, inv, warnFn)
		if err != nil {
			warnings = append(warnings, domain.Warning{
				Field:   "mcp." + name,
				Message: "skipping MCP server: " + err.Error(),
			})
			continue
		}
		if expanded.Skip {
			msg := fmt.Sprintf("skipping MCP server %q: unresolved references", name)
			if expandDetail != "" {
				msg = expandDetail
			}
			warnings = append(warnings, domain.Warning{
				Field:   "mcp." + name,
				Message: msg,
			})
			continue
		}

		entry, sourcePack := mcpPermissionsAndSource(packs, name)

		out = append(out, domain.MCPServer{
			Name:           name,
			Transport:      inv.Transport,
			Timeout:        inv.Timeout,
			Command:        expanded.Command,
			URL:            expanded.URL,
			Env:            expanded.Env,
			Headers:        expanded.Headers,
			AvailableTools: inv.AvailableTools,
			AllowedTools:   sortedCopy(entry.AllowedTools),
			DisabledTools:  sortedCopy(entry.DisabledTools),
			SourcePack:     sourcePack,
			Links:          inv.Links,
			Auth:           inv.Auth,
			Notes:          inv.Notes,
		})
	}
	return out, warnings
}

// enabledServers returns a sorted list of enabled MCP server names from per-pack maps.
func enabledServers(packs []config.ResolvedPack) []string {
	seen := map[string]struct{}{}
	for _, p := range packs {
		for name := range p.MCP {
			seen[name] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// mcpPermissionsAndSource finds the effective permissions and source pack for a server name.
// Last pack declaring the server wins (matches override resolution order).
func mcpPermissionsAndSource(packs []config.ResolvedPack, name string) (config.ResolvedMCPServer, string) {
	var entry config.ResolvedMCPServer
	var sourcePack string
	for _, p := range packs {
		if e, ok := p.MCP[name]; ok {
			entry = e
			sourcePack = p.Name
		}
	}
	return entry, sourcePack
}

// prefixTool creates a prefixed tool name (server_tool).
func prefixTool(server string, tool string) string {
	server = NormalizeServerName(server)
	if server == "" || tool == "" {
		return ""
	}
	prefix := server + "_"
	if len(tool) > len(prefix) && tool[:len(prefix)] == prefix {
		return tool
	}
	return prefix + tool
}

// PrefixToolList creates a sorted list of prefixed tool names.
func PrefixToolList(server string, tools []string) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		p := prefixTool(server, t)
		if p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}
