package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/mcp"
)

type mcpProbeKey struct {
	packRoot string
	server   string
}

// mcpProbeEntry carries the cached probe result plus the time it was taken
// so the picker header can render a relative-time freshness hint.
type mcpProbeEntry struct {
	tools    []string
	probedAt time.Time
}

type mcpProbeRequest struct {
	key mcpProbeKey
	seq int
}

func newMCPProbeKey(packRoot, serverName string) mcpProbeKey {
	return mcpProbeKey{packRoot: packRoot, server: serverName}
}

func (m profilesModel) cachedMCPProbeTools(key mcpProbeKey) ([]string, bool) {
	if m.mcpProbeCache == nil {
		return nil, false
	}
	entry, ok := m.mcpProbeCache[key]
	if !ok {
		return nil, false
	}
	return slices.Clone(entry.tools), true
}

func (m profilesModel) cachedMCPProbedAt(key mcpProbeKey) (time.Time, bool) {
	if m.mcpProbeCache == nil {
		return time.Time{}, false
	}
	entry, ok := m.mcpProbeCache[key]
	if !ok {
		return time.Time{}, false
	}
	return entry.probedAt, true
}

func (m *profilesModel) storeMCPProbeTools(key mcpProbeKey, tools []string) {
	if m.mcpProbeCache == nil {
		m.mcpProbeCache = map[mcpProbeKey]mcpProbeEntry{}
	}
	m.mcpProbeCache[key] = mcpProbeEntry{
		tools:    slices.Clone(tools),
		probedAt: time.Now().UTC(),
	}
}

// persistMCPProbeCache writes the in-memory cache to disk via the app layer.
// Errors are swallowed — the cache is an optimization and a missing disk
// write just means the next TUI session re-probes.
func persistMCPProbeCache(configDir string, cache map[mcpProbeKey]mcpProbeEntry) {
	if configDir == "" {
		return
	}
	out := make(app.MCPProbeCache, len(cache))
	for key, entry := range cache {
		out[app.MCPProbeKey{PackRoot: key.packRoot, Server: key.server}] = app.MCPProbeEntry{
			Tools:    entry.tools,
			ProbedAt: entry.probedAt,
		}
	}
	_ = app.SaveMCPProbeCache(configDir, out)
}

