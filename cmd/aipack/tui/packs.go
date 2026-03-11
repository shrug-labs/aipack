package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shrug-labs/aipack/internal/app"
)

// packPanel tracks which column has focus in the Packs tab.
type packPanel int

const (
	packPanelList    packPanel = iota // left: unified pack list (registry + installed)
	packPanelDetails                  // middle: pack details + content
	packPanelContent                  // right: content browser (navigable)
)

type packItemDetail struct {
	entry     app.PackShowEntry
	fileSizes map[string]int64
	sizeState asyncState
}

type asyncState int

const (
	asyncPending asyncState = iota
	asyncLoading
	asyncLoaded
	asyncError
)

// contentItem represents a navigable item in the pack detail focus mode.
type contentItem struct {
	category string // one of Cat* constants
	id       string
	isHeader bool // true for category separators
}

// registryItem represents a pack in the registry panel.
type registryItem struct {
	name        string
	description string
	repo        string
	path        string
	ref         string
	owner       string
	installed   bool // true if an installed pack matches this name
}

// packListItem is a unified entry in the left panel: either from registry, installed, or both.
type packListItem struct {
	name        string
	installed   bool
	inRegistry  bool
	description string // from registry
	owner       string // from registry
	repo        string // from registry
	ref         string // from registry
	regPath     string // from registry (subdirectory path)
}

type packsModel struct {
	items        []packItemDetail // installed packs (from disk)
	installedMap map[string]int   // name → index into items
	configDir    string
	loadErr      string // non-empty if initial pack load failed
	width        int
	height       int
	focus        packPanel

	// Unified left-panel list.
	listItems  []packListItem
	listCursor int
	listOffset int // scroll offset for list panel

	// Content panel (right column).
	contentItems  []contentItem
	contentCursor int
	contentOffset int // scroll offset for content panel

	// Registry state.
	registry      []registryItem
	registryErr   string
	registryState asyncState
}

func newPacksModel(configDir string) packsModel {
	return packsModel{
		configDir:    configDir,
		installedMap: map[string]int{},
	}
}

func (m packsModel) currentListItem() *packListItem {
	if len(m.listItems) == 0 || m.listCursor < 0 || m.listCursor >= len(m.listItems) {
		return nil
	}
	return &m.listItems[m.listCursor]
}

// currentItem returns the installed pack detail for the selected list item, or nil.
func (m packsModel) currentItem() *packItemDetail {
	li := m.currentListItem()
	if li == nil || !li.installed {
		return nil
	}
	if idx, ok := m.installedMap[li.name]; ok {
		return &m.items[idx]
	}
	return nil
}

func (m packsModel) Init() tea.Cmd {
	return tea.Batch(loadPacks(m.configDir), loadRegistry(m.configDir))
}

func (m packsModel) Update(msg tea.Msg) (packsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case packsLoadedMsg:
		if msg.err != nil {
			m.loadErr = fmt.Sprintf("load packs: %v", msg.err)
			return m, nil
		}
		m.items = msg.items
		m.installedMap = map[string]int{}
		for i, item := range m.items {
			m.installedMap[item.entry.Name] = i
		}
		m.rebuildList()

		// Kick off file size computation for all packs.
		var cmds []tea.Cmd
		for i := range m.items {
			m.items[i].sizeState = asyncLoading
			cmds = append(cmds, computePackSizes(m.items[i].entry))
		}
		return m, tea.Batch(cmds...)

	case packSizesMsg:
		if idx, ok := m.installedMap[msg.packName]; ok {
			m.items[idx].fileSizes = msg.sizes
			m.items[idx].sizeState = asyncLoaded
		}
		return m, nil

	case registryLoadedMsg:
		if msg.err != nil {
			m.registryErr = msg.err.Error()
			m.registryState = asyncError
			return m, nil
		}
		m.registry = msg.items
		m.registryState = asyncLoaded
		m.rebuildList()
		return m, nil

	case tea.KeyMsg:
		switch m.focus {
		case packPanelList:
			return m.updateListPanel(msg)
		case packPanelDetails:
			return m.updateDetailsPanel(msg)
		case packPanelContent:
			return m.updateContentPanel(msg)
		}
	}
	return m, nil
}

