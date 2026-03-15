package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
)

// saveStage tracks the current stage of the save pipeline.
type saveStage int

const (
	saveStageHarness   saveStage = iota // pick harness
	saveStageVectors                    // pick content vectors
	saveStageFiles                      // discover + select files
	saveStageDestPack                   // pick destination pack
	saveStageExecuting                  // running
	saveStageResult                     // show outcome
)

const saveNewPackSentinel = "Create new pack..."

type saveTabModel struct {
	configDir string
	registry  *harness.Registry
	stage     saveStage
	loading   bool
	loadErr   string

	// Stage 1: harness selection.
	availableHarnesses []domain.Harness
	harnessCursor      int
	selectedHarness    domain.Harness

	// Stage 2: vector selection.
	availableVectors []domain.PackCategory
	vectorSelected   map[domain.PackCategory]bool
	vectorCursor     int

	// Stage 3: file selection.
	candidates    []app.SaveCandidate
	sortedIndices []int                 // candidates sorted by state; computed once on arrival
	stateCounts   map[app.FileState]int // per-state counts; computed once on arrival
	fileCursor    int                   // index into sortedIndices (visual position)
	fileOffset    int                   // scroll offset within the rendered file list
	selCount      int                   // cached count of selected candidates

	// Resolved profile context, cached after first resolution.
	resolvedProfile *app.ResolveResult

	// Stage 4: destination pack.
	packOptions  []string // installed pack names + saveNewPackSentinel
	packCursor   int
	destPackName string
	newPackInput bool   // true when typing a new pack name
	newPackName  string // accumulated input

	// Result.
	result    *app.SavePipelineResult
	resultErr string

	diffView *diffViewModel

	width, height int
}

func newSaveTabModel(configDir string, reg *harness.Registry) saveTabModel {
	return saveTabModel{configDir: configDir, registry: reg}
}

func (m saveTabModel) Update(msg tea.Msg) (saveTabModel, tea.Cmd) {
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

	if m.diffView != nil {
		return m.updateDiffView(msg)
	}

	switch msg := msg.(type) {
	case harnessDetectedMsg:
		m.loading = false
		if msg.err != nil {
			m.loadErr = msg.err.Error()
			return m, nil
		}
		m.loadErr = ""
		m.availableHarnesses = msg.harnesses
		m.harnessCursor = 0
		m.stage = saveStageHarness
		return m, nil

	case vectorsDiscoveredMsg:
		m.loading = false
		if msg.err != nil {
			m.loadErr = msg.err.Error()
			return m, nil
		}
		m.loadErr = ""
		m.availableVectors = msg.vectors
		m.vectorSelected = make(map[domain.PackCategory]bool, len(msg.vectors))
		for _, v := range msg.vectors {
			m.vectorSelected[v] = true // all selected by default
		}
		m.vectorCursor = 0
		m.stage = saveStageVectors
		return m, nil

	case saveFilesDiscoveredMsg:
		m.loading = false
		if msg.err != nil {
			m.loadErr = msg.err.Error()
			return m, nil
		}
		m.loadErr = ""
		if len(msg.warnings) > 0 {
			m.loadErr = "warning: " + msg.warnings[0]
		}
		m.candidates = msg.candidates
		m.fileCursor = 0
		m.fileOffset = 0
		m.stage = saveStageFiles
		m.sortedIndices = buildSortedIndices(m.candidates)
		m.selCount = countSelected(m.candidates)
		m.stateCounts = buildStateCounts(m.candidates)
		return m, nil

	case savePipelineDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.resultErr = msg.err.Error()
			m.result = nil
		} else {
			m.resultErr = ""
			m.result = msg.result
		}
		m.stage = saveStageResult
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m saveTabModel) handleKey(msg tea.KeyMsg) (saveTabModel, tea.Cmd) {
	if m.loading {
		return m, nil
	}

	// Text input mode for new pack name.
	if m.newPackInput {
		return m.handleNewPackInput(msg)
	}

	switch m.stage {
	case saveStageHarness:
		return m.handleHarnessKey(msg)
	case saveStageVectors:
		return m.handleVectorsKey(msg)
	case saveStageFiles:
		return m.handleFilesKey(msg)
	case saveStageDestPack:
		return m.handleDestPackKey(msg)
	case saveStageResult:
		return m.handleResultKey(msg)
	}
	return m, nil
}

