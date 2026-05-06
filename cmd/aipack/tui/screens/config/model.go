package configscreen

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ltable "charm.land/lipgloss/v2/table"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

type section int

const (
	sectionSyncDefaults section = iota
	sectionParams
	sectionEnv
)

type pane int

const (
	paneSections pane = iota
	paneContent
)

type Profile struct {
	Name   string
	Path   string
	Config config.ProfileConfig
}

type ProfileSelectMsg struct{}

type HarnessSelectMsg struct{}

type CycleScopeMsg struct{}
type CycleCollisionMsg struct{}

type EditParamMsg struct {
	Key string
}

type EditEnvMsg struct {
	Key string
}

type EnvSelection struct {
	Key        string
	FromDotEnv bool
}

type ParamSelection struct {
	Key         string
	FromProfile bool
}

type Model struct {
	configDir string

	profile Profile
	syncCfg config.SyncConfig
	refs    []app.ProfileRef
	env     []app.EnvEntry

	// Cached views over refs. paramRows/envRows used to walk refs O(N) per
	// row × N_rows × 2-3 rebuilds per Update cycle. Refresh only when
	// refs/env/profile/syncCfg mutate; lookup by name is O(1).
	refsByName      map[string][]app.ProfileRef
	cachedParamRows [][]string
	cachedEnvRows   [][]string
	cachedSyncRows  [][]string
	rowsCacheBuilt  bool

	section section
	focus   pane
	cursor  int
	width   int
	height  int
}

