package save

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	tuiutil "github.com/shrug-labs/aipack/cmd/aipack/tui/util"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

type stage int

const (
	stageHarness   stage = iota // pick harness
	stageVectors                // pick content vectors
	stageFiles                  // discover + select files
	stageDestPack               // pick destination pack
	stageExecuting              // running
	stageResult                 // show outcome
)

const newPackSentinel = "Create new pack..."

type Model struct {
	ctx       context.Context
	eng       *engine.Engine
	configDir string
	registry  *harness.Registry
	stage     stage
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

	// Cached render of the file list. Refreshed on every Update path that
	// mutates candidates/sortedIndices/stateCounts/fileCursor/stage so that
	// View() and clickLayers() read it without rebuilding (the renderer is
	// quadratic over groups × candidates and ran 3× per render before).
	fileListLines      []fileListLine
	fileListCursorLine int

	// Targeting info, resolved from sync-config defaults (no profile needed).
	targetSpec *app.TargetSpec

	// Stage 4: destination pack.
	packOptions  []string // installed pack names + newPackSentinel
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

func New(ctx context.Context, eng *engine.Engine, configDir string, reg *harness.Registry) Model {
	return Model{ctx: ctx, eng: eng, configDir: configDir, registry: reg}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) SetSize(width, height int) common.Screen {
	m.width = width
	m.height = height
	if m.diffView != nil && m.diffView.ready {
		m.diffView.viewport.SetWidth(width - 4)
		m.diffView.viewport.SetHeight(height - 4)
	}
	return m
}

func (m Model) RefreshHarnesses() (Model, tea.Cmd) {
	m.loading = true
	m.stage = stageHarness
	return m, DetectHarnesses(m.registry)
}

func (m Model) RediscoverFiles() (Model, tea.Cmd) {
	m.loading = true
	return m, m.rediscoverFiles()
}

func (m Model) CapturesKey(msg tea.KeyPressMsg) bool {
	if m.newPackInput {
		return msg.String() != "ctrl+c"
	}
	if msg.String() == "esc" && m.stage > stageHarness {
		return true
	}
	if msg.String() == "s" && m.stage == stageFiles {
		return true
	}
	return false
}

func (m Model) HasFileActions() bool {
	return m.stage == stageFiles && m.currentFile() != nil
}

func (m Model) CurrentFile() *app.HarnessFile {
	f := m.currentFile()
	if f == nil {
		return nil
	}
	copy := *f
	return &copy
}

func (m Model) CurrentFileLabel() string {
	if f := m.currentFile(); f != nil {
		return CandidateLabel(*f)
	}
	return ""
}

func (m Model) CurrentHarnessPath() string {
	if f := m.currentFile(); f != nil {
		return f.HarnessPath
	}
	return ""
}

func (m Model) OpenCurrentDiff() (Model, tea.Cmd) {
	c := m.currentCandidate()
	if c == nil {
		return m, nil
	}
	title := CandidateLabel(c.HarnessFile)
	dv := newDiffViewModel(m.width, m.height, title, tuiutil.ShortPath(c.HarnessPath))
	m.diffView = &dv
	return m, m.loadCandidateDiff(*c)
}

func (m Model) SelectCurrentOnlyAndAdvanceToPack() (Model, tea.Cmd) {
	for i := range m.candidates {
		m.candidates[i].Selected = false
	}
	m.selCount = 0
	if c := m.currentCandidate(); c != nil {
		c.Selected = true
		m.selCount = 1
		return m.advanceToPack()
	}
	return m, nil
}

func (m Model) HelpText() string {
	return m.helpText()
}

func (m Model) DiffOpen() bool {
	return m.diffView != nil
}

// resolveTargetSpec builds targeting info from sync-config defaults and the
// environment. This is the TUI's I/O boundary for the save pipeline — it
// reads cwd and sync-config from disk but does not require profile resolution.
func (m Model) resolveTargetSpec() (app.TargetSpec, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return app.TargetSpec{}, fmt.Errorf("resolving working directory: %w", err)
	}
	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(m.configDir))
	if err != nil {
		return app.TargetSpec{}, fmt.Errorf("loading sync-config: %w", err)
	}
	return app.ResolveTargetSpec(syncCfg, m.configDir, cwd, config.HomeDir())
}

