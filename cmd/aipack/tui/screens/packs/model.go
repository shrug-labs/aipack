package packs

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	tuiutil "github.com/shrug-labs/aipack/cmd/aipack/tui/util"
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

	// Per-row pack-update streaming state. Zero-valued (asyncPending,
	// empty type/err, zero clearAt) when no update activity is happening —
	// the row renders without any update suffix in that case. State machine
	// driven by packProgressMsg / packUpdatedMsg / packRowClearMsg.
	updateState   asyncState // pending → loading → loaded/error → pending after auto-clear
	updateType    string     // last phase/status hint for this row
	updateErr     error      // populated for asyncError rows
	updateClearAt time.Time  // wall time at which a terminal row's decoration auto-clears
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
	inIndex     bool
	description string // from registry
	owner       string // from registry
	contact     string
	repo        string // from registry
	ref         string // from registry
	regPath     string // from registry (subdirectory path)
	status      string
	source      string
}

type Model struct {
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

	// Search index state for uninstalled inspected/deep-indexed packs.
	indexDetails map[string]app.IndexedPackDetail
	indexErr     string
	indexState   asyncState

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

	// activeUpdate is non-nil while a streaming pack update is in flight.
	// Set on packUpdateReadyMsg; cleared on packUpdatedMsg. Carries the
	// channels readNextEvent reads from plus the cancel handle the Esc
	// handler invokes.
	activeUpdate *activeUpdate

	// activeInstall is non-nil while a streaming pack install is in flight.
	// Set on packInstallReadyMsg; cleared on packInstalledMsg. Drives the
	// status-bar spinner + phase text and the Esc cancellation handler.
	// Install has no per-row decoration (the pack doesn't exist yet) so
	// progress lives entirely in the status bar.
	activeInstall *activeInstall

	// Spinner for in-flight rows. A single shared frame across all rows in
	// asyncLoading state — simpler than per-row, visually fine because all
	// rows step in unison. Ticks only while activeUpdate is non-nil.
	spinner spinner.Model

	// Batch counters for the status-bar summary line during update --all.
	// batchTotal is set when the operation kicks off (1 for single-pack
	// updates, len(items) for --all); batchInFlight and batchDone update
	// per progress event. Pending = batchTotal - batchDone - batchInFlight.
	batchTotal    int
	batchInFlight int
	batchDone     int

	doubleClick common.DoubleClickTracker
}

const packDoubleClickWindow = 500 * time.Millisecond

// packVersionsCacheEntry holds one pack's version-discovery state. The state
// distinguishes "we haven't asked yet" (no entry in the map) from "we asked
// and got nothing" (entry with empty Versions and asyncLoaded), so the UI can
// show "loading…" vs "no semver tags" vs "lookup failed" without ambiguity.
type packVersionsCacheEntry struct {
	state    asyncState
	versions []app.PackVersion
	err      string
}

type activeInstall struct {
	eventCh  <-chan app.PackInstallEvent
	resultCh <-chan packInstallResult
	cancel   context.CancelFunc
	packName string
	phase    app.PackInstallPhase
}

type activeUpdate struct {
	eventCh  <-chan app.PackUpdateEvent
	resultCh <-chan packUpdateResult
	cancel   context.CancelFunc
	packName string
}

func New(configDir string) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(clrLoading)
	return Model{
		configDir:     configDir,
		installedMap:  map[string]int{},
		indexDetails:  map[string]app.IndexedPackDetail{},
		versionsCache: map[string]packVersionsCacheEntry{},
		driftSet:      map[string]app.PackDrift{},
		spinner:       sp,
	}
}

func (m Model) currentListItem() *packListItem {
	if len(m.listItems) == 0 || m.listCursor < 0 || m.listCursor >= len(m.listItems) {
		return nil
	}
	return &m.listItems[m.listCursor]
}

// currentItem returns the installed pack detail for the selected list item, or nil.
func (m Model) currentItem() *packItemDetail {
	li := m.currentListItem()
	if li == nil || !li.installed {
		return nil
	}
	if idx, ok := m.installedMap[li.name]; ok {
		return &m.items[idx]
	}
	return nil
}

func (m Model) currentIndexedDetail() (app.IndexedPackDetail, bool) {
	li := m.currentListItem()
	if li == nil || !li.inIndex {
		return app.IndexedPackDetail{}, false
	}
	detail, ok := m.indexDetails[li.name]
	return detail, ok
}

func (m Model) indexedResourceFor(ci contentItem) (app.IndexedPackResource, bool) {
	detail, ok := m.currentIndexedDetail()
	if !ok {
		return app.IndexedPackResource{}, false
	}
	wantKind := kindForCategory(ci.category)
	for _, resource := range detail.Resources {
		if resource.Name == ci.id && resource.Kind == wantKind {
			return resource, true
		}
	}
	return app.IndexedPackResource{}, false
}

// versionsCacheEntry returns the cached version-discovery result for a pack,
// or the zero value when not yet queried. The bool reports whether an entry
// exists at all (distinguishing "not asked" from "asked and got empty").
func (m Model) versionsCacheEntry(name string) (packVersionsCacheEntry, bool) {
	if m.versionsCache == nil {
		return packVersionsCacheEntry{}, false
	}
	e, ok := m.versionsCache[name]
	return e, ok
}