func (m saveTabModel) handleHarnessKey(msg tea.KeyMsg) (saveTabModel, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.harnessCursor < len(m.availableHarnesses)-1 {
			m.harnessCursor++
		}
	case "k", "up":
		if m.harnessCursor > 0 {
			m.harnessCursor--
		}
	case "enter":
		if len(m.availableHarnesses) > 0 {
			m.selectedHarness = m.availableHarnesses[m.harnessCursor]
			m.loading = true
			m.stage = saveStageVectors
			return m, discoverVectors(m.selectedHarness, m.configDir, m.registry)
		}
	}
	return m, nil
}

func (m saveTabModel) handleVectorsKey(msg tea.KeyMsg) (saveTabModel, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.vectorCursor < len(m.availableVectors)-1 {
			m.vectorCursor++
		}
	case "k", "up":
		if m.vectorCursor > 0 {
			m.vectorCursor--
		}
	case " ":
		if m.vectorCursor < len(m.availableVectors) {
			cat := m.availableVectors[m.vectorCursor]
			m.vectorSelected[cat] = !m.vectorSelected[cat]
		}
	case "enter":
		return m.advanceToFiles()
	case "esc":
		m.stage = saveStageHarness
		m.loadErr = ""
	}
	return m, nil
}

func (m saveTabModel) handleFilesKey(msg tea.KeyMsg) (saveTabModel, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.fileCursor < len(m.sortedIndices)-1 {
			m.fileCursor++
			m.clampFileOffset()
		}
	case "k", "up":
		if m.fileCursor > 0 {
			m.fileCursor--
			m.clampFileOffset()
		}
	case " ":
		if m.fileCursor < len(m.sortedIndices) {
			ci := m.sortedIndices[m.fileCursor]
			if m.candidates[ci].Selected {
				m.selCount--
			} else {
				m.selCount++
			}
			m.candidates[ci].Selected = !m.candidates[ci].Selected
		}
	case "a":
		// Toggle all.
		allSelected := m.selCount == len(m.candidates)
		if allSelected {
			for i := range m.candidates {
				m.candidates[i].Selected = false
			}
			m.selCount = 0
		} else {
			for i := range m.candidates {
				m.candidates[i].Selected = true
			}
			m.selCount = len(m.candidates)
		}
	case "enter":
		if c := m.currentCandidate(); c != nil {
			return m, func() tea.Msg {
				return previewRequestMsg{
					title:    saveCandidateLabel(c.HarnessFile),
					category: c.Category,
					packName: c.PackName,
					filePath: c.HarnessPath,
				}
			}
		}
	case "s":
		if m.selCount > 0 {
			return m.advanceToPack()
		}
	case "v":
		if c := m.currentCandidate(); c != nil {
			title := saveCandidateLabel(c.HarnessFile)
			dv := newDiffViewModel(m.width, m.height, title, shortPath(c.HarnessPath))
			m.diffView = &dv
			return m, m.loadCandidateDiff(*c)
		}
	case "esc":
		m.stage = saveStageVectors
		m.loadErr = ""
	}
	return m, nil
}

func (m saveTabModel) handleDestPackKey(msg tea.KeyMsg) (saveTabModel, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.packCursor < len(m.packOptions)-1 {
			m.packCursor++
		}
	case "k", "up":
		if m.packCursor > 0 {
			m.packCursor--
		}
	case "enter":
		if len(m.packOptions) == 0 {
			return m, nil
		}
		chosen := m.packOptions[m.packCursor]
		if chosen == saveNewPackSentinel {
			m.newPackInput = true
			m.newPackName = ""
			return m, nil
		}
		return m.executePipeline(chosen, false)
	case "esc":
		m.stage = saveStageFiles
		m.loadErr = ""
	}
	return m, nil
}

