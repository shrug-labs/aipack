package packs

import (
	"context"
	"time"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
)

type LoadedMsg struct {
	Items []app.PackShowEntry
	Err   error
}

type SizesMsg struct {
	PackName string
	Sizes    map[string]int64
}

type VersionsLoadedMsg struct {
	PackName string
	Versions []app.PackVersion
	Err      error
}

type DebounceMsg struct {
	Epoch int
}

type DriftLoadedMsg struct {
	Drifted []app.PackDrift
	Err     error
}

type RegistryLoadedMsg struct {
	Items []RegistryItem
	Err   error
}

type IndexLoadedMsg struct {
	Items []app.IndexedPackDetail
	Err   error
}

type RegistryItem struct {
	Name        string
	Description string
	Repo        string
	Path        string
	Ref         string
	Owner       string
}

type ActionRequestMsg struct{}

type InstalledMsg struct {
	Name string
	Err  error
}

type InstallReadyMsg struct {
	EventCh  <-chan app.PackInstallEvent
	ResultCh <-chan InstallResult
	Cancel   context.CancelFunc
	PackName string
}

type InstallResult struct {
	Name string
	Err  error
}

type InstallProgressMsg struct {
	Event app.PackInstallEvent
}

type RemovedMsg struct {
	Name string
	Err  error
}

type UpdatedMsg struct {
	Name    string
	Results []app.PackUpdateResult
	Err     error
}

type UpdateReadyMsg struct {
	EventCh  <-chan app.PackUpdateEvent
	ResultCh <-chan UpdateResult
	Cancel   context.CancelFunc
	PackName string
}

type UpdateResult struct {
	Results []app.PackUpdateResult
	Err     error
}

type ProgressMsg struct {
	Event app.PackUpdateEvent
}

type CreatedMsg struct {
	Name string
	Err  error
}

type RowClearMsg struct {
	PackName string
	When     time.Time
}

type packsLoadedMsg = LoadedMsg
type packSizesMsg = SizesMsg
type packVersionsLoadedMsg = VersionsLoadedMsg
type versionsDebounceMsg = DebounceMsg
type packDriftLoadedMsg = DriftLoadedMsg
type registryLoadedMsg = RegistryLoadedMsg
type indexLoadedMsg = IndexLoadedMsg
type previewLoadedMsg = common.PreviewLoadedMsg
type packInstalledMsg = InstalledMsg
type packInstallReadyMsg = InstallReadyMsg
type packInstallResult = InstallResult
type packInstallProgressMsg = InstallProgressMsg
type packUpdatedMsg = UpdatedMsg
type packUpdateReadyMsg = UpdateReadyMsg
type packUpdateResult = UpdateResult
type packProgressMsg = ProgressMsg
type packRowClearMsg = RowClearMsg
