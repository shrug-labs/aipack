package packs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func newTestPacksModel(items []packItemDetail) Model {
	m := Model{
		items:         items,
		installedMap:  map[string]int{},
		indexDetails:  map[string]app.IndexedPackDetail{},
		versionsCache: map[string]packVersionsCacheEntry{},
		driftSet:      map[string]app.PackDrift{},
	}
	for i, item := range items {
		m.installedMap[item.entry.Name] = i
	}
	m.rebuildList()
	return m
}

func TestPacksModel_EnterOpensContentPanel(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:   "test-pack",
			Path:   "/tmp/pack",
			Rules:  []string{"rule-a", "rule-b"},
			Agents: []string{"agent-a"},
		}},
	})

	m, cmd := updateTestModel(t, m, testKeyCode(tea.KeyEnter))
	if m.focus != packPanelContent {
		t.Fatal("expected focus=packPanelContent after enter on installed pack")
	}
	if cmd == nil {
		t.Fatal("expected inline preview load command after entering content")
	}
	// Should have header + 2 rules + header + 1 agent = 5 items.
	if len(m.contentItems) != 5 {
		t.Fatalf("expected 5 content items, got %d", len(m.contentItems))
	}
	// Cursor should skip the header.
	if m.contentCursor != 1 {
		t.Fatalf("expected contentCursor=1 (skip header), got %d", m.contentCursor)
	}
	if m.contentItems[1].id != "rule-a" {
		t.Fatalf("expected first non-header item 'rule-a', got %q", m.contentItems[1].id)
	}
}

func TestBuildContentItemsFromEntry_IncludesPrompts(t *testing.T) {
	t.Parallel()
	items := buildContentItemsFromEntry(app.PackShowEntry{
		Name:    "pack-with-prompts",
		Rules:   []string{"rule-a"},
		Prompts: []string{"brainstorm", "review"},
	})

	var rulesHeader, promptsHeader bool
	var promptIDs []string
	for _, it := range items {
		if it.isHeader && it.category == domain.CategoryRules {
			rulesHeader = true
		}
		if it.isHeader && it.category == domain.CategoryPrompts {
			promptsHeader = true
		}
		if !it.isHeader && it.category == domain.CategoryPrompts {
			promptIDs = append(promptIDs, it.id)
		}
	}
	if !rulesHeader {
		t.Error("expected Rules header in content items")
	}
	if !promptsHeader {
		t.Error("expected Prompts header in content items")
	}
	if len(promptIDs) != 2 || promptIDs[0] != "brainstorm" || promptIDs[1] != "review" {
		t.Errorf("expected prompt ids [brainstorm review], got %v", promptIDs)
	}
}

func TestBuildContentItemsFromEntry_IncludesSettings(t *testing.T) {
	t.Parallel()
	items := buildContentItemsFromEntry(app.PackShowEntry{
		Name:     "pack-with-settings",
		Rules:    []string{"rule-a"},
		Settings: []string{"codex/config.toml", "opencode/opencode.json"},
	})

	var settingsHeader bool
	var settingsIDs []string
	for _, it := range items {
		if it.isHeader && it.category == domain.CategorySettings {
			settingsHeader = true
			if it.id != domain.CategorySettings.Label() {
				t.Errorf("settings header label = %q, want %q", it.id, domain.CategorySettings.Label())
			}
		}
		if !it.isHeader && it.category == domain.CategorySettings {
			settingsIDs = append(settingsIDs, it.id)
		}
	}
	if !settingsHeader {
		t.Error("expected Settings header in content items")
	}
	want := []string{"codex/config.toml", "opencode/opencode.json"}
	if len(settingsIDs) != len(want) || settingsIDs[0] != want[0] || settingsIDs[1] != want[1] {
		t.Errorf("settings ids = %v, want %v", settingsIDs, want)
	}
}

func TestPacksModel_RendersSettingsSection(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:     "settings-pack",
			Path:     "/tmp/pack",
			Rules:    []string{"rule-a"},
			Settings: []string{"codex/config.toml"},
		}},
	})
	m = m.SetSize(120, 30).(Model)
	// Drill into the content panel so it renders content for the selected pack.
	m = m.SetFocus(FocusContent)

	out := m.RenderString()
	if !strings.Contains(out, "Settings") {
		t.Errorf("expected rendered content panel to contain 'Settings' header; got:\n%s", out)
	}
	if !strings.Contains(out, "config.toml") {
		t.Errorf("expected rendered content panel to contain settings file id; got:\n%s", out)
	}
}

func TestPacksModel_ListSortedInstalledThenAlphabetical(t *testing.T) {
	t.Parallel()
	m := Model{
		items: []packItemDetail{
			{entry: app.PackShowEntry{Name: "zeta"}},
			{entry: app.PackShowEntry{Name: "alpha"}},
		},
		installedMap: map[string]int{"zeta": 0, "alpha": 1},
		registry: []registryItem{
			{name: "beta", installed: false},
			{name: "alpha", installed: true},
			{name: "aardvark", installed: false},
		},
	}
	m.rebuildList()

	got := []string{
		m.listItems[0].name,
		m.listItems[1].name,
		m.listItems[2].name,
		m.listItems[3].name,
	}
	want := []string{"alpha", "zeta", "aardvark", "beta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected sort order %v, want %v", got, want)
		}
	}
}

func TestPacksModel_ContentNavigation(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a", "rule-b"},
		}},
	})

	// Enter content panel.
	m, _ = updateTestModel(t, m, testKeyCode(tea.KeyEnter))
	if m.contentCursor != 1 {
		t.Fatalf("expected contentCursor=1, got %d", m.contentCursor)
	}

	// Move down.
	m, _ = updateTestModel(t, m, testKeyText("j"))
	if m.contentCursor != 2 {
		t.Fatalf("expected contentCursor=2 after j, got %d", m.contentCursor)
	}

	// Move up.
	m, _ = updateTestModel(t, m, testKeyText("k"))
	if m.contentCursor != 1 {
		t.Fatalf("expected contentCursor=1 after k, got %d", m.contentCursor)
	}
}

func TestPacksModel_ContentEscReturns(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})

	m, _ = updateTestModel(t, m, testKeyCode(tea.KeyEnter))
	if m.focus != packPanelContent {
		t.Fatal("expected focus=packPanelContent")
	}

	m, _ = updateTestModel(t, m, testKeyCode(tea.KeyEsc))
	if m.focus != packPanelList {
		t.Fatal("expected focus=packPanelList after esc")
	}
}