func (m saveTabModel) handleNewPackInput(msg tea.KeyMsg) (saveTabModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.newPackName)
		if name == "" {
			return m, nil
		}
		m.newPackInput = false
		return m.executePipeline(name, true)
	case "esc":
		m.newPackInput = false
	case "backspace":
		if len(m.newPackName) > 0 {
			m.newPackName = m.newPackName[:len(m.newPackName)-1]
		}
	default:
		if len(msg.String()) == 1 {
			m.newPackName += msg.String()
		}
	}
	return m, nil
}

func (m saveTabModel) handleResultKey(msg tea.KeyMsg) (saveTabModel, tea.Cmd) {
	switch msg.String() {
	case "enter", "esc":
		// Reset to stage 1.
		m.stage = saveStageHarness
		m.result = nil
		m.resultErr = ""
		m.candidates = nil
	}
	return m, nil
}

// advanceToFiles collects selected vectors and fires discovery.
func (m saveTabModel) advanceToFiles() (saveTabModel, tea.Cmd) {
	var categories []domain.PackCategory
	for _, v := range m.availableVectors {
		if m.vectorSelected[v] {
			categories = append(categories, v)
		}
	}
	if len(categories) == 0 {
		return m, nil
	}
	m.loading = true
	m.stage = saveStageFiles

	// Resolve and cache profile context for reuse in executePipeline.
	res, _, err := app.ResolveActiveProfile(m.configDir)
	if err != nil {
		m.loadErr = fmt.Sprintf("resolve profile: %s", err)
		m.loading = false
		return m, nil
	}
	m.resolvedProfile = &res

	return m, discoverSaveFiles(app.DiscoverSaveRequest{
		HarnessID:  m.selectedHarness,
		Categories: categories,
		Scope:      res.TargetSpec.Scope,
		ProjectDir: res.TargetSpec.ProjectDir,
		Home:       res.TargetSpec.Home,
		ConfigDir:  m.configDir,
	}, m.registry)
}

// advanceToPack loads pack list and transitions to stage 4.
func (m saveTabModel) advanceToPack() (saveTabModel, tea.Cmd) {
	names, _ := app.InstalledPackNames(m.configDir)
	m.packOptions = append(names, saveNewPackSentinel)
	m.packCursor = 0
	m.stage = saveStageDestPack
	return m, nil
}

// executePipeline fires the async pipeline execution.
func (m saveTabModel) executePipeline(packName string, createPack bool) (saveTabModel, tea.Cmd) {
	m.destPackName = packName
	m.loading = true
	m.stage = saveStageExecuting

	// Use cached profile from advanceToFiles, or resolve fresh if not available.
	var res app.ResolveResult
	if m.resolvedProfile != nil {
		res = *m.resolvedProfile
	} else {
		var err error
		res, _, err = app.ResolveActiveProfile(m.configDir)
		if err != nil {
			m.loadErr = fmt.Sprintf("resolve profile: %s", err)
			m.loading = false
			return m, nil
		}
	}

	// Collect selected candidates.
	var selected []app.SaveCandidate
	for _, c := range m.candidates {
		if c.Selected {
			selected = append(selected, c)
		}
	}

	return m, executeSavePipeline(app.SavePipelineRequest{
		Candidates: selected,
		PackName:   packName,
		ConfigDir:  m.configDir,
		Scope:      res.TargetSpec.Scope,
		ProjectDir: res.TargetSpec.ProjectDir,
		Home:       res.TargetSpec.Home,
		HarnessID:  m.selectedHarness,
		CreatePack: createPack,
	}, m.registry)
}

// selectedCount returns the cached number of selected candidates.
func (m saveTabModel) selectedCount() int {
	return m.selCount
}

// buildSortedIndices returns candidate indices sorted by state sort key.
func buildSortedIndices(candidates []app.SaveCandidate) []int {
	indices := make([]int, len(candidates))
	for i := range indices {
		indices[i] = i
	}
	sort.Slice(indices, func(a, b int) bool {
		return app.StateSortKey(candidates[indices[a]].State) < app.StateSortKey(candidates[indices[b]].State)
	})
	return indices
}

// buildStateCounts tallies candidates by FileState.
func buildStateCounts(candidates []app.SaveCandidate) map[app.FileState]int {
	counts := make(map[app.FileState]int, 5)
	for _, c := range candidates {
		counts[c.State]++
	}
	return counts
}

