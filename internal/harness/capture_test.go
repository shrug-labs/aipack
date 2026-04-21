package harness_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
)

func TestCaptureContentDir_Basic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "alpha.md"), []byte("alpha-body"), 0o644)
	os.WriteFile(filepath.Join(dir, "beta.md"), []byte("beta-body"), 0o644)
	os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("not md"), 0o644)
	os.Mkdir(filepath.Join(dir, "subdir.md"), 0o755) // directory, should be skipped

	var parsed []string
	copies, warnings := harness.CaptureContentDir(dir, "rules", ".md", domain.CategoryRules,
		func(raw []byte, name, srcPath string) error {
			parsed = append(parsed, name)
			return nil
		})

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(copies) != 2 {
		t.Fatalf("expected 2 copies, got %d", len(copies))
	}
	for _, c := range copies {
		if c.Kind != domain.CopyKindFile {
			t.Fatalf("expected CopyKindFile, got %v", c.Kind)
		}
		base := filepath.Base(c.Dst)
		if filepath.Dir(c.Dst) != "rules" {
			t.Fatalf("expected Dst dir 'rules', got %q", c.Dst)
		}
		if base != "alpha.md" && base != "beta.md" {
			t.Fatalf("unexpected Dst filename %q", base)
		}
	}
	if len(parsed) != 2 {
		t.Fatalf("expected 2 parsed items, got %d", len(parsed))
	}
}

func TestCaptureContentDir_ParseError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad.md"), []byte("bad"), 0o644)
	os.WriteFile(filepath.Join(dir, "good.md"), []byte("good"), 0o644)

	var parsed []string
	copies, warnings := harness.CaptureContentDir(dir, "agents", ".md", domain.CategoryAgents,
		func(raw []byte, name, srcPath string) error {
			if name == "bad" {
				return fmt.Errorf("invalid content")
			}
			parsed = append(parsed, name)
			return nil
		})

	if len(copies) != 2 {
		t.Fatalf("expected 2 copies (both files), got %d", len(copies))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if len(parsed) != 1 || parsed[0] != "good" {
		t.Fatalf("expected only 'good' parsed, got %v", parsed)
	}
}

func TestCaptureContentDir_NonexistentDir(t *testing.T) {
	t.Parallel()
	copies, warnings := harness.CaptureContentDir("/nonexistent/path", "rules", ".md", domain.CategoryRules,
		func(raw []byte, name, srcPath string) error { return nil })

	if len(copies) != 0 {
		t.Fatalf("expected 0 copies, got %d", len(copies))
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for nonexistent dir, got %v", warnings)
	}
}

func TestCaptureContentDir_CustomExt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "rule-a.rules"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "skip.md"), []byte("b"), 0o644)

	var parsed []string
	copies, _ := harness.CaptureContentDir(dir, "rules", ".rules", domain.CategoryRules,
		func(raw []byte, name, srcPath string) error {
			parsed = append(parsed, name)
			return nil
		})

	if len(copies) != 1 {
		t.Fatalf("expected 1 copy, got %d", len(copies))
	}
	if len(parsed) != 1 || parsed[0] != "rule-a" {
		t.Fatalf("expected 'rule-a' parsed, got %v", parsed)
	}
}

// TestCaptureContentDir_RuleEncodingRoundTrip verifies an encoded rule
// filename in the harness decodes back to its slashed canonical id and
// produces a slashed-path Dst (the pack-relative location the rule would
// occupy if saved back). This is the round-trip contract for nested rule
// authoring (`team-a/triage` ↔ `team-a__triage.md`).
func TestCaptureContentDir_RuleEncodingRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "flat.md"), []byte("body"), 0o644)
	os.WriteFile(filepath.Join(dir, "team-a__triage.md"), []byte("body"), 0o644)
	os.WriteFile(filepath.Join(dir, "team-b__sub__deep.md"), []byte("body"), 0o644)

	var parsed []string
	copies, warnings := harness.CaptureContentDir(dir, "rules", ".md", domain.CategoryRules,
		func(raw []byte, name, srcPath string) error {
			parsed = append(parsed, name)
			return nil
		})

	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}

	wantNames := map[string]bool{"flat": true, "team-a/triage": true, "team-b/sub/deep": true}
	for _, n := range parsed {
		if !wantNames[n] {
			t.Errorf("unexpected canonical name %q (got %v)", n, parsed)
		}
	}
	if len(parsed) != len(wantNames) {
		t.Errorf("got %d parsed names, want %d: %v", len(parsed), len(wantNames), parsed)
	}

	wantDsts := map[string]bool{
		"rules/flat.md":            true,
		"rules/team-a/triage.md":   true,
		"rules/team-b/sub/deep.md": true,
	}
	for _, c := range copies {
		slashed := filepath.ToSlash(c.Dst)
		if !wantDsts[slashed] {
			t.Errorf("unexpected Dst %q", slashed)
		}
	}
}

