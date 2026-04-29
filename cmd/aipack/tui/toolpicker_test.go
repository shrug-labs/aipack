package tui

import (
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/mcp"
)

// TestProbePhaseLabel locks in the loading-view phase label rendering for
// the MCP tool picker. The labels are user-facing during the (typically
// 1-3 second) probe window, so the wording matters and should not silently
// change with refactors.
func TestProbePhaseLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		phase  mcp.ProbePhase
		detail string
		want   string
	}{
		{
			name: "pre-event default falls back to generic message",
			want: "Probing server...",
		},
		{
			name:   "starting event shows transport label",
			phase:  mcp.ProbePhaseStarting,
			detail: "stdio",
			want:   "Connecting via stdio...",
		},
		{
			name:   "starting with streamable-http transport",
			phase:  mcp.ProbePhaseStarting,
			detail: "streamable-http",
			want:   "Connecting via streamable-http...",
		},
		{
			name:  "starting with empty detail falls back to generic connecting",
			phase: mcp.ProbePhaseStarting,
			want:  "Connecting...",
		},
		{
			name:   "connected event renders generic connected message regardless of transport",
			phase:  mcp.ProbePhaseConnected,
			detail: "stdio",
			want:   "Connected. Listing tools...",
		},
		{
			name:  "listing_tools event shows listing message",
			phase: mcp.ProbePhaseListingTools,
			want:  "Listing tools...",
		},
		{
			name:  "unknown event type uses generic message",
			phase: mcp.ProbePhase("surprise"),
			want:  "Probing server...",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := probePhaseLabel(tc.phase, tc.detail); got != tc.want {
				t.Errorf("probePhaseLabel(%q, %q) = %q, want %q",
					tc.phase, tc.detail, got, tc.want)
			}
		})
	}
}

// TestToolPickerView_LoadingShowsPhase verifies the loading view renders
// the phase label rather than a static "Probing server...".
func TestToolPickerView_LoadingShowsPhase(t *testing.T) {
	t.Parallel()
	p := newToolPicker(dialogMCPToolPicker, "Tools for pack/server:", nil)
	p.loading = true
	p.phase = mcp.ProbePhaseListingTools

	view := p.View()
	if !strings.Contains(view, "Listing tools...") {
		t.Errorf("loading view should contain phase label %q, got:\n%s", "Listing tools...", view)
	}
}

// TestToolPickerView_LoadingPreEventFallback verifies that before any event
// arrives the loading view shows the generic "Probing server..." message.
func TestToolPickerView_LoadingPreEventFallback(t *testing.T) {
	t.Parallel()
	p := newToolPicker(dialogMCPToolPicker, "Tools for pack/server:", nil)
	p.loading = true

	view := p.View()
	if !strings.Contains(view, "Probing server...") {
		t.Errorf("pre-event loading view should fall back to %q, got:\n%s", "Probing server...", view)
	}
}
