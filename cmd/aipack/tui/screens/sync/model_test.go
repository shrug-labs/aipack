package sync

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
)

func init() {
	common.StatusDotActive = "●"
	common.StatusDotInactive = "○"
	common.StatusDotLoading = "⟳"
}

func TestModelIgnoresConfigNavigationKeys(t *testing.T) {
	t.Parallel()

	_, cmd := New("").Update(tea.KeyPressMsg(tea.Key{Text: "k", Code: 'k'}))
	if cmd != nil {
		t.Fatalf("sync status screen should not emit config edit commands, got %T", cmd())
	}
}

func TestModelViewShowsStatusAndPlan(t *testing.T) {
	t.Parallel()

	m := New("/tmp/config").
		WithContext(common.SyncSnapshot{
			ProfileName: "work",
			Status:      common.SyncStatusSynced,
			Target: common.SyncTarget{
				PlanSummary: app.PlanSummary{
					NumRules:    2,
					NumSkills:   1,
					LedgerFiles: 42,
					Ops: []app.PlanOp{
						{Kind: app.PlanOpSkill, Dst: "/tmp/skill.md", SourcePack: "aipack-core"},
					},
					HarnessLedgers: []app.HarnessLedgerInfo{
						{Harness: "cline", Files: 42, UpdatedAt: 1740787815},
					},
				},
			},
		})
	m = m.SetSize(120, 40).(Model)

	rendered := lipgloss.NewCompositor(m.View()).Render()
	for _, want := range []string{
		"up to date",
		"Rules:     2",
		"Skills:    1",
		"Total:     3",
		"Sync Plan: work",
		"Skills (1)",
		"aipack-core",
		"/tmp/skill.md",
		"42 files",
		"Ledgers:",
		"Last sync:",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected view to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestPlanPanelScrollsIndependentlyOfStatus(t *testing.T) {
	t.Parallel()

	var ops []app.PlanOp
	for i := range 20 {
		ops = append(ops, app.PlanOp{
			Kind:       app.PlanOpSkill,
			DiffKind:   domain.DiffCreate,
			SourcePack: "pack",
			Dst:        "/tmp/target-" + string(rune('a'+i)),
		})
	}
	m := New("/tmp/config").
		WithContext(common.SyncSnapshot{
			ProfileName: "work",
			Status:      common.SyncStatusUnsynced,
			Target:      common.SyncTarget{PlanSummary: app.PlanSummary{Ops: ops}},
		})
	m = m.SetSize(100, 16).(Model)
	if m.planOffset != 0 {
		t.Fatalf("initial plan offset = %d, want 0", m.planOffset)
	}

	var cmd tea.Cmd
	var next common.Screen
	for range 6 {
		next, cmd = m.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
		if cmd != nil {
			t.Fatalf("scrolling should not emit commands, got %T", cmd())
		}
		m = next.(Model)
	}
	if m.planOffset == 0 {
		t.Fatalf("expected plan offset to advance after repeated j, got %d", m.planOffset)
	}

	rendered := lipgloss.NewCompositor(m.View()).Render()
	if strings.Index(rendered, "Sync Plan: work") > strings.Index(rendered, "Status") {
		t.Fatalf("expected plan panel to render to the left of status panel:\n%s", rendered)
	}
	if !strings.Contains(rendered, "─── 7/20 ───") {
		t.Fatalf("expected plan cursor footer after scrolling, got:\n%s", rendered)
	}
}

func TestClampPlanOffsetUsesCachedPlanItems(t *testing.T) {
	t.Parallel()

	var ops []app.PlanOp
	for i := range 8 {
		ops = append(ops, app.PlanOp{
			Kind:       app.PlanOpSkill,
			DiffKind:   domain.DiffCreate,
			SourcePack: "pack",
			Dst:        "/tmp/target-" + string(rune('a'+i)),
		})
	}
	m := New("/tmp/config").
		WithContext(common.SyncSnapshot{
			ProfileName: "work",
			Target:      common.SyncTarget{PlanSummary: app.PlanSummary{Ops: ops}},
		})
	if len(m.cachedPlanItems) < 3 {
		t.Fatalf("test setup expected multiple cached items, got %d", len(m.cachedPlanItems))
	}

	m.cachedPlanItems = m.cachedPlanItems[:2]
	m.planCursor = 99
	m.planOffset = 99
	m = m.SetSize(100, 20).(Model)

	if m.planCursor >= len(m.cachedPlanItems) {
		t.Fatalf("cursor = %d, cached items = %d", m.planCursor, len(m.cachedPlanItems))
	}
	pv := m.planView(100, 20)
	if m.planOffset > max(len(m.cachedPlanItems)-pv.ViewportHeight(), 0) {
		t.Fatalf("offset = %d exceeds cached item viewport bounds", m.planOffset)
	}
}

func TestEnterOnPlanItemRequestsDiff(t *testing.T) {
	t.Parallel()

	ops := []app.PlanOp{{
		Kind:       app.PlanOpRule,
		DiffKind:   domain.DiffCreate,
		SourcePack: "pack",
		Dst:        "/tmp/rule.md",
		Content:    []byte("hello"),
	}}
	m := New("/tmp/config").
		WithContext(common.SyncSnapshot{
			ProfileName: "work",
			Target: common.SyncTarget{PlanSummary: app.PlanSummary{
				Ops: ops,
			}},
		})
	m = m.SetSize(100, 20).(Model)

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("expected diff request command")
	}
	msg := cmd()
	req, ok := msg.(common.SyncDiffRequestMsg)
	if !ok {
		t.Fatalf("expected SyncDiffRequestMsg, got %T", msg)
	}
	if req.ProfileName != "work" {
		t.Fatalf("expected profile work, got %q", req.ProfileName)
	}
	if req.Cursor != 1 {
		t.Fatalf("expected cursor to reference first plan item, got %d", req.Cursor)
	}
	if len(req.Ops) != len(ops) {
		t.Fatalf("expected %d ops, got %d", len(ops), len(req.Ops))
	}
}

func TestClickingPlanItemRequestsDiff(t *testing.T) {
	t.Parallel()

	ops := []app.PlanOp{{
		Kind:       app.PlanOpSkill,
		DiffKind:   domain.DiffCreate,
		SourcePack: "pack",
		Dst:        "/tmp/skill.md",
		Content:    []byte("hello"),
	}}
	m := New("/tmp/config").
		WithContext(common.SyncSnapshot{
			ProfileName: "work",
			Target: common.SyncTarget{PlanSummary: app.PlanSummary{
				Ops: ops,
			}},
		})
	m = m.SetSize(100, 20).(Model)

	comp := lipgloss.NewCompositor(m.View())
	if comp.GetLayer("sync:plan:item:1") == nil {
		t.Fatal("expected embedded plan row to register a click layer")
	}

	_, cmd := m.Update(common.LayerHitMsg{
		ID:    "sync:plan:item:1",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd == nil {
		t.Fatal("expected diff request command")
	}
	msg := cmd()
	req, ok := msg.(common.SyncDiffRequestMsg)
	if !ok {
		t.Fatalf("expected SyncDiffRequestMsg, got %T", msg)
	}
	if req.ProfileName != "work" || req.Cursor != 1 {
		t.Fatalf("unexpected diff request: %+v", req)
	}
}

func TestViewDoesNotRegisterConfigClickableLayers(t *testing.T) {
	t.Parallel()

	m := New("/tmp/config").WithContext(common.SyncSnapshot{})
	m = m.SetSize(120, 40).(Model)

	comp := lipgloss.NewCompositor(m.View())
	for _, id := range []string{
		"sync:field:profile",
		"sync:harness:cline",
		"sync:field:scope",
		"sync:field:collision",
	} {
		if comp.GetLayer(id) != nil {
			t.Fatalf("sync screen should not register config layer %q", id)
		}
	}
}

func TestLayerHitDoesNotDispatchConfigIntents(t *testing.T) {
	t.Parallel()

	m := New("").WithContext(common.SyncSnapshot{})

	_, cmd := m.Update(common.LayerHitMsg{
		ID:    "sync:field:scope",
		Mouse: tea.MouseMotionMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("motion should not trigger an action")
	}

	_, cmd = m.Update(common.LayerHitMsg{
		ID:    "sync:field:scope",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatalf("sync screen should not dispatch config intent, got %T", cmd())
	}
}

func TestStatusMatchesShellValueOrder(t *testing.T) {
	t.Parallel()

	if common.SyncStatusPending != 0 || common.SyncStatusLoading != 1 || common.SyncStatusSynced != 2 || common.SyncStatusUnsynced != 3 || common.SyncStatusError != 4 {
		t.Fatalf("sync status values must stay aligned with persisted profile status ordering")
	}
}