func (m Model) Update(msg tea.Msg) (common.Screen, tea.Cmd) {
	if msg, ok := msg.(DiffLoadedMsg); ok {
		if m.diffView != nil {
			errText := ""
			if msg.Err != nil {
				errText = msg.Err.Error()
			}
			m.diffView.setContent(msg.DiffText, msg.IsNew, msg.NewBody, errText)
		}
		return m, nil
	}

	if m.diffView != nil {
		return m.updateDiffView(msg)
	}

	switch msg := msg.(type) {
	case common.FocusMsg, common.BlurMsg:
		return m, nil
	case common.LayerHitMsg:
		if !strings.HasPrefix(msg.ID, "save:") {
			return m, nil
		}
		return m.handleLayerHit(msg)
	case HarnessDetectedMsg:
		m.loading = false
		m.loadErr = ""
		m.availableHarnesses = msg.Harnesses
		m.harnessCursor = 0
		m.stage = stageHarness
		return m, nil

	case VectorsDiscoveredMsg:
		m.loading = false
		if msg.Err != nil {
			m.loadErr = msg.Err.Error()
			return m, nil
		}
		m.loadErr = ""
		m.availableVectors = msg.Vectors
		m.vectorSelected = make(map[domain.PackCategory]bool, len(msg.Vectors))
		for _, v := range msg.Vectors {
			m.vectorSelected[v] = true // all selected by default
		}
		m.vectorCursor = 0
		m.stage = stageVectors
		return m, nil

	case FilesDiscoveredMsg:
		m.loading = false
		if msg.Err != nil {
			m.loadErr = msg.Err.Error()
			return m, nil
		}
		m.loadErr = ""
		if len(msg.Warnings) > 0 {
			m.loadErr = "warning: " + msg.Warnings[0]
		}
		m.candidates = msg.Candidates
		m.fileCursor = 0
		m.fileOffset = 0
		m.stage = stageFiles
		m.sortedIndices = buildSortedIndices(m.candidates)
		m.selCount = countSelected(m.candidates)
		m.stateCounts = buildStateCounts(m.candidates)
		return m.withFileList(), nil

	case PipelineDoneMsg:
		m.loading = false
		if msg.Err != nil {
			m.resultErr = msg.Err.Error()
			m.result = nil
		} else {
			m.resultErr = ""
			m.result = msg.Result
		}
		m.stage = stageResult
		return m, nil

	case tea.PasteMsg:
		if !m.loading && m.newPackInput {
			m.newPackName = common.AppendSingleLinePaste(m.newPackName, msg.Content)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (common.Screen, tea.Cmd) {
	if m.loading {
		return m, nil
	}

	// Text input mode for new pack name.
	if m.newPackInput {
		return m.handleNewPackInput(msg)
	}

	switch m.stage {
	case stageHarness:
		return m.handleHarnessKey(msg)
	case stageVectors:
		return m.handleVectorsKey(msg)
	case stageFiles:
		return m.handleFilesKey(msg)
	case stageDestPack:
		return m.handleDestPackKey(msg)
	case stageResult:
		return m.handleResultKey(msg)
	}
	return m, nil
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

	parts := strings.Split(msg.ID, ":")
	if len(parts) < 3 {
		return m, nil
	}
	idx := -1
	if len(parts) >= 3 {
		fmt.Sscanf(parts[2], "%d", &idx)
	}

	switch parts[1] {
	case "harness":
		if idx >= 0 && idx < len(m.availableHarnesses) {
			m.harnessCursor = idx
			return m.handleHarnessKey(tea.KeyPressMsg(tea.Key{Text: "enter", Code: tea.KeyEnter}))
		}
	case "vector":
		if idx >= 0 && idx < len(m.availableVectors) {
			m.vectorCursor = idx
			cat := m.availableVectors[idx]
			m.vectorSelected[cat] = !m.vectorSelected[cat]
		}
	case "file":
		if idx >= 0 && idx < len(m.sortedIndices) {
			m.fileCursor = idx
			ci := m.sortedIndices[idx]
			if m.candidates[ci].Selected {
				m.selCount--
			} else {
				m.selCount++
			}
			m.candidates[ci].Selected = !m.candidates[ci].Selected
			m.clampFileOffset()
		}
	case "pack":
		if idx >= 0 && idx < len(m.packOptions) {
			m.packCursor = idx
			return m.handleDestPackKey(tea.KeyPressMsg(tea.Key{Text: "enter", Code: tea.KeyEnter}))
		}
	}
	return m, nil
}

func (m Model) handleHarnessKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
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
			ts, err := m.resolveTargetSpec()
			if err != nil {
				m.loadErr = fmt.Sprintf("resolve targeting: %s", err)
				return m, nil
			}
			m.targetSpec = &ts
			m.loading = true
			m.stage = stageVectors
			return m, DiscoverVectors(m.ctx, m.selectedHarness, ts.ProjectDir, ts.Home, m.registry)
		}
	}
	return m, nil
}

