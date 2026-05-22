package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestResolveProfilePacks_DiscoversContent(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	// Create a pack with no content fields in the manifest but content on disk.
	packDir := filepath.Join(configDir, "packs", "disco")
	writePackManifest(t, packDir, "disco")

	rulesDir := filepath.Join(packDir, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "found.md"), []byte("# rule"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries := []config.PackEntry{{Name: "disco"}}
	resolved, errs := ResolveProfilePacks(configDir, entries)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved = %d, want 1", len(resolved))
	}
	ids := resolved[0].Manifest.ContentIDs(domain.CategoryRules)
	if len(ids) != 1 || ids[0] != "found" {
		t.Fatalf("Rules = %v, want [found]", ids)
	}
}

func TestBuildContentTree_BasicSelection(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Root:          ".",
		Rules:         []string{"anti-slop", "show-work"},
		Agents:        []string{"reviewer"},
	}
	packs := []ProfilePackInfo{{Index: 0, Name: "test-pack", Root: "/tmp/packs/test-pack", Manifest: manifest}}
	entries := []config.PackEntry{{Name: "test-pack"}} // default selector = include all

	tree := BuildContentTree(packs, entries)

	if len(tree.Packs) != 1 {
		t.Fatalf("packs = %d, want 1", len(tree.Packs))
	}

	// Default VectorSelector (empty Include/Exclude) means all are selected.
	var ruleItems, agentItems []ContentItem
	for _, item := range tree.Items {
		switch item.Category {
		case domain.CategoryRules:
			ruleItems = append(ruleItems, item)
		case domain.CategoryAgents:
			agentItems = append(agentItems, item)
		}
	}
	if len(ruleItems) != 2 {
		t.Fatalf("rules = %d, want 2", len(ruleItems))
	}
	if len(agentItems) != 1 {
		t.Fatalf("agents = %d, want 1", len(agentItems))
	}
	for _, item := range ruleItems {
		if !item.Enabled {
			t.Errorf("rule %q should be enabled by default", item.ID)
		}
	}
}

func TestBuildContentTree_ExcludeFilter(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Root:          ".",
		Rules:         []string{"anti-slop", "show-work", "verbose"},
	}
	excluded := []string{"show-work"}
	entries := []config.PackEntry{{
		Name:  "test-pack",
		Rules: config.VectorSelector{Exclude: &excluded},
	}}
	packs := []ProfilePackInfo{{Index: 0, Name: "test-pack", Root: "/tmp", Manifest: manifest}}

	tree := BuildContentTree(packs, entries)

	for _, item := range tree.Items {
		if item.ID == "show-work" && item.Enabled {
			t.Error("show-work should be disabled via exclude")
		}
		if item.ID == "anti-slop" && !item.Enabled {
			t.Error("anti-slop should remain enabled")
		}
	}
}

func TestBuildContentTree_HooksDisabledOptOut(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "hook-pack",
		Root:          ".",
		Hooks:         []string{"datetime-injector"},
	}
	entries := []config.PackEntry{{
		Name: "hook-pack",
		Hooks: config.HookSelector{
			Enabled: config.BoolPtr(false),
		},
	}}
	packs := []ProfilePackInfo{{Index: 0, Name: "hook-pack", Root: "/tmp", Manifest: manifest}}

	tree := BuildContentTree(packs, entries)

	if len(tree.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(tree.Items))
	}
	item := tree.Items[0]
	if item.Category != domain.CategoryHooks || item.ID != "datetime-injector" {
		t.Fatalf("item = (%s, %s), want hook datetime-injector", item.Category, item.ID)
	}
	if item.Enabled {
		t.Fatal("hook should be disabled when hooks.enabled is false")
	}
}

