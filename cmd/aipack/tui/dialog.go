package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type dialogKind int

const (
	dialogConfirm dialogKind = iota
	dialogTextInput
	dialogListSelect
	dialogChecklist
)

// listAction maps a key press to an action name for list select dialogs.
type listAction struct {
	key  string // key binding (e.g., "a")
	name string // action name (e.g., "activate")
}

// checkItem represents a single item in a checklist dialog.
type checkItem struct {
	label   string
	checked bool
	danger  bool // renders with warning styling
}

type dialogModel struct {
	kind  dialogKind
	id    string
	title string

	// For confirm dialogs.
	focused int // 0 = yes, 1 = no

	// For text input dialogs.
	textValue string

	// For list select dialogs.
	listItems   []string
	listCursor  int
	listLabels  []string     // per-item annotations displayed before each item
	listActions []listAction // additional key bindings
	listDim     []bool       // per-item dim styling (e.g., sentinels)

	// For checklist dialogs.
	checkItems []checkItem
}

func newConfirmDialog(id, title string) dialogModel {
	return dialogModel{
		kind:  dialogConfirm,
		id:    id,
		title: title,
	}
}

// newDestructiveConfirmDialog creates a confirm dialog that defaults to "No",
// protecting against accidental confirmation of destructive actions.
func newDestructiveConfirmDialog(id, title string) dialogModel {
	return dialogModel{
		kind:    dialogConfirm,
		id:      id,
		title:   title,
		focused: 1, // default to "No"
	}
}

func newTextInputDialog(id, title string) dialogModel {
	return dialogModel{
		kind:  dialogTextInput,
		id:    id,
		title: title,
	}
}

func newListSelectDialog(id, title string, items []string) dialogModel {
	return dialogModel{
		kind:      dialogListSelect,
		id:        id,
		title:     title,
		listItems: items,
	}
}

func newChecklistDialog(id, title string, items []checkItem) dialogModel {
	return dialogModel{
		kind:       dialogChecklist,
		id:         id,
		title:      title,
		checkItems: items,
	}
}

func sendDialogResult(id string, confirmed bool, value string) tea.Cmd {
	return func() tea.Msg {
		return dialogResultMsg{
			id:        id,
			confirmed: confirmed,
			value:     value,
		}
	}
}

func sendChecklistResult(id string, confirmed bool, items []checkItem) tea.Cmd {
	var selected []string
	for _, item := range items {
		if item.checked {
			selected = append(selected, item.label)
		}
	}
	return func() tea.Msg {
		return dialogResultMsg{
			id:        id,
			confirmed: confirmed,
			value:     strings.Join(selected, ","),
			values:    selected,
		}
	}
}

func (d dialogModel) Update(msg tea.Msg) (dialogModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch d.kind {
		case dialogConfirm:
			switch msg.String() {
			case "y":
				return d, sendDialogResult(d.id, true, "")
			case "enter":
				if d.focused == 0 {
					return d, sendDialogResult(d.id, true, "")
				}
				return d, sendDialogResult(d.id, false, "")
			case "n", "esc":
				return d, sendDialogResult(d.id, false, "")
			case "tab", "left", "right", "h", "l":
				d.focused = (d.focused + 1) % 2
			}

		case dialogTextInput:
			switch msg.String() {
			case "enter":
				return d, sendDialogResult(d.id, true, d.textValue)
			case "esc":
				return d, sendDialogResult(d.id, false, "")
			case "backspace":
				if len(d.textValue) > 0 {
					d.textValue = d.textValue[:len(d.textValue)-1]
				}
			default:
				if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
					d.textValue += string(msg.Runes)
				}
			}

		case dialogListSelect:
			switch msg.String() {
			case "enter":
				if len(d.listItems) > 0 {
					return d, sendDialogResult(d.id, true, d.listItems[d.listCursor])
				}
				return d, sendDialogResult(d.id, false, "")
			case "esc":
				return d, sendDialogResult(d.id, false, "")
			case "j", "down":
				if d.listCursor < len(d.listItems)-1 {
					d.listCursor++
				}
			case "k", "up":
				if d.listCursor > 0 {
					d.listCursor--
				}
			default:
				// Check list actions.
				for _, a := range d.listActions {
					if msg.String() == a.key {
						item := ""
						if len(d.listItems) > 0 {
							item = d.listItems[d.listCursor]
						}
						return d, sendDialogResult(d.id, true, a.name+":"+item)
					}
				}
			}

		case dialogChecklist:
			switch msg.String() {
			case " ":
				if len(d.checkItems) > 0 {
					d.checkItems[d.listCursor].checked = !d.checkItems[d.listCursor].checked
				}
			case "enter":
				return d, sendChecklistResult(d.id, true, d.checkItems)
			case "esc":
				return d, sendChecklistResult(d.id, false, d.checkItems)
			case "j", "down":
				if d.listCursor < len(d.checkItems)-1 {
					d.listCursor++
				}
			case "k", "up":
				if d.listCursor > 0 {
					d.listCursor--
				}
			}

		}
	}
	return d, nil
}

func (d dialogModel) View() string {
	var content string

	switch d.kind {
	case dialogConfirm:
		yes := "  Yes  "
		no := "  No   "
		if d.focused == 0 {
			yes = selectedStyle.Render("> Yes <")
		} else {
			no = selectedStyle.Render("> No  <")
		}
		content = fmt.Sprintf("%s\n\n  %s    %s",
			dialogTitleStyle.Render(d.title), yes, no)

	case dialogTextInput:
		cursor := "█"
		content = fmt.Sprintf("%s\n\n  %s%s\n\n  %s",
			dialogTitleStyle.Render(d.title),
			d.textValue, cursor,
			dimStyle.Render("enter:confirm  esc:cancel"))

	case dialogListSelect:
		content = dialogTitleStyle.Render(d.title) + "\n\n"
		for i, item := range d.listItems {
			prefix := "  "
			if i == d.listCursor {
				prefix = "> "
				item = selectedStyle.Render(item)
			} else if i < len(d.listDim) && d.listDim[i] {
				item = dimStyle.Render(item)
			}

			// Prepend annotation (e.g., active dot) if available.
			annotation := ""
			if i < len(d.listLabels) {
				annotation = d.listLabels[i]
			}

			content += prefix + annotation + item + "\n"
		}

	case dialogChecklist:
		content = dialogTitleStyle.Render(d.title) + "\n\n"
		for i, item := range d.checkItems {
			prefix := "  "
			if i == d.listCursor {
				prefix = "> "
			}
			check := "[ ]"
			if item.checked {
				check = "[x]"
			}
			label := item.label
			if item.danger {
				label = errorStyle.Render(label)
				check = errorStyle.Render(check)
			} else if i == d.listCursor {
				label = selectedStyle.Render(label)
			}
			content += prefix + check + " " + label + "\n"
		}

	}

	width := max(lipgloss.Width(content)+6, 30)

	return dialogBorderStyle.Width(width).Render(content)
}

// helpText returns context-sensitive help for the dialog.
func (d dialogModel) helpText() string {
	switch d.kind {
	case dialogListSelect:
		parts := []string{"enter:select", "esc:cancel"}
		for _, a := range d.listActions {
			parts = append(parts, a.key+":"+a.name)
		}
		return strings.Join(parts, "  ")
	case dialogChecklist:
		return "space:toggle  enter:confirm  esc:cancel"
	default:
		return "enter:confirm  esc:cancel"
	}
}
