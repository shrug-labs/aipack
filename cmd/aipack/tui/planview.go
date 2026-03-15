package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shrug-labs/aipack/internal/app"
)

// planViewItem is a single entry in the flat plan list (header or operation).
type planViewItem struct {
	isHeader   bool           // true for group headers like "Writes (3)"
	op         app.PlanOp     // only valid when isHeader == false
	groupStyle lipgloss.Style // for rendering the group badge
	groupLabel string         // "Writes (3)" for headers, "write" for items
}

// planViewModel is a full-screen overlay that displays the sync plan operations
// as a cursor-navigable list. Pressing Enter on an item opens a nested diff overlay.
type planViewModel struct {
	profileName string
	ops         []app.PlanOp
	projectDir  string
	isSavePlan  bool // true for save (harness→pack) plans; false for sync plans

	items        []planViewItem // flattened: headers + operations
	cursor       int            // index into items (always on a non-header item)
	scrollOffset int

	width  int
	height int
	ready  bool

	diffView *diffViewModel // nested diff overlay; non-nil when showing a diff
}

func newPlanViewModel(width, height int, profileName, projectDir string, ops []app.PlanOp, isSavePlan bool) planViewModel {
	m := planViewModel{
		profileName: profileName,
		ops:         ops,
		projectDir:  projectDir,
		isSavePlan:  isSavePlan,
		width:       width,
		height:      height,
		ready:       true,
	}
	m.items = m.buildItems()
	// Set initial cursor to first non-header item.
	for i, item := range m.items {
		if !item.isHeader {
			m.cursor = i
			break
		}
	}
	return m
}

func (m *planViewModel) buildItems() []planViewItem {
	groups := []struct {
		kind  app.PlanOpKind
		label string
		style lipgloss.Style
	}{
		{app.PlanOpRule, "Rules", opRuleStyle},
		{app.PlanOpWorkflow, "Workflows", opWorkflowStyle},
		{app.PlanOpAgent, "Agents", opAgentStyle},
		{app.PlanOpSkill, "Skills", opSkillStyle},
		{app.PlanOpSettings, "Settings", opSettingsStyle},
		{app.PlanOpMCP, "MCP", opMCPStyle},
		{app.PlanOpPrune, "Prunes", opPruneStyle},
	}

	var items []planViewItem
	for _, g := range groups {
		var matched []app.PlanOp
		for _, op := range m.ops {
			if op.Kind == g.kind {
				matched = append(matched, op)
			}
		}
		if len(matched) == 0 {
			continue
		}
		// Header row.
		items = append(items, planViewItem{
			isHeader:   true,
			groupStyle: g.style,
			groupLabel: fmt.Sprintf("%s (%d)", g.label, len(matched)),
		})
		// File rows.
		for _, op := range matched {
			items = append(items, planViewItem{
				op:         op,
				groupStyle: g.style,
				groupLabel: string(g.kind),
			})
		}
	}
	return items
}

// shortDst shortens a destination path relative to the project dir for readability.
func (m *planViewModel) shortDst(dst string) string {
	if m.projectDir != "" && strings.HasPrefix(dst, m.projectDir+"/") {
		return "./" + dst[len(m.projectDir)+1:]
	}
	return shortPath(dst)
}

func (m *planViewModel) viewportHeight() int {
	// Account for header (title + rule + blank line) and border.
	h := m.height - 8
	if h < 5 {
		h = 5
	}
	return h
}

func (m *planViewModel) ensureVisible() {
	vpH := m.viewportHeight()
	if m.cursor < m.scrollOffset {
		m.scrollOffset = m.cursor
	}
	if m.cursor >= m.scrollOffset+vpH {
		m.scrollOffset = m.cursor - vpH + 1
	}
}

func (m planViewModel) Update(msg tea.Msg) (planViewModel, tea.Cmd) {
	// Handle diff loaded result.
	if msg, ok := msg.(diffLoadedMsg); ok {
		if m.diffView != nil {
			errText := ""
			if msg.err != nil {
				errText = msg.err.Error()
			}
			m.diffView.setContent(msg.diffText, msg.isNew, msg.newBody, errText)
		}
		return m, nil
	}

	// If diff overlay is active, delegate.
	if m.diffView != nil {
		return m.updateDiffView(msg)
	}

	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "j", "down":
			for i := m.cursor + 1; i < len(m.items); i++ {
				if !m.items[i].isHeader {
					m.cursor = i
					m.ensureVisible()
					break
				}
			}
		case "k", "up":
			for i := m.cursor - 1; i >= 0; i-- {
				if !m.items[i].isHeader {
					m.cursor = i
					m.ensureVisible()
					break
				}
			}
		case "enter":
			if m.cursor >= 0 && m.cursor < len(m.items) && !m.items[m.cursor].isHeader {
				op := m.items[m.cursor].op
				dv := newDiffViewModel(m.width, m.height,
					filepath.Base(op.Dst), m.shortDst(op.Dst))
				m.diffView = &dv
				return m, m.loadDiff(op)
			}
		case "e", "i":
			if m.cursor >= 0 && m.cursor < len(m.items) && !m.items[m.cursor].isHeader {
				return m, openFileInEditor(m.items[m.cursor].op.Dst)
			}
		}
	}
	return m, nil
}

