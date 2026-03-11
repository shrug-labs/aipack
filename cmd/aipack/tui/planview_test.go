package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shrug-labs/aipack/internal/app"
)

func TestPlanView_VKeyOpensPlanView(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.activeTab = tabProfiles
	m.width = 120
	m.height = 40
	m.profiles.items = []profileItem{
		{
			name: "test",
			syncTarget: syncTargetInfo{
				PlanSummary: app.PlanSummary{Ops: []app.PlanOp{
					{Kind: app.PlanOpWrite, Dst: "/tmp/out/rules/r.md", SourcePack: "my-pack", Size: 100},
				}},
				projectDir: "/tmp/out",
			},
		},
	}

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	rm := result.(rootModel)
	if rm.planView == nil {
		t.Fatal("expected planView to be opened on 'v' key")
	}
	if rm.planView.profileName != "test" {
		t.Fatalf("expected profileName 'test', got %q", rm.planView.profileName)
	}
}

func TestPlanView_VKeyNoOps(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.activeTab = tabProfiles
	m.profiles.items = []profileItem{
		{name: "test", syncTarget: syncTargetInfo{}},
	}

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	rm := result.(rootModel)
	if rm.planView != nil {
		t.Fatal("expected planView to remain nil when no plan ops")
	}
	if !strings.Contains(rm.statusText, "no pending") {
		t.Fatalf("expected status text about no pending changes, got %q", rm.statusText)
	}
}

func TestPlanView_EscCloses(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.width = 120
	m.height = 40
	ops := []app.PlanOp{{Kind: app.PlanOpWrite, Dst: "/tmp/file.md", Size: 50}}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)
	m.planView = &pv

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	rm := result.(rootModel)
	if rm.planView != nil {
		t.Fatal("expected planView to be closed on esc")
	}
}

func TestPlanView_QCloses(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.width = 120
	m.height = 40
	ops := []app.PlanOp{{Kind: app.PlanOpWrite, Dst: "/tmp/file.md", Size: 50}}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)
	m.planView = &pv

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	rm := result.(rootModel)
	if rm.planView != nil {
		t.Fatal("expected planView to be closed on q")
	}
}