func (m Model) handleVectorsKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.vectorCursor < len(m.availableVectors)-1 {
			m.vectorCursor++
		}
	case "k", "up":
		if m.vectorCursor > 0 {
			m.vectorCursor--
		}
	case "space":
		if m.vectorCursor < len(m.availableVectors) {
			cat := m.availableVectors[m.vectorCursor]
			m.vectorSelected[cat] = !m.vectorSelected[cat]
		}
	case "enter":
		return m.advanceToFiles()
	case "esc":
		m.stage = stageHarness
		m.loadErr = ""
	}
	return m, nil
}

func (m Model) handleFilesKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
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
	case "space":
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
				return common.PreviewRequestMsg{
					Title:    CandidateLabel(c.HarnessFile),
					Category: c.Category,
					PackName: c.PackName,
					FilePath: c.HarnessPath,
				}
			}
		}
	case "s":
		if m.selCount > 0 {
			return m.advanceToPack()
		}
	case "v":
		if c := m.currentCandidate(); c != nil {
			title := CandidateLabel(c.HarnessFile)
			dv := newDiffViewModel(m.width, m.height, title, tuiutil.ShortPath(c.HarnessPath))
			m.diffView = &dv
			return m, m.loadCandidateDiff(*c)
		}
	case "esc":
		m.stage = stageVectors
		m.loadErr = ""
	}
	return m.withFileList(), nil
}

func (m Model) handleDestPackKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
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
		if chosen == newPackSentinel {
			m.newPackInput = true
			m.newPackName = ""
			return m, nil
		}
		return m.executePipeline(chosen, false)
	case "esc":
		m.stage = stageFiles
		m.loadErr = ""
	}
	return m, nil
}

func (m Model) handleNewPackInput(msg tea.KeyPressMsg) (Model, tea.Cmd) {
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
		if msg.Text != "" {
			m.newPackName += msg.Text
		}
	}
	return m, nil
}

func (m Model) handleResultKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "esc":
		// Reset to stage 1.
		m.stage = stageHarness
		m.result = nil
		m.resultErr = ""
		m.candidates = nil
	}
	return m, nil
}

// advanceToFiles collects selected vectors and fires discovery.
func (m Model) advanceToFiles() (Model, tea.Cmd) {
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
	m.stage = stageFiles

	// Use cached targeting info, or resolve fresh if not available.
	var ts app.TargetSpec
	if m.targetSpec != nil {
		ts = *m.targetSpec
	} else {
		var err error
		ts, err = m.resolveTargetSpec()
		if err != nil {
			m.loadErr = fmt.Sprintf("resolve targeting: %s", err)
			m.loading = false
			return m, nil
		}
		m.targetSpec = &ts
	}

	return m, DiscoverFiles(m.ctx, m.eng, app.DiscoverSaveRequest{
		HarnessID:  m.selectedHarness,
		Categories: categories,
		Scope:      ts.Scope,
		ProjectDir: ts.ProjectDir,
		Home:       ts.Home,
		ConfigDir:  m.configDir,
	}, m.registry)
}

// advanceToPack loads pack list and transitions to stage 4.
func (m Model) advanceToPack() (Model, tea.Cmd) {
	names, _ := app.InstalledPackNames(m.configDir)
	m.packOptions = append(names, newPackSentinel)
	m.packCursor = 0
	m.stage = stageDestPack
	return m, nil
}