func TestPacksModel_ContentRightMovesToPreviewAndScrolls(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})
	m.focus = packPanelContent
	m.previewState = asyncLoaded
	m.previewData = previewLoadedMsg{
		Title:    "rule-a",
		Category: domain.CategoryRules,
		FilePath: "/tmp/pack/rules/rule-a.md",
		Body:     "line-1\nline-2\nline-3\nline-4\nline-5\nline-6\nline-7",
	}
	m.height = 8
	m.width = 100

	m, _ = updateTestModel(t, m, testKeyCode(tea.KeyRight))
	if m.focus != packPanelPreview {
		t.Fatalf("expected focus=packPanelPreview after right, got %d", m.focus)
	}

	m, _ = updateTestModel(t, m, testKeyText("j"))
	if m.previewOffset == 0 {
		t.Fatal("expected previewOffset to advance after scrolling in preview focus")
	}

	m, _ = updateTestModel(t, m, testKeyCode(tea.KeyEsc))
	if m.focus != packPanelContent {
		t.Fatalf("expected esc from preview to return to content, got %d", m.focus)
	}
}

func TestPacksModel_ContentEnterEmitsPreview(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})

	// Enter content panel.
	m, _ = updateTestModel(t, m, testKeyCode(tea.KeyEnter))
	// Enter on the content item should emit common.PreviewRequestMsg.
	_, cmd := updateTestModel(t, m, testKeyCode(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected cmd for preview request")
	}
	msg := cmd()
	req, ok := msg.(common.PreviewRequestMsg)
	if !ok {
		t.Fatalf("expected common.PreviewRequestMsg, got %T", msg)
	}
	if req.Title != "rule-a" {
		t.Fatalf("expected title 'rule-a', got %q", req.Title)
	}
	if req.PackName != "test-pack" {
		t.Fatalf("expected packName 'test-pack', got %q", req.PackName)
	}
	if req.Category != domain.CategoryRules {
		t.Fatalf("expected category %q, got %q", domain.CategoryRules, req.Category)
	}
}

func TestPacksModel_BuildContentItems(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:       "test-pack",
			Rules:      []string{"r1"},
			Agents:     []string{"a1", "a2"},
			Skills:     []string{"s1"},
			MCPServers: []string{},
		}},
	})

	items := m.buildContentItems(0)
	// Rules header + 1 rule + Agents header + 2 agents + Skills header + 1 skill = 7.
	if len(items) != 7 {
		t.Fatalf("expected 7 items, got %d", len(items))
	}
	if !items[0].isHeader || items[0].id != "Rules" {
		t.Fatalf("expected first item to be Rules header, got %+v", items[0])
	}
	if items[1].id != "r1" || items[1].category != domain.CategoryRules {
		t.Fatalf("expected rules item 'r1', got %+v", items[1])
	}
}

func TestPacksModel_ListSelectionDoesNotLoadInlinePreview(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
		{entry: app.PackShowEntry{
			Name:  "other-pack",
			Path:  "/tmp/other-pack",
			Rules: []string{"rule-b"},
		}},
	})

	_, cmd := updateTestModel(t, m, testKeyText("j"))
	if cmd != nil {
		t.Fatal("expected no inline preview load command while focus remains in list")
	}
}

func TestPacksModel_ListLayerHitDoubleClickRequestsActions(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})

	screen, _ := m.Update(common.LayerHitMsg{
		ID:    "packs:list:0",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	m = screen.(Model)
	if m.focus != packPanelList {
		t.Fatal("first click should only select the pack row")
	}

	screen, cmd := m.Update(common.LayerHitMsg{
		ID:    "packs:list:0",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	m = screen.(Model)
	if m.focus != packPanelList {
		t.Fatalf("double-click should keep list focus, got focus %d", m.focus)
	}
	if cmd == nil {
		t.Fatal("expected action request command")
	}
	if msg := cmd(); msg != (ActionRequestMsg{}) {
		t.Fatalf("expected ActionRequestMsg, got %T", msg)
	}
}

func TestPacksModel_ContentLayerHitDoubleClickOpensPreview(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})
	m, _ = updateTestModel(t, m, testKeyCode(tea.KeyEnter))

	screen, _ := m.Update(common.LayerHitMsg{
		ID:    "packs:content:1",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	m = screen.(Model)
	if m.focus != packPanelContent || m.contentCursor != 1 {
		t.Fatal("first click should select the content row")
	}

	_, cmd := m.Update(common.LayerHitMsg{
		ID:    "packs:content:1",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd == nil {
		t.Fatal("expected double-click to request full preview")
	}
	msg := cmd()
	req, ok := msg.(common.PreviewRequestMsg)
	if !ok {
		t.Fatalf("expected common.PreviewRequestMsg, got %T", msg)
	}
	if req.Title != "rule-a" || req.PackName != "test-pack" || req.Category != domain.CategoryRules {
		t.Fatalf("unexpected preview request: %+v", req)
	}
}

func TestPacksModel_ContentHitLayersAlignAfterCategorySpacer(t *testing.T) {
	t.Parallel()

	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:       "test-pack",
			Path:       "/tmp/pack",
			Rules:      []string{"rule-a"},
			MCPServers: []string{"server-a"},
		}},
	})
	m = m.SetSize(100, 20).(Model)
	m, _ = updateTestModel(t, m, testKeyCode(tea.KeyEnter))

	comp := lipgloss.NewCompositor(m.View())
	rendered := strings.Split(stripSGR(comp.Render()), "\n")
	x, y := findPacksRenderedText(t, rendered, "server-a")
	if got, want := comp.Hit(x, y).ID(), "packs:content:3"; got != want {
		t.Fatalf("Hit(%d,%d) on server-a = %q, want %q\n%s", x, y, got, want, strings.Join(rendered, "\n"))
	}
}

func TestPacksModel_ContentHitLayersAlignWithContentPadding(t *testing.T) {
	prev := common.ContentStyle
	common.ContentStyle = lipgloss.NewStyle().Padding(1, 2)
	t.Cleanup(func() { common.ContentStyle = prev })

	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:       "test-pack",
			Path:       "/tmp/pack",
			Rules:      []string{"rule-a"},
			MCPServers: []string{"server-a"},
		}},
	})
	m = m.SetSize(100, 20).(Model)
	m, _ = updateTestModel(t, m, testKeyCode(tea.KeyEnter))

	comp := lipgloss.NewCompositor(m.View())
	rendered := strings.Split(stripSGR(comp.Render()), "\n")
	x, y := findPacksRenderedText(t, rendered, "server-a")
	if got, want := comp.Hit(x, y).ID(), "packs:content:3"; got != want {
		t.Fatalf("Hit(%d,%d) on server-a with content padding = %q, want %q\n%s", x, y, got, want, strings.Join(rendered, "\n"))
	}
}