// rebuildList merges registry and installed packs into a unified list.
// Registry items come first (sorted), then any installed-only packs not in registry.
func (m *packsModel) rebuildList() {
	seen := map[string]bool{}
	var list []packListItem

	// Registry items first.
	for _, ri := range m.registry {
		_, isInstalled := m.installedMap[ri.name]
		list = append(list, packListItem{
			name:        ri.name,
			installed:   isInstalled,
			inRegistry:  true,
			description: ri.description,
			owner:       ri.owner,
			repo:        ri.repo,
			ref:         ri.ref,
			regPath:     ri.path,
		})
		seen[ri.name] = true
	}

	// Installed packs not in registry.
	for _, item := range m.items {
		if !seen[item.entry.Name] {
			list = append(list, packListItem{
				name:      item.entry.Name,
				installed: true,
			})
		}
	}

	m.listItems = list
	if m.listCursor >= len(m.listItems) {
		m.listCursor = max(0, len(m.listItems)-1)
	}
	m.buildContentForCurrent()
}

func (m packsModel) updateListPanel(msg tea.KeyMsg) (packsModel, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.listCursor < len(m.listItems)-1 {
			m.listCursor++
			m.clampListOffset()
			m.buildContentForCurrent()
		}
	case "k", "up":
		if m.listCursor > 0 {
			m.listCursor--
			m.clampListOffset()
			m.buildContentForCurrent()
		}
	case "enter", "right", "l":
		li := m.currentListItem()
		if li != nil && li.installed && len(m.contentItems) > 0 {
			m.focus = packPanelContent
			m.contentCursor = 0
			if m.contentItems[0].isHeader && len(m.contentItems) > 1 {
				m.contentCursor = 1
			}
		}
	}
	return m, nil
}

func (m packsModel) updateDetailsPanel(msg tea.KeyMsg) (packsModel, tea.Cmd) {
	// Details panel is non-interactive (display only), redirect to list.
	switch msg.String() {
	case "esc", "left", "h":
		m.focus = packPanelList
	}
	return m, nil
}

func (m packsModel) updateContentPanel(msg tea.KeyMsg) (packsModel, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		for i := m.contentCursor + 1; i < len(m.contentItems); i++ {
			if !m.contentItems[i].isHeader {
				m.contentCursor = i
				m.clampContentOffset()
				break
			}
		}
	case "k", "up":
		for i := m.contentCursor - 1; i >= 0; i-- {
			if !m.contentItems[i].isHeader {
				m.contentCursor = i
				m.clampContentOffset()
				break
			}
		}
	case "enter":
		if m.contentCursor >= 0 && m.contentCursor < len(m.contentItems) {
			ci := m.contentItems[m.contentCursor]
			if !ci.isHeader {
				fp := m.contentFilePath(ci)
				if fp != "" {
					item := m.currentItem()
					if item != nil {
						return m, func() tea.Msg {
							return previewRequestMsg{
								title:    ci.id,
								category: ci.category,
								packName: item.entry.Name,
								filePath: fp,
							}
						}
					}
				}
			}
		}
	case "esc", "left", "h":
		m.focus = packPanelList
	}
	return m, nil
}

// buildContentForCurrent rebuilds the content item list for the currently selected pack.
func (m *packsModel) buildContentForCurrent() {
	item := m.currentItem()
	if item == nil {
		m.contentItems = nil
		m.contentCursor = 0
		m.contentOffset = 0
		return
	}
	m.contentItems = buildContentItemsFromEntry(item.entry)
	m.contentCursor = 0
	m.contentOffset = 0
}

// visibleContentH returns the number of visible content lines (excluding header + scroll indicator).
func (m packsModel) visibleContentH() int {
	h := m.height - 2
	if h < 1 {
		return 1
	}
	return h
}

// clampOffset adjusts offset so that cursor is within a visible window of size vis.
func clampOffset(cursor, offset, vis int) int {
	if cursor < offset {
		return cursor
	}
	if cursor >= offset+vis {
		return cursor - vis + 1
	}
	return offset
}

// writeScrollWindow writes the visible slice of lines to sb with a "↓ N more" indicator.
func writeScrollWindow(sb *strings.Builder, lines []string, offset, visibleH int) {
	start := min(offset, len(lines))
	end := min(start+visibleH, len(lines))
	for _, line := range lines[start:end] {
		sb.WriteString(line + "\n")
	}
	if end < len(lines) {
		sb.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more", len(lines)-end)))
	}
}

// clampContentOffset ensures the content cursor is within the visible scroll window.
func (m *packsModel) clampContentOffset() {
	m.contentOffset = clampOffset(m.contentCursor, m.contentOffset, m.visibleContentH())
}