// executePipeline fires the async pipeline execution.
func (m Model) executePipeline(packName string, createPack bool) (Model, tea.Cmd) {
	m.destPackName = packName
	m.loading = true
	m.stage = stageExecuting

	// Use cached targeting info, or resolve fresh if not available.
	var ts app.TargetSpec
	if m.targetSpec != nil {
		ts = *m.targetSpec
	} else {
		var err error
		ts, err = m.resolveTargetSpec()
		if err != nil {
			m.loadErr = fmt.Sprintf("resolve targeting: %s", err)
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

	return m, RunPipeline(m.eng, app.SavePipelineRequest{
		Candidates: selected,
		PackName:   packName,
		ConfigDir:  m.configDir,
		Scope:      ts.Scope,
		ProjectDir: ts.ProjectDir,
		Home:       ts.Home,
		HarnessID:  m.selectedHarness,
		CreatePack: createPack,
	}, m.registry)
}

// selectedCount returns the cached number of selected candidates.
func (m Model) selectedCount() int {
	return m.selCount
}

// buildSortedIndices returns candidate indices sorted by state sort key.
func buildSortedIndices(candidates []app.SaveCandidate) []int {
	indices := make([]int, len(candidates))
	for i := range indices {
		indices[i] = i
	}
	slices.SortFunc(indices, func(a, b int) int {
		return cmp.Compare(app.StateSortKey(candidates[a].State), app.StateSortKey(candidates[b].State))
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
func (m Model) currentFile() *app.HarnessFile {
	if m.stage != stageFiles || m.fileCursor < 0 || m.fileCursor >= len(m.sortedIndices) {
		return nil
	}
	return &m.candidates[m.sortedIndices[m.fileCursor]].HarnessFile
}

func (m Model) currentCandidate() *app.SaveCandidate {
	if m.stage != stageFiles || m.fileCursor < 0 || m.fileCursor >= len(m.sortedIndices) {
		return nil
	}
	return &m.candidates[m.sortedIndices[m.fileCursor]]
}

// helpText returns stage-specific key binding hints.
func (m Model) helpText() string {
	switch m.stage {
	case stageHarness:
		return "j/k:navigate  enter:select │ 1-6/tab:switch  esc:quit"
	case stageVectors:
		return "j/k:navigate  space:toggle  enter:discover │ esc:back"
	case stageFiles:
		return "j/k:navigate  space:toggle  a:toggle-all  v:diff  enter:preview │ s:save  esc:back"
	case stageDestPack:
		if m.newPackInput {
			return "type name  enter:create │ esc:cancel"
		}
		return "j/k:navigate  enter:select │ esc:back"
	case stageResult:
		return "enter/esc:done"
	}
	return ""
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

type fileListLine struct {
	text       string
	fileCursor int
}

func (m Model) View() *lipgloss.Layer {
	// Populate the file-list cache once for both the render and the click
	// layer pass; tests that construct Models directly don't run Update so
	// the cache is empty here even though Update populates it in the live
	// path. Cheap when stage != stageFiles (early return inside withFileList).
	if m.stage == stageFiles && m.fileListLines == nil {
		m = m.withFileList()
	}
	root := lipgloss.NewLayer(m.render())
	root.AddLayers(m.clickLayers()...)
	return root
}

func (m Model) render() string {
	if m.diffView != nil {
		return m.diffView.View()
	}
	if m.loading {
		label := "Loading..."
		switch m.stage {
		case stageHarness:
			label = "Detecting harnesses..."
		case stageVectors:
			label = "Discovering content..."
		case stageFiles:
			label = "Scanning files..."
		case stageExecuting:
			label = fmt.Sprintf("Saving to %s...", m.destPackName)
		}
		return common.ContentStyle.Render(common.DimStyle.Render(label))
	}
	if m.loadErr != "" {
		return common.ContentStyle.Render(common.ErrorStyle.Render("Error: " + m.loadErr))
	}

	switch m.stage {
	case stageHarness:
		return common.ContentStyle.Render(m.viewHarness())
	case stageVectors:
		return common.ContentStyle.Render(m.viewVectors())
	case stageFiles:
		return common.ContentStyle.Render(m.viewFiles())
	case stageDestPack:
		return common.ContentStyle.Render(m.viewDestPack())
	case stageResult:
		return common.ContentStyle.Render(m.viewResult())
	}
	return ""
}

func (m Model) clickLayers() []*lipgloss.Layer {
	if m.loading || m.diffView != nil {
		return nil
	}

	const rowStartY = 4
	layers := []*lipgloss.Layer{}
	switch m.stage {
	case stageHarness:
		for i, h := range m.availableHarnesses {
			layers = append(layers,
				lipgloss.NewLayer(string(h)).ID(fmt.Sprintf("save:harness:%d", i)).X(0).Y(rowStartY+i).Z(-1),
			)
		}
	case stageVectors:
		for i, v := range m.availableVectors {
			layers = append(layers,
				lipgloss.NewLayer(v.Label()).ID(fmt.Sprintf("save:vector:%d", i)).X(0).Y(rowStartY+i).Z(-1),
			)
		}
	case stageFiles:
		lines := m.fileListLines
		offset := common.ClampOffset(m.fileListCursorLine, m.fileOffset, m.visibleFileListHeight())
		start := min(offset, len(lines))
		end := min(start+m.visibleFileListHeight(), len(lines))
		for visible, line := range lines[start:end] {
			if line.fileCursor < 0 || line.fileCursor >= len(m.sortedIndices) {
				continue
			}
			idx := m.sortedIndices[line.fileCursor]
			c := m.candidates[idx]
			layers = append(layers,
				lipgloss.NewLayer(CandidateLabel(c.HarnessFile)).ID(fmt.Sprintf("save:file:%d", line.fileCursor)).X(0).Y(rowStartY+visible).Z(-1),
			)
		}
	case stageDestPack:
		for i, name := range m.packOptions {
			layers = append(layers,
				lipgloss.NewLayer(name).ID(fmt.Sprintf("save:pack:%d", i)).X(0).Y(rowStartY+i).Z(-1),
			)
		}
	}
	return layers
}

func (m Model) viewBreadcrumb() string {
	stages := []string{"Harness", "Vectors", "Files", "Pack"}
	idx := int(m.stage)
	if idx >= len(stages) {
		idx = len(stages) - 1
	}
	var parts []string
	for i, s := range stages {
		if i < idx {
			parts = append(parts, common.DimStyle.Render(s))
		} else if i == idx {
			parts = append(parts, common.SelectedStyle.Render(s))
		} else {
			parts = append(parts, common.DimStyle.Render(s))
		}
	}
	return strings.Join(parts, common.DimStyle.Render(" → ")) + "\n\n"
}

func (m Model) viewHarness() string {
	if len(m.availableHarnesses) == 0 {
		return m.viewBreadcrumb() + common.SelectedStyle.Render("Select harness") + "\n\n" +
			common.DimStyle.Render("No harnesses with content found.")
	}

	col1W := max(m.width*45/100, 30)
	col2W := max(m.width-col1W-6, 20)

	// Left: harness list.
	left := lipgloss.NewStyle().Width(col1W)
	var lb strings.Builder
	lb.WriteString(common.SelectedStyle.Render("Select harness") + "\n\n")
	for i, h := range m.availableHarnesses {
		cursor := "  "
		if i == m.harnessCursor {
			cursor = common.SelectedStyle.Render("> ")
		}
		label := string(h)
		if i == m.harnessCursor {
			label = common.SelectedStyle.Render(label)
		}
		fmt.Fprintf(&lb, "%s%s\n", cursor, label)
	}

	// Right: detail for highlighted harness.
	right := lipgloss.NewStyle().Width(col2W)
	var rb strings.Builder
	rb.WriteString(common.SelectedStyle.Render("Detail") + "\n\n")
	if m.harnessCursor < len(m.availableHarnesses) {
		h := m.availableHarnesses[m.harnessCursor]
		name, desc := harnessDisplayInfo(h)
		rb.WriteString(common.SelectedStyle.Render(name) + "\n\n")
		rb.WriteString(desc + "\n\n")
		rb.WriteString(common.DimStyle.Render("Supported content:") + "\n")
		for _, cat := range domain.AllPackCategories() {
			fmt.Fprintf(&rb, "  %s\n", cat.Label())
		}
		rb.WriteString("  Settings\n")
	}

	sep := common.DimStyle.Render(" │ ")
	return m.viewBreadcrumb() + lipgloss.JoinHorizontal(lipgloss.Top,
		left.Render(lb.String()), sep, right.Render(rb.String()))
}

func (m Model) viewVectors() string {
	if len(m.availableVectors) == 0 {
		return m.viewBreadcrumb() +
			common.SelectedStyle.Render(fmt.Sprintf("Content types (%s)", m.selectedHarness)) + "\n\n" +
			common.DimStyle.Render("No content found for this harness.")
	}

	col1W := max(m.width*45/100, 30)
	col2W := max(m.width-col1W-6, 20)

	// Left: vector checklist.
	left := lipgloss.NewStyle().Width(col1W)
	var lb strings.Builder
	lb.WriteString(common.SelectedStyle.Render(fmt.Sprintf("Content types (%s)", m.selectedHarness)) + "\n\n")
	for i, v := range m.availableVectors {
		cursor := "  "
		if i == m.vectorCursor {
			cursor = common.SelectedStyle.Render("> ")
		}
		check := "[ ]"
		if m.vectorSelected[v] {
			check = "[x]"
		}
		label := v.Label()
		if i == m.vectorCursor {
			label = common.SelectedStyle.Render(label)
		}
		fmt.Fprintf(&lb, "%s%s %s\n", cursor, check, label)
	}

	// Right: detail for highlighted vector.
	right := lipgloss.NewStyle().Width(col2W)
	var rb strings.Builder
	rb.WriteString(common.SelectedStyle.Render("Detail") + "\n\n")
	if m.vectorCursor < len(m.availableVectors) {
		v := m.availableVectors[m.vectorCursor]
		rb.WriteString(common.SelectedStyle.Render(v.Label()) + "\n\n")
		rb.WriteString(categoryDescription(v) + "\n")
	}
	selCount := 0
	for _, v := range m.availableVectors {
		if m.vectorSelected[v] {
			selCount++
		}
	}
	rb.WriteString("\n" + common.DimStyle.Render("─── Summary ───") + "\n")
	fmt.Fprintf(&rb, "%d of %d selected for discovery\n", selCount, len(m.availableVectors))

	sep := common.DimStyle.Render(" │ ")
	return m.viewBreadcrumb() + lipgloss.JoinHorizontal(lipgloss.Top,
		left.Render(lb.String()), sep, right.Render(rb.String()))
}

func (m Model) viewFiles() string {
	col1W := max(m.width*45/100, 35)
	col2W := max(m.width-col1W-6, 20)

	left := m.viewFileList(col1W)
	sep := common.DimStyle.Render(" │ ")
	right := m.viewFileDetail(col2W)

	return m.viewBreadcrumb() + lipgloss.JoinHorizontal(lipgloss.Top, left, sep, right)
}

func (m Model) viewFileList(width int) string {
	if m.fileListLines == nil {
		m = m.withFileList()
	}
	style := lipgloss.NewStyle().Width(width)
	var sb strings.Builder

	selected := m.selectedCount()
	sb.WriteString(common.SelectedStyle.Render(fmt.Sprintf("Files (%d/%d selected)", selected, len(m.candidates))) + "\n")

	// Compute available lines for file list (reserve header and group headers).
	maxLines := max(m.height-6, 5)

	lines := m.fileListLines
	offset := common.ClampOffset(m.fileListCursorLine, m.fileOffset, maxLines)
	start := min(offset, len(lines))
	end := min(start+maxLines, len(lines))
	for _, line := range lines[start:end] {
		sb.WriteString(line.text + "\n")
	}
	if end < len(lines) {
		sb.WriteString(common.DimStyle.Render(fmt.Sprintf("  ... %d more", len(lines)-end)) + "\n")
	}

	return style.Render(sb.String())
}

// withFileList recomputes the file list cache. Call after any mutation that
// affects the rendered list — not from View readers.
func (m Model) withFileList() Model {
	if m.stage != stageFiles {
		m.fileListLines = nil
		m.fileListCursorLine = 0
		return m
	}
	m.fileListLines, m.fileListCursorLine = m.computeFileListLines()
	return m
}

func (m *Model) clampFileOffset() {
	*m = m.withFileList()
	m.fileOffset = common.ClampOffset(m.fileListCursorLine, m.fileOffset, m.visibleFileListHeight())
}

func (m Model) visibleFileListHeight() int {
	maxLines := m.height - 6
	if maxLines < 5 {
		return 5
	}
	return maxLines
}

func (m Model) computeFileListLines() ([]fileListLine, int) {
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

	lines := make([]fileListLine, 0, len(m.candidates)+len(groups)*2)
	cursorLine := 0
	visualPos := 0
	for _, g := range groups {
		count := m.stateCounts[g.state]
		if count == 0 {
			continue
		}
		lines = append(lines,
			fileListLine{text: "", fileCursor: -1},
			fileListLine{text: common.DimStyle.Render(fmt.Sprintf(" %s (%d)", g.label, count)), fileCursor: -1},
		)

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
				cursor = common.SelectedStyle.Render("> ")
			}

			check := "[ ]"
			if c.Selected {
				check = "[x]"
			}

			icon := fileStateIcon(c.State)
			name := CandidateLabel(c.HarnessFile)
			nameStyle := lipgloss.NewStyle()
			if isCurrent {
				nameStyle = common.SelectedStyle
			}

			scopeTag := ""
			if c.Scope != "" {
				scopeTag = "  " + common.DimStyle.Render("["+string(c.Scope)+"]")
			}
			packLabel := ""
			if c.PackName != "" {
				packLabel = "  " + common.DimStyle.Render(c.PackName)
			}
			pathLabel := ""
			if c.Category == domain.CategoryMCP {
				pathLabel = "  " + common.DimStyle.Render(tuiutil.ShortPath(c.HarnessPath))
			}

			lines = append(lines, fileListLine{
				text:       fmt.Sprintf("%s%s %s %s%s%s%s", cursor, check, icon, nameStyle.Render(name), scopeTag, pathLabel, packLabel),
				fileCursor: visualPos,
			})
			visualPos++
		}
	}

	return lines, cursorLine
}

func (m Model) viewFileDetail(width int) string {
	style := lipgloss.NewStyle().Width(width)
	var sb strings.Builder

	sb.WriteString(common.SelectedStyle.Render("Detail") + "\n\n")

	if m.fileCursor < 0 || m.fileCursor >= len(m.sortedIndices) {
		sb.WriteString(common.DimStyle.Render("(no file selected)"))
		return style.Render(sb.String())
	}

	c := m.candidates[m.sortedIndices[m.fileCursor]]
	name := CandidateLabel(c.HarnessFile)
	fmt.Fprintf(&sb, "%s %s\n\n", fileStateIcon(c.State), name)
	fmt.Fprintf(&sb, "State:    %s\n", fileStateLabel(c.State))
	fmt.Fprintf(&sb, "Scope:    %s\n", scopeLabel(c.Scope))
	fmt.Fprintf(&sb, "Category: %s\n", c.Category.Label())
	if c.PackName != "" {
		fmt.Fprintf(&sb, "Pack:     %s\n", c.PackName)
	} else {
		fmt.Fprintf(&sb, "Pack:     %s\n", common.DimStyle.Render("(none)"))
	}
	fmt.Fprintf(&sb, "Size:     %s\n", common.FormatSize(c.Size))
	pathLabel := "Path"
	if c.Category == domain.CategoryMCP {
		pathLabel = "Config"
	}
	fmt.Fprintf(&sb, "%-9s %s\n", pathLabel+":", common.DimStyle.Render(tuiutil.ShortPath(c.HarnessPath)))
	fmt.Fprintf(&sb, "Selected: %v\n", c.Selected)

	// Summary.
	sb.WriteString("\n")
	sb.WriteString(common.DimStyle.Render("─── Summary ───") + "\n")
	counts := m.stateCounts
	fmt.Fprintf(&sb, "Total: %d  Selected: %d\n", len(m.candidates), m.selCount)
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
			fmt.Fprintf(&sb, "  %s %s: %d\n", fileStateIcon(pair.s), pair.l, n)
		}
	}

	return style.Render(sb.String())
}

func (m Model) viewDestPack() string {
	var sb strings.Builder
	sb.WriteString(m.viewBreadcrumb())
	sb.WriteString(common.SelectedStyle.Render(fmt.Sprintf("Destination pack (%d files)", m.selectedCount())) + "\n\n")

	if m.newPackInput {
		sb.WriteString("  Pack name: " + common.SelectedStyle.Render(m.newPackName+"_") + "\n")
		return sb.String()
	}

	for i, name := range m.packOptions {
		cursor := "  "
		if i == m.packCursor {
			cursor = common.SelectedStyle.Render("> ")
		}
		label := name
		if i == m.packCursor {
			label = common.SelectedStyle.Render(label)
		}
		fmt.Fprintf(&sb, "%s%s\n", cursor, label)
	}
	return sb.String()
}

func (m Model) viewResult() string {
	var sb strings.Builder
	sb.WriteString(common.SelectedStyle.Render("Save Complete") + "\n\n")

	if m.resultErr != "" {
		sb.WriteString(common.ErrorStyle.Render("Error: "+m.resultErr) + "\n")
		return sb.String()
	}
	if m.result == nil {
		return sb.String()
	}

	r := m.result
	if r.PackCreated {
		fmt.Fprintf(&sb, "Created new pack: %s\n\n", common.SelectedStyle.Render(m.destPackName))
	} else {
		fmt.Fprintf(&sb, "Saved to pack: %s\n\n", common.SelectedStyle.Render(m.destPackName))
	}

	if len(r.SavedFiles) > 0 {
		fmt.Fprintf(&sb, "Saved %d file(s):\n", len(r.SavedFiles))
		for _, f := range r.SavedFiles {
			fmt.Fprintf(&sb, "  %s → %s\n", common.DimStyle.Render(filepath.Base(f.HarnessPath)), common.DimStyle.Render(tuiutil.ShortPath(f.PackPath)))
		}
	}
	if len(r.Conflicts) > 0 {
		fmt.Fprintf(&sb, "\n%s %d conflict(s) skipped (use --force to override):\n",
			fileStateIcon(app.FileConflict), len(r.Conflicts))
		for _, c := range r.Conflicts {
			fmt.Fprintf(&sb, "  %s\n", filepath.Base(c.HarnessPath))
		}
	}
	if len(r.SecretFindings) > 0 {
		fmt.Fprintf(&sb, "\n%s Secret findings:\n", common.ErrorStyle.Render("!"))
		for _, f := range r.SecretFindings {
			fmt.Fprintf(&sb, "  %s\n", f)
		}
	}

	sb.WriteString(common.DimStyle.Render("\nPress enter or esc to continue."))
	return sb.String()
}

func (m Model) updateDiffView(msg tea.Msg) (Model, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = msg.Width
		m.height = msg.Height
		if m.diffView.ready {
			m.diffView.viewport.SetWidth(msg.Width - 4)
			m.diffView.viewport.SetHeight(msg.Height - 4)
		}
		return m, nil
	}
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		if msg.String() == "esc" || msg.String() == "q" {
			m.diffView = nil
			return m, nil
		}
	}
	dv, cmd := m.diffView.Update(msg)
	m.diffView = &dv
	return m, cmd
}