// countSelected counts the number of selected candidates.
func countSelected(candidates []app.SaveCandidate) int {
	n := 0
	for _, c := range candidates {
		if c.Selected {
			n++
		}
	}
	return n
}

// currentFile returns the focused file for preview, or nil.
func (m saveTabModel) currentFile() *app.HarnessFile {
	if m.stage != saveStageFiles || m.fileCursor < 0 || m.fileCursor >= len(m.sortedIndices) {
		return nil
	}
	return &m.candidates[m.sortedIndices[m.fileCursor]].HarnessFile
}

func (m saveTabModel) currentCandidate() *app.SaveCandidate {
	if m.stage != saveStageFiles || m.fileCursor < 0 || m.fileCursor >= len(m.sortedIndices) {
		return nil
	}
	return &m.candidates[m.sortedIndices[m.fileCursor]]
}

// helpText returns stage-specific key binding hints.
func (m saveTabModel) helpText() string {
	switch m.stage {
	case saveStageHarness:
		return "j/k:navigate  enter:select  tab:switch  esc:quit"
	case saveStageVectors:
		return "j/k:navigate  space:toggle  enter:discover  esc:back"
	case saveStageFiles:
		return "j/k:navigate  space:toggle  a:toggle-all  v:diff  enter:preview  s:save  esc:back"
	case saveStageDestPack:
		if m.newPackInput {
			return "type name  enter:create  esc:cancel"
		}
		return "j/k:navigate  enter:select  esc:back"
	case saveStageResult:
		return "enter/esc:done"
	}
	return ""
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m saveTabModel) View() string {
	if m.diffView != nil {
		return m.diffView.View()
	}
	if m.loading {
		label := "Loading..."
		switch m.stage {
		case saveStageHarness:
			label = "Detecting harnesses..."
		case saveStageVectors:
			label = "Discovering content..."
		case saveStageFiles:
			label = "Scanning files..."
		case saveStageExecuting:
			label = fmt.Sprintf("Saving to %s...", m.destPackName)
		}
		return contentStyle.Render(dimStyle.Render(label))
	}
	if m.loadErr != "" {
		return contentStyle.Render(errorStyle.Render("Error: " + m.loadErr))
	}

	switch m.stage {
	case saveStageHarness:
		return contentStyle.Render(m.viewHarness())
	case saveStageVectors:
		return contentStyle.Render(m.viewVectors())
	case saveStageFiles:
		return contentStyle.Render(m.viewFiles())
	case saveStageDestPack:
		return contentStyle.Render(m.viewDestPack())
	case saveStageResult:
		return contentStyle.Render(m.viewResult())
	}
	return ""
}

func (m saveTabModel) viewBreadcrumb() string {
	stages := []string{"Harness", "Vectors", "Files", "Pack"}
	idx := int(m.stage)
	if idx >= len(stages) {
		idx = len(stages) - 1
	}
	var parts []string
	for i, s := range stages {
		if i < idx {
			parts = append(parts, dimStyle.Render(s))
		} else if i == idx {
			parts = append(parts, selectedStyle.Render(s))
		} else {
			parts = append(parts, dimStyle.Render(s))
		}
	}
	return strings.Join(parts, dimStyle.Render(" → ")) + "\n\n"
}