// openMCPToolPicker loads the available_tools inventory for the MCP server
// at the current tree cursor and opens a tri-state picker pre-populated with
// the profile's existing allowed_tools / always_allowed_tools state. While
// the picker loads, an async probe fires against the live server; the picker
// shows a loading indicator until the probe completes. On success the picker
// rebuilds its items from the live tool list; on failure it falls back to the
// static pack inventory. When re-opening from the bulk menu, the cached probe
// result is reused without re-probing.
func (m rootModel) openMCPToolPicker() (tea.Model, tea.Cmd) {
	item := m.profiles.currentItem()
	if item == nil || item.tree == nil {
		m.statusText = dimStyle.Render("no profile selected")
		return m, nil
	}
	n := item.tree.cursorNode()
	if n == nil || n.kind != nodeItem || n.category != domain.CategoryMCP {
		m.statusText = dimStyle.Render("tool picker only available on MCP items")
		return m, nil
	}
	if n.packIdx < 0 || n.packIdx >= len(item.tree.packs) {
		return m, nil
	}
	packInfo := item.tree.packs[n.packIdx]
	key := newMCPProbeKey(packInfo.Root, n.id)

	// Use cached probe result when re-opening from the bulk menu;
	// otherwise load the static pack inventory as the fallback.
	available, ok := m.profiles.cachedMCPProbeTools(key)
	if !ok {
		var err error
		available, err = loadMCPAvailableTools(packInfo.Root, n.id)
		if err != nil {
			available = nil
		}
	}

	items := toolPickerItemsForServer(available, item.cfg.Packs, packInfo.Name, n.id)

	// Subtract the server being edited from the profile total so the
	// picker footer can add the in-dialog enabled count back in live as
	// the user cycles tool states. If the server is currently disabled,
	// its tools don't contribute to the total at all — the subtraction
	// is a no-op in that case.
	countKey := mcpCountsKey{PackIdx: n.packIdx, Server: n.id}
	profileEnabledExcl := item.tree.mcpCounts.TotalEnabled
	profileAvailExcl := item.tree.mcpCounts.TotalAvailable
	if c, ok := item.tree.mcpCounts.ByServer[countKey]; ok && c.ContributesToTotal {
		profileEnabledExcl -= c.Enabled
		profileAvailExcl -= c.Available
	}

	p := newToolPicker(dialogMCPToolPicker,
		fmt.Sprintf("Tools for %s/%s:", packInfo.Name, n.id),
		items).
		withProfileToolTotal(profileEnabledExcl, profileAvailExcl).
		withServerDisabled(!n.enabled)
	if probedAt, ok := m.profiles.cachedMCPProbedAt(key); ok {
		p = p.withProbedAt(probedAt)
	}

	maxVisible := pickerMaxVisible(m.height)
	if len(items) > maxVisible {
		p.visibleH = maxVisible
	}

	if ok {
		m.mcpProbeActive = nil
		m.picker = &p
		return m, nil
	}

	// First open — fire async probe. Show loading indicator while it runs.
	m.mcpProbeSeq++
	m.mcpProbeActive = &mcpProbeRequest{key: key, seq: m.mcpProbeSeq}
	p.loading = true
	m.picker = &p
	return m, probeMCPServer(m.ctx, packInfo.Root, n.id, item.cfg.Params, key, m.mcpProbeSeq)
}

// probeMCPServer starts an MCP server subprocess, performs the JSON-RPC
// handshake, and calls tools/list. Returns the discovered tool names via
// mcpProbeResultMsg. Runs as a Bubbletea Cmd (goroutine).
func probeMCPServer(ctx context.Context, packRoot, serverName string, params map[string]string, key mcpProbeKey, seq int) tea.Cmd {
	return func() tea.Msg {
		path := filepath.Join(packRoot, "mcp", serverName+".json")
		b, err := os.ReadFile(path)
		if err != nil {
			return mcpProbeResultMsg{key: key, seq: seq, err: fmt.Errorf("read inventory: %w", err)}
		}
		var srv domain.MCPServer
		if err := json.Unmarshal(b, &srv); err != nil {
			return mcpProbeResultMsg{key: key, seq: seq, err: fmt.Errorf("parse inventory: %w", err)}
		}
		srv.PackRoot = packRoot
		expanded, err := engine.ExpandSingleMCPServer(params, srv)
		if err != nil {
			return mcpProbeResultMsg{key: key, seq: seq, err: err}
		}
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		result, err := mcp.Probe(probeCtx, expanded)
		if err != nil {
			return mcpProbeResultMsg{key: key, seq: seq, err: err}
		}
		names := result.ToolNames()
		slices.Sort(names)
		return mcpProbeResultMsg{key: key, seq: seq, tools: names}
	}
}

func saveMCPInventoryToPack(packRoot, serverName string, tools []string) tea.Cmd {
	return func() tea.Msg {
		path := filepath.Join(packRoot, "mcp", serverName+".json")
		return mcpInventorySavedMsg{
			key: newMCPProbeKey(packRoot, serverName),
			err: app.SaveMCPInventoryTools(path, tools),
		}
	}
}

