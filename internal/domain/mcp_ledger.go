package domain

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// MCPLedgerKey returns the synthetic ledger key used for a rendered MCP server
// derived from a harness config file.
func MCPLedgerKey(harnessPath, name string) string {
	return filepath.Clean(harnessPath) + "#mcp:" + name
}

// IsMCPLedgerKey reports whether the ledger key tracks a logical MCP server.
func IsMCPLedgerKey(key string) bool {
	return strings.Contains(filepath.Clean(key), "#mcp:")
}

// MCPInventoryBytes returns the canonical pack-side JSON representation for an
// MCP server, excluding runtime-only profile fields.
func MCPInventoryBytes(server MCPServer) ([]byte, error) {
	server.AllowedTools = nil
	server.DisabledTools = nil
	server.SourcePack = ""
	b, err := json.MarshalIndent(server, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal MCP server %q: %w", server.Name, err)
	}
	return append(b, '\n'), nil
}

// MCPTrackedBytes returns the canonical JSON representation used for MCP sync
// tracking. It excludes pack-only metadata that harness configs do not
// round-trip, so a fresh sync can classify cleanly after capture.
func MCPTrackedBytes(server MCPServer) ([]byte, error) {
	if server.IsStdio() {
		server.Transport = TransportStdio
	}
	server.AvailableTools = nil
	server.Links = nil
	server.Auth = ""
	server.Notes = ""
	return MCPInventoryBytes(server)
}

// BuildMCPActions materializes first-class MCP server actions for a harness
// config path.
func BuildMCPActions(harnessPath string, harness Harness, servers []MCPServer, embedded bool) ([]MCPAction, error) {
	entries := make([]MCPAction, 0, len(servers))
	for _, server := range servers {
		content, err := MCPTrackedBytes(server)
		if err != nil {
			return nil, err
		}
		entries = append(entries, MCPAction{
			Name:         server.Name,
			ConfigPath:   harnessPath,
			Content:      content,
			SourcePack:   server.SourcePack,
			Harness:      harness,
			Embedded:     embedded,
			AllowedTools: append([]string{}, server.AllowedTools...),
		})
	}
	return entries, nil
}