func TestPlanView_ViewTakesOver(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.width = 120
	m.height = 40
	ops := []app.PlanOp{
		{Kind: app.PlanOpWrite, Dst: "/tmp/project/rules/r.md", SourcePack: "my-pack", Size: 100},
		{Kind: app.PlanOpCopy, Dst: "/tmp/project/agents/a.md", SourcePack: "my-pack"},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp/project", ops, false)
	m.planView = &pv

	view := m.View()
	if !strings.Contains(view, "Sync Plan") {
		t.Fatalf("expected view to contain 'Sync Plan', got:\n%s", view)
	}
	if !strings.Contains(view, "2 operations") {
		t.Fatalf("expected view to contain '2 operations', got:\n%s", view)
	}
}

func TestPlanView_GroupsOperations(t *testing.T) {
	t.Parallel()
	ops := []app.PlanOp{
		{Kind: app.PlanOpWrite, Dst: "/tmp/project/r.md", SourcePack: "pack-a", Size: 100},
		{Kind: app.PlanOpWrite, Dst: "/tmp/project/a.md", SourcePack: "pack-a", Size: 200},
		{Kind: app.PlanOpCopy, Dst: "/tmp/project/w.md", SourcePack: "pack-b"},
		{Kind: app.PlanOpSettings, Dst: "/home/.config/settings.json", SourcePack: "pack-a", Size: 50},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp/project", ops, false)
	view := pv.View()

	if !strings.Contains(view, "Writes (2)") {
		t.Fatalf("expected view to contain 'Writes (2)', got:\n%s", view)
	}
	if !strings.Contains(view, "Copies (1)") {
		t.Fatalf("expected view to contain 'Copies (1)', got:\n%s", view)
	}
	if !strings.Contains(view, "Settings (1)") {
		t.Fatalf("expected view to contain 'Settings (1)', got:\n%s", view)
	}
}

func TestPlanView_ShortDst(t *testing.T) {
	t.Parallel()
	pv := planViewModel{projectDir: "/tmp/project"}

	tests := []struct {
		input    string
		expected string
	}{
		{"/tmp/project/rules/r.md", "./rules/r.md"},
		{"/etc/config", "/etc/config"},
	}
	for _, tt := range tests {
		got := pv.shortDst(tt.input)
		if got != tt.expected {
			t.Errorf("shortDst(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestPlanView_SyncTabAlsoOpens(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.activeTab = tabSync
	m.width = 120
	m.height = 40
	m.profiles.items = []profileItem{{
		name:     "test",
		isActive: true,
		syncTarget: syncTargetInfo{
			PlanSummary: app.PlanSummary{Ops: []app.PlanOp{
				{Kind: app.PlanOpWrite, Dst: "/tmp/file.md", Size: 100},
			}},
			projectDir: "/tmp",
		},
	}}

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	rm := result.(rootModel)
	if rm.planView == nil {
		t.Fatal("expected planView to open from sync tab")
	}
}

func TestPlanView_EmptyOpsMessage(t *testing.T) {
	t.Parallel()
	pv := newPlanViewModel(120, 40, "test", "/tmp", nil, false)
	view := pv.View()
	if !strings.Contains(view, "up to date") {
		t.Fatalf("expected 'up to date' message for empty plan, got:\n%s", view)
	}
}

func TestPlanView_HelpText(t *testing.T) {
	t.Parallel()
	pv := planViewModel{}
	help := pv.helpText()
	if !strings.Contains(help, "j/k:navigate") {
		t.Fatalf("expected help to contain 'j/k:navigate', got %q", help)
	}
	if !strings.Contains(help, "enter:diff") {
		t.Fatalf("expected help to contain 'enter:diff', got %q", help)
	}
	if !strings.Contains(help, "esc:close") {
		t.Fatalf("expected help to contain 'esc:close', got %q", help)
	}
}

func TestSyncTargetInfo_PlanOpsPopulated(t *testing.T) {
	t.Parallel()
	target := syncTargetInfo{PlanSummary: app.PlanSummary{
		NumWrites:   2,
		NumCopies:   1,
		NumSettings: 1,
		Ops: []app.PlanOp{
			{Kind: app.PlanOpWrite, Dst: "/a"},
			{Kind: app.PlanOpWrite, Dst: "/b"},
			{Kind: app.PlanOpCopy, Dst: "/c"},
			{Kind: app.PlanOpSettings, Dst: "/d"},
		},
	}}
	if len(target.Ops) != 4 {
		t.Fatalf("expected 4 planOps, got %d", len(target.Ops))
	}
	if target.TotalChanges() != 4 {
		t.Fatalf("expected totalChanges=4, got %d", target.TotalChanges())
	}
}

func TestPlanView_CursorNavigation(t *testing.T) {
	t.Parallel()
	ops := []app.PlanOp{
		{Kind: app.PlanOpWrite, Dst: "/tmp/a.md", Size: 100, Content: []byte("a")},
		{Kind: app.PlanOpWrite, Dst: "/tmp/b.md", Size: 200, Content: []byte("b")},
		{Kind: app.PlanOpCopy, Dst: "/tmp/c.md", Src: "/src/c.md"},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)

	// Cursor should start on first non-header item.
	if pv.items[pv.cursor].isHeader {
		t.Fatal("cursor should not be on a header")
	}
	initial := pv.cursor

	// Press j — should move to next item.
	pv, _ = pv.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if pv.cursor <= initial {
		t.Fatalf("expected cursor to advance past %d, got %d", initial, pv.cursor)
	}
	if pv.items[pv.cursor].isHeader {
		t.Fatal("cursor should skip headers")
	}

	// Press k — should move back.
	prev := pv.cursor
	pv, _ = pv.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if pv.cursor >= prev {
		t.Fatalf("expected cursor to go back from %d, got %d", prev, pv.cursor)
	}
}

func TestPlanView_CursorSkipsHeaders(t *testing.T) {
	t.Parallel()
	// Two groups: writes and copies. Cursor should skip both headers.
	ops := []app.PlanOp{
		{Kind: app.PlanOpWrite, Dst: "/tmp/a.md", Size: 10, Content: []byte("a")},
		{Kind: app.PlanOpCopy, Dst: "/tmp/b.md", Src: "/src/b.md"},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)

	// Navigate through all items.
	for i := 0; i < 10; i++ {
		pv, _ = pv.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		if pv.items[pv.cursor].isHeader {
			t.Fatalf("cursor landed on header at index %d after %d j presses", pv.cursor, i+1)
		}
	}
}

func TestPlanView_EnterOpensDiffView(t *testing.T) {
	t.Parallel()
	ops := []app.PlanOp{
		{Kind: app.PlanOpWrite, Dst: "/tmp/a.md", Size: 10, Content: []byte("hello")},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)

	// Press enter.
	pv, cmd := pv.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if pv.diffView == nil {
		t.Fatal("expected diffView to be created on enter")
	}
	if cmd == nil {
		t.Fatal("expected a command to be returned for async diff loading")
	}
}

func TestPlanView_DiffLoadedSetsContent(t *testing.T) {
	t.Parallel()
	ops := []app.PlanOp{
		{Kind: app.PlanOpWrite, Dst: "/tmp/a.md", Size: 10, Content: []byte("hello")},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)

	// Open diff view.
	dv := newDiffViewModel(120, 40, "a.md", "./a.md")
	pv.diffView = &dv

	// Send diffLoadedMsg.
	pv, _ = pv.Update(diffLoadedMsg{
		dst:      "/tmp/a.md",
		title:    "a.md",
		diffText: "--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new\n",
	})
	if pv.diffView == nil {
		t.Fatal("diffView should still be open")
	}
	if !pv.diffView.ready {
		t.Fatal("diffView should be ready after receiving content")
	}
}

func TestPlanView_EscClosesDiffNotPlan(t *testing.T) {
	t.Parallel()
	m := newRootModel(RunConfig{})
	m.width = 120
	m.height = 40
	ops := []app.PlanOp{{Kind: app.PlanOpWrite, Dst: "/tmp/a.md", Size: 10, Content: []byte("a")}}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)
	dv := newDiffViewModel(120, 40, "a.md", "./a.md")
	dv.setContent("--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new\n", false, "", "")
	pv.diffView = &dv
	m.planView = &pv

	// Esc should close the diff view, not the plan view.
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	rm := result.(rootModel)
	if rm.planView == nil {
		t.Fatal("plan view should still be open")
	}
	if rm.planView.diffView != nil {
		t.Fatal("diff view should be closed")
	}
}

func TestPlanView_DiffNewFile(t *testing.T) {
	t.Parallel()
	ops := []app.PlanOp{
		{Kind: app.PlanOpWrite, Dst: "/tmp/new.md", Size: 5, Content: []byte("hello")},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)
	dv := newDiffViewModel(120, 40, "new.md", "./new.md")
	pv.diffView = &dv

	// Simulate new file response.
	pv, _ = pv.Update(diffLoadedMsg{
		dst:     "/tmp/new.md",
		title:   "new.md",
		isNew:   true,
		newBody: "hello",
	})
	if !pv.diffView.isNew {
		t.Fatal("expected diff view to be marked as new file")
	}
	view := pv.diffView.View()
	if !strings.Contains(view, "New file") {
		t.Fatalf("expected 'New file' in view, got:\n%s", view)
	}
}

func TestPlanView_DiffError(t *testing.T) {
	t.Parallel()
	ops := []app.PlanOp{
		{Kind: app.PlanOpWrite, Dst: "/tmp/a.md", Size: 10, Content: []byte("a")},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)
	dv := newDiffViewModel(120, 40, "a.md", "./a.md")
	pv.diffView = &dv

	pv, _ = pv.Update(diffLoadedMsg{
		dst:   "/tmp/a.md",
		title: "a.md",
		err:   fmt.Errorf("permission denied"),
	})
	view := pv.diffView.View()
	if !strings.Contains(view, "permission denied") {
		t.Fatalf("expected error message in view, got:\n%s", view)
	}
}

func TestPlanView_HelpTextWithDiff(t *testing.T) {
	t.Parallel()
	pv := planViewModel{}
	if pv.helpText() != "j/k:navigate  enter:diff  e:edit  esc:close" {
		t.Fatalf("unexpected help text without diff: %q", pv.helpText())
	}
	dv := newDiffViewModel(120, 40, "a.md", "./a.md")
	pv.diffView = &dv
	if pv.helpText() != "j/k:scroll  esc:back" {
		t.Fatalf("unexpected help text with diff: %q", pv.helpText())
	}
}

func TestPlanView_DiffViewRendersColorizedDiff(t *testing.T) {
	t.Parallel()
	dv := newDiffViewModel(120, 40, "test.md", "./test.md")
	dv.setContent("--- current\n+++ desired\n@@ -1,2 +1,2 @@\n-old line\n+new line\n context\n", false, "", "")

	view := dv.View()
	// View should contain the diff text (rendered through lipgloss).
	if !strings.Contains(view, "Diff: test.md") {
		t.Fatalf("expected title in view, got:\n%s", view)
	}
	// Verify the view contains some content (exact styled text is hard to match).
	if !strings.Contains(view, "old line") {
		t.Fatalf("expected diff content in view, got:\n%s", view)
	}
}

func TestPlanView_DiffIdenticalContent(t *testing.T) {
	t.Parallel()
	dv := newDiffViewModel(120, 40, "same.md", "./same.md")
	dv.setContent("", false, "", "")

	view := dv.View()
	if !strings.Contains(view, "identical") {
		t.Fatalf("expected 'identical' message for empty diff, got:\n%s", view)
	}
}