// handleMCPProbeResult processes the async probe result while the tri-state
// picker dialog is open. On success the picker items are rebuilt from the
// live tool list; on failure the static fallback items (already populated)
// are shown with a warning note.
func (m rootModel) handleMCPProbeResult(msg mcpProbeResultMsg) (tea.Model, tea.Cmd) {
	if m.mcpProbeActive == nil || m.mcpProbeActive.seq != msg.seq || m.mcpProbeActive.key != msg.key {
		return m, nil
	}
	m.mcpProbeActive = nil

	if m.picker == nil || m.picker.id != dialogMCPToolPicker {
		return m, nil
	}
	m.picker.loading = false

	if msg.err != nil {
		m.picker.loadingErr = msg.err.Error()
		return m, nil
	}

	m.profiles.storeMCPProbeTools(msg.key, msg.tools)
	persistMCPProbeCache(m.cfg.ConfigDir, m.profiles.mcpProbeCache)
	refreshCurrentMCPCounts(&m)

	item := m.profiles.currentItem()
	if item == nil || item.tree == nil {
		return m, nil
	}
	n := item.tree.cursorNode()
	if n == nil || n.packIdx < 0 || n.packIdx >= len(item.tree.packs) {
		return m, nil
	}
	packInfo := item.tree.packs[n.packIdx]
	items := toolPickerItemsForServer(msg.tools, item.cfg.Packs, packInfo.Name, n.id)

	m.picker.items = items
	m.picker.cursor = 0
	m.picker.scrollOff = 0
	if probedAt, ok := m.profiles.cachedMCPProbedAt(msg.key); ok {
		m.picker.probedAt = probedAt
	}

	maxVisible := pickerMaxVisible(m.height)
	if len(items) > maxVisible {
		m.picker.visibleH = maxVisible
	} else {
		m.picker.visibleH = 0
	}
	return m, nil
}

// loadMCPAvailableTools reads a single MCP inventory JSON from the pack's
// mcp/ directory and returns its available_tools slice. Used by the TUI to
// populate the tool picker without triggering a full profile resolve.
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

// toolPickerItemsForServer builds picker items from a server's
// available_tools list. Tool states are seeded from the profile's
// allowed_tools / always_allowed_tools / disabled_tools for the given
// pack/server. When the profile is silent on all three fields the server
// is in the "grant all" state (harness native behavior) — every tool
// renders as triAsk so the user can see the effective set and make
// targeted changes. Any explicit entry flips the picker into "profile
// decides": only tools the profile names appear as ask/auto.
func toolPickerItemsForServer(available []string, packs []config.PackEntry, packName, serverName string) []toolPickerItem {
	var serverCfg config.MCPServerConfig
	if pe := findPackEntryByName(packs, packName); pe != nil {
		serverCfg = pe.MCP[serverName]
	}
	allowlistsAbsent := serverCfg.AllowedTools == nil && serverCfg.AlwaysAllowedTools == nil
	allowedSet := config.ToStringSet(serverCfg.AllowedTools)
	alwaysSet := config.ToStringSet(serverCfg.AlwaysAllowedTools)
	disabledSet := config.ToStringSet(serverCfg.DisabledTools)

	items := make([]toolPickerItem, 0, len(available))
	for _, t := range available {
		state := triOff
		switch {
		case disabledSet[t]:
			// Explicitly disabled — stays off, takes priority over allow lists.
		case alwaysSet[t]:
			state = triAuto
		case allowedSet[t]:
			state = triAsk
		case allowlistsAbsent:
			// When no allowlists are set, runtime behavior is "ask by default".
			// This includes disabled-only configs: non-disabled tools stay ask.
			state = triAsk
		}
		items = append(items, toolPickerItem{
			label: t,
			state: state,
		})
	}
	return items
}