func (m saveTabModel) viewHarness() string {
	if len(m.availableHarnesses) == 0 {
		return m.viewBreadcrumb() + panelHeaderStyle.Render("Select harness") + "\n\n" +
			dimStyle.Render("No harnesses with content found.")
	}

	col1W := m.width * 45 / 100
	if col1W < 30 {
		col1W = 30
	}
	col2W := m.width - col1W - 6
	if col2W < 20 {
		col2W = 20
	}

	// Left: harness list.
	left := lipgloss.NewStyle().Width(col1W)
	var lb strings.Builder
	lb.WriteString(panelHeaderStyle.Render("Select harness") + "\n\n")
	for i, h := range m.availableHarnesses {
		cursor := "  "
		if i == m.harnessCursor {
			cursor = selectedStyle.Render("> ")
		}
		label := string(h)
		if i == m.harnessCursor {
			label = selectedStyle.Render(label)
		}
		lb.WriteString(fmt.Sprintf("%s%s\n", cursor, label))
	}

	// Right: detail for highlighted harness.
	right := lipgloss.NewStyle().Width(col2W)
	var rb strings.Builder
	rb.WriteString(panelHeaderStyle.Render("Detail") + "\n\n")
	if m.harnessCursor < len(m.availableHarnesses) {
		h := m.availableHarnesses[m.harnessCursor]
		name, desc := harnessDisplayInfo(h)
		rb.WriteString(selectedStyle.Render(name) + "\n\n")
		rb.WriteString(desc + "\n\n")
		rb.WriteString(dimStyle.Render("Supported content:") + "\n")
		for _, cat := range domain.AllPackCategories() {
			rb.WriteString(fmt.Sprintf("  %s\n", cat.Label()))
		}
		rb.WriteString("  Settings\n")
	}

	sep := dimStyle.Render(" │ ")
	return m.viewBreadcrumb() + lipgloss.JoinHorizontal(lipgloss.Top,
		left.Render(lb.String()), sep, right.Render(rb.String()))
}

func (m saveTabModel) viewVectors() string {
	if len(m.availableVectors) == 0 {
		return m.viewBreadcrumb() +
			panelHeaderStyle.Render(fmt.Sprintf("Content types (%s)", m.selectedHarness)) + "\n\n" +
			dimStyle.Render("No content found for this harness.")
	}

	col1W := m.width * 45 / 100
	if col1W < 30 {
		col1W = 30
	}
	col2W := m.width - col1W - 6
	if col2W < 20 {
		col2W = 20
	}

	// Left: vector checklist.
	left := lipgloss.NewStyle().Width(col1W)
	var lb strings.Builder
	lb.WriteString(panelHeaderStyle.Render(fmt.Sprintf("Content types (%s)", m.selectedHarness)) + "\n\n")
	for i, v := range m.availableVectors {
		cursor := "  "
		if i == m.vectorCursor {
			cursor = selectedStyle.Render("> ")
		}
		check := treeCheckOff
		if m.vectorSelected[v] {
			check = treeCheckOn
		}
		label := v.Label()
		if i == m.vectorCursor {
			label = selectedStyle.Render(label)
		}
		lb.WriteString(fmt.Sprintf("%s%s %s\n", cursor, check, label))
	}

	// Right: detail for highlighted vector.
	right := lipgloss.NewStyle().Width(col2W)
	var rb strings.Builder
	rb.WriteString(panelHeaderStyle.Render("Detail") + "\n\n")
	if m.vectorCursor < len(m.availableVectors) {
		v := m.availableVectors[m.vectorCursor]
		rb.WriteString(selectedStyle.Render(v.Label()) + "\n\n")
		rb.WriteString(categoryDescription(v) + "\n")
	}
	selCount := 0
	for _, v := range m.availableVectors {
		if m.vectorSelected[v] {
			selCount++
		}
	}
	rb.WriteString("\n" + dimStyle.Render("─── Summary ───") + "\n")
	rb.WriteString(fmt.Sprintf("%d of %d selected for discovery\n", selCount, len(m.availableVectors)))

	sep := dimStyle.Render(" │ ")
	return m.viewBreadcrumb() + lipgloss.JoinHorizontal(lipgloss.Top,
		left.Render(lb.String()), sep, right.Render(rb.String()))
}

func (m saveTabModel) viewFiles() string {
	col1W := m.width * 45 / 100
	if col1W < 35 {
		col1W = 35
	}
	col2W := m.width - col1W - 6
	if col2W < 20 {
		col2W = 20
	}

	left := m.viewFileList(col1W)
	sep := dimStyle.Render(" │ ")
	right := m.viewFileDetail(col2W)

	return m.viewBreadcrumb() + lipgloss.JoinHorizontal(lipgloss.Top, left, sep, right)
}