// clampListOffset ensures the list cursor is within the visible scroll window.
func (m *packsModel) clampListOffset() {
	m.listOffset = clampOffset(m.listCursor, m.listOffset, m.visibleContentH())
}

// buildContentItems creates a flat navigable list of content items for a pack.
func (m packsModel) buildContentItems(idx int) []contentItem {
	if idx < 0 || idx >= len(m.items) {
		return nil
	}
	return buildContentItemsFromEntry(m.items[idx].entry)
}

func buildContentItemsFromEntry(entry app.PackShowEntry) []contentItem {
	var items []contentItem

	addCategory := func(category, label string, ids []string) {
		if len(ids) == 0 {
			return
		}
		items = append(items, contentItem{category: category, isHeader: true, id: label})
		for _, id := range ids {
			items = append(items, contentItem{category: category, id: id})
		}
	}

	addCategory(CatRules, categoryLabel(CatRules), entry.Rules)
	addCategory(CatAgents, categoryLabel(CatAgents), entry.Agents)
	addCategory(CatWorkflows, categoryLabel(CatWorkflows), entry.Workflows)
	addCategory(CatSkills, categoryLabel(CatSkills), entry.Skills)
	addCategory(CatMCP, categoryLabel(CatMCP), entry.MCPServers)
	return items
}

// contentFilePath resolves the absolute file path for a content item.
func (m packsModel) contentFilePath(ci contentItem) string {
	item := m.currentItem()
	if item == nil {
		return ""
	}
	root := item.entry.Path
	if root == "" {
		return ""
	}
	return contentPath(root, ci.category, ci.id)
}

// --- View ---

func (m packsModel) View() string {
	if len(m.items) == 0 && m.loadErr == "" && m.registryState != asyncLoaded {
		return contentStyle.Render("Loading packs...")
	}

	// Three-column layout: pack list (25%) | details (35%) | content (40%).
	col1W := m.width * 25 / 100
	col1W = max(col1W, 22)
	col2W := m.width * 35 / 100
	col2W = max(col2W, 25)
	col3W := m.width - col1W - col2W - 8 // padding + separators
	col3W = max(col3W, 20)

	colH := max(m.height, 8)

	col1 := m.viewListPanel(col1W, colH)
	sep := dimStyle.Render(" │ ")
	col2 := m.viewDetailsPanel(col2W, colH)
	col3 := m.viewContentPanel(col3W, colH)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, col1, sep, col2, sep, col3)
	return contentStyle.Render(joined)
}

// viewListPanel renders the left column: unified pack list.
func (m packsModel) viewListPanel(width, height int) string {
	style := lipgloss.NewStyle().Width(width).MaxHeight(height)
	var sb strings.Builder

	sb.WriteString(panelHeaderStyle.Render("Packs") + "\n")

	if m.registryState == asyncError {
		sb.WriteString(dimStyle.Render("  registry: "+m.registryErr) + "\n")
	}

	if len(m.listItems) == 0 {
		sb.WriteString(dimStyle.Render("  (none)") + "\n")
		if m.registryState == asyncError || m.registryState == asyncLoaded {
			sb.WriteString(dimStyle.Render("  Run: aipack pack install <name>") + "\n")
		}
		return style.Render(sb.String())
	}

	// Build all lines, then slice to visible window.
	var lines []string
	focused := m.focus == packPanelList
	for i, li := range m.listItems {
		cursor := "  "
		if focused && i == m.listCursor {
			cursor = selectedStyle.Render("> ")
		}

		nameStyle := lipgloss.NewStyle()
		if focused && i == m.listCursor {
			nameStyle = selectedStyle
		} else if i == m.listCursor {
			nameStyle = lipgloss.NewStyle()
		} else {
			nameStyle = dimStyle
		}

		badge := ""
		if li.installed {
			badge = " " + lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("installed")
		}

		lines = append(lines, fmt.Sprintf("%s%s%s", cursor, nameStyle.Render(li.name), badge))
	}

	writeScrollWindow(&sb, lines, m.listOffset, max(height-2, 1))

	return style.Render(sb.String())
}