// handleMCPToolPickerResult writes the picker result back to the current
// profile's MCP server config and schedules a save. Writes are literal:
// allowed_tools gets the full ask∪auto set and always_allowed_tools gets
// the auto set, both as explicit sorted lists. When the final selection
// matches the pack manifest's defaults (silent: all ask, no auto, no off),
// the entry is dropped from the profile so the resolver falls back to the
// manifest defaults on next sync and the profile YAML stays quiet — same
// "match defaults → omit" semantics as config.MCPToConfig on the tree-save
// path.
func (m rootModel) handleMCPToolPickerResult(msg toolPickerResultMsg) (tea.Model, tea.Cmd) {
	m.mcpProbeActive = nil

	if !msg.confirmed {
		return m, nil
	}
	item := m.profiles.currentItem()
	if item == nil || item.tree == nil {
		return m, nil
	}
	n := item.tree.cursorNode()
	if n == nil || n.kind != nodeItem || n.category != domain.CategoryMCP {
		return m, nil
	}
	if n.packIdx < 0 || n.packIdx >= len(item.tree.packs) {
		return m, nil
	}
	packInfo := item.tree.packs[n.packIdx]
	key := newMCPProbeKey(packInfo.Root, n.id)

	// Use probed available list if the probe succeeded; otherwise fall back
	// to the static pack inventory so the computed disabled_tools list
	// matches the tool set the user actually saw in the picker.
	available, ok := m.profiles.cachedMCPProbeTools(key)
	if !ok {
		var err error
		available, err = loadMCPAvailableTools(packInfo.Root, n.id)
		if err != nil {
			m.statusText = errorStyle.Render(fmt.Sprintf("tool picker save: %v", err))
			return m, nil
		}
	}
	pe := findPackEntry(&item.cfg, packInfo.Name)
	if pe == nil {
		return m, nil
	}
	srv := pe.MCP[n.id]
	allowedOut, alwaysOut, disabledOut := pickerOutputForSave(
		msg.ask, msg.auto, available, srv,
	)
	srv.AllowedTools = allowedOut
	srv.AlwaysAllowedTools = alwaysOut
	srv.DisabledTools = disabledOut
	if mcpServerConfigIsZero(srv) {
		// Match-defaults with nothing else set → drop the entry so the
		// profile YAML stays quiet and the resolver falls back to the
		// manifest defaults on next sync. If the server wasn't in the
		// map at all, this is a no-op.
		delete(pe.MCP, n.id)
	} else {
		if pe.MCP == nil {
			pe.MCP = map[string]config.MCPServerConfig{}
		}
		pe.MCP[n.id] = srv
	}

	markMCPDirty(&m)

	if msg.bulk {
		// Save state, then chain into the bulk action menu. Set the
		// return-to-tools flag so the bulk result handler re-opens the
		// picker afterward instead of returning to the tree.
		m.pendingReturnToTools = true
		next, nextCmd := m.openMCPBulkMenu()
		return next, tea.Batch(saveProfile(m.cfg.ConfigDir, item.name, item.cfg), nextCmd)
	}
	return m, saveProfile(m.cfg.ConfigDir, item.name, item.cfg)
}

// openMCPBulkMenu opens the bulk action menu for the MCP server at the
// current tree cursor. Extracted so both the tree "." handler and the
// tri-state picker "." can share it.
func (m rootModel) openMCPBulkMenu() (tea.Model, tea.Cmd) {
	item := m.profiles.currentItem()
	if item == nil || item.tree == nil {
		return m, nil
	}
	n := item.tree.cursorNode()
	if n == nil || n.kind != nodeItem || n.category != domain.CategoryMCP {
		return m, nil
	}
	toggleLabel := actMCPDisableServer
	if !n.enabled {
		toggleLabel = actMCPEnableServer
	}
	items := []string{actMCPEnableAll, actMCPAlwaysAllowAll, toggleLabel, actMCPReset}
	if _, ok := m.profiles.cachedMCPProbeTools(newMCPProbeKey(item.tree.packs[n.packIdx].Root, n.id)); ok {
		items = append(items, actMCPSaveInventory)
	}
	d := newListSelectDialog(dialogMCPBulkAction,
		fmt.Sprintf("MCP tools for %s:", n.id),
		items)
	m.dialog = &d
	return m, nil
}

