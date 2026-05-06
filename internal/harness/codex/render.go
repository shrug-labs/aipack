package codex

import (
	"maps"

	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

// codexApprovalModeApprove is the Codex per-tool auto-approve value.
const codexApprovalModeApprove = "approve"
const defaultCodexPluginMarketplace = "openai-curated"

type codexMCPServer struct {
	Enabled           bool                       `toml:"enabled"`
	Type              string                     `toml:"type,omitempty"` // omit for stdio (default)
	EnabledTools      []string                   `toml:"enabled_tools,omitempty"`
	DisabledTools     []string                   `toml:"disabled_tools,omitempty"`
	StartupTimeoutSec int                        `toml:"startup_timeout_sec"`
	Command           string                     `toml:"command,omitempty"` // stdio only
	Args              []string                   `toml:"args,omitempty"`    // stdio only
	Env               map[string]string          `toml:"env,omitempty"`     // stdio only
	URL               string                     `toml:"url,omitempty"`     // sse / streamable-http
	Headers           map[string]string          `toml:"headers,omitempty"` // sse / streamable-http
	Tools             map[string]codexToolConfig `toml:"tools,omitempty"`   // per-tool overrides (approval_mode)
}

// codexToolConfig is the per-tool entry under [mcp_servers.<name>.tools.<tool>].
// Currently only approval_mode is populated; other per-tool fields may be
// added as Codex exposes them.
type codexToolConfig struct {
	ApprovalMode string `toml:"approval_mode,omitempty"`
}

// buildMCPEntries renders MCP servers to Codex TOML format.
// Codex uses unprefixed tool names (it applies its own prefix internally).
// AllowedTools and AlwaysAllowedTools are unioned into enabled_tools for
// visibility; tools in AlwaysAllowedTools additionally receive a per-tool
// [mcp_servers.<name>.tools.<tool>] approval_mode = "approve" stanza so
// Codex auto-approves them without prompting.
func buildMCPEntries(servers []domain.MCPServer) (map[string]codexMCPServer, []domain.Warning) {
	expanded, warnings := engine.ExpandMCPServers(servers)

	mcp := map[string]codexMCPServer{}
	for _, s := range expanded {
		timeout := s.Timeout
		if timeout == 0 {
			timeout = 10
		}
		enabledTools := engine.UnionToolLists(s.AllowedTools, s.AlwaysAllowedTools)
		entry := codexMCPServer{
			Enabled:           true,
			EnabledTools:      enabledTools,
			DisabledTools:     append([]string{}, s.DisabledTools...),
			StartupTimeoutSec: timeout,
		}
		if len(s.AlwaysAllowedTools) > 0 {
			tools := make(map[string]codexToolConfig, len(s.AlwaysAllowedTools))
			for _, t := range s.AlwaysAllowedTools {
				tools[t] = codexToolConfig{ApprovalMode: codexApprovalModeApprove}
			}
			entry.Tools = tools
		}
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
	return mcp, warnings
}

func buildPluginEntries(plugins []domain.Plugin) map[string]map[string]any {
	if len(plugins) == 0 {
		return nil
	}
	out := make(map[string]map[string]any, len(plugins))
	for _, p := range plugins {
		out[p.Binding(defaultCodexPluginMarketplace)] = map[string]any{"enabled": true}
	}
	return out
}

// RenderBytes produces the full config.toml content including MCP servers and
// agent registrations.
func RenderBytes(base []byte, servers []domain.MCPServer, agentRegs map[string]map[string]any) ([]byte, []domain.Warning, error) {
	return RenderBytesWithPlugins(base, servers, agentRegs, nil)
}

// RenderBytesWithPlugins produces the full config.toml content including MCP
// servers, agent registrations, and plugin enable stanzas.
func RenderBytesWithPlugins(base []byte, servers []domain.MCPServer, agentRegs map[string]map[string]any, plugins []domain.Plugin) ([]byte, []domain.Warning, error) {
	root := map[string]any{}
	if len(base) > 0 {
		if err := toml.Unmarshal(base, &root); err != nil {
			return nil, nil, err
		}
	}

	entries, warnings := buildMCPEntries(servers)
	root["mcp_servers"] = entries

	// Merge agent registrations into the [agents] table.
	if len(agentRegs) > 0 {
		agents := map[string]any{}
		// Preserve existing global agent settings (max_threads, max_depth, etc.).
		if existing, ok := root["agents"].(map[string]any); ok {
			maps.Copy(agents, existing)
		}
		for name, reg := range agentRegs {
			agents[name] = reg
		}
		root["agents"] = agents
	}

	if pluginEntries := buildPluginEntries(plugins); len(pluginEntries) > 0 {
		pluginRoot := map[string]any{}
		if existing, ok := root["plugins"].(map[string]any); ok {
			maps.Copy(pluginRoot, existing)
		}
		for name, reg := range pluginEntries {
			pluginRoot[name] = reg
		}
		root["plugins"] = pluginRoot
	}

	out, err := toml.Marshal(root)
	if err != nil {
		return nil, warnings, err
	}
	return append(out, '\n'), warnings, nil
}

// RenderManagedKeysOnly produces a TOML document containing only the
// sync-managed keys (mcp_servers + agent registrations), without base template.
func RenderManagedKeysOnly(servers []domain.MCPServer, agentRegs map[string]map[string]any) ([]byte, []domain.Warning, error) {
	return RenderBytes(nil, servers, agentRegs)
}

func RenderManagedKeysOnlyWithPlugins(servers []domain.MCPServer, agentRegs map[string]map[string]any, plugins []domain.Plugin) ([]byte, []domain.Warning, error) {
	return RenderBytesWithPlugins(nil, servers, agentRegs, plugins)
}
