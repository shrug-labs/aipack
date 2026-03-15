package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
)

// packPanel tracks which column has focus in the Packs tab.
type packPanel int

const (
	packPanelList    packPanel = iota // left: unified pack list + pack info
	packPanelContent                  // middle: content browser (navigable)
	packPanelPreview                  // right: inline preview (scrollable)
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
	category domain.PackCategory // one of domain.Category* constants
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

	// Passive preview panel (right column).
	previewPath   string
	previewData   previewLoadedMsg
	previewState  asyncState
	previewOffset int

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
		if previewCmd := m.loadInlinePreview(); previewCmd != nil {
			cmds = append(cmds, previewCmd)
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
		return m, m.loadInlinePreview()

	case previewLoadedMsg:
		if msg.filePath != m.previewPath {
			return m, nil
		}
		m.previewData = msg
		if msg.err != nil {
			m.previewState = asyncError
		} else {
			m.previewState = asyncLoaded
		}
		return m, nil

	case tea.KeyMsg:
		switch m.focus {
		case packPanelList:
			return m.updateListPanel(msg)
		case packPanelContent:
			return m.updateContentPanel(msg)
		case packPanelPreview:
			return m.updatePreviewPanel(msg)
		}
	}
	return m, nil
}

// rebuildList merges registry and installed packs into a unified list.
func (m *packsModel) rebuildList() {
	seen := map[string]bool{}
	var list []packListItem

	// Registry items first.
	for _, ri := range m.registry {
		_, isInstalled := m.installedMap[ri.name]
		li := packListItem{
			name:        ri.name,
			installed:   isInstalled,
			inRegistry:  true,
			description: ri.description,
			owner:       ri.owner,
			repo:        ri.repo,
			ref:         ri.ref,
			regPath:     ri.path,
		}
		list = append(list, li)
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

	sort.Slice(list, func(i, j int) bool {
		if list[i].installed != list[j].installed {
			return list[i].installed
		}
		return strings.ToLower(list[i].name) < strings.ToLower(list[j].name)
	})

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
			return m, nil
		}
	case "k", "up":
		if m.listCursor > 0 {
			m.listCursor--
			m.clampListOffset()
			m.buildContentForCurrent()
			return m, nil
		}
	case "enter", "right", "l":
		li := m.currentListItem()
		if li != nil && li.installed && len(m.contentItems) > 0 {
			m.focus = packPanelContent
			return m, m.loadInlinePreview()
		}
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
				return m, m.loadInlinePreview()
			}
		}
	case "k", "up":
		for i := m.contentCursor - 1; i >= 0; i-- {
			if !m.contentItems[i].isHeader {
				m.contentCursor = i
				m.clampContentOffset()
				return m, m.loadInlinePreview()
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
	case "right", "l":
		if m.previewState == asyncLoaded {
			m.focus = packPanelPreview
		}
	case "esc", "left", "h":
		m.focus = packPanelList
	}
	return m, nil
}

