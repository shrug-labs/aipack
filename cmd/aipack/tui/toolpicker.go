package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// triState is the per-item state for the MCP tool picker.
//   - triOff: tool not in any allow list (disabled_tools computed at save time)
//   - triAsk: tool in allowed_tools (harness prompts for approval)
//   - triAuto: tool in always_allowed_tools (auto-approved)
type triState int

const (
	triOff triState = iota
	triAsk
	triAuto
)

// toolPickerItem is one row in the MCP tool picker.
type toolPickerItem struct {
	label string
	state triState
}

// toolPicker is a sub-model for the MCP tool picker. It does not share state
// with dialogModel: the picker has its own async probe lifecycle, scroll
// viewport, key shortcuts, and result message shape, and dialogModel was
// never meant to carry that much state per kind.
type toolPicker struct {
	id     string
	title  string
	items  []toolPickerItem
	cursor int

	// profileTotal is the profile-wide effective MCP tool count excluding
	// the server currently being edited. Added to the picker's live
	// per-tool-state sum to render the "profile: N / limit" footer segment
	// so users can see how the cross-harness limit interacts with their
	// edits in real time.
	profileTotal     int
	profileAvailable int

	// visibleH is the max number of tool rows shown at once. Zero means
	// "show all" (small lists). When the server has more tools, the list
	// scrolls and the footer stays pinned.
	visibleH  int
	scrollOff int

	// loading is true while an async MCP probe is in flight. The picker
	// shows a spinner and only responds to esc. When the probe completes,
	// loading is cleared and items are populated with live tools (or the
	// static fallback on error).
	loading bool
	// loadingErr is set when the probe fails. Displayed as a dim note
	// above the static-fallback tool list so users know the inventory may
	// be stale.
	loadingErr string
	// serverDisabled indicates the MCP server is disabled in the current
	// profile. Shown as a banner so users know their tool-list edits will
	// persist but be inert until the server is re-enabled.
	serverDisabled bool

	// probedAt is the time the items were (last) probed. Zero means the
	// items came from the static pack inventory and no probe has run in
	// this session. Rendered as a relative-time hint ("probed 2h ago") so
	// users can decide whether to `r`-refresh.
	probedAt time.Time
}

// pickerMaxVisible returns the number of tool rows that fit alongside the
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
//   - title + blank → 2
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
func pickerMaxVisible(termHeight int) int {
	return max(termHeight-20, 3)
}

// newToolPicker creates an MCP tool picker. User-visible states cycle
// off → ask → auto → off on <space>.
func newToolPicker(id, title string, items []toolPickerItem) toolPicker {
	return toolPicker{
		id:    id,
		title: title,
		items: items,
	}
}

// withProfileToolTotal attaches the profile-wide tool count (excluding this
// server) so the footer can render "profile: N / limit" against the
// cross-harness MCP tool budget in real time.
func (p toolPicker) withProfileToolTotal(enabled, available int) toolPicker {
	p.profileTotal = enabled
	p.profileAvailable = available
	return p
}

// withServerDisabled marks the picker as editing a server that is disabled
// in the current profile. A banner is rendered so users understand their
// selections persist but only take effect once the server is re-enabled.
func (p toolPicker) withServerDisabled(disabled bool) toolPicker {
	p.serverDisabled = disabled
	return p
}

// withProbedAt attaches the timestamp of the probe that produced the items.
// Pass a zero value when the items came from the static inventory (no probe
// yet); the header hides the freshness hint in that case.
func (p toolPicker) withProbedAt(t time.Time) toolPicker {
	p.probedAt = t
	return p
}

func (p toolPicker) Update(msg tea.Msg) (toolPicker, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	key := km.String()

	if p.loading {
		if key == "esc" {
			return p, sendToolPickerResult(p.id, false, p.items)
		}
		return p, nil
	}
	if len(p.items) == 0 {
		switch key {
		case "enter", "esc":
			return p, sendToolPickerResult(p.id, true, p.items)
		}
		return p, nil
	}

	cursor := &p.items[p.cursor]
	switch key {
	case " ":
		switch cursor.state {
		case triOff:
			cursor.state = triAsk
		case triAsk:
			cursor.state = triAuto
		case triAuto:
			cursor.state = triOff
		}
	case "x", "d":
		cursor.state = triOff
	case "a":
		cursor.state = triAsk
	case "A":
		cursor.state = triAuto
	case "enter", "esc":
		// Save and exit. The picker has no "cancel" — exit intent is always
		// "keep what I see"; escape is a navigation dismiss, not a revert.
		return p, sendToolPickerResult(p.id, true, p.items)
	case ".":
		// Dismiss the picker (saving current state) and chain into the bulk
		// action menu. The root handler sees the bulk flag and reopens the
		// picker after the bulk action completes.
		return p, sendToolPickerBulkRequest(p.id, p.items)
	case "r":
		// Force-refresh: invalidate the cached probe and re-run it. The
		// picker stays open in loading state; the root handler fires the
		// probe command. Current selections are kept — the re-probe only
		// affects which tools appear as options.
		p.loading = true
		p.loadingErr = ""
		return p, sendToolPickerRefresh(p.id)
	case "j", "down":
		if p.cursor < len(p.items)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "pgdown", "ctrl+f":
		step := p.visibleH
		if step <= 0 {
			step = len(p.items)
		}
		p.cursor = min(p.cursor+step, len(p.items)-1)
	case "pgup", "ctrl+b":
		step := p.visibleH
		if step <= 0 {
			step = len(p.items)
		}
		p.cursor = max(p.cursor-step, 0)
	case "g", "home":
		p.cursor = 0
	case "G", "end":
		p.cursor = len(p.items) - 1
	}

	// Clamp scroll so the cursor stays visible within the visibleH window.
	// visibleH == 0 means all items fit; no scrolling needed.
	if p.visibleH > 0 {
		if p.cursor < p.scrollOff {
			p.scrollOff = p.cursor
		}
		if p.cursor >= p.scrollOff+p.visibleH {
			p.scrollOff = p.cursor - p.visibleH + 1
		}
	}
	return p, nil
}

