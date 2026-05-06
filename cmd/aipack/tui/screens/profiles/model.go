package profiles

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

type panelFocus int

const (
	panelProfiles panelFocus = iota // top-left: profile list
	panelPacks                      // bottom-left: pack roster
	panelTree                       // right: content tree
)

type Model struct {
	ctx        context.Context
	eng        *engine.Engine
	items      []profileItem
	cursor     int
	focus      panelFocus
	packCursor int // cursor within the pack list (left panel)
	configDir  string
	dirty      bool
	loadErr    string // non-empty if initial profile load failed
	width      int
	height     int

	// Scroll offsets for each panel.
	profileOffset int
	packOffset    int

	// Successful live MCP tool probes keyed by pack root + server name.
	// Initial tree counts still come from static inventory; once a server
	// is probed, tree rebuilds and count refreshes prefer the cached live
	// inventory for that specific server.
	mcpProbeCache map[mcpProbeKey]mcpProbeEntry

	doubleClick common.DoubleClickTracker
}

const (
	doubleClickWindow   = 500 * time.Millisecond
	packItemCheckStartX = 2
	packItemCheckEndX   = 4
	treeItemCheckStartX = 4
	treeItemCheckEndX   = 6
	treeItemLabelStartX = 8
)

// treeVisibleH returns the height available for the tree panel content.
func (m Model) treeVisibleH() int {
	colH := max(m.height-4, 8)
	return colH
}

func (m *Model) clampProfileOffset() {
	visH := max(m.treeVisibleH()-2, 1)
	m.profileOffset = common.ClampOffset(m.cursor, m.profileOffset, visH)
}

func New(ctx context.Context, eng *engine.Engine, configDir string) Model {
	return Model{
		eng:           eng,
		ctx:           ctx,
		configDir:     configDir,
		mcpProbeCache: loadMCPProbeCacheForTUI(configDir),
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) SetSize(width, height int) common.Screen {
	m.width = width
	m.height = height
	return m
}

// loadMCPProbeCacheForTUI reads the persistent cache written by prior TUI
// sessions and `aipack mcp inspect-tools` runs. Returns an empty (non-nil)
// map when no cache exists or loading fails — the cache is an optimization,
// never required.
func loadMCPProbeCacheForTUI(configDir string) map[mcpProbeKey]mcpProbeEntry {
	out := map[mcpProbeKey]mcpProbeEntry{}
	if configDir == "" {
		return out
	}
	for key, entry := range app.LoadMCPProbeCache(configDir) {
		out[mcpProbeKey{packRoot: key.PackRoot, server: key.Server}] = mcpProbeEntry{
			tools:    entry.Tools,
			probedAt: entry.ProbedAt,
		}
	}
	return out
}

// initCmd returns the command to load profiles at startup.
func (m Model) initCmd(syncCfg config.SyncConfig) tea.Cmd {
	return Load(m.configDir, syncCfg)
}

func (m Model) Update(msg tea.Msg) (common.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case common.FocusMsg, common.BlurMsg:
		return m, nil
	case common.LayerHitMsg:
		if !strings.HasPrefix(msg.ID, "profiles:") {
			return m, nil
		}
		return m.handleLayerHit(msg)
	case LoadedMsg:
		if msg.Err != nil {
			m.loadErr = fmt.Sprintf("load profiles: %v", msg.Err)
			return m, nil
		}
		m.items = msg.Items
		// Start cursor on the active (default) profile.
		m.cursor = 0
		for i, item := range m.items {
			if item.isActive {
				m.cursor = i
				break
			}
		}
		m.clampProfileOffset()
		// Build tree for initially focused profile.
		m = m.ensureTree()
		// Start file size computation; sync check is triggered by rootModel.
		return m, m.computeFileSizesCmd()

	case SyncStatusMsg:
		for i := range m.items {
			if m.items[i].name == msg.ProfileName {
				if msg.Err != nil {
					m.items[i].syncState = common.SyncStatusError
					m.items[i].syncErrText = msg.Err.Error()
				} else if msg.Synced {
					m.items[i].syncState = common.SyncStatusSynced
					m.items[i].syncErrText = ""
				} else {
					m.items[i].syncState = common.SyncStatusUnsynced
					m.items[i].syncErrText = ""
				}
				m.items[i].syncTarget = msg.Target
				m.items[i].syncWarnings = msg.Warnings
			}
		}
		return m, nil

	case FileSizeMsg:
		for i := range m.items {
			if m.items[i].name == msg.ProfileName && m.items[i].tree != nil {
				m.items[i].tree.updateFileSizes(msg.Sizes)
			}
		}
		return m, nil

	case tea.KeyPressMsg:
		switch m.focus {
		case panelProfiles:
			return m.updateProfileList(msg)
		case panelPacks:
			return m.updatePackRoster(msg)
		case panelTree:
			return m.updateTree(msg)
		}
	}
	return m, nil
}

