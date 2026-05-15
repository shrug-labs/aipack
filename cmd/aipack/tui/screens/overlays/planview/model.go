package planview

import (
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	tuiutil "github.com/shrug-labs/aipack/cmd/aipack/tui/util"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/engine"
)

var (
	previewBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("205")).
				Padding(0, 1)
	previewTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	opRuleStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	opWorkflowStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	opAgentStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	opSkillStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("79"))
	opSettingsStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	opMCPStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	opStaleStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

type OpenFunc func(string) tea.Cmd

type DiffLoadedMsg struct {
	Dst      string
	Title    string
	DiffText string
	Note     string
	IsNew    bool
	NewBody  string
	Err      error
}

// Item is a single entry in the flat plan list (header or operation).
type Item struct {
	IsHeader   bool           // true for group headers like "Writes (3)"
	Op         app.PlanOp     // only valid when IsHeader == false
	GroupStyle lipgloss.Style // for rendering the group badge
	GroupLabel string         // "Writes (3)" for headers, "write" for Items
}

// Model is a full-screen overlay that displays the sync plan operations
// as a Cursor-navigable list. Pressing Enter on an item opens a nested diff overlay.
type Model struct {
	ProfileName string
	Ops         []app.PlanOp
	ProjectDir  string
	IsSavePlan  bool // true for save (harness→pack) plans; false for sync plans

	Items        []Item // flattened: headers + operations
	Cursor       int    // index into Items (always on a non-header item)
	ScrollOffset int

	Width  int
	Height int
	Ready  bool

	DiffView *DiffViewModel // nested diff overlay; non-nil when showing a diff

	openEditor OpenFunc
}

func (m Model) IsSavePlanOverlay() bool {
	return m.IsSavePlan
}

func (m Model) HasDiffView() bool {
	return m.DiffView != nil
}

func New(Width, Height int, ProfileName, ProjectDir string, Ops []app.PlanOp, IsSavePlan bool, openEditor OpenFunc) Model {
	m := Model{
		ProfileName: ProfileName,
		Ops:         Ops,
		ProjectDir:  ProjectDir,
		IsSavePlan:  IsSavePlan,
		Width:       Width,
		Height:      Height,
		Ready:       true,
		openEditor:  openEditor,
	}
	m.Items = m.BuildItems()
	// Set initial Cursor to first non-header item.
	for i, item := range m.Items {
		if !item.IsHeader {
			m.Cursor = i
			break
		}
	}
	return m
}

func (m *Model) BuildItems() []Item {
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
		{app.PlanOpStale, "Stale", opStaleStyle},
	}

	var Items []Item
	for _, g := range groups {
		var matched []app.PlanOp
		for _, Op := range m.Ops {
			if Op.Kind == g.kind {
				matched = append(matched, Op)
			}
		}
		if len(matched) == 0 {
			continue
		}
		// Header row.
		Items = append(Items, Item{
			IsHeader:   true,
			GroupStyle: g.style,
			GroupLabel: fmt.Sprintf("%s (%d)", g.label, len(matched)),
		})
		// File rows.
		for _, Op := range matched {
			Items = append(Items, Item{
				Op:         Op,
				GroupStyle: g.style,
				GroupLabel: string(g.kind),
			})
		}
	}
	return Items
}

// ShortDst shortens a destination path relative to the project dir for readability.
func (m *Model) ShortDst(dst string) string {
	if rel, ok := shortRelativeDisplayPath(m.ProjectDir, dst); ok {
		return "./" + rel
	}
	return tuiutil.ShortPath(dst)
}

func shortRelativeDisplayPath(base, target string) (string, bool) {
	base = strings.TrimRight(cleanDisplayPath(base), "/")
	target = cleanDisplayPath(target)
	if base == "" || base == "." || target == base {
		return "", false
	}
	prefix := base + "/"
	if !strings.HasPrefix(target, prefix) {
		return "", false
	}
	return target[len(prefix):], true
}

func cleanDisplayPath(p string) string {
	return pathpkg.Clean(strings.ReplaceAll(p, "\\", "/"))
}

func (m *Model) ViewportHeight() int {
	// Account for header (title + rule + blank line) and border.
	h := max(m.Height-8, 5)
	return h
}

func (m *Model) ensureVisible() {
	m.ScrollOffset = common.ClampOffset(m.Cursor, m.ScrollOffset, m.ViewportHeight())
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) SetSize(Width, Height int) common.Screen {
	return m.SetSizeModel(Width, Height)
}