func New(configDir string) Model {
	return Model{
		configDir: configDir,
		width:     100,
		height:    24,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) SetSize(width, height int) common.Screen {
	m.width = width
	m.height = height
	return m
}

func (m Model) SetContext(profile Profile, syncCfg config.SyncConfig) Model {
	m.profile = profile
	m.syncCfg = syncCfg
	m.buildRowsCache()
	m.clampCursor()
	return m
}

func (m Model) SetRefs(refs []app.ProfileRef) Model {
	m.refs = slices.Clone(refs)
	m.buildRowsCache()
	m.clampCursor()
	return m
}

func (m Model) SetEnvEntries(entries []app.EnvEntry) Model {
	m.env = slices.Clone(entries)
	m.buildRowsCache()
	m.clampCursor()
	return m
}

func (m Model) Update(msg tea.Msg) (common.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case common.FocusMsg, common.BlurMsg:
		return m, nil
	case common.LayerHitMsg:
		if !strings.HasPrefix(msg.ID, "config:") {
			return m, nil
		}
		return m.handleLayerHit(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (common.Screen, tea.Cmd) {
	if m.focus == paneSections {
		switch msg.String() {
		case "j", "down":
			m.section++
			m.clampSection()
			m.cursor = 0
			m.clampCursor()
		case "k", "up":
			m.section--
			m.clampSection()
			m.cursor = 0
			m.clampCursor()
		case "l", "right", "enter", "space":
			m.focus = paneContent
			m.cursor = 0
			m.clampCursor()
		}
		return m, nil
	}

	switch msg.String() {
	case "j", "down":
		m.cursor++
		m.clampCursor()
	case "k", "up":
		m.cursor--
		m.clampCursor()
	case "h", "left", "esc":
		m.focus = paneSections
	case "enter", "space":
		return m.activateCurrent()
	}
	return m, nil
}

func (m Model) handleLayerHit(msg common.LayerHitMsg) (common.Screen, tea.Cmd) {
	if !common.IsLeftClick(msg.Mouse) {
		return m, nil
	}

	switch {
	case msg.ID == "config:section:params":
		m.section = sectionParams
		m.focus = paneSections
		m.cursor = 0
	case msg.ID == "config:section:env":
		m.section = sectionEnv
		m.focus = paneSections
		m.cursor = 0
	case msg.ID == "config:section:sync-defaults":
		m.section = sectionSyncDefaults
		m.focus = paneSections
		m.cursor = 0
	case strings.HasPrefix(msg.ID, "config:param:"):
		key := strings.TrimPrefix(msg.ID, "config:param:")
		m.section = sectionParams
		m.focus = paneContent
		if idx := indexRow(m.paramRows(), key); idx >= 0 {
			m.cursor = idx
		}
		return m, func() tea.Msg { return EditParamMsg{Key: key} }
	case strings.HasPrefix(msg.ID, "config:env:"):
		key := strings.TrimPrefix(msg.ID, "config:env:")
		m.section = sectionEnv
		m.focus = paneContent
		if idx := indexRow(m.envRows(), key); idx >= 0 {
			m.cursor = idx
		}
		return m, func() tea.Msg { return EditEnvMsg{Key: key} }
	case msg.ID == "config:default-profile":
		m.section = sectionSyncDefaults
		m.focus = paneContent
		m.cursor = 0
		return m, func() tea.Msg { return ProfileSelectMsg{} }
	case msg.ID == "config:scope":
		m.section = sectionSyncDefaults
		m.focus = paneContent
		m.cursor = 2
		return m, func() tea.Msg { return CycleScopeMsg{} }
	case msg.ID == "config:collision":
		m.section = sectionSyncDefaults
		m.focus = paneContent
		m.cursor = 3
		return m, func() tea.Msg { return CycleCollisionMsg{} }
	case msg.ID == "config:harnesses":
		m.section = sectionSyncDefaults
		m.focus = paneContent
		m.cursor = 1
		return m, func() tea.Msg { return HarnessSelectMsg{} }
	}
	return m, nil
}

func indexRow(rows [][]string, key string) int {
	for i, row := range rows {
		if len(row) > 0 && row[0] == key {
			return i
		}
	}
	return -1
}

func (m Model) activateCurrent() (common.Screen, tea.Cmd) {
	if m.focus == paneSections {
		m.focus = paneContent
		m.cursor = 0
		m.clampCursor()
		return m, nil
	}

	switch m.section {
	case sectionParams:
		rows := m.paramRows()
		if m.cursor >= 0 && m.cursor < len(rows) {
			key := rows[m.cursor][0]
			return m, func() tea.Msg { return EditParamMsg{Key: key} }
		}
	case sectionEnv:
		rows := m.envRows()
		if m.cursor >= 0 && m.cursor < len(rows) {
			key := rows[m.cursor][0]
			return m, func() tea.Msg { return EditEnvMsg{Key: key} }
		}
	case sectionSyncDefaults:
		switch m.cursor {
		case 0:
			return m, func() tea.Msg { return ProfileSelectMsg{} }
		case 1:
			return m, func() tea.Msg { return HarnessSelectMsg{} }
		case 2:
			return m, func() tea.Msg { return CycleScopeMsg{} }
		case 3:
			return m, func() tea.Msg { return CycleCollisionMsg{} }
		}
	}
	return m, nil
}

func (m Model) IsContentFocused() bool {
	return m.focus == paneContent
}

func (m Model) SectionName() string {
	return m.section.String()
}

func (m Model) CurrentParamKey() (string, bool) {
	if m.section != sectionParams {
		return "", false
	}
	rows := m.paramRows()
	if m.cursor < 0 || m.cursor >= len(rows) || len(rows[m.cursor]) == 0 {
		return "", false
	}
	return rows[m.cursor][0], true
}

func (m Model) CurrentParamSelection() (ParamSelection, bool) {
	key, ok := m.CurrentParamKey()
	if !ok {
		return ParamSelection{}, false
	}
	_, fromProfile := m.profile.Config.Params[key]
	return ParamSelection{
		Key:         key,
		FromProfile: fromProfile,
	}, true
}

func (m Model) CurrentEnvSelection() (EnvSelection, bool) {
	if m.section != sectionEnv {
		return EnvSelection{}, false
	}
	rows := m.envRows()
	if m.cursor < 0 || m.cursor >= len(rows) || len(rows[m.cursor]) < 3 {
		return EnvSelection{}, false
	}
	return EnvSelection{
		Key:        rows[m.cursor][0],
		FromDotEnv: rows[m.cursor][2] == ".env",
	}, true
}

func (m Model) SelectParam(key string) (Model, bool) {
	m.section = sectionParams
	m.focus = paneContent
	if idx := indexRow(m.paramRows(), key); idx >= 0 {
		m.cursor = idx
		return m, true
	}
	m.clampCursor()
	return m, false
}

func (m Model) SelectEnv(key string) (Model, bool) {
	m.section = sectionEnv
	m.focus = paneContent
	if idx := indexRow(m.envRows(), key); idx >= 0 {
		m.cursor = idx
		return m, true
	}
	m.clampCursor()
	return m, false
}

func (m Model) HelpText() string {
	if m.focus == paneContent {
		return "j/k:row  enter:edit  .:actions  left/esc:sections"
	}
	return "j/k:section  enter/right:content  .:actions"
}

func (m *Model) clampSection() {
	m.section = section(min(max(int(m.section), 0), sectionCount()-1))
}

func (m *Model) clampCursor() {
	m.clampSection()
	maxCursor := m.rowCount() - 1
	if maxCursor < 0 {
		m.cursor = 0
		return
	}
	m.cursor = min(max(m.cursor, 0), maxCursor)
}

func sectionCount() int {
	return 3
}

func (m Model) rowCount() int {
	switch m.section {
	case sectionParams:
		return len(m.paramRows())
	case sectionEnv:
		return len(m.envRows())
	case sectionSyncDefaults:
		return len(m.syncDefaultRows())
	default:
		return 0
	}
}

func (m Model) View() *lipgloss.Layer {
	rows := m.activeRows()
	root := lipgloss.NewLayer(m.render(rows))
	root.AddLayers(m.clickLayers(rows)...)
	return root
}

func (m Model) render(rows [][]string) string {
	screenWidth := max(m.width-4, 60)
	leftW := 18
	rightW := max(screenWidth-leftW-4, 40)

	header := common.SelectedStyle.Render("Config: " + m.profileName())
	if m.profile.Path != "" {
		header += "  " + common.DimStyle.Render(shortProfilePath(m.profile.Path))
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(leftW).Render(m.renderSections()),
		lipgloss.NewStyle().Width(2).Render(""),
		lipgloss.NewStyle().Width(rightW).Render(m.renderActiveSection(rightW, rows)),
	)

	status := m.statusLine(rows)
	content := lipgloss.JoinVertical(lipgloss.Left, header, common.DimStyle.Render(strings.Repeat("─", min(screenWidth, 96))), body, "", status)
	return common.ContentStyle.Render(content)
}

func (m Model) profileName() string {
	if strings.TrimSpace(m.profile.Name) != "" {
		return m.profile.Name
	}
	if strings.TrimSpace(m.syncCfg.Defaults.Profile) != "" {
		return m.syncCfg.Defaults.Profile
	}
	return "default"
}

func shortProfilePath(path string) string {
	if path == "" {
		return ""
	}
	path = strings.ReplaceAll(path, "\\", "/")
	const marker = "/profiles/"
	if idx := strings.LastIndex(path, marker); idx >= 0 {
		return "profiles/" + path[idx+len(marker):]
	}
	return path
}

func (m Model) renderSections() string {
	rows := []struct {
		section section
		label   string
	}{
		{sectionSyncDefaults, "Sync Defaults"},
		{sectionParams, "Params"},
		{sectionEnv, "Env"},
	}
	var sb strings.Builder
	sb.WriteString(common.SelectedStyle.Render("Sections") + "\n")
	for _, row := range rows {
		prefix := "  "
		style := common.DimStyle
		if row.section == m.section {
			style = common.SelectedStyle
			if m.focus == paneSections {
				prefix = common.SelectedStyle.Render("> ")
			}
		}
		sb.WriteString(prefix + style.Render(row.label) + "\n")
	}
	return sb.String()
}

func (m Model) renderActiveSection(width int, rows [][]string) string {
	switch m.section {
	case sectionEnv:
		return m.renderTable("Env", []string{"KEY", "VALUE", "AVAILABLE FROM", "USED BY"}, rows, width, m.focus == paneContent)
	case sectionSyncDefaults:
		return m.renderTable("Sync Defaults", []string{"SETTING", "VALUE", "STORED IN"}, rows, width, m.focus == paneContent)
	default:
		return m.renderTable("Params", []string{"KEY", "VALUE", "STATUS", "USED BY"}, rows, width, m.focus == paneContent)
	}
}

func (m Model) renderTable(label string, headers []string, rows [][]string, width int, active bool) string {
	t := ltable.New().
		Headers(headers...).
		Rows(rows...).
		Border(lipgloss.NormalBorder()).
		BorderStyle(common.DimStyle).
		StyleFunc(func(row, col int) lipgloss.Style {
			style := lipgloss.NewStyle().Padding(0, 1)
			if row == ltable.HeaderRow {
				return style.Bold(true).Foreground(lipgloss.Color("81"))
			}
			if active && row == m.cursor {
				return style.Inherit(common.SelectedStyle)
			}
			if col >= len(headers)-1 {
				return style.Inherit(common.DimStyle)
			}
			return style
		}).
		Width(max(width-2, 20))
	if len(rows) == 0 {
		return common.SelectedStyle.Render(label) + "\n" + common.DimStyle.Render("(none)")
	}
	return common.SelectedStyle.Render(label) + "\n" + t.String()
}

func (m Model) statusLine(rows [][]string) string {
	if m.focus == paneSections {
		return common.DimStyle.Render("Choose a section, then press right or enter")
	}

	switch m.section {
	case sectionParams:
		if m.cursor >= 0 && m.cursor < len(rows) {
			return common.DimStyle.Render(rows[m.cursor][0] + " stored in profiles/" + m.profileName() + ".yaml")
		}
	case sectionEnv:
		if m.cursor >= 0 && m.cursor < len(rows) {
			return common.DimStyle.Render(rows[m.cursor][0] + " resolved from " + strings.ToLower(rows[m.cursor][2]))
		}
	case sectionSyncDefaults:
		if m.cursor >= 0 && m.cursor < len(rows) {
			return common.DimStyle.Render(rows[m.cursor][0] + " stored in " + rows[m.cursor][2])
		}
	}
	return ""
}

func (m Model) activeRows() [][]string {
	switch m.section {
	case sectionParams:
		return m.paramRows()
	case sectionEnv:
		return m.envRows()
	case sectionSyncDefaults:
		return m.syncDefaultRows()
	default:
		return nil
	}
}

func (m Model) paramRows() [][]string {
	m = m.withRowsCache()
	return m.cachedParamRows
}

func (m Model) envRows() [][]string {
	m = m.withRowsCache()
	return m.cachedEnvRows
}

func (m Model) withRowsCache() Model {
	if !m.rowsCacheBuilt {
		m.buildRowsCache()
	}
	return m
}

// buildRowsCache populates refsByName and the three cached row slices.
func (m *Model) buildRowsCache() {
	m.refsByName = make(map[string][]app.ProfileRef, len(m.refs))
	for _, ref := range m.refs {
		m.refsByName[ref.Name] = append(m.refsByName[ref.Name], ref)
	}

	m.cachedParamRows = m.computeParamRows()
	m.cachedEnvRows = m.computeEnvRows()
	m.cachedSyncRows = m.computeSyncDefaultRows()
	m.rowsCacheBuilt = true
}

func (m Model) computeParamRows() [][]string {
	keys := make(map[string]struct{}, len(m.profile.Config.Params))
	for key := range m.profile.Config.Params {
		keys[key] = struct{}{}
	}
	for _, ref := range m.refs {
		if ref.Kind == domain.RefKindParam {
			keys[ref.Name] = struct{}{}
		}
	}
	names := slices.Sorted(maps.Keys(keys))

	rows := make([][]string, 0, len(names))
	for _, name := range names {
		value, hasValue := m.profile.Config.Params[name]
		status := "set"
		if !hasValue {
			status = "missing"
			value = "missing"
		}
		if ref, ok := m.firstRefForKind(name, domain.RefKindParam); ok && !hasValue {
			status = ref.Status
			if status == "defaulted" {
				value = defaultFromDisplay(ref.Display)
			}
			if status == "missing" && value == "" {
				value = "missing"
			}
		}
		rows = append(rows, []string{name, value, status, m.usedByForKind(name, domain.RefKindParam)})
	}
	return rows
}

func (m Model) computeEnvRows() [][]string {
	seen := make(map[string]struct{}, len(m.env))
	for _, entry := range m.env {
		if entry.Key != "" {
			seen[entry.Key] = struct{}{}
		}
	}
	for _, ref := range m.refs {
		if ref.Kind == domain.RefKindEnv {
			seen[ref.Name] = struct{}{}
		}
	}
	names := slices.Sorted(maps.Keys(seen))

	rows := make([][]string, 0, len(names))
	for _, name := range names {
		ref, _ := m.firstRefForKind(name, domain.RefKindEnv)
		status := ref.Status
		if status == "" && envEntryPresent(m.env, name) {
			status = "dotenv"
		}
		rows = append(rows, []string{name, envValue(status), envAvailableFrom(status), m.usedByForKind(name, domain.RefKindEnv)})
	}
	return rows
}

// firstRefForKind returns the first ref of the given kind matching name. O(k)
// where k is the number of refs sharing this name (typically 1-3); refsByName
// pre-filters from the global refs slice.
func (m Model) firstRefForKind(name, kind string) (app.ProfileRef, bool) {
	for _, ref := range m.refsByName[name] {
		if ref.Kind == kind {
			return ref, true
		}
	}
	return app.ProfileRef{}, false
}

func (m Model) usedByForKind(name, kind string) string {
	seen := map[string]struct{}{}
	var parts []string
	for _, ref := range m.refsByName[name] {
		if ref.Kind != kind {
			continue
		}
		label := ref.Pack
		if label == "" {
			label = ref.Target
		}
		if label == "" {
			label = ref.Location
		}
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		parts = append(parts, label)
	}
	slices.Sort(parts)
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func envEntryPresent(entries []app.EnvEntry, key string) bool {
	for _, entry := range entries {
		if entry.Key == key {
			return true
		}
	}
	return false
}

func (m Model) syncDefaultRows() [][]string {
	m = m.withRowsCache()
	return m.cachedSyncRows
}

func (m Model) computeSyncDefaultRows() [][]string {
	profile := m.syncCfg.Defaults.Profile
	if profile == "" {
		profile = "default"
	}
	harnesses := strings.Join(m.syncCfg.Defaults.Harnesses, ", ")
	if harnesses == "" {
		harnesses = "(default harness)"
	}
	scope := m.syncCfg.Defaults.Scope
	if scope == "" {
		scope = string(domain.ScopeGlobal)
	}
	collision := string(m.syncCfg.Defaults.CollisionStrategy)
	if collision == "" {
		collision = string(config.CollisionLastWins)
	}
	return [][]string{
		{"Default profile", profile, "sync-config.yaml"},
		{"Harnesses", harnesses, "sync-config.yaml"},
		{"Write target", scope, "sync-config.yaml"},
		{"Collisions", collision, "sync-config.yaml"},
	}
}

func defaultFromDisplay(display string) string {
	_, after, ok := strings.Cut(display, ":-")
	if !ok {
		return "default"
	}
	value := strings.TrimSuffix(after, "}")
	if value == "" {
		return "default"
	}
	return value
}

func envValue(status string) string {
	if status == "missing" {
		return "missing"
	}
	return "********"
}

func envAvailableFrom(status string) string {
	switch status {
	case "dotenv":
		return ".env"
	case "env":
		return "shell"
	case "missing":
		return "missing"
	default:
		return status
	}
}

func (m Model) clickLayers(rows [][]string) []*lipgloss.Layer {
	top, _, _, left := common.ContentStyle.GetPadding()
	layers := []*lipgloss.Layer{
		lipgloss.NewLayer(hitRow(16)).ID("config:section:sync-defaults").X(left).Y(top + 3).Z(-1),
		lipgloss.NewLayer(hitRow(16)).ID("config:section:params").X(left).Y(top + 4).Z(-1),
		lipgloss.NewLayer(hitRow(16)).ID("config:section:env").X(left).Y(top + 5).Z(-1),
	}

	tableX := left + 20
	rowY := top + 6
	switch m.section {
	case sectionParams:
		for i, row := range rows {
			layers = append(layers, lipgloss.NewLayer(hitRow(max(m.width-tableX-4, 20))).ID("config:param:"+row[0]).X(tableX).Y(rowY+i).Z(-1))
		}
	case sectionEnv:
		for i, row := range rows {
			layers = append(layers, lipgloss.NewLayer(hitRow(max(m.width-tableX-4, 20))).ID("config:env:"+row[0]).X(tableX).Y(rowY+i).Z(-1))
		}
	case sectionSyncDefaults:
		layers = append(layers,
			lipgloss.NewLayer(hitRow(max(m.width-tableX-4, 20))).ID("config:default-profile").X(tableX).Y(rowY).Z(-1),
			lipgloss.NewLayer(hitRow(max(m.width-tableX-4, 20))).ID("config:harnesses").X(tableX).Y(rowY+1).Z(-1),
			lipgloss.NewLayer(hitRow(max(m.width-tableX-4, 20))).ID("config:scope").X(tableX).Y(rowY+2).Z(-1),
			lipgloss.NewLayer(hitRow(max(m.width-tableX-4, 20))).ID("config:collision").X(tableX).Y(rowY+3).Z(-1),
		)
	}
	return layers
}

func hitRow(width int) string {
	return strings.Repeat(" ", max(width, 1))
}

func (s section) String() string {
	switch s {
	case sectionParams:
		return "params"
	case sectionEnv:
		return "env"
	case sectionSyncDefaults:
		return "sync-defaults"
	default:
		return fmt.Sprintf("section(%d)", s)
	}
}
