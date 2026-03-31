package harness

import (
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestCheckPromotionCollisions_NoCollision(t *testing.T) {
	t.Parallel()
	skills := []domain.Skill{{Name: "alpha"}}
	workflows := []domain.Workflow{{Name: "beta"}}
	agents := []domain.Agent{{Name: "gamma"}}

	if err := CheckPromotionCollisions(skills, workflows, agents); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckPromotionCollisions_SkillWorkflow(t *testing.T) {
	t.Parallel()
	skills := []domain.Skill{{Name: "deploy"}}
	workflows := []domain.Workflow{{Name: "deploy"}}

	err := CheckPromotionCollisions(skills, workflows, nil)
	if err == nil {
		t.Fatal("expected collision error for skill+workflow with same name")
	}
	if want := `name collision: skill "deploy" and workflow "deploy"`; !contains(err.Error(), want) {
		t.Fatalf("error message: got %q, want substring %q", err.Error(), want)
	}
}

func TestCheckPromotionCollisions_SkillAgent(t *testing.T) {
	t.Parallel()
	skills := []domain.Skill{{Name: "review"}}
	agents := []domain.Agent{{Name: "review"}}

	err := CheckPromotionCollisions(skills, nil, agents)
	if err == nil {
		t.Fatal("expected collision error for skill+agent with same name")
	}
	if want := `name collision: skill "review" and agent "review"`; !contains(err.Error(), want) {
		t.Fatalf("error message: got %q, want substring %q", err.Error(), want)
	}
}

func TestCheckPromotionCollisions_WorkflowAgent(t *testing.T) {
	t.Parallel()
	workflows := []domain.Workflow{{Name: "triage"}}
	agents := []domain.Agent{{Name: "triage"}}

	err := CheckPromotionCollisions(nil, workflows, agents)
	if err == nil {
		t.Fatal("expected collision error for workflow+agent with same name")
	}
	if want := `name collision: workflow "triage" and agent "triage"`; !contains(err.Error(), want) {
		t.Fatalf("error message: got %q, want substring %q", err.Error(), want)
	}
}

func TestCheckPromotionCollisions_NilSlices(t *testing.T) {
	t.Parallel()
	if err := CheckPromotionCollisions(nil, nil, nil); err != nil {
		t.Fatalf("unexpected error for nil slices: %v", err)
	}
}

func TestStripDescPrefix_Workflow(t *testing.T) {
	t.Parallel()
	got := StripDescPrefix("[Workflow] Fix deployment issues")
	if got != "Fix deployment issues" {
		t.Fatalf("got %q, want %q", got, "Fix deployment issues")
	}
}

func TestStripDescPrefix_Agent(t *testing.T) {
	t.Parallel()
	got := StripDescPrefix("[Agent] Code review specialist")
	if got != "Code review specialist" {
		t.Fatalf("got %q, want %q", got, "Code review specialist")
	}
}

func TestStripDescPrefix_NoPrefix(t *testing.T) {
	t.Parallel()
	got := StripDescPrefix("Plain description")
	if got != "Plain description" {
		t.Fatalf("got %q, want %q", got, "Plain description")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
