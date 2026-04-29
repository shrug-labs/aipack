package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"

	"github.com/shrug-labs/aipack/internal/app"
)

// TestFormatRowSuffix locks in the minimal-glyph rendering contract:
// spinner during update, ✓ on success, ✗ on error, nothing for idle or
// cancelled. No phase labels, hash transitions, link targets, or other
// detail text — that visual debt accumulates fast across batches.
func TestFormatRowSuffix(t *testing.T) {
	t.Parallel()
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	tests := []struct {
		name     string
		detail   packItemDetail
		wantSubs []string
		wantNot  []string
	}{
		{
			name:    "idle row renders nothing",
			detail:  packItemDetail{updateState: asyncPending},
			wantNot: []string{"✓", "✗"},
		},
		{
			name: "in-flight row renders only the spinner glyph",
			detail: packItemDetail{
				updateState: asyncLoading,
				updateType:  string(app.PackUpdatePhaseCloning),
			},
			// Spinner.View() output is theme-dependent; just assert the
			// row contains no phase label or detail text.
			wantNot: []string{"cloning", "✓", "✗"},
		},
		{
			name: "terminal updated renders bare check",
			detail: packItemDetail{
				updateState: asyncLoaded,
				updateType:  string(app.StatusUpdated),
			},
			wantSubs: []string{"✓"},
			wantNot:  []string{"updated", "→", "->"},
		},
		{
			name: "terminal up_to_date renders bare check",
			detail: packItemDetail{
				updateState: asyncLoaded,
				updateType:  string(app.StatusUpToDate),
			},
			wantSubs: []string{"✓"},
			wantNot:  []string{"up-to-date"},
		},
		{
			name: "terminal linked renders bare check (no path)",
			detail: packItemDetail{
				updateState: asyncLoaded,
				updateType:  "linked",
			},
			wantSubs: []string{"✓"},
			wantNot:  []string{"linked", "/tmp"},
		},
		{
			name: "terminal local renders bare check",
			detail: packItemDetail{
				updateState: asyncLoaded,
				updateType:  "local",
			},
			wantSubs: []string{"✓"},
			wantNot:  []string{"local"},
		},
		{
			name: "cancelled state should not render any suffix",
			detail: packItemDetail{
				updateState: asyncPending,
				updateType:  "cancelled",
			},
			wantNot: []string{"✓", "✗", "⊘", "cancelled"},
		},
		{
			name: "error renders red ✗ glyph alone",
			detail: packItemDetail{
				updateState: asyncError,
				updateType:  string(app.StatusError),
				updateErr:   errors.New("clone failed: network unreachable"),
			},
			wantSubs: []string{"✗"},
			wantNot:  []string{"error", "network unreachable"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatRowSuffix(tc.detail, sp)
			for _, sub := range tc.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("suffix %q does not contain %q", got, sub)
				}
			}
			for _, sub := range tc.wantNot {
				if strings.Contains(got, sub) {
					t.Errorf("suffix %q unexpectedly contains %q", got, sub)
				}
			}
		})
	}
}

