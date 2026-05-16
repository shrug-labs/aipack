package dialog

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
)

var (
	dialogBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("205")).
				Padding(1, 2)
	dialogTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
)

type Kind int

const (
	Confirm Kind = iota
	TextInput
	ListSelect
	Checklist
)

// ListAction maps a key press to an action name for list select dialogs.
type ListAction struct {
	Key  string // key binding (e.g., "a")
	Name string // action name (e.g., "activate")
}

// CheckItem represents a single item in a checklist dialog.
type CheckItem struct {
	Label   string
	Checked bool
	Danger  bool // renders with warning styling
}

type ResultMsg struct {
	ID        string
	Confirmed bool
	Value     string
	Values    []string
}

type Model struct {
	Kind   Kind
	ID     string
	Title  string
	width  int
	height int

	// For confirm dialogs.
	Focused int // 0 = yes, 1 = no

	// For text input dialogs.
	TextValue string

	// For list select dialogs.
	ListItems   []string
	ListCursor  int
	ListLabels  []string     // per-item annotations displayed before each item
	ListActions []ListAction // additional key bindings
	ListDim     []bool       // per-item dim styling (e.g., sentinels)

	// For checklist dialogs.
	CheckItems []CheckItem
}

func NewConfirm(id, title string) Model {
	return Model{
		Kind:  Confirm,
		ID:    id,
		Title: title,
	}
}

// NewDestructiveConfirm creates a confirm dialog that defaults to "No",
// protecting against accidental confirmation of destructive actions.
func NewDestructiveConfirm(id, title string) Model {
	return Model{
		Kind:    Confirm,
		ID:      id,
		Title:   title,
		Focused: 1, // default to "No"
	}
}

func NewTextInput(id, title string) Model {
	return Model{
		Kind:  TextInput,
		ID:    id,
		Title: title,
	}
}

func NewListSelect(id, title string, items []string) Model {
	return Model{
		Kind:      ListSelect,
		ID:        id,
		Title:     title,
		ListItems: items,
	}
}

func NewChecklist(id, title string, items []CheckItem) Model {
	return Model{
		Kind:       Checklist,
		ID:         id,
		Title:      title,
		CheckItems: items,
	}
}

func sendDialogResult(id string, confirmed bool, value string) tea.Cmd {
	return func() tea.Msg {
		return ResultMsg{
			ID:        id,
			Confirmed: confirmed,
			Value:     value,
		}
	}
}

func sendChecklistResult(id string, confirmed bool, items []CheckItem) tea.Cmd {
	var selected []string
	for _, item := range items {
		if item.Checked {
			selected = append(selected, item.Label)
		}
	}
	return func() tea.Msg {
		return ResultMsg{
			ID:        id,
			Confirmed: confirmed,
			Value:     strings.Join(selected, ","),
			Values:    selected,
		}
	}
}

func (d Model) Init() tea.Cmd { return nil }

func (d Model) SetSize(width, height int) common.Screen {
	return d.SetSizeModel(width, height)
}

func (d Model) SetSizeModel(width, height int) Model {
	d.width = width
	d.height = height
	d.clampCursor()
	return d
}

func (d Model) Update(msg tea.Msg) (common.Screen, tea.Cmd) {
	return d.UpdateModel(msg)
}

func (d Model) UpdateModel(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case common.LayerHitMsg:
		return d.handleLayerHit(msg)
	case tea.PasteMsg:
		if d.Kind == TextInput {
			d.TextValue = common.AppendSingleLinePaste(d.TextValue, msg.Content)
		}
		return d, nil
	case tea.KeyPressMsg:
		switch d.Kind {
		case Confirm:
			switch msg.String() {
			case "y":
				return d, sendDialogResult(d.ID, true, "")
			case "enter":
				if d.Focused == 0 {
					return d, sendDialogResult(d.ID, true, "")
				}
				return d, sendDialogResult(d.ID, false, "")
			case "n", "esc":
				return d, sendDialogResult(d.ID, false, "")
			case "tab", "left", "right", "h", "l":
				d.Focused = (d.Focused + 1) % 2
			}

		case TextInput:
			switch msg.String() {
			case "enter":
				return d, sendDialogResult(d.ID, true, d.TextValue)
			case "esc":
				return d, sendDialogResult(d.ID, false, "")
			case "backspace":
				if len(d.TextValue) > 0 {
					d.TextValue = d.TextValue[:len(d.TextValue)-1]
				}
			default:
				if msg.Text != "" {
					d.TextValue += msg.Text
				}
			}

		case ListSelect:
			switch msg.String() {
			case "enter":
				if len(d.ListItems) > 0 {
					d.clampCursor()
					return d, sendDialogResult(d.ID, true, d.ListItems[d.ListCursor])
				}
				return d, sendDialogResult(d.ID, false, "")
			case "esc":
				return d, sendDialogResult(d.ID, false, "")
			case "j", "down":
				if d.ListCursor < len(d.ListItems)-1 {
					d.ListCursor++
				}
			case "k", "up":
				if d.ListCursor > 0 {
					d.ListCursor--
				}
			default:
				// Check list actions.
				for _, a := range d.ListActions {
					if msg.String() == a.Key {
						item := ""
						if len(d.ListItems) > 0 {
							item = d.ListItems[d.ListCursor]
						}
						return d, sendDialogResult(d.ID, true, a.Name+":"+item)
					}
				}
			}

		case Checklist:
			switch msg.String() {
			case "space":
				if len(d.CheckItems) > 0 {
					d.clampCursor()
					d.CheckItems[d.ListCursor].Checked = !d.CheckItems[d.ListCursor].Checked
				}
			case "enter":
				return d, sendChecklistResult(d.ID, true, d.CheckItems)
			case "esc":
				return d, sendChecklistResult(d.ID, false, d.CheckItems)
			case "j", "down":
				if d.ListCursor < len(d.CheckItems)-1 {
					d.ListCursor++
				}
			case "k", "up":
				if d.ListCursor > 0 {
					d.ListCursor--
				}
			}

		}
	}
	return d, nil
}

