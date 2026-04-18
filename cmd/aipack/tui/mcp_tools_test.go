package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
)

// ---------------------------------------------------------------------------
// toolPickerItemsForServer
// ---------------------------------------------------------------------------

func TestTriStateItemsForServer_SilentGrantsAllAsAsk(t *testing.T) {
	t.Parallel()
	available := []string{"tool-a", "tool-b", "tool-c"}
	packs := []config.PackEntry{{Name: "pk"}} // no MCP entry → silent
	items := toolPickerItemsForServer(available, packs, "pk", "srv")
	if len(items) != 3 {
		t.Fatalf("items=%d want 3", len(items))
	}
	for _, it := range items {
		if it.state != triAsk {
			t.Errorf("%s state=%v want triAsk (silent profile grants all)", it.label, it.state)
		}
	}
}

func TestTriStateItemsForServer_ExplicitAllowedShowsAskForListedOnly(t *testing.T) {
	t.Parallel()
	available := []string{"a", "b", "c"}
	packs := []config.PackEntry{{
		Name: "pk",
		MCP:  map[string]config.MCPServerConfig{"srv": {AllowedTools: []string{"a"}}},
	}}
	items := toolPickerItemsForServer(available, packs, "pk", "srv")
	got := map[string]triState{}
	for _, it := range items {
		got[it.label] = it.state
	}
	if got["a"] != triAsk {
		t.Errorf("a=%v want triAsk", got["a"])
	}
	if got["b"] != triOff {
		t.Errorf("b=%v want triOff", got["b"])
	}
	if got["c"] != triOff {
		t.Errorf("c=%v want triOff", got["c"])
	}
}

func TestTriStateItemsForServer_AutoTakesPrecedenceOverAllowed(t *testing.T) {
	t.Parallel()
	available := []string{"a", "b"}
	packs := []config.PackEntry{{
		Name: "pk",
		MCP: map[string]config.MCPServerConfig{"srv": {
			AllowedTools:       []string{"a"},
			AlwaysAllowedTools: []string{"a"},
		}},
	}}
	items := toolPickerItemsForServer(available, packs, "pk", "srv")
	for _, it := range items {
		if it.label == "a" && it.state != triAuto {
			t.Errorf("a state=%v want triAuto", it.state)
		}
	}
}

func TestTriStateItemsForServer_DisabledStaysOff(t *testing.T) {
	t.Parallel()
	available := []string{"a", "b"}
	packs := []config.PackEntry{{
		Name: "pk",
		MCP: map[string]config.MCPServerConfig{"srv": {
			AllowedTools:  []string{"a", "b"},
			DisabledTools: []string{"a"},
		}},
	}}
	items := toolPickerItemsForServer(available, packs, "pk", "srv")
	for _, it := range items {
		if it.label == "a" && it.state != triOff {
			t.Errorf("a state=%v want triOff (disabled beats allowed)", it.state)
		}
	}
}

func TestTriStateItemsForServer_DisabledOnlyTreatsOthersAsAsk(t *testing.T) {
	t.Parallel()
	available := []string{"a", "b", "c"}
	packs := []config.PackEntry{{
		Name: "pk",
		MCP: map[string]config.MCPServerConfig{"srv": {
			DisabledTools: []string{"a"},
		}},
	}}
	items := toolPickerItemsForServer(available, packs, "pk", "srv")
	got := map[string]triState{}
	for _, it := range items {
		got[it.label] = it.state
	}
	if got["a"] != triOff {
		t.Errorf("a=%v want triOff (explicitly disabled)", got["a"])
	}
	if got["b"] != triAsk {
		t.Errorf("b=%v want triAsk (disabled-only should otherwise behave like silent grant-all)", got["b"])
	}
	if got["c"] != triAsk {
		t.Errorf("c=%v want triAsk (disabled-only should otherwise behave like silent grant-all)", got["c"])
	}
}

// ---------------------------------------------------------------------------
// pickerOutputForSave
// ---------------------------------------------------------------------------