func (m packsModel) updatePreviewPanel(msg tea.KeyMsg) (packsModel, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		maxOffset := max(0, len(buildInlinePreviewLines(m.previewData, m.previewWidth()))-m.visiblePreviewH())
		if m.previewOffset < maxOffset {
			m.previewOffset++
		}
	case "k", "up":
		if m.previewOffset > 0 {
			m.previewOffset--
		}
	case "esc", "left", "h":
		m.focus = packPanelContent
	case "enter":
		ci := m.currentContentItem()
		item := m.currentItem()
		if ci != nil && item != nil {
			fp := m.contentFilePath(*ci)
			if fp != "" {
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
	return m, nil
}

// buildContentForCurrent rebuilds the content item list for the currently selected pack.
func (m *packsModel) buildContentForCurrent() {
	item := m.currentItem()
	if item == nil {
		m.contentItems = nil
		m.contentCursor = 0
		m.contentOffset = 0
		m.previewPath = ""
		m.previewData = previewLoadedMsg{}
		m.previewState = asyncPending
		m.previewOffset = 0
		return
	}
	m.contentItems = buildContentItemsFromEntry(item.entry)
	m.contentCursor = firstContentCursor(m.contentItems)
	m.contentOffset = 0
}

func firstContentCursor(items []contentItem) int {
	for i, ci := range items {
		if !ci.isHeader {
			return i
		}
	}
	return 0
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

	addCategory := func(category domain.PackCategory, ids []string) {
		if len(ids) == 0 {
			return
		}
		items = append(items, contentItem{category: category, isHeader: true, id: category.Label()})
		for _, id := range ids {
			items = append(items, contentItem{category: category, id: id})
		}
	}

	for _, cat := range domain.AllPackCategories() {
		addCategory(cat, entry.ContentIDs(cat))
	}
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

func (m packsModel) currentContentItem() *contentItem {
	if len(m.contentItems) == 0 || m.contentCursor < 0 || m.contentCursor >= len(m.contentItems) {
		return nil
	}
	ci := m.contentItems[m.contentCursor]
	if ci.isHeader {
		return nil
	}
	return &ci
}

func (m *packsModel) loadInlinePreview() tea.Cmd {
	if m.focus == packPanelList {
		m.previewPath = ""
		m.previewData = previewLoadedMsg{}
		m.previewState = asyncPending
		m.previewOffset = 0
		return nil
	}
	ci := m.currentContentItem()
	item := m.currentItem()
	if ci == nil || item == nil {
		m.previewPath = ""
		m.previewData = previewLoadedMsg{}
		m.previewState = asyncPending
		return nil
	}
	filePath := m.contentFilePath(*ci)
	if filePath == "" {
		m.previewPath = ""
		m.previewData = previewLoadedMsg{}
		m.previewState = asyncPending
		return nil
	}
	m.previewPath = filePath
	m.previewData = previewLoadedMsg{}
	m.previewState = asyncLoading
	m.previewOffset = 0
	return loadPreview(ci.id, ci.category, item.entry.Name, filePath)
}

// --- View ---

func (m packsModel) View() string {
	if len(m.items) == 0 && m.loadErr == "" && m.registryState != asyncLoaded {
		return contentStyle.Render("Loading packs...")
	}

	innerW := max(m.width-4, 20)
	innerH := max(m.height-2, 8)
	sepW := 5

	// Three-column layout: left stack | content | preview.
	col1W := innerW * 28 / 100
	col1W = max(col1W, 30)
	col2W := innerW * 31 / 100
	col2W = max(col2W, 34)
	col3W := innerW - col1W - col2W - (sepW * 2)
	col3W = max(col3W, 32)
	if col1W+col2W+col3W+(sepW*2) > innerW {
		col3W = max(innerW-col1W-col2W-(sepW*2), 24)
	}

	colH := innerH

	col1 := m.viewListAndInfoPanel(col1W, colH)
	col2 := m.viewContentPanel(col2W, colH)
	col3 := m.viewPreviewPanel(col3W, colH)
	sep := verticalSeparator(colH, sepW)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, col1, sep, col2, sep, col3)
	return contentStyle.Render(joined)
}

func (m packsModel) viewListAndInfoPanel(width, height int) string {
	listH := height * 52 / 100
	if listH < 8 {
		listH = 8
	}
	if listH > height-6 {
		listH = max(height-6, 4)
	}
	sepH := 1
	infoH := max(height-listH-sepH, 6)
	if listH+sepH+infoH > height {
		infoH = max(height-listH-sepH, 4)
	}

	list := m.viewListPanel(width, listH)
	sepW := width
	if sepW > 80 {
		sepW = 80
	}
	sep := dimStyle.Render(strings.Repeat("─", sepW))
	info := m.viewPackInfoPanel(width, infoH)
	return lipgloss.JoinVertical(lipgloss.Left, list, sep, info)
}

// viewListPanel renders the top-left pack list.
func (m packsModel) viewListPanel(width, height int) string {
	var sb strings.Builder

	sb.WriteString(renderPanelHeader("Packs", m.focus == packPanelList) + "\n")
	sb.WriteString(panelSubtleStyle.Render(fmt.Sprintf("%d installed, %d total", len(m.items), len(m.listItems))) + "\n\n")

	if m.registryState == asyncError {
		sb.WriteString(errorStyle.Render("Registry: "+m.registryErr) + "\n")
	}

	if len(m.listItems) == 0 {
		sb.WriteString(dimStyle.Render("(none)") + "\n")
		if m.registryState == asyncError || m.registryState == asyncLoaded {
			sb.WriteString(dimStyle.Render("Run: aipack pack install <name>") + "\n")
		}
		return renderPackPanel(width, height, m.focus == packPanelList, sb.String())
	}

	var lines []string
	focused := m.focus == packPanelList
	for i, li := range m.listItems {
		cursor := "  "
		if focused && i == m.listCursor {
			cursor = selectedStyle.Render("▸ ")
		}

		var nameStyle lipgloss.Style
		if i == m.listCursor {
			nameStyle = selectedStyle
		} else if li.installed {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		} else {
			nameStyle = dimStyle
		}

		lines = append(lines, cursor+nameStyle.Render(li.name))
	}

	writeScrollWindow(&sb, lines, m.listOffset, max(height-4, 1))

	return renderPackPanel(width, height, m.focus == packPanelList, sb.String())
}

// viewPackInfoPanel renders the lower-left pack info block.
// The registry section is pinned to the bottom of the panel.
func (m packsModel) viewPackInfoPanel(width, height int) string {
	li := m.currentListItem()
	if li == nil {
		return renderPackPanel(width, height, false, dimStyle.Render("No pack selected"))
	}

	innerW := max(width-4, 20)
	labelW := 12
	registryStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("114"))

	// Top section: pack details.
	var top strings.Builder
	top.WriteString(selectedStyle.Render(li.name) + "\n")

	if item := m.currentItem(); item != nil {
		if item.entry.Version != "" {
			top.WriteString(infoField("Version", "v"+item.entry.Version, labelW) + "\n")
		}
		if item.entry.Method != "" {
			name, style := methodDisplay(item.entry.Method)
			top.WriteString(infoField("Method", style.Render(name), labelW) + "\n")
		}
		if item.fileSizes != nil {
			if total, ok := item.fileSizes["total"]; ok && total > 0 {
				top.WriteString(infoField("Size", formatSize(total), labelW) + "\n")
			}
		}
		if installedAt := formatInstallDate(item.entry.InstalledAt); installedAt != "" {
			top.WriteString(infoField("Installed", installedAt, labelW) + "\n")
		}
		src := sourceForMethod(item.entry.Method, item.entry.Origin, item.entry.Path)
		if item.entry.Method == "link" || item.entry.Method == "copy" || item.entry.Method == "local" {
			_, style := methodDisplay(item.entry.Method)
			src = style.Render(src)
		}
		top.WriteString(infoField("Source", src, labelW) + "\n")
	} else {
		top.WriteString(dimStyle.Render("Not installed locally.") + "\n")
	}

	if !li.inRegistry {
		return renderPackPanel(width, height, false, top.String())
	}

	// Bottom section: registry, pinned to panel bottom.
	// Build fields first (without description), then compute how many
	// lines are left for the description to fill.
	var bot strings.Builder
	bot.WriteString(registryStyle.Render("Registry") + "\n")
	bot.WriteString(infoField("Name", li.name, labelW) + "\n")
	if li.owner != "" {
		bot.WriteString(infoField("Owner", li.owner, labelW) + "\n")
	}
	if li.repo != "" {
		bot.WriteString(infoField("Repo", repoBaseName(li.repo, li.regPath), labelW) + "\n")
	}
	if li.ref != "" {
		bot.WriteString(infoField("Ref", li.ref, labelW) + "\n")
	}

	topLines := strings.Count(top.String(), "\n")
	botLines := strings.Count(bot.String(), "\n")

	// Determine how many lines the description can use.
	if li.description != "" {
		descW := max(innerW-labelW, 10)
		available := height - topLines - botLines - 2 // -2 for gap + about line
		descMaxLines := max(min(available, 3), 1)
		desc := wrapAndTruncate(strings.TrimSpace(li.description), descW, descMaxLines)
		bot.WriteString(infoField("About", desc, labelW) + "\n")
		botLines += strings.Count(desc, "\n") + 1
	}

	// Horizontal rule between pack details and registry, with the header
	// sitting just below the rule after a small gap.
	rule := dimStyle.Render(strings.Repeat("─", innerW))
	gap := height - topLines - botLines - 2 // -2 for rule + gap line
	if gap < 1 {
		gap = 1
	}

	return renderPackPanel(width, height, false, top.String()+strings.Repeat("\n", gap)+rule+"\n"+bot.String())
}

// wrapAndTruncate wraps text to the given width and truncates after maxLines.
func wrapAndTruncate(text string, width, maxLines int) string {
	if width < 8 {
		width = 8
	}
	if maxLines < 1 {
		maxLines = 1
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}

	var lines []string
	current := words[0]
	for _, word := range words[1:] {
		next := current + " " + word
		if lipgloss.Width(next) <= width {
			current = next
			continue
		}
		lines = append(lines, current)
		current = word
		if len(lines) == maxLines {
			lines[maxLines-1] += "..."
			return strings.Join(lines, "\n"+strings.Repeat(" ", 12))
		}
	}
	lines = append(lines, current)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] += "..."
	}
	return strings.Join(lines, "\n"+strings.Repeat(" ", 12))
}

