package profiles

import (
	"maps"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

type MCPContext struct {
	ProfileName               string
	PackName                  string
	PackRoot                  string
	Server                    string
	Params                    map[string]string
	Packs                     []config.PackEntry
	ServerEnabled             bool
	ProfileEnabledExcluding   int
	ProfileAvailableExcluding int
}

func (m Model) CurrentMCPContext() (MCPContext, bool) {
	item := m.currentItem()
	if item == nil || item.tree == nil {
		return MCPContext{}, false
	}
	n := item.tree.cursorNode()
	if n == nil || n.kind != nodeItem || n.category != domain.CategoryMCP {
		return MCPContext{}, false
	}
	if n.packIdx < 0 || n.packIdx >= len(item.tree.packs) {
		return MCPContext{}, false
	}
	packInfo := item.tree.packs[n.packIdx]
	ctx := MCPContext{
		ProfileName:               item.name,
		PackName:                  packInfo.Name,
		PackRoot:                  packInfo.Root,
		Server:                    n.id,
		Params:                    cloneStringMap(item.cfg.Params),
		Packs:                     slices.Clone(item.cfg.Packs),
		ServerEnabled:             n.enabled,
		ProfileEnabledExcluding:   item.tree.mcpCounts.TotalEnabled,
		ProfileAvailableExcluding: item.tree.mcpCounts.TotalAvailable,
	}
	countKey := mcpCountsKey{PackIdx: n.packIdx, Server: n.id}
	if c, ok := item.tree.mcpCounts.ByServer[countKey]; ok && c.ContributesToTotal {
		ctx.ProfileEnabledExcluding -= c.Enabled
		ctx.ProfileAvailableExcluding -= c.Available
	}
	return ctx, true
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func (m Model) CachedMCPProbeTools(packRoot, server string) ([]string, bool) {
	if m.mcpProbeCache == nil {
		return nil, false
	}
	entry, ok := m.mcpProbeCache[mcpProbeKey{packRoot: packRoot, server: server}]
	if !ok {
		return nil, false
	}
	return slices.Clone(entry.tools), true
}

func (m Model) CachedMCPProbedAt(packRoot, server string) (time.Time, bool) {
	if m.mcpProbeCache == nil {
		return time.Time{}, false
	}
	entry, ok := m.mcpProbeCache[mcpProbeKey{packRoot: packRoot, server: server}]
	if !ok {
		return time.Time{}, false
	}
	return entry.probedAt, true
}

func (m Model) StoreMCPProbeTools(packRoot, server string, tools []string) Model {
	if m.mcpProbeCache == nil {
		m.mcpProbeCache = map[mcpProbeKey]mcpProbeEntry{}
	}
	m.mcpProbeCache[mcpProbeKey{packRoot: packRoot, server: server}] = mcpProbeEntry{
		tools:    slices.Clone(tools),
		probedAt: time.Now().UTC(),
	}
	return m
}

func (m Model) ProbeCacheForPersist() app.MCPProbeCache {
	out := make(app.MCPProbeCache, len(m.mcpProbeCache))
	for key, entry := range m.mcpProbeCache {
		out[app.MCPProbeKey{PackRoot: key.packRoot, Server: key.server}] = app.MCPProbeEntry{
			Tools:    slices.Clone(entry.tools),
			ProbedAt: entry.probedAt,
		}
	}
	return out
}

func (m Model) RefreshCurrentMCPCounts() Model {
	item := m.currentItem()
	if item == nil || item.tree == nil {
		return m
	}
	item.tree.mcpCounts = computeMCPCountsWithProbeCache(item.tree.packs, item.cfg, m.mcpProbeCache)
	item.tree.applyMCPCounts()
	return m
}

func (m Model) MutateCurrentMCPServer(mutator func(config.MCPServerConfig) (config.MCPServerConfig, bool, error)) (Model, tea.Cmd, error) {
	item := m.currentItem()
	if item == nil || item.tree == nil {
		return m, nil, nil
	}
	n := item.tree.cursorNode()
	if n == nil || n.kind != nodeItem || n.category != domain.CategoryMCP {
		return m, nil, nil
	}
	if n.packIdx < 0 || n.packIdx >= len(item.tree.packs) {
		return m, nil, nil
	}
	packInfo := item.tree.packs[n.packIdx]
	pe := findPackEntryByName(item.cfg.Packs, packInfo.Name)
	if pe == nil {
		return m, nil, nil
	}
	srv := pe.MCP[n.id]
	updated, applied, err := mutator(srv)
	if err != nil || !applied {
		return m, nil, err
	}
	if mcpServerConfigIsZero(updated) {
		delete(pe.MCP, n.id)
	} else {
		if pe.MCP == nil {
			pe.MCP = map[string]config.MCPServerConfig{}
		}
		pe.MCP[n.id] = updated
	}
	n.enabled = updated.Enabled == nil || *updated.Enabled
	m = m.RefreshCurrentMCPCounts()
	item.dirty = true
	if item.isActive {
		item.syncState = common.SyncStatusPending
	}
	m.dirty = true
	return m, Save(m.configDir, item.name, item.cfg), nil
}

func mcpServerConfigIsZero(c config.MCPServerConfig) bool {
	return c.Enabled == nil &&
		len(c.AllowedTools) == 0 &&
		len(c.AlwaysAllowedTools) == 0 &&
		len(c.DisabledTools) == 0
}
