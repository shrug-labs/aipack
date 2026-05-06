package picker

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/mcp"
)

var (
	clrLoading        = lipgloss.Color("226")
	dialogBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("205")).
				Padding(1, 2)
	dialogTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	treeCheckOff     = "[ ]"
	treeCheckOn      = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("[x]")
	triCheckAuto     = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("[■]")
)

// TriState is the per-item State for the MCP tool picker.
//   - TriOff: tool not in any allow list (disabled_tools computed at save time)
//   - TriAsk: tool in allowed_tools (harness prompts for approval)
//   - TriAuto: tool in always_allowed_tools (auto-approved)
type TriState int

const (
	TriOff TriState = iota
	TriAsk
	TriAuto
)

// Item is one row in the MCP tool picker.
type Item struct {
	Label string
	State TriState
}

type ResultMsg struct {
	ID        string
	Confirmed bool
	Ask       []string
	Auto      []string
	Bulk      bool
}

type RefreshMsg struct {
	ID string
}

// Model is a sub-model for the MCP tool picker. It does not share State
// with dialogModel: the picker has its own async probe lifecycle, scroll
// viewport, key shortcuts, and result message shape, and dialogModel was
// never meant to carry that much State per kind.
type Model struct {
	ID     string
	Title  string
	Items  []Item
	Cursor int

	// ProfileTotal is the profile-wide effective MCP tool count excluding
	// the server currently being edited. Added to the picker's live
	// per-tool-State sum to render the "profile: N / limit" footer segment
	// so users can see how the cross-harness limit interacts with their
	// edits in real time.
	ProfileTotal     int
	ProfileAvailable int

	// VisibleH is the max number of tool rows shown at once. Zero means
	// "show all" (small lists). When the server has more tools, the list
	// scrolls and the footer stays pinned.
	VisibleH  int
	ScrollOff int

	// Loading is true while an async MCP probe is in flight. The picker
	// shows a Spinner and only responds to esc. When the probe completes,
	// Loading is cleared and Items are populated with live tools (or the
	// static fallback on error).
	Loading bool
	// Phase tracks the most recent mcp.probe.* event type during Loading
	// so the Loading view can show "starting", "listing tools", etc.
	// instead of a generic "Probing server..." message.
	Phase mcp.ProbePhase
	// PhaseDetail carries the Detail field from the most recent probe
	// event (e.g. transport Label "stdio" / "streamable-http" for
	// mcp.probe.starting). Rendered next to the Phase Label.
	PhaseDetail string
	// Spinner is the animated Loading indicator. Ticks only while Loading
	// is true; the rootModel routes Spinner.TickMsg to its Update.
	Spinner spinner.Model
	// LoadingErr is set when the probe fails. Displayed as a dim note
	// above the static-fallback tool list so users know the inventory may
	// be stale.
	LoadingErr string
	// ServerDisabled indicates the MCP server is disabled in the current
	// profile. Shown as a banner so users know their tool-list edits will
	// persist but be inert until the server is re-enabled.
	ServerDisabled bool

	// ProbedAt is the time the Items were (last) probed. Zero means the
	// Items came from the static pack inventory and no probe has run in
	// this session. Rendered as a relative-time hint ("probed 2h ago") so
	// users can decide whether to `r`-refresh.
	ProbedAt time.Time
}

// MaxVisible returns the number of tool rows that fit alongside the
// picker's fixed chrome within a terminal of the given total height.
//
// The picker content is wrapped by tui.go before render:
//
//   - tabBar + statusLine + help bar → 3 rows subtracted from m.height
//   - contentStyle.Padding(1, 2) → 2 rows (1 top, 1 bottom)
//   - explicit `"\n" + View() + "\n"` pad → 2 rows
//
// Inside picker.View():
//
//   - dialogBorderStyle → 2 border rows
//   - Title + blank → 2
//   - legend + blank → 2
//   - blank + counts footer → 2
//   - freshness hint (+ blank) → up to 2
//   - server-disabled banner (+ blank) → up to 2
//   - "↑ N more" / "↓ N more" indicators → up to 2
//
// Worst-case fixed overhead (with all banners + both indicators) is 21 rows,
// typical is 17. Reserving 20 keeps the counts footer visible across common
// states. The minimum of 3 prevents total hiding on small terminals.
//
// When the terminal is too small to show a usable picker, the counts footer
// still takes priority — losing a row of item visibility is preferable to
// losing the "N enabled / N total" tally users rely on for the cross-harness
// tool budget.
func MaxVisible(termHeight int) int {
	return max(termHeight-20, 3)
}

