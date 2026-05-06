package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
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

type mcpProbeRequest struct {
	key mcpProbeKey
	seq int
}

// activeMCPProbe tracks an in-flight streaming MCP probe. The rootModel
// holds at most one; readNextProbeEvent uses the channels until eventCh
// closes and the terminal mcpProbeResultMsg lands. cancel propagates ctx
// cancellation when the user dismisses the picker (esc) or refreshes (r).
type activeMCPProbe struct {
	key      mcpProbeKey
	seq      int
	eventCh  <-chan mcp.ProbeEvent
	resultCh <-chan mcpProbeStreamResult
	cancel   context.CancelFunc
}

func newMCPProbeKey(packRoot, serverName string) mcpProbeKey {
	return mcpProbeKey{packRoot: packRoot, server: serverName}
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
	ctx, ok := m.profilesScreen().CurrentMCPContext()
	if !ok {
		m.statusText = common.DimStyle.Render("no profile selected")
		return m, nil
	}
	key := newMCPProbeKey(ctx.PackRoot, ctx.Server)

	// Use cached probe result when re-opening from the bulk menu;
	// otherwise load the static pack inventory as the fallback.
	available, cached := m.profilesScreen().CachedMCPProbeTools(ctx.PackRoot, ctx.Server)
	if !cached {
		var err error
		available, err = loadMCPAvailableTools(ctx.PackRoot, ctx.Server)
		if err != nil {
			available = nil
		}
	}

	items := toolPickerItemsForServer(available, ctx.Packs, ctx.PackName, ctx.Server)

	p := newToolPicker(dialogMCPToolPicker,
		fmt.Sprintf("Tools for %s/%s:", ctx.PackName, ctx.Server),
		items).
		WithProfileToolTotal(ctx.ProfileEnabledExcluding, ctx.ProfileAvailableExcluding).
		WithServerDisabled(!ctx.ServerEnabled)
	if probedAt, ok := m.profilesScreen().CachedMCPProbedAt(ctx.PackRoot, ctx.Server); ok {
		p = p.WithProbedAt(probedAt)
	}

	maxVisible := pickerMaxVisible(m.height)
	if len(items) > maxVisible {
		p.VisibleH = maxVisible
	}

	if cached {
		m.mcpProbeActive = nil
		m.cancelInflightMCPProbe()
		m = m.withPicker(p)
		return m, nil
	}

	// First open — fire async probe. Show loading indicator while it runs.
	m.cancelInflightMCPProbe()
	m.mcpProbeSeq++
	m.mcpProbeActive = &mcpProbeRequest{key: key, seq: m.mcpProbeSeq}
	p.Loading = true
	m = m.withPicker(p)
	return m, tea.Batch(
		probeMCPServer(m.ctx, m.cfg.ConfigDir, ctx.PackRoot, ctx.Server, ctx.Params, key, m.mcpProbeSeq),
		p.Spinner.Tick,
	)
}

// cancelInflightMCPProbe cancels any active streaming probe context. The
// producer goroutine drains its small event buffer, sends its terminal
// result, closes eventCh, and exits — but the rootModel ignores those
// stale messages because mcpProbeStream is now nil (or about to be).
func (m *rootModel) cancelInflightMCPProbe() {
	if m.mcpProbeStream == nil {
		return
	}
	m.mcpProbeStream.cancel()
	m.mcpProbeStream = nil
}

