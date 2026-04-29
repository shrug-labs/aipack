package tui

import (
	"context"
	"time"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/mcp"
)

// profilesLoadedMsg is sent after the initial profile listing completes.
type profilesLoadedMsg struct {
	items []profileItem
	err   error
}

// packsLoadedMsg is sent after the installed packs list loads.
type packsLoadedMsg struct {
	items []packItemDetail
	err   error
}

// syncTargetInfo holds resolved sync target details for display.
// It embeds app.PlanSummary for counts and ops, adding TUI-specific fields.
type syncTargetInfo struct {
	app.PlanSummary
	harnesses  []string
	scope      domain.Scope
	projectDir string
}

// syncStatusMsg delivers the result of a lazy sync-status check.
type syncStatusMsg struct {
	profileName string
	synced      bool
	target      syncTargetInfo
	warnings    []domain.Warning
	err         error
}

// fileSizeMsg delivers computed file sizes for a profile's tree.
type fileSizeMsg struct {
	profileName string
	sizes       map[string]int64
}

// diffLoadedMsg carries the result of an async diff computation for the plan view.
type diffLoadedMsg struct {
	dst      string
	title    string
	diffText string // unified diff output; empty string for new files
	isNew    bool   // true when destination file doesn't exist yet
	newBody  string // desired content for display when isNew is true
	err      error
}

// syncDoneMsg is sent after an actual sync (apply) completes.
type syncDoneMsg struct {
	profileName  string
	filesWritten int
	warnings     []domain.Warning
	err          error
}

// profileSavedMsg is sent after a profile YAML write completes.
type profileSavedMsg struct {
	profileName string
	err         error
}

// profileActivatedMsg is sent after sync-config defaults.profile is updated.
type profileActivatedMsg struct {
	profileName string
	err         error
}

// profileCreatedMsg is sent after a new profile is created.
type profileCreatedMsg struct {
	name string
	err  error
}

// profileDeletedMsg is sent after a profile is deleted.
type profileDeletedMsg struct {
	name string
	err  error
}

// packSizesMsg delivers computed file sizes for a single pack.
type packSizesMsg struct {
	packName string
	sizes    map[string]int64
}

// packVersionsLoadedMsg delivers the result of an async ls-remote tag query
// for a single pack. Drives both the inline version display in the details
// panel and the version picker shown before install.
type packVersionsLoadedMsg struct {
	packName string
	versions []app.PackVersion
	err      error
}

// versionsDebounceMsg is dispatched by a tea.Tick after a cursor-move delay
// to fire the deferred version load. The epoch guards against stale ticks:
// if the user moved the cursor again before the tick fired, a newer tick
// is already in flight and this one must be discarded. Without this, each
// keystroke would spawn its own ls-remote while the cursor is still moving.
type versionsDebounceMsg struct {
	epoch int
}

// packDriftLoadedMsg delivers the result of an async pack-drift scan over
// every installed pack. Drives the drift indicator on list rows. The slice
// is the *complete* drift state — not a delta — so the receiving model
// rebuilds its drift set from scratch on each delivery.
type packDriftLoadedMsg struct {
	drifted []app.PackDrift
	err     error
}

// registryLoadedMsg is sent after the registry finishes loading.
type registryLoadedMsg struct {
	items []registryItem
	err   error
}

// syncConfigSavedMsg is sent after sync-config is written to disk.
type syncConfigSavedMsg struct {
	err     error
	syncCfg config.SyncConfig
}

// syncToggleHarnessMsg signals rootModel to toggle a harness in the sync config.
type syncToggleHarnessMsg struct{ harness string }

// syncCycleScopeMsg signals rootModel to cycle the sync scope.
type syncCycleScopeMsg struct{}

// syncCycleCollisionMsg signals rootModel to cycle the collision strategy.
type syncCycleCollisionMsg struct{}

// dialogResultMsg carries the outcome of a dialog overlay.
type dialogResultMsg struct {
	id        string
	confirmed bool
	value     string
	values    []string // for checklist dialogs: individually selected items
}

// toolPickerResultMsg carries the outcome of the MCP tool picker. Tools not
// present in either ask or auto are "off" — disabled_tools is computed at
// save time by comparing the user's selection against pack manifest defaults.
type toolPickerResultMsg struct {
	id        string
	confirmed bool
	ask       []string // items in the "ask" state → allowed_tools
	auto      []string // items in the "auto" state → always_allowed_tools
	bulk      bool     // when true, save then chain to the bulk action menu
}