func (m Model) SetSizeModel(Width, Height int) Model {
	m.Width = Width
	m.Height = Height
	if m.DiffView != nil {
		m.DiffView.Width = Width
		m.DiffView.Height = Height
		if m.DiffView.Ready {
			m.DiffView.viewport.SetWidth(diffViewportWidth(Width))
			m.DiffView.viewport.SetHeight(diffViewportHeight(Height))
		}
	}
	return m
}

func (m Model) Update(msg tea.Msg) (common.Screen, tea.Cmd) {
	return m.UpdateModel(msg)
}

func (m Model) UpdateModel(msg tea.Msg) (Model, tea.Cmd) {
	// Handle diff loaded result.
	if msg, ok := msg.(DiffLoadedMsg); ok {
		if m.DiffView != nil {
			errText := ""
			if msg.Err != nil {
				errText = msg.Err.Error()
			}
			m.DiffView.Note = msg.Note
			m.DiffView.SetContent(msg.DiffText, msg.IsNew, msg.NewBody, errText)
		}
		return m, nil
	}

	if msg, ok := msg.(common.LayerHitMsg); ok {
		return m.handleLayerHit(msg)
	}

	// If diff overlay is active, delegate.
	if m.DiffView != nil {
		return m.UpdateDiffView(msg)
	}

	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.String() {
		case "j", "down":
			for i := m.Cursor + 1; i < len(m.Items); i++ {
				if !m.Items[i].IsHeader {
					m.Cursor = i
					m.ensureVisible()
					break
				}
			}
		case "k", "up":
			for i := m.Cursor - 1; i >= 0; i-- {
				if !m.Items[i].IsHeader {
					m.Cursor = i
					m.ensureVisible()
					break
				}
			}
		case "enter":
			if m.Cursor >= 0 && m.Cursor < len(m.Items) && !m.Items[m.Cursor].IsHeader {
				Op := m.Items[m.Cursor].Op
				dv := NewDiffViewModel(m.Width, m.Height,
					filepath.Base(Op.Dst), m.ShortDst(Op.Dst))
				m.DiffView = &dv
				return m, m.LoadDiff(Op)
			}
		case "e", "i":
			if m.Cursor >= 0 && m.Cursor < len(m.Items) && !m.Items[m.Cursor].IsHeader {
				if m.openEditor != nil {
					return m, m.openEditor(m.Items[m.Cursor].Op.Dst)
				}
			}
		}
	}
	return m, nil
}

func (m Model) handleLayerHit(msg common.LayerHitMsg) (Model, tea.Cmd) {
	if !common.IsLeftClick(msg.Mouse) || !strings.HasPrefix(msg.ID, "planview:") {
		return m, nil
	}
	if msg.ID == "planview:diff:close" && m.DiffView != nil {
		m.DiffView = nil
		return m, nil
	}
	index, ok := common.ParseLayerIndex(msg.ID, "planview:item:")
	if !ok || m.DiffView != nil {
		return m, nil
	}
	if index < 0 || index >= len(m.Items) || m.Items[index].IsHeader {
		return m, nil
	}
	m.Cursor = index
	m.ensureVisible()
	op := m.Items[index].Op
	dv := NewDiffViewModel(m.Width, m.Height, filepath.Base(op.Dst), m.ShortDst(op.Dst))
	m.DiffView = &dv
	return m, m.LoadDiff(op)
}

func (m Model) UpdateDiffView(msg tea.Msg) (Model, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		m.Width = msg.Width
		m.Height = msg.Height
		if m.DiffView.Ready {
			m.DiffView.viewport.SetWidth(diffViewportWidth(msg.Width))
			m.DiffView.viewport.SetHeight(diffViewportHeight(msg.Height))
		}
		return m, nil
	}
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		if msg.String() == "esc" || msg.String() == "q" {
			m.DiffView = nil
			return m, nil
		}
	}
	dv, cmd := m.DiffView.Update(msg)
	m.DiffView = &dv
	return m, cmd
}

func (m Model) View() *lipgloss.Layer {
	root := lipgloss.NewLayer(m.Render())
	root.AddLayers(lipgloss.NewLayer("[close]").ID("planview:close").X(max(2, m.Width-10)).Y(1).Z(-1))
	if m.DiffView != nil {
		root.AddLayers(lipgloss.NewLayer("[back]").ID("planview:diff:close").X(max(2, m.Width-17)).Y(1).Z(-1))
		return root
	}
	if m.IsSavePlan {
		root.AddLayers(lipgloss.NewLayer("[save]").ID("planview:confirm").X(max(2, m.Width-18)).Y(1).Z(-1))
	}
	y := 4
	end := min(m.ScrollOffset+m.ViewportHeight(), len(m.Items))
	for i := m.ScrollOffset; i < end; i++ {
		if m.Items[i].IsHeader {
			if i > m.ScrollOffset {
				y++
			}
			y++
			continue
		}
		root.AddLayers(lipgloss.NewLayer(m.ShortDst(m.Items[i].Op.Dst)).ID(fmt.Sprintf("planview:item:%d", i)).X(6).Y(y).Z(-1))
		y++
	}
	return root
}