func (m Model) handleLayerHit(msg common.LayerHitMsg) (common.Screen, tea.Cmd) {
	if delta := common.MouseWheelDelta(msg.Mouse); delta != 0 {
		return m.handleWheelHit(msg.ID, delta)
	}

	mouse := msg.Mouse.Mouse()
	switch msg.Mouse.(type) {
	case tea.MouseClickMsg:
		if mouse.Button != tea.MouseLeft {
			return m, nil
		}
	case tea.MouseMotionMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg:
		return m, nil
	default:
		return m, nil
	}

	switch {
	case strings.HasPrefix(msg.ID, "profiles:item:"):
		idx, ok := common.ParseLayerIndex(msg.ID, "profiles:item:")
		if !ok || idx < 0 || idx >= len(m.items) {
			return m, nil
		}
		m.cursor = idx
		m.focus = panelProfiles
		m.clampProfileOffset()
		m.packCursor = 0
		m.packOffset = 0
		m = m.ensureTree()
		if m.isDoubleClick(msg.ID) {
			return m, func() tea.Msg { return ActionRequestMsg{} }
		}
		return m, m.computeFileSizesCmd()
	case strings.HasPrefix(msg.ID, "profiles:pack:"):
		idx, ok := common.ParseLayerIndex(msg.ID, "profiles:pack:")
		item := m.currentItem()
		if !ok || item == nil || idx < 0 || idx > len(item.cfg.Packs) {
			return m, nil
		}
		m.focus = panelPacks
		m.packCursor = idx
		m.packOffset = common.ClampOffset(m.packCursor, m.packOffset, max(m.treeVisibleH()-2, 1))
		if idx == len(item.cfg.Packs) {
			return m, func() tea.Msg { return AddPackRequestMsg{} }
		}
		if localX := m.packClickLocalX(mouse.X); localX >= packItemCheckStartX && localX <= packItemCheckEndX {
			m = m.togglePackEnabled(idx)
			return m, m.computeFileSizesCmd()
		}
		if m.isDoubleClick(msg.ID) {
			return m, func() tea.Msg { return ActionRequestMsg{} }
		}
	case strings.HasPrefix(msg.ID, "profiles:tree:"):
		idx, ok := common.ParseLayerIndex(msg.ID, "profiles:tree:")
		item := m.currentItem()
		if !ok || item == nil || item.tree == nil || idx < 0 || idx >= len(item.tree.nodes) {
			return m, nil
		}
		m.focus = panelTree
		item.tree.cursor = idx
		item.tree.clampOffset(max(m.treeVisibleH()-2, 1))
		node := item.tree.nodes[idx]
		if node.kind == nodeCategory {
			m = m.toggleCurrentTreeNode()
			return m, nil
		}
		if node.kind == nodeItem && m.treeClickLocalX(mouse.X) >= treeItemCheckStartX && m.treeClickLocalX(mouse.X) <= treeItemCheckEndX {
			m = m.toggleCurrentTreeNode()
			return m, nil
		}
		if node.kind == nodeItem && m.isDoubleClick(msg.ID) {
			return m, func() tea.Msg { return ActionRequestMsg{} }
		}
	}
	return m, nil
}

func (m Model) handleWheelHit(id string, delta int) (common.Screen, tea.Cmd) {
	if !strings.HasPrefix(id, "profiles:tree:") {
		return m, nil
	}
	item := m.currentItem()
	if item == nil || item.tree == nil {
		return m, nil
	}
	m.focus = panelTree
	for range common.MouseWheelRows {
		if delta > 0 {
			item.tree.moveDown()
		} else {
			item.tree.moveUp()
		}
	}
	item.tree.clampOffset(max(m.treeVisibleH()-2, 1))
	return m, nil
}