func TestPacksModel_ContentClampUsesRenderedRows(t *testing.T) {
	t.Parallel()

	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:       "test-pack",
			Path:       "/tmp/pack",
			Rules:      []string{"rule-a"},
			MCPServers: []string{"server-a"},
		}},
	})
	m = m.SetSize(80, 8).(Model)
	m, _ = updateTestModel(t, m, testKeyCode(tea.KeyEnter))
	m.contentCursor = 3
	m.clampContentOffset()

	if got := m.contentOffset; got != 2 {
		t.Fatalf("contentOffset = %d, want rendered-row offset 2", got)
	}
}

func TestPacksModel_ContentLayerHitMouseWheelScrollsContent(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a", "rule-b", "rule-c", "rule-d", "rule-e", "rule-f"},
		}},
	})
	m = m.SetSize(80, 8).(Model)
	m, _ = updateTestModel(t, m, testKeyCode(tea.KeyEnter))
	if m.contentCursor != 1 {
		t.Fatalf("precondition: contentCursor = %d, want 1", m.contentCursor)
	}

	screen, cmd := m.Update(common.LayerHitMsg{
		ID:    "packs:content:1",
		Mouse: tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}),
	})
	if cmd == nil {
		t.Fatal("wheel down should refresh inline preview for new content cursor")
	}
	m = screen.(Model)
	if m.focus != packPanelContent {
		t.Fatalf("focus = %d, want content", m.focus)
	}
	if got := m.contentCursor; got <= 1 {
		t.Fatalf("wheel down should move content cursor, got %d", got)
	}
	if got := m.contentOffset; got == 0 {
		t.Fatalf("wheel down should keep cursor in the rendered content window, offset got %d", got)
	}

	screen, _ = m.Update(common.LayerHitMsg{
		ID:    "packs:content:6",
		Mouse: tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}),
	})
	m = screen.(Model)
	if got := m.contentCursor; got >= 6 {
		t.Fatalf("wheel up should move content cursor upward, got %d", got)
	}
}

var packsTestSGRPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripSGR(s string) string {
	return packsTestSGRPattern.ReplaceAllString(s, "")
}

func findPacksRenderedText(t *testing.T, lines []string, text string) (int, int) {
	t.Helper()
	for y, line := range lines {
		if x := strings.Index(line, text); x >= 0 {
			return x, y
		}
	}
	t.Fatalf("could not find %q in render:\n%s", text, strings.Join(lines, "\n"))
	return 0, 0
}

func TestPacksModel_ContentFilePath(t *testing.T) {
	t.Parallel()
	root := filepath.Join("/tmp", "pack")
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "test-pack", Path: root}},
	})

	tests := []struct {
		ci       contentItem
		expected string
	}{
		{contentItem{category: domain.CategoryRules, id: "rule-a"}, filepath.Join(root, "rules", "rule-a.md")},
		{contentItem{category: domain.CategoryAgents, id: "agent-a"}, filepath.Join(root, "agents", "agent-a.md")},
		{contentItem{category: domain.CategoryWorkflows, id: "wf-a"}, filepath.Join(root, "workflows", "wf-a.md")},
		{contentItem{category: domain.CategorySkills, id: "skill-a"}, filepath.Join(root, "skills", "skill-a", "SKILL.md")},
	}
	for _, tt := range tests {
		got := m.contentFilePath(tt.ci)
		if got != tt.expected {
			t.Errorf("contentFilePath(%+v) = %q, want %q", tt.ci, got, tt.expected)
		}
	}
}

func TestPacksModel_EmptyPackNoContentFocus(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "empty-pack", Path: "/tmp/empty"}},
	})

	m, _ = updateTestModel(t, m, testKeyCode(tea.KeyEnter))
	if m.focus != packPanelList {
		t.Fatal("expected focus=packPanelList for pack with no content")
	}
}

func TestPacksModel_ContentPanelShowsCategoryCounts(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:      "test-pack",
			Path:      "/tmp/pack",
			Rules:     []string{"rule-a", "rule-b"},
			Workflows: []string{"wf-a"},
		}},
	})
	m.width = 120
	m.height = 24

	view := m.viewContentPanel(36, 20)
	if !strings.Contains(view, "Rules (2)") {
		t.Fatalf("expected Rules count in content panel, got:\n%s", view)
	}
	if !strings.Contains(view, "Workflows (1)") {
		t.Fatalf("expected Workflows count in content panel, got:\n%s", view)
	}
}

func TestPacksModel_PackInfoIsCompact(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:        "test-pack",
			Path:        "/tmp/pack",
			Origin:      "/very/long/source/path/to/test-pack",
			Version:     "2026.03.10",
			Method:      "link",
			InstalledAt: "2026-03-12T21:18:52Z",
			Rules:       []string{"rule-a"},
		}},
	})
	m.width = 120
	m.height = 24
	m.registry = []registryItem{{
		name:        "test-pack",
		description: "A longish description for compact rendering.",
		repo:        "ssh://git/example/repo.git",
		ref:         "main",
		owner:       "myteam",
	}}
	m.registryState = asyncLoaded
	m.rebuildList()
	m.items[0].fileSizes = map[string]int64{"total": 4096}

	view := m.viewPackInfoPanel(40, 18)
	if strings.Contains(view, "Registry:") {
		t.Fatalf("expected compact pack info without verbose Registry section, got:\n%s", view)
	}
	if strings.Contains(view, "Installed:") {
		t.Fatalf("expected compact pack info without Installed field label, got:\n%s", view)
	}
	if !strings.Contains(view, "v2026.03.10") {
		t.Fatalf("expected compact version badge, got:\n%s", view)
	}
	if !strings.Contains(view, "A longish description") {
		t.Fatalf("expected compact description, got:\n%s", view)
	}
	if strings.Contains(view, "1 rules") {
		t.Fatalf("expected content counts to move out of pack info, got:\n%s", view)
	}
}