func (m saveTabModel) viewFileList(width int) string {
	style := lipgloss.NewStyle().Width(width)
	var sb strings.Builder

	selected := m.selectedCount()
	sb.WriteString(panelHeaderStyle.Render(fmt.Sprintf("Files (%d/%d selected)", selected, len(m.candidates))) + "\n")

	// Compute available lines for file list (reserve header and group headers).
	maxLines := m.height - 6
	if maxLines < 5 {
		maxLines = 5
	}

	lines, cursorLine := m.buildFileListLines()
	offset := clampOffset(cursorLine, m.fileOffset, maxLines)
	start := min(offset, len(lines))
	end := min(start+maxLines, len(lines))
	for _, line := range lines[start:end] {
		sb.WriteString(line + "\n")
	}
	if end < len(lines) {
		sb.WriteString(dimStyle.Render(fmt.Sprintf("  ... %d more", len(lines)-end)) + "\n")
	}

	return style.Render(sb.String())
}

func (m *saveTabModel) clampFileOffset() {
	_, cursorLine := m.buildFileListLines()
	m.fileOffset = clampOffset(cursorLine, m.fileOffset, m.visibleFileListHeight())
}

func (m saveTabModel) visibleFileListHeight() int {
	maxLines := m.height - 6
	if maxLines < 5 {
		return 5
	}
	return maxLines
}

func (m saveTabModel) buildFileListLines() ([]string, int) {
	type stateGroup struct {
		state app.FileState
		label string
	}
	groups := []stateGroup{
		{app.FileConflict, "Conflicts"},
		{app.FileModified, "Modified"},
		{app.FileSettings, "Settings"},
		{app.FileUntracked, "Untracked"},
		{app.FileClean, "Clean"},
	}

	lines := make([]string, 0, len(m.candidates)+len(groups)*2)
	cursorLine := 0
	visualPos := 0
	for _, g := range groups {
		count := m.stateCounts[g.state]
		if count == 0 {
			continue
		}
		lines = append(lines, "", dimStyle.Render(fmt.Sprintf(" %s (%d)", g.label, count)))

		for _, idx := range m.sortedIndices {
			c := m.candidates[idx]
			if c.State != g.state {
				continue
			}

			isCurrent := visualPos == m.fileCursor
			if isCurrent {
				cursorLine = len(lines)
			}

			cursor := "  "
			if isCurrent {
				cursor = selectedStyle.Render("> ")
			}

			check := treeCheckOff
			if c.Selected {
				check = treeCheckOn
			}

			icon := fileStateIcon(c.State)
			name := saveCandidateLabel(c.HarnessFile)
			nameStyle := lipgloss.NewStyle()
			if isCurrent {
				nameStyle = selectedStyle
			}

			scopeTag := ""
			if c.Scope != "" {
				scopeTag = "  " + dimStyle.Render("["+string(c.Scope)+"]")
			}
			packLabel := ""
			if c.PackName != "" {
				packLabel = "  " + dimStyle.Render(c.PackName)
			}
			pathLabel := ""
			if c.Category == domain.CategoryMCP {
				pathLabel = "  " + dimStyle.Render(shortPath(c.HarnessPath))
			}

			lines = append(lines, fmt.Sprintf("%s%s %s %s%s%s%s", cursor, check, icon, nameStyle.Render(name), scopeTag, pathLabel, packLabel))
			visualPos++
		}
	}

	return lines, cursorLine
}

