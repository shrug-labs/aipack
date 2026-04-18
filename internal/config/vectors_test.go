package config

import (
	"reflect"
	"slices"
	"testing"
)

// ---------------------------------------------------------------------------
// SelectionsToVector tests
// ---------------------------------------------------------------------------

func TestSelectionsToVector_All(t *testing.T) {
	t.Parallel()
	inv := []string{"a", "b", "c"}
	got := SelectionsToVector(inv, []string{"a", "b", "c"})
	if got.Include != nil || got.Exclude != nil {
		t.Fatalf("all selected should be nil,nil; got include=%v exclude=%v", got.Include, got.Exclude)
	}
}

func TestSelectionsToVector_None(t *testing.T) {
	t.Parallel()
	inv := []string{"a", "b", "c"}
	got := SelectionsToVector(inv, []string{})
	if got.Include != nil {
		t.Fatalf("none selected must NOT emit Include — empty Include is equivalent to nil (\"include all\") by design for content vectors; got include=%v", *got.Include)
	}
	if got.Exclude == nil {
		t.Fatalf("none selected should emit Exclude of full inventory")
	}
	if !slices.Equal(*got.Exclude, []string{"a", "b", "c"}) {
		t.Fatalf("expected exclude=[a,b,c] (sorted full inventory), got %v", *got.Exclude)
	}
}

// Ensures the writer never emits a VectorSelector that the resolver would
// silently read as "include all" when the user's intent is "include none".
// Regression guard for the TUI-side bug where disabling every skill for a
// pack produced include: [] in YAML which the content-vector resolver read
// as "include all".
func TestSelectionsToVector_NoneRoundTripsToZero(t *testing.T) {
	t.Parallel()
	inv := []string{"alpha", "beta", "gamma"}
	sel := SelectionsToVector(inv, []string{})
	resolved := ResolveCurrentVector(inv, sel)
	if len(resolved) != 0 {
		t.Fatalf("none-selected write+resolve round trip must yield zero items; got %v", resolved)
	}
}

func TestSelectionsToVector_FewSelected(t *testing.T) {
	t.Parallel()
	inv := []string{"a", "b", "c", "d", "e"}
	got := SelectionsToVector(inv, []string{"a"})
	if got.Include == nil {
		t.Fatalf("few selected should use Include")
	}
	if len(*got.Include) != 1 || (*got.Include)[0] != "a" {
		t.Fatalf("expected include=[a], got %v", *got.Include)
	}
}

func TestSelectionsToVector_MostSelected(t *testing.T) {
	t.Parallel()
	inv := []string{"a", "b", "c", "d", "e"}
	got := SelectionsToVector(inv, []string{"a", "b", "c", "d"})
	if got.Exclude == nil {
		t.Fatalf("most selected should use Exclude")
	}
	if len(*got.Exclude) != 1 || (*got.Exclude)[0] != "e" {
		t.Fatalf("expected exclude=[e], got %v", *got.Exclude)
	}
}

func TestSelectionsToVector_Single(t *testing.T) {
	t.Parallel()
	inv := []string{"only"}
	got := SelectionsToVector(inv, []string{"only"})
	if got.Include != nil || got.Exclude != nil {
		t.Fatalf("single item all selected should be nil,nil")
	}
}

func TestSelectionsToVector_EmptyInventory(t *testing.T) {
	t.Parallel()
	got := SelectionsToVector([]string{}, []string{})
	if got.Include != nil || got.Exclude != nil {
		t.Fatalf("empty inventory should be nil,nil")
	}
}

func TestSelectionsToVector_Deterministic(t *testing.T) {
	t.Parallel()
	inv := []string{"z", "a", "m", "b"}
	got := SelectionsToVector(inv, []string{"z", "a"})
	if got.Include == nil {
		t.Fatalf("expected Include")
	}
	// Should be sorted.
	sorted := append([]string{}, *got.Include...)
	slices.Sort(sorted)
	for i, v := range *got.Include {
		if v != sorted[i] {
			t.Fatalf("Include not sorted: %v", *got.Include)
		}
	}
}

// ---------------------------------------------------------------------------
// MCPToConfig tests
// ---------------------------------------------------------------------------

func TestMCPToConfig_EnabledServerExplicitTools(t *testing.T) {
	t.Parallel()
	got := MCPToConfig([]string{"foo"}, []string{"foo"}, map[string][]string{"foo": {"b", "a"}}, nil)
	cfg := got["foo"]
	if cfg.Enabled == nil || !*cfg.Enabled {
		t.Fatalf("expected enabled")
	}
	if !reflect.DeepEqual(cfg.AllowedTools, []string{"a", "b"}) {
		t.Fatalf("expected sorted AllowedTools=[a b], got %v", cfg.AllowedTools)
	}
}

func TestMCPToConfig_Disabled(t *testing.T) {
	t.Parallel()
	got := MCPToConfig([]string{"foo"}, []string{}, nil, nil)
	cfg := got["foo"]
	if cfg.Enabled == nil || *cfg.Enabled {
		t.Fatalf("expected disabled")
	}
}

func TestMCPToConfig_SilentWhenNoToolsProvided(t *testing.T) {
	t.Parallel()
	got := MCPToConfig([]string{"foo"}, []string{"foo"}, nil, nil)
	cfg := got["foo"]
	if cfg.Enabled == nil || !*cfg.Enabled {
		t.Fatalf("expected enabled")
	}
	if cfg.AllowedTools != nil || cfg.AlwaysAllowedTools != nil {
		t.Fatalf("expected silent tool lists; got allowed=%v always=%v", cfg.AllowedTools, cfg.AlwaysAllowedTools)
	}
}

