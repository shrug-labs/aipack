package tui

import (
	planviewoverlay "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/overlays/planview"
	"github.com/shrug-labs/aipack/internal/app"
)

type planViewModel = planviewoverlay.Model
type diffViewModel = planviewoverlay.DiffViewModel

func newPlanViewModel(width, height int, profileName, projectDir string, ops []app.PlanOp, isSavePlan bool) planViewModel {
	return planviewoverlay.New(width, height, profileName, projectDir, ops, isSavePlan, openFileInEditor)
}

func newDiffViewModel(width, height int, title, dst string) diffViewModel {
	return planviewoverlay.NewDiffViewModel(width, height, title, dst)
}
