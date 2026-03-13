package domain

import "testing"

func TestRuleFrontmatter_Validate_RequiredFields(t *testing.T) {
	t.Parallel()
	fm := RuleFrontmatter{}
	ws := fm.Validate("my-rule")
	if len(ws) < 2 {
		t.Fatalf("expected warnings for missing name and description, got %v", ws)
	}
}

func TestRuleFrontmatter_Validate_NameMismatch(t *testing.T) {
	t.Parallel()
	fm := RuleFrontmatter{Name: "wrong-name", Description: "d"}
	ws := fm.Validate("my-rule")
	found := false
	for _, w := range ws {
		if w.Field == "name" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected name mismatch warning, got %v", ws)
	}
}

func TestRuleFrontmatter_Validate_OK(t *testing.T) {
	t.Parallel()
	fm := RuleFrontmatter{Name: "my-rule", Description: "does things"}
	ws := fm.Validate("my-rule")
	if len(ws) != 0 {
		t.Fatalf("expected no warnings, got %v", ws)
	}
}

func TestAgentFrontmatter_Validate_RequiredFields(t *testing.T) {
	t.Parallel()
	fm := AgentFrontmatter{}
	ws := fm.Validate("my-agent")
	if len(ws) < 2 {
		t.Fatalf("expected warnings for missing name and description, got %v", ws)
	}
}

func TestAgentFrontmatter_ValidateRefs_UnknownMCPServer(t *testing.T) {
	t.Parallel()
	fm := AgentFrontmatter{Name: "a", Description: "d", MCPServers: []string{"unknown"}}
	known := map[string]struct{}{"atlassian": {}}
	ws := fm.ValidateRefs("a", known, nil)
	if len(ws) == 0 {
		t.Fatal("expected warning for unknown mcp_server")
	}
}

func TestAgentFrontmatter_ValidateRefs_UnknownSkill(t *testing.T) {
	t.Parallel()
	fm := AgentFrontmatter{Name: "a", Description: "d", Skills: []string{"missing"}}
	known := map[string]struct{}{"real-skill": {}}
	ws := fm.ValidateRefs("a", nil, known)
	if len(ws) == 0 {
		t.Fatal("expected warning for unknown skill")
	}
}

func TestAgentFrontmatter_ValidateRefs_OK(t *testing.T) {
	t.Parallel()
	fm := AgentFrontmatter{Name: "a", Description: "d", MCPServers: []string{"atlassian"}, Skills: []string{"triage"}}
	servers := map[string]struct{}{"atlassian": {}}
	skills := map[string]struct{}{"triage": {}}
	ws := fm.ValidateRefs("a", servers, skills)
	if len(ws) != 0 {
		t.Fatalf("expected no warnings, got %v", ws)
	}
}

func TestSkillFrontmatter_Validate_RequiredFields(t *testing.T) {
	t.Parallel()
	fm := SkillFrontmatter{}
	ws := fm.Validate("my-skill")
	if len(ws) < 2 {
		t.Fatalf("expected warnings for missing name and description, got %v", ws)
	}
}

func TestWorkflowFrontmatter_Validate_MissingName(t *testing.T) {
	t.Parallel()
	fm := WorkflowFrontmatter{Description: "does things"}
	ws := fm.Validate("my-wf")
	found := false
	for _, w := range ws {
		if w.Field == "name" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning for missing name, got %v", ws)
	}
}

func TestWorkflowFrontmatter_Validate_MissingDescription(t *testing.T) {
	t.Parallel()
	fm := WorkflowFrontmatter{Name: "my-wf"}
	ws := fm.Validate("my-wf")
	if len(ws) < 1 {
		t.Fatalf("expected warning for missing description, got %v", ws)
	}
}

func TestWorkflowFrontmatter_Validate_OK(t *testing.T) {
	t.Parallel()
	fm := WorkflowFrontmatter{Name: "my-wf", Description: "does things"}
	ws := fm.Validate("my-wf")
	if len(ws) != 0 {
		t.Fatalf("expected no warnings, got %v", ws)
	}
}
