package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	profilescreen "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/profiles"
	"github.com/shrug-labs/aipack/internal/app"
)

func TestPlanView_VKeyOpensPlanView(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabProfiles
	m.width = 120
	m.height = 40
	m = seedProfiles(m, profilescreen.Seed{
		Name: "test",
		SyncTarget: common.SyncTarget{
			PlanSummary: app.PlanSummary{Ops: []app.PlanOp{
				{Kind: app.PlanOpRule, Dst: "/tmp/out/rules/r.md", SourcePack: "my-pack", Size: 100},
			}},
			ProjectDir: "/tmp/out",
		},
	})

	result, _ := m.Update(testKeyText("v"))
	rm := result.(rootModel)
	if rm.planView == nil {
		t.Fatal("expected planView to be opened on 'v' key")
	}
	if rm.planView.ProfileName != "test" {
		t.Fatalf("expected profileName 'test', got %q", rm.planView.ProfileName)
	}
}

func TestPlanView_VKeyNoOps(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabProfiles
	m = seedProfiles(m, profilescreen.Seed{Name: "test", SyncTarget: common.SyncTarget{}})

	result, _ := m.Update(testKeyText("v"))
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
	m := newRootModel(context.Background(), RunConfig{})
	m.width = 120
	m.height = 40
	ops := []app.PlanOp{{Kind: app.PlanOpRule, Dst: "/tmp/file.md", Size: 50}}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)
	m.planView = &pv

	result, _ := m.Update(testKeyCode(tea.KeyEsc))
	rm := result.(rootModel)
	if rm.planView != nil {
		t.Fatal("expected planView to be closed on esc")
	}
}

func TestPlanView_QCloses(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.width = 120
	m.height = 40
	ops := []app.PlanOp{{Kind: app.PlanOpRule, Dst: "/tmp/file.md", Size: 50}}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)
	m.planView = &pv

	result, _ := m.Update(testKeyText("q"))
	rm := result.(rootModel)
	if rm.planView != nil {
		t.Fatal("expected planView to be closed on q")
	}
}

func TestPlanView_CtrlCQuitsFromPlanView(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.width = 120
	m.height = 40
	ops := []app.PlanOp{{Kind: app.PlanOpRule, Dst: "/tmp/file.md", Size: 50}}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)
	m.planView = &pv

	result, cmd := m.Update(testKeyCtrl('c'))
	rm := result.(rootModel)
	if !rm.quitting {
		t.Fatal("expected ctrl+c to quit from plan view")
	}
	if cmd == nil {
		t.Fatal("expected quit command")
	}
}

func TestPlanView_CtrlCQuitsFromDiffError(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.width = 120
	m.height = 40
	ops := []app.PlanOp{{Kind: app.PlanOpRule, Dst: "/tmp/file.md", Size: 50}}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)
	dv := newDiffViewModel(120, 40, "file.md", "./file.md")
	dv.SetContent("", false, "", "read /tmp/file.md: is a directory")
	pv.DiffView = &dv
	m.planView = &pv

	result, cmd := m.Update(testKeyCtrl('c'))
	rm := result.(rootModel)
	if !rm.quitting {
		t.Fatal("expected ctrl+c to quit from diff error view")
	}
	if cmd == nil {
		t.Fatal("expected quit command")
	}
}