// TestBuildContentTree_QuietPack pins the TUI tree's selection state against
// quiet-pack semantics — the resolver at internal/config/profile_resolve.go
// treats a nil-include selector on a quiet pack as "include nothing," but
// BuildContentTree used to call ResolveCurrentVector directly and resolve
// the same selector as "include all," producing a tree full of [x] items
// that sync wouldn't actually deliver. Mirrors the quiet-aware logic so
// what the user sees matches what sync ships.
func TestBuildContentTree_QuietPack(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "big-pack",
		Root:          ".",
		Rules:         []string{"alpha", "beta", "gamma"},
		Agents:        []string{"reviewer"},
		MCP:           []string{"srv-a", "srv-b"},
	}
	packs := []ProfilePackInfo{{Index: 0, Name: "big-pack", Root: "/tmp/packs/big-pack", Manifest: manifest}}

	// Quiet pack with no explicit selectors at all — every vector must
	// resolve to nothing selected. MCP too.
	wfInclude := []string{"deploy"}
	entries := []config.PackEntry{{
		Name:  "big-pack",
		Quiet: true,
		Workflows: config.VectorSelector{
			Include: &wfInclude,
		},
	}}

	tree := BuildContentTree(packs, entries)
	enabled := map[string][]string{}
	for _, item := range tree.Items {
		if item.Enabled {
			enabled[string(item.Category)] = append(enabled[string(item.Category)], item.ID)
		}
	}

	if n := len(enabled[string(domain.CategoryRules)]); n != 0 {
		t.Errorf("quiet pack with nil rules include: got %d enabled rules, want 0 (%v)",
			n, enabled[string(domain.CategoryRules)])
	}
	if n := len(enabled[string(domain.CategoryAgents)]); n != 0 {
		t.Errorf("quiet pack with nil agents include: got %d enabled agents, want 0", n)
	}
	if n := len(enabled[string(domain.CategoryMCP)]); n != 0 {
		t.Errorf("quiet pack with no explicit mcp map: got %d enabled servers, want 0 (%v)",
			n, enabled[string(domain.CategoryMCP)])
	}

	// Every item should still appear in the tree (discoverability) — it's
	// just the Enabled flag that flips. Count total items across categories
	// to confirm the tree didn't drop rows.
	rules, agents, mcps := 0, 0, 0
	for _, item := range tree.Items {
		switch item.Category {
		case domain.CategoryRules:
			rules++
		case domain.CategoryAgents:
			agents++
		case domain.CategoryMCP:
			mcps++
		}
	}
	if rules != 3 || agents != 1 || mcps != 2 {
		t.Errorf("tree row counts = rules:%d agents:%d mcp:%d, want 3/1/2 (discoverability)",
			rules, agents, mcps)
	}
}

// TestBuildContentTree_QuietPackExplicitIncludeActivates verifies the
// opt-in escape hatch: a quiet pack's explicit non-empty include list
// activates only those items. Mirrors the resolver's quiet path.
func TestBuildContentTree_QuietPackExplicitIncludeActivates(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "big-pack",
		Root:          ".",
		Rules:         []string{"alpha", "beta", "gamma"},
		MCP:           []string{"srv-a", "srv-b"},
	}
	packs := []ProfilePackInfo{{Index: 0, Name: "big-pack", Root: "/tmp/packs/big-pack", Manifest: manifest}}

	ruleInclude := []string{"alpha"}
	trueVal := true
	entries := []config.PackEntry{{
		Name:  "big-pack",
		Quiet: true,
		Rules: config.VectorSelector{
			Include: &ruleInclude,
		},
		MCP: map[string]config.MCPServerConfig{
			"srv-a": {Enabled: &trueVal},
		},
	}}

	tree := BuildContentTree(packs, entries)
	for _, item := range tree.Items {
		switch item.Category {
		case domain.CategoryRules:
			wantEnabled := item.ID == "alpha"
			if item.Enabled != wantEnabled {
				t.Errorf("rule %q: Enabled=%v, want %v", item.ID, item.Enabled, wantEnabled)
			}
		case domain.CategoryMCP:
			wantEnabled := item.ID == "srv-a"
			if item.Enabled != wantEnabled {
				t.Errorf("mcp %q: Enabled=%v, want %v", item.ID, item.Enabled, wantEnabled)
			}
		}
	}
}