func TestPacksModel_DetailsPanelShowsRemoteOriginAsSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		method     string
		origin     string
		installDir string
	}{
		{
			name:       "clone",
			method:     config.MethodClone,
			origin:     "https://github.com/example/remote-pack.git",
			installDir: "/tmp/aipack/packs/remote-pack",
		},
		{
			name:       "archive",
			method:     config.MethodArchive,
			origin:     "https://example.com/packs/remote-pack.tar.gz",
			installDir: "/tmp/aipack/packs/archive-pack",
		},
		{
			name:       "http-tarball",
			method:     config.MethodHTTPTarball,
			origin:     "https://example.com/packs/remote-pack.tgz",
			installDir: "/tmp/aipack/packs/http-pack",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newTestPacksModel([]packItemDetail{
				{entry: app.PackShowEntry{
					Name:   tt.name + "-pack",
					Path:   tt.installDir,
					Method: tt.method,
					Origin: tt.origin,
				}},
			})

			view := stripSGR(m.viewPackInfoPanel(90, 18))
			if !strings.Contains(view, tt.origin) {
				t.Fatalf("expected Source to show remote origin %q, got:\n%s", tt.origin, view)
			}
			if strings.Contains(view, tt.installDir) {
				t.Fatalf("expected Source to hide installed directory %q for remote pack, got:\n%s", tt.installDir, view)
			}
		})
	}
}

func TestWrapWords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{
			name:  "fits on one line",
			input: "short text",
			width: 20,
			want:  "short text",
		},
		{
			name:  "wraps on word boundaries",
			input: "the quick brown fox",
			width: 10,
			want:  "the quick\nbrown fox",
		},
		{
			name:  "hard-breaks unbreakable single token longer than width",
			input: "supercalifragilistic",
			width: 8,
			want:  "supercal\nifragili\nstic",
		},
		{
			name:  "mixes hard-break and soft-break",
			input: "ok superlongword next",
			width: 6,
			want:  "ok\nsuperl\nongwor\nd next",
		},
		{
			name:  "empty string",
			input: "",
			width: 10,
			want:  "",
		},
		{
			name:  "zero width returns input",
			input: "anything",
			width: 0,
			want:  "anything",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := wrapWords(tt.input, tt.width); got != tt.want {
				t.Errorf("wrapWords(%q, %d):\ngot:  %q\nwant: %q", tt.input, tt.width, got, tt.want)
			}
		})
	}
}

func TestPacksModel_DetailsPanelWrapsValuesUnderColumn(t *testing.T) {
	t.Parallel()
	// Long About description should wrap at the value column boundary,
	// not bleed into where the next label would appear. Continuation lines
	// must align at column labelW (12 chars), not column 0.
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:   "wide-pack",
			Method: "clone",
		}},
	})
	m.registry = []registryItem{{
		name:        "wide-pack",
		description: "A reasonably long description that will need to wrap across multiple lines inside the details panel value column.",
	}}
	m.registryState = asyncLoaded
	m.rebuildList()
	m.width = 140
	m.height = 30
	m.listCursor = 0

	view := m.viewPackInfoPanel(48, 24)
	lines := strings.Split(view, "\n")

	// Find a non-first line that begins inside the wrapped About value.
	// Every wrapped continuation should be padded to column labelW, never
	// start at column 0 (which would visually collide with the label).
	foundContinuation := false
	for i, line := range lines {
		stripped := strings.TrimRight(line, " ")
		if i == 0 || stripped == "" {
			continue
		}
		// Look for a line that starts with whitespace and contains text —
		// that's a continuation row of a wrapped value.
		if strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "" {
			// All wrap-continuation rows must be indented past the label
			// gutter. The "About" label is 5 chars, padded to labelW=12,
			// so continuation rows must start with at least 12 spaces of
			// indent. Verify by checking that the first non-space char is
			// at column ≥ labelW.
			firstNonSpace := len(line) - len(strings.TrimLeft(line, " "))
			if strings.Contains(line, "wrap") || strings.Contains(line, "lines") || strings.Contains(line, "panel") {
				if firstNonSpace < 12 {
					t.Errorf("continuation line %q has first non-space at col %d, expected >= 12", line, firstNonSpace)
				}
				foundContinuation = true
			}
		}
	}
	if !foundContinuation {
		t.Fatalf("expected to find a wrapped About continuation line in:\n%s", view)
	}
}

func TestPacksModel_DetailsPanelShowsPin(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:       "pinned-pack",
			Path:       "/tmp/pinned-pack",
			Method:     "clone",
			Version:    "1.2.3",
			Pin:        "v1.2.3",
			Ref:        "v1.2.3",
			CommitHash: "abc1234def5678",
			Origin:     "https://example.com/pinned-pack",
		}},
	})
	m.width = 120
	m.height = 30
	m.listCursor = 0

	view := m.viewPackInfoPanel(40, 24)

	// Pin label must appear so users can see the pack is locked.
	if !strings.Contains(view, "v1.2.3 (pinned)") {
		t.Fatalf("expected pin label in details, got:\n%s", view)
	}
	// Ref row is suppressed when Pin is present (Pin already encodes the
	// same information plus the "(pinned)" status).
	if strings.Contains(view, "Ref     ") {
		t.Fatalf("expected Ref row to be hidden when Pin is present, got:\n%s", view)
	}
	// Short commit hash, not full.
	if !strings.Contains(view, "abc1234") {
		t.Fatalf("expected short commit hash in details, got:\n%s", view)
	}
	if strings.Contains(view, "abc1234def5678") {
		t.Fatalf("expected commit hash to be shortened, got:\n%s", view)
	}
}

func TestPacksModel_DetailsPanelShowsRefForUnpinned(t *testing.T) {
	t.Parallel()
	// Unpinned clone tracking a branch — Ref should appear since Pin is empty.
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:       "tracking-pack",
			Path:       "/tmp/tracking-pack",
			Method:     "clone",
			Ref:        "main",
			CommitHash: "deadbeef0000",
			Origin:     "https://example.com/tracking-pack",
		}},
	})
	m.width = 120
	m.height = 30
	m.listCursor = 0

	view := m.viewPackInfoPanel(40, 24)
	if strings.Contains(view, "(pinned)") {
		t.Fatalf("expected no pin label for unpinned pack, got:\n%s", view)
	}
	if !strings.Contains(view, "main") {
		t.Fatalf("expected branch ref in details, got:\n%s", view)
	}
	if !strings.Contains(view, "deadbee") {
		t.Fatalf("expected commit hash (short form) in details, got:\n%s", view)
	}
}

