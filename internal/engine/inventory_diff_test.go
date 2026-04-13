package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestDiffInventory_EmptyOnIdentical(t *testing.T) {
	t.Parallel()
	inv := domain.PackInventory{
		Rules:  []string{"a", "b"},
		Skills: map[string]domain.SkillSnapshot{"x": {Description: "does x"}},
	}
	d := DiffInventory("p", inv, inv)
	if !d.Empty() {
		t.Errorf("expected empty diff, got %+v", d)
	}
}

func TestDiffInventory_AddedAndRemoved(t *testing.T) {
	t.Parallel()
	old := domain.PackInventory{
		Rules:  []string{"a", "b"},
		Skills: map[string]domain.SkillSnapshot{"x": {}},
		MCPServers: map[string]domain.MCPServerSnapshot{
			"legacy-server": {},
		},
	}
	newInv := domain.PackInventory{
		Rules:  []string{"a", "c"},
		Skills: map[string]domain.SkillSnapshot{"x": {}},
		MCPServers: map[string]domain.MCPServerSnapshot{
			"fresh-server": {RequiredRefs: []domain.RequiredRef{{Kind: domain.RefKindParam, Name: "api_key"}}},
		},
	}
	d := DiffInventory("my-team-pack", old, newInv)
	if len(d.AddedRules) != 1 || d.AddedRules[0] != "c" {
		t.Errorf("AddedRules = %v, want [c]", d.AddedRules)
	}
	if len(d.RemovedRules) != 1 || d.RemovedRules[0] != "b" {
		t.Errorf("RemovedRules = %v, want [b]", d.RemovedRules)
	}
	if _, ok := d.AddedServers["fresh-server"]; !ok {
		t.Errorf("AddedServers missing 'fresh-server': %+v", d.AddedServers)
	}
	if len(d.RemovedServers) != 1 || d.RemovedServers[0] != "legacy-server" {
		t.Errorf("RemovedServers = %v, want [legacy-server]", d.RemovedServers)
	}
}

func TestDiffInventory_ChangedSkill(t *testing.T) {
	t.Parallel()
	old := domain.PackInventory{
		Skills: map[string]domain.SkillSnapshot{
			"deployer": {Description: "deploys v1", Assets: []string{"deploy.sh"}},
		},
	}
	newInv := domain.PackInventory{
		Skills: map[string]domain.SkillSnapshot{
			"deployer": {Description: "deploys v2", Assets: []string{"deploy.sh", "rollback.sh"}},
		},
	}
	d := DiffInventory("p", old, newInv)
	if len(d.ChangedSkills) != 1 {
		t.Fatalf("expected 1 ChangedSkill, got %v", d.ChangedSkills)
	}
	ch := d.ChangedSkills["deployer"]
	if ch.OldDescription != "deploys v1" || ch.NewDescription != "deploys v2" {
		t.Errorf("description diff = %q → %q", ch.OldDescription, ch.NewDescription)
	}
	if len(ch.AddedAssets) != 1 || ch.AddedAssets[0] != "rollback.sh" {
		t.Errorf("AddedAssets = %v", ch.AddedAssets)
	}
	if len(ch.RemovedAssets) != 0 {
		t.Errorf("RemovedAssets = %v", ch.RemovedAssets)
	}
}

func TestDiffInventory_ChangedServer(t *testing.T) {
	t.Parallel()
	old := domain.PackInventory{
		MCPServers: map[string]domain.MCPServerSnapshot{
			"api": {
				AvailableTools: []string{"get", "list"},
				RequiredRefs:   []domain.RequiredRef{{Kind: domain.RefKindParam, Name: "base_url"}},
			},
		},
	}
	newInv := domain.PackInventory{
		MCPServers: map[string]domain.MCPServerSnapshot{
			"api": {
				AvailableTools: []string{"create", "get", "list"},
				RequiredRefs: []domain.RequiredRef{
					{Kind: domain.RefKindParam, Name: "base_url"},
					{Kind: domain.RefKindEnv, Name: "API_TOKEN"},
				},
			},
		},
	}
	d := DiffInventory("p", old, newInv)
	if len(d.ChangedServers) != 1 {
		t.Fatalf("expected 1 ChangedServer, got %v", d.ChangedServers)
	}
	ch := d.ChangedServers["api"]
	if len(ch.AddedTools) != 1 || ch.AddedTools[0] != "create" {
		t.Errorf("AddedTools = %v", ch.AddedTools)
	}
	if len(ch.AddedRefs) != 1 || ch.AddedRefs[0].Kind != domain.RefKindEnv || ch.AddedRefs[0].Name != "API_TOKEN" {
		t.Errorf("AddedRefs = %+v", ch.AddedRefs)
	}
	if len(ch.RemovedTools) != 0 || len(ch.RemovedRefs) != 0 {
		t.Errorf("unexpected removals: tools=%v refs=%+v", ch.RemovedTools, ch.RemovedRefs)
	}
}