func (m saveTabModel) viewFileDetail(width int) string {
	style := lipgloss.NewStyle().Width(width)
	var sb strings.Builder

	sb.WriteString(panelHeaderStyle.Render("Detail") + "\n\n")

	if m.fileCursor < 0 || m.fileCursor >= len(m.sortedIndices) {
		sb.WriteString(dimStyle.Render("(no file selected)"))
		return style.Render(sb.String())
	}

	c := m.candidates[m.sortedIndices[m.fileCursor]]
	name := saveCandidateLabel(c.HarnessFile)
	sb.WriteString(fmt.Sprintf("%s %s\n\n", fileStateIcon(c.State), name))
	sb.WriteString(fmt.Sprintf("State:    %s\n", fileStateLabel(c.State)))
	sb.WriteString(fmt.Sprintf("Scope:    %s\n", scopeLabel(c.Scope)))
	sb.WriteString(fmt.Sprintf("Category: %s\n", c.Category.Label()))
	if c.PackName != "" {
		sb.WriteString(fmt.Sprintf("Pack:     %s\n", c.PackName))
	} else {
		sb.WriteString(fmt.Sprintf("Pack:     %s\n", dimStyle.Render("(none)")))
	}
	sb.WriteString(fmt.Sprintf("Size:     %s\n", formatSize(c.Size)))
	pathLabel := "Path"
	if c.Category == domain.CategoryMCP {
		pathLabel = "Config"
	}
	sb.WriteString(fmt.Sprintf("%-9s %s\n", pathLabel+":", dimStyle.Render(shortPath(c.HarnessPath))))
	sb.WriteString(fmt.Sprintf("Selected: %v\n", c.Selected))

	// Summary.
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("─── Summary ───") + "\n")
	counts := m.stateCounts
	sb.WriteString(fmt.Sprintf("Total: %d  Selected: %d\n", len(m.candidates), m.selCount))
	for _, pair := range []struct {
		s app.FileState
		l string
	}{
		{app.FileConflict, "Conflicts"},
		{app.FileModified, "Modified"},
		{app.FileSettings, "Settings"},
		{app.FileUntracked, "Untracked"},
		{app.FileClean, "Clean"},
	} {
		if n := counts[pair.s]; n > 0 {
			sb.WriteString(fmt.Sprintf("  %s %s: %d\n", fileStateIcon(pair.s), pair.l, n))
		}
	}

	return style.Render(sb.String())
}

func (m saveTabModel) viewDestPack() string {
	var sb strings.Builder
	sb.WriteString(m.viewBreadcrumb())
	sb.WriteString(panelHeaderStyle.Render(fmt.Sprintf("Destination pack (%d files)", m.selectedCount())) + "\n\n")

	if m.newPackInput {
		sb.WriteString("  Pack name: " + selectedStyle.Render(m.newPackName+"_") + "\n")
		return sb.String()
	}

	for i, name := range m.packOptions {
		cursor := "  "
		if i == m.packCursor {
			cursor = selectedStyle.Render("> ")
		}
		label := name
		if i == m.packCursor {
			label = selectedStyle.Render(label)
		}
		sb.WriteString(fmt.Sprintf("%s%s\n", cursor, label))
	}
	return sb.String()
}

func (m saveTabModel) viewResult() string {
	var sb strings.Builder
	sb.WriteString(panelHeaderStyle.Render("Save Complete") + "\n\n")

	if m.resultErr != "" {
		sb.WriteString(errorStyle.Render("Error: "+m.resultErr) + "\n")
		return sb.String()
	}
	if m.result == nil {
		return sb.String()
	}

	r := m.result
	if r.PackCreated {
		sb.WriteString(fmt.Sprintf("Created new pack: %s\n\n", selectedStyle.Render(m.destPackName)))
	} else {
		sb.WriteString(fmt.Sprintf("Saved to pack: %s\n\n", selectedStyle.Render(m.destPackName)))
	}

	if len(r.SavedFiles) > 0 {
		sb.WriteString(fmt.Sprintf("Saved %d file(s):\n", len(r.SavedFiles)))
		for _, f := range r.SavedFiles {
			sb.WriteString(fmt.Sprintf("  %s → %s\n", dimStyle.Render(filepath.Base(f.HarnessPath)), dimStyle.Render(shortPath(f.PackPath))))
		}
	}
	if len(r.Conflicts) > 0 {
		sb.WriteString(fmt.Sprintf("\n%s %d conflict(s) skipped (use --force to override):\n",
			fileStateIcon(app.FileConflict), len(r.Conflicts)))
		for _, c := range r.Conflicts {
			sb.WriteString(fmt.Sprintf("  %s\n", filepath.Base(c.HarnessPath)))
		}
	}
	if len(r.SecretFindings) > 0 {
		sb.WriteString(fmt.Sprintf("\n%s Secret findings:\n", errorStyle.Render("!")))
		for _, f := range r.SecretFindings {
			sb.WriteString(fmt.Sprintf("  %s\n", f))
		}
	}

	sb.WriteString(dimStyle.Render("\nPress enter or esc to continue."))
	return sb.String()
}