// probeMCPServer starts an MCP server subprocess, performs the JSON-RPC
// handshake, and calls tools/list. Streams mcp.probe.* events through an
// in-memory channel for the picker's loading view; the terminal result
// arrives as mcpProbeResultMsg once eventCh closes. Inventory-read /
// inventory-parse / param-expand failures bypass streaming entirely —
// they don't run the probe — and surface as a synchronous result.
func probeMCPServer(ctx context.Context, configDir, packRoot, serverName string, params map[string]string, key mcpProbeKey, seq int) tea.Cmd {
	return func() tea.Msg {
		env, err := config.LoadDotEnv(config.DotEnvPath(configDir))
		if err != nil {
			return mcpProbeResultMsg{key: key, seq: seq, err: fmt.Errorf("load .env: %w", err)}
		}
		expanded, err := loadExpandedMCPServerForProbe(packRoot, serverName, params, env)
		if err != nil {
			return mcpProbeResultMsg{key: key, seq: seq, err: err}
		}
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		eventCh := make(chan mcp.ProbeEvent, 8)
		resultCh := make(chan mcpProbeStreamResult, 1)
		go func() {
			defer cancel()
			defer close(eventCh)
			defer func() {
				if r := recover(); r != nil {
					resultCh <- mcpProbeStreamResult{err: fmt.Errorf("panic: %v", r)}
				}
			}()
			result, err := mcp.Probe(probeCtx, expanded, nil, eventCh)
			if err != nil {
				resultCh <- mcpProbeStreamResult{err: err}
				return
			}
			names := result.ToolNames()
			slices.Sort(names)
			resultCh <- mcpProbeStreamResult{tools: names}
		}()
		return mcpProbeReadyMsg{
			key:      key,
			seq:      seq,
			eventCh:  eventCh,
			resultCh: resultCh,
			cancel:   cancel,
		}
	}
}

func loadExpandedMCPServerForProbe(packRoot, serverName string, params map[string]string, env map[string]string) (domain.MCPServer, error) {
	path := filepath.Join(packRoot, "mcp", serverName+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("read inventory: %w", err)
	}
	var srv domain.MCPServer
	if err := json.Unmarshal(b, &srv); err != nil {
		return domain.MCPServer{}, fmt.Errorf("parse inventory: %w", err)
	}
	srv.PackRoot = packRoot
	return engine.ExpandSingleMCPServerWithEnv(params, env, srv)
}

// readNextProbeEvent waits for the next event on eventCh; on close, drains
// resultCh and returns the terminal mcpProbeResultMsg. Re-issued by the
// rootModel after each mcpProbeProgressMsg so the stream drains at TUI pace.
func readNextProbeEvent(eventCh <-chan mcp.ProbeEvent, resultCh <-chan mcpProbeStreamResult, key mcpProbeKey, seq int) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-eventCh
		if ok {
			return mcpProbeProgressMsg{key: key, seq: seq, event: e}
		}
		res := <-resultCh
		return mcpProbeResultMsg{key: key, seq: seq, tools: res.tools, err: res.err}
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

// handleMCPProbeReady stores the streaming channels on the rootModel and
// kicks off the read loop plus the picker spinner tick.
func (m rootModel) handleMCPProbeReady(msg mcpProbeReadyMsg) (tea.Model, tea.Cmd) {
	if m.mcpProbeActive == nil || m.mcpProbeActive.seq != msg.seq || m.mcpProbeActive.key != msg.key {
		// Stale or cancelled before ready — drain the goroutine quietly.
		msg.cancel()
		return m, nil
	}
	m.mcpProbeStream = &activeMCPProbe{
		key:      msg.key,
		seq:      msg.seq,
		eventCh:  msg.eventCh,
		resultCh: msg.resultCh,
		cancel:   msg.cancel,
	}
	if m.picker != nil && m.picker.ID == dialogMCPToolPicker {
		// Reset phase so the loading view renders the generic "Probing..."
		// label until the first event lands. Pre-event default avoids a
		// flash of stale phase text from a previous probe.
		m.picker.Phase = ""
		m.picker.PhaseDetail = ""
	}
	cmds := []tea.Cmd{readNextProbeEvent(msg.eventCh, msg.resultCh, msg.key, msg.seq)}
	if m.picker != nil && m.picker.Loading {
		cmds = append(cmds, m.picker.Spinner.Tick)
	}
	return m, tea.Batch(cmds...)
}