func TestPickerOutputForSave_AllAskIsSilent(t *testing.T) {
	t.Parallel()
	available := []string{"a", "b", "c"}
	allowed, always, disabled := pickerOutputForSave(available, nil, available, config.MCPServerConfig{})
	if allowed != nil || always != nil || disabled != nil {
		t.Errorf("all-ask should be nil/nil/nil (silent=grant all); got allowed=%v always=%v disabled=%v", allowed, always, disabled)
	}
}

func TestPickerOutputForSave_PartialAskEmitsExplicitAllowed(t *testing.T) {
	t.Parallel()
	available := []string{"a", "b", "c"}
	ask := []string{"a", "b"} // c is off
	allowed, always, disabled := pickerOutputForSave(ask, nil, available, config.MCPServerConfig{})
	if !slices.Equal(allowed, []string{"a", "b"}) {
		t.Errorf("allowed=%v want [a b]", allowed)
	}
	if always != nil {
		t.Errorf("always=%v want nil", always)
	}
	if disabled != nil {
		t.Errorf("disabled=%v want nil (off captured by absence from allowed)", disabled)
	}
}

func TestPickerOutputForSave_AutoEmitsBoth(t *testing.T) {
	t.Parallel()
	available := []string{"a", "b"}
	ask := []string{"a"}
	auto := []string{"b"}
	allowed, always, disabled := pickerOutputForSave(ask, auto, available, config.MCPServerConfig{})
	if !slices.Equal(allowed, []string{"a", "b"}) {
		t.Errorf("allowed=%v want [a b] (ask ∪ auto)", allowed)
	}
	if !slices.Equal(always, []string{"b"}) {
		t.Errorf("always=%v want [b]", always)
	}
	if disabled != nil {
		t.Errorf("disabled=%v want nil", disabled)
	}
}

func TestPickerOutputForSave_AllAutoEmitsExplicit(t *testing.T) {
	t.Parallel()
	available := []string{"a", "b"}
	allowed, always, disabled := pickerOutputForSave(nil, available, available, config.MCPServerConfig{})
	if !slices.Equal(allowed, []string{"a", "b"}) {
		t.Errorf("allowed=%v want [a b]", allowed)
	}
	if !slices.Equal(always, []string{"a", "b"}) {
		t.Errorf("always=%v want [a b]", always)
	}
	if disabled != nil {
		t.Errorf("disabled=%v want nil", disabled)
	}
}

func TestPickerOutputForSave_DisabledOnlyRoundTripPreservesDisabledTools(t *testing.T) {
	t.Parallel()
	// Initial profile intent: disabled_tools-only with disabled=["a"].
	// If the user opens the picker and saves without changing effective state,
	// we should keep disabled-only semantics so newly discovered tools remain
	// allowed by default.
	available := []string{"a", "b", "c"}
	ask := []string{"b", "c"} // effective state with only "a" disabled
	auto := []string{}

	allowed, always, disabled := pickerOutputForSave(ask, auto, available, config.MCPServerConfig{
		DisabledTools: []string{"a"},
	})
	if allowed != nil {
		t.Errorf("allowed=%v want nil (disabled-only policy should not collapse to explicit allowlist)", allowed)
	}
	if always != nil {
		t.Errorf("always=%v want nil", always)
	}
	if !slices.Equal(disabled, []string{"a"}) {
		t.Errorf("disabled=%v want [a] (preserve disabled-only semantics)", disabled)
	}
}

// ---------------------------------------------------------------------------
// applyMCPBulkAction — dispatch layer's pure mutation helper
// ---------------------------------------------------------------------------

func TestApplyMCPBulkAction_EnableAllClearsLists(t *testing.T) {
	t.Parallel()
	srv := config.MCPServerConfig{
		Enabled:            boolPtr(false),
		AllowedTools:       []string{"kept"},
		AlwaysAllowedTools: []string{"kept"},
		DisabledTools:      []string{"kept"},
	}
	next, applied, err := applyMCPBulkAction(srv, actMCPEnableAll, nil)
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v, want applied=true err=nil", applied, err)
	}
	if next.Enabled == nil || !*next.Enabled {
		t.Errorf("Enabled=%v, want true (enable-all flips enabled on)", next.Enabled)
	}
	if next.AllowedTools != nil || next.AlwaysAllowedTools != nil || next.DisabledTools != nil {
		t.Errorf("enable-all should clear every tool list (silent = grant all); got %+v", next)
	}
}