func TestFormatDriftReport_SplitByBrokenRefs(t *testing.T) {
	t.Parallel()
	diff := domain.InventoryDiff{
		PackName:       "my-team-pack",
		AddedSkills:    map[string]domain.SkillSnapshot{"fresh-skill": {Description: "new thing"}},
		RemovedSkills:  []string{"stale-skill"},
		RemovedServers: []string{"stale-server"},
	}
	broken := []domain.BrokenRef{
		{PackName: "my-team-pack", Category: domain.CategorySkills, Direction: "include", ID: "stale-skill"},
		{PackName: "my-team-pack", Category: domain.CategoryMCP, Direction: "", ID: "stale-server"},
	}
	var buf bytes.Buffer
	FormatDriftReport(&buf, diff, broken, "v0.2.1", "v0.3.0")
	out := buf.String()

	wantSubstrings := []string{
		"Drift detected in my-team-pack (v0.2.1 → v0.3.0):",
		"Removed (affects your profile):",
		"stale-skill",
		"stale-server",
		"referenced via skills.include",
		"referenced via mcp.stale-server",
		"Added:",
		"fresh-skill",
		"new thing",
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(out, s) {
			t.Errorf("drift output missing %q:\n%s", s, out)
		}
	}

	// Should NOT have an "Other" section because everything removed is in brokenRefs.
	if strings.Contains(out, "Removed (other)") {
		t.Errorf("unexpected 'Removed (other)' section:\n%s", out)
	}
}

func TestFormatDriftReport_ChangedSections(t *testing.T) {
	t.Parallel()
	diff := domain.InventoryDiff{
		PackName: "p",
		ChangedSkills: map[string]domain.SkillChange{
			"deployer": {
				OldDescription: "deploys v1",
				NewDescription: "deploys v2",
				AddedAssets:    []string{"rollback.sh"},
			},
		},
		ChangedServers: map[string]domain.MCPServerChange{
			"api": {
				AddedTools: []string{"create"},
				AddedRefs:  []domain.RequiredRef{{Kind: domain.RefKindEnv, Name: "API_TOKEN"}},
			},
		},
	}
	var buf bytes.Buffer
	FormatDriftReport(&buf, diff, nil, "", "")
	out := buf.String()

	wantSubstrings := []string{
		"Changed:",
		"~ skills   deployer",
		`description: "deploys v1" → "deploys v2"`,
		"+ship: rollback.sh",
		"~ mcp      api",
		"+tool: create",
		"+requires: {env:API_TOKEN}",
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(out, s) {
			t.Errorf("drift output missing %q:\n%s", s, out)
		}
	}
}

func TestFormatDriftReport_EmptyDiffNoOutput(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	FormatDriftReport(&buf, domain.InventoryDiff{PackName: "p"}, nil, "", "")
	if buf.Len() != 0 {
		t.Errorf("empty diff produced output: %q", buf.String())
	}
}

func TestFormatDriftReport_OtherRemovals(t *testing.T) {
	t.Parallel()
	diff := domain.InventoryDiff{
		PackName:     "p",
		RemovedRules: []string{"stale-rule"},
	}
	var buf bytes.Buffer
	FormatDriftReport(&buf, diff, nil, "", "")
	out := buf.String()
	if !strings.Contains(out, "Removed (other):") {
		t.Errorf("expected 'Removed (other)' section:\n%s", out)
	}
	if strings.Contains(out, "affects your profile") {
		t.Errorf("unexpected 'affects your profile' section with no broken refs:\n%s", out)
	}
}

