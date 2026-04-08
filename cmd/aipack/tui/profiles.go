package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

type profilesModel struct {
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
}

// treeVisibleH returns the height available for the tree panel content.
func (m profilesModel) treeVisibleH() int {
	colH := max(m.height-4, 8)
	return colH
}

func (m *profilesModel) clampProfileOffset() {
	visH := max(m.treeVisibleH()-2, 1)
	m.profileOffset = clampOffset(m.cursor, m.profileOffset, visH)
}

func newProfilesModel(ctx context.Context, eng *engine.Engine, configDir string) profilesModel {
	return profilesModel{
		eng:       eng,
		ctx:       ctx,
		configDir: configDir,
	}
}

// initCmd returns the command to load profiles at startup.
func (m profilesModel) initCmd(syncCfg config.SyncConfig) tea.Cmd {
	return loadProfiles(m.configDir, syncCfg)
}

func (m profilesModel) Update(msg tea.Msg) (profilesModel, tea.Cmd) {
	switch msg := msg.(type) {
	case profilesLoadedMsg:
		if msg.err != nil {
			m.loadErr = fmt.Sprintf("load profiles: %v", msg.err)
			return m, nil
		}
		m.items = msg.items
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

	case syncStatusMsg:
		for i := range m.items {
			if m.items[i].name == msg.profileName {
				if msg.err != nil {
					m.items[i].syncState = syncError
					m.items[i].syncErrText = msg.err.Error()
				} else if msg.synced {
					m.items[i].syncState = syncSynced
					m.items[i].syncErrText = ""
				} else {
					m.items[i].syncState = syncUnsynced
					m.items[i].syncErrText = ""
				}
				m.items[i].syncTarget = msg.target
				m.items[i].syncWarnings = msg.warnings
			}
		}
		return m, nil

	case fileSizeMsg:
		for i := range m.items {
			if m.items[i].name == msg.profileName && m.items[i].tree != nil {
				m.items[i].tree.updateFileSizes(msg.sizes)
			}
		}
		return m, nil

	case tea.KeyMsg:
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

func (m profilesModel) updateProfileList(msg tea.KeyMsg) (profilesModel, tea.Cmd) {
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

func (m profilesModel) updatePackRoster(msg tea.KeyMsg) (profilesModel, tea.Cmd) {
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
		m.packOffset = clampOffset(m.packCursor, m.packOffset, visH)
	case "k", "up":
		m.packCursor--
		if m.packCursor < 0 {
			m.packCursor = totalItems - 1
		}
		m.packOffset = clampOffset(m.packCursor, m.packOffset, visH)
	case " ":
		if m.packCursor < len(item.cfg.Packs) {
			m = m.togglePackEnabled(m.packCursor)
			return m, m.computeFileSizesCmd()
		}
	case "enter", "right", "l":
		// "Add pack..." virtual item is at the end.
		if m.packCursor == len(item.cfg.Packs) {
			return m, func() tea.Msg { return requestAddPackMsg{} }
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

func (m profilesModel) updateTree(msg tea.KeyMsg) (profilesModel, tea.Cmd) {
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
	case " ":
		if item.tree.toggle() {
			ct := item.tree.toContentTree()
			app.ApplyContentTree(ct, item.cfg.Packs)
			item.tree.refreshConflicts(item.cfg.Packs)
			m.dirty = true
			item.dirty = true
			if item.isActive {
				item.syncState = syncPending // invalidate stale sync status
			}
		}
	case "enter":
		n := item.tree.cursorNode()
		if n == nil {
			break
		}
		if n.kind == nodeCategory {
			item.tree.toggle() // expand/collapse
			break
		}
		// Item node: open preview if it has a file path.
		fp := item.tree.filePath()
		if fp == "" {
			break // MCP or unresolvable
		}
		packName := ""
		if n.packIdx >= 0 && n.packIdx < len(item.tree.packs) {
			packName = item.tree.packs[n.packIdx].Name
		}
		return m, func() tea.Msg {
			return previewRequestMsg{
				title:    n.id,
				category: n.category,
				packName: packName,
				filePath: fp,
			}
		}
	case "esc", "left", "h":
		m.focus = panelPacks
	}
	return m, nil
}

// togglePackEnabled toggles the Enabled flag on the pack at the given index,
// rebuilds the content tree, and marks the model dirty.
func (m profilesModel) togglePackEnabled(idx int) profilesModel {
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
func (m profilesModel) movePackUp(idx int) profilesModel {
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
func (m profilesModel) movePackDown(idx int) profilesModel {
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
func (m profilesModel) rebuildTree() profilesModel {
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
		item.syncState = syncPending
	}
	return m
}

// boolPtr returns a pointer to a bool value.
var boolPtr = config.BoolPtr

// ensureTree builds a content tree for the currently focused profile if needed.
func (m profilesModel) ensureTree() profilesModel {
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
	item.tree = &tree
	item.treeErr = ""
	return m
}

// computeFileSizesCmd returns a tea.Cmd to compute file sizes for the current tree.
func (m profilesModel) computeFileSizesCmd() tea.Cmd {
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
		return fileSizeMsg{profileName: profileName, sizes: sizes}
	}
}

// checkSyncCmd returns a tea.Cmd to check sync status for the active profile.
// Uses activeItem() so sync status is always checked for the default profile
// regardless of cursor position in the Profiles tab.
func (m profilesModel) checkSyncCmd(syncCfg config.SyncConfig, reg *harness.Registry) tea.Cmd {
	item := m.activeItem()
	if item == nil || item.syncState == syncLoading {
		return nil
	}
	if len(item.cfg.Packs) == 0 {
		item.syncState = syncSynced // nothing to sync
		return nil
	}
	item.syncState = syncLoading
	return checkSyncStatus(m.ctx, m.eng, m.configDir, item.name, item.path, item.cfg, syncCfg, reg)
}

func (m profilesModel) currentItem() *profileItem {
	if len(m.items) == 0 || m.cursor < 0 || m.cursor >= len(m.items) {
		return nil
	}
	return &m.items[m.cursor]
}

// activeItem returns the profile marked as active (default), regardless of cursor position.
func (m profilesModel) activeItem() *profileItem {
	for i := range m.items {
		if m.items[i].isActive {
			return &m.items[i]
		}
	}
	return nil
}

func (m profilesModel) saveAll() tea.Cmd {
	var cmds []tea.Cmd
	for _, item := range m.items {
		if item.dirty {
			cmds = append(cmds, saveProfile(m.configDir, item.name, item.cfg))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// addPackToProfile appends a pack to the current profile's in-memory config.
func (m profilesModel) addPackToProfile(packName string) profilesModel {
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
func (m profilesModel) removePackFromProfile(packName string) profilesModel {
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
func (m profilesModel) profilePackNames() []string {
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

// shortPath replaces $HOME prefix with ~.
func shortPath(p string) string {
	home := config.HomeDir()
	if home == "" {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func (m profilesModel) View() string {
	if len(m.items) == 0 {
		return contentStyle.Render("Loading profiles...")
	}

	// Three-column layout: fit columns 1+2 to their content, give remainder to 3.
	col1W := m.profilesMinWidth()
	col2W := m.rosterMinWidth()
	col3W := max(m.width-col1W-col2W-8, 20) // 8 = separators + padding

	colH := max(m.height-4, 8) // account for tab bar + help bar

	col1 := m.viewProfileList(col1W, colH)
	sep := dimStyle.Render(" │ ")
	col2 := m.viewPackRoster(col2W, colH)
	col3 := m.viewTreePanel(col3W, colH)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, col1, sep, col2, sep, col3)
	return contentStyle.Render(joined)
}

// profilesMinWidth returns the minimum column width to fit all profile names
// without wrapping: cursor(2) + dot(1) + space(1) + name + " (active)"(9) + padding(2).
func (m profilesModel) profilesMinWidth() int {
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
func (m profilesModel) rosterMinWidth() int {
	const (
		prefix = 6  // "> [x] " or "  [x] "
		pad    = 2  // breathing room
		minW   = 22 // floor
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
	return max(w+pad, minW)
}

// viewProfileList renders the top-left panel with all profiles.
func (m profilesModel) viewProfileList(width, height int) string {
	style := lipgloss.NewStyle().Width(width).Height(height)
	var sb strings.Builder

	sb.WriteString(panelHeaderStyle.Render("Profiles") + "\n")

	var lines []string
	focused := m.focus == panelProfiles
	for i, item := range m.items {
		cursor := "  "
		if focused && i == m.cursor {
			cursor = selectedStyle.Render("> ")
		}

		dot := dimStyle.Render("○") // non-active profiles: neutral dot
		if item.isActive {
			switch item.syncState {
			case syncSynced:
				dot = statusDotActive // green: synced
			case syncUnsynced:
				dot = statusDotInactive // red: out of sync
			case syncLoading:
				dot = statusDotLoading // yellow: checking
			case syncError:
				dot = statusDotInactive // red: error
			default:
				dot = dimStyle.Render("●") // pending: dim filled
			}
		}

		nameStyle := lipgloss.NewStyle()
		if i == m.cursor {
			nameStyle = selectedStyle
		} else if !focused {
			nameStyle = dimStyle
		}

		label := ""
		if item.isActive {
			label = dimStyle.Render(" (active)")
		}

		warnBadge := ""
		if n := len(item.syncWarnings); n > 0 {
			warnBadge = warningStyle.Render(fmt.Sprintf(" %d warning", n))
			if n > 1 {
				warnBadge = warningStyle.Render(fmt.Sprintf(" %d warnings", n))
			}
		}

		lines = append(lines, fmt.Sprintf("%s%s %s%s%s", cursor, dot, nameStyle.Render(item.name), label, warnBadge))
	}

	writeScrollWindow(&sb, lines, m.profileOffset, max(height-2, 1))

	return style.Render(sb.String())
}

// viewPackRoster renders the bottom-left panel with packs for the selected profile.
func (m profilesModel) viewPackRoster(width, height int) string {
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
			cursor = selectedStyle.Render("> ")
		}
		check := "[x]"
		if !enabled {
			check = "[ ]"
		}

		nameStyle := packColorBright(i)
		if !enabled {
			nameStyle = dimStyle
		}
		name := nameStyle.Render(pe.Name)
		label := ""
		if config.SettingsDisabled(pe.Settings.Enabled) {
			label = " " + dimStyle.Render("(settings off)")
		}
		lines = append(lines, fmt.Sprintf("%s%s %s%s", cursor, check, name, label))
	}

	// "Add pack..." virtual item.
	addIdx := len(item.cfg.Packs)
	addCursor := "  "
	if focused && m.packCursor == addIdx {
		addCursor = selectedStyle.Render("> ")
	}
	addLabel := dimStyle.Render("Add pack...")
	if focused && m.packCursor == addIdx {
		addLabel = selectedStyle.Render("Add pack...")
	}
	lines = append(lines, addCursor+addLabel)

	writeScrollWindow(&sb, lines, m.packOffset, max(height-2, 1))

	return style.Render(sb.String())
}

// toggleSettingsDisabled toggles settings.enabled: false for the named pack.
// If currently disabled, clears to nil (contribute by default).
// If currently nil or true, sets to false (opt out).
func (m profilesModel) toggleSettingsDisabled(packName string) profilesModel {
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
		item.syncState = syncPending
	}
	return m
}

// mutateOverride resolves the override slice for the given pack/category and
// calls mutate to apply the change. Handles all validation and post-mutation
// bookkeeping (conflict refresh, dirty flags).
func (m profilesModel) mutateOverride(packIdx int, category domain.PackCategory, id string, mutate func(*[]string, string)) profilesModel {
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
		item.syncState = syncPending
	}
	return m
}

// setOverride adds the given content id to the override list for the pack at
// packIdx in the current profile.
func (m profilesModel) setOverride(packIdx int, category domain.PackCategory, id string) profilesModel {
	return m.mutateOverride(packIdx, category, id, func(overrides *[]string, id string) {
		if !slices.Contains(*overrides, id) {
			*overrides = append(*overrides, id)
		}
	})
}

// removeOverride removes the given content id from the override list for the
// pack at packIdx.
func (m profilesModel) removeOverride(packIdx int, category domain.PackCategory, id string) profilesModel {
	return m.mutateOverride(packIdx, category, id, func(overrides *[]string, id string) {
		*overrides = slices.DeleteFunc(*overrides, func(s string) bool { return s == id })
	})
}

// viewTreePanel renders the right panel with the content tree.
func (m profilesModel) viewTreePanel(width, height int) string {
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
	if itemCount > 0 {
		sb.WriteString(panelHeaderStyle.Render(fmt.Sprintf("Content (%d)", itemCount)) + "\n")
	} else {
		sb.WriteString(panelHeaderStyle.Render("Content") + "\n")
	}

	if item.tree != nil {
		sb.WriteString(item.tree.view(m.focus == panelTree, width, height-1))
	} else if item.treeErr != "" {
		sb.WriteString(errorStyle.Render(fmt.Sprintf("error: %s", item.treeErr)))
		sb.WriteString("\n")
	} else if len(item.cfg.Packs) == 0 {
		sb.WriteString(dimStyle.Render("No packs in this profile."))
		sb.WriteString("\n")
		sb.WriteString(dimStyle.Render("Move → to add a pack."))
		sb.WriteString("\n")
	} else {
		sb.WriteString(dimStyle.Render("(no content)"))
		sb.WriteString("\n")
	}

	return style.Render(sb.String())
}
