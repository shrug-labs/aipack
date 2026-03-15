package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

type syncTabModel struct {
	syncCfg   config.SyncConfig // read-only snapshot, set by rootModel before Update/View
	configDir string
	cursor    int  // index into flat field list
	prune     bool // session-only toggle: delete stale managed files on sync

	// Active profile state — populated by rootModel before View/Update.
	activeSync syncTabSnapshot

	// Derived data.
	allHarnesses []string // domain.HarnessNames()
	profileNames []string // from profilesLoadedMsg

	width, height int
}

func newSyncTabModel(configDir string) syncTabModel {
	return syncTabModel{
		configDir:    configDir,
		allHarnesses: domain.HarnessNames(),
	}
}

func (m syncTabModel) Init() tea.Cmd {
	return nil
}

// fieldCount returns the total number of navigable fields:
// 1 (profile) + len(allHarnesses) + 1 (scope) + 1 (prune).
func (m syncTabModel) fieldCount() int {
	return 1 + len(m.allHarnesses) + 2
}

func (m syncTabModel) Update(msg tea.Msg) (syncTabModel, tea.Cmd) {
	switch msg := msg.(type) {
	case profilesLoadedMsg:
		if msg.err == nil {
			names := make([]string, len(msg.items))
			for i, item := range msg.items {
				names[i] = item.name
			}
			m.profileNames = names
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m syncTabModel) handleKey(msg tea.KeyMsg) (syncTabModel, tea.Cmd) {
	fc := m.fieldCount()
	switch msg.String() {
	case "j", "down":
		m.cursor = (m.cursor + 1) % fc
	case "k", "up":
		m.cursor--
		if m.cursor < 0 {
			m.cursor = fc - 1
		}
	case " ", "enter":
		return m.editField()
	}
	return m, nil
}

// syncProfileSelectMsg signals the root model to open a profile select dialog.
type syncProfileSelectMsg struct{}

// editField handles space/enter on the currently focused field.
// Emits intent messages for mutations; rootModel handles the actual state change.
func (m syncTabModel) editField() (syncTabModel, tea.Cmd) {
	if m.cursor == 0 {
		// Profile field — signal root model to open profile list dialog.
		if len(m.profileNames) > 0 {
			return m, func() tea.Msg { return syncProfileSelectMsg{} }
		}
		return m, nil
	}

	harnessEnd := 1 + len(m.allHarnesses)
	if m.cursor >= 1 && m.cursor < harnessEnd {
		idx := m.cursor - 1
		harness := m.allHarnesses[idx]
		return m, func() tea.Msg { return syncToggleHarnessMsg{harness: harness} }
	}

	if m.cursor == harnessEnd {
		return m, func() tea.Msg { return syncCycleScopeMsg{} }
	}

	if m.cursor == harnessEnd+1 {
		m.prune = !m.prune
		return m, nil
	}

	return m, nil
}

// toggleHarness delegates to app.ToggleSyncHarness.
func toggleHarness(cfg config.SyncConfig, name string) config.SyncConfig {
	return app.ToggleSyncHarness(cfg, name)
}

// cycleScope delegates to app.CycleSyncScope.
func cycleScope(cfg config.SyncConfig) config.SyncConfig {
	return app.CycleSyncScope(cfg)
}

func (m syncTabModel) harnessEnabled(name string) bool {
	for _, h := range m.syncCfg.Defaults.Harnesses {
		if h == name {
			return true
		}
	}
	return false
}

func (m syncTabModel) View() string {
	leftW := m.width * 40 / 100
	if leftW < 30 {
		leftW = 30
	}
	rightW := m.width - leftW - 6
	if rightW < 20 {
		rightW = 20
	}

	left := m.viewConfigPanel(leftW)
	right := m.viewStatusPanel(rightW)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return contentStyle.Render(joined)
}

func (m syncTabModel) viewConfigPanel(width int) string {
	style := lipgloss.NewStyle().Width(width)
	var sb strings.Builder

	cursor := 0

	// Profile field.
	profileName := m.syncCfg.Defaults.Profile
	if profileName == "" {
		profileName = "default"
	}
	indicator := "  "
	if m.cursor == cursor {
		indicator = selectedStyle.Render("> ")
	}
	sb.WriteString(fmt.Sprintf("%sProfile:   %s\n", indicator, profileName))
	cursor++

	// Harness checkboxes.
	sb.WriteString("  Harnesses:\n")
	for _, h := range m.allHarnesses {
		indicator = "  "
		if m.cursor == cursor {
			indicator = selectedStyle.Render("> ")
		}
		check := "[ ]"
		if m.harnessEnabled(h) {
			check = "[x]"
		}
		sb.WriteString(fmt.Sprintf("  %s%s %s\n", indicator, check, h))
		cursor++
	}

	// Scope field.
	indicator = "  "
	if m.cursor == cursor {
		indicator = selectedStyle.Render("> ")
	}
	scope := m.syncCfg.Defaults.Scope
	if scope == "" {
		scope = string(domain.ScopeProject)
	}
	sb.WriteString(fmt.Sprintf("%sScope:     %s\n", indicator, scope))

	// Prune indicator (session toggle, not a navigable field).
	pruneLabel := "off"
	if m.prune {
		pruneLabel = "on"
	}
	cursor++
	indicator = "  "
	if m.cursor == cursor {
		indicator = selectedStyle.Render("> ")
	}
	sb.WriteString(fmt.Sprintf("%sPrune:     %s\n", indicator, pruneLabel))

	sb.WriteString(fmt.Sprintf("  Config:    %s\n", dimStyle.Render(shortPath(m.configDir))))

	return style.Render(sb.String())
}

func (m syncTabModel) viewStatusPanel(width int) string {
	style := lipgloss.NewStyle().Width(width)
	var sb strings.Builder

	snap := m.activeSync

	// Status.
	dot := statusDotInactive
	label := ""
	switch snap.syncState {
	case syncLoading:
		dot = statusDotLoading
		label = " checking..."
	case syncSynced:
		dot = statusDotActive
		label = " up to date"
	case syncUnsynced:
		n := snap.syncTarget.TotalChanges()
		label = fmt.Sprintf(" %d pending", n)
	case syncPending:
		dot = dimStyle.Render("—")
	case syncError:
		label = " error"
	}
	sb.WriteString(fmt.Sprintf("Status:  %s%s\n", dot, label))

	if snap.syncState == syncError && snap.syncErrText != "" {
		sb.WriteString(errorStyle.Render(fmt.Sprintf("  %s", snap.syncErrText)) + "\n")
	}

	// Pending changes breakdown.
	if snap.syncState == syncUnsynced || snap.syncState == syncSynced {
		sb.WriteString("\n")
		sb.WriteString("Pending Changes:\n")
		sb.WriteString(fmt.Sprintf("  Rules:     %d\n", snap.syncTarget.NumRules))
		sb.WriteString(fmt.Sprintf("  Workflows: %d\n", snap.syncTarget.NumWorkflows))
		sb.WriteString(fmt.Sprintf("  Agents:    %d\n", snap.syncTarget.NumAgents))
		sb.WriteString(fmt.Sprintf("  Skills:    %d\n", snap.syncTarget.NumSkills))
		sb.WriteString(fmt.Sprintf("  Settings:  %d\n", snap.syncTarget.NumSettings))
		sb.WriteString(fmt.Sprintf("  MCP:       %d\n", snap.syncTarget.NumMCP))
		sb.WriteString(fmt.Sprintf("  Prunes:    %d\n", snap.syncTarget.NumPrunes))
		sb.WriteString(fmt.Sprintf("  Total:     %d\n", snap.syncTarget.TotalChanges()))
	}

	// Per-harness ledger info.
	if len(snap.syncTarget.HarnessLedgers) > 0 {
		sb.WriteString("\n")
		sb.WriteString("Ledgers:\n")
		for _, hl := range snap.syncTarget.HarnessLedgers {
			line := fmt.Sprintf("  %-12s %d files", hl.Harness, hl.Files)
			if hl.UpdatedAt > 0 {
				line += dimStyle.Render("  " + formatSyncAge(hl.UpdatedAt))
			}
			sb.WriteString(line + "\n")
		}
	}

	// Last sync timestamp — use the most recent harness ledger.
	if latest := latestLedgerTime(snap.syncTarget.HarnessLedgers); latest > 0 {
		sb.WriteString("\n")
		t := time.Unix(latest, 0)
		sb.WriteString(fmt.Sprintf("Last sync: %s\n", dimStyle.Render(t.Format("2006-01-02 15:04:05"))))
	}

	// Warnings.
	if len(snap.syncWarnings) > 0 {
		sb.WriteString("\n")
		sb.WriteString(warningStyle.Render(fmt.Sprintf("Warnings (%d):", len(snap.syncWarnings))) + "\n")
		for _, w := range snap.syncWarnings {
			fmt.Fprintf(&sb, "  %s\n", w.String())
		}
	}

	return style.Render(sb.String())
}

// latestLedgerTime returns the most recent UpdatedAt across all harness ledgers.
func latestLedgerTime(ledgers []app.HarnessLedgerInfo) int64 {
	var latest int64
	for _, hl := range ledgers {
		if hl.UpdatedAt > latest {
			latest = hl.UpdatedAt
		}
	}
	return latest
}

// formatSyncAge returns a human-friendly relative time like "2m ago" or "3d ago".
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