func (m Model) treePanelX() int {
	const padX = 2
	return padX + m.profilesMinWidth() + 3 + m.rosterMinWidth() + 3
}

func (m Model) packPanelX() int {
	const padX = 2
	return padX + m.profilesMinWidth() + 3
}

func (m Model) packClickLocalX(x int) int {
	return x - m.packPanelX()
}

func (m Model) treeClickLocalX(x int) int {
	return x - m.treePanelX()
}

func (m *Model) isDoubleClick(id string) bool {
	return m.doubleClick.Hit(id, doubleClickWindow)
}

func (m Model) updateProfileList(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.cursor < len(m.items)-1 {
			m.cursor++
			m.clampProfileOffset()
			m.packCursor = 0
			m.packOffset = 0
			m = m.ensureTree()
			return m, m.computeFileSizesCmd()
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			m.clampProfileOffset()
			m.packCursor = 0
			m.packOffset = 0
			m = m.ensureTree()
			return m, m.computeFileSizesCmd()
		}
	case "enter", "right", "l":
		item := m.currentItem()
		if item != nil {
			m.focus = panelPacks
		}
	}
	return m, nil
}

func (m Model) updatePackRoster(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	item := m.currentItem()
	if item == nil {
		return m, nil
	}
	// Virtual item count: packs + "Add pack..." row.
	totalItems := len(item.cfg.Packs) + 1
	visH := max(m.treeVisibleH()-2, 1)
	switch msg.String() {
	case "j", "down":
		m.packCursor = (m.packCursor + 1) % totalItems
		m.packOffset = common.ClampOffset(m.packCursor, m.packOffset, visH)
	case "k", "up":
		m.packCursor--
		if m.packCursor < 0 {
			m.packCursor = totalItems - 1
		}
		m.packOffset = common.ClampOffset(m.packCursor, m.packOffset, visH)
	case "space":
		if m.packCursor < len(item.cfg.Packs) {
			m = m.togglePackEnabled(m.packCursor)
			return m, m.computeFileSizesCmd()
		}
	case "enter", "right", "l":
		// "Add pack..." virtual item is at the end.
		if m.packCursor == len(item.cfg.Packs) {
			return m, func() tea.Msg { return AddPackRequestMsg{} }
		}
		if item.tree != nil {
			m.focus = panelTree
		}
	case "J":
		if m.packCursor < len(item.cfg.Packs) {
			m = m.movePackDown(m.packCursor)
			return m, m.computeFileSizesCmd()
		}
	case "K":
		if m.packCursor < len(item.cfg.Packs) {
			m = m.movePackUp(m.packCursor)
			return m, m.computeFileSizesCmd()
		}
	case "esc", "left", "h":
		m.focus = panelProfiles
	}
	return m, nil
}

func (m Model) updateTree(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	item := m.currentItem()
	if item == nil || item.tree == nil {
		m.focus = panelPacks
		return m, nil
	}

	treeH := max(m.treeVisibleH()-2, 1)
	switch msg.String() {
	case "j", "down":
		item.tree.moveDown()
		item.tree.clampOffset(treeH)
	case "k", "up":
		item.tree.moveUp()
		item.tree.clampOffset(treeH)
	case "space":
		m = m.toggleCurrentTreeNode()
	case "enter":
		n := item.tree.cursorNode()
		if n == nil {
			break
		}
		if n.kind == nodeCategory {
			m = m.toggleCurrentTreeNode()
			break
		}
		// Item node: open preview if it has a file path.
		// MCP items have no file path — enter on MCP is intercepted at
		// the root level to open the tool picker instead.
		fp := item.tree.filePath()
		if fp == "" {
			break
		}
		packName := ""
		if n.packIdx >= 0 && n.packIdx < len(item.tree.packs) {
			packName = item.tree.packs[n.packIdx].Name
		}
		return m, func() tea.Msg {
			return common.PreviewRequestMsg{
				Title:    n.id,
				Category: n.category,
				PackName: packName,
				FilePath: fp,
			}
		}
	case "esc", "left", "h":
		m.focus = panelPacks
	}
	return m, nil
}

