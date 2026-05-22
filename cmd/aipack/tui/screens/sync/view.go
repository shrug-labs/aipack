package sync

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	planviewoverlay "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/overlays/planview"
	"github.com/shrug-labs/aipack/internal/app"
)

func (m Model) View() *lipgloss.Layer {
	pv := m.planView(m.planPanelWidth(), m.panelHeight())
	root := lipgloss.NewLayer(m.render(pv))
	root.AddLayers(m.clickLayers(pv)...)
	return root
}

func (m Model) render(pv planviewoverlay.Model) string {
	width := max(m.width-4, 60)
	height := m.panelHeight()
	if width < 92 {
		sections := []string{
			m.viewPlanPanel(pv),
			"",
			m.viewStatusPanel(width, height),
		}
		return common.ContentStyle.Render(lipgloss.NewStyle().Width(width).Render(strings.Join(sections, "\n")))
	}

	gap := 2
	statusWidth := min(max(width/3, 36), 52)
	body := lipgloss.JoinHorizontal(lipgloss.Top,
		m.viewPlanPanel(pv),
		strings.Repeat(" ", gap),
		m.viewStatusPanel(statusWidth, height),
	)
	return common.ContentStyle.Render(lipgloss.NewStyle().Width(width).Render(body))
}

func (m Model) viewStatusPanel(width, height int) string {
	var sb strings.Builder

	snap := m.snapshot

	dot := common.StatusDotInactive
	label := ""
	switch snap.Status {
	case common.SyncStatusLoading:
		dot = common.StatusDotLoading
		label = " checking..."
	case common.SyncStatusSynced:
		dot = common.StatusDotActive
		label = " up to date"
	case common.SyncStatusUnsynced:
		n := snap.Target.TotalChanges()
		label = fmt.Sprintf(" %d pending", n)
	case common.SyncStatusPending:
		dot = common.DimStyle.Render("-")
	case common.SyncStatusError:
		label = " error"
	}
	fmt.Fprintf(&sb, "Status:  %s%s\n", dot, label)

	if snap.Status == common.SyncStatusError && snap.ErrText != "" {
		sb.WriteString(common.ErrorStyle.Render(fmt.Sprintf("  %s", snap.ErrText)) + "\n")
	}

	if snap.Status == common.SyncStatusUnsynced || (snap.Status == common.SyncStatusSynced && snap.Target.TotalChanges() > 0) {
		sb.WriteString("\n")
		sb.WriteString("Pending summary:\n")
		fmt.Fprintf(&sb, "  Rules:     %d\n", snap.Target.NumRules)
		fmt.Fprintf(&sb, "  Workflows: %d\n", snap.Target.NumWorkflows)
		fmt.Fprintf(&sb, "  Agents:    %d\n", snap.Target.NumAgents)
		fmt.Fprintf(&sb, "  Skills:    %d\n", snap.Target.NumSkills)
		fmt.Fprintf(&sb, "  Hooks:     %d\n", snap.Target.NumHooks)
		fmt.Fprintf(&sb, "  Settings:  %d\n", snap.Target.NumSettings)
		fmt.Fprintf(&sb, "  MCP:       %d\n", snap.Target.NumMCP)
		fmt.Fprintf(&sb, "  Stale:     %d\n", snap.Target.NumStale)
		fmt.Fprintf(&sb, "  Total:     %d\n", snap.Target.TotalChanges())
	}

	if len(snap.Target.HarnessLedgers) > 0 {
		sb.WriteString("\n")
		sb.WriteString("Ledgers:\n")
		for _, hl := range snap.Target.HarnessLedgers {
			line := fmt.Sprintf("  %-12s %d files", hl.Harness, hl.Files)
			if hl.UpdatedAt > 0 {
				line += common.DimStyle.Render("  " + formatSyncAge(hl.UpdatedAt))
			}
			sb.WriteString(line + "\n")
		}
	}

	if latest := latestLedgerTime(snap.Target.HarnessLedgers); latest > 0 {
		sb.WriteString("\n")
		t := time.Unix(latest, 0)
		fmt.Fprintf(&sb, "Last sync: %s\n", common.DimStyle.Render(t.Format("2006-01-02 15:04:05")))
	}

	if len(snap.Warnings) > 0 {
		sb.WriteString("\n")
		sb.WriteString(common.WarningStyle.Render(fmt.Sprintf("Warnings (%d):", len(snap.Warnings))) + "\n")
		for _, w := range snap.Warnings {
			fmt.Fprintf(&sb, "  %s\n", w.String())
		}
	}

	content := strings.Join(clipLines(strings.Split(strings.TrimRight(sb.String(), "\n"), "\n"), height-4), "\n")
	return m.panelStyle(width, height).Render(indentBlock(common.SelectedStyle.Render("Status") + "\n\n" + content))
}

func (m Model) viewPlanPanel(pv planviewoverlay.Model) string {
	return pv.Render()
}

func (m Model) clickLayers(pv planviewoverlay.Model) []*lipgloss.Layer {
	if len(pv.Items) == 0 {
		return nil
	}

	padX := common.ContentStyle.GetPaddingLeft()
	padY := common.ContentStyle.GetPaddingTop()
	rowX := padX + 1
	rowW := max(m.planPanelWidth()-2, 1)
	rowHit := strings.Repeat(" ", rowW)

	var layers []*lipgloss.Layer
	y := padY + 4
	end := min(pv.ScrollOffset+pv.ViewportHeight(), len(pv.Items))
	for i := pv.ScrollOffset; i < end; i++ {
		if pv.Items[i].IsHeader {
			if i > pv.ScrollOffset {
				y++
			}
			y++
			continue
		}
		layers = append(layers,
			lipgloss.NewLayer(rowHit).ID(fmt.Sprintf("sync:plan:item:%d", i)).X(rowX).Y(y).Z(-1),
		)
		y++
	}
	return layers
}

func (m Model) panelStyle(width, height int) lipgloss.Style {
	return lipgloss.NewStyle().
		Width(max(width-2, 20)).
		Height(max(height-2, 6)).
		Border(lipgloss.NormalBorder())
}

func (m Model) panelHeight() int {
	return max(m.height-3, 10)
}

func (m Model) planPanelWidth() int {
	width := max(m.width-4, 60)
	if width < 92 {
		return width
	}
	gap := 2
	statusWidth := min(max(width/3, 36), 52)
	return max(width-statusWidth-gap, 40)
}

func clipLines(lines []string, limit int) []string {
	limit = max(limit, 1)
	if len(lines) <= limit {
		return lines
	}
	out := append([]string(nil), lines[:limit]...)
	out[len(out)-1] = "..."
	return out
}

func indentBlock(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = " " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func latestLedgerTime(ledgers []app.HarnessLedgerInfo) int64 {
	var latest int64
	for _, hl := range ledgers {
		if hl.UpdatedAt > latest {
			latest = hl.UpdatedAt
		}
	}
	return latest
}

func formatSyncAge(epochS int64) string {
	d := time.Since(time.Unix(epochS, 0))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