func TestPlanView_ViewTakesOver(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.width = 120
	m.height = 40
	ops := []app.PlanOp{
		{Kind: app.PlanOpRule, Dst: "/tmp/project/rules/r.md", SourcePack: "my-pack", Size: 100},
		{Kind: app.PlanOpSkill, Dst: "/tmp/project/agents/a.md", SourcePack: "my-pack"},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp/project", ops, false)
	m.planView = &pv

	view := m.View().Content
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
		{Kind: app.PlanOpRule, Dst: "/tmp/project/r.md", SourcePack: "pack-a", Size: 100},
		{Kind: app.PlanOpRule, Dst: "/tmp/project/a.md", SourcePack: "pack-a", Size: 200},
		{Kind: app.PlanOpSkill, Dst: "/tmp/project/w.md", SourcePack: "pack-b"},
		{Kind: app.PlanOpSettings, Dst: "/home/.config/settings.json", SourcePack: "pack-a", Size: 50},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp/project", ops, false)
	view := pv.Render()

	if !strings.Contains(view, "Rules (2)") {
		t.Fatalf("expected view to contain 'Rules (2)', got:\n%s", view)
	}
	if !strings.Contains(view, "Skills (1)") {
		t.Fatalf("expected view to contain 'Skills (1)', got:\n%s", view)
	}
	if !strings.Contains(view, "Settings (1)") {
		t.Fatalf("expected view to contain 'Settings (1)', got:\n%s", view)
	}
}

func TestPlanView_ShortDst(t *testing.T) {
	t.Parallel()
	pv := planViewModel{ProjectDir: "/tmp/project"}

	tests := []struct {
		input    string
		expected string
	}{
		{"/tmp/project/rules/r.md", "./rules/r.md"},
		{"/etc/config", "/etc/config"},
	}
	for _, tt := range tests {
		got := pv.ShortDst(tt.input)
		if got != tt.expected {
			t.Errorf("shortDst(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestPlanView_SyncTabAlsoOpens(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabSync
	m.width = 120
	m.height = 40
	m = seedProfiles(m, profilescreen.Seed{
		Name:     "test",
		IsActive: true,
		SyncTarget: common.SyncTarget{
			PlanSummary: app.PlanSummary{Ops: []app.PlanOp{
				{Kind: app.PlanOpRule, Dst: "/tmp/file.md", Size: 100},
			}},
			ProjectDir: "/tmp",
		},
	})

	result, _ := m.Update(testKeyText("v"))
	rm := result.(rootModel)
	if rm.planView == nil {
		t.Fatal("expected planView to open from sync tab")
	}
}

func TestPlanView_SyncDiffRequestOpensDiff(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.width = 120
	m.height = 40

	result, cmd := m.Update(common.SyncDiffRequestMsg{
		ProfileName: "test",
		ProjectDir:  "/tmp",
		Ops: []app.PlanOp{{
			Kind:       app.PlanOpRule,
			Dst:        "/tmp/file.md",
			SourcePack: "pack",
			Content:    []byte("hello"),
		}},
		Cursor: 1,
	})
	rm := result.(rootModel)
	if rm.planView == nil {
		t.Fatal("expected planView to open from sync diff request")
	}
	if rm.planView.DiffView == nil {
		t.Fatal("expected sync diff request to open nested diff view")
	}
	if cmd == nil {
		t.Fatal("expected diff load command")
	}
}

func TestPlanView_SyncDiffRequestBackClosesToSyncTab(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabSync
	m.width = 120
	m.height = 40

	result, _ := m.Update(common.SyncDiffRequestMsg{
		ProfileName: "test",
		ProjectDir:  "/tmp",
		Ops: []app.PlanOp{{
			Kind:    app.PlanOpRule,
			Dst:     "/tmp/file.md",
			Content: []byte("hello"),
		}},
		Cursor: 1,
	})
	rm := result.(rootModel)
	if rm.planView == nil || rm.planView.DiffView == nil {
		t.Fatal("expected direct sync diff to open")
	}

	result, _ = rm.Update(testKeyCode(tea.KeyEsc))
	rm = result.(rootModel)
	if rm.planView != nil {
		t.Fatal("expected esc/back from direct sync diff to close all the way to the sync tab")
	}
	if rm.activeTab != tabSync {
		t.Fatalf("expected to remain on sync tab, got %v", rm.activeTab)
	}
}

func TestPlanView_SyncDiffRequestBackLayerClosesToSyncTab(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabSync
	m.width = 120
	m.height = 40

	result, _ := m.Update(common.SyncDiffRequestMsg{
		ProfileName: "test",
		ProjectDir:  "/tmp",
		Ops: []app.PlanOp{{
			Kind:    app.PlanOpSkill,
			Dst:     "/tmp/skill.md",
			Content: []byte("hello"),
		}},
		Cursor: 1,
	})
	rm := result.(rootModel)
	if rm.planView == nil || rm.planView.DiffView == nil {
		t.Fatal("expected direct sync diff to open")
	}

	result, _ = rm.Update(common.LayerHitMsg{
		ID:    "planview:diff:close",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	rm = result.(rootModel)
	if rm.planView != nil {
		t.Fatal("expected diff back layer from direct sync diff to close all the way to the sync tab")
	}
	if rm.activeTab != tabSync {
		t.Fatalf("expected to remain on sync tab, got %v", rm.activeTab)
	}
}

func TestPlanView_EmptyOpsMessage(t *testing.T) {
	t.Parallel()
	pv := newPlanViewModel(120, 40, "test", "/tmp", nil, false)
	view := pv.Render()
	if !strings.Contains(view, "up to date") {
		t.Fatalf("expected 'up to date' message for empty plan, got:\n%s", view)
	}
}

func TestPlanView_HelpText(t *testing.T) {
	t.Parallel()
	pv := planViewModel{}
	help := pv.HelpText()
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
	target := common.SyncTarget{PlanSummary: app.PlanSummary{
		NumRules:    2,
		NumSkills:   1,
		NumSettings: 1,
		Ops: []app.PlanOp{
			{Kind: app.PlanOpRule, Dst: "/a"},
			{Kind: app.PlanOpRule, Dst: "/b"},
			{Kind: app.PlanOpSkill, Dst: "/c"},
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
		{Kind: app.PlanOpRule, Dst: "/tmp/a.md", Size: 100, Content: []byte("a")},
		{Kind: app.PlanOpRule, Dst: "/tmp/b.md", Size: 200, Content: []byte("b")},
		{Kind: app.PlanOpSkill, Dst: "/tmp/c.md", Src: "/src/c.md"},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)

	// Cursor should start on first non-header item.
	if pv.Items[pv.Cursor].IsHeader {
		t.Fatal("cursor should not be on a header")
	}
	initial := pv.Cursor

	// Press j — should move to next item.
	pv, _ = pv.UpdateModel(testKeyText("j"))
	if pv.Cursor <= initial {
		t.Fatalf("expected cursor to advance past %d, got %d", initial, pv.Cursor)
	}
	if pv.Items[pv.Cursor].IsHeader {
		t.Fatal("cursor should skip headers")
	}

	// Press k — should move back.
	prev := pv.Cursor
	pv, _ = pv.UpdateModel(testKeyText("k"))
	if pv.Cursor >= prev {
		t.Fatalf("expected cursor to go back from %d, got %d", prev, pv.Cursor)
	}
}

func TestPlanView_CursorSkipsHeaders(t *testing.T) {
	t.Parallel()
	// Two groups: writes and copies. Cursor should skip both headers.
	ops := []app.PlanOp{
		{Kind: app.PlanOpRule, Dst: "/tmp/a.md", Size: 10, Content: []byte("a")},
		{Kind: app.PlanOpSkill, Dst: "/tmp/b.md", Src: "/src/b.md"},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)

	// Navigate through all items.
	for i := range 10 {
		pv, _ = pv.UpdateModel(testKeyText("j"))
		if pv.Items[pv.Cursor].IsHeader {
			t.Fatalf("cursor landed on header at index %d after %d j presses", pv.Cursor, i+1)
		}
	}
}

func TestPlanView_EnterOpensDiffView(t *testing.T) {
	t.Parallel()
	ops := []app.PlanOp{
		{Kind: app.PlanOpRule, Dst: "/tmp/a.md", Size: 10, Content: []byte("hello")},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)

	// Press enter.
	pv, cmd := pv.UpdateModel(testKeyCode(tea.KeyEnter))
	if pv.DiffView == nil {
		t.Fatal("expected diffView to be created on enter")
	}
	if cmd == nil {
		t.Fatal("expected a command to be returned for async diff loading")
	}
}

func TestPlanView_DiffLoadedSetsContent(t *testing.T) {
	t.Parallel()
	ops := []app.PlanOp{
		{Kind: app.PlanOpRule, Dst: "/tmp/a.md", Size: 10, Content: []byte("hello")},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)

	// Open diff view.
	dv := newDiffViewModel(120, 40, "a.md", "./a.md")
	pv.DiffView = &dv

	// Send diffLoadedMsg.
	pv, _ = pv.UpdateModel(diffLoadedMsg{
		Dst:      "/tmp/a.md",
		Title:    "a.md",
		DiffText: "--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new\n",
	})
	if pv.DiffView == nil {
		t.Fatal("diffView should still be open")
	}
	if !pv.DiffView.Ready {
		t.Fatal("diffView should be ready after receiving content")
	}
}

func TestPlanView_EscClosesDiffNotPlan(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.width = 120
	m.height = 40
	ops := []app.PlanOp{{Kind: app.PlanOpRule, Dst: "/tmp/a.md", Size: 10, Content: []byte("a")}}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)
	dv := newDiffViewModel(120, 40, "a.md", "./a.md")
	dv.SetContent("--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new\n", false, "", "")
	pv.DiffView = &dv
	m.planView = &pv

	// Esc should close the diff view, not the plan view.
	result, _ := m.Update(testKeyCode(tea.KeyEsc))
	rm := result.(rootModel)
	if rm.planView == nil {
		t.Fatal("plan view should still be open")
	}
	if rm.planView.DiffView != nil {
		t.Fatal("diff view should be closed")
	}
}

func TestPlanView_DiffNewFile(t *testing.T) {
	t.Parallel()
	ops := []app.PlanOp{
		{Kind: app.PlanOpRule, Dst: "/tmp/new.md", Size: 5, Content: []byte("hello")},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)
	dv := newDiffViewModel(120, 40, "new.md", "./new.md")
	pv.DiffView = &dv

	// Simulate new file response.
	pv, _ = pv.UpdateModel(diffLoadedMsg{
		Dst:     "/tmp/new.md",
		Title:   "new.md",
		IsNew:   true,
		NewBody: "hello",
	})
	if !pv.DiffView.IsNew {
		t.Fatal("expected diff view to be marked as new file")
	}
	view := pv.DiffView.View()
	if !strings.Contains(view, "New file") {
		t.Fatalf("expected 'New file' in view, got:\n%s", view)
	}
}

func TestPlanView_DiffError(t *testing.T) {
	t.Parallel()
	ops := []app.PlanOp{
		{Kind: app.PlanOpRule, Dst: "/tmp/a.md", Size: 10, Content: []byte("a")},
	}
	pv := newPlanViewModel(120, 40, "test", "/tmp", ops, false)
	dv := newDiffViewModel(120, 40, "a.md", "./a.md")
	pv.DiffView = &dv

	pv, _ = pv.UpdateModel(diffLoadedMsg{
		Dst:   "/tmp/a.md",
		Title: "a.md",
		Err:   fmt.Errorf("permission denied"),
	})
	view := pv.DiffView.View()
	if !strings.Contains(view, "permission denied") {
		t.Fatalf("expected error message in view, got:\n%s", view)
	}
}

func TestPlanView_HelpTextWithDiff(t *testing.T) {
	t.Parallel()
	pv := planViewModel{}
	if pv.HelpText() != "j/k:navigate  enter:diff  e:edit  esc:close" {
		t.Fatalf("unexpected help text without diff: %q", pv.HelpText())
	}
	dv := newDiffViewModel(120, 40, "a.md", "./a.md")
	pv.DiffView = &dv
	if pv.HelpText() != "j/k:scroll  esc:back" {
		t.Fatalf("unexpected help text with diff: %q", pv.HelpText())
	}
}

func TestPlanView_DiffViewRendersColorizedDiff(t *testing.T) {
	t.Parallel()
	dv := newDiffViewModel(120, 40, "test.md", "./test.md")
	dv.SetContent("--- current\n+++ desired\n@@ -1,2 +1,2 @@\n-old line\n+new line\n context\n", false, "", "")

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

func TestPlanView_DiffOverlayDoesNotCoverHelpBar(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	result, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = result.(rootModel)

	pv := newPlanViewModel(80, 11, "default", "/tmp/project", []app.PlanOp{
		{Kind: app.PlanOpRule, Dst: "/tmp/project/rules/long.md", SourcePack: "core"},
	}, false)
	dv := newDiffViewModel(80, 11, "long.md", "./rules/long.md")
	var diff strings.Builder
	diff.WriteString("--- current\n+++ desired\n@@ -1,20 +1,20 @@\n")
	longOld := strings.Repeat("old-", 80)
	longNew := strings.Repeat("new-", 80)
	for i := range 30 {
		fmt.Fprintf(&diff, "-old line %02d %s\n+new line %02d %s\n", i, longOld, i, longNew)
	}
	dv.SetContent(diff.String(), false, "", "")
	pv.DiffView = &dv
	m.planView = &pv

	lines := strings.Split(stripSGRForTUITest(m.View().Content), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "j/k:scroll") || !strings.Contains(last, "esc:back") {
		t.Fatalf("expected help bar on final row, got %q\nfull view:\n%s", last, strings.Join(lines, "\n"))
	}
}

func TestPlanView_DiffIdenticalContent(t *testing.T) {
	t.Parallel()
	dv := newDiffViewModel(120, 40, "same.md", "./same.md")
	dv.SetContent("", false, "", "")

	view := dv.View()
	if !strings.Contains(view, "identical") {
		t.Fatalf("expected 'identical' message for empty diff, got:\n%s", view)
	}
}
