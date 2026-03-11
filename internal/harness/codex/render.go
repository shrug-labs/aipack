package codex

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

type codexMCPServer struct {
	Enabled           bool              `toml:"enabled"`
	EnabledTools      []string          `toml:"enabled_tools"`
	DisabledTools     []string          `toml:"disabled_tools,omitempty"`
	StartupTimeoutSec int               `toml:"startup_timeout_sec"`
	Command           string            `toml:"command"`
	Args              []string          `toml:"args"`
	Env               map[string]string `toml:"env,omitempty"`
}

// buildMCPEntries renders MCP servers to Codex TOML format.
// Codex uses unprefixed tool names (it applies its own prefix internally).
func buildMCPEntries(servers []domain.MCPServer, resolveEnv bool) (map[string]codexMCPServer, []domain.Warning) {
	expanded, warnings := engine.ExpandMCPForRender(servers, resolveEnv, engine.EnvRefFormatShell)

	mcp := map[string]codexMCPServer{}
	for _, s := range expanded {
		if !s.IsStdio() {
			warnings = append(warnings, domain.Warning{
				Message: fmt.Sprintf("MCP server %q uses %s transport, which Codex does not support — skipping", s.Name, s.Transport),
			})
			continue
		}
		if len(s.Command) == 0 {
			continue
		}
		timeout := s.Timeout
		if timeout == 0 {
			timeout = 10
		}
		entry := codexMCPServer{
			Enabled:           true,
			EnabledTools:      append([]string{}, s.AllowedTools...),
			DisabledTools:     append([]string{}, s.DisabledTools...),
			StartupTimeoutSec: timeout,
			Command:           s.Command[0],
			Args:              s.Command[1:],
		}
		if len(s.Env) > 0 {
			entry.Env = s.Env
		}
		mcp[s.Name] = entry
	}
	return mcp, warnings
}

// RenderBytes produces the full config.toml content.
func RenderBytes(base []byte, servers []domain.MCPServer, resolveEnv bool) ([]byte, []domain.Warning, error) {
	root := map[string]any{}
	if len(base) > 0 {
		if err := toml.Unmarshal(base, &root); err != nil {
			return nil, nil, err
		}
	}

	entries, warnings := buildMCPEntries(servers, resolveEnv)
	root["mcp_servers"] = entries
	out, err := toml.Marshal(root)
	if err != nil {
		return nil, warnings, err
	}
	return append(out, '\n'), warnings, nil
}

// RenderMCPOnly produces a TOML document containing ONLY the mcp_servers table.
func RenderMCPOnly(servers []domain.MCPServer, resolveEnv bool) ([]byte, []domain.Warning, error) {
	return RenderBytes(nil, servers, resolveEnv)
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