func TestPacksModel_DetailsPanelShowsLatestForInstalledClone(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:    "behind-pack",
			Path:    "/tmp/behind-pack",
			Method:  "clone",
			Version: "1.2.3",
			Pin:     "v1.2.3",
		}},
	})
	m.width = 120
	m.height = 30
	// Seed the version cache as if loadPackVersions had completed:
	// remote has v2.0.0, v1.5.0, v1.2.3 — installed pin is v1.2.3.
	m.versionsCache["behind-pack"] = packVersionsCacheEntry{
		state: asyncLoaded,
		versions: []app.PackVersion{
			{Version: "v2.0.0"},
			{Version: "v1.5.0"},
			{Version: "v1.2.3"},
		},
	}

	view := m.viewPackInfoPanel(50, 24)
	if !strings.Contains(view, "Latest") {
		t.Fatalf("expected Latest row when newer version available, got:\n%s", view)
	}
	if !strings.Contains(view, "v2.0.0") {
		t.Fatalf("expected latest version label, got:\n%s", view)
	}
}

func TestPacksModel_DetailsPanelHidesLatestWhenOnNewest(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:    "current-pack",
			Path:    "/tmp/current-pack",
			Method:  "clone",
			Version: "2.0.0",
			Pin:     "v2.0.0",
		}},
	})
	m.width = 120
	m.height = 30
	m.versionsCache["current-pack"] = packVersionsCacheEntry{
		state: asyncLoaded,
		versions: []app.PackVersion{
			{Version: "v2.0.0"},
			{Version: "v1.5.0"},
		},
	}

	view := m.viewPackInfoPanel(50, 24)
	// No drift signal — Latest row would just be noise.
	if strings.Contains(view, "Latest") {
		t.Fatalf("expected no Latest row when on newest version, got:\n%s", view)
	}
}

func TestPacksModel_RegistryPanelShowsVersions(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m.registry = []registryItem{{
		name:        "discoverable",
		description: "test pack",
		owner:       "team",
		repo:        "https://example.com/discoverable",
		ref:         "main",
	}}
	m.registryState = asyncLoaded
	m.rebuildList()
	m.versionsCache["discoverable"] = packVersionsCacheEntry{
		state: asyncLoaded,
		versions: []app.PackVersion{
			{Version: "v3.0.0"},
			{Version: "v2.5.1"},
			{Version: "v2.0.0"},
			{Version: "v1.0.0"},
		},
	}
	m.width = 120
	m.height = 30

	view := m.viewPackInfoPanel(60, 30)
	if !strings.Contains(view, "Versions") {
		t.Fatalf("expected Versions row in registry block, got:\n%s", view)
	}
	if !strings.Contains(view, "v3.0.0") {
		t.Fatalf("expected newest version in inline list, got:\n%s", view)
	}
	// Caps at three plus a "+N more" suffix; v1.0.0 is the 4th and should
	// not appear in the inline list itself.
	if !strings.Contains(view, "+1 more") {
		t.Fatalf("expected '+1 more' overflow indicator, got:\n%s", view)
	}
}

func TestPacksModel_RegistryPanelShowsLoadingState(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m.registry = []registryItem{{
		name: "loading-pack",
		repo: "https://example.com/loading-pack",
	}}
	m.registryState = asyncLoaded
	m.rebuildList()
	m.versionsCache["loading-pack"] = packVersionsCacheEntry{state: asyncLoading}
	m.width = 120
	m.height = 30

	view := m.viewPackInfoPanel(60, 30)
	if !strings.Contains(view, "loading") {
		t.Fatalf("expected loading indicator while versions in flight, got:\n%s", view)
	}
}

func TestPacksModel_InspectedPackAppearsWithIndexedDetails(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m.indexDetails = map[string]app.IndexedPackDetail{
		"preview-pack": {
			Name:          "preview-pack",
			Version:       "1.2.3",
			Description:   "Previewable pack",
			Repo:          "https://example.com/preview-pack.git",
			Ref:           "main",
			Path:          "packs/preview",
			Owner:         "platform",
			Contact:       "#platform",
			Status:        "inspected",
			Source:        "inspected",
			LastIndexedAt: 1700000000,
			Resources: []app.IndexedPackResource{
				{Kind: "workflow", Name: "release-check", Description: "Release checklist", Path: "workflows/release-check.md", Body: "Check release state before shipping."},
				{Kind: "mcp", Name: "tools", Description: "Tool server", Path: "mcp/tools.json"},
			},
		},
	}
	m.indexState = asyncLoaded
	m.rebuildList()
	m.width = 120
	m.height = 30

	if len(m.listItems) != 1 || m.listItems[0].name != "preview-pack" || !m.listItems[0].inIndex {
		t.Fatalf("indexed pack missing from list: %+v", m.listItems)
	}
	view := m.viewPackInfoPanel(60, 30)
	for _, want := range []string{"Not installed locally", "inspected", "v1.2.3", "1 workflow", "1 mcp-server", "#platform", "preview-pack/packs/preview"} {
		if !strings.Contains(view, want) {
			t.Fatalf("details missing %q:\n%s", want, view)
		}
	}
}

func TestPacksModel_UninstalledIndexedContentIsBrowsable(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m.indexDetails = map[string]app.IndexedPackDetail{
		"preview-pack": {
			Name:   "preview-pack",
			Status: "inspected",
			Source: "inspected",
			Resources: []app.IndexedPackResource{{
				Kind:        "workflow",
				Name:        "release-check",
				Description: "Release checklist",
				Category:    "ops",
				Path:        "workflows/release-check.md",
				Body:        "Check release state before shipping.",
			}},
		},
	}
	m.indexState = asyncLoaded
	m.rebuildList()
	m.width = 120
	m.height = 30

	m, cmd := updateTestModel(t, m, testKeyCode(tea.KeyEnter))
	if m.focus != packPanelContent {
		t.Fatalf("expected focus=packPanelContent, got %d", m.focus)
	}
	if cmd != nil {
		t.Fatal("did not expect file preview load for indexed-only content")
	}
	content := m.viewContentPanel(42, 20)
	if !strings.Contains(content, "Workflows (1)") || !strings.Contains(content, "release-check") {
		t.Fatalf("indexed content panel missing resource:\n%s", content)
	}
	details := m.viewDetailsOrPreviewPanel(48, 20)
	for _, want := range []string{"release-check", "Workflow", "Release checklist", "Check release state"} {
		if !strings.Contains(details, want) {
			t.Fatalf("indexed resource details missing %q:\n%s", want, details)
		}
	}
}