func (m planViewModel) updateDiffView(msg tea.Msg) (planViewModel, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = msg.Width
		m.height = msg.Height
		if m.diffView.ready {
			m.diffView.viewport.Width = msg.Width - 4
			m.diffView.viewport.Height = msg.Height - 4
		}
		return m, nil
	}
	if msg, ok := msg.(tea.KeyMsg); ok {
		if msg.String() == "esc" || msg.String() == "q" {
			m.diffView = nil
			return m, nil
		}
	}
	dv, cmd := m.diffView.Update(msg)
	m.diffView = &dv
	return m, cmd
}

func (m planViewModel) View() string {
	// If diff overlay is active, render it instead.
	if m.diffView != nil {
		return m.diffView.View()
	}

	if !m.ready {
		return previewBorderStyle.
			Width(m.width - 2).
			Height(m.height - 2).
			Render("\n  Loading...")
	}

	maxW := m.width - 4
	if maxW < 20 {
		maxW = 20
	}

	var sb strings.Builder

	// Header.
	planLabel := "Sync"
	if m.isSavePlan {
		planLabel = "Save"
	}
	header := fmt.Sprintf("%s Plan: %s  (%d operations)", planLabel, m.profileName, len(m.ops))
	sb.WriteString(previewTitleStyle.Render(header))
	sb.WriteString("\n")
	ruleW := maxW
	if ruleW > 80 {
		ruleW = 80
	}
	sb.WriteString(strings.Repeat("─", ruleW))
	sb.WriteString("\n\n")

	if len(m.items) == 0 {
		sb.WriteString(dimStyle.Render("No pending operations — everything is up to date."))
	} else {
		vpH := m.viewportHeight()
		end := m.scrollOffset + vpH
		if end > len(m.items) {
			end = len(m.items)
		}
		for i := m.scrollOffset; i < end; i++ {
			item := m.items[i]
			if item.isHeader {
				if i > m.scrollOffset {
					sb.WriteString("\n")
				}
				sb.WriteString(item.groupStyle.Bold(true).Render(item.groupLabel))
				sb.WriteString("\n")
				continue
			}

			dst := m.shortDst(item.op.Dst)
			prefix := "  "
			if i == m.cursor {
				prefix = selectedStyle.Render("> ")
			}
			line := fmt.Sprintf("%s%s  %s", prefix, item.groupStyle.Render(item.groupLabel), dst)
			if item.op.SourcePack != "" {
				line += "  " + dimStyle.Render("("+item.op.SourcePack+")")
			}
			if item.op.Size > 0 {
				line += "  " + fileSizeStyle.Render(formatSize(int64(item.op.Size)))
			}
			sb.WriteString(line + "\n")
		}
	}

	// Count selectable items for footer.
	selectableCount := 0
	cursorPos := 0
	for i, item := range m.items {
		if !item.isHeader {
			selectableCount++
			if i == m.cursor {
				cursorPos = selectableCount
			}
		}
	}
	footer := dimStyle.Render(fmt.Sprintf("─── %d/%d ───", cursorPos, selectableCount))

	content := sb.String() + "\n" + footer
	return previewBorderStyle.
		Width(m.width - 2).
		Height(m.height - 2).
		Render(content)
}

func (m planViewModel) helpText() string {
	if m.diffView != nil {
		return "j/k:scroll  esc:back"
	}
	if m.isSavePlan {
		return "j/k:navigate  enter:diff  e:edit  s:confirm  esc:cancel"
	}
	return "j/k:navigate  enter:diff  e:edit  esc:close"
}

