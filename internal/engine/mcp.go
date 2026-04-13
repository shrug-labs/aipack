package engine

import (
	"fmt"
	"maps"
	"slices"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
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
		defaultAllowed := mcpDefaultAllowedTools(packs, sourcePack, name)
		requiredRefs := extractRequiredRefs(inv)

		out = append(out, domain.MCPServer{
			Name:                name,
			Transport:           inv.Transport,
			Timeout:             inv.Timeout,
			Command:             expanded.Command,
			URL:                 expanded.URL,
			Env:                 expanded.Env,
			Headers:             expanded.Headers,
			AvailableTools:      inv.AvailableTools,
			AllowedTools:        sortedCopy(entry.AllowedTools),
			DisabledTools:       sortedCopy(entry.DisabledTools),
			SourcePack:          sourcePack,
			DefaultAllowedTools: sortedCopy(defaultAllowed),
			RequiredRefs:        requiredRefs,
			Links:               inv.Links,
			Auth:                inv.Auth,
			Notes:               inv.Notes,
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
	out := slices.Sorted(maps.Keys(seen))
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

// mcpDefaultAllowedTools returns the pack author's declared default
// allowed-tool list for a server, looked up on the source pack's manifest.
func mcpDefaultAllowedTools(packs []config.ResolvedPack, sourcePack, serverName string) []string {
	if sourcePack == "" {
		return nil
	}
	for _, p := range packs {
		if p.Name != sourcePack {
			continue
		}
		if def, ok := p.Manifest.MCP.Servers[serverName]; ok {
			return def.DefaultAllowedTools
		}
		return nil
	}
	return nil
}

// extractRequiredRefs walks the raw pre-expansion strings on an MCP server
// inventory entry and returns the deduped, sorted set of {params.*} and
// {env:*} references. Strings scanned: Command args, Env values, URL,
// and Header values. A ref with the same name in two different kinds
// (e.g. {params.X} and {env:X}) produces two distinct entries.
func extractRequiredRefs(inv domain.MCPServer) []domain.RequiredRef {
	seen := map[string]struct{}{}
	var refs []domain.RequiredRef
	addParam := func(name string) {
		key := "param:" + name
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		refs = append(refs, domain.RequiredRef{Kind: domain.RefKindParam, Name: name})
	}
	addEnv := func(name string) {
		key := "env:" + name
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		refs = append(refs, domain.RequiredRef{Kind: domain.RefKindEnv, Name: name})
	}

	walkString := func(s string) {
		_ = util.WalkParamRefs(s, func(ref util.ParamRef) error {
			if ref.Name != "" {
				addParam(ref.Name)
			}
			return nil
		})
		_ = util.WalkEnvRefs(s, func(ref util.EnvRef) error {
			if ref.Name != "" {
				addEnv(ref.Name)
			}
			return nil
		})
	}

	for _, s := range inv.Command {
		walkString(s)
	}
	for _, s := range inv.Env {
		walkString(s)
	}
	walkString(inv.URL)
	for _, s := range inv.Headers {
		walkString(s)
	}

	slices.SortFunc(refs, func(a, b domain.RequiredRef) int {
		if a.Kind != b.Kind {
			// Params before env, matching {params.*} reading order.
			if a.Kind == domain.RefKindParam {
				return -1
			}
			return 1
		}
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return refs
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
	slices.Sort(out)
	return out
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}
