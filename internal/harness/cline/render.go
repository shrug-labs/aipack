package cline

import (
	"encoding/json"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

type clineMCPServer struct {
	Disabled    bool              `json:"disabled"`
	Timeout     int               `json:"timeout"`
	Type        string            `json:"type"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	AlwaysAllow []string          `json:"alwaysAllow,omitempty"`
}

// RenderBytes produces the cline_mcp_settings.json content from typed MCPServers.
func RenderBytes(base []byte, servers []domain.MCPServer) ([]byte, []domain.Warning, error) {
	root := map[string]any{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &root); err != nil {
			return nil, nil, err
		}
	}

	expanded, warnings := engine.ExpandMCPServers(servers)

	mcp := map[string]clineMCPServer{}
	for _, s := range expanded {
		timeout := s.Timeout
		if timeout == 0 {
			timeout = 10 // default: 10 seconds
		}
		entry := clineMCPServer{
			Disabled: false,
			Timeout:  timeout,
			Type:     s.Transport,
		}
		if entry.Type == "" {
			entry.Type = domain.TransportStdio
		}
		if s.IsStdio() {
			if len(s.Command) == 0 {
				continue
			}
			entry.Command = s.Command[0]
			entry.Args = s.Command[1:]
			entry.Env = s.Env
		} else {
			entry.URL = s.URL
			if len(s.Headers) > 0 {
				entry.Headers = s.Headers
			}
		}
		if len(s.AllowedTools) > 0 {
			entry.AlwaysAllow = engine.PrefixToolList(s.Name, s.AllowedTools)
		}
		mcp[s.Name] = entry
	}

	merged := map[string]any{}
	if existing, ok := root["mcpServers"].(map[string]any); ok {
		for k, v := range existing {
			merged[k] = v
		}
	}
	for k, v := range mcp {
		merged[k] = v
	}
	root["mcpServers"] = merged

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, warnings, err
	}
	return append(out, '\n'), warnings, nil
}

// StripManagedKeys removes sync-managed keys from rendered settings.
func StripManagedKeys(rendered []byte) ([]byte, error) {
	root := map[string]any{}
	if err := json.Unmarshal(rendered, &root); err != nil {
		return nil, err
	}
	delete(root, "mcpServers")
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