func TestPacksModel_ApplyContentHintSelectsIndexedResource(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m.indexDetails = map[string]app.IndexedPackDetail{
		"preview-pack": {
			Name:   "preview-pack",
			Status: "inspected",
			Source: "inspected",
			Resources: []app.IndexedPackResource{{
				Kind: "workflow",
				Name: "release-check",
			}},
		},
	}
	m.indexState = asyncLoaded
	m.rebuildList()
	m = m.ApplyCursorHint("preview-pack")

	next, cmd, ok := m.ApplyContentHint("workflow", "release-check")
	if !ok {
		t.Fatal("expected content hint to resolve")
	}
	if cmd != nil {
		t.Fatal("did not expect file preview command for indexed-only resource")
	}
	if next.focus != packPanelContent {
		t.Fatalf("expected focus=packPanelContent, got %d", next.focus)
	}
	sel, ok := next.CurrentContentSelection()
	if !ok {
		t.Fatal("expected content selection")
	}
	if sel.Category != domain.CategoryWorkflows || sel.ID != "release-check" {
		t.Fatalf("selection = %+v, want workflow release-check", sel)
	}
}

func TestPacksModel_ApplyContentHintLoadsInstalledPreview(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules", "alpha.md"), []byte("rule body"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "demo",
			Path:  dir,
			Rules: []string{"alpha"},
		}},
	})
	m = m.ApplyCursorHint("demo")

	next, cmd, ok := m.ApplyContentHint("rule", "alpha")
	if !ok {
		t.Fatal("expected content hint to resolve")
	}
	if cmd == nil {
		t.Fatal("expected inline preview command for installed content")
	}
	if next.previewState != asyncLoading {
		t.Fatalf("previewState = %v, want asyncLoading", next.previewState)
	}
	if next.previewPath != filepath.Join(dir, "rules", "alpha.md") {
		t.Fatalf("previewPath = %q, want rule path", next.previewPath)
	}
}

func TestPacksModel_VersionsLoadedMsgPopulatesCache(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m, _ = updateTestModel(t, m, VersionsLoadedMsg{
		PackName: "fresh-pack",
		Versions: []app.PackVersion{
			{Version: "v1.0.0"},
		},
	})
	cached, ok := m.versionsCacheEntry("fresh-pack")
	if !ok {
		t.Fatal("expected cache entry after VersionsLoadedMsg")
	}
	if cached.state != asyncLoaded {
		t.Errorf("expected state asyncLoaded, got %v", cached.state)
	}
	if len(cached.versions) != 1 || cached.versions[0].Version != "v1.0.0" {
		t.Errorf("unexpected cached versions: %+v", cached.versions)
	}
}

func TestPacksModel_VersionsLoadedMsgErrorMarksFailed(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m, _ = updateTestModel(t, m, VersionsLoadedMsg{
		PackName: "broken-pack",
		Err:      fmt.Errorf("network down"),
	})
	cached, ok := m.versionsCacheEntry("broken-pack")
	if !ok {
		t.Fatal("expected cache entry to record the failure")
	}
	if cached.state != asyncError {
		t.Errorf("expected state asyncError, got %v", cached.state)
	}
}

// TestClassifyGitError pins the narrow classifier against real captured
// stderr strings from `git ls-remote` against github.com under each
// failure mode. The negative cases cover the generic fallback phrases
// that used to match an earlier, broader implementation — we explicitly
// do not match "Could not read from remote repository" or "Authentication
// failed" alone because they appear for many unrelated failure modes
// (DNS outages, VPN drops, repo not found) and would mislead the recovery
// hint.
func TestClassifyGitError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want gitErrorKind
	}{
		{"empty", "", gitErrOther},
		{
			name: "publickey_denied",
			in: "git@github.com: Permission denied (publickey).\n" +
				"fatal: Could not read from remote repository.\n\n" +
				"Please make sure you have the correct access rights\n" +
				"and the repository exists.",
			want: gitErrSSHPublickeyDenied,
		},
		{
			name: "host_key_failed",
			in: "@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\n" +
				"@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @\n" +
				"Host key verification failed.\n" +
				"fatal: Could not read from remote repository.",
			want: gitErrSSHHostKeyFailed,
		},
		{
			name: "dns_failure_ssh",
			in: "ssh: Could not resolve hostname nonexistent.example.invalid: " +
				"nodename nor servname provided, or not known\n" +
				"fatal: Could not read from remote repository.",
			want: gitErrOther,
		},
		{
			name: "connection_refused_ssh",
			in: "ssh: connect to host localhost port 65432: Connection refused\n" +
				"fatal: Could not read from remote repository.",
			want: gitErrOther,
		},
		{
			name: "dns_failure_https",
			in:   "fatal: unable to access 'https://nonexistent.example.invalid/foo.git/': Could not resolve host: nonexistent.example.invalid",
			want: gitErrOther,
		},
		{
			name: "connection_refused_https",
			in:   "fatal: unable to access 'https://localhost:65432/foo.git/': Failed to connect to localhost port 65432 after 0 ms: Couldn't connect to server",
			want: gitErrOther,
		},
		{
			name: "https_auth_failed",
			in: "remote: Invalid username or token. Password authentication is not supported for Git operations.\n" +
				"fatal: Authentication failed for 'https://github.com/example/repo.git/'",
			want: gitErrOther,
		},
		{
			name: "github_repo_not_found",
			in: "ERROR: Repository not found.\n" +
				"fatal: Could not read from remote repository.",
			want: gitErrOther,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyGitError(tc.in); got != tc.want {
				t.Errorf("classifyGitError(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestPacksModel_RecoveryHintForCurrent(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "remote", Method: "clone"}},
	})

	// Before any error: no hint.
	if got := m.recoveryHintForCurrent(); got != "" {
		t.Errorf("no cache entry should produce empty hint, got %q", got)
	}

	// Generic fallback error (DNS failure over SSH): no hint.
	m.versionsCache["remote"] = packVersionsCacheEntry{
		state: asyncError,
		err:   "ssh: Could not resolve hostname github.invalid: nodename nor servname provided, or not known\nfatal: Could not read from remote repository.",
	}
	if got := m.recoveryHintForCurrent(); got != "" {
		t.Errorf("DNS failure should not render a recovery hint, got %q", got)
	}

	// SSH publickey denied: ssh-add hint.
	m.versionsCache["remote"] = packVersionsCacheEntry{
		state: asyncError,
		err:   "git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.",
	}
	if got := m.recoveryHintForCurrent(); !strings.Contains(got, "ssh-add") {
		t.Errorf("publickey denied should render ssh-add hint, got %q", got)
	}

	// Host key failure: known_hosts hint.
	m.versionsCache["remote"] = packVersionsCacheEntry{
		state: asyncError,
		err:   "Host key verification failed.\nfatal: Could not read from remote repository.",
	}
	if got := m.recoveryHintForCurrent(); !strings.Contains(got, "known_hosts") {
		t.Errorf("host key failure should render known_hosts hint, got %q", got)
	}
}

func TestPacksModel_MaybeLoadVersionsSkipsLinkPacks(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "linked", Method: "link"}},
	})
	cmd := m.maybeLoadVersionsForCursor()
	if cmd != nil {
		t.Fatal("link-method packs should not trigger version loads")
	}
	if _, ok := m.versionsCacheEntry("linked"); ok {
		t.Fatal("link-method packs should not be added to the cache")
	}
}