func (m Model) loadCandidateDiff(c app.SaveCandidate) tea.Cmd {
	return func() tea.Msg {
		title := CandidateLabel(c.HarnessFile)
		if c.Kind == domain.CopyKindDir {
			return DiffLoadedMsg{
				Dst:   c.HarnessPath,
				Title: title,
				Err:   fmt.Errorf("diff unavailable for directories"),
			}
		}

		desired := c.Content
		if desired == nil {
			content, err := os.ReadFile(c.HarnessPath)
			if err != nil {
				return DiffLoadedMsg{Dst: c.HarnessPath, Title: title, Err: err}
			}
			desired = content
		}

		if c.PackPath == "" {
			return DiffLoadedMsg{
				Dst:     c.HarnessPath,
				Title:   title,
				IsNew:   true,
				NewBody: string(desired),
			}
		}

		onDisk, err := os.ReadFile(c.PackPath)
		if err != nil {
			if os.IsNotExist(err) {
				return DiffLoadedMsg{
					Dst:     c.PackPath,
					Title:   title,
					IsNew:   true,
					NewBody: string(desired),
				}
			}
			return DiffLoadedMsg{Dst: c.PackPath, Title: title, Err: err}
		}

		labelA := filepath.Base(c.PackPath) + " (in pack)"
		labelB := title + " (in harness)"
		diffText := engine.UnifiedDiff(onDisk, desired, labelA, labelB)
		return DiffLoadedMsg{Dst: c.PackPath, Title: title, DiffText: diffText}
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

func CandidateLabel(f app.HarnessFile) string {
	if f.RelPath != "" {
		return f.RelPath
	}
	return filepath.Base(f.HarnessPath)
}

// rediscoverFiles re-runs file discovery with the current harness/vector/targeting state.
func (m Model) rediscoverFiles() tea.Cmd {
	var categories []domain.PackCategory
	for _, v := range m.availableVectors {
		if m.vectorSelected[v] {
			categories = append(categories, v)
		}
	}
	if len(categories) == 0 || m.targetSpec == nil {
		return nil
	}
	return DiscoverFiles(m.ctx, m.eng, app.DiscoverSaveRequest{
		HarnessID:  m.selectedHarness,
		Categories: categories,
		Scope:      m.targetSpec.Scope,
		ProjectDir: m.targetSpec.ProjectDir,
		Home:       m.targetSpec.Home,
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
	case domain.CategoryHooks:
		return "Executable lifecycle handlers rendered into\nnative harness hook configuration."
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
		return lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("unknown")
	}
}
