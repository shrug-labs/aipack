package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// loadMCPInventoryDir loads all .json files from a directory into an MCP server map.
// Server names are normalized to lowercase.
func loadMCPInventoryDir(dir string) (map[string]domain.MCPServer, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".json" {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)

	out := map[string]domain.MCPServer{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var s domain.MCPServer
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		k := NormalizeServerName(s.Name)
		if k == "" {
			return nil, fmt.Errorf("missing server name in %s", p)
		}
		if _, ok := out[k]; ok {
			return nil, fmt.Errorf("duplicate server name %q (from %s)", k, p)
		}
		out[k] = s
	}
	return out, nil
}

// LoadMCPInventoryForPacks loads MCP server inventories from each pack's mcp directory,
// filtering to only servers referenced by the pack's MCP map.
func LoadMCPInventoryForPacks(packs []config.ResolvedPack) (map[string]domain.MCPServer, error) {
	inventory := map[string]domain.MCPServer{}
	for _, pack := range packs {
		if len(pack.MCP) == 0 {
			continue
		}
		inv, err := loadMCPInventoryDir(filepath.Join(pack.Root, "mcp"))
		if err != nil {
			return nil, err
		}
		for name, server := range inv {
			if _, ok := pack.MCP[name]; !ok {
				continue
			}
			if _, ok := inventory[name]; ok {
				return nil, fmt.Errorf("duplicate MCP server inventory for %s", name)
			}
			inventory[name] = server
		}
	}
	return inventory, nil
}
