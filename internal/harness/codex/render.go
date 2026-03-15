package codex

import (
	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

type codexMCPServer struct {
	Enabled           bool              `toml:"enabled"`
	Type              string            `toml:"type,omitempty"` // omit for stdio (default)
	EnabledTools      []string          `toml:"enabled_tools,omitempty"`
	DisabledTools     []string          `toml:"disabled_tools,omitempty"`
	StartupTimeoutSec int               `toml:"startup_timeout_sec"`
	Command           string            `toml:"command,omitempty"` // stdio only
	Args              []string          `toml:"args,omitempty"`    // stdio only
	Env               map[string]string `toml:"env,omitempty"`     // stdio only
	URL               string            `toml:"url,omitempty"`     // sse / streamable-http
	Headers           map[string]string `toml:"headers,omitempty"` // sse / streamable-http
}

// buildMCPEntries renders MCP servers to Codex TOML format.
// Codex uses unprefixed tool names (it applies its own prefix internally).
func buildMCPEntries(servers []domain.MCPServer) (map[string]codexMCPServer, []domain.Warning) {
	expanded, warnings := engine.ExpandMCPServers(servers)

	mcp := map[string]codexMCPServer{}
	for _, s := range expanded {
		timeout := s.Timeout
		if timeout == 0 {
			timeout = 10
		}
		entry := codexMCPServer{
			Enabled:           true,
			EnabledTools:      append([]string{}, s.AllowedTools...),
			DisabledTools:     append([]string{}, s.DisabledTools...),
			StartupTimeoutSec: timeout,
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

// RenderBytes produces the full config.toml content.
func RenderBytes(base []byte, servers []domain.MCPServer) ([]byte, []domain.Warning, error) {
	root := map[string]any{}
	if len(base) > 0 {
		if err := toml.Unmarshal(base, &root); err != nil {
			return nil, nil, err
		}
	}

	entries, warnings := buildMCPEntries(servers)
	root["mcp_servers"] = entries
	out, err := toml.Marshal(root)
	if err != nil {
		return nil, warnings, err
	}
	return append(out, '\n'), warnings, nil
}

// RenderMCPOnly produces a TOML document containing ONLY the mcp_servers table.
func RenderMCPOnly(servers []domain.MCPServer) ([]byte, []domain.Warning, error) {
	return RenderBytes(nil, servers)
}

// StripManagedKeys removes sync-managed keys from rendered settings.
func StripManagedKeys(rendered []byte) ([]byte, error) {
	root := map[string]any{}
	if err := toml.Unmarshal(rendered, &root); err != nil {
		return nil, err
	}
	delete(root, "mcp_servers")
	out, err := toml.Marshal(root)
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