func (m Model) toggleCurrentTreeNode() Model {
	item := m.currentItem()
	if item == nil || item.tree == nil {
		return m
	}
	if item.tree.toggle() {
		ct := item.tree.toContentTree()
		app.ApplyContentTree(ct, item.cfg.Packs)
		item.tree.refreshConflicts(item.cfg.Packs)
		m.dirty = true
		item.dirty = true
		if item.isActive {
			item.syncState = common.SyncStatusPending
		}
	}
	return m
}

// togglePackEnabled toggles the Enabled flag on the pack at the given index,
// rebuilds the content tree, and marks the model dirty.
func (m Model) togglePackEnabled(idx int) Model {
	item := m.currentItem()
	if item == nil || idx < 0 || idx >= len(item.cfg.Packs) {
		return m
	}
	pe := &item.cfg.Packs[idx]
	if pe.Enabled == nil || *pe.Enabled {
		pe.Enabled = boolPtr(false)
	} else {
		pe.Enabled = nil // nil = enabled (default-true)
	}
	return m.rebuildTree()
}

// movePackUp swaps the pack at idx with the one above it, moving it earlier
// in the precedence order. The cursor follows the moved pack.
func (m Model) movePackUp(idx int) Model {
	item := m.currentItem()
	if item == nil || idx <= 0 || idx >= len(item.cfg.Packs) {
		return m
	}
	item.cfg.Packs[idx], item.cfg.Packs[idx-1] = item.cfg.Packs[idx-1], item.cfg.Packs[idx]
	m.packCursor = idx - 1
	m = m.rebuildTree()
	return m
}

// movePackDown swaps the pack at idx with the one below it, moving it later
// in the precedence order. The cursor follows the moved pack.
func (m Model) movePackDown(idx int) Model {
	item := m.currentItem()
	if item == nil || idx < 0 || idx >= len(item.cfg.Packs)-1 {
		return m
	}
	item.cfg.Packs[idx], item.cfg.Packs[idx+1] = item.cfg.Packs[idx+1], item.cfg.Packs[idx]
	m.packCursor = idx + 1
	m = m.rebuildTree()
	return m
}

// rebuildTree invalidates and rebuilds the tree, marking the profile dirty.
func (m Model) rebuildTree() Model {
	item := m.currentItem()
	if item == nil {
		return m
	}
	item.tree = nil
	item.treeErr = ""
	m = m.ensureTree()
	m.dirty = true
	item.dirty = true
	if item.isActive {
		item.syncState = common.SyncStatusPending
	}
	return m
}

// boolPtr returns a pointer to a bool value.
var boolPtr = config.BoolPtr

// ensureTree builds a content tree for the currently focused profile if needed.
func (m Model) ensureTree() Model {
	item := m.currentItem()
	if item == nil || item.tree != nil {
		return m
	}
	if len(item.cfg.Packs) == 0 {
		return m
	}

	// Resolve all packs via app layer.
	allPacks, errs := app.ResolveProfilePacks(m.configDir, item.cfg.Packs)

	// Filter to enabled packs only for the tree.
	var enabled []app.ProfilePackInfo
	for _, p := range allPacks {
		pe := item.cfg.Packs[p.Index]
		if pe.Enabled != nil && !*pe.Enabled {
			continue
		}
		enabled = append(enabled, p)
	}

	if len(enabled) == 0 {
		if len(errs) > 0 {
			item.treeErr = errs[0]
		} else {
			item.treeErr = "no pack sources could be resolved"
		}
		return m
	}

	ct := app.BuildContentTree(enabled, item.cfg.Packs)
	tree := buildTreeFromContent(ct)
	tree.mcpCounts = computeMCPCountsWithProbeCache(enabled, item.cfg, m.mcpProbeCache)
	tree.applyMCPCounts()
	item.tree = &tree
	item.treeErr = ""
	return m
}

