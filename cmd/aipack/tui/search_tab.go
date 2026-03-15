package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
)

type searchFocus int

const (
	searchFocusInput searchFocus = iota
	searchFocusResults
)

var kindOptions = []string{"", "rule", "skill", "workflow", "agent", "pack"}
var categoryOptions = []string{"", "ops", "dev", "infra", "governance", "meta"}
var installedOptions = []string{"", "installed", "available"}

type searchTabModel struct {
	configDir string

	query   string
	results []app.SearchResult
	focus   searchFocus
	cursor  int
	offset  int // scroll offset for results list
	loading bool
	errText string

	searched bool // true after first search completes

	// Filters.
	kindFilter      string // "" = all, or a specific kind
	categoryFilter  string // "" = all, or a specific category
	installedFilter string // "" = all, "installed", "available"

	width  int
	height int
}

func newSearchTabModel(configDir string) searchTabModel {
	return searchTabModel{
		configDir: configDir,
		focus:     searchFocusInput,
	}
}

func (m searchTabModel) Update(msg tea.Msg) (searchTabModel, tea.Cmd) {
	switch msg := msg.(type) {
	case searchResultsMsg:
		m.loading = false
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.results = nil
		} else {
			m.errText = ""
			m.results = msg.results
		}
		m.searched = true
		m.cursor = 0
		m.offset = 0
		return m, nil

	case tea.KeyMsg:
		switch m.focus {
		case searchFocusInput:
			return m.updateInput(msg)
		case searchFocusResults:
			return m.updateResults(msg)
		}
	}
	return m, nil
}

func (m searchTabModel) updateInput(msg tea.KeyMsg) (searchTabModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.loading = true
		m.focus = searchFocusResults
		return m, runSearch(m.configDir, m.query, m.kindFilter, m.categoryFilter, m.installedFilter)
	case "down":
		if len(m.results) > 0 {
			m.focus = searchFocusResults
		}
		return m, nil
	case "esc":
		if len(m.results) > 0 {
			m.focus = searchFocusResults
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
		if len(msg.String()) == 1 {
			m.query += msg.String()
		}
		return m, nil
	}
}

func (m searchTabModel) updateResults(msg tea.KeyMsg) (searchTabModel, tea.Cmd) {
	switch msg.String() {
	case "/":
		m.focus = searchFocusInput
		return m, nil
	case "esc":
		m.focus = searchFocusInput
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
			m.focus = searchFocusInput
		}
		return m, nil
	case "f":
		for i, k := range kindOptions {
			if k == m.kindFilter {
				m.kindFilter = kindOptions[(i+1)%len(kindOptions)]
				break
			}
		}
		m.loading = true
		return m, runSearch(m.configDir, m.query, m.kindFilter, m.categoryFilter, m.installedFilter)
	case "c":
		for i, c := range categoryOptions {
			if c == m.categoryFilter {
				m.categoryFilter = categoryOptions[(i+1)%len(categoryOptions)]
				break
			}
		}
		m.loading = true
		return m, runSearch(m.configDir, m.query, m.kindFilter, m.categoryFilter, m.installedFilter)
	case " ":
		for i, opt := range installedOptions {
			if opt == m.installedFilter {
				m.installedFilter = installedOptions[(i+1)%len(installedOptions)]
				break
			}
		}
		m.loading = true
		return m, runSearch(m.configDir, m.query, m.kindFilter, m.categoryFilter, m.installedFilter)
	case "i":
		if m.cursor < len(m.results) {
			r := m.results[m.cursor]
			if !r.Installed {
				return m, func() tea.Msg {
					return requestSearchInstallMsg{packName: r.Pack}
				}
			}
		}
		return m, nil
	case "enter":
		if m.cursor < len(m.results) {
			r := m.results[m.cursor]
			fp := app.SearchResultFilePath(r)
			if fp != "" {
				return m, func() tea.Msg {
					return previewRequestMsg{
						title:    r.Name,
						category: kindToCategory(r.Kind),
						packName: r.Pack,
						filePath: fp,
					}
				}
			}
		}
		return m, nil
	}
	return m, nil
}

// requestSearchInstallMsg signals rootModel to open the install-from-search dialog.
type requestSearchInstallMsg struct {
	packName string
}