func (m saveTabModel) updateDiffView(msg tea.Msg) (saveTabModel, tea.Cmd) {
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

func (m saveTabModel) loadCandidateDiff(c app.SaveCandidate) tea.Cmd {
	return func() tea.Msg {
		title := saveCandidateLabel(c.HarnessFile)
		if c.Kind == domain.CopyKindDir {
			return diffLoadedMsg{
				dst:   c.HarnessPath,
				title: title,
				err:   fmt.Errorf("diff unavailable for directories"),
			}
		}

		desired := c.Content
		if desired == nil {
			content, err := os.ReadFile(c.HarnessPath)
			if err != nil {
				return diffLoadedMsg{dst: c.HarnessPath, title: title, err: err}
			}
			desired = content
		}

		if c.PackPath == "" {
			return diffLoadedMsg{
				dst:     c.HarnessPath,
				title:   title,
				isNew:   true,
				newBody: string(desired),
			}
		}

		onDisk, err := os.ReadFile(c.PackPath)
		if err != nil {
			if os.IsNotExist(err) {
				return diffLoadedMsg{
					dst:     c.PackPath,
					title:   title,
					isNew:   true,
					newBody: string(desired),
				}
			}
			return diffLoadedMsg{dst: c.PackPath, title: title, err: err}
		}

		labelA := filepath.Base(c.PackPath) + " (in pack)"
		labelB := title + " (in harness)"
		diffText := app.ComputeDiff(onDisk, desired, labelA, labelB)
		return diffLoadedMsg{dst: c.PackPath, title: title, diffText: diffText}
	}
}

// ---------------------------------------------------------------------------
// Display helpers (preserved from original)
// ---------------------------------------------------------------------------

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

func saveCandidateLabel(f app.HarnessFile) string {
	if f.Category == domain.CategoryMCP && f.RelPath != "" {
		return f.RelPath
	}
	return filepath.Base(f.HarnessPath)
}

// rediscoverFiles re-runs file discovery with the current harness/vector/profile state.
func (m saveTabModel) rediscoverFiles() tea.Cmd {
	var categories []domain.PackCategory
	for _, v := range m.availableVectors {
		if m.vectorSelected[v] {
			categories = append(categories, v)
		}
	}
	if len(categories) == 0 || m.resolvedProfile == nil {
		return nil
	}
	return discoverSaveFiles(app.DiscoverSaveRequest{
		HarnessID:  m.selectedHarness,
		Categories: categories,
		Scope:      m.resolvedProfile.TargetSpec.Scope,
		ProjectDir: m.resolvedProfile.TargetSpec.ProjectDir,
		Home:       m.resolvedProfile.TargetSpec.Home,
		ConfigDir:  m.configDir,
	}, m.registry)
}

// harnessDisplayInfo returns a display name and description for a harness.
func harnessDisplayInfo(h domain.Harness) (string, string) {
	switch h {
	case domain.HarnessCline:
		return "Cline", "VS Code / Cursor extension for AI-assisted coding."
	case domain.HarnessClaudeCode:
		return "Claude Code", "Anthropic terminal CLI agent."
	case domain.HarnessCodex:
		return "Codex", "OpenAI Codex terminal CLI."
	case domain.HarnessOpenCode:
		return "OpenCode", "Open-source terminal coding agent."
	}
	return string(h), ""
}

// categoryDescription returns a brief description of a content category.
func categoryDescription(c domain.PackCategory) string {
	switch c {
	case domain.CategoryRules:
		return "Always-loaded behavioral constraints that shape\nhow the agent operates."
	case domain.CategoryAgents:
		return "Constrained tool-using personas with specific\ncapabilities and permissions."
	case domain.CategoryWorkflows:
		return "Executable multi-step processes invoked by\nname or trigger condition."
	case domain.CategorySkills:
		return "On-demand knowledge and methodology, loaded\nwhen a matching trigger fires."
	case domain.CategoryMCP:
		return "MCP server configurations that provide\nexternal tool access."
	case domain.CategorySettings:
		return "Harness-specific settings files controlling\nagent behavior and permissions."
	}
	return ""
}

func scopeLabel(s domain.Scope) string {
	switch s {
	case domain.ScopeGlobal:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Render("global")
	case domain.ScopeProject:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("149")).Render("project")
	default:
		return dimStyle.Render("unknown")
	}
}