func TestPacksModel_MaybeLoadVersionsSkipsIndexOnlyPacks(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m.indexDetails = map[string]app.IndexedPackDetail{
		"preview-pack": {
			Name:   "preview-pack",
			Repo:   "https://example.com/preview-pack.git",
			Status: "inspected",
			Source: "inspected",
		},
	}
	m.indexState = asyncLoaded
	m.rebuildList()

	cmd := m.maybeLoadVersionsForCursor()
	if cmd != nil {
		t.Fatal("index-only packs should not trigger version loads")
	}
	if _, ok := m.versionsCacheEntry("preview-pack"); ok {
		t.Fatal("index-only packs should not be added to the cache")
	}
}

func TestPacksModel_MaybeLoadVersionsClonePacksSchedule(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "remote", Method: "clone"}},
	})
	cmd := m.maybeLoadVersionsForCursor()
	if cmd == nil {
		t.Fatal("clone-method packs should trigger a version load")
	}
	cached, ok := m.versionsCacheEntry("remote")
	if !ok {
		t.Fatal("expected cache entry seeded with loading state")
	}
	if cached.state != asyncLoading {
		t.Errorf("expected loading state, got %v", cached.state)
	}
	// A second call must NOT spawn a duplicate request — the cache entry
	// already records the in-flight load. Without this guard, every cursor
	// blip would re-fire the network call.
	if again := m.maybeLoadVersionsForCursor(); again != nil {
		t.Fatal("expected no duplicate load when one is already in flight")
	}
}

func TestPacksModel_DriftLoadedPopulatesSet(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m, _ = updateTestModel(t, m, DriftLoadedMsg{
		Drifted: []app.PackDrift{
			{Name: "alpha", Method: "clone", Reason: "remote ref has moved since install"},
			{Name: "bravo", Method: "clone", Reason: "remote ref has moved since install"},
		},
	})
	if _, ok := m.driftSet["alpha"]; !ok {
		t.Error("expected alpha in drift set")
	}
	if _, ok := m.driftSet["bravo"]; !ok {
		t.Error("expected bravo in drift set")
	}
	if _, ok := m.driftSet["charlie"]; ok {
		t.Error("charlie should not be in drift set")
	}
}

func TestPacksModel_DriftLoadedReplacesPriorState(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	// Seed an existing entry that should disappear after a fresh scan.
	m.driftSet["stale"] = app.PackDrift{Name: "stale"}

	m, _ = updateTestModel(t, m, DriftLoadedMsg{
		Drifted: []app.PackDrift{
			{Name: "fresh"},
		},
	})
	if _, ok := m.driftSet["stale"]; ok {
		t.Error("stale entry should be cleared by fresh scan")
	}
	if _, ok := m.driftSet["fresh"]; !ok {
		t.Error("fresh entry should be present")
	}
}

func TestPacksModel_ListPanelShowsDriftIndicator(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "drifted", Method: "clone"}},
		{entry: app.PackShowEntry{Name: "current", Method: "clone"}},
	})
	m.driftSet["drifted"] = app.PackDrift{Name: "drifted", Reason: "remote ref has moved"}
	m.width = 120
	m.height = 30

	view := m.viewListPanel(40, 16)
	// The drifted row gets the indicator; the current row doesn't.
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, "drifted") && !strings.Contains(line, "↑") {
			t.Fatalf("drifted row should have ↑ indicator, got: %q", line)
		}
		if strings.Contains(line, "current") && strings.Contains(line, "↑") {
			t.Fatalf("current row should not have ↑ indicator, got: %q", line)
		}
	}
}

func TestPacksModel_ListPanelShowsPinSuffix(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:   "pinned-pack",
			Method: "clone",
			Pin:    "v2.0.0",
		}},
		{entry: app.PackShowEntry{
			Name:   "loose-pack",
			Method: "clone",
		}},
	})
	m.width = 120
	m.height = 30

	view := m.viewListPanel(40, 16)
	// Pinned row gets the suffix.
	if !strings.Contains(view, "v2.0.0 (pinned)") {
		t.Fatalf("expected pin suffix on pinned row, got:\n%s", view)
	}
	// Unpinned row appears without "(pinned)" anywhere on its line.
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, "loose-pack") && strings.Contains(line, "pinned") {
			t.Fatalf("loose-pack row should not have pin suffix, got line: %q", line)
		}
	}
}

func TestPacksModel_ListPanelOmitsStatusBadges(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "alpha"}},
	})
	m.registry = []registryItem{{
		name:  "beta",
		repo:  "ssh://git/example/repo.git",
		owner: "myteam",
	}}
	m.registryState = asyncLoaded
	m.rebuildList()
	m.listCursor = 0

	view := m.viewListPanel(24, 10)
	if !strings.Contains(view, common.SelectedStyle.Render("alpha")) {
		t.Fatalf("expected selected pack name to stay pink, got:\n%s", view)
	}
	if strings.Contains(view, common.SelectedStyle.Render("alpha")+"  installed") {
		t.Fatalf("expected installed badge removed from selected row, got:\n%s", view)
	}
	if strings.Contains(view, "beta  registry") {
		t.Fatalf("expected registry badge removed from pack rows, got:\n%s", view)
	}
	if strings.Contains(view, "ssh://git/example/repo.git") {
		t.Fatalf("expected list panel to avoid duplicated repo metadata, got:\n%s", view)
	}
}

func TestPacksModel_RegistryRowShowsRegisteredStatusFromIndex(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m.registry = []registryItem{{
		name: "registered-pack",
		repo: "https://example.com/registered-pack.git",
	}}
	m.registryState = asyncLoaded
	m.indexDetails = map[string]app.IndexedPackDetail{
		"registered-pack": {
			Name:   "registered-pack",
			Status: "registered",
			Source: "registry",
		},
	}
	m.indexState = asyncLoaded
	m.rebuildList()

	view := stripSGR(m.viewListPanel(48, 10))
	if !strings.Contains(view, "registered-pack") || !strings.Contains(view, "registered") {
		t.Fatalf("expected registry row to show restored registered status, got:\n%s", view)
	}
}