func TestBuildContentTree_GlobSelector(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "glob-pack",
		Root:          ".",
		Rules:         []string{"ops-a", "ops-b", "dev"},
	}
	include := []string{"ops-*"}
	entries := []config.PackEntry{{
		Name:  "glob-pack",
		Rules: config.VectorSelector{Include: &include},
	}}
	packs := []ProfilePackInfo{{Index: 0, Name: "glob-pack", Root: "/tmp", Manifest: manifest}}

	tree := BuildContentTree(packs, entries)
	for _, item := range tree.Items {
		want := item.ID != "dev"
		if item.Enabled != want {
			t.Fatalf("%s enabled=%v, want %v", item.ID, item.Enabled, want)
		}
	}
}

func TestBuildContentTree_MCPServers(t *testing.T) {
	t.Parallel()

	f := false
	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Root:          ".",
		MCP:           []string{"srv-a", "deploy-tool"},
	}
	entries := []config.PackEntry{{
		Name: "test-pack",
		MCP: map[string]config.MCPServerConfig{
			"deploy-tool": {Enabled: &f},
		},
	}}
	packs := []ProfilePackInfo{{Index: 0, Name: "test-pack", Root: "/tmp", Manifest: manifest}}

	tree := BuildContentTree(packs, entries)

	var mcpItems []ContentItem
	for _, item := range tree.Items {
		if item.Category == domain.CategoryMCP {
			mcpItems = append(mcpItems, item)
		}
	}
	if len(mcpItems) != 2 {
		t.Fatalf("mcp items = %d, want 2", len(mcpItems))
	}
	for _, item := range mcpItems {
		if item.ID == "deploy-tool" && item.Enabled {
			t.Error("deploy-tool should be disabled")
		}
		if item.ID == "srv-a" && !item.Enabled {
			t.Error("atlassian should be enabled by default")
		}
	}
}

func TestBuildContentTree_MultiPack(t *testing.T) {
	t.Parallel()

	m1 := config.PackManifest{SchemaVersion: 2, Name: "core", Root: ".", Rules: []string{"rule-a"}}
	m2 := config.PackManifest{SchemaVersion: 2, Name: "extras", Root: ".", Rules: []string{"rule-b", "rule-c"}}

	packs := []ProfilePackInfo{
		{Index: 0, Name: "core", Root: "/tmp/core", Manifest: m1},
		{Index: 1, Name: "extras", Root: "/tmp/extras", Manifest: m2},
	}
	entries := []config.PackEntry{{Name: "core"}, {Name: "extras"}}

	tree := BuildContentTree(packs, entries)

	if len(tree.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(tree.Items))
	}

	packNames := map[string]int{}
	for _, item := range tree.Items {
		packNames[item.PackName]++
	}
	if packNames["core"] != 1 || packNames["extras"] != 2 {
		t.Errorf("pack distribution: %v", packNames)
	}
}

func TestApplyContentTree_RoundTrip(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Root:          ".",
		Rules:         []string{"anti-slop", "show-work"},
	}
	entries := []config.PackEntry{{Name: "test-pack"}}
	packs := []ProfilePackInfo{{Index: 0, Name: "test-pack", Root: "/tmp", Manifest: manifest}}

	tree := BuildContentTree(packs, entries)

	// All should start enabled.
	for i := range tree.Items {
		if !tree.Items[i].Enabled {
			t.Fatalf("item %q not enabled before toggle", tree.Items[i].ID)
		}
	}

	// Disable "show-work".
	for i := range tree.Items {
		if tree.Items[i].ID == "show-work" {
			tree.Items[i].Enabled = false
		}
	}

	ApplyContentTree(tree, entries)

	// The entry should now have an exclude selector for "show-work".
	sel := entries[0].VectorSelectorFor(domain.CategoryRules)
	resolved := config.ResolveCurrentVector(manifest.Rules, *sel)
	resolvedSet := config.ToStringSet(resolved)

	if resolvedSet["show-work"] {
		t.Error("show-work should be excluded after ApplyContentTree")
	}
	if !resolvedSet["anti-slop"] {
		t.Error("anti-slop should remain included")
	}
}