// TestApplyProgress_StateTransitions covers the per-row state machine.
func TestApplyProgress_StateTransitions(t *testing.T) {
	t.Parallel()

	newModel := func() packsModel {
		m := newPacksModel("/tmp/test")
		m.items = []packItemDetail{{}}
		m.installedMap["mypack"] = 0
		m.batchTotal = 1
		return m
	}

	t.Run("intermediate event transitions to loading and increments inflight", func(t *testing.T) {
		t.Parallel()
		m := newModel()
		m, _ = m.applyProgress(app.PackUpdateEvent{
			Phase: app.PackUpdatePhaseCloning,
			Pack:  "mypack",
		})
		if m.items[0].updateState != asyncLoading {
			t.Fatalf("state = %v, want asyncLoading", m.items[0].updateState)
		}
		if m.batchInFlight != 1 {
			t.Errorf("batchInFlight = %d, want 1", m.batchInFlight)
		}
	})

	t.Run("re-entering loading does not double-count inflight", func(t *testing.T) {
		t.Parallel()
		m := newModel()
		m, _ = m.applyProgress(app.PackUpdateEvent{Phase: app.PackUpdatePhaseCloning, Pack: "mypack"})
		m, _ = m.applyProgress(app.PackUpdateEvent{Phase: app.PackUpdatePhaseExtracting, Pack: "mypack"})
		if m.batchInFlight != 1 {
			t.Errorf("batchInFlight = %d, want 1 (no double-count)", m.batchInFlight)
		}
	})

	t.Run("terminal success transitions to loaded, decrements inflight, schedules clear", func(t *testing.T) {
		t.Parallel()
		m := newModel()
		m, _ = m.applyProgress(app.PackUpdateEvent{Phase: app.PackUpdatePhaseCloning, Pack: "mypack"})
		m, cmd := m.applyProgress(app.PackUpdateEvent{
			Pack:   "mypack",
			Result: &app.PackUpdateResult{Name: "mypack", Status: app.StatusUpdated, Message: "abc1234"},
		})
		if m.items[0].updateState != asyncLoaded {
			t.Errorf("state = %v, want asyncLoaded", m.items[0].updateState)
		}
		if m.batchInFlight != 0 {
			t.Errorf("batchInFlight = %d, want 0", m.batchInFlight)
		}
		if m.batchDone != 1 {
			t.Errorf("batchDone = %d, want 1", m.batchDone)
		}
		if m.items[0].updateClearAt.IsZero() {
			t.Error("expected updateClearAt to be set on terminal success")
		}
		if cmd == nil {
			t.Error("expected a row-clear tea.Cmd to be scheduled")
		}
	})

	t.Run("terminal error transitions to asyncError and schedules auto-clear", func(t *testing.T) {
		t.Parallel()
		m := newModel()
		m, _ = m.applyProgress(app.PackUpdateEvent{Phase: app.PackUpdatePhaseCloning, Pack: "mypack"})
		m, cmd := m.applyProgress(app.PackUpdateEvent{
			Pack:   "mypack",
			Result: &app.PackUpdateResult{Name: "mypack", Status: app.StatusError, Message: "boom"},
			Err:    errors.New("boom"),
		})
		if m.items[0].updateState != asyncError {
			t.Errorf("state = %v, want asyncError", m.items[0].updateState)
		}
		if m.items[0].updateClearAt.IsZero() {
			t.Error("expected error rows to schedule auto-clear (10s TTL)")
		}
		if cmd == nil {
			t.Error("expected a clear cmd for error rows")
		}
	})

	t.Run("event with empty Pack is ignored", func(t *testing.T) {
		t.Parallel()
		m := newModel()
		m, cmd := m.applyProgress(app.PackUpdateEvent{Phase: app.PackUpdatePhaseExtracting, Pack: ""})
		if m.batchInFlight != 0 || cmd != nil {
			t.Error("expected empty-pack event to be a no-op")
		}
	})

	t.Run("event for unknown pack is ignored", func(t *testing.T) {
		t.Parallel()
		m := newModel()
		m, cmd := m.applyProgress(app.PackUpdateEvent{Pack: "ghost", Result: &app.PackUpdateResult{Name: "ghost", Status: app.StatusUpdated}})
		if m.batchDone != 0 || cmd != nil {
			t.Error("expected unknown-pack event to be a no-op")
		}
	})
}

// TestApplyRowClear covers freshness checking on auto-clear ticks.
func TestApplyRowClear(t *testing.T) {
	t.Parallel()

	newModelWithLoadedRow := func() (packsModel, packRowClearMsg) {
		m := newPacksModel("/tmp/test")
		m.items = []packItemDetail{{}}
		m.installedMap["mypack"] = 0
		m, _ = m.applyProgress(app.PackUpdateEvent{Phase: app.PackUpdatePhaseCloning, Pack: "mypack"})
		m, _ = m.applyProgress(app.PackUpdateEvent{
			Pack:   "mypack",
			Result: &app.PackUpdateResult{Name: "mypack", Status: app.StatusUpdated, Message: "abc1234"},
		})
		msg := packRowClearMsg{packName: "mypack", when: m.items[0].updateClearAt}
		return m, msg
	}

	t.Run("matching clearAt resets state to idle", func(t *testing.T) {
		t.Parallel()
		m, msg := newModelWithLoadedRow()
		m = m.applyRowClear(msg)
		if m.items[0].updateState != asyncPending {
			t.Errorf("state = %v, want asyncPending", m.items[0].updateState)
		}
		if m.items[0].updateType != "" {
			t.Error("expected type to be cleared")
		}
	})

	t.Run("stale clearAt (row went back to loading) is ignored", func(t *testing.T) {
		t.Parallel()
		m, msg := newModelWithLoadedRow()
		// User re-runs update; row goes back to loading with new clearAt.
		m, _ = m.applyProgress(app.PackUpdateEvent{Phase: app.PackUpdatePhaseCloning, Pack: "mypack"})
		// Stale tick fires.
		m = m.applyRowClear(msg)
		if m.items[0].updateState != asyncLoading {
			t.Errorf("state = %v, want asyncLoading (stale tick should be ignored)", m.items[0].updateState)
		}
	})

	t.Run("unknown pack name is a no-op", func(t *testing.T) {
		t.Parallel()
		m, _ := newModelWithLoadedRow()
		m = m.applyRowClear(packRowClearMsg{packName: "ghost"})
		if m.items[0].updateState != asyncLoaded {
			t.Error("expected unknown-pack clear to leave model unchanged")
		}
	})
}