// TestCaptureContentDir_NonRuleNoEncoding verifies that for non-rule
// categories, the leaf basename flows through verbatim — no `__` decoding,
// flat Dst — preserving the invocation handle as the canonical id.
func TestCaptureContentDir_NonRuleNoEncoding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "team__deploy.md"), []byte("body"), 0o644)

	var parsed []string
	copies, _ := harness.CaptureContentDir(dir, "workflows", ".md", domain.CategoryWorkflows,
		func(raw []byte, name, srcPath string) error {
			parsed = append(parsed, name)
			return nil
		})

	if len(parsed) != 1 || parsed[0] != "team__deploy" {
		t.Fatalf("expected ['team__deploy'] (literal, no decode), got %v", parsed)
	}
	if len(copies) != 1 || filepath.ToSlash(copies[0].Dst) != "workflows/team__deploy.md" {
		t.Fatalf("expected flat Dst workflows/team__deploy.md, got %q", copies[0].Dst)
	}
}

func TestCaptureContentDir_IgnoresSubdirs(t *testing.T) {
	t.Parallel()
	// Non-recursive: any .md file living inside a subdirectory must be
	// silently ignored — the harness side is flat for every category.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ignored-subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "flat.md"), []byte("flat"), 0o644)
	os.WriteFile(filepath.Join(dir, "ignored-subdir", "nested.md"), []byte("nested"), 0o644)

	var parsed []string
	copies, _ := harness.CaptureContentDir(dir, "rules", ".md", domain.CategoryRules,
		func(raw []byte, name, srcPath string) error {
			parsed = append(parsed, name)
			return nil
		})

	if len(parsed) != 1 || parsed[0] != "flat" {
		t.Fatalf("expected only ['flat'], got %v", parsed)
	}
	if len(copies) != 1 {
		t.Fatalf("expected 1 copy, got %d", len(copies))
	}
}

func TestCaptureSkills_FlatOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Top-level skill — captured.
	topSkill := filepath.Join(dir, "alpha")
	if err := os.MkdirAll(topSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(topSkill, "SKILL.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bundled-asset subdir of a skill containing SKILL.md — must be ignored.
	innerFixture := filepath.Join(topSkill, "fixtures", "inner")
	if err := os.MkdirAll(innerFixture, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(innerFixture, "SKILL.md"), []byte("inner"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Group-style subdirectory containing a SKILL.md — must be ignored
	// (skills are flat; only direct children of skillsDir count).
	if err := os.MkdirAll(filepath.Join(dir, "group", "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "group", "beta", "SKILL.md"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bare dir without SKILL.md — silently skipped.
	if err := os.MkdirAll(filepath.Join(dir, "no-skill"), 0o755); err != nil {
		t.Fatal(err)
	}

	copies, skills, warnings := harness.CaptureSkills(dir, "skills")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(skills) != 1 || skills[0].Name != "alpha" {
		t.Fatalf("expected exactly [alpha]; got %v", skills)
	}
	if len(copies) != 1 || filepath.ToSlash(copies[0].Dst) != "skills/alpha" || copies[0].Kind != domain.CopyKindDir {
		t.Fatalf("expected one CopyKindDir at skills/alpha; got %v", copies)
	}
}

func TestCaptureSkills_NonexistentDir(t *testing.T) {
	t.Parallel()
	copies, skills, warnings := harness.CaptureSkills("/nonexistent/path", "skills")
	if copies != nil || skills != nil {
		t.Fatalf("expected nil slices for nonexistent dir, got %v / %v", copies, skills)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for missing dir, got %v", warnings)
	}
}