func TestApplyContentTree_QuietPackAllSelectedUsesInclude(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "quiet-pack",
		Root:          ".",
		Rules:         []string{"alpha", "beta"},
	}
	include := []string{"alpha"}
	entries := []config.PackEntry{{
		Name:  "quiet-pack",
		Quiet: true,
		Rules: config.VectorSelector{Include: &include},
	}}
	packs := []ProfilePackInfo{{Index: 0, Name: "quiet-pack", Root: "/tmp", Manifest: manifest}}

	tree := BuildContentTree(packs, entries)
	for i := range tree.Items {
		tree.Items[i].Enabled = true
	}
	ApplyContentTree(tree, entries)

	sel := entries[0].VectorSelectorFor(domain.CategoryRules)
	if sel.Include == nil || len(*sel.Include) != 2 {
		t.Fatalf("quiet all-selected should remain explicit include, got include=%v exclude=%v", sel.Include, sel.Exclude)
	}
	resolved, err := config.ResolveProfileVectorSelection("quiet-pack", domain.CategoryRules, manifest.Rules, *sel, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 2 {
		t.Fatalf("quiet all-selected round trip = %v, want all rules", resolved)
	}
}

func TestApplyContentTree_HookToggleClearsEnabledOptOut(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "hook-pack",
		Root:          ".",
		Hooks:         []string{"datetime-injector", "audit-log"},
	}
	entries := []config.PackEntry{{
		Name: "hook-pack",
		Hooks: config.HookSelector{
			Enabled: config.BoolPtr(false),
		},
	}}
	packs := []ProfilePackInfo{{Index: 0, Name: "hook-pack", Root: "/tmp", Manifest: manifest}}

	tree := BuildContentTree(packs, entries)
	for i := range tree.Items {
		if tree.Items[i].ID == "datetime-injector" {
			tree.Items[i].Enabled = true
		}
	}
	ApplyContentTree(tree, entries)

	if entries[0].Hooks.Enabled != nil {
		t.Fatalf("Hooks.Enabled = %v, want nil so selected hooks can render", *entries[0].Hooks.Enabled)
	}
	selected, err := config.ResolveProfileVectorSelection("hook-pack", domain.CategoryHooks, manifest.Hooks, entries[0].Hooks.VectorSelector, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "datetime-injector" {
		t.Fatalf("selected hooks = %v, want [datetime-injector]", selected)
	}
}

func TestApplyContentTree_MCPToggle(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Root:          ".",
		MCP:           []string{"srv-a", "deploy-tool"},
	}
	entries := []config.PackEntry{{Name: "test-pack"}}
	packs := []ProfilePackInfo{{Index: 0, Name: "test-pack", Root: "/tmp", Manifest: manifest}}

	tree := BuildContentTree(packs, entries)

	// Disable "deploy-tool".
	for i := range tree.Items {
		if tree.Items[i].ID == "deploy-tool" {
			tree.Items[i].Enabled = false
		}
	}

	ApplyContentTree(tree, entries)

	mcpCfg := entries[0].MCP
	if mcpCfg == nil {
		t.Fatal("MCP config should be set after apply")
	}
	if dtCfg, ok := mcpCfg["deploy-tool"]; !ok {
		t.Error("deploy-tool should be in MCP config")
	} else if dtCfg.Enabled == nil || *dtCfg.Enabled {
		t.Error("deploy-tool should be disabled")
	}
}