// TestApplyCancellation locks in the cancellation contract: cancelled
// rows reset to idle (asyncPending) so they render no glyph or text. The
// status-bar "Cancelled update" message provides the global feedback;
// per-row decoration is intentionally absent.
func TestApplyCancellation(t *testing.T) {
	t.Parallel()

	newModel := func() packsModel {
		m := newPacksModel("/tmp/test")
		m.items = []packItemDetail{
			{updateState: asyncLoading, updateType: string(app.PackUpdatePhaseCloning)},
			{updateState: asyncLoading},
			{updateState: asyncPending},
		}
		m.installedMap["mid-flight-pack"] = 0
		m.installedMap["never-started-pack"] = 1
		m.installedMap["completed-pack"] = 2
		return m
	}

	t.Run("cancelled row resets to idle (no glyph)", func(t *testing.T) {
		t.Parallel()
		m := newModel()
		m = m.applyCancellation([]app.PackUpdateResult{
			{Name: "mid-flight-pack", Status: app.StatusError, Message: "cancelled"},
		})
		it := m.items[0]
		if it.updateState != asyncPending {
			t.Errorf("state = %v, want asyncPending (cancelled = invisible)", it.updateState)
		}
		if it.updateType != "" || it.updateErr != nil {
			t.Errorf("expected all per-row update fields cleared, got %+v", it)
		}
	})

	t.Run("non-cancelled error results are not touched", func(t *testing.T) {
		t.Parallel()
		m := newModel()
		m = m.applyCancellation([]app.PackUpdateResult{
			{Name: "mid-flight-pack", Status: app.StatusError, Message: "git clone failed"},
		})
		// applyCancellation leaves non-cancelled results alone; the
		// progress event handler is responsible for the error transition.
		if m.items[0].updateState != asyncLoading {
			t.Errorf("state = %v, want asyncLoading (untouched by applyCancellation)", m.items[0].updateState)
		}
	})

	t.Run("unknown pack is a no-op", func(t *testing.T) {
		t.Parallel()
		m := newModel()
		m = m.applyCancellation([]app.PackUpdateResult{
			{Name: "ghost", Status: app.StatusError, Message: "cancelled"},
		})
		if m.items[0].updateState != asyncLoading {
			t.Error("expected unknown-pack cancellation to be a no-op")
		}
	})
}

// TestAnyCancelled covers the predicate that gates the "Cancelled update"
// status-text branch.
func TestAnyCancelled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		results []app.PackUpdateResult
		want    bool
	}{
		{name: "empty results", want: false},
		{
			name: "no cancelled results",
			results: []app.PackUpdateResult{
				{Name: "a", Status: app.StatusUpdated},
				{Name: "b", Status: app.StatusUpToDate},
			},
		},
		{
			name: "real error is not cancelled",
			results: []app.PackUpdateResult{
				{Name: "a", Status: app.StatusError, Message: "git clone failed"},
			},
		},
		{
			name: "single cancelled result",
			results: []app.PackUpdateResult{
				{Name: "a", Status: app.StatusUpdated},
				{Name: "b", Status: app.StatusError, Message: "cancelled"},
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := anyCancelled(tc.results); got != tc.want {
				t.Errorf("anyCancelled = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPacksBatchStatus covers the status-bar summary line shape.
func TestPacksBatchStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		m    packsModel
		want string
	}{
		{
			name: "all in flight",
			m:    packsModel{batchTotal: 3, batchInFlight: 3},
			want: "Updating: 3 in flight, 0 done, 0 pending — Esc to cancel",
		},
		{
			name: "mid-progress",
			m:    packsModel{batchTotal: 5, batchInFlight: 2, batchDone: 1},
			want: "Updating: 2 in flight, 1 done, 2 pending — Esc to cancel",
		},
		{
			name: "all done",
			m:    packsModel{batchTotal: 2, batchDone: 2},
			want: "Updating: 0 in flight, 2 done, 0 pending — Esc to cancel",
		},
		{
			name: "no total set falls back to in-flight + done",
			m:    packsModel{batchInFlight: 1, batchDone: 1},
			want: "Updating: 1 in flight, 1 done, 0 pending — Esc to cancel",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := packsBatchStatus(tc.m); got != tc.want {
				t.Errorf("packsBatchStatus =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}
