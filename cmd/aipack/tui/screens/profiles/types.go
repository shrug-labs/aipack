package profiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

type profileItem struct {
	name         string
	path         string
	isActive     bool
	syncState    common.SyncStatus
	syncTarget   common.SyncTarget
	syncErrText  string
	syncWarnings []domain.Warning
	bundledFrom  []string
	cfg          config.ProfileConfig
	tree         *treeModel
	treeErr      string
	dirty        bool
}

type mcpProbeKey struct {
	packRoot string
	server   string
}

type mcpProbeEntry struct {
	tools    []string
	probedAt time.Time
}

type mcpCounts struct {
	Available          int
	Enabled            int
	ContributesToTotal bool
}

type mcpCountsKey struct {
	PackIdx int
	Server  string
}

type profileMCPCounts struct {
	ByServer       map[mcpCountsKey]mcpCounts
	TotalAvailable int
	TotalEnabled   int
}

func computeMCPCountsWithProbeCache(packs []app.ProfilePackInfo, cfg config.ProfileConfig, probeCache map[mcpProbeKey]mcpProbeEntry) profileMCPCounts {
	out := profileMCPCounts{ByServer: map[mcpCountsKey]mcpCounts{}}
	owners := map[string]int{}
	for pi, p := range packs {
		pe := findPackEntryByName(cfg.Packs, p.Name)
		for _, serverName := range p.Manifest.MCP {
			if isServerEnabled(pe, serverName) {
				owners[serverName] = pi
			}
		}
	}
	for pi, p := range packs {
		pe := findPackEntryByName(cfg.Packs, p.Name)
		for _, serverName := range p.Manifest.MCP {
			available := loadMCPAvailableToolsWithProbeCache(p.Root, serverName, probeCache)
			enabled := countEnabledTools(pe, serverName, available)
			ownerIdx, hasOwner := owners[serverName]
			contributes := hasOwner && ownerIdx == pi
			out.ByServer[mcpCountsKey{PackIdx: pi, Server: serverName}] = mcpCounts{
				Enabled:            enabled,
				Available:          len(available),
				ContributesToTotal: contributes,
			}
			if contributes {
				out.TotalEnabled += enabled
				out.TotalAvailable += len(available)
			}
		}
	}
	return out
}

func loadMCPAvailableToolsWithProbeCache(packRoot, serverName string, probeCache map[mcpProbeKey]mcpProbeEntry) []string {
	if probeCache != nil {
		if entry, ok := probeCache[mcpProbeKey{packRoot: packRoot, server: serverName}]; ok {
			return slices.Clone(entry.tools)
		}
	}
	available, err := loadMCPAvailableTools(packRoot, serverName)
	if err != nil {
		return nil
	}
	return available
}

func loadMCPAvailableTools(packRoot, serverName string) ([]string, error) {
	path := filepath.Join(packRoot, "mcp", serverName+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var srv domain.MCPServer
	if err := json.Unmarshal(b, &srv); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := slices.Clone(srv.AvailableTools)
	slices.Sort(out)
	return out, nil
}

func countEnabledTools(pe *config.PackEntry, serverName string, available []string) int {
	if !isServerEnabled(pe, serverName) {
		return 0
	}
	var serverCfg config.MCPServerConfig
	if pe != nil {
		serverCfg = pe.MCP[serverName]
	}
	allowlistsAbsent := serverCfg.AllowedTools == nil && serverCfg.AlwaysAllowedTools == nil
	disabled := config.ToStringSet(serverCfg.DisabledTools)
	allowed := config.ToStringSet(serverCfg.AllowedTools)
	always := config.ToStringSet(serverCfg.AlwaysAllowedTools)
	count := 0
	for _, tool := range available {
		if disabled[tool] {
			continue
		}
		if allowlistsAbsent || allowed[tool] || always[tool] {
			count++
		}
	}
	return count
}

func isServerEnabled(pe *config.PackEntry, serverName string) bool {
	if pe == nil {
		return true
	}
	srvCfg, ok := pe.MCP[serverName]
	if !ok || srvCfg.Enabled == nil {
		return true
	}
	return *srvCfg.Enabled
}

func findPackEntryByName(packs []config.PackEntry, name string) *config.PackEntry {
	for i := range packs {
		if packs[i].Name == name {
			return &packs[i]
		}
	}
	return nil
}