// computeFileSizesCmd returns a tea.Cmd to compute file sizes for the current tree.
func (m Model) computeFileSizesCmd() tea.Cmd {
	item := m.currentItem()
	if item == nil || item.tree == nil {
		return nil
	}
	if item.tree.hasSizes() {
		return nil
	}
	profileName := item.name
	packs := item.tree.packs
	return func() tea.Msg {
		sizes := app.PackContentSizes(packs)
		return FileSizeMsg{ProfileName: profileName, Sizes: sizes}
	}
}

// checkSyncCmd returns a tea.Cmd to check sync status for the active profile.
// Uses activeItem() so sync status is always checked for the default profile
// regardless of cursor position in the Profiles tab.
func (m Model) checkSyncCmd(syncCfg config.SyncConfig, reg *harness.Registry) tea.Cmd {
	item := m.activeItem()
	if item == nil || item.syncState == common.SyncStatusLoading {
		return nil
	}
	if len(item.cfg.Packs) == 0 {
		item.syncState = common.SyncStatusSynced // nothing to sync
		return nil
	}
	item.syncState = common.SyncStatusLoading
	return CheckSyncStatus(m.ctx, m.eng, m.configDir, item.name, item.path, item.cfg, syncCfg, reg)
}

func (m Model) currentItem() *profileItem {
	if len(m.items) == 0 || m.cursor < 0 || m.cursor >= len(m.items) {
		return nil
	}
	return &m.items[m.cursor]
}

// activeItem returns the profile marked as active (default), regardless of cursor position.
func (m Model) activeItem() *profileItem {
	for i := range m.items {
		if m.items[i].isActive {
			return &m.items[i]
		}
	}
	return nil
}

