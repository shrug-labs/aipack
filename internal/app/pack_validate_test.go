package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
)

func findingExists(findings []config.Finding, path, message string) bool {
	for _, f := range findings {
		if f.Path == path && f.Message == message {
			return true
		}
	}
	return false
}

func TestRunPackValidate_MissingFrontmatterFinding(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, "rules", "missing-frontmatter.md"), "body\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if rep.OK {
		t.Fatal("expected invalid pack")
	}
	if !findingExists(rep.Findings, "rules/missing-frontmatter.md", "missing YAML frontmatter") {
		t.Fatalf("expected frontmatter finding, got %v", rep.Findings)
	}
}

func TestRunPackValidate_MissingFrontmatterFindingAcrossAuthoredKinds(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, "agents", "reviewer.md"), "body\n")
	writeFile(t, filepath.Join(packDir, "workflows", "ship.md"), "body\n")
	writeFile(t, filepath.Join(packDir, "skills", "triage", "SKILL.md"), "body\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if rep.OK {
		t.Fatal("expected invalid pack")
	}
	for _, want := range []struct{ path, msg string }{
		{"agents/reviewer.md", "missing YAML frontmatter"},
		{"workflows/ship.md", "missing YAML frontmatter"},
		{"skills/triage/SKILL.md", "missing YAML frontmatter"},
	} {
		if !findingExists(rep.Findings, want.path, want.msg) {
			t.Fatalf("expected finding %q at %q, got %v", want.msg, want.path, rep.Findings)
		}
	}
}

func TestRunPackValidate_SkillSupportingFilesDoNotRequireFrontmatter(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, "skills", "triage", "notes.md"), "body\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if !rep.OK {
		t.Fatalf("expected supporting skill markdown to be ignored, got %v", rep.Findings)
	}
}

func TestRunPackValidate_LeadingFrontmatterMarkerCountsAsPresent(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, "rules", "open-frontmatter.md"), "---\nname: broken\nbody\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if findingExists(rep.Findings, "rules/open-frontmatter.md", "missing YAML frontmatter") {
		t.Fatalf("expected leading frontmatter marker to count as present, got %v", rep.Findings)
	}
}

func TestRunPackValidate_DocsAreExcludedFromSecretScan(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, "docs", "guide.md"), "ocid1.instance.oc1.phx.secret\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if !rep.OK {
		t.Fatalf("expected docs secret to be exempt, got %v", rep.Findings)
	}
}

func TestRunPackValidate_TopLevelMarkdownIsScannedForSecrets(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, "notes.md"), "AKIAIOSFODNN7EXAMPLE\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if rep.OK {
		t.Fatal("expected invalid pack")
	}
	if !findingExists(rep.Findings, "notes.md", "matches secret pattern 'AKIA[0-9A-Z]{16}'") {
		t.Fatalf("expected top-level markdown secret finding, got %v", rep.Findings)
	}
}

func TestRunPackValidate_DoesNotRequireContentDirectoriesWhenManifestVectorsAreEmpty(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"schema_version":1,"name":"demo","version":"0.1.0","root":".","rules":[],"agents":[],"workflows":[],"skills":[],"mcp":{"servers":{}},"configs":{"harness_settings":{}}}`)

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if !rep.OK {
		t.Fatalf("expected empty vectors to allow missing content dirs, got %v", rep.Findings)
	}
}

func TestRunPackValidate_RejectsEnvFiles(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, ".env.production"), "SECRET=1\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if rep.OK {
		t.Fatal("expected invalid pack")
	}
	if !findingExists(rep.Findings, ".env.production", "forbidden .env file") {
		t.Fatalf("expected .env finding, got %v", rep.Findings)
	}
}

func TestRunPackValidate_AllowsDotEnvExample(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, ".env.example"), "EXAMPLE=1\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if !rep.OK {
		t.Fatalf("expected .env.example to be allowed, got %v", rep.Findings)
	}
}

func TestRunPackValidate_RejectsSecretsInMarkdownOutsideDocs(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, "rules", "has-frontmatter.md"), "---\nname: has-frontmatter\ndescription: test\nmetadata:\n  owner: test\n  last_updated: 2026-03-11\n---\nocid1.instance.oc1.phx.secret\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if rep.OK {
		t.Fatal("expected invalid pack")
	}
	if !findingExists(rep.Findings, "rules/has-frontmatter.md", "matches secret pattern '\\bocid1\\.[a-z0-9.]+'") {
		t.Fatalf("expected secret finding, got %v", rep.Findings)
	}
}

func writePackValidateFixture(t *testing.T) string {
	t.Helper()
	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"schema_version":1,"name":"demo","version":"0.1.0","root":".","rules":[],"agents":[],"workflows":[],"skills":[],"mcp":{"servers":{}},"configs":{"harness_settings":{}}}`)
	if err := os.MkdirAll(filepath.Join(packDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"agents", "workflows", "skills"} {
		if err := os.MkdirAll(filepath.Join(packDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return packDir
}

func TestRunPackValidate_FrontmatterMissingDescription(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"schema_version":1,"name":"demo","root":".","rules":["no-desc"],"agents":[],"workflows":[],"skills":[]}`)
	writeFile(t, filepath.Join(packDir, "rules", "no-desc.md"), "---\nname: no-desc\n---\nbody\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	// Should still be OK=true since frontmatter issues are warnings.
	found := false
	for _, f := range rep.Findings {
		if f.Path == "rules/no-desc.md" && f.Category == config.FindingCategoryFrontmatter {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected frontmatter warning for missing description, got %v", rep.Findings)
	}
	if !rep.OK {
		t.Fatal("frontmatter warnings should not set OK=false")
	}
}

func TestRunPackValidate_AgentUnknownMCPServer(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"schema_version":1,"name":"demo","root":".","rules":[],"agents":["bad"],"workflows":[],"skills":[],"mcp":{"servers":{}}}`)
	writeFile(t, filepath.Join(packDir, "agents", "bad.md"), "---\nname: bad\ndescription: test\nmcp_servers:\n  - nonexistent\n---\nbody\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	found := false
	for _, f := range rep.Findings {
		if f.Category == config.FindingCategoryConsistency {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected consistency warning for unknown mcp_server, got %v", rep.Findings)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