// markMCPDirty refreshes MCP counts and marks the profile + root dirty.
func markMCPDirty(m *rootModel) {
	refreshCurrentMCPCounts(m)

	item := m.profiles.currentItem()
	if item == nil || item.tree == nil {
		return
	}
	m.profiles.dirty = true
	item.dirty = true
	if item.isActive {
		item.syncState = syncPending
	}
	m.dirty = true
}

func refreshCurrentMCPCounts(m *rootModel) {
	item := m.profiles.currentItem()
	if item == nil || item.tree == nil {
		return
	}
	item.tree.mcpCounts = computeMCPCountsWithProbeCache(item.tree.packs, item.cfg, m.profiles.mcpProbeCache)
	item.tree.applyMCPCounts()
}

// mcpServerConfigIsZero reports whether an MCPServerConfig has no
// meaningful content — all four fields are nil or empty. Zero-value
// entries should be removed from the profile's MCP map rather than
// serialized as empty blocks.
func mcpServerConfigIsZero(c config.MCPServerConfig) bool {
	return c.Enabled == nil &&
		len(c.AllowedTools) == 0 &&
		len(c.AlwaysAllowedTools) == 0 &&
		len(c.DisabledTools) == 0
}

// pickerOutputForSave converts the user's ask/auto selection into profile
// fields.
//
// Baseline behavior:
//   - all ask, no auto, no off → nil/nil/nil (silent)
//   - otherwise emit explicit allowed_tools (+ always_allowed_tools if set)
//
// Compatibility behavior:
//   - if the prior config was disabled_tools-only, preserve that encoding
//     (nil allowed + disabled list) when no auto tools are selected.
func pickerOutputForSave(ask, auto, available []string, existing config.MCPServerConfig) (allowed, always, disabled []string) {
	union := engine.UnionToolLists(ask, auto)
	unionSet := config.ToStringSet(union)

	var disabledList []string
	for _, t := range available {
		if !unionSet[t] {
			disabledList = append(disabledList, t)
		}
	}
	slices.Sort(disabledList)

	// Silent: every visible tool is ask, nothing is auto, nothing is off.
	// Equivalent to harness default — write nil lists so future tool-list
	// growth inherits the same behavior.
	if len(auto) == 0 && len(disabledList) == 0 {
		return nil, nil, nil
	}
	wasDisabledOnly := existing.AllowedTools == nil && existing.AlwaysAllowedTools == nil && len(existing.DisabledTools) > 0
	if wasDisabledOnly && len(auto) == 0 {
		return nil, nil, disabledList
	}

	allowed = union
	if len(auto) > 0 {
		always = engine.UnionToolLists(auto, nil)
	}
	return allowed, always, nil
}

