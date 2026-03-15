package app

import (
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestBuildContentTree_BasicSelection(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 1,
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
		SchemaVersion: 1,
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

func TestBuildContentTree_MCPServers(t *testing.T) {
	t.Parallel()

	f := false
	manifest := config.PackManifest{
		SchemaVersion: 1,
		Name:          "test-pack",
		Root:          ".",
		MCP: config.MCPPack{
			Servers: map[string]config.MCPDefaults{
				"atlassian": {},
				"dope":      {},
			},
		},
	}
	entries := []config.PackEntry{{
		Name: "test-pack",
		MCP: map[string]config.MCPServerConfig{
			"dope": {Enabled: &f},
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
		if item.ID == "dope" && item.Enabled {
			t.Error("dope should be disabled")
		}
		if item.ID == "atlassian" && !item.Enabled {
			t.Error("atlassian should be enabled by default")
		}
	}
}

func TestBuildContentTree_MultiPack(t *testing.T) {
	t.Parallel()

	m1 := config.PackManifest{SchemaVersion: 1, Name: "core", Root: ".", Rules: []string{"rule-a"}}
	m2 := config.PackManifest{SchemaVersion: 1, Name: "extras", Root: ".", Rules: []string{"rule-b", "rule-c"}}

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
		SchemaVersion: 1,
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

func TestApplyContentTree_MCPToggle(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{
		SchemaVersion: 1,
		Name:          "test-pack",
		Root:          ".",
		MCP: config.MCPPack{
			Servers: map[string]config.MCPDefaults{
				"atlassian": {},
				"dope":      {},
			},
		},
	}
	entries := []config.PackEntry{{Name: "test-pack"}}
	packs := []ProfilePackInfo{{Index: 0, Name: "test-pack", Root: "/tmp", Manifest: manifest}}

	tree := BuildContentTree(packs, entries)

	// Disable "dope".
	for i := range tree.Items {
		if tree.Items[i].ID == "dope" {
			tree.Items[i].Enabled = false
		}
	}

	ApplyContentTree(tree, entries)

	mcpCfg := entries[0].MCP
	if mcpCfg == nil {
		t.Fatal("MCP config should be set after apply")
	}
	if dopeCfg, ok := mcpCfg["dope"]; !ok {
		t.Error("dope should be in MCP config")
	} else if dopeCfg.Enabled == nil || *dopeCfg.Enabled {
		t.Error("dope should be disabled")
	}
}

func TestBuildContentTree_EmptyManifest(t *testing.T) {
	t.Parallel()

	manifest := config.PackManifest{SchemaVersion: 1, Name: "empty", Root: "."}
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
		SchemaVersion: 1,
		Name:          "all",
		Root:          ".",
		Rules:         []string{"r1"},
		Agents:        []string{"a1"},
		Workflows:     []string{"w1"},
		Skills:        []string{"s1"},
		MCP: config.MCPPack{
			Servers: map[string]config.MCPDefaults{"m1": {}},
		},
	}
	packs := []ProfilePackInfo{{Index: 0, Name: "all", Root: "/tmp", Manifest: manifest}}
	entries := []config.PackEntry{{Name: "all"}}

	tree := BuildContentTree(packs, entries)

	if len(tree.Items) != 5 {
		t.Fatalf("items = %d, want 5", len(tree.Items))
	}

	// Items should be ordered: rules, agents, workflows, skills, mcp.
	expected := []domain.PackCategory{
		domain.CategoryRules, domain.CategoryAgents, domain.CategoryWorkflows,
		domain.CategorySkills, domain.CategoryMCP,
	}
	for i, item := range tree.Items {
		if item.Category != expected[i] {
			t.Errorf("item[%d].Category = %q, want %q", i, item.Category, expected[i])
		}
	}
}
