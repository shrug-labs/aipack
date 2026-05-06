package profiles

import (
	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/domain"
)

type LoadedMsg struct {
	Items []profileItem
	Err   error
}

type CreatedMsg struct {
	Name string
	Err  error
}

type DeletedMsg struct {
	Name string
	Err  error
}

type ActivatedMsg struct {
	ProfileName string
	Err         error
}

type SavedMsg struct {
	ProfileName string
	Err         error
}

type SyncStatusMsg struct {
	ProfileName string
	Synced      bool
	Target      common.SyncTarget
	Warnings    []domain.Warning
	Err         error
}

type FileSizeMsg struct {
	ProfileName string
	Sizes       map[string]int64
}

type AddPackRequestMsg struct{}
type ActionRequestMsg struct{}