func TestBuildContentTree_MCPInclusiveMapMatchesResolver(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Root:          ".",
		MCP:           []string{"jira", "slack"},
	}
	entries := []config.PackEntry{{
		Name: "test-pack",
		MCP: map[string]config.MCPServerConfig{
			"slack": {Enabled: config.BoolPtr(true)},
		},
	}}
	packs := []ProfilePackInfo{{Index: 0, Name: "test-pack", Root: "/tmp", Manifest: manifest}}

	tree := BuildContentTree(packs, entries)

	enabled := map[string]bool{}
	for _, item := range tree.Items {
		if item.Category == domain.CategoryMCP {
			enabled[item.ID] = item.Enabled
		}
	}
	if enabled["jira"] {
		t.Fatal("jira should be disabled because a non-disable-only MCP map is inclusive")
	}
	if !enabled["slack"] {
		t.Fatal("slack should be enabled from the explicit MCP map")
	}
}

func TestBuildContentTree_DisabledPackItemsInactive(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Root:          ".",
		Rules:         []string{"anti-slop"},
		MCP:           []string{"jira"},
	}
	entries := []config.PackEntry{{
		Name:    "test-pack",
		Enabled: config.BoolPtr(false),
	}}
	packs := []ProfilePackInfo{{Index: 0, Name: "test-pack", Root: "/tmp", Manifest: manifest}}

	tree := BuildContentTree(packs, entries)

	if len(tree.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(tree.Items))
	}
	for _, item := range tree.Items {
		if item.Enabled {
			t.Fatalf("%s/%s should be inactive while pack is disabled", item.Category, item.ID)
		}
	}
}

func TestApplyContentTree_DisabledPackPreservesSelectors(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Root:          ".",
		Rules:         []string{"anti-slop"},
		MCP:           []string{"jira"},
	}
	entries := []config.PackEntry{{
		Name:    "test-pack",
		Enabled: config.BoolPtr(false),
	}}
	packs := []ProfilePackInfo{{Index: 0, Name: "test-pack", Root: "/tmp", Manifest: manifest}}
	tree := BuildContentTree(packs, entries)
	for i := range tree.Items {
		tree.Items[i].Enabled = true
	}

	ApplyContentTree(tree, entries)

	if entries[0].Rules.Include != nil || entries[0].Rules.Exclude != nil {
		t.Fatalf("rules selector = %+v, want preserved empty selector", entries[0].Rules)
	}
	if entries[0].MCP != nil {
		t.Fatalf("mcp = %+v, want preserved nil map", entries[0].MCP)
	}
}

func TestApplyContentTree_MCPPreservesExplicitEmptyToolLists(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Root:          ".",
		MCP:           []string{"srv-a"},
	}
	entries := []config.PackEntry{{
		Name: "test-pack",
		MCP: map[string]config.MCPServerConfig{
			"srv-a": {
				Enabled:            config.BoolPtr(true),
				AllowedTools:       []string{},
				AlwaysAllowedTools: []string{},
			},
		},
	}}
	packs := []ProfilePackInfo{{Index: 0, Name: "test-pack", Root: "/tmp", Manifest: manifest}}

	tree := BuildContentTree(packs, entries)
	ApplyContentTree(tree, entries)

	srv := entries[0].MCP["srv-a"]
	if srv.AllowedTools == nil {
		t.Fatal("explicit empty allowed_tools override should survive ApplyContentTree")
	}
	if srv.AlwaysAllowedTools == nil {
		t.Fatal("explicit empty always_allowed_tools override should survive ApplyContentTree")
	}
}