func (m Model) saveAll() tea.Cmd {
	var cmds []tea.Cmd
	for _, item := range m.items {
		if item.dirty {
			cmds = append(cmds, Save(m.configDir, item.name, item.cfg))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// addPackToProfile appends a pack to the current profile's in-memory config.
func (m Model) addPackToProfile(packName string) Model {
	item := m.currentItem()
	if item == nil {
		return m
	}

	// Add pack entry (enabled by default).
	item.cfg.Packs = append(item.cfg.Packs, config.PackEntry{
		Name: packName,
	})
	return m.rebuildTree()
}

// removePackFromProfile removes a pack from the current profile's in-memory config.
func (m Model) removePackFromProfile(packName string) Model {
	item := m.currentItem()
	if item == nil {
		return m
	}

	// Remove from packs.
	var newPacks []config.PackEntry
	for _, pe := range item.cfg.Packs {
		if pe.Name != packName {
			newPacks = append(newPacks, pe)
		}
	}
	item.cfg.Packs = newPacks
	return m.rebuildTree()
}

// profilePackNames returns pack names in the current profile.
func (m Model) profilePackNames() []string {
	item := m.currentItem()
	if item == nil {
		return nil
	}
	names := make([]string, len(item.cfg.Packs))
	for i, pe := range item.cfg.Packs {
		names[i] = pe.Name
	}
	return names
}

func (m Model) View() *lipgloss.Layer {
	root := lipgloss.NewLayer(m.render())
	root.AddLayers(m.clickLayers()...)
	return root
}

func (m Model) render() string {
	if len(m.items) == 0 {
		return common.ContentStyle.Render("Loading profiles...")
	}

	// Three-column layout: fit columns 1+2 to their content, give remainder to 3.
	col1W := m.profilesMinWidth()
	col2W := m.rosterMinWidth()
	col3W := max(m.width-col1W-col2W-8, 20) // 8 = separators + padding

	colH := max(m.height-4, 8) // account for tab bar + help bar

	col1 := m.viewProfileList(col1W, colH)
	sep := common.DimStyle.Render(" │ ")
	col2 := m.viewPackRoster(col2W, colH)
	col3 := m.viewTreePanel(col3W, colH)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, col1, sep, col2, sep, col3)
	return common.ContentStyle.Render(joined)
}

func (m Model) clickLayers() []*lipgloss.Layer {
	const (
		padX = 2
		padY = 1
	)
	layers := make([]*lipgloss.Layer, 0, len(m.items)+8)
	col1W := m.profilesMinWidth()
	visibleProfiles := max(m.treeVisibleH()-2, 1)
	for i := m.profileOffset; i < len(m.items) && i < m.profileOffset+visibleProfiles; i++ {
		layers = append(layers,
			lipgloss.NewLayer(hitRow(col1W)).ID(fmt.Sprintf("profiles:item:%d", i)).X(padX).Y(padY+1+i-m.profileOffset).Z(-1),
		)
	}

	item := m.currentItem()
	if item == nil {
		return layers
	}

	packX := padX + col1W + 3
	col2W := m.rosterMinWidth()
	visiblePacks := max(m.treeVisibleH()-2, 1)
	endPack := min(len(item.cfg.Packs), m.packOffset+visiblePacks)
	for i := m.packOffset; i < endPack; i++ {
		layers = append(layers,
			lipgloss.NewLayer(hitRow(col2W)).ID(fmt.Sprintf("profiles:pack:%d", i)).X(packX).Y(padY+1+i-m.packOffset).Z(-1),
		)
	}
	if len(item.cfg.Packs) >= m.packOffset && len(item.cfg.Packs) < m.packOffset+visiblePacks {
		layers = append(layers,
			lipgloss.NewLayer(hitRow(col2W)).ID(fmt.Sprintf("profiles:pack:%d", len(item.cfg.Packs))).X(packX).Y(padY+1+len(item.cfg.Packs)-m.packOffset).Z(-1),
		)
	}

	if item.tree == nil {
		return layers
	}
	treeX := packX + col2W + 3
	col3W := max(m.width-col1W-col2W-8, 20)
	treeVisible := max(m.treeVisibleH()-2, 1)
	visibleIndex := 0
	drawn := 0
	for i := range item.tree.nodes {
		if !item.tree.isVisible(i) {
			continue
		}
		if visibleIndex < item.tree.offset {
			visibleIndex++
			continue
		}
		if drawn >= treeVisible {
			break
		}
		layers = append(layers,
			lipgloss.NewLayer(hitRow(col3W)).ID(fmt.Sprintf("profiles:tree:%d", i)).X(treeX).Y(padY+1+drawn).Z(-1),
		)
		visibleIndex++
		drawn++
	}
	return layers
}

func hitRow(width int) string {
	return strings.Repeat(" ", max(width, 1))
}

// profilesMinWidth returns the minimum column width to fit all profile names
// without wrapping: cursor(2) + dot(1) + space(1) + name + " (active)"(9) + padding(2).
func (m Model) profilesMinWidth() int {
	const (
		prefix = 4  // "> ● " or "  ○ "
		pad    = 2  // breathing room
		minW   = 18 // floor
	)
	w := len("Profiles") // header
	for _, item := range m.items {
		lineW := prefix + len(item.name)
		if item.isActive {
			lineW += len(" (active)")
		}
		if n := len(item.syncWarnings); n > 0 {
			lineW += len(fmt.Sprintf(" %d warnings", n))
		}
		if lineW > w {
			w = lineW
		}
	}
	return max(w+pad, minW)
}

// rosterMinWidth returns the minimum column width to fit all pack roster entries
// without wrapping: cursor(2) + check(3) + space(1) + name + " (settings off)"(15) + padding(2).
func (m Model) rosterMinWidth() int {
	const (
		prefix = 6  // "> [x] " or "  [x] "
		pad    = 2  // breathing room
		minW   = 22 // floor
		maxW   = 32 // keep the content tree wide even with long pack names
	)
	item := m.currentItem()
	if item == nil {
		return minW
	}
	w := len("Add pack...") + 2 // virtual item + cursor indent
	for _, pe := range item.cfg.Packs {
		lineW := prefix + len(pe.Name)
		if config.SettingsDisabled(pe.Settings.Enabled) {
			lineW += len(" (settings off)")
		}
		if lineW > w {
			w = lineW
		}
	}
	// Header: "Packs (NN/NN)" — no cursor/check prefix on the header line.
	header := len(fmt.Sprintf("Packs (%d/%d)", len(item.cfg.Packs), len(item.cfg.Packs)))
	if header > w {
		w = header
	}
	return min(max(w+pad, minW), maxW)
}

// viewProfileList renders the top-left panel with all profiles.
func (m Model) viewProfileList(width, height int) string {
	style := lipgloss.NewStyle().Width(width).Height(height)
	var sb strings.Builder

	sb.WriteString(panelHeaderStyle.Render("Profiles") + "\n")

	var lines []string
	focused := m.focus == panelProfiles
	for i, item := range m.items {
		cursor := "  "
		if focused && i == m.cursor {
			cursor = common.SelectedStyle.Render("> ")
		}

		dot := common.DimStyle.Render("○") // non-active profiles: neutral dot
		if item.isActive {
			switch item.syncState {
			case common.SyncStatusSynced:
				dot = statusDotActive // green: synced
			case common.SyncStatusUnsynced:
				dot = statusDotInactive // red: out of sync
			case common.SyncStatusLoading:
				dot = statusDotLoading // yellow: checking
			case common.SyncStatusError:
				dot = statusDotInactive // red: error
			default:
				dot = common.DimStyle.Render("●") // pending: dim filled
			}
		}

		nameStyle := lipgloss.NewStyle()
		if i == m.cursor {
			nameStyle = common.SelectedStyle
		} else if !focused {
			nameStyle = common.DimStyle
		}

		label := ""
		if item.isActive {
			label = common.DimStyle.Render(" (active)")
		}

		warnBadge := ""
		if n := len(item.syncWarnings); n > 0 {
			warnBadge = common.WarningStyle.Render(fmt.Sprintf(" %d warning", n))
			if n > 1 {
				warnBadge = common.WarningStyle.Render(fmt.Sprintf(" %d warnings", n))
			}
		}

		lines = append(lines, fmt.Sprintf("%s%s %s%s%s", cursor, dot, nameStyle.Render(item.name), label, warnBadge))
	}

	common.WriteScrollWindow(&sb, lines, m.profileOffset, max(height-2, 1), func(int) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("  ...")
	})

	return style.Render(sb.String())
}

// viewPackRoster renders the bottom-left panel with packs for the selected profile.
func (m Model) viewPackRoster(width, height int) string {
	item := m.currentItem()
	if item == nil {
		return ""
	}

	style := lipgloss.NewStyle().Width(width).Height(height)
	var sb strings.Builder

	// Pack count header.
	enabledCount := 0
	for _, pe := range item.cfg.Packs {
		if pe.Enabled == nil || *pe.Enabled {
			enabledCount++
		}
	}
	sb.WriteString(panelHeaderStyle.Render(fmt.Sprintf("Packs (%d/%d)", enabledCount, len(item.cfg.Packs))) + "\n")

	var lines []string
	focused := m.focus == panelPacks
	for i, pe := range item.cfg.Packs {
		enabled := pe.Enabled == nil || *pe.Enabled

		cursor := "  "
		if focused && i == m.packCursor {
			cursor = common.SelectedStyle.Render("> ")
		}
		check := "[x]"
		if !enabled {
			check = "[ ]"
		}

		nameStyle := packColorBright(i)
		if !enabled {
			nameStyle = common.DimStyle
		} else if focused && i == m.packCursor {
			nameStyle = common.SelectedStyle
		}
		name := nameStyle.Render(pe.Name)
		label := ""
		if config.SettingsDisabled(pe.Settings.Enabled) {
			label = " " + common.DimStyle.Render("(settings off)")
		}
		lines = append(lines, fmt.Sprintf("%s%s %s%s", cursor, check, name, label))
	}

	// "Add pack..." virtual item.
	addIdx := len(item.cfg.Packs)
	addCursor := "  "
	if focused && m.packCursor == addIdx {
		addCursor = common.SelectedStyle.Render("> ")
	}
	addLabel := common.DimStyle.Render("Add pack...")
	if focused && m.packCursor == addIdx {
		addLabel = common.SelectedStyle.Render("Add pack...")
	}
	lines = append(lines, addCursor+addLabel)

	lines = common.TruncateLines(lines, width)
	common.WriteScrollWindow(&sb, lines, m.packOffset, max(height-2, 1), func(int) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("  ...")
	})

	return style.Render(sb.String())
}

