package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shrug-labs/aipack/internal/app"
)

// saveTabFocus tracks which panel has focus.
type saveTabFocus int

const (
	savePanelFiles  saveTabFocus = iota // left: file list
	savePanelDetail                     // right: file detail
)

type saveTabModel struct {
	configDir string
	files     []app.HarnessFile
	cursor    int
	focus     saveTabFocus
	loading   bool
	loadErr   string
	noSync    bool // true when no ledger exists (never synced)

	// Counts by state.
	countModified  int
	countConflict  int
	countSettings  int
	countUntracked int
	countClean     int

	// Ledger info.
	ledgerPath  string
	ledgerFiles int

	width, height int
}

func newSaveTabModel(configDir string) saveTabModel {
	return saveTabModel{configDir: configDir}
}

func (m saveTabModel) Update(msg tea.Msg) (saveTabModel, tea.Cmd) {
	switch msg := msg.(type) {
	case inspectResultMsg:
		m.loading = false
		if msg.err != nil {
			m.loadErr = msg.err.Error()
			return m, nil
		}
		m.loadErr = ""
		m.files = msg.result.Files
		// Sort by display group order so viewFileList can iterate sequentially.
		sort.Slice(m.files, func(i, j int) bool {
			return app.StateSortKey(m.files[i].State) < app.StateSortKey(m.files[j].State)
		})
		m.noSync = !msg.result.HasLedger
		m.ledgerPath = msg.result.LedgerPath
		m.ledgerFiles = msg.result.LedgerFiles
		m.recount()
		if m.cursor >= len(m.files) {
			m.cursor = max(0, len(m.files)-1)
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m saveTabModel) handleKey(msg tea.KeyMsg) (saveTabModel, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.cursor < len(m.files)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "enter", "right", "l":
		if m.focus == savePanelFiles && len(m.files) > 0 {
			// Open preview for the focused file.
			f := m.files[m.cursor]
			return m, func() tea.Msg {
				return previewRequestMsg{
					title:    filepath.Base(f.HarnessPath),
					category: f.Category,
					packName: f.PackName,
					filePath: f.HarnessPath,
				}
			}
		}
	}
	return m, nil
}

func (m *saveTabModel) recount() {
	m.countModified = 0
	m.countConflict = 0
	m.countSettings = 0
	m.countUntracked = 0
	m.countClean = 0
	for _, f := range m.files {
		switch f.State {
		case app.FileModified:
			m.countModified++
		case app.FileConflict:
			m.countConflict++
		case app.FileSettings:
			m.countSettings++
		case app.FileUntracked:
			m.countUntracked++
		case app.FileClean:
			m.countClean++
		}
	}
}

func (m saveTabModel) currentFile() *app.HarnessFile {
	if len(m.files) == 0 || m.cursor < 0 || m.cursor >= len(m.files) {
		return nil
	}
	return &m.files[m.cursor]
}

func (m saveTabModel) pendingCount() int {
	return m.countModified + m.countConflict + m.countSettings
}

func (m saveTabModel) View() string {
	if m.loading {
		return contentStyle.Render("Inspecting harness files...")
	}
	if m.loadErr != "" {
		return contentStyle.Render(errorStyle.Render("Error: " + m.loadErr))
	}
	if m.noSync {
		return contentStyle.Render(dimStyle.Render("No ledger found — run sync first to establish file tracking."))
	}
	if len(m.files) == 0 {
		return contentStyle.Render(dimStyle.Render("No harness files found."))
	}

	col1W := m.width * 40 / 100
	if col1W < 30 {
		col1W = 30
	}
	col2W := m.width - col1W - 6
	if col2W < 20 {
		col2W = 20
	}

	left := m.viewFileList(col1W)
	sep := dimStyle.Render(" │ ")
	right := m.viewDetail(col2W)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, left, sep, right)
	return contentStyle.Render(joined)
}