func (d Model) handleLayerHit(msg common.LayerHitMsg) (Model, tea.Cmd) {
	if !strings.HasPrefix(msg.ID, "dialog:") {
		return d, nil
	}
	isClick := common.IsLeftClick(msg.Mouse)
	_, isMotion := msg.Mouse.(tea.MouseMotionMsg)
	if !isClick && !isMotion {
		return d, nil
	}

	switch msg.ID {
	case "dialog:confirm", "dialog:yes":
		if !isClick {
			return d, nil
		}
		switch d.Kind {
		case Confirm:
			return d, sendDialogResult(d.ID, true, "")
		case TextInput:
			return d, sendDialogResult(d.ID, true, d.TextValue)
		case ListSelect:
			if len(d.ListItems) > 0 {
				return d, sendDialogResult(d.ID, true, d.ListItems[d.ListCursor])
			}
			return d, sendDialogResult(d.ID, false, "")
		case Checklist:
			return d, sendChecklistResult(d.ID, true, d.CheckItems)
		}
	case "dialog:cancel", "dialog:no", "dialog:close":
		if !isClick {
			return d, nil
		}
		switch d.Kind {
		case Checklist:
			return d, sendChecklistResult(d.ID, false, d.CheckItems)
		default:
			return d, sendDialogResult(d.ID, false, "")
		}
	default:
		if index, ok := common.ParseLayerIndex(msg.ID, "dialog:item:"); ok {
			if index < 0 {
				return d, nil
			}
			switch d.Kind {
			case ListSelect:
				if index >= 0 && index < len(d.ListItems) {
					d.ListCursor = index
					if !isClick {
						return d, nil
					}
					return d, sendDialogResult(d.ID, true, d.ListItems[d.ListCursor])
				}
			case Checklist:
				if index >= 0 && index < len(d.CheckItems) {
					d.ListCursor = index
					if !isClick {
						return d, nil
					}
					d.CheckItems[index].Checked = !d.CheckItems[index].Checked
				}
			}
		}
	}
	return d, nil
}

func (d Model) View() *lipgloss.Layer {
	root := lipgloss.NewLayer(d.Render())
	root.AddLayers(lipgloss.NewLayer("[close]").ID("dialog:close").X(4).Y(1).Z(-1))
	switch d.Kind {
	case Confirm:
		root.AddLayers(
			lipgloss.NewLayer(" Yes ").ID("dialog:yes").X(4).Y(4).Z(-1),
			lipgloss.NewLayer(" No ").ID("dialog:no").X(13).Y(4).Z(-1),
		)
	case TextInput:
		root.AddLayers(
			lipgloss.NewLayer("[ok]").ID("dialog:confirm").X(4).Y(6).Z(-1),
			lipgloss.NewLayer("[cancel]").ID("dialog:cancel").X(9).Y(6).Z(-1),
		)
	case ListSelect:
		start, end := d.visibleRange(len(d.ListItems))
		yBase := 4
		if start > 0 {
			yBase++
		}
		for i, item := range d.ListItems[start:end] {
			index := start + i
			root.AddLayers(lipgloss.NewLayer(item).ID(fmt.Sprintf("dialog:item:%d", index)).X(4).Y(yBase + i).Z(-1))
		}
		confirmY := yBase + end - start + 1
		if end < len(d.ListItems) {
			confirmY++
		}
		root.AddLayers(lipgloss.NewLayer("[select]").ID("dialog:confirm").X(4).Y(confirmY).Z(-1))
	case Checklist:
		start, end := d.visibleRange(len(d.CheckItems))
		yBase := 4
		if start > 0 {
			yBase++
		}
		for i, item := range d.CheckItems[start:end] {
			index := start + i
			root.AddLayers(lipgloss.NewLayer(item.Label).ID(fmt.Sprintf("dialog:item:%d", index)).X(8).Y(yBase + i).Z(-1))
		}
		confirmY := yBase + end - start + 1
		if end < len(d.CheckItems) {
			confirmY++
		}
		root.AddLayers(lipgloss.NewLayer("[confirm]").ID("dialog:confirm").X(4).Y(confirmY).Z(-1))
	}
	return root
}