// handleMCPProbeProgress updates the picker's phase label from the latest
// event and re-issues the read loop. Stale events (probe was superseded
// by a refresh) are dropped.
func (m rootModel) handleMCPProbeProgress(msg mcpProbeProgressMsg) (tea.Model, tea.Cmd) {
	if m.mcpProbeStream == nil || m.mcpProbeStream.seq != msg.seq || m.mcpProbeStream.key != msg.key {
		return m, nil
	}
	if m.picker != nil && m.picker.Loading && m.picker.ID == dialogMCPToolPicker {
		// Skip listing_tools when connected is already showing — they
		// fire back-to-back from the transport functions, and connected
		// carries the richer "Connected via <transport>. Listing tools..."
		// label that subsumes the bare listing_tools UX.
		if msg.event.Phase == mcp.ProbePhaseListingTools && m.picker.Phase == mcp.ProbePhaseConnected {
			// Keep current phase. Still re-issue the read loop below.
		} else {
			m.picker.Phase = msg.event.Phase
			m.picker.PhaseDetail = msg.event.Transport
		}
	}
	return m, readNextProbeEvent(m.mcpProbeStream.eventCh, m.mcpProbeStream.resultCh, m.mcpProbeStream.key, m.mcpProbeStream.seq)
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
	m.mcpProbeStream = nil

	if m.picker == nil || m.picker.ID != dialogMCPToolPicker {
		return m, nil
	}
	m.picker.Loading = false
	m.picker.Phase = ""
	m.picker.PhaseDetail = ""

	if msg.err != nil {
		m.picker.LoadingErr = msg.err.Error()
		return m, nil
	}

	profiles := m.profilesScreen().StoreMCPProbeTools(msg.key.packRoot, msg.key.server, msg.tools)
	profiles = profiles.RefreshCurrentMCPCounts()
	m = m.setProfilesScreen(profiles)
	if m.cfg.ConfigDir != "" {
		_ = app.SaveMCPProbeCache(m.cfg.ConfigDir, profiles.ProbeCacheForPersist())
	}

	ctx, ok := m.profilesScreen().CurrentMCPContext()
	if !ok {
		return m, nil
	}
	items := toolPickerItemsForServer(msg.tools, ctx.Packs, ctx.PackName, ctx.Server)

	m.picker.Items = items
	m.picker.Cursor = 0
	m.picker.ScrollOff = 0
	if probedAt, ok := m.profilesScreen().CachedMCPProbedAt(msg.key.packRoot, msg.key.server); ok {
		m.picker.ProbedAt = probedAt
	}

	maxVisible := pickerMaxVisible(m.height)
	if len(items) > maxVisible {
		m.picker.VisibleH = maxVisible
	} else {
		m.picker.VisibleH = 0
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
			Label: t,
			State: state,
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

	if !msg.Confirmed {
		return m, nil
	}
	ctx, ok := m.profilesScreen().CurrentMCPContext()
	if !ok {
		return m, nil
	}

	// Use probed available list if the probe succeeded; otherwise fall back
	// to the static pack inventory so the computed disabled_tools list
	// matches the tool set the user actually saw in the picker.
	available, ok := m.profilesScreen().CachedMCPProbeTools(ctx.PackRoot, ctx.Server)
	if !ok {
		var err error
		available, err = loadMCPAvailableTools(ctx.PackRoot, ctx.Server)
		if err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("tool picker save: %v", err))
			return m, nil
		}
	}
	profiles, saveCmd, err := m.profilesScreen().MutateCurrentMCPServer(func(srv config.MCPServerConfig) (config.MCPServerConfig, bool, error) {
		allowedOut, alwaysOut, disabledOut := pickerOutputForSave(
			msg.Ask, msg.Auto, available, srv,
		)
		srv.AllowedTools = allowedOut
		srv.AlwaysAllowedTools = alwaysOut
		srv.DisabledTools = disabledOut
		return srv, true, nil
	})
	if err != nil {
		m.statusText = common.ErrorStyle.Render(err.Error())
		return m, nil
	}
	m = m.setProfilesScreen(profiles)
	m.dirty = m.dirty || profiles.Dirty()

	if msg.Bulk {
		// Save state, then chain into the bulk action menu. Set the
		// return-to-tools flag so the bulk result handler re-opens the
		// picker afterward instead of returning to the tree.
		m.pendingReturnToTools = true
		next, nextCmd := m.openMCPBulkMenu()
		return next, tea.Batch(saveCmd, nextCmd)
	}
	return m, saveCmd
}