// versionsEligible reports whether the current list item is one we can query
// remote tags for. Clone-method installed packs and registry-backed packs
// qualify; link/copy/local installs and index-only previews have no pack
// source we can resolve by name through the version service.
func (m Model) versionsEligible(li *packListItem) bool {
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
func (m *Model) versionLoadCandidate() *packListItem {
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
func (m *Model) maybeLoadVersionsForCursor() tea.Cmd {
	li := m.versionLoadCandidate()
	if li == nil {
		return nil
	}
	if m.versionsCache == nil {
		m.versionsCache = map[string]packVersionsCacheEntry{}
	}
	m.versionsCache[li.name] = packVersionsCacheEntry{state: asyncLoading}
	return LoadVersions(context.Background(), m.configDir, li.name)
}

// scheduleVersionsLoad debounces version loads across cursor-move bursts:
// the caller bumps the epoch and a tea.Tick later delivers the captured
// epoch back as versionsDebounceMsg, which only fires the load if no newer
// cursor move has intervened. Without this, holding j/k spawned a 30s git
// ls-remote per keystroke — blocking on pinentry for users with locked SSH
// keys. Returns nil (emits no command at all) when the cursor target is
// ineligible or already cached.
func (m *Model) scheduleVersionsLoad() tea.Cmd {
	if m.versionLoadCandidate() == nil {
		return nil
	}
	m.versionsDebounceEpoch++
	epoch := m.versionsDebounceEpoch
	return tea.Tick(versionsDebounceDelay, func(_ time.Time) tea.Msg {
		return versionsDebounceMsg{Epoch: epoch}
	})
}

// refreshVersionsForCursor drops any cached version entry for the current
// pack and kicks off an immediate (non-debounced) reload. Bound to the 'r'
// key so users can force a re-check after fixing credentials or pushing a
// new tag upstream, without leaving the packs tab.
func (m *Model) refreshVersionsForCursor() tea.Cmd {
	li := m.currentListItem()
	if !m.versionsEligible(li) {
		return nil
	}
	if m.versionsCache != nil {
		delete(m.versionsCache, li.name)
	}
	return m.maybeLoadVersionsForCursor()
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		Load(m.configDir),
		LoadRegistry(m.configDir),
		LoadIndexDetails(m.configDir),
		LoadDrift(context.Background(), m.configDir),
	)
}

func (m Model) SetSize(width, height int) common.Screen {
	m.width = width
	m.height = height
	return m
}

func (m Model) Update(msg tea.Msg) (common.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case common.FocusMsg, common.BlurMsg:
		return m, nil
	case common.LayerHitMsg:
		if !strings.HasPrefix(msg.ID, "packs:") {
			return m, nil
		}
		return m.handleLayerHit(msg)
	case packsLoadedMsg:
		if msg.Err != nil {
			m.loadErr = fmt.Sprintf("load packs: %v", msg.Err)
			return m, nil
		}
		// Preserve per-row update decorations across reload — without this,
		// the loadPacks fired by packUpdatedMsg wipes the terminal state
		// (✓ updated, ✗ error, etc.) before the user can see it.
		prevUpdate := map[string]packItemDetail{}
		for _, it := range m.items {
			if it.updateState != asyncPending {
				prevUpdate[it.entry.Name] = it
			}
		}
		m.items = make([]packItemDetail, 0, len(msg.Items))
		for _, e := range msg.Items {
			m.items = append(m.items, packItemDetail{
				entry:     e,
				sizeState: asyncPending,
			})
		}
		m.installedMap = map[string]int{}
		if m.versionsCache == nil {
			m.versionsCache = map[string]packVersionsCacheEntry{}
		}
		for i, item := range m.items {
			m.installedMap[item.entry.Name] = i
			if prev, ok := prevUpdate[item.entry.Name]; ok {
				m.items[i].updateState = prev.updateState
				m.items[i].updateType = prev.updateType
				m.items[i].updateErr = prev.updateErr
				m.items[i].updateClearAt = prev.updateClearAt
			}
		}
		m.rebuildList()

		// Kick off file size computation for all packs.
		var cmds []tea.Cmd
		for i := range m.items {
			m.items[i].sizeState = asyncLoading
			cmds = append(cmds, ComputeSizes(m.items[i].entry))
		}
		if previewCmd := m.loadInlinePreview(); previewCmd != nil {
			cmds = append(cmds, previewCmd)
		}
		return m, tea.Batch(cmds...)

	case packSizesMsg:
		if idx, ok := m.installedMap[msg.PackName]; ok {
			m.items[idx].fileSizes = msg.Sizes
			m.items[idx].sizeState = asyncLoaded
		}
		return m, nil

	case registryLoadedMsg:
		if msg.Err != nil {
			m.registryErr = msg.Err.Error()
			m.registryState = asyncError
			return m, nil
		}
		m.registry = make([]registryItem, 0, len(msg.Items))
		for _, item := range msg.Items {
			m.registry = append(m.registry, registryItem{
				name:        item.Name,
				description: item.Description,
				repo:        item.Repo,
				path:        item.Path,
				ref:         item.Ref,
				owner:       item.Owner,
			})
		}
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

	case indexLoadedMsg:
		if msg.Err != nil {
			m.indexErr = msg.Err.Error()
			m.indexState = asyncError
			return m, nil
		}
		m.indexDetails = make(map[string]app.IndexedPackDetail, len(msg.Items))
		for _, item := range msg.Items {
			m.indexDetails[item.Name] = item
		}
		m.indexErr = ""
		m.indexState = asyncLoaded
		m.rebuildList()
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
			versions: msg.Versions,
		}
		if msg.Err != nil {
			entry.state = asyncError
			entry.err = msg.Err.Error()
		}
		if m.versionsCache == nil {
			m.versionsCache = map[string]packVersionsCacheEntry{}
		}
		m.versionsCache[msg.PackName] = entry
		return m, nil

	case versionsDebounceMsg:
		// Stale debounce tick — a newer cursor move has already bumped the
		// epoch, so its own tick is in flight and this one must drop.
		if msg.Epoch != m.versionsDebounceEpoch {
			return m, nil
		}
		return m, m.maybeLoadVersionsForCursor()

	case packDriftLoadedMsg:
		// Replace the drift set wholesale — the loader returns the complete
		// state, not a delta. An error from the loader is treated as "no
		// drift signal" rather than as a UI-blocking failure (drift is a
		// passive hint; offline runs degrade gracefully).
		next := make(map[string]app.PackDrift, len(msg.Drifted))
		for _, d := range msg.Drifted {
			next[d.Name] = d
		}
		m.driftSet = next
		return m, nil

	case packInstallReadyMsg:
		m.activeInstall = &activeInstall{
			eventCh:  msg.EventCh,
			resultCh: msg.ResultCh,
			cancel:   msg.Cancel,
			packName: msg.PackName,
		}
		return m, tea.Batch(ReadNextInstallEvent(msg.EventCh, msg.ResultCh), m.spinner.Tick)

	case packInstallProgressMsg:
		if m.activeInstall != nil {
			m.activeInstall.phase = msg.Event.Phase
		}
		if ai := m.activeInstall; ai != nil {
			return m, ReadNextInstallEvent(ai.eventCh, ai.resultCh)
		}
		return m, nil

	case packInstalledMsg:
		m.activeInstall = nil
		if msg.Err == nil {
			delete(m.driftSet, msg.Name)
		}
		return m, nil

	case packUpdateReadyMsg:
		m.activeUpdate = &activeUpdate{
			eventCh:  msg.EventCh,
			resultCh: msg.ResultCh,
			cancel:   msg.Cancel,
			packName: msg.PackName,
		}
		if msg.PackName == "" {
			m.batchTotal = len(m.items)
		} else {
			m.batchTotal = 1
		}
		m.batchInFlight = 0
		m.batchDone = 0
		return m, tea.Batch(ReadNextEvent(msg.EventCh, msg.ResultCh, msg.PackName), m.spinner.Tick)

	case packProgressMsg:
		var clearCmd tea.Cmd
		m, clearCmd = m.applyProgress(msg.Event)
		var cmds []tea.Cmd
		if au := m.activeUpdate; au != nil {
			cmds = append(cmds, ReadNextEvent(au.eventCh, au.resultCh, au.packName))
		}
		if clearCmd != nil {
			cmds = append(cmds, clearCmd)
		}
		return m, tea.Batch(cmds...)

	case packRowClearMsg:
		m = m.applyRowClear(msg)
		return m, nil

	case packUpdatedMsg:
		m.activeUpdate = nil
		m.batchTotal = 0
		m.batchInFlight = 0
		m.batchDone = 0
		m = m.applyCancellation(msg.Results)
		var cmds []tea.Cmd
		m, cmds = m.reconcileFromResults(msg.Results)
		for _, r := range msg.Results {
			if r.Status == app.StatusUpdated || r.Status == app.StatusUpToDate {
				delete(m.driftSet, r.Name)
			}
		}
		return m, tea.Batch(cmds...)

	case spinner.TickMsg:
		if m.activeUpdate != nil || m.activeInstall != nil {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case previewLoadedMsg:
		if msg.FilePath != m.previewPath {
			return m, nil
		}
		m.previewData = msg
		if msg.Err != nil {
			m.previewState = asyncError
		} else {
			m.previewState = asyncLoaded
		}
		return m, nil

	case tea.KeyPressMsg:
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
	case strings.HasPrefix(msg.ID, "packs:list:"):
		idx, ok := common.ParseLayerIndex(msg.ID, "packs:list:")
		if !ok || idx < 0 || idx >= len(m.listItems) {
			return m, nil
		}
		m.listCursor = idx
		m.focus = packPanelList
		m.clampListOffset()
		m.buildContentForCurrent()
		if m.isDoubleClick(msg.ID) {
			return m, func() tea.Msg { return ActionRequestMsg{} }
		}
		return m, m.scheduleVersionsLoad()
	case strings.HasPrefix(msg.ID, "packs:content:"):
		idx, ok := common.ParseLayerIndex(msg.ID, "packs:content:")
		if !ok || idx < 0 || idx >= len(m.contentItems) || m.contentItems[idx].isHeader {
			return m, nil
		}
		m.focus = packPanelContent
		m.contentCursor = idx
		m.clampContentOffset()
		if m.isDoubleClick(msg.ID) {
			if cmd := m.currentContentPreviewCmd(); cmd != nil {
				return m, cmd
			}
		}
		return m, m.loadInlinePreview()
	}
	return m, nil
}