// toggleSettingsDisabled toggles settings.enabled: false for the named pack.
// If currently disabled, clears to nil (contribute by default).
// If currently nil or true, sets to false (opt out).
func (m Model) toggleSettingsDisabled(packName string) Model {
	item := m.currentItem()
	if item == nil {
		return m
	}
	for i := range item.cfg.Packs {
		if item.cfg.Packs[i].Name == packName {
			if item.cfg.Packs[i].Settings.Enabled != nil && !*item.cfg.Packs[i].Settings.Enabled {
				item.cfg.Packs[i].Settings.Enabled = nil // re-enable (default: contribute)
			} else {
				item.cfg.Packs[i].Settings.Enabled = boolPtr(false) // opt out
			}
			break
		}
	}
	m.dirty = true
	item.dirty = true
	if item.isActive {
		item.syncState = common.SyncStatusPending
	}
	return m
}

// mutateOverride resolves the override slice for the given pack/category and
// calls mutate to apply the change. Handles all validation and post-mutation
// bookkeeping (conflict refresh, dirty flags).
func (m Model) mutateOverride(packIdx int, category domain.PackCategory, id string, mutate func(*[]string, string)) Model {
	item := m.currentItem()
	if item == nil {
		return m
	}
	if item.tree == nil || packIdx < 0 || packIdx >= len(item.tree.packs) {
		return m
	}
	entryIdx := item.tree.packs[packIdx].Index
	if entryIdx < 0 || entryIdx >= len(item.cfg.Packs) {
		return m
	}
	pe := &item.cfg.Packs[entryIdx]
	overrides := pe.OverridesForCategory(category)
	if overrides == nil {
		return m
	}
	mutate(overrides, id)
	item.tree.refreshConflicts(item.cfg.Packs)
	m.dirty = true
	item.dirty = true
	if item.isActive {
		item.syncState = common.SyncStatusPending
	}
	return m
}