// New creates an MCP tool picker. User-visible states cycle
// off → ask → auto → off on <space>.
func New(ID, Title string, Items []Item) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(clrLoading)
	return Model{
		ID:      ID,
		Title:   Title,
		Items:   Items,
		Spinner: sp,
	}
}

// WithProfileToolTotal attaches the profile-wide tool count (excluding this
// server) so the footer can render "profile: N / limit" against the
// cross-harness MCP tool budget in real time.
func (p Model) WithProfileToolTotal(enabled, available int) Model {
	p.ProfileTotal = enabled
	p.ProfileAvailable = available
	return p
}

// WithServerDisabled marks the picker as editing a server that is disabled
// in the current profile. A banner is rendered so users understand their
// selections persist but only take effect once the server is re-enabled.
func (p Model) WithServerDisabled(disabled bool) Model {
	p.ServerDisabled = disabled
	return p
}

// WithProbedAt attaches the timestamp of the probe that produced the Items.
// Pass a zero value when the Items came from the static inventory (no probe
// yet); the header hides the freshness hint in that case.
func (p Model) WithProbedAt(t time.Time) Model {
	p.ProbedAt = t
	return p
}

func (p Model) Init() tea.Cmd { return nil }

func (p Model) SetSize(_, _ int) common.Screen { return p }

func (p Model) Update(msg tea.Msg) (common.Screen, tea.Cmd) {
	return p.UpdateModel(msg)
}

func (p Model) UpdateModel(msg tea.Msg) (Model, tea.Cmd) {
	if hit, ok := msg.(common.LayerHitMsg); ok {
		return p.handleLayerHit(hit)
	}

	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	key := km.String()

	if p.Loading {
		if key == "esc" {
			return p, sendResult(p.ID, false, p.Items)
		}
		return p, nil
	}
	if len(p.Items) == 0 {
		switch key {
		case "enter", "esc":
			return p, sendResult(p.ID, true, p.Items)
		}
		return p, nil
	}

	cursor := &p.Items[p.Cursor]
	switch key {
	case "space":
		switch cursor.State {
		case TriOff:
			cursor.State = TriAsk
		case TriAsk:
			cursor.State = TriAuto
		case TriAuto:
			cursor.State = TriOff
		}
	case "x", "d":
		cursor.State = TriOff
	case "a":
		cursor.State = TriAsk
	case "A":
		cursor.State = TriAuto
	case "enter", "esc":
		// Save and exit. The picker has no "cancel" — exit intent is always
		// "keep what I see"; escape is a navigation dismiss, not a revert.
		return p, sendResult(p.ID, true, p.Items)
	case ".":
		// Dismiss the picker (saving current State) and chain into the bulk
		// action menu. The root handler sees the bulk flag and reopens the
		// picker after the bulk action completes.
		return p, sendBulkRequest(p.ID, p.Items)
	case "r":
		// Force-refresh: invalidate the cached probe and re-run it. The
		// picker stays open in Loading State; the root handler fires the
		// probe command. Current selections are kept — the re-probe only
		// affects which tools appear as options.
		p.Loading = true
		p.LoadingErr = ""
		return p, sendRefresh(p.ID)
	case "j", "down":
		if p.Cursor < len(p.Items)-1 {
			p.Cursor++
		}
	case "k", "up":
		if p.Cursor > 0 {
			p.Cursor--
		}
	case "pgdown", "ctrl+f":
		step := p.VisibleH
		if step <= 0 {
			step = len(p.Items)
		}
		p.Cursor = min(p.Cursor+step, len(p.Items)-1)
	case "pgup", "ctrl+b":
		step := p.VisibleH
		if step <= 0 {
			step = len(p.Items)
		}
		p.Cursor = max(p.Cursor-step, 0)
	case "g", "home":
		p.Cursor = 0
	case "G", "end":
		p.Cursor = len(p.Items) - 1
	}

	// VisibleH == 0 means all Items fit; no scrolling needed.
	if p.VisibleH > 0 {
		p.ScrollOff = common.ClampOffset(p.Cursor, p.ScrollOff, p.VisibleH)
	}
	return p, nil
}

