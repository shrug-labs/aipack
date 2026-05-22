package configscreen

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
)

func init() {
	common.StatusDotActive = "*"
	common.StatusDotInactive = "o"
	common.StatusDotLoading = "~"
}

func TestViewUsesStorageAccurateConfigLabels(t *testing.T) {
	t.Parallel()

	cfg := config.SyncConfig{}
	cfg.Defaults.Profile = "work"
	cfg.Defaults.Harnesses = []string{"codex"}
	cfg.Defaults.Scope = "global"
	cfg.Defaults.CollisionStrategy = config.CollisionLastWins

	m := New("/tmp/config").
		SetContext(Profile{
			Name: "work",
			Path: "/tmp/config/profiles/work.yaml",
			Config: config.ProfileConfig{
				Params: map[string]string{"workspace": "team-alpha"},
			},
		}, cfg).
		SetRefs([]app.ProfileRef{
			{Kind: "param", Name: "workspace", Status: "set", Pack: "team-tools", Target: "server"},
			{Kind: "param", Name: "mode", Display: "{params.mode:-prod}", Status: "defaulted", Pack: "setup-common"},
			{Kind: "env", Name: "API_TOKEN", Status: "dotenv", Pack: "jira"},
			{Kind: "env", Name: "TOOL_HOME", Status: "env", Pack: "team-tools"},
		})
	m = m.SetSize(120, 32).(Model)

	view := lipgloss.NewCompositor(m.View()).Render()
	params := m
	params.section = sectionParams
	view += "\n" + lipgloss.NewCompositor(params.View()).Render()
	env := m
	env.section = sectionEnv
	view += "\n" + lipgloss.NewCompositor(env.View()).Render()
	defaults := m
	defaults.section = sectionSyncDefaults
	view += "\n" + lipgloss.NewCompositor(defaults.View()).Render()
	for _, want := range []string{
		"Config: work",
		"Params",
		"STATUS",
		"USED BY",
		"Env",
		"AVAILABLE FROM",
		"Sync Defaults",
		"Rendered names",
		"STORED IN",
		"profiles/work.yaml",
		"sync-config.yaml",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	for _, bad := range []string{"profile-scoped", "SOURCE", "SCOPE"} {
		if strings.Contains(view, bad) {
			t.Fatalf("view contains confusing label %q:\n%s", bad, view)
		}
	}
}

func TestSettersPersistRowsCache(t *testing.T) {
	t.Parallel()

	m := New("/tmp/config").
		SetContext(Profile{
			Name: "work",
			Config: config.ProfileConfig{
				Params: map[string]string{"workspace": "team-alpha"},
			},
		}, config.SyncConfig{}).
		SetRefs([]app.ProfileRef{
			{Kind: "param", Name: "mode", Display: "{params.mode:-prod}", Status: "defaulted", Pack: "setup-common"},
			{Kind: "env", Name: "API_TOKEN", Status: "dotenv", Pack: "jira"},
		}).
		SetEnvEntries([]app.EnvEntry{{Key: "API_TOKEN", Value: "secret"}})

	if !m.rowsCacheBuilt {
		t.Fatal("expected setters to persist the rows cache")
	}
	if len(m.refsByName) != 2 {
		t.Fatalf("refsByName has %d entries, want 2", len(m.refsByName))
	}

	m.section = sectionParams
	if got := len(m.activeRows()); got != 2 {
		t.Fatalf("param rows = %d, want 2", got)
	}
	if !m.rowsCacheBuilt {
		t.Fatal("activeRows should not rebuild a discarded value-copy cache")
	}
}

func TestShortProfilePathHandlesWindowsSeparators(t *testing.T) {
	t.Parallel()

	got := shortProfilePath(`C:\Users\dev\AppData\Roaming\aipack\profiles\work.yaml`)
	if got != "profiles/work.yaml" {
		t.Fatalf("shortProfilePath windows path = %q, want %q", got, "profiles/work.yaml")
	}
}

func TestPaneNavigationMovesSectionsThenContent(t *testing.T) {
	t.Parallel()

	m := New("/tmp/config").
		SetContext(Profile{
			Name: "work",
			Config: config.ProfileConfig{
				Params: map[string]string{"workspace": "team-alpha"},
			},
		}, config.SyncConfig{}).
		SetRefs([]app.ProfileRef{
			{Kind: "env", Name: "API_TOKEN", Status: "dotenv", Pack: "jira"},
		})

	if m.focus != paneSections {
		t.Fatalf("expected initial focus on sections, got %v", m.focus)
	}
	if m.section != sectionSyncDefaults {
		t.Fatalf("expected initial section to be Sync Defaults, got %v", m.section)
	}
	screen, cmd := m.Update(keyText("j"))
	if cmd != nil {
		t.Fatal("did not expect section navigation command")
	}
	m = screen.(Model)
	if m.section != sectionParams || m.focus != paneSections {
		t.Fatalf("expected Params section focus, got section=%v focus=%v", m.section, m.focus)
	}

	screen, cmd = m.Update(keyText("j"))
	if cmd != nil {
		t.Fatal("did not expect section navigation command")
	}
	m = screen.(Model)
	if m.section != sectionEnv || m.focus != paneSections {
		t.Fatalf("expected Env section focus, got section=%v focus=%v", m.section, m.focus)
	}

	screen, cmd = m.Update(keyText("right"))
	if cmd != nil {
		t.Fatal("did not expect right navigation command")
	}
	m = screen.(Model)
	if m.section != sectionEnv || m.focus != paneContent || m.cursor != 0 {
		t.Fatalf("expected Env content focus at row 0, got section=%v focus=%v cursor=%d", m.section, m.focus, m.cursor)
	}

	screen, cmd = m.Update(keyText("enter"))
	if cmd == nil {
		t.Fatal("expected enter on content row to edit env")
	}
	if msg := cmd(); msg != (EditEnvMsg{Key: "API_TOKEN"}) {
		t.Fatalf("expected env edit message, got %#v", msg)
	}
	m = screen.(Model)

	screen, cmd = m.Update(keyText("esc"))
	if cmd != nil {
		t.Fatal("did not expect esc navigation command")
	}
	m = screen.(Model)
	if m.focus != paneSections {
		t.Fatalf("expected esc to return to sections pane, got %v", m.focus)
	}
}

func TestLayerHitOnSyncDefaultsEmitsConfigIntent(t *testing.T) {
	t.Parallel()

	cfg := config.SyncConfig{}
	cfg.Defaults.Harnesses = []string{"codex"}
	m := New("/tmp/config").SetContext(Profile{Name: "work"}, cfg)
	m.section = sectionSyncDefaults
	m.focus = paneContent

	screen, cmd := m.Update(common.LayerHitMsg{
		ID:    "config:harnesses",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd == nil {
		t.Fatal("expected harness click command")
	}
	if msg := cmd(); msg != (HarnessSelectMsg{}) {
		t.Fatalf("expected HarnessSelectMsg, got %#v", msg)
	}
	m = screen.(Model)
	if m.section != sectionSyncDefaults {
		t.Fatalf("expected sync defaults section, got %v", m.section)
	}
	if m.focus != paneContent {
		t.Fatalf("expected content focus, got %v", m.focus)
	}

	screen, cmd = m.Update(common.LayerHitMsg{
		ID:    "config:namespaced",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd == nil {
		t.Fatal("expected namespaced click command")
	}
	if msg := cmd(); msg != (ToggleNamespacedMsg{}) {
		t.Fatalf("expected ToggleNamespacedMsg, got %#v", msg)
	}
	m = screen.(Model)
	if m.cursor != 4 {
		t.Fatalf("expected namespaced cursor row 4, got %d", m.cursor)
	}
}

func TestSyncDefaultsNamespacedRowActivates(t *testing.T) {
	t.Parallel()

	m := New("/tmp/config").SetContext(Profile{Name: "work"}, config.SyncConfig{})
	m.section = sectionSyncDefaults
	m.focus = paneContent
	m.cursor = 4

	_, cmd := m.Update(keyText("enter"))
	if cmd == nil {
		t.Fatal("expected namespaced row activation command")
	}
	if msg := cmd(); msg != (ToggleNamespacedMsg{}) {
		t.Fatalf("expected ToggleNamespacedMsg, got %#v", msg)
	}
}

func TestViewRegistersFullRowHitLayers(t *testing.T) {
	t.Parallel()

	cfg := config.SyncConfig{}
	cfg.Defaults.Harnesses = []string{"codex"}
	m := New("/tmp/config").
		SetContext(Profile{Name: "work", Config: config.ProfileConfig{Params: map[string]string{"workspace": "team-alpha"}}}, cfg).
		SetRefs([]app.ProfileRef{{Kind: "env", Name: "API_TOKEN", Status: "dotenv", Pack: "jira"}})
	m = m.SetSize(120, 32).(Model)

	comp := lipgloss.NewCompositor(m.View())
	for _, id := range []string{"config:section:sync-defaults", "config:section:params", "config:section:env", "config:default-profile"} {
		if comp.GetLayer(id) == nil {
			t.Fatalf("expected layer %q", id)
		}
	}

	params := m
	params.section = sectionParams
	comp = lipgloss.NewCompositor(params.View())
	if comp.GetLayer("config:param:workspace") == nil {
		t.Fatal("expected param row layer")
	}

	env := m
	env.section = sectionEnv
	comp = lipgloss.NewCompositor(env.View())
	if comp.GetLayer("config:env:API_TOKEN") == nil {
		t.Fatal("expected env row layer")
	}

	defaults := m
	defaults.section = sectionSyncDefaults
	defaults.focus = paneContent
	comp = lipgloss.NewCompositor(defaults.View())
	if comp.GetLayer("config:harnesses") == nil {
		t.Fatal("expected harness row layer")
	}
	if comp.GetLayer("config:namespaced") == nil {
		t.Fatal("expected namespaced row layer")
	}
}

func TestViewHitLayersAlignWithRenderedRows(t *testing.T) {
	t.Parallel()

	cfg := config.SyncConfig{}
	cfg.Defaults.Harnesses = []string{"codex"}
	m := New("/tmp/config").
		SetContext(Profile{Name: "work", Config: config.ProfileConfig{Params: map[string]string{"workspace": "team-alpha"}}}, cfg).
		SetRefs([]app.ProfileRef{{Kind: "env", Name: "API_TOKEN", Status: "dotenv", Pack: "jira"}})
	m = m.SetSize(120, 32).(Model)

	params := m
	params.section = sectionParams
	comp := lipgloss.NewCompositor(params.View())
	rendered := strings.Split(comp.Render(), "\n")
	x, y := findRenderedText(t, rendered, "workspace")
	if got := comp.Hit(x, y).ID(); got != "config:param:workspace" {
		t.Fatalf("Hit(%d,%d) on workspace = %q, want config:param:workspace\n%s", x, y, got, strings.Join(rendered, "\n"))
	}

	env := m
	env.section = sectionEnv
	comp = lipgloss.NewCompositor(env.View())
	rendered = strings.Split(comp.Render(), "\n")
	x, y = findRenderedText(t, rendered, "API_TOKEN")
	if got := comp.Hit(x, y).ID(); got != "config:env:API_TOKEN" {
		t.Fatalf("Hit(%d,%d) on API_TOKEN = %q, want config:env:API_TOKEN\n%s", x, y, got, strings.Join(rendered, "\n"))
	}

	defaults := m
	defaults.section = sectionSyncDefaults
	defaults.focus = paneContent
	comp = lipgloss.NewCompositor(defaults.View())
	rendered = strings.Split(comp.Render(), "\n")
	x, y = findRenderedText(t, rendered, "Harnesses")
	if got := comp.Hit(x, y).ID(); got != "config:harnesses" {
		t.Fatalf("Hit(%d,%d) on Harnesses = %q, want config:harnesses\n%s", x, y, got, strings.Join(rendered, "\n"))
	}
	x, y = findRenderedText(t, rendered, "Rendered names")
	if got := comp.Hit(x, y).ID(); got != "config:namespaced" {
		t.Fatalf("Hit(%d,%d) on Rendered names = %q, want config:namespaced\n%s", x, y, got, strings.Join(rendered, "\n"))
	}
}

func findRenderedText(t *testing.T, lines []string, text string) (int, int) {
	t.Helper()
	for y, line := range lines {
		if x := strings.Index(line, text); x >= 0 {
			return x, y
		}
	}
	t.Fatalf("could not find %q in render:\n%s", text, strings.Join(lines, "\n"))
	return 0, 0
}

func keyText(text string) tea.KeyPressMsg {
	switch text {
	case "right":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyRight})
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
	default:
		code := rune(0)
		if len(text) == 1 {
			code = []rune(text)[0]
		}
		return tea.KeyPressMsg(tea.Key{Text: text, Code: code})
	}
}