func (m saveTabModel) viewFileList(width int) string {
	style := lipgloss.NewStyle().Width(width)
	var sb strings.Builder

	// Summary header.
	sb.WriteString(panelHeaderStyle.Render("Files") + "\n")

	// Group files by state and render with group headers.
	type group struct {
		state app.FileState
		label string
		count int
	}
	groups := []group{
		{app.FileConflict, "Conflicts", m.countConflict},
		{app.FileModified, "Modified", m.countModified},
		{app.FileSettings, "Settings", m.countSettings},
		{app.FileUntracked, "Untracked", m.countUntracked},
		{app.FileClean, "Clean", m.countClean},
	}

	idx := 0 // tracks position in m.files
	for _, g := range groups {
		if g.count == 0 {
			continue
		}
		sb.WriteString(dimStyle.Render(fmt.Sprintf("\n %s (%d)", g.label, g.count)) + "\n")

		for idx < len(m.files) && m.files[idx].State == g.state {
			f := m.files[idx]
			cursor := "  "
			if idx == m.cursor {
				cursor = selectedStyle.Render("> ")
			}

			icon := fileStateIcon(f.State)
			name := filepath.Base(f.HarnessPath)
			nameStyle := lipgloss.NewStyle()
			if idx == m.cursor {
				nameStyle = selectedStyle
			}

			packLabel := ""
			if f.PackName != "" {
				packLabel = "  " + dimStyle.Render(f.PackName)
			}

			sb.WriteString(fmt.Sprintf("%s%s %s%s\n", cursor, icon, nameStyle.Render(name), packLabel))
			idx++
		}
	}

	return style.Render(sb.String())
}

func (m saveTabModel) viewDetail(width int) string {
	style := lipgloss.NewStyle().Width(width)
	var sb strings.Builder

	sb.WriteString(panelHeaderStyle.Render("Detail") + "\n\n")

	f := m.currentFile()
	if f == nil {
		sb.WriteString(dimStyle.Render("(no file selected)"))
		return style.Render(sb.String())
	}

	// File info.
	name := filepath.Base(f.HarnessPath)
	sb.WriteString(fmt.Sprintf("%s %s\n\n", fileStateIcon(f.State), name))

	sb.WriteString(fmt.Sprintf("State:    %s\n", fileStateLabel(f.State)))
	sb.WriteString(fmt.Sprintf("Category: %s\n", f.Category))
	if f.PackName != "" {
		sb.WriteString(fmt.Sprintf("Pack:     %s\n", f.PackName))
	} else {
		sb.WriteString(fmt.Sprintf("Pack:     %s\n", dimStyle.Render("(none)")))
	}
	sb.WriteString(fmt.Sprintf("Size:     %s\n", formatSize(f.Size)))
	sb.WriteString(fmt.Sprintf("Harness:  %s\n", dimStyle.Render(shortPath(f.HarnessPath))))
	if f.PackPath != "" {
		sb.WriteString(fmt.Sprintf("Pack:     %s\n", dimStyle.Render(shortPath(f.PackPath))))
	}

	// Summary section.
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("─── Summary ───") + "\n")
	total := len(m.files)
	sb.WriteString(fmt.Sprintf("Total files: %d\n", total))
	if m.countConflict > 0 {
		sb.WriteString(fmt.Sprintf("  %s Conflicts:  %d\n", fileStateIcon(app.FileConflict), m.countConflict))
	}
	if m.countModified > 0 {
		sb.WriteString(fmt.Sprintf("  %s Modified:   %d\n", fileStateIcon(app.FileModified), m.countModified))
	}
	if m.countSettings > 0 {
		sb.WriteString(fmt.Sprintf("  %s Settings:   %d\n", fileStateIcon(app.FileSettings), m.countSettings))
	}
	if m.countUntracked > 0 {
		sb.WriteString(fmt.Sprintf("  %s Untracked:  %d\n", fileStateIcon(app.FileUntracked), m.countUntracked))
	}
	sb.WriteString(fmt.Sprintf("  %s Clean:      %d\n", fileStateIcon(app.FileClean), m.countClean))

	if m.ledgerPath != "" {
		sb.WriteString(fmt.Sprintf("\nLedger: %s\n", dimStyle.Render(shortPath(m.ledgerPath))))
		sb.WriteString(fmt.Sprintf("  %d entries tracked\n", m.ledgerFiles))
	}

	return style.Render(sb.String())
}

func fileStateIcon(s app.FileState) string {
	switch s {
	case app.FileModified:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("●") // orange
	case app.FileConflict:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("◆") // red
	case app.FileSettings:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("177")).Render("◐") // purple
	case app.FileUntracked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("○") // gray
	case app.FileClean:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("40")).Render("✓") // green
	default:
		return " "
	}
}

func fileStateLabel(s app.FileState) string {
	switch s {
	case app.FileModified:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("modified")
	case app.FileConflict:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("conflict")
	case app.FileSettings:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("177")).Render("settings changed")
	case app.FileUntracked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("untracked")
	case app.FileClean:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("40")).Render("clean")
	default:
		return "unknown"
	}
}