func (m Model) handleWheelHit(id string, delta int) (common.Screen, tea.Cmd) {
	if !strings.HasPrefix(id, "packs:content:") {
		return m, nil
	}
	m.focus = packPanelContent
	moved := false
	for range common.MouseWheelRows {
		if delta > 0 {
			if !m.moveContentCursorDown() {
				break
			}
		} else if !m.moveContentCursorUp() {
			break
		}
		moved = true
	}
	if !moved {
		return m, nil
	}
	m.clampContentOffset()
	return m, m.loadInlinePreview()
}

func (m *Model) isDoubleClick(id string) bool {
	return m.doubleClick.Hit(id, packDoubleClickWindow)
}

// rebuildList merges registry and installed packs into a unified list.
func (m *Model) rebuildList() {
	seen := map[string]bool{}
	var list []packListItem

	// Registry items first.
	for _, ri := range m.registry {
		_, isInstalled := m.installedMap[ri.name]
		indexDetail, inIndex := m.indexDetails[ri.name]
		li := packListItem{
			name:        ri.name,
			installed:   isInstalled,
			inRegistry:  true,
			inIndex:     inIndex,
			description: ri.description,
			owner:       ri.owner,
			contact:     indexDetail.Contact,
			repo:        ri.repo,
			ref:         ri.ref,
			regPath:     ri.path,
			status:      indexDetail.Status,
			source:      indexDetail.Source,
		}
		if li.description == "" {
			li.description = indexDetail.Description
		}
		if li.owner == "" {
			li.owner = indexDetail.Owner
		}
		if li.contact == "" {
			li.contact = indexDetail.Contact
		}
		if li.repo == "" {
			li.repo = indexDetail.Repo
		}
		if li.ref == "" {
			li.ref = indexDetail.Ref
		}
		if li.regPath == "" {
			li.regPath = indexDetail.Path
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

	// Index-only packs, including one-off inspected packs, may not exist in
	// the registry cache yet. Surface them as first-class uninstalled rows.
	for _, detail := range m.indexDetails {
		if seen[detail.Name] || detail.Installed {
			continue
		}
		list = append(list, packListItem{
			name:        detail.Name,
			inIndex:     true,
			description: detail.Description,
			owner:       detail.Owner,
			contact:     detail.Contact,
			repo:        detail.Repo,
			ref:         detail.Ref,
			regPath:     detail.Path,
			status:      detail.Status,
			source:      detail.Source,
		})
		seen[detail.Name] = true
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

func (m Model) updateListPanel(msg tea.KeyPressMsg) (Model, tea.Cmd) {
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
		if li != nil && len(m.contentItems) > 0 {
			m.focus = packPanelContent
			return m, m.loadInlinePreview()
		}
	}
	return m, nil
}

func (m Model) updateContentPanel(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.moveContentCursorDown() {
			m.clampContentOffset()
			return m, m.loadInlinePreview()
		}
	case "k", "up":
		if m.moveContentCursorUp() {
			m.clampContentOffset()
			return m, m.loadInlinePreview()
		}
	case "enter":
		if cmd := m.currentContentPreviewCmd(); cmd != nil {
			return m, cmd
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

func (m *Model) moveContentCursorDown() bool {
	for i := m.contentCursor + 1; i < len(m.contentItems); i++ {
		if !m.contentItems[i].isHeader {
			m.contentCursor = i
			return true
		}
	}
	return false
}

func (m *Model) moveContentCursorUp() bool {
	for i := m.contentCursor - 1; i >= 0; i-- {
		if !m.contentItems[i].isHeader {
			m.contentCursor = i
			return true
		}
	}
	return false
}

func (m Model) updatePreviewPanel(msg tea.KeyPressMsg) (Model, tea.Cmd) {
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
					return common.PreviewRequestMsg{
						Title:    ci.id,
						Category: ci.category,
						PackName: item.entry.Name,
						FilePath: fp,
					}
				}
			}
		}
	}
	return m, nil
}

func (m Model) currentContentPreviewCmd() tea.Cmd {
	if m.contentCursor < 0 || m.contentCursor >= len(m.contentItems) {
		return nil
	}
	ci := m.contentItems[m.contentCursor]
	if ci.isHeader {
		return nil
	}
	fp := m.contentFilePath(ci)
	if fp == "" {
		return nil
	}
	item := m.currentItem()
	if item == nil {
		return nil
	}
	return func() tea.Msg {
		return common.PreviewRequestMsg{
			Title:    ci.id,
			Category: ci.category,
			PackName: item.entry.Name,
			FilePath: fp,
		}
	}
}

// buildContentForCurrent rebuilds the content item list for the currently selected pack.
func (m *Model) buildContentForCurrent() {
	item := m.currentItem()
	if item == nil {
		if detail, ok := m.currentIndexedDetail(); ok {
			m.contentItems = buildContentItemsFromIndexed(detail.Resources)
			m.contentCursor = firstContentCursor(m.contentItems)
			m.contentOffset = 0
		} else {
			m.contentItems = nil
			m.contentCursor = 0
			m.contentOffset = 0
		}
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

// visibleContentH returns the number of scroll-window rows used by
// writeContentItemLines. Keep this aligned with the content panel renderer.
func (m Model) visibleContentH() int {
	panelH := max(m.height-2, 8)
	return contentWindowRows(panelH)
}

func contentWindowRows(panelHeight int) int {
	return max(panelHeight-5, 1)
}

// listVisibleRows returns the number of pack rows visible in the list panel
// for a panel of the given total height. Header (1) + count subtitle (1) +
// blank line (1) + reserved scroll indicator (1) = 4 lines of chrome.
func listVisibleRows(panelHeight int) int {
	return max(panelHeight-4, 1)
}

// clampContentOffset ensures the content cursor is within the visible scroll window.
func (m *Model) clampContentOffset() {
	m.contentOffset = common.ClampOffset(m.contentCursorRow(), m.contentOffset, m.visibleContentH())
}

func (m Model) contentCursorRow() int {
	row := 0
	seenCategory := false
	for i, ci := range m.contentItems {
		if ci.isHeader {
			if seenCategory {
				row++
			}
			if i == m.contentCursor {
				return row
			}
			row++
			seenCategory = true
			continue
		}
		if i == m.contentCursor {
			return row
		}
		row++
	}
	return 0
}

// clampListOffset ensures the list cursor is within the visible scroll window.
func (m *Model) clampListOffset() {
	panelH := max(m.height-2, 8)
	m.listOffset = common.ClampOffset(m.listCursor, m.listOffset, listVisibleRows(panelH))
}

// buildContentItems creates a flat navigable list of content items for a pack.
func (m Model) buildContentItems(idx int) []contentItem {
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

func buildContentItemsFromIndexed(resources []app.IndexedPackResource) []contentItem {
	byCategory := map[domain.PackCategory][]string{}
	for _, resource := range resources {
		category, ok := domain.ParseSingularLabel(resource.Kind)
		if !ok {
			continue
		}
		byCategory[category] = append(byCategory[category], resource.Name)
	}
	for category := range byCategory {
		slices.Sort(byCategory[category])
	}

	var items []contentItem
	for _, category := range packTabCategories() {
		ids := byCategory[category]
		if len(ids) == 0 {
			continue
		}
		items = append(items, contentItem{category: category, isHeader: true, id: category.Label()})
		for _, id := range ids {
			items = append(items, contentItem{category: category, id: id})
		}
	}
	return items
}

func kindForCategory(category domain.PackCategory) string {
	switch category {
	case domain.CategoryRules:
		return "rule"
	case domain.CategoryAgents:
		return "agent"
	case domain.CategoryWorkflows:
		return "workflow"
	case domain.CategorySkills:
		return "skill"
	case domain.CategoryHooks:
		return "hook"
	case domain.CategoryMCP:
		return "mcp"
	case domain.CategoryPlugins:
		return "plugin"
	case domain.CategoryPrompts:
		return "prompt"
	case domain.CategorySettings:
		return "setting"
	default:
		return string(category)
	}
}

// packTabCategories returns the category display order for the Packs tab.
// Keep a stable, scan-friendly content display order for installed packs.
func packTabCategories() []domain.PackCategory {
	return []domain.PackCategory{
		domain.CategoryRules,
		domain.CategoryAgents,
		domain.CategoryWorkflows,
		domain.CategorySkills,
		domain.CategoryHooks,
		domain.CategoryPlugins,
		domain.CategoryPrompts,
		domain.CategoryMCP,
		domain.CategorySettings,
	}
}

// contentFilePath resolves the absolute file path for a content item.
func (m Model) contentFilePath(ci contentItem) string {
	item := m.currentItem()
	if item == nil || item.entry.Path == "" {
		return ""
	}
	return item.entry.ContentPath(ci.category, ci.id)
}

func (m Model) currentContentItem() *contentItem {
	if len(m.contentItems) == 0 || m.contentCursor < 0 || m.contentCursor >= len(m.contentItems) {
		return nil
	}
	ci := m.contentItems[m.contentCursor]
	if ci.isHeader {
		return nil
	}
	return &ci
}

func (m *Model) loadInlinePreview() tea.Cmd {
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
	return common.LoadPreview(ci.id, ci.category, item.entry.Name, filePath)
}

// --- View ---

func (m Model) View() *lipgloss.Layer {
	root := lipgloss.NewLayer(m.Render())
	root.AddLayers(m.clickLayers()...)
	return root
}

func (m Model) Render() string {
	if len(m.items) == 0 && m.loadErr == "" && m.registryState != asyncLoaded {
		return common.ContentStyle.Render("Loading packs...")
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
	return common.ContentStyle.Render(joined)
}

func (m Model) clickLayers() []*lipgloss.Layer {
	if m.width <= 0 || m.height <= 0 {
		return nil
	}
	innerW := max(m.width-4, 20)
	innerH := max(m.height-2, 8)
	sepW := 5
	col1W := max(innerW*28/100, 30)
	col2W := max(innerW*31/100, 34)
	col3W := innerW - col1W - col2W - (sepW * 2)
	col3W = max(col3W, 32)
	if col1W+col2W+col3W+(sepW*2) > innerW {
		col3W = max(innerW-col1W-col2W-(sepW*2), 24)
	}

	var layers []*lipgloss.Layer
	padX := common.ContentStyle.GetPaddingLeft()
	padY := common.ContentStyle.GetPaddingTop()
	listRows := listVisibleRows(innerH)
	for row := range listRows {
		idx := m.listOffset + row
		if idx >= len(m.listItems) {
			break
		}
		layers = append(layers,
			lipgloss.NewLayer(strings.Repeat(" ", col1W)).
				ID(fmt.Sprintf("packs:list:%d", idx)).
				X(padX).
				Y(padY+3+row).
				Z(-1),
		)
	}

	contentX := padX + col1W + sepW
	rows := m.contentRenderRows(col2W, m.currentItem())
	for row := 0; row < contentWindowRows(innerH); row++ {
		rowIdx := m.contentOffset + row
		if rowIdx >= len(rows) {
			break
		}
		idx := rows[rowIdx].itemIndex
		if idx < 0 {
			continue
		}
		layers = append(layers,
			lipgloss.NewLayer(strings.Repeat(" ", col2W)).
				ID(fmt.Sprintf("packs:content:%d", idx)).
				X(contentX).
				Y(padY+3+row).
				Z(-1),
		)
	}
	_ = col3W
	return layers
}

// viewDetailsOrPreviewPanel renders the right column. When the packs list is
// focused, it shows pack details. When the user has drilled into the content
// list (or is scrolling the preview), it shows the file preview.
func (m Model) viewDetailsOrPreviewPanel(width, height int) string {
	if m.focus == packPanelList {
		return m.viewPackInfoPanel(width, height)
	}
	if m.currentItem() == nil {
		return m.viewIndexedResourcePanel(width, height)
	}
	return m.viewPreviewPanel(width, height)
}

// viewListPanel renders the top-left pack list.
func (m Model) viewListPanel(width, height int) string {
	var sb strings.Builder

	sb.WriteString(renderPanelHeader("Packs", m.focus == packPanelList) + "\n")
	sb.WriteString(panelSubtleStyle.Render(fmt.Sprintf("%d installed, %d total", len(m.items), len(m.listItems))) + "\n\n")

	if m.registryState == asyncError {
		sb.WriteString(common.ErrorStyle.Render("Registry: "+m.registryErr) + "\n")
	}

	if len(m.listItems) == 0 {
		sb.WriteString(common.DimStyle.Render("(none)") + "\n")
		if m.registryState == asyncError || m.registryState == asyncLoaded {
			sb.WriteString(common.DimStyle.Render("Run: aipack pack install <name>") + "\n")
		}
		return renderPackPanel(width, height, m.focus == packPanelList, sb.String())
	}

	var lines []string
	focused := m.focus == packPanelList
	for i, li := range m.listItems {
		cursor := "  "
		if focused && i == m.listCursor {
			cursor = common.SelectedStyle.Render("▸ ")
		}

		var nameStyle lipgloss.Style
		if i == m.listCursor {
			nameStyle = common.SelectedStyle
		} else if li.installed {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		} else {
			nameStyle = common.DimStyle
		}

		// Suffix pinned packs with their pin label so the list reads as
		// a status view, not just a name list. Lookup is cheap (one map
		// hit per row); rendered dim so it doesn't fight the pack name.
		row := cursor + nameStyle.Render(li.name)
		var updateSuffix string
		if installSuffix := m.installRowSuffix(li.name); installSuffix != "" {
			updateSuffix = installSuffix
		}
		if li.installed {
			if idx, ok := m.installedMap[li.name]; ok {
				if pin := m.items[idx].entry.PinLabel(); pin != "" {
					row += "  " + common.DimStyle.Render(pin)
				}
				// Update-streaming decoration: spinner while in flight,
				// ✓/✗ glyph when terminal, blank when idle. Lives after
				// the pin so users read name → pin → drift → status.
				if updateSuffix == "" {
					updateSuffix = formatRowSuffix(m.items[idx], m.spinner)
				}
			}
		} else if li.status != "" {
			row += "  " + common.DimStyle.Render(li.status)
		} else if li.inIndex {
			row += "  " + common.DimStyle.Render("indexed")
		}
		// Drift indicator. Drawn after the pin label so users get a
		// consistent left-to-right read: name → pin → drift. The arrow is
		// terse on purpose — the details panel surfaces the full hash diff
		// when the user navigates to the row. Yellow weight signals "your
		// attention is wanted" without escalating to red error styling.
		if _, drifted := m.driftSet[li.name]; drifted {
			row += "  " + common.WarningStyle.Render("↑")
		}
		if updateSuffix != "" {
			row += "  " + updateSuffix
		}
		lines = append(lines, row)
	}

	common.WriteScrollWindow(&sb, lines, m.listOffset, listVisibleRows(height), func(remaining int) string {
		return common.DimStyle.Render(fmt.Sprintf("  ↓ %d more", remaining))
	})

	return renderPackPanel(width, height, m.focus == packPanelList, sb.String())
}

func (m Model) installRowSuffix(name string) string {
	if m.activeInstall == nil || m.activeInstall.packName != name {
		return ""
	}
	return m.spinner.View() + " " + common.DimStyle.Render(installPhaseLabel(m.activeInstall.phase))
}

// viewPackInfoPanel renders the right column when the packs list is focused:
// pack details followed by a registry block separated by a rule.
func (m Model) viewPackInfoPanel(width, height int) string {
	li := m.currentListItem()
	if li == nil {
		var sb strings.Builder
		sb.WriteString(renderPanelHeader("Details", false) + "\n")
		sb.WriteString(common.DimStyle.Render("No pack selected") + "\n")
		return renderPackPanel(width, height, false, sb.String())
	}

	innerW := max(width-4, 20)
	labelW := 12
	registryStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("114"))

	var sb strings.Builder
	sb.WriteString(renderPanelHeader("Details", false) + "\n")
	sb.WriteString(common.SelectedStyle.Render(li.name) + "\n")
	if m.activeInstall != nil && m.activeInstall.packName == li.name {
		sb.WriteString(m.spinner.View() + " " + common.DimStyle.Render(packsInstallStatus(m)) + "\n")
	}

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
				sb.WriteString(infoField("Size", common.FormatSize(total), labelW, innerW) + "\n")
			}
		}
		if installedAt := formatInstallDate(item.entry.InstalledAt); installedAt != "" {
			sb.WriteString(infoField("Installed", installedAt, labelW, innerW) + "\n")
		}
		// Checked is the last time we probed the remote — refreshes on every
		// pack-update probe regardless of outcome. Material evidence the
		// user just verified the pack even when content was already current.
		if checkedAt := formatInstallDate(item.entry.LastCheckedAt); checkedAt != "" {
			sb.WriteString(infoField("Checked", checkedAt, labelW, innerW) + "\n")
		}
		src := sourceForMethod(item.entry.Method, item.entry.Origin, item.entry.Path)
		if item.entry.Method == config.MethodLink || item.entry.Method == config.MethodCopy || item.entry.Method == config.MethodLocal {
			_, style := methodDisplay(item.entry.Method)
			src = style.Render(src)
		}
		sb.WriteString(infoField("Source", src, labelW, innerW) + "\n")
	} else {
		sb.WriteString(common.DimStyle.Render("Not installed locally.") + "\n")
		if detail, ok := m.currentIndexedDetail(); ok {
			if detail.Status != "" {
				sb.WriteString(infoField("Status", detail.Status, labelW, innerW) + "\n")
			}
			if detail.Version != "" {
				sb.WriteString(infoField("Version", "v"+detail.Version, labelW, innerW) + "\n")
			}
			if summary := indexedPackSummary(detail); summary != "" {
				sb.WriteString(infoField("Content", summary, labelW, innerW) + "\n")
			}
			if indexedAt := formatIndexedAt(detail.LastIndexedAt); indexedAt != "" {
				sb.WriteString(infoField("Indexed", indexedAt, labelW, innerW) + "\n")
			}
			if detail.Contact != "" {
				sb.WriteString(infoField("Contact", detail.Contact, labelW, innerW) + "\n")
			}
			if detail.Repo != "" {
				sb.WriteString(infoField("Source", repoBaseName(detail.Repo, detail.Path), labelW, innerW) + "\n")
			}
			if detail.Ref != "" {
				sb.WriteString(infoField("Ref", detail.Ref, labelW, innerW) + "\n")
			}
		}
	}

	if li.inRegistry {
		if li.description != "" {
			sb.WriteString(infoField("About", strings.TrimSpace(li.description), labelW, innerW) + "\n")
		}
		sb.WriteString("\n")
		sb.WriteString(common.DimStyle.Render(strings.Repeat("─", innerW)) + "\n")
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
func (m Model) installedLatestHint(name string, entry app.PackShowEntry) string {
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
	latest := cached.versions[0].Version // PackListVersions returns descending display semver
	// Suppress when the installed pin already matches the latest — no drift,
	// no need for the user's attention. Pin may be namespaced
	// ("my-pack/v0.3.0"); extract the semver portion before comparing.
	// Fall back to pack.json Version when the pack isn't pinned.
	current := source.SemverFromRef(entry.Pin)
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
func (m Model) registryVersionsLine(name string) string {
	cached, ok := m.versionsCacheEntry(name)
	if !ok {
		return "" // not yet triggered; cursor focus loader will populate
	}
	switch cached.state {
	case asyncLoading:
		return common.DimStyle.Render("loading…")
	case asyncError:
		switch classifyGitError(cached.err) {
		case gitErrSSHPublickeyDenied:
			return common.DimStyle.Render("(ssh auth required)")
		case gitErrSSHHostKeyFailed:
			return common.DimStyle.Render("(host key issue)")
		}
		return common.DimStyle.Render("(unavailable)")
	}
	if len(cached.versions) == 0 {
		return common.DimStyle.Render("(none)")
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
func (m Model) recoveryHintForCurrent() string {
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
		return common.DimStyle.Render("ssh auth failed — run `ssh-add` in another terminal, then press r to retry")
	case gitErrSSHHostKeyFailed:
		return common.DimStyle.Render("host key mismatch — verify the remote host and update ~/.ssh/known_hosts, then press r to retry")
	}
	return ""
}

// sourceForMethod returns the Source value for the details pane.
// Installed packs should show where they came from; the installed directory is
// only a fallback for old metadata that lacks an origin.
func sourceForMethod(method, origin, installPath string) string {
	switch method {
	case config.MethodLink, config.MethodCopy, config.MethodLocal, config.MethodClone, config.MethodArchive, config.MethodHTTPTarball:
		if origin != "" {
			return tuiutil.ShortPath(origin)
		}
	}
	if installPath != "" {
		return tuiutil.ShortPath(installPath)
	}
	if origin != "" {
		return tuiutil.ShortPath(origin)
	}
	return common.DimStyle.Render("(unknown)")
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
		return method, common.DimStyle
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
	return lipgloss.JoinHorizontal(lipgloss.Top, common.DimStyle.Render(padded), valueCol)
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

func indexedPackSummary(detail app.IndexedPackDetail) string {
	var counts app.ContentCounts
	for _, resource := range detail.Resources {
		if category, ok := domain.ParseSingularLabel(resource.Kind); ok {
			counts.Add(category)
		}
	}
	if counts.IsZero() {
		return ""
	}
	return counts.String()
}

// viewContentPanel renders the middle column: content browser for installed packs.
func (m Model) viewContentPanel(width, height int) string {
	var sb strings.Builder
	innerW := panelInnerWidth(width)

	sb.WriteString(renderPanelHeader("Content", m.focus == packPanelContent) + "\n")

	li := m.currentListItem()
	if li == nil || !li.installed {
		if li != nil && !li.installed {
			if detail, ok := m.currentIndexedDetail(); ok && len(m.contentItems) > 0 {
				sb.WriteString(contentSummaryStyle.Render(common.TruncateText(indexedPackSummary(detail), innerW)) + "\n\n")
				m.writeContentItemLines(&sb, innerW, height, nil)
				return renderPackPanel(width, height, m.focus == packPanelContent, sb.String())
			}
			sb.WriteString(common.DimStyle.Render("No indexed content yet") + "\n")
			sb.WriteString(common.DimStyle.Render("Run inspect or install to browse content") + "\n")
		}
		return renderPackPanel(width, height, m.focus == packPanelContent, sb.String())
	}

	item := m.currentItem()
	if len(m.contentItems) == 0 {
		sb.WriteString(common.DimStyle.Render("(no content)") + "\n")
		return renderPackPanel(width, height, m.focus == packPanelContent, sb.String())
	}

	sb.WriteString(contentSummaryStyle.Render(common.TruncateText(installedPackSummary(item.entry), innerW)) + "\n\n")

	m.writeContentItemLines(&sb, innerW, height, item)

	return renderPackPanel(width, height, m.focus == packPanelContent, sb.String())
}

type contentRenderRow struct {
	line      string
	itemIndex int
}

func (m Model) contentRenderRows(innerW int, item *packItemDetail) []contentRenderRow {
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
				if w := len(common.FormatSize(sz)); w > maxSizeW {
					maxSizeW = w
				}
			}
		}
	}

	var rows []contentRenderRow
	focused := m.focus == packPanelContent
	seenCategory := false
	for i, ci := range m.contentItems {
		if ci.isHeader {
			cs := stats[ci.category]
			if seenCategory {
				rows = append(rows, contentRenderRow{itemIndex: -1})
			}
			line := ci.id
			if cs != nil {
				line += fmt.Sprintf(" (%d)", cs.count)
				if cs.hasSize {
					line = alignWithSize(line, common.FormatSize(cs.total), innerW)
				}
			}
			rows = append(rows, contentRenderRow{line: categoryHeaderStyle.Render(line), itemIndex: -1})
			seenCategory = true
			continue
		}

		cursor := "  "
		if focused && i == m.contentCursor {
			cursor = common.SelectedStyle.Render("▸ ")
		}

		label := ci.id
		if i == m.contentCursor {
			label = common.SelectedStyle.Render(label)
		}

		line := cursor + label
		if item != nil && item.fileSizes != nil {
			if sz, ok := item.fileSizes[ci.category.DirName()+"/"+ci.id]; ok && sz >= 0 {
				line = alignWithSize(line, common.FormatSize(sz), innerW)
			}
		}
		rows = append(rows, contentRenderRow{line: line, itemIndex: i})
	}
	return rows
}

func (m Model) writeContentItemLines(sb *strings.Builder, innerW, height int, item *packItemDetail) {
	rows := m.contentRenderRows(innerW, item)
	lines := make([]string, len(rows))
	for i, row := range rows {
		lines[i] = row.line
	}

	common.WriteScrollWindow(sb, lines, m.contentOffset, contentWindowRows(height), func(remaining int) string {
		return common.DimStyle.Render(fmt.Sprintf("  ↓ %d more", remaining))
	})
}

func (m Model) viewIndexedResourcePanel(width, height int) string {
	var sb strings.Builder
	innerW := max(width-4, 20)
	labelW := 12

	sb.WriteString(renderPanelHeader("Details", false) + "\n")
	ci := m.currentContentItem()
	if ci == nil {
		sb.WriteString(common.DimStyle.Render("Select indexed content") + "\n")
		return renderPackPanel(width, height, false, sb.String())
	}
	resource, ok := m.indexedResourceFor(*ci)
	if !ok {
		sb.WriteString(common.DimStyle.Render("Indexed metadata unavailable") + "\n")
		return renderPackPanel(width, height, false, sb.String())
	}

	sb.WriteString(common.SelectedStyle.Render(resource.Name) + "\n")
	sb.WriteString(infoField("Type", ci.category.SingularLabel(), labelW, innerW) + "\n")
	if resource.Description != "" {
		sb.WriteString(infoField("About", resource.Description, labelW, innerW) + "\n")
	}
	if resource.Category != "" {
		sb.WriteString(infoField("Category", resource.Category, labelW, innerW) + "\n")
	}
	if resource.Owner != "" {
		sb.WriteString(infoField("Owner", resource.Owner, labelW, innerW) + "\n")
	}
	if resource.LastUpdated != "" {
		sb.WriteString(infoField("Updated", resource.LastUpdated, labelW, innerW) + "\n")
	}
	if resource.Path != "" {
		sb.WriteString(infoField("Path", resource.Path, labelW, innerW) + "\n")
	}
	if resource.Source != "" {
		sb.WriteString(infoField("Indexed", resource.Source, labelW, innerW) + "\n")
	}
	body := strings.TrimSpace(resource.Body)
	if body != "" {
		sb.WriteString("\n" + common.DimStyle.Render(strings.Repeat("─", min(innerW, 80))) + "\n")
		lines := strings.Split(body, "\n")
		for _, line := range common.TruncateLines(lines, innerW) {
			sb.WriteString(line + "\n")
		}
	}
	return renderPackPanel(width, height, false, sb.String())
}

func (m Model) viewPreviewPanel(width, height int) string {
	var sb strings.Builder

	sb.WriteString(renderPanelHeader("Preview", m.focus == packPanelPreview) + "\n")

	li := m.currentListItem()
	if li == nil || !li.installed {
		if li != nil && !li.installed {
			sb.WriteString(common.DimStyle.Render("Install to preview content") + "\n")
		}
		return renderPackPanel(width, height, m.focus == packPanelPreview, sb.String())
	}
	if len(m.contentItems) == 0 {
		sb.WriteString(common.DimStyle.Render("(no preview)") + "\n")
		return renderPackPanel(width, height, m.focus == packPanelPreview, sb.String())
	}

	switch m.previewState {
	case asyncLoading:
		sb.WriteString(common.DimStyle.Render("Preparing preview...") + "\n")
		return renderPackPanel(width, height, m.focus == packPanelPreview, sb.String())
	case asyncError:
		errText := "preview unavailable"
		if m.previewData.Err != nil {
			errText = m.previewData.Err.Error()
		}
		sb.WriteString(common.ErrorStyle.Render("Error: "+errText) + "\n")
		return renderPackPanel(width, height, m.focus == packPanelPreview, sb.String())
	case asyncPending:
		sb.WriteString(common.DimStyle.Render("Select content to preview") + "\n")
		return renderPackPanel(width, height, m.focus == packPanelPreview, sb.String())
	}

	lines := buildInlinePreviewLines(m.previewData, width)
	common.WriteScrollWindow(&sb, lines, m.previewOffset, max(height-2, 1), func(remaining int) string {
		return common.DimStyle.Render(fmt.Sprintf("  ↓ %d more", remaining))
	})
	return renderPackPanel(width, height, m.focus == packPanelPreview, sb.String())
}

func (m Model) visiblePreviewH() int {
	colH := max(m.height-2, 8)
	return max(colH-2, 1)
}

func (m Model) previewWidth() int {
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
		common.SelectedStyle.Render(msg.Category.SingularLabel() + "  " + msg.Title),
		common.DimStyle.Render(tuiutil.ShortPath(msg.FilePath)),
		strings.Repeat("─", ruleW),
	}

	if len(msg.Frontmatter) > 0 {
		lines = append(lines, "")
		for _, e := range msg.Frontmatter {
			lines = append(lines, previewKeyStyle.Render(e.Key+":")+" "+e.Value)
		}
		lines = append(lines, "", strings.Repeat("─", ruleW))
	}

	body := strings.TrimRight(msg.Body, "\n")
	if body == "" && len(msg.Frontmatter) == 0 {
		lines = append(lines, "", common.DimStyle.Render("(empty)"))
		return lines
	}
	if body != "" {
		lines = append(lines, "")
		lines = append(lines, strings.Split(body, "\n")...)
	}
	return common.TruncateLines(lines, contentW)
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
	style := common.DimStyle
	if focused {
		style = common.SelectedStyle
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

func alignWithSize(left, size string, width int) string {
	leftWidth := lipgloss.Width(left)
	sizeWidth := lipgloss.Width(size)
	const trailingPad = 3
	if leftWidth+sizeWidth+2+trailingPad >= width {
		left = common.TruncateText(left, max(width-sizeWidth-2-trailingPad, 4))
		leftWidth = lipgloss.Width(left)
	}
	gap := max(width-leftWidth-sizeWidth-trailingPad, 2)
	return left + strings.Repeat(" ", gap) + common.DimStyle.Render(size) + strings.Repeat(" ", trailingPad)
}

// formatInstallDate renders the lockfile's RFC3339 InstalledAt as a
// minute-precision local-time string. The timestamp refreshes whenever
// pack-update writes new content to disk (StatusUpdated path), so a
// recently-updated pack shows a recent timestamp — material evidence
// that the user's update actually changed the pack.
func formatInstallDate(raw string) string {
	if raw == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

func formatIndexedAt(raw int64) string {
	if raw <= 0 {
		return ""
	}
	return time.Unix(raw, 0).Local().Format("2006-01-02 15:04")
}