func (p toolPicker) View() string {
	content := dialogTitleStyle.Render(p.title) + "\n\n"
	if p.loading {
		content += "  " + statusDotLoading + " Probing server...\n"
		content += "\n" + dimStyle.Render("  esc:cancel") + "\n"
		return dialogBorderStyle.Width(max(lipgloss.Width(content)+6, 30)).Render(content)
	}
	if p.serverDisabled {
		content += warningStyle.Render("  ⚠ server disabled — changes take effect when server is enabled") + "\n\n"
	}
	if p.loadingErr != "" {
		errText := p.loadingErr
		if len(errText) > 60 {
			errText = errText[:57] + "..."
		}
		content += warningStyle.Render("  ⚠ probe failed — showing pack inventory") + "\n"
		content += dimStyle.Render("    "+errText) + "\n\n"
	}
	if hint := p.freshnessHint(); hint != "" {
		content += dimStyle.Render("  "+hint) + "\n\n"
	}
	legend := "  " + treeCheckOff + dimStyle.Render(" off  ") + treeCheckOn + dimStyle.Render(" ask  ") + triCheckAuto + dimStyle.Render(" auto")
	content += legend + "\n\n"

	// Render only the visible window when scroll is active.
	visStart, visEnd := 0, len(p.items)
	if p.visibleH > 0 && len(p.items) > p.visibleH {
		visStart = p.scrollOff
		visEnd = min(visStart+p.visibleH, len(p.items))
		if visStart > 0 {
			content += dimStyle.Render(fmt.Sprintf("  ↑ %d more", visStart)) + "\n"
		}
	}
	for i := visStart; i < visEnd; i++ {
		item := p.items[i]
		prefix := "  "
		if i == p.cursor {
			prefix = "> "
		}
		marker := treeCheckOff
		switch item.state {
		case triAsk:
			marker = treeCheckOn
		case triAuto:
			marker = triCheckAuto
		case triOff:
			marker = treeCheckOff
		}
		label := item.label
		if i == p.cursor {
			label = selectedStyle.Render(label)
		}
		content += prefix + marker + " " + label + "\n"
	}
	if p.visibleH > 0 && visEnd < len(p.items) {
		content += dimStyle.Render(fmt.Sprintf("  ↓ %d more", len(p.items)-visEnd)) + "\n"
	}
	off, ask, auto := toolPickerCounts(p.items)
	enabled := ask + auto
	total := len(p.items)
	countSegment := fmt.Sprintf("%d off · %d ask · %d auto  │  %d / %d enabled", off, ask, auto, enabled, total)

	profileTotal := p.profileTotal + enabled
	profileAvailable := p.profileAvailable + total
	profileSegment := fmt.Sprintf("  │  profile: %d/%d tools", profileTotal, profileAvailable)
	footer := countSegment + profileSegment
	content += "\n" + dimStyle.Render(footer) + "\n"

	return dialogBorderStyle.Width(max(lipgloss.Width(content)+6, 30)).Render(content)
}

// freshnessHint returns a dim relative-time string like "probed 2h ago"
// for the header. Returns "" when no probe timestamp is set (items came
// from the static inventory).
func (p toolPicker) freshnessHint() string {
	if p.probedAt.IsZero() {
		return ""
	}
	d := time.Since(p.probedAt)
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

func (p toolPicker) helpText() string {
	if p.loading {
		return "esc:cancel"
	}
	return "j/k:move  g/G:top/bot  space:cycle  a:ask  A:auto  x/d:off  r:refresh  .:bulk  enter/esc:save"
}

// toolPickerCounts tallies items by their tri-state.
func toolPickerCounts(items []toolPickerItem) (off, ask, auto int) {
	for _, item := range items {
		switch item.state {
		case triOff:
			off++
		case triAsk:
			ask++
		case triAuto:
			auto++
		}
	}
	return
}

// partitionToolPickerItems splits items into the persisted MCP tool lists.
func partitionToolPickerItems(items []toolPickerItem) (ask, auto []string) {
	for _, item := range items {
		switch item.state {
		case triAsk:
			ask = append(ask, item.label)
		case triAuto:
			auto = append(auto, item.label)
		}
	}
	return
}

func sendToolPickerResult(id string, confirmed bool, items []toolPickerItem) tea.Cmd {
	ask, auto := partitionToolPickerItems(items)
	return func() tea.Msg {
		return toolPickerResultMsg{
			id:        id,
			confirmed: confirmed,
			ask:       ask,
			auto:      auto,
		}
	}
}

func sendToolPickerRefresh(id string) tea.Cmd {
	return func() tea.Msg {
		return toolPickerRefreshMsg{id: id}
	}
}

func sendToolPickerBulkRequest(id string, items []toolPickerItem) tea.Cmd {
	ask, auto := partitionToolPickerItems(items)
	return func() tea.Msg {
		return toolPickerResultMsg{
			id:        id,
			confirmed: true,
			ask:       ask,
			auto:      auto,
			bulk:      true,
		}
	}
}