func (m Model) Render() string {
	// If diff overlay is active, render it instead.
	if m.DiffView != nil {
		return m.DiffView.View()
	}

	if !m.Ready {
		return previewBorderStyle.
			Width(m.Width - 2).
			Height(m.Height - 2).
			Render("\n  Loading...")
	}

	maxW := max(m.Width-6, 20)

	var sb strings.Builder

	// Header.
	planLabel := "Sync"
	if m.IsSavePlan {
		planLabel = "Save"
	}
	header := fmt.Sprintf("%s Plan: %s  (%d operations)", planLabel, m.ProfileName, len(m.Ops))
	sb.WriteString(previewTitleStyle.Render(header))
	sb.WriteString("\n")
	ruleW := min(maxW, 80)
	sb.WriteString(strings.Repeat("─", ruleW))
	sb.WriteString("\n\n")

	if len(m.Items) == 0 {
		sb.WriteString(common.DimStyle.Render("No pending operations — everything is up to date."))
	} else {
		vpH := m.ViewportHeight()
		end := min(m.ScrollOffset+vpH, len(m.Items))
		for i := m.ScrollOffset; i < end; i++ {
			item := m.Items[i]
			if item.IsHeader {
				if i > m.ScrollOffset {
					sb.WriteString("\n")
				}
				sb.WriteString(item.GroupStyle.Bold(true).Render(item.GroupLabel))
				sb.WriteString("\n")
				continue
			}

			prefix := "  "
			if i == m.Cursor {
				prefix = common.SelectedStyle.Render("> ")
			}
			sb.WriteString(m.renderPlanRow(item, prefix, maxW) + "\n")
		}
	}

	// Count selectable Items for footer.
	selectableCount := 0
	cursorPos := 0
	for i, item := range m.Items {
		if !item.IsHeader {
			selectableCount++
			if i == m.Cursor {
				cursorPos = selectableCount
			}
		}
	}
	footer := common.DimStyle.Render(fmt.Sprintf("─── %d/%d ───", cursorPos, selectableCount))

	content := sb.String() + "\n" + footer
	return previewBorderStyle.
		Width(m.Width - 2).
		Height(m.Height - 2).
		Render(content)
}

func (m Model) renderPlanRow(item Item, prefix string, width int) string {
	dst := m.ShortDst(item.Op.Dst)
	suffixWidth := 0
	suffix := ""
	if item.Op.SourcePack != "" {
		pack := "(" + item.Op.SourcePack + ")"
		suffixWidth += 2 + lipgloss.Width(pack)
		suffix += "  " + common.DimStyle.Render(pack)
	}
	if item.Op.Size > 0 {
		size := common.FormatSize(int64(item.Op.Size))
		suffixWidth += 2 + lipgloss.Width(size)
		suffix += "  " + common.DimStyle.Render(size)
	}

	prefixWidth := 2
	labelWidth := lipgloss.Width(item.GroupLabel)
	dstWidth := max(width-prefixWidth-labelWidth-2-suffixWidth, 8)
	dst = truncateRunes(dst, dstWidth)
	return fmt.Sprintf("%s%s  %s%s", prefix, item.GroupStyle.Render(item.GroupLabel), dst, suffix)
}