func TestApplyMCPBulkAction_AlwaysAllowAllRefusesWithoutProbe(t *testing.T) {
	t.Parallel()
	srv := config.MCPServerConfig{AllowedTools: []string{"kept"}}
	next, applied, err := applyMCPBulkAction(srv, actMCPAlwaysAllowAll, nil)
	if err == nil {
		t.Fatalf("expected refusal error, got nil")
	}
	if applied {
		t.Errorf("applied=true, want false (no probe → no mutation)")
	}
	if !slices.Equal(next.AllowedTools, []string{"kept"}) {
		t.Errorf("refusal path must leave srv untouched; got %+v", next)
	}
	if !strings.Contains(err.Error(), "probe") {
		t.Errorf("error should mention probing; got: %v", err)
	}
}

func TestApplyMCPBulkAction_AlwaysAllowAllUsesProbedToolsSorted(t *testing.T) {
	t.Parallel()
	srv := config.MCPServerConfig{
		AllowedTools:  []string{"gone"},
		DisabledTools: []string{"gone"},
	}
	probed := []string{"zeta", "alpha", "mu"}
	next, applied, err := applyMCPBulkAction(srv, actMCPAlwaysAllowAll, probed)
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if !slices.Equal(next.AlwaysAllowedTools, []string{"alpha", "mu", "zeta"}) {
		t.Errorf("AlwaysAllowedTools=%v, want sorted probe result", next.AlwaysAllowedTools)
	}
	if next.AllowedTools != nil || next.DisabledTools != nil {
		t.Errorf("always-allow-all should clear allowed + disabled; got %+v", next)
	}
	if next.Enabled == nil || !*next.Enabled {
		t.Errorf("Enabled=%v, want true", next.Enabled)
	}
}

func TestApplyMCPBulkAction_DisableServerClearsLists(t *testing.T) {
	t.Parallel()
	srv := config.MCPServerConfig{
		Enabled:            boolPtr(true),
		AllowedTools:       []string{"x"},
		AlwaysAllowedTools: []string{"y"},
		DisabledTools:      []string{"z"},
	}
	next, applied, err := applyMCPBulkAction(srv, actMCPDisableServer, nil)
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if next.Enabled == nil || *next.Enabled {
		t.Errorf("Enabled=%v, want false", next.Enabled)
	}
	if next.AllowedTools != nil || next.AlwaysAllowedTools != nil || next.DisabledTools != nil {
		t.Errorf("disable should clear tool lists (meaningless on disabled server); got %+v", next)
	}
}

func TestApplyMCPBulkAction_EnableServerPreservesLists(t *testing.T) {
	t.Parallel()
	srv := config.MCPServerConfig{
		Enabled:       boolPtr(false),
		AllowedTools:  []string{"kept"},
		DisabledTools: []string{"kept-disabled"},
	}
	next, applied, err := applyMCPBulkAction(srv, actMCPEnableServer, nil)
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if next.Enabled == nil || !*next.Enabled {
		t.Errorf("Enabled=%v, want true", next.Enabled)
	}
	if !slices.Equal(next.AllowedTools, []string{"kept"}) || !slices.Equal(next.DisabledTools, []string{"kept-disabled"}) {
		t.Errorf("enable-server must preserve tool lists; got %+v", next)
	}
}