// sourceForMethod returns the Source value for the details pane.
// link/copy show the origin path; clone/archive show the installed path on disk.
func sourceForMethod(method, origin, installPath string) string {
	switch method {
	case "link", "copy", "local":
		if origin != "" {
			return shortPath(origin)
		}
	case "clone", "archive":
		if installPath != "" {
			return shortPath(installPath)
		}
	}
	if installPath != "" {
		return shortPath(installPath)
	}
	if origin != "" {
		return shortPath(origin)
	}
	return dimStyle.Render("(unknown)")
}

// methodDisplay maps a raw install method to a user-facing display name and style.
func methodDisplay(method string) (string, lipgloss.Style) {
	switch method {
	case "link":
		return "link", lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	case "copy", "local":
		return "local", lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	case "clone", "archive":
		return "remote", lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	default:
		return method, dimStyle
	}
}

// repoBaseName extracts the repository name from a URL, stripping the .git
// suffix. If regPath is non-empty, it is appended as a subdirectory.
func repoBaseName(rawURL, regPath string) string {
	if rawURL == "" {
		return ""
	}
	// Take the last path segment.
	name := rawURL
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	// Strip .git suffix.
	name = strings.TrimSuffix(name, ".git")
	if name == "" {
		return ""
	}
	if regPath != "" {
		return name + "/" + regPath
	}
	return name
}

