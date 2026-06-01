package domain

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

const mcpLedgerKeyMarker = "#mcp:"

// MCPLedgerKey returns the synthetic ledger key used for a rendered MCP server
// derived from a harness config file.
func MCPLedgerKey(harnessPath, name string) string {
	return filepath.Clean(harnessPath) + mcpLedgerKeyMarker + name
}

// IsMCPLedgerKey reports whether the ledger key tracks a logical MCP server.
func IsMCPLedgerKey(key string) bool {
	return strings.Contains(filepath.Clean(key), mcpLedgerKeyMarker)
}

// SplitMCPLedgerKey decomposes an MCP ledger key ("<configPath>#mcp:<name>")
// into its config path and server name. ok is false if key is not an MCP key.
func SplitMCPLedgerKey(key string) (configPath string, serverName string, ok bool) {
	key = filepath.Clean(key)
	idx := strings.LastIndex(key, mcpLedgerKeyMarker)
	if idx < 0 || idx+len(mcpLedgerKeyMarker) >= len(key) {
		return "", "", false
	}
	return filepath.Clean(key[:idx]), key[idx+len(mcpLedgerKeyMarker):], true
}

// MCPServerNamesForPath returns the set of aipack-managed MCP server names
// tracked in the ledger for the given harness config path. Callers use this to
// surgically remove only managed servers from a shared config file, preserving
// any servers the user added by hand.
func MCPServerNamesForPath(managed map[string]Entry, configPath string) map[string]struct{} {
	return MCPServerNamesByPath(managed).ForPath(configPath)
}

// MCPServerNameIndex maps harness config paths to the aipack-managed MCP
// server names tracked under each path.
type MCPServerNameIndex map[string]map[string]struct{}

// MCPServerNamesByPath indexes aipack-managed MCP server names by harness
// config path so callers that touch several owned files do not rescan the
// ledger for each path.
func MCPServerNamesByPath(managed map[string]Entry) MCPServerNameIndex {
	index := MCPServerNameIndex{}
	for key := range managed {
		p, name, ok := SplitMCPLedgerKey(key)
		if !ok {
			continue
		}
		if index[p] == nil {
			index[p] = map[string]struct{}{}
		}
		index[p][name] = struct{}{}
	}
	return index
}

// ForPath returns the managed MCP server names tracked for configPath.
func (index MCPServerNameIndex) ForPath(configPath string) map[string]struct{} {
	cleanPath := filepath.Clean(configPath)
	if names, ok := index[cleanPath]; ok {
		return names
	}
	return map[string]struct{}{}
}

// MCPInventoryBytes returns the canonical pack-side JSON representation for an
// MCP server, excluding runtime-only profile fields.
func MCPInventoryBytes(server MCPServer) ([]byte, error) {
	server.AllowedTools = nil
	server.AlwaysAllowedTools = nil
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
			Name:               server.Name,
			ConfigPath:         harnessPath,
			Content:            content,
			SourcePack:         server.SourcePack,
			Harness:            harness,
			Embedded:           embedded,
			AllowedTools:       append([]string{}, server.AllowedTools...),
			AlwaysAllowedTools: append([]string{}, server.AlwaysAllowedTools...),
		})
	}
	return entries, nil
}