func (p Model) handleLayerHit(msg common.LayerHitMsg) (Model, tea.Cmd) {
	if !common.IsLeftClick(msg.Mouse) || !strings.HasPrefix(msg.ID, "picker:") {
		return p, nil
	}
	switch msg.ID {
	case "picker:save", "picker:close":
		if p.Loading {
			return p, sendResult(p.ID, false, p.Items)
		}
		return p, sendResult(p.ID, true, p.Items)
	case "picker:bulk":
		if !p.Loading && len(p.Items) > 0 {
			return p, sendBulkRequest(p.ID, p.Items)
		}
	case "picker:refresh":
		if !p.Loading {
			p.Loading = true
			p.LoadingErr = ""
			return p, sendRefresh(p.ID)
		}
	default:
		index, ok := common.ParseLayerIndex(msg.ID, "picker:item:")
		if !ok || p.Loading {
			return p, nil
		}
		if index < 0 || index >= len(p.Items) {
			return p, nil
		}
		p.Cursor = index
		switch p.Items[index].State {
		case TriOff:
			p.Items[index].State = TriAsk
		case TriAsk:
			p.Items[index].State = TriAuto
		case TriAuto:
			p.Items[index].State = TriOff
		}
	}
	return p, nil
}

func (p Model) View() *lipgloss.Layer {
	root := lipgloss.NewLayer(p.Render())
	root.AddLayers(
		lipgloss.NewLayer("[save]").ID("picker:save").X(4).Y(1).Z(-1),
		lipgloss.NewLayer("[refresh]").ID("picker:refresh").X(11).Y(1).Z(-1),
		lipgloss.NewLayer("[bulk]").ID("picker:bulk").X(22).Y(1).Z(-1),
		lipgloss.NewLayer("[close]").ID("picker:close").X(29).Y(1).Z(-1),
	)
	visStart, visEnd := p.visibleRange()
	y := 7
	if p.ServerDisabled {
		y += 2
	}
	if p.LoadingErr != "" {
		y += 3
	}
	if p.freshnessHint() != "" {
		y += 2
	}
	if visStart > 0 {
		y++
	}
	for i := visStart; i < visEnd; i++ {
		root.AddLayers(lipgloss.NewLayer(p.Items[i].Label).ID(fmt.Sprintf("picker:item:%d", i)).X(8).Y(y + i - visStart).Z(-1))
	}
	return root
}

func (p Model) visibleRange() (int, int) {
	visStart, visEnd := 0, len(p.Items)
	if p.VisibleH > 0 && len(p.Items) > p.VisibleH {
		visStart = p.ScrollOff
		visEnd = min(visStart+p.VisibleH, len(p.Items))
	}
	return visStart, visEnd
}

func (p Model) Render() string {
	content := dialogTitleStyle.Render(p.Title) + "\n\n"
	if p.Loading {
		content += "  " + p.Spinner.View() + " " + ProbePhaseLabel(p.Phase, p.PhaseDetail) + "\n"
		content += "\n" + common.DimStyle.Render("  esc:cancel") + "\n"
		return dialogBorderStyle.Width(max(lipgloss.Width(content)+6, 30)).Render(content)
	}
	if p.ServerDisabled {
		content += common.WarningStyle.Render("  ⚠ server disabled — changes take effect when server is enabled") + "\n\n"
	}
	if p.LoadingErr != "" {
		errText := p.LoadingErr
		if len(errText) > 60 {
			errText = errText[:57] + "..."
		}
		content += common.WarningStyle.Render("  ⚠ probe failed — showing pack inventory") + "\n"
		content += common.DimStyle.Render("    "+errText) + "\n\n"
	}
	if hint := p.freshnessHint(); hint != "" {
		content += common.DimStyle.Render("  "+hint) + "\n\n"
	}
	legend := "  " + treeCheckOff + common.DimStyle.Render(" off  ") + treeCheckOn + common.DimStyle.Render(" ask  ") + triCheckAuto + common.DimStyle.Render(" auto")
	content += legend + "\n\n"

	// Render only the visible window when scroll is active.
	visStart, visEnd := 0, len(p.Items)
	if p.VisibleH > 0 && len(p.Items) > p.VisibleH {
		visStart = p.ScrollOff
		visEnd = min(visStart+p.VisibleH, len(p.Items))
		if visStart > 0 {
			content += common.DimStyle.Render(fmt.Sprintf("  ↑ %d more", visStart)) + "\n"
		}
	}
	for i := visStart; i < visEnd; i++ {
		item := p.Items[i]
		prefix := "  "
		if i == p.Cursor {
			prefix = "> "
		}
		marker := treeCheckOff
		switch item.State {
		case TriAsk:
			marker = treeCheckOn
		case TriAuto:
			marker = triCheckAuto
		case TriOff:
			marker = treeCheckOff
		}
		Label := item.Label
		if i == p.Cursor {
			Label = common.SelectedStyle.Render(Label)
		}
		content += prefix + marker + " " + Label + "\n"
	}
	if p.VisibleH > 0 && visEnd < len(p.Items) {
		content += common.DimStyle.Render(fmt.Sprintf("  ↓ %d more", len(p.Items)-visEnd)) + "\n"
	}
	off, ask, auto := Counts(p.Items)
	enabled := ask + auto
	total := len(p.Items)
	countSegment := fmt.Sprintf("%d off · %d ask · %d auto  │  %d / %d enabled", off, ask, auto, enabled, total)

	ProfileTotal := p.ProfileTotal + enabled
	ProfileAvailable := p.ProfileAvailable + total
	profileSegment := fmt.Sprintf("  │  profile: %d/%d tools", ProfileTotal, ProfileAvailable)
	footer := countSegment + profileSegment
	content += "\n" + common.DimStyle.Render(footer) + "\n"

	return dialogBorderStyle.Width(max(lipgloss.Width(content)+6, 30)).Render(content)
}