func (m *searchTabModel) ensureVisible() {
	maxVisible := m.resultAreaHeight()
	if maxVisible < 1 {
		maxVisible = 1
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+maxVisible {
		m.offset = m.cursor - maxVisible + 1
	}
}

func (m searchTabModel) resultAreaHeight() int {
	// Subtract: search line, blank, filter line, blank, footer line.
	h := m.height - 5
	if h < 1 {
		h = 1
	}
	return h
}

func (m searchTabModel) View() string {
	var sb strings.Builder

	// Search input line.
	cursor := ""
	if m.focus == searchFocusInput {
		cursor = "█"
	}
	inputLabel := dimStyle.Render("Search: ")
	if m.focus == searchFocusInput {
		inputLabel = selectedStyle.Render("Search: ")
	}
	sb.WriteString(inputLabel + m.query + cursor + "\n\n")

	// Filter chips.
	kindLabel := "all"
	if m.kindFilter != "" {
		kindLabel = m.kindFilter
	}
	catLabel := "all"
	if m.categoryFilter != "" {
		catLabel = m.categoryFilter
	}
	instLabel := "all"
	if m.installedFilter != "" {
		instLabel = m.installedFilter
	}
	sb.WriteString(dimStyle.Render(fmt.Sprintf("Kind: [%s]  Category: [%s]  Show: [%s]", kindLabel, catLabel, instLabel)) + "\n\n")

	// Results area.
	if m.loading {
		sb.WriteString(dimStyle.Render("searching..."))
		return contentStyle.Render(sb.String())
	}
	if m.errText != "" {
		sb.WriteString(errorStyle.Render("Error: " + m.errText))
		return contentStyle.Render(sb.String())
	}
	if !m.searched {
		sb.WriteString(dimStyle.Render("type a query and press enter to search"))
		return contentStyle.Render(sb.String())
	}
	if len(m.results) == 0 {
		sb.WriteString(dimStyle.Render("no results"))
		return contentStyle.Render(sb.String())
	}

	// Compute column widths.
	maxKind, maxPack := 0, 0
	for _, r := range m.results {
		if len(r.Kind) > maxKind {
			maxKind = len(r.Kind)
		}
		if len(r.Pack) > maxPack {
			maxPack = len(r.Pack)
		}
	}

	maxVisible := m.resultAreaHeight()
	end := m.offset + maxVisible
	if end > len(m.results) {
		end = len(m.results)
	}

	for i := m.offset; i < end; i++ {
		r := m.results[i]
		prefix := "  "
		isSelected := m.focus == searchFocusResults && i == m.cursor

		installedMark := dimStyle.Render("✗")
		if r.Installed {
			installedMark = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("✓")
		}

		kind := fmt.Sprintf("%-*s", maxKind, r.Kind)
		pack := fmt.Sprintf("%-*s", maxPack, r.Pack)

		if isSelected {
			prefix = "> "
			line := selectedStyle.Render(prefix+kind+"  "+pack+"  "+r.Name) + "  " + installedMark
			sb.WriteString(line + "\n")
		} else {
			sb.WriteString(fmt.Sprintf("%s%s  %s  %s  %s\n",
				prefix,
				searchKindStyle(r.Kind).Render(kind),
				dimStyle.Render(pack),
				r.Name,
				installedMark))
		}
		// Show body snippet on a second line when available.
		if r.Snippet != "" {
			snippet := r.Snippet
			if len(snippet) > m.width-6 && m.width > 10 {
				snippet = snippet[:m.width-9] + "..."
			}
			sb.WriteString("    " + dimStyle.Render(snippet) + "\n")
		}
	}

	// Footer with key hints.
	footer := fmt.Sprintf("%d result(s)", len(m.results))
	if m.focus == searchFocusResults {
		footer += "  /search  f:kind  c:category  space:installed  enter:preview  i:install"
	}
	sb.WriteString(dimStyle.Render("\n" + footer))

	return contentStyle.Render(sb.String())
}

var kindStyles = map[string]lipgloss.Style{
	"rule":     lipgloss.NewStyle().Foreground(lipgloss.Color("75")),
	"skill":    lipgloss.NewStyle().Foreground(lipgloss.Color("114")),
	"workflow": lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
	"agent":    lipgloss.NewStyle().Foreground(lipgloss.Color("141")),
	"pack":     lipgloss.NewStyle().Foreground(lipgloss.Color("204")),
}

func searchKindStyle(kind string) lipgloss.Style {
	if s, ok := kindStyles[kind]; ok {
		return s
	}
	return dimStyle
}

func kindToCategory(kind string) domain.PackCategory {
	cat, _ := domain.ParseSingularLabel(kind)
	return cat
}