// handleMCPBulkActionResult implements the bulk actions from the picker's
// in-dialog action menu:
//
//   - Enable all tools — clears every tool list and sets Enabled: true.
//     Silent config = "grant all" under the harness default (every probed
//     tool callable, prompt per call), so the profile YAML stays quiet.
//   - Always allow all tools — writes the cached probe result into
//     always_allowed_tools as an explicit sorted list. Refused if the
//     server has not been probed this session ("silent" means "ask per
//     call", not "auto-approve", so there is no silent encoding for this
//     action).
//   - Disable MCP server / Enable MCP server — flips Enabled. The label
//     swaps based on current state; the "Disable" variant also clears any
//     profile tool-list overrides because they're meaningless on a
//     disabled server.
//   - Reset to pack defaults — clears tool lists, preserves Enabled. The
//     resolver falls back to manifest defaults on next sync.
//
// After the write, the content tree is rebuilt so enablement state changes
// propagate to the [x] / [ ] marker in the tree view immediately — the
// picker mutates the profile config directly, bypassing the tree toggle
// path, so the tree would otherwise display stale state until next load.
// Entries that become zero-valued (no Enabled, no tool lists, no
// disabled_tools) are deleted from the profile map to keep the YAML quiet.
func (m rootModel) handleMCPBulkActionResult(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	if !msg.confirmed {
		if m.pendingReturnToTools {
			m.pendingReturnToTools = false
			return m.openMCPToolPicker()
		}
		return m, nil
	}
	item := m.profiles.currentItem()
	if item == nil || item.tree == nil {
		return m, nil
	}
	n := item.tree.cursorNode()
	if n == nil || n.kind != nodeItem || n.category != domain.CategoryMCP {
		return m, nil
	}
	if n.packIdx < 0 || n.packIdx >= len(item.tree.packs) {
		return m, nil
	}
	packInfo := item.tree.packs[n.packIdx]
	probeKey := newMCPProbeKey(packInfo.Root, n.id)

	pe := findPackEntry(&item.cfg, packInfo.Name)
	if pe == nil {
		return m, nil
	}
	srv := pe.MCP[n.id]

	// Save-inventory writes to the pack's mcp/<server>.json, not the profile,
	// so it bypasses the mutation helper entirely.
	if msg.value == actMCPSaveInventory {
		tools, ok := m.profiles.cachedMCPProbeTools(probeKey)
		if !ok {
			m.statusText = errorStyle.Render("save inventory needs the live tool list — wait for the probe to finish (or close and reopen the picker if the probe failed)")
			if m.pendingReturnToTools {
				m.pendingReturnToTools = false
				return m.openMCPToolPicker()
			}
			return m, nil
		}
		return m, saveMCPInventoryToPack(packInfo.Root, n.id, tools)
	}

	probedTools, _ := m.profiles.cachedMCPProbeTools(probeKey)
	updated, applied, err := applyMCPBulkAction(srv, msg.value, probedTools)
	if err != nil {
		m.statusText = errorStyle.Render(err.Error())
		if m.pendingReturnToTools {
			m.pendingReturnToTools = false
			return m.openMCPToolPicker()
		}
		return m, nil
	}
	if !applied {
		return m, nil
	}
	srv = updated
	if mcpServerConfigIsZero(srv) {
		delete(pe.MCP, n.id)
	} else {
		if pe.MCP == nil {
			pe.MCP = map[string]config.MCPServerConfig{}
		}
		pe.MCP[n.id] = srv
	}

	// Mirror the server's Enabled state onto the tree node so the
	// [x] / [ ] marker updates immediately without rebuilding the tree
	// (which would reset cursor position). A nil Enabled means "inherit
	// from pack manifest", which for MCP servers is treated as enabled by
	// the tree's initial build from ContentTree.
	n.enabled = srv.Enabled == nil || *srv.Enabled

	markMCPDirty(&m)

	if m.pendingReturnToTools {
		m.pendingReturnToTools = false
		next, nextCmd := m.openMCPToolPicker()
		return next, tea.Batch(saveProfile(m.cfg.ConfigDir, item.name, item.cfg), nextCmd)
	}
	return m, saveProfile(m.cfg.ConfigDir, item.name, item.cfg)
}

// applyMCPBulkAction is the pure mutation layer for the picker's bulk menu.
// applied=false means "leave the profile alone" (unknown action); err!=nil
// means "surface a user-visible refusal" (always-allow-all with no probe).
// Save-inventory is NOT handled here — it writes to the pack, not the
// profile, and stays in the dispatch layer next to the IO.
func applyMCPBulkAction(srv config.MCPServerConfig, action string, probedTools []string) (next config.MCPServerConfig, applied bool, err error) {
	switch action {
	case actMCPEnableAll:
		srv.Enabled = boolPtr(true)
		srv.AllowedTools = nil
		srv.AlwaysAllowedTools = nil
		srv.DisabledTools = nil
		return srv, true, nil
	case actMCPAlwaysAllowAll:
		if len(probedTools) == 0 {
			return srv, false, fmt.Errorf("always-allow-all needs the live tool list — wait for the probe to finish (or close and reopen the picker if the probe failed)")
		}
		srv.Enabled = boolPtr(true)
		srv.AllowedTools = nil
		srv.AlwaysAllowedTools = engine.UnionToolLists(probedTools, nil)
		srv.DisabledTools = nil
		return srv, true, nil
	case actMCPDisableServer:
		srv.Enabled = boolPtr(false)
		srv.AllowedTools = nil
		srv.AlwaysAllowedTools = nil
		srv.DisabledTools = nil
		return srv, true, nil
	case actMCPEnableServer:
		srv.Enabled = boolPtr(true)
		return srv, true, nil
	case actMCPReset:
		srv.AllowedTools = nil
		srv.AlwaysAllowedTools = nil
		srv.DisabledTools = nil
		return srv, true, nil
	}
	return srv, false, nil
}

