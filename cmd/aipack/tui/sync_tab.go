package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

type syncTabModel struct {
	syncCfg   config.SyncConfig // read-only snapshot, set by rootModel before Update/View
	configDir string
	cursor    int // index into flat field list

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
// 1 (profile) + len(allHarnesses) + 1 (scope).
func (m syncTabModel) fieldCount() int {
	return 1 + len(m.allHarnesses) + 1
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

	return m, nil
}

// toggleHarness returns a new SyncConfig with the named harness toggled on or off.
// Builds a new slice to avoid mutating the caller's backing array.
func toggleHarness(cfg config.SyncConfig, name string) config.SyncConfig {
	for i, h := range cfg.Defaults.Harnesses {
		if h == name {
			out := make([]string, 0, len(cfg.Defaults.Harnesses)-1)
			out = append(out, cfg.Defaults.Harnesses[:i]...)
			out = append(out, cfg.Defaults.Harnesses[i+1:]...)
			cfg.Defaults.Harnesses = out
			return cfg
		}
	}
	cfg.Defaults.Harnesses = append(cfg.Defaults.Harnesses, name)
	return cfg
}

// cycleScope returns a new SyncConfig with the scope toggled between project and global.
func cycleScope(cfg config.SyncConfig) config.SyncConfig {
	if cfg.Defaults.Scope == string(domain.ScopeGlobal) {
		cfg.Defaults.Scope = string(domain.ScopeProject)
	} else {
		cfg.Defaults.Scope = string(domain.ScopeGlobal)
	}
	return cfg
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
	sb.WriteString("Harnesses:\n")
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

	// Static config info.
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("Config:") + "\n")
	sb.WriteString(fmt.Sprintf("  Config dir: %s\n", dimStyle.Render(shortPath(m.configDir))))

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

	// Target info.
	if len(snap.syncTarget.harnesses) > 0 || snap.syncTarget.scope != "" {
		sb.WriteString("\n")
		sb.WriteString("Target:\n")
		if len(snap.syncTarget.harnesses) > 0 {
			sb.WriteString(fmt.Sprintf("  Harness: %s\n", strings.Join(snap.syncTarget.harnesses, ", ")))
		}
		if snap.syncTarget.scope != "" {
			sb.WriteString(fmt.Sprintf("  Scope:   %s\n", snap.syncTarget.scope))
		}
		if snap.syncTarget.projectDir != "" {
			sb.WriteString(fmt.Sprintf("  Dir:     %s\n", shortPath(snap.syncTarget.projectDir)))
		}
	}

	// Pending changes breakdown.
	if snap.syncState == syncUnsynced || snap.syncState == syncSynced {
		sb.WriteString("\n")
		sb.WriteString("Pending Changes:\n")
		sb.WriteString(fmt.Sprintf("  Writes:    %d\n", snap.syncTarget.NumWrites))
		sb.WriteString(fmt.Sprintf("  Copies:    %d\n", snap.syncTarget.NumCopies))
		sb.WriteString(fmt.Sprintf("  Settings:  %d\n", snap.syncTarget.NumSettings))
		sb.WriteString(fmt.Sprintf("  Plugins:   %d\n", snap.syncTarget.NumPlugins))
		sb.WriteString(fmt.Sprintf("  Prunes:    %d\n", snap.syncTarget.NumPrunes))
		sb.WriteString(fmt.Sprintf("  Total:     %d\n", snap.syncTarget.TotalChanges()))
	}

	// Ledger info.
	if snap.syncTarget.LedgerPath != "" || snap.syncTarget.LedgerFiles > 0 {
		sb.WriteString("\n")
		sb.WriteString("Ledger:\n")
		if snap.syncTarget.LedgerPath != "" {
			sb.WriteString(fmt.Sprintf("  Path:    %s\n", shortPath(snap.syncTarget.LedgerPath)))
		}
		sb.WriteString(fmt.Sprintf("  Files:   %d managed\n", snap.syncTarget.LedgerFiles))
		if snap.syncTarget.ledgerTime > 0 {
			t := time.Unix(snap.syncTarget.ledgerTime, 0)
			sb.WriteString(fmt.Sprintf("  Updated: %s\n", t.Format("2006-01-02 15:04:05")))
		}
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
