package profiles

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func init() {
	common.StatusDotActive = "*"
	common.StatusDotInactive = "o"
	common.StatusDotLoading = "~"
}

func TestModelImplementsScreenAndViewRegistersLayers(t *testing.T) {
	t.Parallel()

	var _ common.Screen = New(context.Background(), nil, "/tmp/config")
	m := New(context.Background(), nil, "/tmp/config")
	m.items = []profileItem{{
		name:     "default",
		isActive: true,
		cfg:      config.ProfileConfig{Packs: []config.PackEntry{{Name: "core"}}},
	}}
	m = m.SetSize(120, 40).(Model)

	comp := lipgloss.NewCompositor(m.View())
	for _, id := range []string{"profiles:item:0", "profiles:pack:0"} {
		if comp.GetLayer(id) == nil {
			t.Fatalf("expected layer %q", id)
		}
	}
}

func TestViewRegistersFullRowHitLayers(t *testing.T) {
	t.Parallel()

	tree := treeModel{
		nodes: []treeNode{
			{kind: nodeCategory, label: "Rules", expanded: true, category: domain.CategoryRules, parentIdx: -1},
			{kind: nodeItem, label: "anti-slop", enabled: true, category: domain.CategoryRules, id: "anti-slop", packIdx: 0, parentIdx: 0, fileSize: -1},
		},
		packs: []app.ProfilePackInfo{{Index: 0, Name: "core"}},
	}
	m := New(context.Background(), nil, "/tmp/config")
	m.items = []profileItem{{
		name: "default-profile-with-a-long-name",
		cfg:  config.ProfileConfig{Packs: []config.PackEntry{{Name: "core-pack-with-a-long-name"}}},
		tree: &tree,
	}}
	m = m.SetSize(120, 40).(Model)

	comp := lipgloss.NewCompositor(m.View())
	const (
		padX = 2
		padY = 1
	)
	col1W := m.profilesMinWidth()
	packX := padX + col1W + 3
	col2W := m.rosterMinWidth()
	treeX := packX + col2W + 3
	col3W := max(m.width-col1W-col2W-8, 20)

	tests := []struct {
		name string
		id   string
		x    int
		y    int
	}{
		{name: "profile", id: "profiles:item:0", x: padX + col1W - 1, y: padY + 1},
		{name: "pack", id: "profiles:pack:0", x: packX + col2W - 1, y: padY + 1},
		{name: "tree", id: "profiles:tree:1", x: treeX + col3W - 1, y: padY + 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := comp.Hit(tt.x, tt.y).ID(); got != tt.id {
				t.Fatalf("Hit(%d,%d) = %q, want %q", tt.x, tt.y, got, tt.id)
			}
		})
	}
}

func TestContentHeaderOmitsSelectedPackAttribution(t *testing.T) {
	t.Parallel()

	tree := treeModel{
		nodes: []treeNode{
			{kind: nodeCategory, label: "Rules", expanded: true, category: domain.CategoryRules, parentIdx: -1},
			{kind: nodeItem, label: "anti-slop", enabled: true, category: domain.CategoryRules, id: "anti-slop", packIdx: 0, parentIdx: 0, fileSize: -1},
		},
		cursor: 1,
		packs:  []app.ProfilePackInfo{{Index: 4, Name: "core"}},
	}
	m := New(context.Background(), nil, "/tmp/config")
	m.items = []profileItem{{
		name: "default",
		cfg:  config.ProfileConfig{Packs: []config.PackEntry{{Name: "core"}}},
		tree: &tree,
	}}
	m = m.SetSize(120, 40).(Model)

	view := lipgloss.NewCompositor(m.View()).Render()
	if strings.Contains(view, "Content (1) · core") {
		t.Fatalf("content header should not repeat selected pack attribution, got:\n%s", view)
	}
	if !strings.Contains(view, "Content (1)") {
		t.Fatalf("expected content count header, got:\n%s", view)
	}
}

