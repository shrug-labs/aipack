package engine

import (
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestStripFrontmatterKeys_Agent_RemovesHarness(t *testing.T) {
	t.Parallel()
	fm := domain.AgentFrontmatter{
		Name:        "explorer",
		Description: "Fast exploration",
		Tools:       []string{"Read", "Grep"},
		Harness: map[string]map[string]any{
			"codex": {"model": "o3"},
		},
	}
	out, err := StripFrontmatterKeys(fm, "harness")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "harness") {
		t.Fatalf("should strip harness key, got:\n%s", s)
	}
	if !strings.Contains(s, "explorer") {
		t.Fatal("should preserve name")
	}
	if !strings.Contains(s, "Read") {
		t.Fatal("should preserve tools")
	}
}

func TestStripFrontmatterKeys_Rule_RemovesPaths(t *testing.T) {
	t.Parallel()
	fm := domain.RuleFrontmatter{
		Name:        "my-rule",
		Description: "Does things",
		Paths:       []string{"src/**/*.go"},
	}
	out, err := StripFrontmatterKeys(fm, "paths")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "paths") {
		t.Fatalf("should strip paths key, got:\n%s", s)
	}
	if !strings.Contains(s, "my-rule") {
		t.Fatal("should preserve name")
	}
}

func TestStripFrontmatterKeys_NoKeysToStrip(t *testing.T) {
	t.Parallel()
	fm := domain.AgentFrontmatter{
		Name:        "simple",
		Description: "No harness-specific fields",
	}
	out, err := StripFrontmatterKeys(fm, "harness")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "simple") {
		t.Fatal("should preserve content when nothing to strip")
	}
}

func TestStripFrontmatterKeys_MultipleKeys(t *testing.T) {
	t.Parallel()
	fm := domain.AgentFrontmatter{
		Name:        "test",
		Description: "test agent",
		Tools:       []string{"Read"},
		Harness: map[string]map[string]any{
			"codex": {"model": "o3"},
		},
	}
	out, err := StripFrontmatterKeys(fm, "harness", "tools")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "harness") {
		t.Fatal("should strip harness")
	}
	if strings.Contains(s, "tools") {
		t.Fatal("should strip tools")
	}
	if !strings.Contains(s, "test") {
		t.Fatal("should preserve name")
	}
}