func truncateRunes(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func (m Model) HelpText() string {
	if m.DiffView != nil {
		return "j/k:scroll  esc:back"
	}
	if m.IsSavePlan {
		return "j/k:navigate  enter:diff  e:edit  s:confirm  esc:cancel"
	}
	return "j/k:navigate  enter:diff  e:edit  esc:close"
}

// LoadDiff reads the on-disk file and computes a unified diff against the desired content.
func (m Model) LoadDiff(Op app.PlanOp) tea.Cmd {
	return func() tea.Msg {
		title := filepath.Base(Op.Dst)

		// Stale entries show the file that will be deleted.
		if Op.Kind == app.PlanOpStale {
			onDisk, err := os.ReadFile(Op.Dst)
			if err != nil {
				return DiffLoadedMsg{Dst: Op.Dst, Title: title, Err: err}
			}
			labelA := filepath.Base(Op.Dst) + " (will be removed)"
			diffText := engine.UnifiedDiff(onDisk, nil, labelA, "/dev/null")
			return DiffLoadedMsg{Dst: Op.Dst, Title: title, DiffText: diffText}
		}

		// Determine desired content.
		desired := Op.Content
		if desired == nil && Op.Src != "" {
			var err error
			desired, err = os.ReadFile(Op.Src)
			if err != nil {
				return DiffLoadedMsg{Dst: Op.Dst, Title: title, Err: err}
			}
		}

		if desired == nil {
			return DiffLoadedMsg{
				Dst:   Op.Dst,
				Title: title,
				Err:   fmt.Errorf("no content available for diff"),
			}
		}

		if Op.Diff != "" {
			_, _, note := planDiffLabelsAndNote(Op, m.IsSavePlan)
			return DiffLoadedMsg{Dst: Op.Dst, Title: title, DiffText: Op.Diff, Note: note}
		}

		// Read current on-disk file.
		onDisk, err := os.ReadFile(Op.Dst)
		if err != nil {
			if os.IsNotExist(err) {
				return DiffLoadedMsg{
					Dst:     Op.Dst,
					Title:   title,
					IsNew:   true,
					NewBody: string(desired),
				}
			}
			return DiffLoadedMsg{Dst: Op.Dst, Title: title, Err: err}
		}

		labelA, labelB, note := planDiffLabelsAndNote(Op, m.IsSavePlan)
		diffText := engine.UnifiedDiff(onDisk, desired, labelA, labelB)

		return DiffLoadedMsg{Dst: Op.Dst, Title: title, DiffText: diffText, Note: note}
	}
}

func planDiffLabelsAndNote(Op app.PlanOp, isSavePlan bool) (string, string, string) {
	base := filepath.Base(Op.Dst)
	labelA := base + " (current)"
	labelB := base + " (desired)"
	if isSavePlan {
		labelA = base + " (in pack)"
		labelB = base + " (in harness)"
		return labelA, labelB, ""
	}
	note := ""
	switch Op.Kind {
	case app.PlanOpSettings, app.PlanOpMCP:
		if len(Op.MergeOps) > 0 {
			labelB = base + " (after merge)"
			note = "Merged settings diff: user-owned keys outside the shown hunks are preserved."
		}
	}
	return labelA, labelB, note
}

// ─────────────────────────────────────────────────────
// DiffViewModel — nested overlay for displaying a unified diff.
// ─────────────────────────────────────────────────────

type DiffViewModel struct {
	Title string
	Dst   string
	Note  string
	IsNew bool

	viewport viewport.Model
	Ready    bool
	ErrText  string
	Width    int
	Height   int
}

func NewDiffViewModel(Width, Height int, title, dst string) DiffViewModel {
	return DiffViewModel{
		Title:  title,
		Dst:    dst,
		Width:  Width,
		Height: Height,
	}
}

func (m *DiffViewModel) SetContent(diffText string, isNew bool, newBody string, errText string) {
	if errText != "" {
		m.ErrText = errText
		m.Ready = true
		return
	}

	m.IsNew = isNew

	maxW := diffViewportWidth(m.Width)
	var sb strings.Builder
	common.RenderUnifiedDiffWithNote(&sb, m.Title, m.Dst, diffText, isNew, newBody, m.Note, maxW, "─")

	vpH := diffViewportHeight(m.Height)
	vp := viewport.New(viewport.WithWidth(maxW), viewport.WithHeight(vpH))
	vp.SetContent(sb.String())
	m.viewport = vp
	m.Ready = true
}

func (m DiffViewModel) Update(msg tea.Msg) (DiffViewModel, tea.Cmd) {
	if m.Ready {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m DiffViewModel) View() string {
	if m.ErrText != "" {
		content := fmt.Sprintf("\n  %s\n\n  %s\n",
			previewTitleStyle.Render("Diff: "+m.Title),
			common.ErrorStyle.Render("Error: "+m.ErrText))
		return previewBorderStyle.
			Width(m.Width - 2).
			Height(m.Height - 2).
			Render(content)
	}

	if !m.Ready {
		return previewBorderStyle.
			Width(m.Width - 2).
			Height(m.Height - 2).
			Render("\n  Loading...")
	}

	pct := m.viewport.ScrollPercent()
	scrollInfo := fmt.Sprintf("%3.0f%%", pct*100)
	footer := common.DimStyle.Render(fmt.Sprintf("─── %s ───", scrollInfo))

	content := m.viewport.View() + "\n" + footer
	return previewBorderStyle.
		Width(max(m.Width-2, 1)).
		Height(max(m.Height-2, 1)).
		Render(content)
}

func diffViewportWidth(width int) int {
	return max(width-6, 20)
}

func diffViewportHeight(height int) int {
	return max(height-4, 1)
}