// freshnessHint returns a dim relative-time string like "probed 2h ago"
// for the header. Returns "" when no probe timestamp is set (Items came
// from the static inventory).
func (p Model) freshnessHint() string {
	if p.ProbedAt.IsZero() {
		return ""
	}
	d := time.Since(p.ProbedAt)
	switch {
	case d < time.Minute:
		return "probed just now · r to refresh"
	case d < time.Hour:
		return fmt.Sprintf("probed %dm ago · r to refresh", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("probed %dh ago · r to refresh", int(d.Hours()))
	default:
		return fmt.Sprintf("probed %dd ago · r to refresh", int(d.Hours()/24))
	}
}

func (p Model) HelpText() string {
	if p.Loading {
		return "esc:cancel"
	}
	return "j/k:move  g/G:top/bot  space:cycle  a:ask  A:auto  x/d:off  r:refresh  .:bulk  enter/esc:save"
}

// Counts tallies Items by their tri-State.
func Counts(Items []Item) (off, ask, auto int) {
	for _, item := range Items {
		switch item.State {
		case TriOff:
			off++
		case TriAsk:
			ask++
		case TriAuto:
			auto++
		}
	}
	return
}

// PartitionItems splits Items into the persisted MCP tool lists.
func PartitionItems(Items []Item) (ask, auto []string) {
	for _, item := range Items {
		switch item.State {
		case TriAsk:
			ask = append(ask, item.Label)
		case TriAuto:
			auto = append(auto, item.Label)
		}
	}
	return
}

func sendResult(ID string, confirmed bool, Items []Item) tea.Cmd {
	ask, auto := PartitionItems(Items)
	return func() tea.Msg {
		return ResultMsg{
			ID:        ID,
			Confirmed: confirmed,
			Ask:       ask,
			Auto:      auto,
		}
	}
}

func sendRefresh(ID string) tea.Cmd {
	return func() tea.Msg {
		return RefreshMsg{ID: ID}
	}
}

// ProbePhaseLabel turns a probe Phase + transport identifier into a human-
// readable Phase Label for the picker's Loading view.
func ProbePhaseLabel(p mcp.ProbePhase, transport string) string {
	switch p {
	case mcp.ProbePhaseStarting:
		if transport != "" {
			return fmt.Sprintf("Connecting via %s...", transport)
		}
		return "Connecting..."
	case mcp.ProbePhaseConnected:
		return "Connected. Listing tools..."
	case mcp.ProbePhaseListingTools:
		return "Listing tools..."
	default:
		// Pre-event default (probe just kicked off, no event observed yet)
		// or unknown event type. Generic message preserves the historical
		// "Probing server..." behavior.
		return "Probing server..."
	}
}

func sendBulkRequest(ID string, Items []Item) tea.Cmd {
	ask, auto := PartitionItems(Items)
	return func() tea.Msg {
		return ResultMsg{
			ID:        ID,
			Confirmed: true,
			Ask:       ask,
			Auto:      auto,
			Bulk:      true,
		}
	}
}