func (d Model) Render() string {
	var content string

	switch d.Kind {
	case Confirm:
		yes := "  Yes  "
		no := "  No   "
		if d.Focused == 0 {
			yes = common.SelectedStyle.Render("> Yes <")
		} else {
			no = common.SelectedStyle.Render("> No  <")
		}
		content = fmt.Sprintf("%s\n\n  %s    %s",
			dialogTitleStyle.Render(d.Title), yes, no)

	case TextInput:
		cursor := "█"
		content = fmt.Sprintf("%s\n\n  %s%s\n\n  %s",
			dialogTitleStyle.Render(d.Title),
			d.TextValue, cursor,
			common.DimStyle.Render("enter:confirm  esc:cancel"))

	case ListSelect:
		content = dialogTitleStyle.Render(d.Title) + "\n\n"
		start, end := d.visibleRange(len(d.ListItems))
		if start > 0 {
			content += common.DimStyle.Render("  ...") + "\n"
		}
		for i, item := range d.ListItems[start:end] {
			index := start + i
			prefix := "  "
			if index == d.ListCursor {
				prefix = "> "
				item = common.SelectedStyle.Render(item)
			} else if index < len(d.ListDim) && d.ListDim[index] {
				item = common.DimStyle.Render(item)
			}

			// Prepend annotation (e.g., active dot) if available.
			annotation := ""
			if index < len(d.ListLabels) {
				annotation = d.ListLabels[index]
			}

			content += prefix + annotation + item + "\n"
		}
		if end < len(d.ListItems) {
			content += common.DimStyle.Render("  ...") + "\n"
		}

	case Checklist:
		content = dialogTitleStyle.Render(d.Title) + "\n\n"
		start, end := d.visibleRange(len(d.CheckItems))
		if start > 0 {
			content += common.DimStyle.Render("  ...") + "\n"
		}
		for i, item := range d.CheckItems[start:end] {
			index := start + i
			prefix := "  "
			if index == d.ListCursor {
				prefix = "> "
			}
			check := "[ ]"
			if item.Checked {
				check = "[x]"
			}
			label := item.Label
			if item.Danger {
				label = common.ErrorStyle.Render(label)
				check = common.ErrorStyle.Render(check)
			} else if index == d.ListCursor {
				label = common.SelectedStyle.Render(label)
			}
			content += prefix + check + " " + label + "\n"
		}
		if end < len(d.CheckItems) {
			content += common.DimStyle.Render("  ...") + "\n"
		}

	}

	width := max(lipgloss.Width(content)+6, 30)
	if d.width > 0 {
		width = min(width, max(d.width-4, 20))
	}

	return dialogBorderStyle.Width(width).Render(content)
}

func (d Model) visibleRange(total int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	rows := d.availableListRows(total)
	cursor := min(max(d.ListCursor, 0), total-1)
	for {
		start := 0
		if cursor >= rows {
			start = cursor - rows + 1
		}
		if start > 0 {
			start = min(start, max(total-rows, 0))
		}
		reserved := 0
		if start > 0 {
			reserved++
		}
		if start+rows < total {
			reserved++
		}
		nextRows := min(max(d.availableListRows(total)-reserved, 1), total)
		if nextRows == rows {
			return start, min(start+rows, total)
		}
		rows = nextRows
	}
}

func (d Model) availableListRows(total int) int {
	if d.height <= 0 {
		return total
	}
	// Rounded border + vertical padding consume five rows in the rendered
	// block, including the trailing blank line produced by padded content.
	// The title and blank spacer consume two content rows. Ellipsis rows are
	// reserved in visibleRange when the window is scrolled.
	return min(max(d.height-7, 1), total)
}

func (d *Model) clampCursor() {
	count := 0
	switch d.Kind {
	case ListSelect:
		count = len(d.ListItems)
	case Checklist:
		count = len(d.CheckItems)
	default:
		return
	}
	if count == 0 {
		d.ListCursor = 0
		return
	}
	d.ListCursor = min(max(d.ListCursor, 0), count-1)
}

// HelpText returns context-sensitive help for the dialog.
func (d Model) HelpText() string {
	switch d.Kind {
	case ListSelect:
		parts := []string{"enter:select", "esc:cancel"}
		for _, a := range d.ListActions {
			parts = append(parts, a.Key+":"+a.Name)
		}
		return strings.Join(parts, "  ")
	case Checklist:
		return "space:toggle  enter:confirm  esc:cancel"
	default:
		return "enter:confirm  esc:cancel"
	}
}
