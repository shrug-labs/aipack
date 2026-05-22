package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestCheckPromotionCollisions_NoCollision(t *testing.T) {
	t.Parallel()
	skills := []domain.Skill{{Name: "alpha"}}
	workflows := []domain.Workflow{{Name: "beta"}}
	agents := []domain.Agent{{Name: "gamma"}}

	if err := CheckPromotionCollisions(false, skills, workflows, agents); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckPromotionCollisions_NamespacedAllowsCrossPackSourceIDOverlap(t *testing.T) {
	t.Parallel()
	skills := []domain.Skill{{Name: "deploy", SourcePack: "pack-a"}}
	workflows := []domain.Workflow{{Name: "deploy", SourcePack: "pack-b"}}
	agents := []domain.Agent{{Name: "deploy", SourcePack: "pack-c"}}

	if err := CheckPromotionCollisions(true, skills, workflows, agents); err != nil {
		t.Fatalf("unexpected namespaced collision: %v", err)
	}
}

func TestCheckPromotionCollisions_NamespacedSamePackSourceIDCollides(t *testing.T) {
	t.Parallel()
	skills := []domain.Skill{{Name: "deploy", SourcePack: "pack-a"}}
	workflows := []domain.Workflow{{Name: "deploy", SourcePack: "pack-a"}}

	err := CheckPromotionCollisions(true, skills, workflows, nil)
	if err == nil {
		t.Fatal("expected namespaced collision error for same-pack skill+workflow with same name")
	}
	if want := `name collision: skill "deploy" and workflow "deploy"`; !contains(err.Error(), want) {
		t.Fatalf("error message: got %q, want substring %q", err.Error(), want)
	}
}

func TestCheckPromotionCollisions_SkillWorkflow(t *testing.T) {
	t.Parallel()
	skills := []domain.Skill{{Name: "deploy"}}
	workflows := []domain.Workflow{{Name: "deploy"}}

	err := CheckPromotionCollisions(false, skills, workflows, nil)
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

	err := CheckPromotionCollisions(false, skills, nil, agents)
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

	err := CheckPromotionCollisions(false, nil, workflows, agents)
	if err == nil {
		t.Fatal("expected collision error for workflow+agent with same name")
	}
	if want := `name collision: workflow "triage" and agent "triage"`; !contains(err.Error(), want) {
		t.Fatalf("error message: got %q, want substring %q", err.Error(), want)
	}
}

func TestCheckPromotionCollisions_NilSlices(t *testing.T) {
	t.Parallel()
	if err := CheckPromotionCollisions(false, nil, nil, nil); err != nil {
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

// TestCapturePromotedContent_FlatOnly verifies skills/agents/workflows are
// captured only from direct child dirs of skillsDir. Sub-grouped dirs
// (`team/reviewer/SKILL.md`) are silently ignored; bundled-asset SKILL.md
// nested inside an outer skill (`outer/fixtures/inner/SKILL.md`) is also
// ignored — the outer skill keeps its bundled assets without producing a
// duplicate skill entry.
func TestCapturePromotedContent_FlatOnly(t *testing.T) {
	t.Parallel()
	skillsDir := t.TempDir()

	writeSkill := func(relDir string, fm PromotedFrontmatter, body string) {
		t.Helper()
		full := filepath.Join(skillsDir, relDir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		content := BuildPromotedMD(fm, body)
		if err := os.WriteFile(filepath.Join(full, "SKILL.md"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Flat plain skill — captured.
	writeSkill("alpha", PromotedFrontmatter{
		Name: "alpha", Description: "Alpha skill",
	}, "Alpha body")
	// Flat promoted agent — captured.
	writeSkill("reviewer", PromotedFrontmatter{
		Name: "reviewer", Description: "[Agent] Reviewer", SourceType: SourceTypeAgent,
	}, "Review the code")
	// Flat promoted workflow — captured.
	writeSkill("ship", PromotedFrontmatter{
		Name: "ship", Description: "[Workflow] Ship", SourceType: SourceTypeWorkflow,
	}, "Ship step 1\nShip step 2")
	// Sub-grouped skill — must be silently ignored.
	writeSkill("team/should-skip", PromotedFrontmatter{
		Name: "should-skip", Description: "Nested — must be ignored",
	}, "skip body")
	// Outer skill with a bundled-asset SKILL.md inside its tree — outer
	// captured, inner must not appear.
	writeSkill("outer", PromotedFrontmatter{
		Name: "outer", Description: "Outer skill",
	}, "Outer body")
	writeSkill("outer/fixtures/inner", PromotedFrontmatter{
		Name: "inner", Description: "Inner fixture",
	}, "Inner body")
	// Bare dir without SKILL.md — silently skipped.
	if err := os.MkdirAll(filepath.Join(skillsDir, "no-skill"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := &CaptureResult{}
	CapturePromotedContent(skillsDir, nil, res)

	if len(res.Agents) != 1 || res.Agents[0].Name != "reviewer" {
		t.Fatalf("agents: want [reviewer], got %v", res.Agents)
	}
	if len(res.Workflows) != 1 || res.Workflows[0].Name != "ship" {
		t.Fatalf("workflows: want [ship], got %v", res.Workflows)
	}

	writeDsts := map[string]bool{}
	for _, w := range res.Writes {
		writeDsts[filepath.ToSlash(w.Dst)] = true
	}
	for _, want := range []string{"agents/reviewer.md", "workflows/ship.md"} {
		if !writeDsts[want] {
			t.Errorf("missing Write for %q; got %v", want, writeDsts)
		}
	}

	gotSkillNames := map[string]bool{}
	for _, s := range res.Skills {
		gotSkillNames[s.Name] = true
	}
	for _, want := range []string{"alpha", "outer"} {
		if !gotSkillNames[want] {
			t.Errorf("missing plain skill %q; got %v", want, gotSkillNames)
		}
	}
	for _, unwanted := range []string{"no-skill", "team", "should-skip", "team/should-skip", "outer/fixtures/inner"} {
		if gotSkillNames[unwanted] {
			t.Errorf("unwanted plain skill %q present; got %v", unwanted, gotSkillNames)
		}
	}
	if len(res.Skills) != 2 {
		t.Errorf("expected 2 plain skills (alpha, outer); got %d: %v", len(res.Skills), res.Skills)
	}

	copyDsts := map[string]bool{}
	for _, c := range res.Copies {
		if c.Kind != domain.CopyKindDir {
			t.Errorf("plain-skill copy kind = %v, want CopyKindDir (%q)", c.Kind, c.Dst)
		}
		copyDsts[filepath.ToSlash(c.Dst)] = true
	}
	for _, want := range []string{"skills/alpha", "skills/outer"} {
		if !copyDsts[want] {
			t.Errorf("missing plain-skill copy Dst %q; got %v", want, copyDsts)
		}
	}
}

// TestCapturePromotedContent_NonexistentDir verifies the function returns
// cleanly when skillsDir does not exist (harness layout may omit it).
func TestCapturePromotedContent_NonexistentDir(t *testing.T) {
	t.Parallel()
	res := &CaptureResult{}
	CapturePromotedContent("/nonexistent/skillsDir", nil, res)
	if len(res.Skills) != 0 || len(res.Agents) != 0 || len(res.Workflows) != 0 ||
		len(res.Copies) != 0 || len(res.Writes) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("expected empty result for missing dir, got %+v", res)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := range len(s) - len(sub) + 1 {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
