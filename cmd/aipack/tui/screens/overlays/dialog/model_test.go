package dialog

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
)

func TestViewHitLayersDoNotAlterRenderedDialog(t *testing.T) {
	t.Parallel()

	m := NewConfirm("confirm", "Save changes?")
	got := stripSGR(lipgloss.NewCompositor(m.View()).Render())
	want := stripSGR(m.Render())
	if got != want {
		t.Fatalf("composited view changed rendered dialog\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestListSelectItemClickConfirmsRenderedItem(t *testing.T) {
	t.Parallel()

	m := NewListSelect("actions", "Actions:", []string{"Open file", "Edit file"})
	comp := lipgloss.NewCompositor(m.View())
	lines := strings.Split(stripSGR(comp.Render()), "\n")
	x, y := findRenderedText(t, lines, "Edit file")
	id := comp.Hit(x, y).ID()

	next, cmd := m.UpdateModel(common.LayerHitMsg{
		ID:    id,
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd == nil {
		t.Fatalf("expected item click to confirm selection; Hit(%d,%d) = %q\n%s", x, y, id, strings.Join(lines, "\n"))
	}
	result, ok := cmd().(ResultMsg)
	if !ok {
		t.Fatalf("expected ResultMsg, got %T", cmd())
	}
	if !result.Confirmed || result.Value != "Edit file" {
		t.Fatalf("expected confirmed Edit file, got confirmed=%v value=%q", result.Confirmed, result.Value)
	}
	if next.ListCursor != 1 {
		t.Fatalf("expected clicked item cursor 1, got %d", next.ListCursor)
	}
}

func TestListSelectItemHoverMovesCursorWithoutConfirming(t *testing.T) {
	t.Parallel()

	m := NewListSelect("actions", "Actions:", []string{"Open file", "Edit file", "Preview update"})
	comp := lipgloss.NewCompositor(m.View())
	lines := strings.Split(stripSGR(comp.Render()), "\n")
	x, y := findRenderedText(t, lines, "Preview update")
	id := comp.Hit(x, y).ID()

	next, cmd := m.UpdateModel(common.LayerHitMsg{
		ID:    id,
		Mouse: tea.MouseMotionMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("hover should move cursor without confirming selection")
	}
	if next.ListCursor != 2 {
		t.Fatalf("expected hovered item cursor 2, got %d", next.ListCursor)
	}
}

func TestChecklistItemHoverMovesCursorWithoutToggling(t *testing.T) {
	t.Parallel()

	m := NewChecklist("actions", "Actions:", []CheckItem{
		{Label: "profiles"},
		{Label: "registries"},
	})
	comp := lipgloss.NewCompositor(m.View())
	lines := strings.Split(stripSGR(comp.Render()), "\n")
	x, y := findRenderedText(t, lines, "registries")
	id := comp.Hit(x, y).ID()

	next, cmd := m.UpdateModel(common.LayerHitMsg{
		ID:    id,
		Mouse: tea.MouseMotionMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("hover should move cursor without confirming checklist")
	}
	if next.ListCursor != 1 {
		t.Fatalf("expected hovered item cursor 1, got %d", next.ListCursor)
	}
	if next.CheckItems[1].Checked {
		t.Fatal("hover should not toggle checklist item")
	}
}

func TestListSelectRenderIsHeightBoundedAndKeepsCursorVisible(t *testing.T) {
	t.Parallel()

	items := []string{"zero", "one", "two", "three", "four", "five", "six", "seven"}
	m := NewListSelect("profiles", "Select profile:", items).SetSizeModel(80, 10)
	m.ListCursor = 7

	rendered := stripSGR(m.Render())
	if lipgloss.Height(rendered) > 10 {
		t.Fatalf("expected rendered height <= 10, got %d:\n%s", lipgloss.Height(rendered), rendered)
	}
	if strings.Contains(rendered, "zero") {
		t.Fatalf("expected top item to scroll out of bounded dialog:\n%s", rendered)
	}
	if !strings.Contains(rendered, "> seven") {
		t.Fatalf("expected cursor item visible:\n%s", rendered)
	}

	comp := lipgloss.NewCompositor(m.View())
	if comp.GetLayer("dialog:item:7") == nil {
		t.Fatal("expected visible cursor item layer")
	}
	if comp.GetLayer("dialog:item:0") != nil {
		t.Fatal("did not expect hidden item layer")
	}
}

func TestChecklistRenderIsHeightBoundedAndKeepsCursorVisible(t *testing.T) {
	t.Parallel()

	items := []CheckItem{
		{Label: "cline"},
		{Label: "claudecode"},
		{Label: "codex", Checked: true},
		{Label: "opencode"},
		{Label: "future"},
	}
	m := NewChecklist("harnesses", "Select harnesses:", items).SetSizeModel(80, 9)
	m.ListCursor = 4

	rendered := stripSGR(m.Render())
	if lipgloss.Height(rendered) > 9 {
		t.Fatalf("expected rendered height <= 9, got %d:\n%s", lipgloss.Height(rendered), rendered)
	}
	if strings.Contains(rendered, "cline") {
		t.Fatalf("expected first checklist item to scroll out:\n%s", rendered)
	}
	if !strings.Contains(rendered, "> [ ] future") {
		t.Fatalf("expected cursor checklist item visible:\n%s", rendered)
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

var sgrPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripSGR(s string) string {
	return sgrPattern.ReplaceAllString(s, "")
}
