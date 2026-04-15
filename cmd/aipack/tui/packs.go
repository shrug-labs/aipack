package tui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/source"
	"github.com/shrug-labs/aipack/internal/util"
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

	// Versions cache. Lazily populated by loadPackVersions when the cursor
	// lands on an eligible pack (clone-method or registry-only). Drives the
	// inline version display in the details panel and warms the cache for
	// the install-time version picker. Keyed by pack name.
	versionsCache map[string]packVersionsCacheEntry

	// versionsDebounceEpoch is incremented on every cursor move that would
	// schedule a version load. A deferred tea.Tick delivers versionsDebounceMsg
	// with the epoch value captured at schedule time; the handler only fires
	// the load when the incoming epoch still matches. Holding j/k collapses
	// to a single load on the pack where the cursor settles.
	versionsDebounceEpoch int

	// Drift set. Populated once by loadPackDrift when the packs tab
	// initializes. The parent rootModel clears per-pack entries on
	// successful install/update of that pack, but the set is never
	// re-scanned in-session — a TUI restart re-runs the network probe.
	// Drives the drift indicator on list rows. Empty set means "no
	// drift detected" OR "scan hasn't run yet" — those cases render
	// identically (no indicator). Keyed by pack name; value is the
	// recorded drift entry for tooltip-style hover or future surfacing.
	driftSet map[string]app.PackDrift
}

// packVersionsCacheEntry holds one pack's version-discovery state. The state
// distinguishes "we haven't asked yet" (no entry in the map) from "we asked
// and got nothing" (entry with empty Versions and asyncLoaded), so the UI can
// show "loading…" vs "no semver tags" vs "lookup failed" without ambiguity.
type packVersionsCacheEntry struct {
	state    asyncState
	versions []app.PackVersion
	err      string
}