// toolPickerRefreshMsg is emitted by the picker when the user presses `r`.
// The root handler invalidates the cache for the current server and
// re-fires the probe; the picker stays open in loading state.
type toolPickerRefreshMsg struct {
	id string
}

// profileItem holds the state for a single profile in the list.
type profileItem struct {
	name         string
	path         string
	isActive     bool
	syncState    syncStatus
	syncTarget   syncTargetInfo
	syncErrText  string // non-empty if sync check failed
	syncWarnings []domain.Warning
	cfg          config.ProfileConfig
	tree         *treeModel
	treeErr      string // non-empty if tree building failed
	dirty        bool   // true if this profile's config was modified in-memory
}

// previewRequestMsg signals the root model to open a file preview overlay.
type previewRequestMsg struct {
	title    string
	category domain.PackCategory
	packName string
	filePath string
}

// previewLoadedMsg carries the result of an async file read for preview.
type previewLoadedMsg struct {
	title       string
	category    domain.PackCategory
	packName    string
	filePath    string
	frontmatter []fmEntry // parsed YAML frontmatter key-value pairs
	body        string
	err         error
}

// fmEntry is a single frontmatter key-value pair, preserving YAML order.
type fmEntry struct {
	key   string
	value string
}

// requestAddPackMsg signals rootModel to open the add-pack dialog from the pack roster.
type requestAddPackMsg struct{}

// editorFinishedMsg is sent when the $EDITOR process exits.
type editorFinishedMsg struct {
	filePath string
	err      error
}

// systemOpenErrorMsg is sent when the OS open command fails to start.
// Success is silent — the opened app runs detached, so no completion event.
type systemOpenErrorMsg struct {
	err error
}

// packInstalledMsg is sent after a pack is installed via the packs tab.
type packInstalledMsg struct {
	name string
	err  error
}

// packInstallReadyMsg signals that a streaming pack-install has started.
// The worker goroutine fills eventCh as PackInstall emits phase events;
// once eventCh closes, the terminal outcome lands on resultCh. cancel is
// the ctx cancellation handle the Esc handler invokes.
type packInstallReadyMsg struct {
	eventCh  <-chan app.PackInstallEvent
	resultCh <-chan packInstallResult
	cancel   context.CancelFunc
	packName string // best-effort label for the status bar
}

// packInstallResult carries the terminal outcome of a streaming install
// from the worker goroutine to the TUI loop, before being lifted into
// packInstalledMsg by readNextInstallEvent.
type packInstallResult struct {
	name string
	err  error
}

// packInstallProgressMsg carries one phase event from a streaming install.
type packInstallProgressMsg struct {
	event app.PackInstallEvent
}

// packRemovedMsg is sent after a pack is removed via the packs tab.
type packRemovedMsg struct {
	name string
	err  error
}

// packUpdatedMsg is sent after a pack is updated via the packs tab.
type packUpdatedMsg struct {
	name    string
	results []app.PackUpdateResult
	err     error
}

// packUpdateReadyMsg signals that a streaming pack-update operation has
// started. The worker goroutine fills eventCh as PackUpdate emits progress
// events; once eventCh closes, the terminal outcome lands on resultCh.
// cancel is the ctx cancellation handle the Esc handler invokes.
type packUpdateReadyMsg struct {
	eventCh  <-chan app.PackUpdateEvent
	resultCh <-chan packUpdateResult
	cancel   context.CancelFunc
	packName string
}

// packProgressMsg carries one event from a streaming pack-update.
type packProgressMsg struct {
	event app.PackUpdateEvent
}

// bundledApprovedMsg is sent after the user confirms bundled content candidates via the checklist.
type bundledApprovedMsg struct {
	err error
}

// savePlanMsg is sent after a dry-run round-trip save completes, carrying
// plan entries for the save plan view.
type savePlanMsg struct {
	profileName string
	ops         []app.PlanOp
	warnings    []domain.Warning
	err         error
}

// saveDoneMsg is sent after a round-trip save (harness → packs) completes.
type saveDoneMsg struct {
	profileName string
	saved       int
	unchanged   int
	warnings    []domain.Warning
	err         error
}

