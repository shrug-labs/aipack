package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/harness"
)

type panelFocus int

const (
	panelProfiles panelFocus = iota // top-left: profile list
	panelPacks                      // bottom-left: pack roster
	panelTree                       // right: content tree
)

type profilesModel struct {
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

func newProfilesModel(configDir string) profilesModel {
	return profilesModel{
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
	visH := max(m.treeVisibleH()-2, 1)
	switch msg.String() {
	case "j", "down":
		if m.cursor < len(m.items)-1 {
			m.cursor++
			m.profileOffset = clampOffset(m.cursor, m.profileOffset, visH)
			m.packCursor = 0
			m.packOffset = 0
			m = m.ensureTree()
			return m, m.computeFileSizesCmd()
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			m.profileOffset = clampOffset(m.cursor, m.profileOffset, visH)
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
			item.tree.applyToProfile(item.cfg.Packs)
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
			packName = item.tree.packs[n.packIdx].name
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
	// Rebuild tree to reflect the change.
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

// boolPtr is a local alias for config.BoolPtr.
var boolPtr = config.BoolPtr

// ensureTree builds a content tree for the currently focused profile if needed.
func (m profilesModel) ensureTree() profilesModel {
	item := m.currentItem()
	if item == nil || item.tree != nil {
		return m
	}
	if len(item.cfg.Packs) == 0 {
		item.treeErr = "no packs configured in this profile"
		return m
	}

	// Resolve all packs; only include enabled in tree.
	var resolved []packInfo
	var errs []string
	for i, pe := range item.cfg.Packs {
		manifestPath := filepath.Join(m.configDir, "packs", pe.Name, "pack.json")
		manifest, err := config.LoadPackManifest(manifestPath)
		if err != nil {
			errs = append(errs, fmt.Sprintf("pack %q: %v", pe.Name, err))
			continue
		}
		packRoot := config.ResolvePackRoot(manifestPath, manifest.Root)

		if pe.Enabled != nil && !*pe.Enabled {
			continue // skip disabled packs from tree
		}
		resolved = append(resolved, packInfo{
			idx:      i,
			name:     pe.Name,
			root:     packRoot,
			manifest: manifest,
		})
	}

	if len(resolved) == 0 {
		if len(errs) > 0 {
			item.treeErr = errs[0]
		} else {
			item.treeErr = "no pack sources could be resolved"
		}
		return m
	}

	tree := buildMultiPackTree(resolved, item.cfg.Packs)
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
		sizes := computeFileSizesForProfile(packs)
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
	item.syncState = syncLoading
	return checkSyncStatus(m.configDir, item.name, item.path, item.cfg, syncCfg, reg)
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

	// Rebuild tree.
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

	// Rebuild tree.
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
	home := os.Getenv("HOME")
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

	// Three-column layout: profiles (20%) | packs (30%) | content tree (50%).
	col1W := m.width * 20 / 100
	if col1W < 20 {
		col1W = 20
	}
	col2W := m.width * 30 / 100
	if col2W < 25 {
		col2W = 25
	}
	col3W := m.width - col1W - col2W - 8 // account for padding/borders
	if col3W < 20 {
		col3W = 20
	}

	colH := m.height - 4 // account for tab bar + help bar
	if colH < 8 {
		colH = 8
	}

	col1 := m.viewProfileList(col1W, colH)
	sep := dimStyle.Render(" │ ")
	col2 := m.viewPackRoster(col2W, colH)
	col3 := m.viewTreePanel(col3W, colH)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, col1, sep, col2, sep, col3)
	return contentStyle.Render(joined)
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
		if !focused && i != m.cursor {
			nameStyle = dimStyle
		}
		if focused && i == m.cursor {
			nameStyle = selectedStyle
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

	settingsPack := m.settingsSourcePack()

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
		if pe.Name == settingsPack {
			label = " " + dimStyle.Render("(settings)")
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

// settingsSourcePack returns the name of the pack with settings.enabled: true.
func (m profilesModel) settingsSourcePack() string {
	item := m.currentItem()
	if item == nil {
		return ""
	}
	for _, pe := range item.cfg.Packs {
		if pe.Settings.Enabled != nil && *pe.Settings.Enabled {
			return pe.Name
		}
	}
	return ""
}

// setSettingsSource sets settings.enabled on the named pack and clears it on all others.
func (m profilesModel) setSettingsSource(packName string) profilesModel {
	item := m.currentItem()
	if item == nil {
		return m
	}
	for i := range item.cfg.Packs {
		if item.cfg.Packs[i].Name == packName {
			item.cfg.Packs[i].Settings.Enabled = boolPtr(true)
		} else {
			item.cfg.Packs[i].Settings.Enabled = nil // nil = false (opt-in default)
		}
	}
	m.dirty = true
	item.dirty = true
	if item.isActive {
		item.syncState = syncPending
	}
	return m
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
		sb.WriteString(item.tree.view(m.focus == panelTree, height-1))
	} else if item.treeErr != "" {
		sb.WriteString(errorStyle.Render(fmt.Sprintf("error: %s", item.treeErr)))
		sb.WriteString("\n")
	} else {
		sb.WriteString(dimStyle.Render("(no content)"))
		sb.WriteString("\n")
	}

	return style.Render(sb.String())
}