func TestBuildInventories_CoversAllCategories(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	packRoot := filepath.Join(configDir, "packs", "mypack")
	if err := os.MkdirAll(filepath.Join(packRoot, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packRoot, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packRoot, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packRoot, "skills", "skill-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packRoot, "mcp"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := config.PackManifest{
		SchemaVersion: 1,
		Name:          "mypack",
		Version:       "1.0.0",
		Root:          ".",
		Rules:         []string{"rule-a", "rule-b"},
		Agents:        []string{"agent-a"},
		Workflows:     []string{"flow-a"},
		Skills:        []string{"skill-a"},
		MCP: config.MCPPack{Servers: map[string]config.MCPDefaults{
			"server-a": {DefaultAllowedTools: []string{"tool_x"}},
		}},
	}
	if err := config.SavePackManifest(filepath.Join(packRoot, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"rule-a.md", "rule-b.md"} {
		if err := os.WriteFile(filepath.Join(packRoot, "rules", file), []byte("---\nname: test\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(packRoot, "agents", "agent-a.md"), []byte("---\nname: agent-a\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "workflows", "flow-a.md"), []byte("---\nname: flow-a\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "skills", "skill-a", "SKILL.md"), []byte("---\nname: skill-a\ndescription: does a thing\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "skills", "skill-a", "helper.sh"), []byte("echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "skills", "skill-a", "template.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "mcp", "server-a.json"), []byte(`{
  "name": "server-a",
  "transport": "stdio",
  "command": ["run", "{params.base_url}"],
  "available_tools": ["tool_x", "tool_y"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	profile := domain.Profile{
		Packs: []domain.Pack{{
			Name:      "mypack",
			Root:      packRoot,
			Rules:     []domain.Rule{{Name: "rule-a"}, {Name: "rule-b"}},
			Agents:    []domain.Agent{{Name: "agent-a"}},
			Workflows: []domain.Workflow{{Name: "flow-a"}},
			Skills: []domain.Skill{{
				Name:        "skill-a",
				Frontmatter: domain.SkillFrontmatter{Description: "does a thing"},
				Assets:      []string{"helper.sh", "template.txt"},
			}},
		}},
		MCPServers: []domain.MCPServer{
			{
				Name:                "server-a",
				SourcePack:          "mypack",
				AvailableTools:      []string{"tool_x", "tool_y"},
				DefaultAllowedTools: []string{"tool_x"},
				RequiredRefs:        []domain.RequiredRef{{Kind: domain.RefKindParam, Name: "base_url"}},
			},
			{Name: "other-server", SourcePack: "different-pack"},
		},
	}

	now, _ := time.Parse(time.RFC3339, "2026-04-12T00:00:00Z")
	inventories, err := BuildInventories(New(nil, nil), configDir, profile, now)
	if err != nil {
		t.Fatalf("BuildInventories: %v", err)
	}
	inv, ok := inventories["mypack"]
	if !ok {
		t.Fatalf("mypack inventory missing")
	}
	if len(inv.Rules) != 2 || inv.Rules[0] != "rule-a" || inv.Rules[1] != "rule-b" {
		t.Errorf("Rules = %v", inv.Rules)
	}
	if len(inv.Agents) != 1 || inv.Agents[0] != "agent-a" {
		t.Errorf("Agents = %v", inv.Agents)
	}
	skill, ok := inv.Skills["skill-a"]
	if !ok {
		t.Fatalf("skill-a snapshot missing: %v", inv.Skills)
	}
	if skill.Description != "does a thing" {
		t.Errorf("skill description = %q", skill.Description)
	}
	if len(skill.Assets) != 2 || skill.Assets[0] != "helper.sh" || skill.Assets[1] != "template.txt" {
		t.Errorf("skill assets = %v", skill.Assets)
	}
	if len(inv.MCPServers) != 1 {
		t.Errorf("MCPServers = %v, want 1 entry", inv.MCPServers)
	}
	snap, ok := inv.MCPServers["server-a"]
	if !ok {
		t.Fatalf("MCPServers missing server-a")
	}
	if len(snap.AvailableTools) != 2 || snap.AvailableTools[0] != "tool_x" {
		t.Errorf("AvailableTools = %v", snap.AvailableTools)
	}
	if len(snap.DefaultAllowedTools) != 1 || snap.DefaultAllowedTools[0] != "tool_x" {
		t.Errorf("DefaultAllowedTools = %v", snap.DefaultAllowedTools)
	}
	if len(snap.RequiredRefs) != 1 || snap.RequiredRefs[0].Kind != domain.RefKindParam || snap.RequiredRefs[0].Name != "base_url" {
		t.Errorf("RequiredRefs = %+v", snap.RequiredRefs)
	}
	if _, ok := inv.MCPServers["other-server"]; ok {
		t.Errorf("MCPServers should not contain other pack's server")
	}
}