func TestMCPToConfig_BothToolLists(t *testing.T) {
	t.Parallel()
	got := MCPToConfig(
		[]string{"bar"},
		[]string{"bar"},
		map[string][]string{"bar": {"a"}},
		map[string][]string{"bar": {"b"}},
	)
	cfg := got["bar"]
	if !reflect.DeepEqual(cfg.AllowedTools, []string{"a"}) {
		t.Errorf("AllowedTools=%v want [a]", cfg.AllowedTools)
	}
	if !reflect.DeepEqual(cfg.AlwaysAllowedTools, []string{"b"}) {
		t.Errorf("AlwaysAllowedTools=%v want [b]", cfg.AlwaysAllowedTools)
	}
}

// ---------------------------------------------------------------------------
// ResolveCurrentVector tests
// ---------------------------------------------------------------------------

func TestResolveCurrentVector_NilSelector(t *testing.T) {
	t.Parallel()
	inv := []string{"a", "b", "c"}
	got := ResolveCurrentVector(inv, VectorSelector{})
	if len(got) != 3 {
		t.Fatalf("nil selector should return all; got %v", got)
	}
}

func TestResolveCurrentVector_Include(t *testing.T) {
	t.Parallel()
	inc := []string{"a"}
	got := ResolveCurrentVector([]string{"a", "b"}, VectorSelector{Include: &inc})
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("expected [a], got %v", got)
	}
}

func TestResolveCurrentVector_Exclude(t *testing.T) {
	t.Parallel()
	exc := []string{"b"}
	got := ResolveCurrentVector([]string{"a", "b", "c"}, VectorSelector{Exclude: &exc})
	if len(got) != 2 {
		t.Fatalf("expected [a,c], got %v", got)
	}
}

// ---------------------------------------------------------------------------
// VectorEqual tests
// ---------------------------------------------------------------------------

func TestVectorEqual_BothNil(t *testing.T) {
	t.Parallel()
	if !VectorEqual(VectorSelector{}, VectorSelector{}) {
		t.Fatal("both nil should be equal")
	}
}

func TestVectorEqual_NilVsEmpty(t *testing.T) {
	t.Parallel()
	empty := []string{}
	a := VectorSelector{Include: &empty}
	b := VectorSelector{}
	if VectorEqual(a, b) {
		t.Fatal("nil vs empty-include should not be equal")
	}
}

func TestVectorEqual_SameContent(t *testing.T) {
	t.Parallel()
	inc1 := []string{"a", "b"}
	inc2 := []string{"b", "a"}
	a := VectorSelector{Include: &inc1}
	b := VectorSelector{Include: &inc2}
	if !VectorEqual(a, b) {
		t.Fatal("same content (different order) should be equal")
	}
}

func TestVectorEqual_DifferentContent(t *testing.T) {
	t.Parallel()
	inc1 := []string{"a"}
	inc2 := []string{"b"}
	a := VectorSelector{Include: &inc1}
	b := VectorSelector{Include: &inc2}
	if VectorEqual(a, b) {
		t.Fatal("different content should not be equal")
	}
}

// ---------------------------------------------------------------------------
// MCPConfigEqual tests
// ---------------------------------------------------------------------------

func TestMCPConfigEqual_BothEmpty(t *testing.T) {
	t.Parallel()
	if !MCPConfigEqual(map[string]MCPServerConfig{}, map[string]MCPServerConfig{}) {
		t.Fatal("both empty should be equal")
	}
}

func TestMCPConfigEqual_DifferentKeys(t *testing.T) {
	t.Parallel()
	a := map[string]MCPServerConfig{"foo": {Enabled: BoolPtr(true)}}
	b := map[string]MCPServerConfig{"bar": {Enabled: BoolPtr(true)}}
	if MCPConfigEqual(a, b) {
		t.Fatal("different keys should not be equal")
	}
}

func TestMCPConfigEqual_DifferentEnabled(t *testing.T) {
	t.Parallel()
	a := map[string]MCPServerConfig{"foo": {Enabled: BoolPtr(true)}}
	b := map[string]MCPServerConfig{"foo": {Enabled: BoolPtr(false)}}
	if MCPConfigEqual(a, b) {
		t.Fatal("different enabled should not be equal")
	}
}

func TestMCPConfigEqual_NilVsSetEnabled(t *testing.T) {
	t.Parallel()
	a := map[string]MCPServerConfig{"foo": {Enabled: nil}}
	b := map[string]MCPServerConfig{"foo": {Enabled: BoolPtr(true)}}
	if MCPConfigEqual(a, b) {
		t.Fatal("nil vs set enabled should not be equal")
	}
}

func TestMCPConfigEqual_SameToolsDifferentOrder(t *testing.T) {
	t.Parallel()
	a := map[string]MCPServerConfig{"foo": {Enabled: BoolPtr(true), AllowedTools: []string{"b", "a"}}}
	b := map[string]MCPServerConfig{"foo": {Enabled: BoolPtr(true), AllowedTools: []string{"a", "b"}}}
	if !MCPConfigEqual(a, b) {
		t.Fatal("same tools in different order should be equal")
	}
}

func TestMCPConfigEqual_DifferentLengths(t *testing.T) {
	t.Parallel()
	a := map[string]MCPServerConfig{"foo": {}, "bar": {}}
	b := map[string]MCPServerConfig{"foo": {}}
	if MCPConfigEqual(a, b) {
		t.Fatal("different lengths should not be equal")
	}
}