// viewDetailsPanel renders the middle column: details for the selected pack.
func (m packsModel) viewDetailsPanel(width, height int) string {
	style := lipgloss.NewStyle().Width(width).MaxHeight(height)
	li := m.currentListItem()
	if li == nil {
		return style.Render(dimStyle.Render("No pack selected"))
	}

	var sb strings.Builder

	sb.WriteString(panelHeaderStyle.Render(li.name) + "\n")

	// If installed, show installed pack metadata.
	if item := m.currentItem(); item != nil {
		if item.entry.Version != "" {
			fmt.Fprintf(&sb, "  Version:   %s\n", item.entry.Version)
		}
		if item.entry.Method != "" {
			fmt.Fprintf(&sb, "  Method:    %s\n", item.entry.Method)
		}
		if item.entry.Origin != "" {
			fmt.Fprintf(&sb, "  Origin:    %s\n", dimStyle.Render(shortPath(item.entry.Origin)))
		}
		if item.entry.InstalledAt != "" {
			fmt.Fprintf(&sb, "  Installed: %s\n", item.entry.InstalledAt)
		}
		if item.fileSizes != nil {
			if total, ok := item.fileSizes["total"]; ok && total > 0 {
				fmt.Fprintf(&sb, "  Size:      %s\n", fileSizeStyle.Render(formatSize(total)))
			}
		}

		// Content summary.
		summary := installedPackSummary(item.entry)
		if summary != "" {
			fmt.Fprintf(&sb, "  Content:   %s\n", dimStyle.Render(summary))
		}
	} else {
		// Not installed — show registry info.
		sb.WriteString(dimStyle.Render("  not installed") + "\n")
	}

	sb.WriteString("\n")

	// Registry metadata (shown for all registry items).
	if li.inRegistry {
		sb.WriteString(dimStyle.Render("  Registry:") + "\n")
		if li.description != "" {
			fmt.Fprintf(&sb, "    %s\n", li.description)
		}
		if li.repo != "" {
			fmt.Fprintf(&sb, "    Repo:  %s\n", dimStyle.Render(li.repo))
		}
		if li.regPath != "" {
			fmt.Fprintf(&sb, "    Path:  %s\n", dimStyle.Render(li.regPath))
		}
		if li.ref != "" {
			fmt.Fprintf(&sb, "    Ref:   %s\n", dimStyle.Render(li.ref))
		}
		if li.owner != "" {
			fmt.Fprintf(&sb, "    Owner: %s\n", dimStyle.Render(li.owner))
		}
	}

	return style.Render(sb.String())
}

// installedPackSummary returns a compact summary like "3 rules, 1 agent, 3 mcp".
func installedPackSummary(e app.PackShowEntry) string {
	var parts []string
	if n := len(e.Rules); n > 0 {
		parts = append(parts, fmt.Sprintf("%d rules", n))
	}
	if n := len(e.Agents); n > 0 {
		parts = append(parts, fmt.Sprintf("%d agents", n))
	}
	if n := len(e.Workflows); n > 0 {
		parts = append(parts, fmt.Sprintf("%d workflows", n))
	}
	if n := len(e.Skills); n > 0 {
		parts = append(parts, fmt.Sprintf("%d skills", n))
	}
	if n := len(e.MCPServers); n > 0 {
		parts = append(parts, fmt.Sprintf("%d mcp", n))
	}
	return strings.Join(parts, ", ")
}

// viewContentPanel renders the right column: content browser for installed packs.
func (m packsModel) viewContentPanel(width, height int) string {
	style := lipgloss.NewStyle().Width(width).MaxHeight(height)
	var sb strings.Builder

	sb.WriteString(panelHeaderStyle.Render("Content") + "\n")

	li := m.currentListItem()
	if li == nil || !li.installed {
		if li != nil && !li.installed {
			sb.WriteString(dimStyle.Render("  Install to browse content") + "\n")
		}
		return style.Render(sb.String())
	}

	item := m.currentItem()
	if len(m.contentItems) == 0 {
		sb.WriteString(dimStyle.Render("  (no content)") + "\n")
		return style.Render(sb.String())
	}

	// Render all lines, then slice to visible window.
	var lines []string
	focused := m.focus == packPanelContent
	for i, ci := range m.contentItems {
		if ci.isHeader {
			lines = append(lines, fmt.Sprintf("  %s:", ci.id))
			continue
		}

		cursor := "    "
		if focused && i == m.contentCursor {
			cursor = "  > "
		}

		label := ci.id
		if focused && i == m.contentCursor {
			label = selectedStyle.Render(label)
		}

		line := cursor + label
		if item != nil && item.fileSizes != nil {
			if sz, ok := item.fileSizes[ci.category+"/"+ci.id]; ok && sz >= 0 {
				line += "  " + fileSizeStyle.Render(formatSize(sz))
			}
		}
		lines = append(lines, line)
	}

	writeScrollWindow(&sb, lines, m.contentOffset, max(height-2, 1))

	return style.Render(sb.String())
}