// findPackEntry returns a pointer to the profile's pack entry by name, or
// nil if the pack is not registered in the profile.
func findPackEntry(cfg *config.ProfileConfig, name string) *config.PackEntry {
	return findPackEntryByName(cfg.Packs, name)
}

// ---------------------------------------------------------------------------
// MCP tool counts
// ---------------------------------------------------------------------------

type mcpCounts struct {
	Enabled            int
	Available          int
	ContributesToTotal bool
}

type mcpCountsKey struct {
	PackIdx int
	Server  string
}

type profileMCPCounts struct {
	ByServer       map[mcpCountsKey]mcpCounts
	TotalEnabled   int
	TotalAvailable int
}

// computeMCPCountsWithProbeCache walks the resolved profile packs and computes
// tool counts for every MCP server the profile exposes. When a probe cache is
// provided, counts prefer the probed tool list over the static inventory.
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
		if len(p.Manifest.MCP) == 0 {
			continue
		}
		pe := findPackEntryByName(cfg.Packs, p.Name)
		for _, serverName := range p.Manifest.MCP {
			available := loadMCPAvailableToolsWithProbeCache(p.Root, serverName, probeCache)

			enabled := countEnabledTools(pe, serverName, available)
			key := mcpCountsKey{PackIdx: pi, Server: serverName}
			ownerIdx, hasOwner := owners[serverName]
			contributes := hasOwner && ownerIdx == pi
			out.ByServer[key] = mcpCounts{
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
		if entry, ok := probeCache[newMCPProbeKey(packRoot, serverName)]; ok {
			return slices.Clone(entry.tools)
		}
	}
	available, err := loadMCPAvailableTools(packRoot, serverName)
	if err != nil {
		return nil
	}
	return available
}

// countEnabledTools returns the number of tools from `available` that will
// be callable for the given server under the profile's current state.
// When no allowlists are set, non-disabled tools are callable by default.
// Once allowed_tools or always_allowed_tools is set, only listed tools count.
func countEnabledTools(pe *config.PackEntry, serverName string, available []string) int {
	if !isServerEnabled(pe, serverName) {
		return 0
	}
	var serverCfg config.MCPServerConfig
	if pe != nil {
		serverCfg = pe.MCP[serverName]
	}
	allowlistsAbsent := serverCfg.AllowedTools == nil && serverCfg.AlwaysAllowedTools == nil
	allowedSet := config.ToStringSet(serverCfg.AllowedTools)
	alwaysSet := config.ToStringSet(serverCfg.AlwaysAllowedTools)
	disabledSet := config.ToStringSet(serverCfg.DisabledTools)

	n := 0
	for _, t := range available {
		if disabledSet[t] {
			continue
		}
		if allowlistsAbsent || allowedSet[t] || alwaysSet[t] {
			n++
		}
	}
	return n
}

func isServerEnabled(pe *config.PackEntry, serverName string) bool {
	if pe == nil {
		return true
	}
	srvCfg, ok := pe.MCP[serverName]
	if !ok {
		return true
	}
	if srvCfg.Enabled == nil {
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
