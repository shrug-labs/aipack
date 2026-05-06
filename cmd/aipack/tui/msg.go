package tui

import (
	"context"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	dialogoverlay "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/overlays/dialog"
	pickeroverlay "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/overlays/picker"
	planviewoverlay "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/overlays/planview"
	packsscreen "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/packs"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/mcp"
)

type packsLoadedMsg = packsscreen.LoadedMsg
type packSizesMsg = packsscreen.SizesMsg
type packVersionsLoadedMsg = packsscreen.VersionsLoadedMsg
type versionsDebounceMsg = packsscreen.DebounceMsg
type packDriftLoadedMsg = packsscreen.DriftLoadedMsg
type registryLoadedMsg = packsscreen.RegistryLoadedMsg
type indexLoadedMsg = packsscreen.IndexLoadedMsg

type diffLoadedMsg = planviewoverlay.DiffLoadedMsg

// syncDoneMsg is sent after an actual sync (apply) completes.
type syncDoneMsg struct {
	profileName  string
	filesWritten int
	warnings     []domain.Warning
	err          error
}

// syncConfigSavedMsg is sent after sync-config is written to disk.
type syncConfigSavedMsg struct {
	err     error
	syncCfg config.SyncConfig
}

type envSetMsg struct {
	key string
	err error
}

type envUnsetMsg struct {
	key string
	err error
}

type dialogResultMsg = dialogoverlay.ResultMsg

type toolPickerResultMsg = pickeroverlay.ResultMsg
type toolPickerRefreshMsg = pickeroverlay.RefreshMsg

type previewLoadedMsg = common.PreviewLoadedMsg
type fmEntry = common.FrontmatterEntry

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

type packInstalledMsg = packsscreen.InstalledMsg
type packInstallReadyMsg = packsscreen.InstallReadyMsg
type packInstallResult = packsscreen.InstallResult
type packInstallProgressMsg = packsscreen.InstallProgressMsg
type packRemovedMsg = packsscreen.RemovedMsg
type packUpdatedMsg = packsscreen.UpdatedMsg
type packUpdateReadyMsg = packsscreen.UpdateReadyMsg
type packUpdateResult = packsscreen.UpdateResult
type packProgressMsg = packsscreen.ProgressMsg

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

// packCreatedMsg is sent after a new pack is scaffolded and registered.
type packCreatedMsg = packsscreen.CreatedMsg

// statusClearMsg is sent by a timer to auto-clear transient status text.
type statusClearMsg struct{ id int }

// packRowClearMsg is sent by a per-row timer to revert a pack row's
// update decoration after the auto-clear TTL. The handler checks `when`
// against the row's recorded clearAt to ignore stale ticks (e.g. when
// the row entered a new update before the previous clear fired).
type packRowClearMsg = packsscreen.RowClearMsg