func TestApplyContentTree_MCPPreservesDisabledTools(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "test-pack",
		Root:          ".",
		MCP:           []string{"srv-a"},
	}
	entries := []config.PackEntry{{
		Name: "test-pack",
		MCP: map[string]config.MCPServerConfig{
			"srv-a": {
				Enabled:       config.BoolPtr(true),
				DisabledTools: []string{"write"},
			},
		},
	}}
	packs := []ProfilePackInfo{{Index: 0, Name: "test-pack", Root: "/tmp", Manifest: manifest}}

	tree := BuildContentTree(packs, entries)
	ApplyContentTree(tree, entries)

	srv := entries[0].MCP["srv-a"]
	if len(srv.DisabledTools) != 1 || srv.DisabledTools[0] != "write" {
		t.Fatalf("disabled_tools = %v, want [write]", srv.DisabledTools)
	}
}

func TestBuildContentTree_EmptyManifest(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{SchemaVersion: 2, Name: "empty", Root: "."}
	packs := []ProfilePackInfo{{Index: 0, Name: "empty", Root: "/tmp", Manifest: manifest}}
	entries := []config.PackEntry{{Name: "empty"}}

	tree := BuildContentTree(packs, entries)

	if len(tree.Items) != 0 {
		t.Errorf("items = %d, want 0 for empty manifest", len(tree.Items))
	}
}

func TestBuildContentTree_CategoryOrdering(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "all",
		Root:          ".",
		Rules:         []string{"r1"},
		Agents:        []string{"a1"},
		Workflows:     []string{"w1"},
		Skills:        []string{"s1"},
		Hooks:         []string{"h1"},
		Plugins:       []string{"p1"},
		MCP:           []string{"m1"},
	}
	packs := []ProfilePackInfo{{Index: 0, Name: "all", Root: "/tmp", Manifest: manifest}}
	entries := []config.PackEntry{{Name: "all"}}

	tree := BuildContentTree(packs, entries)

	if len(tree.Items) != 7 {
		t.Fatalf("items = %d, want 7", len(tree.Items))
	}

	// Items should follow domain category order.
	expected := []domain.PackCategory{
		domain.CategoryAgents, domain.CategoryHooks, domain.CategoryMCP, domain.CategoryPlugins, domain.CategoryRules,
		domain.CategorySkills, domain.CategoryWorkflows,
	}
	for i, item := range tree.Items {
		if item.Category != expected[i] {
			t.Errorf("item[%d].Category = %q, want %q", i, item.Category, expected[i])
		}
	}
}

// ---------------------------------------------------------------------------
// DetectConflicts tests
// ---------------------------------------------------------------------------

func TestDetectConflicts_SinglePack_NoConflict(t *testing.T) {
	t.Parallel()

	packs := []ProfilePackInfo{
		{Index: 0, Name: "core", Manifest: config.PackManifest{Rules: []string{"r1", "r2"}}},
	}
	entries := []config.PackEntry{{Name: "core"}}
	items := []ContentItem{
		{ID: "r1", Category: domain.CategoryRules, PackIdx: 0, PackName: "core", Enabled: true},
		{ID: "r2", Category: domain.CategoryRules, PackIdx: 0, PackName: "core", Enabled: true},
	}

	result := DetectConflicts(items, packs, entries)

	for i, cs := range result {
		if len(cs.ConflictPacks) > 0 {
			t.Errorf("item[%d] (%s): unexpected ConflictPacks %v", i, items[i].ID, cs.ConflictPacks)
		}
		if cs.IsOverride || cs.IsOverridden {
			t.Errorf("item[%d] (%s): unexpected override state", i, items[i].ID)
		}
	}
}

