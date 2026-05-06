package common

import (
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
)

// SyncStatus is the sync state of a profile as seen across screens.
type SyncStatus int

const (
	SyncStatusPending SyncStatus = iota
	SyncStatusLoading
	SyncStatusSynced
	SyncStatusUnsynced
	SyncStatusError
)

// SyncSnapshot is the cross-screen view of the active profile's sync state.
type SyncSnapshot struct {
	ProfileName string
	Status      SyncStatus
	Target      SyncTarget
	ErrText     string
	Warnings    []domain.Warning
}

// SyncTarget describes what a sync operation would apply.
type SyncTarget struct {
	app.PlanSummary
	Harnesses  []string
	Scope      domain.Scope
	ProjectDir string
}

// SyncDiffRequestMsg is dispatched when a screen needs the sync diff overlay.
type SyncDiffRequestMsg struct {
	ProfileName  string
	ProjectDir   string
	Ops          []app.PlanOp
	Cursor       int
	ScrollOffset int
}