// savePlanContext stashes the profile info needed to execute a save after
// the user confirms in the save plan view.
type savePlanContext struct {
	profileName string
}

// syncTabSnapshot holds the active profile's sync state,
// passed from rootModel to syncTabModel to avoid duplication.
type syncTabSnapshot struct {
	syncState    syncStatus
	syncTarget   syncTargetInfo
	syncErrText  string
	syncWarnings []domain.Warning
}

// saveToPackMsg is sent after saving a file to a pack completes.
type saveToPackMsg struct {
	harnessPath string
	packName    string
	err         error
}

// moveToPackMsg is sent after moving a content item between packs.
type moveToPackMsg struct {
	id       string // content ID (e.g. "triage")
	category domain.PackCategory
	fromPack string
	toPack   string
	err      error
}

// searchResultsMsg delivers search results from the index DB.
type searchResultsMsg struct {
	results []app.SearchResult
	err     error
}

// mcpProbeResultMsg delivers the result of an async MCP server probe
// (tools/list via JSON-RPC). Received while the tri-state picker dialog is
// open in loading state. On success the picker rebuilds its items from the
// live tool list; on failure it falls back to the static pack inventory.
type mcpProbeResultMsg struct {
	key   mcpProbeKey
	seq   int
	tools []string
	err   error
}

// mcpProbeStreamResult carries the terminal outcome of a streaming MCP
// probe from the worker goroutine to the TUI loop, before being lifted
// into mcpProbeResultMsg by readNextProbeEvent.
type mcpProbeStreamResult struct {
	tools []string
	err   error
}

// mcpProbeReadyMsg signals that a streaming MCP probe has started. Issued
// synchronously from probeMCPServer; the worker goroutine fills eventCh
// with ProbeEvent values as the handshake/listing progresses, then closes
// eventCh and delivers the terminal result on resultCh.
type mcpProbeReadyMsg struct {
	key      mcpProbeKey
	seq      int
	eventCh  <-chan mcp.ProbeEvent
	resultCh <-chan mcpProbeStreamResult
	cancel   context.CancelFunc
}

// mcpProbeProgressMsg carries one ProbeEvent for the active probe.
// The picker's loading view reads event.Phase to render the current phase
// (starting / connected / listing_tools).
type mcpProbeProgressMsg struct {
	key   mcpProbeKey
	seq   int
	event mcp.ProbeEvent
}

type mcpInventorySavedMsg struct {
	key mcpProbeKey
	err error
}

// searchInstallMsg is sent after a pack is installed from the search tab.
type searchInstallMsg struct {
	name string
	err  error
}

// packCreatedMsg is sent after a new pack is scaffolded and registered.
type packCreatedMsg struct {
	name string
	err  error
}

// ---------------------------------------------------------------------------
// Save pipeline messages
// ---------------------------------------------------------------------------

// harnessDetectedMsg delivers available harnesses for save stage 1.
type harnessDetectedMsg struct {
	harnesses []domain.Harness
}

// vectorsDiscoveredMsg delivers available content vectors for save stage 2.
type vectorsDiscoveredMsg struct {
	vectors []domain.PackCategory
	err     error
}

// saveFilesDiscoveredMsg delivers file candidates for save stage 3.
type saveFilesDiscoveredMsg struct {
	candidates []app.SaveCandidate
	warnings   []string
	err        error
}

// saveFileDeletedMsg delivers the result of deleting a harness file.
type saveFileDeletedMsg struct {
	path string
	err  error
}

// savePipelineDoneMsg delivers the result of pipeline execution.
type savePipelineDoneMsg struct {
	result *app.SavePipelineResult
	err    error
}

// statusClearMsg is sent by a timer to auto-clear transient status text.
type statusClearMsg struct{ id int }

// packRowClearMsg is sent by a per-row timer to revert a pack row's
// update decoration after the auto-clear TTL. The handler checks `when`
// against the row's recorded clearAt to ignore stale ticks (e.g. when
// the row entered a new update before the previous clear fired).
type packRowClearMsg struct {
	packName string
	when     time.Time
}

// syncStatus represents the sync state of a profile.
type syncStatus int

const (
	syncPending syncStatus = iota
	syncLoading
	syncSynced
	syncUnsynced
	syncError
)