func TestDetectConflicts_TwoPacks_SameID_Conflict(t *testing.T) {
	t.Parallel()

	packs := []ProfilePackInfo{
		{Index: 0, Name: "base"},
		{Index: 1, Name: "team"},
	}
	entries := []config.PackEntry{{Name: "base"}, {Name: "team"}}
	items := []ContentItem{
		{ID: "shared", Category: domain.CategoryRules, PackIdx: 0, PackName: "base", Enabled: true},
		{ID: "shared", Category: domain.CategoryRules, PackIdx: 1, PackName: "team", Enabled: true},
	}

	result := DetectConflicts(items, packs, entries)

	// Both items should report each other as conflict peers.
	if len(result[0].ConflictPacks) != 1 || result[0].ConflictPacks[0] != "team" {
		t.Errorf("item[0] ConflictPacks = %v, want [team]", result[0].ConflictPacks)
	}
	if len(result[1].ConflictPacks) != 1 || result[1].ConflictPacks[0] != "base" {
		t.Errorf("item[1] ConflictPacks = %v, want [base]", result[1].ConflictPacks)
	}
	// No overrides declared — neither should have override state.
	if result[0].IsOverride || result[0].IsOverridden {
		t.Error("item[0] should not have override state without explicit overrides")
	}
	if result[1].IsOverride || result[1].IsOverridden {
		t.Error("item[1] should not have override state without explicit overrides")
	}
}

func TestDetectConflicts_OverrideDeclared(t *testing.T) {
	t.Parallel()

	packs := []ProfilePackInfo{
		{Index: 0, Name: "base"},
		{Index: 1, Name: "team"},
	}
	entries := []config.PackEntry{
		{Name: "base"},
		{Name: "team", Overrides: config.Overrides{Rules: []string{"shared"}}},
	}
	items := []ContentItem{
		{ID: "shared", Category: domain.CategoryRules, PackIdx: 0, PackName: "base", Enabled: true},
		{ID: "shared", Category: domain.CategoryRules, PackIdx: 1, PackName: "team", Enabled: true},
	}

	result := DetectConflicts(items, packs, entries)

	// team declares the override → IsOverride=true
	if !result[1].IsOverride {
		t.Error("team's 'shared' should be marked as override winner")
	}
	// base is overridden → IsOverridden=true
	if !result[0].IsOverridden {
		t.Error("base's 'shared' should be marked as overridden")
	}
	// Both still report conflict peers.
	if len(result[0].ConflictPacks) != 1 {
		t.Errorf("item[0] should still show conflict peer, got %v", result[0].ConflictPacks)
	}
}

func TestDetectConflicts_DisabledItemExcluded(t *testing.T) {
	t.Parallel()

	packs := []ProfilePackInfo{
		{Index: 0, Name: "base"},
		{Index: 1, Name: "team"},
	}
	entries := []config.PackEntry{{Name: "base"}, {Name: "team"}}
	items := []ContentItem{
		{ID: "shared", Category: domain.CategoryRules, PackIdx: 0, PackName: "base", Enabled: false}, // disabled
		{ID: "shared", Category: domain.CategoryRules, PackIdx: 1, PackName: "team", Enabled: true},
	}

	result := DetectConflicts(items, packs, entries)

	// Disabled item should have no conflict state.
	if len(result[0].ConflictPacks) > 0 || result[0].IsOverride || result[0].IsOverridden {
		t.Errorf("disabled item[0] should have no conflict state, got %+v", result[0])
	}
	// Enabled item has no peer (the only other provider is disabled) → no conflict.
	if len(result[1].ConflictPacks) > 0 {
		t.Errorf("item[1] should have no conflict (peer disabled), got %v", result[1].ConflictPacks)
	}
}

func TestDetectConflicts_OverrideWithoutConflict_IsNoOp(t *testing.T) {
	t.Parallel()

	// Only one pack provides "unique" — declaring an override for it is harmless.
	packs := []ProfilePackInfo{
		{Index: 0, Name: "solo"},
	}
	entries := []config.PackEntry{
		{Name: "solo", Overrides: config.Overrides{Rules: []string{"unique"}}},
	}
	items := []ContentItem{
		{ID: "unique", Category: domain.CategoryRules, PackIdx: 0, PackName: "solo", Enabled: true},
	}

	result := DetectConflicts(items, packs, entries)

	// No conflict exists — override flag should NOT be set (gated by hasConflict).
	if result[0].IsOverride {
		t.Error("override without conflict should not set IsOverride")
	}
}