// openMCPBulkMenu opens the bulk action menu for the MCP server at the
// current tree cursor. Extracted so both the tree "." handler and the
// tri-state picker "." can share it.
func (m rootModel) openMCPBulkMenu() (tea.Model, tea.Cmd) {
	ctx, ok := m.profilesScreen().CurrentMCPContext()
	if !ok {
		return m, nil
	}
	toggleLabel := actMCPDisableServer
	if !ctx.ServerEnabled {
		toggleLabel = actMCPEnableServer
	}
	items := []string{actMCPEnableAll, actMCPAlwaysAllowAll, toggleLabel, actMCPReset}
	if _, ok := m.profilesScreen().CachedMCPProbeTools(ctx.PackRoot, ctx.Server); ok {
		items = append(items, actMCPSaveInventory)
	}
	d := newListSelectDialog(dialogMCPBulkAction,
		fmt.Sprintf("MCP tools for %s:", ctx.Server),
		items)
	m = m.withDialog(d)
	return m, nil
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
	if !msg.Confirmed {
		if m.pendingReturnToTools {
			m.pendingReturnToTools = false
			return m.openMCPToolPicker()
		}
		return m, nil
	}
	ctx, ok := m.profilesScreen().CurrentMCPContext()
	if !ok {
		return m, nil
	}
	probeKey := newMCPProbeKey(ctx.PackRoot, ctx.Server)

	// Save-inventory writes to the pack's mcp/<server>.json, not the profile,
	// so it bypasses the mutation helper entirely.
	if msg.Value == actMCPSaveInventory {
		tools, ok := m.profilesScreen().CachedMCPProbeTools(probeKey.packRoot, probeKey.server)
		if !ok {
			m.statusText = common.ErrorStyle.Render("save inventory needs the live tool list — wait for the probe to finish (or close and reopen the picker if the probe failed)")
			if m.pendingReturnToTools {
				m.pendingReturnToTools = false
				return m.openMCPToolPicker()
			}
			return m, nil
		}
		return m, saveMCPInventoryToPack(ctx.PackRoot, ctx.Server, tools)
	}

	probedTools, _ := m.profilesScreen().CachedMCPProbeTools(probeKey.packRoot, probeKey.server)
	profiles, saveCmd, err := m.profilesScreen().MutateCurrentMCPServer(func(srv config.MCPServerConfig) (config.MCPServerConfig, bool, error) {
		return applyMCPBulkAction(srv, msg.Value, probedTools)
	})
	if err != nil {
		m.statusText = common.ErrorStyle.Render(err.Error())
		if m.pendingReturnToTools {
			m.pendingReturnToTools = false
			return m.openMCPToolPicker()
		}
		return m, nil
	}
	if saveCmd == nil {
		return m, nil
	}
	m = m.setProfilesScreen(profiles)
	m.dirty = m.dirty || profiles.Dirty()

	if m.pendingReturnToTools {
		m.pendingReturnToTools = false
		next, nextCmd := m.openMCPToolPicker()
		return next, tea.Batch(saveCmd, nextCmd)
	}
	return m, saveCmd
}

// applyMCPBulkAction is the pure mutation layer for the picker's bulk menu.
// applied=false means "leave the profile alone" (unknown action); err!=nil
// means "surface a user-visible refusal" (always-allow-all with no probe).
// Save-inventory is NOT handled here — it writes to the pack, not the
// profile, and stays in the dispatch layer next to the IO.
func applyMCPBulkAction(srv config.MCPServerConfig, action string, probedTools []string) (next config.MCPServerConfig, applied bool, err error) {
	switch action {
	case actMCPEnableAll:
		srv.Enabled = config.BoolPtr(true)
		srv.AllowedTools = nil
		srv.AlwaysAllowedTools = nil
		srv.DisabledTools = nil
		return srv, true, nil
	case actMCPAlwaysAllowAll:
		if len(probedTools) == 0 {
			return srv, false, fmt.Errorf("always-allow-all needs the live tool list — wait for the probe to finish (or close and reopen the picker if the probe failed)")
		}
		srv.Enabled = config.BoolPtr(true)
		srv.AllowedTools = nil
		srv.AlwaysAllowedTools = engine.UnionToolLists(probedTools, nil)
		srv.DisabledTools = nil
		return srv, true, nil
	case actMCPDisableServer:
		srv.Enabled = config.BoolPtr(false)
		srv.AllowedTools = nil
		srv.AlwaysAllowedTools = nil
		srv.DisabledTools = nil
		return srv, true, nil
	case actMCPEnableServer:
		srv.Enabled = config.BoolPtr(true)
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

func findPackEntryByName(packs []config.PackEntry, name string) *config.PackEntry {
	for i := range packs {
		if packs[i].Name == name {
			return &packs[i]
		}
	}
	return nil
}
