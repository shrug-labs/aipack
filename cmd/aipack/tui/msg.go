package tui

import (
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
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

// dialogResultMsg carries the outcome of a dialog overlay.
type dialogResultMsg struct {
	id        string
	confirmed bool
	value     string
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

// packAddedMsg is sent after a pack is added via the packs tab.
type packAddedMsg struct {
	name string
	err  error
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
	err       error
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

// syncStatus represents the sync state of a profile.
type syncStatus int

const (
	syncPending syncStatus = iota
	syncLoading
	syncSynced
	syncUnsynced
	syncError
)