// infoField renders a labeled key-value pair with aligned columns.
func infoField(label, value string, labelWidth int) string {
	padded := label + strings.Repeat(" ", max(labelWidth-len(label), 1))
	return dimStyle.Render(padded) + value
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

// viewContentPanel renders the middle column: content browser for installed packs.
func (m packsModel) viewContentPanel(width, height int) string {
	var sb strings.Builder
	innerW := panelInnerWidth(width)

	sb.WriteString(renderPanelHeader("Content", m.focus == packPanelContent) + "\n")

	li := m.currentListItem()
	if li == nil || !li.installed {
		if li != nil && !li.installed {
			sb.WriteString(dimStyle.Render("Install to browse content") + "\n")
		}
		return renderPackPanel(width, height, m.focus == packPanelContent, sb.String())
	}

	item := m.currentItem()
	if len(m.contentItems) == 0 {
		sb.WriteString(dimStyle.Render("(no content)") + "\n")
		return renderPackPanel(width, height, m.focus == packPanelContent, sb.String())
	}

	sb.WriteString(contentSummaryStyle.Render(truncateText(installedPackSummary(item.entry), innerW)) + "\n\n")

	// Build category stats and alignment widths, then render lines.
	type catStats struct {
		count   int
		total   int64
		hasSize bool
	}
	stats := map[domain.PackCategory]*catStats{}
	maxLabelW := 0
	maxSizeW := 0
	for _, ci := range m.contentItems {
		if ci.isHeader {
			continue
		}
		cs := stats[ci.category]
		if cs == nil {
			cs = &catStats{}
			stats[ci.category] = cs
		}
		cs.count++
		if len(ci.id) > maxLabelW {
			maxLabelW = len(ci.id)
		}
		if item != nil && item.fileSizes != nil {
			if sz, ok := item.fileSizes[ci.category.DirName()+"/"+ci.id]; ok && sz >= 0 {
				cs.total += sz
				cs.hasSize = true
				if w := len(formatSize(sz)); w > maxSizeW {
					maxSizeW = w
				}
			}
		}
	}

	var lines []string
	focused := m.focus == packPanelContent
	seenCategory := false
	for i, ci := range m.contentItems {
		if ci.isHeader {
			cs := stats[ci.category]
			if seenCategory {
				lines = append(lines, "")
			}
			line := ci.id
			if cs != nil {
				line += fmt.Sprintf(" (%d)", cs.count)
				if cs.hasSize {
					line = alignWithSize(line, formatSize(cs.total), innerW)
				}
			}
			lines = append(lines, categoryHeaderStyle.Render(line))
			seenCategory = true
			continue
		}

		cursor := "  "
		if focused && i == m.contentCursor {
			cursor = selectedStyle.Render("▸ ")
		}

		label := ci.id
		if i == m.contentCursor {
			label = selectedStyle.Render(label)
		}

		line := cursor + label
		if item != nil && item.fileSizes != nil {
			if sz, ok := item.fileSizes[ci.category.DirName()+"/"+ci.id]; ok && sz >= 0 {
				line = alignWithSize(line, formatSize(sz), innerW)
			}
		}
		lines = append(lines, line)
	}

	writeScrollWindow(&sb, lines, m.contentOffset, max(height-4, 1))

	return renderPackPanel(width, height, m.focus == packPanelContent, sb.String())
}

func (m packsModel) viewPreviewPanel(width, height int) string {
	var sb strings.Builder

	sb.WriteString(renderPanelHeader("Preview", m.focus == packPanelPreview) + "\n")

	li := m.currentListItem()
	if li == nil || !li.installed {
		if li != nil && !li.installed {
			sb.WriteString(dimStyle.Render("Install to preview content") + "\n")
		}
		return renderPackPanel(width, height, m.focus == packPanelPreview, sb.String())
	}
	if len(m.contentItems) == 0 {
		sb.WriteString(dimStyle.Render("(no preview)") + "\n")
		return renderPackPanel(width, height, m.focus == packPanelPreview, sb.String())
	}

	switch m.previewState {
	case asyncLoading:
		sb.WriteString(dimStyle.Render("Preparing preview...") + "\n")
		return renderPackPanel(width, height, m.focus == packPanelPreview, sb.String())
	case asyncError:
		errText := "preview unavailable"
		if m.previewData.err != nil {
			errText = m.previewData.err.Error()
		}
		sb.WriteString(errorStyle.Render("Error: "+errText) + "\n")
		return renderPackPanel(width, height, m.focus == packPanelPreview, sb.String())
	case asyncPending:
		if m.focus == packPanelList {
			sb.WriteString(dimStyle.Render("Open content to load a preview") + "\n")
		} else {
			sb.WriteString(dimStyle.Render("Select content to preview") + "\n")
		}
		return renderPackPanel(width, height, m.focus == packPanelPreview, sb.String())
	}

	lines := buildInlinePreviewLines(m.previewData, width)
	writeScrollWindow(&sb, lines, m.previewOffset, max(height-2, 1))
	return renderPackPanel(width, height, m.focus == packPanelPreview, sb.String())
}

func (m packsModel) visiblePreviewH() int {
	colH := max(m.height-2, 8)
	return max(colH-2, 1)
}

func (m packsModel) previewWidth() int {
	innerW := max(m.width-4, 20)
	sepW := 5
	col1W := max(innerW*28/100, 30)
	col2W := max(innerW*31/100, 34)
	col3W := innerW - col1W - col2W - (sepW * 2)
	col3W = max(col3W, 32)
	if col1W+col2W+col3W+(sepW*2) > innerW {
		col3W = max(innerW-col1W-col2W-(sepW*2), 24)
	}
	return col3W
}

func buildInlinePreviewLines(msg previewLoadedMsg, width int) []string {
	contentW := panelInnerWidth(width)
	ruleW := contentW
	if ruleW < 10 {
		ruleW = 10
	}
	if ruleW > 80 {
		ruleW = 80
	}

	lines := []string{
		previewTitleStyle.Render(msg.category.SingularLabel() + "  " + msg.title),
		dimStyle.Render(shortPath(msg.filePath)),
		strings.Repeat("─", ruleW),
	}

	if len(msg.frontmatter) > 0 {
		lines = append(lines, "")
		for _, e := range msg.frontmatter {
			lines = append(lines, previewKeyStyle.Render(e.key+":")+" "+e.value)
		}
		lines = append(lines, "", strings.Repeat("─", ruleW))
	}

	body := strings.TrimRight(msg.body, "\n")
	if body == "" && len(msg.frontmatter) == 0 {
		lines = append(lines, "", dimStyle.Render("(empty)"))
		return lines
	}
	if body != "" {
		lines = append(lines, "")
		lines = append(lines, strings.Split(body, "\n")...)
	}
	return truncateLines(lines, contentW)
}

func renderPackPanel(width, height int, focused bool, content string) string {
	_ = focused
	content = fitPanelContent(content, panelInnerWidth(width), panelInnerHeight(height))
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(content)
}

func verticalSeparator(height, width int) string {
	if height < 1 {
		height = 1
	}
	if width < 1 {
		width = 1
	}
	lines := make([]string, height)
	for i := range lines {
		if width == 1 {
			lines[i] = dimStyle.Render("│")
			continue
		}
		lines[i] = strings.Repeat(" ", width/2) + dimStyle.Render("│") + strings.Repeat(" ", width-width/2-1)
	}
	return strings.Join(lines, "\n")
}

func renderPanelHeader(title string, _ bool) string {
	return panelHeaderStyle.Render(title)
}

func panelInnerWidth(width int) int {
	return max(width, 8)
}

func panelInnerHeight(height int) int {
	return max(height, 1)
}

func fitPanelContent(content string, width, height int) string {
	_ = width
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func truncateLines(lines []string, width int) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, truncateText(line, width))
	}
	return out
}

func alignWithSize(left, size string, width int) string {
	leftWidth := lipgloss.Width(left)
	sizeWidth := lipgloss.Width(size)
	const trailingPad = 3
	if leftWidth+sizeWidth+2+trailingPad >= width {
		left = truncateText(left, max(width-sizeWidth-2-trailingPad, 4))
		leftWidth = lipgloss.Width(left)
	}
	gap := max(width-leftWidth-sizeWidth-trailingPad, 2)
	return left + strings.Repeat(" ", gap) + fileSizeStyle.Render(size) + strings.Repeat(" ", trailingPad)
}

func truncateText(s string, width int) string {
	if width < 4 || lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	return string(runes[:max(width-1, 1)]) + "…"
}

func formatInstallDate(raw string) string {
	if raw == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return ""
	}
	return t.Format("2006-01-02")
}