func TestDetectConflicts_DifferentCategories_NoConflict(t *testing.T) {
	t.Parallel()

	// Same ID in different categories should not conflict.
	packs := []ProfilePackInfo{
		{Index: 0, Name: "pack-a"},
		{Index: 1, Name: "pack-b"},
	}
	entries := []config.PackEntry{{Name: "pack-a"}, {Name: "pack-b"}}
	items := []ContentItem{
		{ID: "shared", Category: domain.CategoryRules, PackIdx: 0, PackName: "pack-a", Enabled: true},
		{ID: "shared", Category: domain.CategoryAgents, PackIdx: 1, PackName: "pack-b", Enabled: true},
	}

	result := DetectConflicts(items, packs, entries)

	for i, cs := range result {
		if len(cs.ConflictPacks) > 0 {
			t.Errorf("item[%d]: cross-category same ID should not conflict, got %v", i, cs.ConflictPacks)
		}
	}
}

func TestDetectConflicts_ThreePacks_Conflict(t *testing.T) {
	t.Parallel()

	packs := []ProfilePackInfo{
		{Index: 0, Name: "alpha"},
		{Index: 1, Name: "beta"},
		{Index: 2, Name: "gamma"},
	}
	entries := []config.PackEntry{{Name: "alpha"}, {Name: "beta"}, {Name: "gamma"}}
	items := []ContentItem{
		{ID: "shared", Category: domain.CategoryRules, PackIdx: 0, PackName: "alpha", Enabled: true},
		{ID: "shared", Category: domain.CategoryRules, PackIdx: 1, PackName: "beta", Enabled: true},
		{ID: "shared", Category: domain.CategoryRules, PackIdx: 2, PackName: "gamma", Enabled: true},
	}

	result := DetectConflicts(items, packs, entries)

	// Each item should list the other two as conflict peers.
	if len(result[0].ConflictPacks) != 2 {
		t.Errorf("alpha: expected 2 conflict peers, got %v", result[0].ConflictPacks)
	}
	if len(result[1].ConflictPacks) != 2 {
		t.Errorf("beta: expected 2 conflict peers, got %v", result[1].ConflictPacks)
	}
	if len(result[2].ConflictPacks) != 2 {
		t.Errorf("gamma: expected 2 conflict peers, got %v", result[2].ConflictPacks)
	}
}

func TestDetectConflicts_CompetingOverrides(t *testing.T) {
	t.Parallel()

	// Two packs both declare overrides for "shared" — the last one wins
	// but both items should be flagged with CompetingOverride.
	packs := []ProfilePackInfo{
		{Index: 0, Name: "alpha"},
		{Index: 1, Name: "beta"},
	}
	entries := []config.PackEntry{
		{Name: "alpha", Overrides: config.Overrides{Rules: []string{"shared"}}},
		{Name: "beta", Overrides: config.Overrides{Rules: []string{"shared"}}},
	}
	items := []ContentItem{
		{ID: "shared", Category: domain.CategoryRules, PackIdx: 0, PackName: "alpha", Enabled: true},
		{ID: "shared", Category: domain.CategoryRules, PackIdx: 1, PackName: "beta", Enabled: true},
	}

	result := DetectConflicts(items, packs, entries)

	// beta wins (last in profile order).
	if !result[1].IsOverride {
		t.Error("beta should be the override winner")
	}
	if !result[0].IsOverridden {
		t.Error("alpha should be overridden")
	}

	// Both should be flagged as competing.
	if !result[0].CompetingOverride {
		t.Error("alpha should have CompetingOverride=true")
	}
	if !result[1].CompetingOverride {
		t.Error("beta should have CompetingOverride=true")
	}
}