// loadDiff reads the on-disk file and computes a unified diff against the desired content.
func (m planViewModel) loadDiff(op app.PlanOp) tea.Cmd {
	return func() tea.Msg {
		title := filepath.Base(op.Dst)

		// Prune entries show the file that will be deleted.
		if op.Kind == app.PlanOpPrune {
			onDisk, err := os.ReadFile(op.Dst)
			if err != nil {
				return diffLoadedMsg{dst: op.Dst, title: title, err: err}
			}
			labelA := filepath.Base(op.Dst) + " (will be removed)"
			diffText := app.ComputeDiff(onDisk, nil, labelA, "/dev/null")
			return diffLoadedMsg{dst: op.Dst, title: title, diffText: diffText}
		}

		// Determine desired content.
		desired := op.Content
		if desired == nil && op.Src != "" {
			var err error
			desired, err = os.ReadFile(op.Src)
			if err != nil {
				return diffLoadedMsg{dst: op.Dst, title: title, err: err}
			}
		}

		if desired == nil {
			return diffLoadedMsg{
				dst:   op.Dst,
				title: title,
				err:   fmt.Errorf("no content available for diff"),
			}
		}

		// Read current on-disk file.
		onDisk, err := os.ReadFile(op.Dst)
		if err != nil {
			if os.IsNotExist(err) {
				return diffLoadedMsg{
					dst:     op.Dst,
					title:   title,
					isNew:   true,
					newBody: string(desired),
				}
			}
			return diffLoadedMsg{dst: op.Dst, title: title, err: err}
		}

		labelA := filepath.Base(op.Dst) + " (current)"
		labelB := filepath.Base(op.Dst) + " (desired)"
		if m.isSavePlan {
			labelA = filepath.Base(op.Dst) + " (in pack)"
			labelB = filepath.Base(op.Dst) + " (in harness)"
		}
		diffText := app.ComputeDiff(onDisk, desired, labelA, labelB)

		return diffLoadedMsg{dst: op.Dst, title: title, diffText: diffText}
	}
}

// ─────────────────────────────────────────────────────
// diffViewModel — nested overlay for displaying a unified diff.
// ─────────────────────────────────────────────────────

type diffViewModel struct {
	title string
	dst   string
	isNew bool

	viewport viewport.Model
	ready    bool
	errText  string
	width    int
	height   int
}

func newDiffViewModel(width, height int, title, dst string) diffViewModel {
	return diffViewModel{
		title:  title,
		dst:    dst,
		width:  width,
		height: height,
	}
}

func (m *diffViewModel) setContent(diffText string, isNew bool, newBody string, errText string) {
	if errText != "" {
		m.errText = errText
		m.ready = true
		return
	}

	m.isNew = isNew

	maxW := m.width - 4
	if maxW < 20 {
		maxW = 20
	}

	var sb strings.Builder

	// Header.
	if isNew {
		sb.WriteString(previewTitleStyle.Render("New file: " + m.title))
	} else {
		sb.WriteString(previewTitleStyle.Render("Diff: " + m.title))
	}
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render(m.dst))
	sb.WriteString("\n")
	ruleW := maxW
	if ruleW > 80 {
		ruleW = 80
	}
	sb.WriteString(strings.Repeat("─", ruleW))
	sb.WriteString("\n\n")

	if isNew {
		// Show full content for new files.
		for _, line := range strings.Split(newBody, "\n") {
			sb.WriteString(diffAddStyle.Render("+ " + line))
			sb.WriteString("\n")
		}
	} else if diffText == "" {
		sb.WriteString(dimStyle.Render("(no changes — content is identical)"))
	} else {
		// Colorize diff lines.
		for _, line := range strings.Split(diffText, "\n") {
			if line == "" {
				sb.WriteString("\n")
				continue
			}
			switch {
			case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
				sb.WriteString(dimStyle.Bold(true).Render(line))
			case strings.HasPrefix(line, "@@"):
				sb.WriteString(diffHunkStyle.Render(line))
			case strings.HasPrefix(line, "+"):
				sb.WriteString(diffAddStyle.Render(line))
			case strings.HasPrefix(line, "-"):
				sb.WriteString(diffRemoveStyle.Render(line))
			default:
				sb.WriteString(line)
			}
			sb.WriteString("\n")
		}
	}

	vpH := m.height - 4
	if vpH < 5 {
		vpH = 5
	}
	vp := viewport.New(maxW, vpH)
	vp.SetContent(sb.String())
	m.viewport = vp
	m.ready = true
}

func (m diffViewModel) Update(msg tea.Msg) (diffViewModel, tea.Cmd) {
	if m.ready {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m diffViewModel) View() string {
	if m.errText != "" {
		content := fmt.Sprintf("\n  %s\n\n  %s\n",
			previewTitleStyle.Render("Diff: "+m.title),
			errorStyle.Render("Error: "+m.errText))
		return previewBorderStyle.
			Width(m.width - 2).
			Height(m.height - 2).
			Render(content)
	}

	if !m.ready {
		return previewBorderStyle.
			Width(m.width - 2).
			Height(m.height - 2).
			Render("\n  Loading...")
	}

	pct := m.viewport.ScrollPercent()
	scrollInfo := fmt.Sprintf("%3.0f%%", pct*100)
	footer := dimStyle.Render(fmt.Sprintf("─── %s ───", scrollInfo))

	content := m.viewport.View() + "\n" + footer
	return previewBorderStyle.
		Width(m.width - 2).
		Height(m.height - 2).
		Render(content)
}