func TestStatusHintReportsFocusedTreeItemAttribution(t *testing.T) {
	t.Parallel()

	tree := treeModel{
		nodes: []treeNode{
			{kind: nodeCategory, label: "Rules", expanded: true, category: domain.CategoryRules, parentIdx: -1},
			{kind: nodeItem, label: "anti-slop", enabled: true, category: domain.CategoryRules, id: "anti-slop", packIdx: 0, parentIdx: 0, fileSize: -1},
		},
		cursor: 1,
		packs:  []app.ProfilePackInfo{{Index: 4, Name: "core"}},
	}
	m := New(context.Background(), nil, "/tmp/config")
	m.items = []profileItem{{
		name: "default",
		cfg:  config.ProfileConfig{Packs: []config.PackEntry{{Name: "core"}}},
		tree: &tree,
	}}
	m.focus = panelTree

	got := m.StatusHint()
	if !strings.Contains(got, "anti-slop from ") || !strings.Contains(got, packColorBright(4).Render("core")) {
		t.Fatalf("StatusHint() = %q, want selected item attribution with colored pack name", got)
	}

	m.focus = panelProfiles
	if got := m.StatusHint(); got != "" {
		t.Fatalf("StatusHint() outside tree focus = %q, want empty", got)
	}
}

func TestPackRosterTruncatesSelectedPackWithoutColorBleed(t *testing.T) {
	prevSelected := common.SelectedStyle
	common.SelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	t.Cleanup(func() { common.SelectedStyle = prevSelected })

	m := New(context.Background(), nil, "/tmp/config")
	m.items = []profileItem{{
		name: "default",
		cfg: config.ProfileConfig{Packs: []config.PackEntry{
			{Name: "standard-coding-rules-and-more"},
			{Name: "oci-dev-starter-pack"},
		}},
	}}
	m.focus = panelPacks
	m.packCursor = 0

	view := m.viewPackRoster(32, 12)
	var selectedLine, nextLine string
	for line := range strings.SplitSeq(view, "\n") {
		stripped := ansi.Strip(line)
		if strings.Contains(stripped, "standard-coding-rules") {
			selectedLine = line
		}
		if strings.Contains(stripped, "oci-dev-starter-pack") {
			nextLine = line
		}
	}
	if selectedLine == "" || nextLine == "" {
		t.Fatalf("expected selected and following pack lines, got:\n%s", view)
	}
	if !strings.Contains(selectedLine, "\x1b[0m") && !strings.Contains(selectedLine, "\x1b[m") {
		t.Fatalf("truncated selected pack line should reset ANSI style before newline: %q", selectedLine)
	}
	if !strings.Contains(nextLine, packColorBright(1).Render("oci-dev-starter-pack")) {
		t.Fatalf("following pack should keep its pack color, got line %q in view:\n%s", nextLine, view)
	}
}

func TestPackColorPaletteHasDistinctFirstScreenful(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for i := range 8 {
		rendered := packColorBright(i).Render("pack")
		if seen[rendered] {
			t.Fatalf("pack color repeated before 8 rows at index %d", i)
		}
		seen[rendered] = true
	}
}