func newPacksModel(configDir string) packsModel {
	return packsModel{
		configDir:     configDir,
		installedMap:  map[string]int{},
		versionsCache: map[string]packVersionsCacheEntry{},
		driftSet:      map[string]app.PackDrift{},
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

// versionsCacheEntry returns the cached version-discovery result for a pack,
// or the zero value when not yet queried. The bool reports whether an entry
// exists at all (distinguishing "not asked" from "asked and got empty").
func (m packsModel) versionsCacheEntry(name string) (packVersionsCacheEntry, bool) {
	if m.versionsCache == nil {
		return packVersionsCacheEntry{}, false
	}
	e, ok := m.versionsCache[name]
	return e, ok
}

// versionsEligible reports whether the current list item is one we can query
// remote tags for. Clone-method installed packs and registry-only packs both
// qualify; link/copy/local installs and packs with no registry record have
// no remote git origin to query.
func (m packsModel) versionsEligible(li *packListItem) bool {
	if li == nil {
		return false
	}
	if li.installed {
		if item := m.currentItem(); item != nil {
			return item.entry.Method == config.MethodClone
		}
		return false
	}
	return li.inRegistry
}

// versionsDebounceDelay is the idle window the packs tab waits before
// resolving a queued version load. Holding j/k through a 20-pack registry
// collapses to a single ls-remote for the pack under the cursor when it
// settles, instead of 20 concurrent subprocesses per keystroke burst.
const versionsDebounceDelay = 300 * time.Millisecond

// versionLoadCandidate returns the current list item iff it's eligible for
// a version load AND has no cached entry yet. Both the immediate and
// debounced load paths share this precondition, so they funnel through
// here. Refresh is deliberately different — it wants to proceed even on a
// cache hit — and reaches for versionsEligible directly.
func (m *packsModel) versionLoadCandidate() *packListItem {
	li := m.currentListItem()
	if !m.versionsEligible(li) {
		return nil
	}
	if _, ok := m.versionsCacheEntry(li.name); ok {
		return nil
	}
	return li
}

// maybeLoadVersionsForCursor returns a tea.Cmd that asynchronously fetches
// available semver tags for the currently selected pack — but only when the
// pack is eligible (clone or registry) and not already cached or in flight.
// Returns nil when there's nothing to do, so callers can safely chain it
// into tea.Batch without conditional branching.
//
// This is the *immediate* load path, used by one-shot callers (initial
// registry load, explicit refresh). Cursor-move paths must go through
// scheduleVersionsLoad to benefit from debouncing.
func (m *packsModel) maybeLoadVersionsForCursor() tea.Cmd {
	li := m.versionLoadCandidate()
	if li == nil {
		return nil
	}
	if m.versionsCache == nil {
		m.versionsCache = map[string]packVersionsCacheEntry{}
	}
	m.versionsCache[li.name] = packVersionsCacheEntry{state: asyncLoading}
	return loadPackVersions(context.Background(), m.configDir, li.name)
}

// scheduleVersionsLoad debounces version loads across cursor-move bursts:
// the caller bumps the epoch and a tea.Tick later delivers the captured
// epoch back as versionsDebounceMsg, which only fires the load if no newer
// cursor move has intervened. Without this, holding j/k spawned a 30s git
// ls-remote per keystroke — blocking on pinentry for users with locked SSH
// keys. Returns nil (emits no command at all) when the cursor target is
// ineligible or already cached.
func (m *packsModel) scheduleVersionsLoad() tea.Cmd {
	if m.versionLoadCandidate() == nil {
		return nil
	}
	m.versionsDebounceEpoch++
	epoch := m.versionsDebounceEpoch
	return tea.Tick(versionsDebounceDelay, func(_ time.Time) tea.Msg {
		return versionsDebounceMsg{epoch: epoch}
	})
}

// refreshVersionsForCursor drops any cached version entry for the current
// pack and kicks off an immediate (non-debounced) reload. Bound to the 'r'
// key so users can force a re-check after fixing credentials or pushing a
// new tag upstream, without leaving the packs tab.
func (m *packsModel) refreshVersionsForCursor() tea.Cmd {
	li := m.currentListItem()
	if !m.versionsEligible(li) {
		return nil
	}
	if m.versionsCache != nil {
		delete(m.versionsCache, li.name)
	}
	return m.maybeLoadVersionsForCursor()
}

func (m packsModel) Init() tea.Cmd {
	return tea.Batch(
		loadPacks(m.configDir),
		loadRegistry(m.configDir),
		loadPackDrift(context.Background(), m.configDir),
	)
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
		if m.versionsCache == nil {
			m.versionsCache = map[string]packVersionsCacheEntry{}
		}
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
		// Warm versions for the initial selection (usually the first
		// registry pack on a fresh tab open). Subsequent cursor moves
		// trigger their own loads via updateListPanel.
		var cmds []tea.Cmd
		if previewCmd := m.loadInlinePreview(); previewCmd != nil {
			cmds = append(cmds, previewCmd)
		}
		if vcmd := m.maybeLoadVersionsForCursor(); vcmd != nil {
			cmds = append(cmds, vcmd)
		}
		return m, tea.Batch(cmds...)

	case packVersionsLoadedMsg:
		entry := packVersionsCacheEntry{
			state:    asyncLoaded,
			versions: msg.versions,
		}
		if msg.err != nil {
			entry.state = asyncError
			entry.err = msg.err.Error()
		}
		if m.versionsCache == nil {
			m.versionsCache = map[string]packVersionsCacheEntry{}
		}
		m.versionsCache[msg.packName] = entry
		return m, nil

	case versionsDebounceMsg:
		// Stale debounce tick — a newer cursor move has already bumped the
		// epoch, so its own tick is in flight and this one must drop.
		if msg.epoch != m.versionsDebounceEpoch {
			return m, nil
		}
		return m, m.maybeLoadVersionsForCursor()

	case packDriftLoadedMsg:
		// Replace the drift set wholesale — the loader returns the complete
		// state, not a delta. An error from the loader is treated as "no
		// drift signal" rather than as a UI-blocking failure (drift is a
		// passive hint; offline runs degrade gracefully).
		next := make(map[string]app.PackDrift, len(msg.drifted))
		for _, d := range msg.drifted {
			next[d.Name] = d
		}
		m.driftSet = next
		return m, nil

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

	slices.SortFunc(list, func(a, b packListItem) int {
		if a.installed != b.installed {
			if a.installed {
				return -1
			}
			return 1
		}
		return cmp.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
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
			return m, m.scheduleVersionsLoad()
		}
	case "k", "up":
		if m.listCursor > 0 {
			m.listCursor--
			m.clampListOffset()
			m.buildContentForCurrent()
			return m, m.scheduleVersionsLoad()
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

// listVisibleRows returns the number of pack rows visible in the list panel
// for a panel of the given total height. Header (1) + count subtitle (1) +
// blank line (1) + reserved scroll indicator (1) = 4 lines of chrome.
func listVisibleRows(panelHeight int) int {
	return max(panelHeight-4, 1)
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
	panelH := max(m.height-2, 8)
	m.listOffset = clampOffset(m.listCursor, m.listOffset, listVisibleRows(panelH))
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

	for _, cat := range packTabCategories() {
		addCategory(cat, entry.ContentIDs(cat))
	}
	return items
}

// packTabCategories returns the category display order for the Packs tab.
// Unlike domain.AllPackCategories (which enumerates categories that sync/save
// processes), this list also includes prompts so pack-shipped slash commands
// surface alongside rules, agents, workflows, skills, and MCP servers in the
// pack content browser. Prompts are not captured by harness save pipelines,
// so they stay out of the global category list.
func packTabCategories() []domain.PackCategory {
	return []domain.PackCategory{
		domain.CategoryRules,
		domain.CategoryAgents,
		domain.CategoryWorkflows,
		domain.CategorySkills,
		domain.CategoryPrompts,
		domain.CategoryMCP,
	}
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

	col1 := m.viewListPanel(col1W, colH)
	col2 := m.viewContentPanel(col2W, colH)
	col3 := m.viewDetailsOrPreviewPanel(col3W, colH)
	sep1 := verticalSeparator(colH, sepW, m.focus == packPanelContent)
	sep2 := verticalSeparator(colH, sepW, m.focus == packPanelPreview)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, col1, sep1, col2, sep2, col3)
	return contentStyle.Render(joined)
}

// viewDetailsOrPreviewPanel renders the right column. When the packs list is
// focused, it shows pack details. When the user has drilled into the content
// list (or is scrolling the preview), it shows the file preview.
func (m packsModel) viewDetailsOrPreviewPanel(width, height int) string {
	if m.focus == packPanelList {
		return m.viewPackInfoPanel(width, height)
	}
	return m.viewPreviewPanel(width, height)
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

		// Suffix pinned packs with their pin label so the list reads as
		// a status view, not just a name list. Lookup is cheap (one map
		// hit per row); rendered dim so it doesn't fight the pack name.
		row := cursor + nameStyle.Render(li.name)
		if li.installed {
			if idx, ok := m.installedMap[li.name]; ok {
				if pin := m.items[idx].entry.PinLabel(); pin != "" {
					row += "  " + dimStyle.Render(pin)
				}
			}
		}
		// Drift indicator. Drawn after the pin label so users get a
		// consistent left-to-right read: name → pin → drift. The arrow is
		// terse on purpose — the details panel surfaces the full hash diff
		// when the user navigates to the row. Yellow weight signals "your
		// attention is wanted" without escalating to red error styling.
		if _, drifted := m.driftSet[li.name]; drifted {
			row += "  " + driftStyle.Render("↑")
		}
		lines = append(lines, row)
	}

	writeScrollWindow(&sb, lines, m.listOffset, listVisibleRows(height))

	return renderPackPanel(width, height, m.focus == packPanelList, sb.String())
}

// viewPackInfoPanel renders the right column when the packs list is focused:
// pack details followed by a registry block separated by a rule.
func (m packsModel) viewPackInfoPanel(width, height int) string {
	li := m.currentListItem()
	if li == nil {
		var sb strings.Builder
		sb.WriteString(renderPanelHeader("Details", false) + "\n")
		sb.WriteString(dimStyle.Render("No pack selected") + "\n")
		return renderPackPanel(width, height, false, sb.String())
	}

	innerW := max(width-4, 20)
	labelW := 12
	registryStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("114"))

	var sb strings.Builder
	sb.WriteString(renderPanelHeader("Details", false) + "\n")
	sb.WriteString(selectedStyle.Render(li.name) + "\n")

	if item := m.currentItem(); item != nil {
		if item.entry.Version != "" {
			sb.WriteString(infoField("Version", "v"+item.entry.Version, labelW, innerW) + "\n")
		}
		// Pin/Ref/Commit surface the v0.21 lockfile state. Pin wins over
		// Ref because it carries the same information plus the "(pinned)"
		// indicator; an unpinned clone shows the tracked branch via Ref.
		if pin := item.entry.PinLabel(); pin != "" {
			sb.WriteString(infoField("Pin", pin, labelW, innerW) + "\n")
		} else if item.entry.Ref != "" {
			sb.WriteString(infoField("Ref", item.entry.Ref, labelW, innerW) + "\n")
		}
		if item.entry.CommitHash != "" {
			sb.WriteString(infoField("Commit", util.ShortHash(item.entry.CommitHash), labelW, innerW) + "\n")
		}
		// "Latest" surfaces drift for installed clone packs at a glance.
		// Hidden when versions aren't loaded yet, when no semver tags exist,
		// or when the user is already on the latest tag — those cases would
		// only add noise to the panel.
		if hint := m.installedLatestHint(li.name, item.entry); hint != "" {
			sb.WriteString(infoField("Latest", hint, labelW, innerW) + "\n")
		}
		if item.entry.Method != "" {
			name, style := methodDisplay(item.entry.Method)
			sb.WriteString(infoField("Method", style.Render(name), labelW, innerW) + "\n")
		}
		if item.fileSizes != nil {
			if total, ok := item.fileSizes["total"]; ok && total > 0 {
				sb.WriteString(infoField("Size", formatSize(total), labelW, innerW) + "\n")
			}
		}
		if installedAt := formatInstallDate(item.entry.InstalledAt); installedAt != "" {
			sb.WriteString(infoField("Installed", installedAt, labelW, innerW) + "\n")
		}
		src := sourceForMethod(item.entry.Method, item.entry.Origin, item.entry.Path)
		if item.entry.Method == config.MethodLink || item.entry.Method == config.MethodCopy || item.entry.Method == config.MethodLocal {
			_, style := methodDisplay(item.entry.Method)
			src = style.Render(src)
		}
		sb.WriteString(infoField("Source", src, labelW, innerW) + "\n")
	} else {
		sb.WriteString(dimStyle.Render("Not installed locally.") + "\n")
	}

	if li.inRegistry {
		if li.description != "" {
			sb.WriteString(infoField("About", strings.TrimSpace(li.description), labelW, innerW) + "\n")
		}
		sb.WriteString("\n")
		sb.WriteString(dimStyle.Render(strings.Repeat("─", innerW)) + "\n")
		sb.WriteString(registryStyle.Render("Registry") + "\n")
		sb.WriteString(infoField("Name", li.name, labelW, innerW) + "\n")
		if li.owner != "" {
			sb.WriteString(infoField("Owner", li.owner, labelW, innerW) + "\n")
		}
		if li.repo != "" {
			sb.WriteString(infoField("Repo", repoBaseName(li.repo, li.regPath), labelW, innerW) + "\n")
		}
		if li.ref != "" {
			sb.WriteString(infoField("Ref", li.ref, labelW, innerW) + "\n")
		}
		// Inline version list for registry-only packs answers "what versions
		// can I pick?" without leaving the browse view. Loading and error
		// states are surfaced explicitly so users can tell why the line is
		// missing tags.
		if !li.installed {
			if vline := m.registryVersionsLine(li.name); vline != "" {
				sb.WriteString(infoField("Versions", vline, labelW, innerW) + "\n")
			}
		}
	}

	// A passphrase-locked ~/.ssh/id_rsa with no agent-cached entry turns
	// every ls-remote into a "(unavailable)" row with no visible recovery
	// path. Surfacing the ssh-add recipe inline makes the failure self-serve.
	if hint := m.recoveryHintForCurrent(); hint != "" {
		sb.WriteString("\n" + hint + "\n")
	}

	return renderPackPanel(width, height, false, sb.String())
}

// installedLatestHint returns a short label for the "Latest" row in the
// details panel of an installed clone pack. Returns "" when there's nothing
// useful to show: cache miss, in-flight load, no semver tags, or already on
// the newest tag. The hint is intentionally cheap — it's a drift signal, not
// a full version list (the registry block has the full list when applicable).
func (m packsModel) installedLatestHint(name string, entry app.PackShowEntry) string {
	if entry.Method != config.MethodClone {
		return ""
	}
	cached, ok := m.versionsCacheEntry(name)
	if !ok || cached.state == asyncLoading {
		return ""
	}
	if cached.state == asyncError || len(cached.versions) == 0 {
		return ""
	}
	latest := cached.versions[0].Version // FilterSemverTags returns descending
	// Suppress when the installed pin already matches the latest — no drift,
	// no need for the user's attention. We compare against Pin first
	// (authoritative for clone packs) and fall back to pack.json Version.
	current := entry.Pin
	if current == "" {
		current = entry.Version
	}
	if current != "" && source.StripVersionPrefix(current) == source.StripVersionPrefix(latest) {
		return ""
	}
	return latest
}

// registryVersionsLine renders the version-discovery state for a registry-only
// pack as a comma-separated inline string. Caps at three entries plus a "+N"
// suffix when there are more — the goal is "show me what's available" at a
// glance, not a full enumeration.
func (m packsModel) registryVersionsLine(name string) string {
	cached, ok := m.versionsCacheEntry(name)
	if !ok {
		return "" // not yet triggered; cursor focus loader will populate
	}
	switch cached.state {
	case asyncLoading:
		return dimStyle.Render("loading…")
	case asyncError:
		switch classifyGitError(cached.err) {
		case gitErrSSHPublickeyDenied:
			return dimStyle.Render("(ssh auth required)")
		case gitErrSSHHostKeyFailed:
			return dimStyle.Render("(host key issue)")
		}
		return dimStyle.Render("(unavailable)")
	}
	if len(cached.versions) == 0 {
		return dimStyle.Render("(none)")
	}
	const inlineCap = 3
	versions := cached.versions
	tail := ""
	if len(versions) > inlineCap {
		tail = fmt.Sprintf(" +%d more", len(versions)-inlineCap)
		versions = versions[:inlineCap]
	}
	parts := make([]string, len(versions))
	for i, v := range versions {
		parts[i] = v.Version
	}
	return strings.Join(parts, ", ") + tail
}

// gitErrorKind classifies a git subprocess error string into one of a small
// set of categories that have a distinctive stderr signature AND a single
// universal user recovery. Patterns that would match generic git fallback
// phrases ("Could not read from remote repository", "authentication failed")
// are intentionally NOT matched — those appear for many unrelated failure
// modes (DNS outages, VPN drops, repo not found, connection refused) and
// matching them would surface the wrong recovery hint.
type gitErrorKind int

const (
	gitErrOther gitErrorKind = iota
	// gitErrSSHPublickeyDenied — ssh offered a key the remote rejected.
	// Covers passphrase-locked local keys not in the agent, keys not
	// registered on the remote account, and agent-empty states.
	// Signature: "Permission denied (publickey".
	gitErrSSHPublickeyDenied
	// gitErrSSHHostKeyFailed — ~/.ssh/known_hosts has a stored key that
	// doesn't match the server. Signature: "Host key verification failed".
	gitErrSSHHostKeyFailed
)

// classifyGitError inspects a captured git stderr string and returns the
// narrowest recognized classification. Empty / unrecognized strings map to
// gitErrOther so the caller can fall back to a generic "(unavailable)"
// rendering without a misleading recovery hint.
func classifyGitError(errMsg string) gitErrorKind {
	if errMsg == "" {
		return gitErrOther
	}
	if strings.Contains(errMsg, "Permission denied (publickey") {
		return gitErrSSHPublickeyDenied
	}
	if strings.Contains(errMsg, "Host key verification failed") {
		return gitErrSSHHostKeyFailed
	}
	return gitErrOther
}

// recoveryHintForCurrent returns a dim recovery hint line when the currently
// selected pack's cached version lookup failed with an error we can classify
// as user-fixable. Returns "" otherwise, so the caller can unconditionally
// append.
func (m packsModel) recoveryHintForCurrent() string {
	li := m.currentListItem()
	if li == nil {
		return ""
	}
	cached, ok := m.versionsCacheEntry(li.name)
	if !ok || cached.state != asyncError {
		return ""
	}
	switch classifyGitError(cached.err) {
	case gitErrSSHPublickeyDenied:
		return dimStyle.Render("ssh auth failed — run `ssh-add` in another terminal, then press r to retry")
	case gitErrSSHHostKeyFailed:
		return dimStyle.Render("host key mismatch — verify the remote host and update ~/.ssh/known_hosts, then press r to retry")
	}
	return ""
}

// sourceForMethod returns the Source value for the details pane.
// link/copy show the origin path; clone/archive show the installed path on disk.
func sourceForMethod(method, origin, installPath string) string {
	switch method {
	case config.MethodLink, config.MethodCopy, config.MethodLocal:
		if origin != "" {
			return shortPath(origin)
		}
	case config.MethodClone, config.MethodArchive:
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
	case config.MethodLink:
		return "link", lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	case config.MethodCopy, config.MethodLocal:
		return "local", lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	case config.MethodClone, config.MethodArchive:
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
// infoField renders a "label   value" pair where long values wrap inside
// the value column instead of bleeding into the next row's label gutter.
// The value is pre-wrapped on word boundaries (lipgloss's own Width() does
// character-wrap, which produces ugly mid-word breaks at narrow widths),
// then handed to lipgloss as a fixed-width block. JoinHorizontal stacks
// the label column next to it — empty rows below the single-line label
// keep continuation lines of the value aligned at the same x offset.
func infoField(label, value string, labelWidth, contentW int) string {
	padded := label + strings.Repeat(" ", max(labelWidth-len(label), 1))
	valueW := max(contentW-labelWidth, 10)

	// Styled values (containing ANSI escapes) skip the word-wrap step
	// because strings.Fields would tokenize across escape sequences and
	// break the styling. The only realistic case is the Source field for
	// link/copy packs at narrow widths — character-wrap there is an
	// acceptable degradation for an edge case.
	if !strings.Contains(value, "\x1b") {
		value = wrapWords(value, valueW)
	}

	valueCol := lipgloss.NewStyle().Width(valueW).Render(value)
	return lipgloss.JoinHorizontal(lipgloss.Top, dimStyle.Render(padded), valueCol)
}

// wrapWords greedy-wraps a plain-text string to lines no wider than width,
// preserving word boundaries. Words longer than width are hard-broken so
// every line fits the column.
func wrapWords(s string, width int) string {
	if width <= 0 {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var lines []string
	var current string
	for _, w := range words {
		if len(w) > width {
			if current != "" {
				lines = append(lines, current)
				current = ""
			}
			for len(w) > width {
				lines = append(lines, w[:width])
				w = w[width:]
			}
			current = w
			continue
		}
		if current == "" {
			current = w
			continue
		}
		if len(current)+1+len(w) > width {
			lines = append(lines, current)
			current = w
		} else {
			current += " " + w
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return strings.Join(lines, "\n")
}

func installedPackSummary(e app.PackShowEntry) string {
	c := e.Counts()
	if c.IsZero() {
		return ""
	}
	return c.String()
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
		sb.WriteString(dimStyle.Render("Select content to preview") + "\n")
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
	ruleW := min(max(contentW, 10), 80)

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

func verticalSeparator(height, width int, focused bool) string {
	if height < 1 {
		height = 1
	}
	if width < 1 {
		width = 1
	}
	style := dimStyle
	if focused {
		style = selectedStyle
	}
	lines := make([]string, height)
	for i := range lines {
		if width == 1 {
			lines[i] = style.Render("│")
			continue
		}
		lines[i] = strings.Repeat(" ", width/2) + style.Render("│") + strings.Repeat(" ", width-width/2-1)
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
