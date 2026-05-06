package tui

import dialogoverlay "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/overlays/dialog"

type dialogModel = dialogoverlay.Model
type checkItem = dialogoverlay.CheckItem
type listAction = dialogoverlay.ListAction

func newConfirmDialog(id, title string) dialogModel {
	return dialogoverlay.NewConfirm(id, title)
}

func newDestructiveConfirmDialog(id, title string) dialogModel {
	return dialogoverlay.NewDestructiveConfirm(id, title)
}

func newTextInputDialog(id, title string) dialogModel {
	return dialogoverlay.NewTextInput(id, title)
}

func newListSelectDialog(id, title string, items []string) dialogModel {
	return dialogoverlay.NewListSelect(id, title, items)
}

func newChecklistDialog(id, title string, items []checkItem) dialogModel {
	return dialogoverlay.NewChecklist(id, title, items)
}
