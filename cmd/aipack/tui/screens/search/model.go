package search

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
)

type focus int

const (
	focusInput focus = iota
	focusResults
)

const (
	searchStatusInstalled  = "installed"
	searchStatusRegistered = "registered"
	searchStatusInspected  = "inspected"
	searchStatusAvailable  = "available"
)

var kindOptions = []string{
	"",
	domain.CategoryRules.SingularLabel(),
	domain.CategorySkills.SingularLabel(),
	domain.CategoryWorkflows.SingularLabel(),
	domain.CategoryAgents.SingularLabel(),
	domain.CategoryHooks.SingularLabel(),
	"pack",
}
var categoryOptions = []string{"", "ops", "dev", "infra", "governance", "meta"}
var installedOptions = []string{"", searchStatusInstalled, searchStatusRegistered, searchStatusInspected, searchStatusAvailable}

type Model struct {
	configDir string

	query   string
	results []app.SearchResult
	// Column widths cached at ResultsMsg time so View() doesn't re-walk all
	// results on every render. Recomputed only when results mutate.
	maxKindWidth int
	maxPackWidth int
	focus        focus
	cursor       int
	offset       int // scroll offset for results list
	loading      bool
	errText      string

	searched bool // true after first search completes

	// Filters.
	kindFilter      string // "" = all, or a specific kind
	categoryFilter  string // "" = all, or a specific category
	installedFilter string // "" = all, or one of "installed", "registered", "inspected", "available"

	width  int
	height int
}

func New(configDir string) Model {
	return Model{
		configDir: configDir,
		focus:     focusInput,
	}
}

// NewWithInitial returns a Search model seeded with query/filter state. If any
// search state is provided, Init runs the search immediately.
func NewWithInitial(configDir, query, kind, category, installed string) Model {
	m := New(configDir)
	m.query = query
	m.kindFilter = kind
	m.categoryFilter = category
	m.installedFilter = installed
	if m.hasInitialSearch() {
		m.loading = true
	}
	return m
}

func (m Model) hasInitialSearch() bool {
	return strings.TrimSpace(m.query) != "" ||
		m.kindFilter != "" ||
		m.categoryFilter != "" ||
		m.installedFilter != ""
}

// computeColumnWidths returns the longest Kind and Pack lengths across all
// results. Called at ResultsMsg time so View() can read m.maxKindWidth /
// m.maxPackWidth directly.
func computeColumnWidths(results []app.SearchResult) (kindW, packW int) {
	for _, r := range results {
		kindW = max(kindW, len(r.Kind))
		packW = max(packW, len(r.Pack))
	}
	return
}

func (m Model) Init() tea.Cmd {
	if !m.hasInitialSearch() {
		return nil
	}
	return RunSearch(m.configDir, m.query, m.kindFilter, m.categoryFilter, m.installedFilter)
}

func (m Model) SetSize(width, height int) common.Screen {
	m.width = width
	m.height = height
	return m
}

func (m Model) Preload() (common.Screen, tea.Cmd) {
	if m.searched || m.loading {
		return m, nil
	}
	return m, nil
}

func (m Model) Refresh() (common.Screen, tea.Cmd) {
	if !m.searched {
		return m, nil
	}
	m.loading = true
	return m, RefreshSearch(m.configDir, m.query, m.kindFilter, m.categoryFilter, m.installedFilter)
}

func (m Model) CapturesKey(msg tea.KeyPressMsg) bool {
	if m.focus != focusInput {
		return false
	}
	switch msg.String() {
	case "tab", "shift+tab", "ctrl+c", "1", "2", "3", "4", "5", "6":
		return false
	default:
		return true
	}
}

func (m Model) ResultsFocused() bool {
	return m.focus == focusResults
}

func (m Model) HelpText() string {
	if m.focus == focusInput {
		return "enter:search  down:results  ctrl+u:clear │ 1-6/tab:switch  ctrl+c:quit"
	}
	action := "enter:details"
	if m.cursor < len(m.results) && m.results[m.cursor].Installed {
		action += "  p:preview"
	}
	return "j/k:navigate  " + action + "  /:search  f:kind  space:show │ 1-6/tab:switch  esc:back"
}

func (m Model) resultAreaHeight() int {
	// Subtract fixed chrome plus Content padding so results never run
	// into the app-level help/status line.
	top, _, bottom, _ := common.ContentStyle.GetPadding()
	const fixedRows = 6 // search, blank, filters, blank, footer gap, footer
	h := max(m.height-top-bottom-fixedRows, 1)
	return h
}

func resultRowHeight(r app.SearchResult) int {
	if strings.TrimSpace(r.Snippet) != "" {
		return 2
	}
	return 1
}