func TestPacksModel_ListPanelShowsActiveRegistryInstall(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m.registry = []registryItem{{
		name: "beta",
	}}
	m.registryState = asyncLoaded
	m.rebuildList()
	m.activeInstall = &activeInstall{
		packName: "beta",
		phase:    app.PackInstallPhaseCloning,
	}

	listView := stripSGR(m.viewListPanel(36, 10))
	if !strings.Contains(listView, "beta") || !strings.Contains(listView, "cloning") {
		t.Fatalf("expected active install row feedback, got:\n%s", listView)
	}

	detailView := stripSGR(m.viewPackInfoPanel(42, 14))
	if !strings.Contains(detailView, "cloning beta") || !strings.Contains(detailView, "Esc to cancel") {
		t.Fatalf("expected active install detail feedback, got:\n%s", detailView)
	}
}

func TestPacksModel_ListPanelOmitsTerminalInstallNotice(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m.activeInstall = &activeInstall{packName: "beta"}

	m, _ = updateTestModel(t, m, InstalledMsg{
		Name: "beta",
		Err:  fmt.Errorf("clone failed"),
	})

	view := stripSGR(m.viewListPanel(50, 10))
	if strings.Contains(view, "install error") || strings.Contains(view, "clone failed") {
		t.Fatalf("expected terminal install status to stay out of list panel, got:\n%s", view)
	}
}

func TestPacksModel_ContentSelectionStaysHighlightedInPreviewFocus(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a", "rule-b"},
		}},
	})
	m.focus = packPanelPreview
	m.contentCursor = 2
	m.items[0].fileSizes = map[string]int64{
		"rules/rule-a": 1024,
		"rules/rule-b": 2048,
	}

	view := m.viewContentPanel(40, 12)
	if !strings.Contains(view, common.SelectedStyle.Render("rule-b")) {
		t.Fatalf("expected selected content item to stay highlighted in preview focus, got:\n%s", view)
	}
}

func TestPacksModel_ContentPanelDoesNotInsertGapAfterCategoryHeader(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a", "rule-b"},
		}},
	})
	m.width = 120
	m.height = 20

	view := m.viewContentPanel(40, 16)
	if strings.Contains(view, "Rules (2)\n\n") {
		t.Fatalf("expected first content item immediately after category header, got:\n%s", view)
	}
}

func TestPacksModel_RegistryLoaded(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "example-pack"}},
	})

	m, _ = updateTestModel(t, m, RegistryLoadedMsg{
		Items: []RegistryItem{
			{Name: "example-pack", Description: "Example ops pack"},
			{Name: "devtools", Description: "Dev tools pack"},
		},
	})

	if m.registryState != asyncLoaded {
		t.Fatal("expected registryState=asyncLoaded")
	}
	if len(m.listItems) != 2 {
		t.Fatalf("expected 2 list items, got %d", len(m.listItems))
	}
	// example-pack should be marked installed.
	if !m.listItems[0].installed {
		t.Fatal("expected example-pack to be marked installed")
	}
	if m.listItems[1].installed {
		t.Fatal("expected devtools to NOT be marked installed")
	}
}

func TestCreatePack_ScaffoldsAndRegisters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Set up minimal sync-config.
	scPath := config.SyncConfigPath(dir)
	if err := config.SaveSyncConfig(scPath, config.SyncConfig{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}

	cmd := Create(dir, "test-created")
	msg := cmd()
	created, ok := msg.(CreatedMsg)
	if !ok {
		t.Fatalf("expected CreatedMsg, got %T", msg)
	}
	if created.Err != nil {
		t.Fatalf("unexpected error: %v", created.Err)
	}
	if created.Name != "test-created" {
		t.Fatalf("expected name 'test-created', got %q", created.Name)
	}

	// Verify pack was scaffolded.
	packDir := filepath.Join(dir, "packs", "test-created")
	if _, err := os.Stat(filepath.Join(packDir, "pack.json")); err != nil {
		t.Fatalf("expected pack.json to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(packDir, "rules")); err != nil {
		t.Fatalf("expected rules/ dir to exist: %v", err)
	}

	// Verify registered in lockfile.
	lf, err := config.LoadLockfile(config.LockfilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := lf.Packs["test-created"]
	if !ok {
		t.Fatal("expected pack to be registered in lockfile")
	}
	if meta.Method != config.MethodLocal {
		t.Fatalf("expected method %q, got %q", config.MethodLocal, meta.Method)
	}
}

func TestCreatePack_DuplicateErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	scPath := config.SyncConfigPath(dir)
	if err := config.SaveSyncConfig(scPath, config.SyncConfig{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}

	// First create succeeds.
	cmd := Create(dir, "dup-pack")
	msg := cmd().(CreatedMsg)
	if msg.Err != nil {
		t.Fatalf("first create failed: %v", msg.Err)
	}

	// Second create should fail (pack.json already exists).
	cmd = Create(dir, "dup-pack")
	msg = cmd().(CreatedMsg)
	if msg.Err == nil {
		t.Fatal("expected error on duplicate pack create")
	}
}

func TestCreatePack_DefaultNameErrorsGracefully(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if err := config.SaveSyncConfig(config.SyncConfigPath(dir), config.SyncConfig{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}

	cmd := Create(dir, "default")
	msg := cmd().(CreatedMsg)
	if msg.Err == nil {
		t.Fatal("expected error for reserved default pack name")
	}
	if !strings.Contains(msg.Err.Error(), "reserved") {
		t.Fatalf("expected reserved-name error, got: %v", msg.Err)
	}
	if _, err := os.Stat(filepath.Join(dir, "packs", "default")); !os.IsNotExist(err) {
		t.Fatalf("reserved pack should not be scaffolded, stat err=%v", err)
	}
}

func TestPacksView_SeparatorHighlightsWithFocus(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})
	m.width = 120
	m.height = 20

	// In list focus, both separators should be dim.
	m.focus = packPanelList
	viewList := m.Render()

	// In content focus, the view should differ (left separator lights up).
	m.focus = packPanelContent
	viewContent := m.Render()

	if viewList == viewContent {
		t.Fatal("expected view to change when focus moves from list to content")
	}
}

func TestPacksModel_UninstalledNoContentFocus(t *testing.T) {
	t.Parallel()
	m := Model{
		installedMap: map[string]int{},
	}
	m.registry = []registryItem{
		{name: "uninstalled-pack", description: "not installed"},
	}
	m.registryState = asyncLoaded
	m.rebuildList()

	// Enter on uninstalled pack should not switch to content panel.
	m, _ = updateTestModel(t, m, testKeyCode(tea.KeyEnter))
	if m.focus != packPanelList {
		t.Fatal("expected focus=packPanelList for uninstalled pack")
	}
}