// setOverride adds the given content id to the override list for the pack at
// packIdx in the current profile.
func (m Model) setOverride(packIdx int, category domain.PackCategory, id string) Model {
	return m.mutateOverride(packIdx, category, id, func(overrides *[]string, id string) {
		if !slices.Contains(*overrides, id) {
			*overrides = append(*overrides, id)
		}
	})
}

// removeOverride removes the given content id from the override list for the
// pack at packIdx.
func (m Model) removeOverride(packIdx int, category domain.PackCategory, id string) Model {
	return m.mutateOverride(packIdx, category, id, func(overrides *[]string, id string) {
		*overrides = slices.DeleteFunc(*overrides, func(s string) bool { return s == id })
	})
}

// viewTreePanel renders the right panel with the content tree.
func (m Model) viewTreePanel(width, height int) string {
	item := m.currentItem()
	if item == nil {
		return ""
	}

	style := lipgloss.NewStyle().Width(width).Height(height)
	var sb strings.Builder

	// Header with item count.
	itemCount := 0
	if item.tree != nil {
		for _, n := range item.tree.nodes {
			if n.kind == nodeItem {
				itemCount++
			}
		}
	}
	header := "Content"
	if itemCount > 0 {
		header = fmt.Sprintf("Content (%d)", itemCount)
	}
	sb.WriteString(panelHeaderStyle.Render(header) + "\n")

	usedH := 1
	if len(item.bundledFrom) > 0 {
		warning := fmt.Sprintf("pack-provided profile from %s; duplicate before editing; pack update can overwrite it.", strings.Join(item.bundledFrom, ", "))
		sb.WriteString(common.WarningStyle.Render(warning) + "\n")
		usedH++
	}

	if item.tree != nil {
		sb.WriteString(item.tree.view(m.focus == panelTree, width, height-usedH))
	} else if item.treeErr != "" {
		sb.WriteString(common.ErrorStyle.Render(fmt.Sprintf("error: %s", item.treeErr)))
		sb.WriteString("\n")
	} else if len(item.cfg.Packs) == 0 {
		sb.WriteString(common.DimStyle.Render("No packs in this profile."))
		sb.WriteString("\n")
		sb.WriteString(common.DimStyle.Render("Move → to add a pack."))
		sb.WriteString("\n")
	} else {
		sb.WriteString(common.DimStyle.Render("(no content)"))
		sb.WriteString("\n")
	}

	return style.Render(sb.String())
}