func TestApplyMCPBulkAction_ResetClearsListsPreservesEnabled(t *testing.T) {
	t.Parallel()
	srv := config.MCPServerConfig{
		Enabled:            boolPtr(true),
		AllowedTools:       []string{"a"},
		AlwaysAllowedTools: []string{"b"},
		DisabledTools:      []string{"c"},
	}
	next, applied, err := applyMCPBulkAction(srv, actMCPReset, nil)
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if next.Enabled == nil || !*next.Enabled {
		t.Errorf("Enabled=%v, want true (reset preserves enabled)", next.Enabled)
	}
	if next.AllowedTools != nil || next.AlwaysAllowedTools != nil || next.DisabledTools != nil {
		t.Errorf("reset should clear all tool lists; got %+v", next)
	}
}

func TestApplyMCPBulkAction_UnknownActionNoop(t *testing.T) {
	t.Parallel()
	srv := config.MCPServerConfig{AllowedTools: []string{"kept"}}
	next, applied, err := applyMCPBulkAction(srv, "not-a-bulk-action", nil)
	if err != nil {
		t.Errorf("unknown action should be silent noop, got err: %v", err)
	}
	if applied {
		t.Errorf("applied=true for unknown action, want false")
	}
	if !slices.Equal(next.AllowedTools, []string{"kept"}) {
		t.Errorf("noop should leave srv untouched; got %+v", next)
	}
}

// ---------------------------------------------------------------------------
// Collapse-to-silent — picker output of all-ask drops the profile entry
// ---------------------------------------------------------------------------

func TestPickerOutputForSave_CollapsesToSilentYieldsZeroConfig(t *testing.T) {
	t.Parallel()
	// The picker save path relies on mcpServerConfigIsZero to detect when
	// the resulting MCPServerConfig should be dropped from the profile map
	// instead of serialized as an empty block. Pin the end-to-end behavior:
	// all-ask + no prior disabled-only → nil/nil/nil → isZero → drop entry.
	available := []string{"a", "b", "c"}
	allowed, always, disabled := pickerOutputForSave(available, nil, available, config.MCPServerConfig{})
	cfg := config.MCPServerConfig{AllowedTools: allowed, AlwaysAllowedTools: always, DisabledTools: disabled}
	if !mcpServerConfigIsZero(cfg) {
		t.Fatalf("expected zero config after collapse-to-silent; got %+v", cfg)
	}
}

// ---------------------------------------------------------------------------
// probe cache round-trip (memory → disk → next session)
// ---------------------------------------------------------------------------

func TestMCPProbeCache_PersistsAcrossProfilesModelInstances(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	// Session 1: store a probe result to memory, persist to disk.
	m1 := newProfilesModel(context.Background(), nil, configDir)
	key := mcpProbeKey{packRoot: "/packs/test", server: "srv-1"}
	m1.storeMCPProbeTools(key, []string{"tool-alpha", "tool-beta"})
	persistMCPProbeCache(configDir, m1.mcpProbeCache)

	// Session 2: new profiles model loads cache from disk — must see the
	// result without re-probing.
	m2 := newProfilesModel(context.Background(), nil, configDir)
	tools, ok := m2.cachedMCPProbeTools(key)
	if !ok {
		t.Fatalf("session 2 did not see cached probe — seed path broken")
	}
	if !slices.Equal(tools, []string{"tool-alpha", "tool-beta"}) {
		t.Errorf("tools = %v, want [tool-alpha tool-beta]", tools)
	}
	if _, ok := m2.cachedMCPProbedAt(key); !ok {
		t.Error("probedAt timestamp did not survive persist/load")
	}
}

// ---------------------------------------------------------------------------
// findPackEntry
// ---------------------------------------------------------------------------

func TestFindPackEntry_Found(t *testing.T) {
	t.Parallel()
	cfg := &config.ProfileConfig{Packs: []config.PackEntry{{Name: "a"}, {Name: "b"}}}
	pe := findPackEntry(cfg, "b")
	if pe == nil || pe.Name != "b" {
		t.Errorf("want name=b; got %+v", pe)
	}
}

func TestFindPackEntry_NotFound(t *testing.T) {
	t.Parallel()
	cfg := &config.ProfileConfig{Packs: []config.PackEntry{{Name: "a"}}}
	if findPackEntry(cfg, "missing") != nil {
		t.Errorf("want nil")
	}
}
