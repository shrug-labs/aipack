package search

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
)

func (m Model) Update(msg tea.Msg) (common.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case ResultsMsg:
		selected := resultKey{}
		if msg.PreserveSelection {
			selected, _ = m.currentResultKey()
		}
		m.loading = false
		if msg.Err != nil {
			m.errText = msg.Err.Error()
			m.results = nil
		} else {
			m.errText = ""
			m.results = msg.Results
		}
		m.maxKindWidth, m.maxPackWidth = computeColumnWidths(m.results)
		m.searched = true
		if msg.PreserveSelection {
			m.restoreSelection(selected)
		} else {
			m.cursor = 0
			m.offset = 0
		}
		return m, nil

	case common.FocusMsg:
		return m.Preload()

	case common.BlurMsg:
		return m, nil

	case common.LayerHitMsg:
		if !strings.HasPrefix(msg.ID, "search:") {
			return m, nil
		}
		return m.handleLayerHit(msg)

	case tea.PasteMsg:
		if m.focus == focusInput {
			m.query = common.AppendSingleLinePaste(m.query, msg.Content)
		}
		return m, nil

	case tea.KeyPressMsg:
		switch m.focus {
		case focusInput:
			return m.updateInput(msg)
		case focusResults:
			return m.updateResults(msg)
		}
	}
	return m, nil
}

type resultKey struct {
	pack string
	kind string
	name string
}

func (m Model) currentResultKey() (resultKey, bool) {
	if m.cursor < 0 || m.cursor >= len(m.results) {
		return resultKey{}, false
	}
	r := m.results[m.cursor]
	return resultKey{pack: r.Pack, kind: r.Kind, name: r.Name}, true
}

func (m *Model) restoreSelection(selected resultKey) {
	if len(m.results) == 0 {
		m.cursor = 0
		m.offset = 0
		return
	}
	if selected != (resultKey{}) {
		for i, r := range m.results {
			if r.Pack == selected.pack && r.Kind == selected.kind && r.Name == selected.name {
				m.cursor = i
				m.ensureVisible()
				return
			}
		}
	}
	m.cursor = min(m.cursor, len(m.results)-1)
	m.ensureVisible()
}

func (m Model) updateInput(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.loading = true
		m.focus = focusResults
		return m, RunSearch(m.configDir, m.query, m.kindFilter, m.categoryFilter, m.installedFilter)
	case "down":
		if len(m.results) > 0 {
			m.focus = focusResults
		}
		return m, nil
	case "esc":
		if len(m.results) > 0 {
			m.focus = focusResults
		}
		return m, nil
	case "backspace":
		if len(m.query) > 0 {
			m.query = m.query[:len(m.query)-1]
		}
		return m, nil
	case "ctrl+u":
		m.query = ""
		return m, nil
	default:
		if msg.Text != "" {
			m.query += msg.Text
		}
		return m, nil
	}
}

func (m Model) updateResults(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "/":
		m.focus = focusInput
		return m, nil
	case "esc":
		m.focus = focusInput
		return m, nil
	case "j", "down":
		if m.cursor < len(m.results)-1 {
			m.cursor++
			m.ensureVisible()
		}
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			m.ensureVisible()
		} else {
			m.focus = focusInput
		}
		return m, nil
	case "f":
		return m.cycleKind()
	case "c":
		return m.cycleCategory()
	case "space":
		return m.cycleInstalled()
	case "p":
		if m.cursor < len(m.results) && m.results[m.cursor].Installed {
			return m, previewResult(m.results[m.cursor])
		}
		return m, nil
	case "enter":
		return m, m.activateCurrent()
	}
	return m, nil
}

func (m *Model) ensureVisible() {
	maxVisible := max(m.resultAreaHeight(), 1)
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	for m.offset < m.cursor && m.visibleLineCount(m.offset, m.cursor+1) > maxVisible {
		m.offset++
	}
}

func (m Model) visibleLineCount(start, end int) int {
	lines := 0
	for i := start; i < end && i < len(m.results); i++ {
		lines += resultRowHeight(m.results[i])
	}
	return lines
}

func (m Model) handleLayerHit(msg common.LayerHitMsg) (common.Screen, tea.Cmd) {
	mouse := msg.Mouse.Mouse()
	switch msg.Mouse.(type) {
	case tea.MouseClickMsg:
		if mouse.Button != tea.MouseLeft {
			return m, nil
		}
	case tea.MouseMotionMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg:
		return m, nil
	default:
		return m, nil
	}

	switch {
	case msg.ID == "search:input":
		m.focus = focusInput
		return m, nil
	case msg.ID == "search:filter:kind":
		return m.cycleKind()
	case msg.ID == "search:filter:category":
		return m.cycleCategory()
	case msg.ID == "search:filter:installed":
		return m.cycleInstalled()
	case strings.HasPrefix(msg.ID, "search:result:"):
		idx, ok := common.ParseLayerIndex(msg.ID, "search:result:")
		if !ok || idx >= len(m.results) {
			return m, nil
		}
		m.cursor = idx
		m.focus = focusResults
		m.ensureVisible()
		return m, m.activateCurrent()
	default:
		return m, nil
	}
}

func (m Model) cycleKind() (Model, tea.Cmd) {
	for i, k := range kindOptions {
		if k == m.kindFilter {
			m.kindFilter = kindOptions[(i+1)%len(kindOptions)]
			break
		}
	}
	m.loading = true
	return m, RunSearch(m.configDir, m.query, m.kindFilter, m.categoryFilter, m.installedFilter)
}

func (m Model) cycleCategory() (Model, tea.Cmd) {
	for i, c := range categoryOptions {
		if c == m.categoryFilter {
			m.categoryFilter = categoryOptions[(i+1)%len(categoryOptions)]
			break
		}
	}
	m.loading = true
	return m, RunSearch(m.configDir, m.query, m.kindFilter, m.categoryFilter, m.installedFilter)
}

func (m Model) cycleInstalled() (Model, tea.Cmd) {
	for i, opt := range installedOptions {
		if opt == m.installedFilter {
			m.installedFilter = installedOptions[(i+1)%len(installedOptions)]
			break
		}
	}
	m.loading = true
	return m, RunSearch(m.configDir, m.query, m.kindFilter, m.categoryFilter, m.installedFilter)
}

func (m Model) activateCurrent() tea.Cmd {
	if m.cursor >= len(m.results) {
		return nil
	}
	r := m.results[m.cursor]
	return openPackResult(r)
}

func openPackResult(r app.SearchResult) tea.Cmd {
	if r.Pack == "" {
		return nil
	}
	return func() tea.Msg {
		msg := OpenPackMsg{PackName: r.Pack}
		if r.Kind != "pack" {
			msg.Kind = r.Kind
			msg.Name = r.Name
		}
		return msg
	}
}

func previewResult(r app.SearchResult) tea.Cmd {
	fp := app.SearchResultFilePath(r)
	if fp == "" {
		return nil
	}
	return func() tea.Msg {
		return common.PreviewRequestMsg{
			Title:    r.Name,
			Category: kindToCategory(r.Kind),
			PackName: r.Pack,
			FilePath: fp,
		}
	}
}

func kindToCategory(kind string) domain.PackCategory {
	cat, _ := domain.ParseSingularLabel(kind)
	return cat
}