func TestLayerHitSelectsProfileByMouseKind(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil, "/tmp/config")
	m.items = []profileItem{{name: "default"}, {name: "other"}}

	screen, cmd := m.Update(common.LayerHitMsg{
		ID:    "profiles:item:1",
		Mouse: tea.MouseMotionMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("motion should not trigger an action")
	}
	m = screen.(Model)
	if m.cursor != 0 {
		t.Fatalf("motion changed cursor to %d", m.cursor)
	}

	screen, _ = m.Update(common.LayerHitMsg{
		ID:    "profiles:item:1",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	m = screen.(Model)
	if m.cursor != 1 {
		t.Fatalf("click should select profile 1, got %d", m.cursor)
	}
}

func TestProfileLayerHitDoubleClickRequestsActions(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil, "/tmp/config")
	m.items = []profileItem{{name: "default"}, {name: "other"}}

	screen, cmd := m.Update(common.LayerHitMsg{
		ID:    "profiles:item:1",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("first click should only select")
	}
	m = screen.(Model)

	screen, cmd = m.Update(common.LayerHitMsg{
		ID:    "profiles:item:1",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	m = screen.(Model)
	if cmd == nil {
		t.Fatal("second click should request action menu")
	}
	if msg := cmd(); msg != (ActionRequestMsg{}) {
		t.Fatalf("expected ActionRequestMsg, got %T", msg)
	}
	if m.focus != panelProfiles || m.cursor != 1 {
		t.Fatal("double-click should keep profile row selected")
	}
}

func TestPackLayerHitTogglesCheckboxOnly(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil, "/tmp/config")
	m.items = []profileItem{{
		name: "default",
		cfg: config.ProfileConfig{Packs: []config.PackEntry{
			{Name: "core"},
			{Name: "tools"},
		}},
	}}
	m = m.SetSize(120, 40).(Model)
	packX := 2 + m.profilesMinWidth() + 3

	screen, _ := m.Update(common.LayerHitMsg{
		ID:    "profiles:pack:1",
		Mouse: tea.MouseClickMsg(tea.Mouse{X: packX + 6, Button: tea.MouseLeft}),
	})
	m = screen.(Model)
	if m.items[0].cfg.Packs[1].Enabled != nil {
		t.Fatal("label click should select without toggling pack")
	}
	if m.dirty || m.items[0].dirty {
		t.Fatal("label click should not dirty the profile")
	}

	screen, _ = m.Update(common.LayerHitMsg{
		ID:    "profiles:pack:1",
		Mouse: tea.MouseClickMsg(tea.Mouse{X: packX + 2, Button: tea.MouseLeft}),
	})
	m = screen.(Model)
	enabled := m.items[0].cfg.Packs[1].Enabled
	if enabled == nil || *enabled {
		t.Fatal("checkbox click should disable the pack")
	}
	if !m.dirty || !m.items[0].dirty {
		t.Fatal("checkbox click should dirty the profile")
	}
}

func TestPackLayerHitDoubleClickRequestsActions(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil, "/tmp/config")
	m.items = []profileItem{{
		name: "default",
		cfg:  config.ProfileConfig{Packs: []config.PackEntry{{Name: "core"}}},
	}}
	m = m.SetSize(120, 40).(Model)
	packX := 2 + m.profilesMinWidth() + 3

	screen, cmd := m.Update(common.LayerHitMsg{
		ID:    "profiles:pack:0",
		Mouse: tea.MouseClickMsg(tea.Mouse{X: packX + 6, Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("first click should only select")
	}
	m = screen.(Model)

	screen, cmd = m.Update(common.LayerHitMsg{
		ID:    "profiles:pack:0",
		Mouse: tea.MouseClickMsg(tea.Mouse{X: packX + 6, Button: tea.MouseLeft}),
	})
	m = screen.(Model)
	if cmd == nil {
		t.Fatal("second click should request action menu")
	}
	if msg := cmd(); msg != (ActionRequestMsg{}) {
		t.Fatalf("expected ActionRequestMsg, got %T", msg)
	}
	if m.focus != panelPacks || m.packCursor != 0 {
		t.Fatal("double-click should keep pack row selected")
	}
}

func TestTreeLayerHitTogglesCategoryRows(t *testing.T) {
	t.Parallel()

	tree := treeModel{
		nodes: []treeNode{
			{kind: nodeCategory, label: "Skills", expanded: true, category: domain.CategorySkills, parentIdx: -1},
			{kind: nodeItem, label: "aipack-system", enabled: true, category: domain.CategorySkills, id: "aipack-system", packIdx: 0, parentIdx: 0, fileSize: -1},
		},
		packs: []app.ProfilePackInfo{{Index: 0, Name: "core"}},
	}
	m := New(context.Background(), nil, "/tmp/config")
	m.items = []profileItem{{
		name: "default",
		cfg:  config.ProfileConfig{Packs: []config.PackEntry{{Name: "core"}}},
		tree: &tree,
	}}
	m = m.SetSize(120, 40).(Model)

	screen, cmd := m.Update(common.LayerHitMsg{
		ID:    "profiles:tree:0",
		Mouse: tea.MouseClickMsg(tea.Mouse{X: m.treePanelX() + 2, Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("category click should not emit a command")
	}
	m = screen.(Model)
	if m.items[0].tree.nodes[0].expanded {
		t.Fatal("category click should collapse expanded row")
	}
	if m.dirty || m.items[0].dirty {
		t.Fatal("category collapse should not dirty the profile")
	}
}

func TestTreeLayerHitTogglesItemCheckboxOnly(t *testing.T) {
	t.Parallel()

	tree := treeModel{
		nodes: []treeNode{
			{kind: nodeCategory, label: "Rules", expanded: true, category: domain.CategoryRules, parentIdx: -1},
			{kind: nodeItem, label: "anti-slop", enabled: true, category: domain.CategoryRules, id: "anti-slop", packIdx: 0, parentIdx: 0, fileSize: -1},
		},
		cursor: 1,
		packs:  []app.ProfilePackInfo{{Index: 0, Name: "core"}},
	}
	m := New(context.Background(), nil, "/tmp/config")
	m.items = []profileItem{{
		name: "default",
		cfg:  config.ProfileConfig{Packs: []config.PackEntry{{Name: "core"}}},
		tree: &tree,
	}}
	m = m.SetSize(120, 40).(Model)

	screen, _ := m.Update(common.LayerHitMsg{
		ID:    "profiles:tree:1",
		Mouse: tea.MouseClickMsg(tea.Mouse{X: m.treePanelX() + treeItemLabelStartX, Button: tea.MouseLeft}),
	})
	m = screen.(Model)
	if !m.items[0].tree.nodes[1].enabled {
		t.Fatal("label click should select without toggling content")
	}
	if m.dirty {
		t.Fatal("label click should not dirty the profile")
	}

	screen, cmd := m.Update(common.LayerHitMsg{
		ID:    "profiles:tree:1",
		Mouse: tea.MouseClickMsg(tea.Mouse{X: m.treePanelX() + treeItemCheckStartX, Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("checkbox click should not emit a command")
	}
	m = screen.(Model)
	if m.items[0].tree.nodes[1].enabled {
		t.Fatal("checkbox click should toggle content off")
	}
	if !m.dirty || !m.items[0].dirty {
		t.Fatal("checkbox click should dirty the profile")
	}
}

func TestTreeLayerHitDoubleClickRequestsActions(t *testing.T) {
	t.Parallel()

	tree := treeModel{
		nodes: []treeNode{
			{kind: nodeCategory, label: "Rules", expanded: true, category: domain.CategoryRules, parentIdx: -1},
			{kind: nodeItem, label: "anti-slop", enabled: true, category: domain.CategoryRules, id: "anti-slop", packIdx: 0, parentIdx: 0, fileSize: -1},
		},
		packs: []app.ProfilePackInfo{{Index: 0, Name: "core"}},
	}
	m := New(context.Background(), nil, "/tmp/config")
	m.items = []profileItem{{
		name: "default",
		cfg:  config.ProfileConfig{Packs: []config.PackEntry{{Name: "core"}}},
		tree: &tree,
	}}
	m = m.SetSize(120, 40).(Model)

	screen, cmd := m.Update(common.LayerHitMsg{
		ID:    "profiles:tree:1",
		Mouse: tea.MouseClickMsg(tea.Mouse{X: m.treePanelX() + treeItemLabelStartX, Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("first click should only select")
	}
	m = screen.(Model)

	screen, cmd = m.Update(common.LayerHitMsg{
		ID:    "profiles:tree:1",
		Mouse: tea.MouseClickMsg(tea.Mouse{X: m.treePanelX() + treeItemLabelStartX, Button: tea.MouseLeft}),
	})
	m = screen.(Model)
	if cmd == nil {
		t.Fatal("second click should request action menu")
	}
	if msg := cmd(); msg != (ActionRequestMsg{}) {
		t.Fatalf("expected ActionRequestMsg, got %T", msg)
	}
	if m.focus != panelTree || m.items[0].tree.cursor != 1 {
		t.Fatal("double-click should keep tree row selected")
	}
}

func TestTreeLayerHitMouseWheelScrollsTree(t *testing.T) {
	t.Parallel()

	nodes := []treeNode{{kind: nodeCategory, label: "Rules", expanded: true, category: domain.CategoryRules, parentIdx: -1}}
	for range 8 {
		nodes = append(nodes, treeNode{
			kind:      nodeItem,
			label:     "rule",
			enabled:   true,
			category:  domain.CategoryRules,
			id:        "rule",
			packIdx:   0,
			parentIdx: 0,
			fileSize:  -1,
		})
	}
	tree := treeModel{
		nodes:  nodes,
		cursor: 1,
		packs:  []app.ProfilePackInfo{{Index: 0, Name: "core"}},
	}
	m := New(context.Background(), nil, "/tmp/config")
	m.items = []profileItem{{
		name: "default",
		cfg:  config.ProfileConfig{Packs: []config.PackEntry{{Name: "core"}}},
		tree: &tree,
	}}
	m = m.SetSize(120, 8).(Model)

	screen, cmd := m.Update(common.LayerHitMsg{
		ID:    "profiles:tree:1",
		Mouse: tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}),
	})
	if cmd != nil {
		t.Fatal("wheel scroll should not emit a command")
	}
	m = screen.(Model)
	if m.focus != panelTree {
		t.Fatalf("focus = %d, want tree", m.focus)
	}
	if got := m.items[0].tree.cursor; got <= 1 {
		t.Fatalf("wheel down should move tree cursor, got %d", got)
	}

	screen, _ = m.Update(common.LayerHitMsg{
		ID:    "profiles:tree:4",
		Mouse: tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}),
	})
	m = screen.(Model)
	if got := m.items[0].tree.offset; got == 0 {
		t.Fatalf("repeated wheel down should advance tree offset in small viewport, got %d", got)
	}

	screen, _ = m.Update(common.LayerHitMsg{
		ID:    "profiles:tree:7",
		Mouse: tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}),
	})
	m = screen.(Model)
	if got := m.items[0].tree.cursor; got >= 7 {
		t.Fatalf("wheel up should move tree cursor upward, got %d", got)
	}
}

func TestProfilesTreeCursorWrapsAtBothEnds(t *testing.T) {
	t.Parallel()

	nodes := []treeNode{{kind: nodeCategory, label: "Rules", expanded: true, category: domain.CategoryRules, parentIdx: -1}}
	for range 8 {
		nodes = append(nodes, treeNode{
			kind:      nodeItem,
			label:     "rule",
			enabled:   true,
			category:  domain.CategoryRules,
			id:        "rule",
			packIdx:   0,
			parentIdx: 0,
			fileSize:  -1,
		})
	}
	lastVisible := len(nodes)
	nodes = append(nodes,
		treeNode{kind: nodeCategory, label: "Skills", expanded: false, category: domain.CategorySkills, parentIdx: -1},
		treeNode{kind: nodeItem, label: "hidden-skill", category: domain.CategorySkills, id: "hidden-skill", packIdx: 0, parentIdx: lastVisible, fileSize: -1},
	)
	tree := treeModel{
		nodes: nodes,
		packs: []app.ProfilePackInfo{{Index: 0, Name: "core"}},
	}
	m := New(context.Background(), nil, "/tmp/config")
	m.focus = panelTree
	m.items = []profileItem{{
		name: "default",
		cfg:  config.ProfileConfig{Packs: []config.PackEntry{{Name: "core"}}},
		tree: &tree,
	}}
	m = m.SetSize(120, 8).(Model)

	screen, cmd := m.Update(testKeyText("k"))
	if cmd != nil {
		t.Fatal("tree cursor motion should not emit a command")
	}
	m = screen.(Model)
	if got, want := m.items[0].tree.cursor, lastVisible; got != want {
		t.Fatalf("tree cursor = %d, want last visible node at %d", got, want)
	}
	if got := m.items[0].tree.offset; got == 0 {
		t.Fatalf("wrapped tree cursor should move viewport to the bottom, offset = %d", got)
	}

	screen, cmd = m.Update(testKeyText("j"))
	if cmd != nil {
		t.Fatal("tree cursor motion should not emit a command")
	}
	m = screen.(Model)
	if got := m.items[0].tree.cursor; got != 0 {
		t.Fatalf("tree cursor = %d, want first visible node at 0", got)
	}
	if got := m.items[0].tree.offset; got != 0 {
		t.Fatalf("wrapped tree cursor should move viewport to the top, offset = %d", got)
	}
}
