package domain

import "testing"

func TestPackInventory_Contains(t *testing.T) {
	t.Parallel()
	inv := PackInventory{
		Rules:     []string{"rule-a", "rule-b"},
		Workflows: []string{"flow-a"},
		Agents:    []string{"agent-a"},
		Skills: map[string]SkillSnapshot{
			"skill-a": {Description: "does a thing"},
		},
		MCPServers: map[string]MCPServerSnapshot{
			"server-a": {},
		},
	}

	cases := []struct {
		category PackCategory
		id       string
		want     bool
	}{
		{CategoryRules, "rule-a", true},
		{CategoryRules, "not-there", false},
		{CategorySkills, "skill-a", true},
		{CategoryWorkflows, "flow-a", true},
		{CategoryAgents, "agent-a", true},
		{CategoryMCP, "server-a", true},
		{CategoryMCP, "server-b", false},
		{CategoryPrompts, "anything", false}, // unsupported category returns false
	}

	for _, tc := range cases {
		t.Run(string(tc.category)+"/"+tc.id, func(t *testing.T) {
			if got := inv.Contains(tc.category, tc.id); got != tc.want {
				t.Errorf("Contains(%s, %q) = %v, want %v", tc.category, tc.id, got, tc.want)
			}
		})
	}
}

func TestInventoryDiff_Empty(t *testing.T) {
	t.Parallel()
	d := InventoryDiff{}
	if !d.Empty() {
		t.Error("zero-value diff should be Empty")
	}
	d.AddedRules = []string{"x"}
	if d.Empty() {
		t.Error("diff with added rule should not be Empty")
	}
}

func TestSortedCopy(t *testing.T) {
	t.Parallel()
	in := []string{"c", "a", "b"}
	out := SortedCopy(in)
	if len(out) != 3 || out[0] != "a" || out[1] != "b" || out[2] != "c" {
		t.Errorf("SortedCopy returned %v, want [a b c]", out)
	}
	// Input should be unchanged.
	if in[0] != "c" {
		t.Error("SortedCopy mutated input slice")
	}
}
