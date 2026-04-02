package app

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestExtractPackContent_ContentPaths(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	os.MkdirAll(filepath.Join(srcDir, "src", "skills", "deploy"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "src", "skills", "deploy", "SKILL.md"),
		[]byte("---\nname: deploy\ndescription: deploy\n---\nbody\n"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "config", "rules"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "config", "rules", "style.md"),
		[]byte("---\nname: style\n---\nbody\n"), 0o644)
	// Repo debris.
	os.WriteFile(filepath.Join(srcDir, "README.md"), []byte("# Repo"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, ".git", "objects"), 0o755)

	paths := map[domain.PackCategory]string{
		domain.CategorySkills: "src/skills",
		domain.CategoryRules:  "config/rules",
	}

	staging, manifest, err := extractPackContent(t.TempDir(), srcDir, paths, "ext-pack", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	// Standard layout produced.
	if _, err := os.Stat(filepath.Join(staging, "pack.json")); err != nil {
		t.Fatal("pack.json not created")
	}
	if _, err := os.Stat(filepath.Join(staging, "skills", "deploy", "SKILL.md")); err != nil {
		t.Fatal("skills/deploy/SKILL.md not at standard path")
	}
	if _, err := os.Stat(filepath.Join(staging, "rules", "style.md")); err != nil {
		t.Fatal("rules/style.md not at standard path")
	}
	// Repo debris excluded.
	if _, err := os.Stat(filepath.Join(staging, "README.md")); !os.IsNotExist(err) {
		t.Fatal("README.md should not be in extracted pack")
	}
	if _, err := os.Stat(filepath.Join(staging, ".git")); !os.IsNotExist(err) {
		t.Fatal(".git should not be in extracted pack")
	}
	// Manifest correct.
	if manifest.Name != "ext-pack" {
		t.Fatalf("name = %q, want ext-pack", manifest.Name)
	}
	if len(manifest.Skills) != 1 || manifest.Skills[0] != "deploy" {
		t.Fatalf("skills = %v, want [deploy]", manifest.Skills)
	}
	if len(manifest.Rules) != 1 || manifest.Rules[0] != "style" {
		t.Fatalf("rules = %v, want [style]", manifest.Rules)
	}
}

func TestExtractPackContent_ContentPaths_UnmappedTypesOmitted(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	os.MkdirAll(filepath.Join(srcDir, "my-rules"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "my-rules", "one.md"),
		[]byte("---\nname: one\n---\nbody\n"), 0o644)

	paths := map[domain.PackCategory]string{
		domain.CategoryRules: "my-rules",
	}

	staging, _, err := extractPackContent(t.TempDir(), srcDir, paths, "rules-only", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	if _, err := os.Stat(filepath.Join(staging, "skills")); !os.IsNotExist(err) {
		t.Fatal("skills/ should not exist for unmapped type")
	}
	if _, err := os.Stat(filepath.Join(staging, "agents")); !os.IsNotExist(err) {
		t.Fatal("agents/ should not exist for unmapped type")
	}
}

func TestExtractPackContent_ContentPaths_MissingSourceErrors(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()

	paths := map[domain.PackCategory]string{
		domain.CategoryRules: "does-not-exist",
	}

	_, _, err := extractPackContent(t.TempDir(), srcDir, paths, "bad", "")
	if err == nil {
		t.Fatal("expected error for missing source directory")
	}
}

func TestExtractPackContent_StandardPack(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "std-pack", Version: "1.0.0", Root: ".",
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)
	os.MkdirAll(filepath.Join(srcDir, "rules"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "rules", "alpha.md"),
		[]byte("---\nname: alpha\n---\nbody\n"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "skills", "my-skill"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "skills", "my-skill", "SKILL.md"),
		[]byte("---\nname: my-skill\ndescription: d\n---\nbody\n"), 0o644)
	// Non-pack files.
	os.MkdirAll(filepath.Join(srcDir, ".git", "objects"), 0o755)
	os.WriteFile(filepath.Join(srcDir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "README.md"), []byte("# Pack"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "tests"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "tests", "test_pack.go"), []byte("package test"), 0o644)

	staging, man, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	// Pack content present.
	if _, err := os.Stat(filepath.Join(staging, "pack.json")); err != nil {
		t.Fatal("pack.json missing")
	}
	if _, err := os.Stat(filepath.Join(staging, "rules", "alpha.md")); err != nil {
		t.Fatal("rules/alpha.md missing")
	}
	if _, err := os.Stat(filepath.Join(staging, "skills", "my-skill", "SKILL.md")); err != nil {
		t.Fatal("skills/my-skill/SKILL.md missing")
	}
	if man.Name != "std-pack" {
		t.Fatalf("name = %q, want std-pack", man.Name)
	}

	// Repo debris excluded.
	if _, err := os.Stat(filepath.Join(staging, ".git")); !os.IsNotExist(err) {
		t.Fatal(".git should not be in extracted pack")
	}
	if _, err := os.Stat(filepath.Join(staging, "README.md")); !os.IsNotExist(err) {
		t.Fatal("README.md should not be in extracted pack")
	}
	if _, err := os.Stat(filepath.Join(staging, "tests")); !os.IsNotExist(err) {
		t.Fatal("tests/ should not be in extracted pack")
	}
}

func TestExtractPackContent_StandardPack_SkillResourceFiles(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "rich-skills", Version: "1.0.0", Root: ".",
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)

	// Skill with SKILL.md + scripts/ + resources/ subdirectories.
	skillDir := filepath.Join(srcDir, "skills", "deploy")
	os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755)
	os.MkdirAll(filepath.Join(skillDir, "resources"), 0o755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: deploy\ndescription: d\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(skillDir, "scripts", "run.sh"),
		[]byte("#!/bin/bash\necho deploy"), 0o755)
	os.WriteFile(filepath.Join(skillDir, "resources", "template.yaml"),
		[]byte("kind: deployment"), 0o644)

	staging, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	// SKILL.md present.
	if _, err := os.Stat(filepath.Join(staging, "skills", "deploy", "SKILL.md")); err != nil {
		t.Fatal("SKILL.md missing")
	}
	// scripts/ and resources/ must also survive extraction.
	if _, err := os.Stat(filepath.Join(staging, "skills", "deploy", "scripts", "run.sh")); err != nil {
		t.Fatal("scripts/run.sh missing — skill resource files not extracted")
	}
	if _, err := os.Stat(filepath.Join(staging, "skills", "deploy", "resources", "template.yaml")); err != nil {
		t.Fatal("resources/template.yaml missing — skill resource files not extracted")
	}
}

func TestExtractPackContent_StandardPack_MCPAndConfigs(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "mcp-pack", Version: "1.0.0", Root: ".",
		MCP: config.MCPPack{Servers: map[string]config.MCPDefaults{
			"my-server": {},
		}},
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)

	os.MkdirAll(filepath.Join(srcDir, "mcp"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "mcp", "my-server.json"),
		[]byte(`{"name":"my-server","command":["echo"]}`), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "configs", "claudecode"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "configs", "claudecode", "settings.json"),
		[]byte(`{}`), 0o644)

	staging, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	if _, err := os.Stat(filepath.Join(staging, "mcp", "my-server.json")); err != nil {
		t.Fatal("mcp/my-server.json missing")
	}
	if _, err := os.Stat(filepath.Join(staging, "configs", "claudecode", "settings.json")); err != nil {
		t.Fatal("configs/claudecode/settings.json missing")
	}
}

func TestExtractPackContent_ContentPaths_SkillResourceFiles(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()

	// Non-standard layout with skill that has resource files.
	skillDir := filepath.Join(srcDir, "custom", "skills", "review")
	os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: review\ndescription: r\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(skillDir, "scripts", "check.sh"),
		[]byte("#!/bin/bash\necho review"), 0o755)

	paths := map[domain.PackCategory]string{
		domain.CategorySkills: "custom/skills",
	}

	staging, _, err := extractPackContent(t.TempDir(), srcDir, paths, "ext", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	if _, err := os.Stat(filepath.Join(staging, "skills", "review", "SKILL.md")); err != nil {
		t.Fatal("SKILL.md missing")
	}
	if _, err := os.Stat(filepath.Join(staging, "skills", "review", "scripts", "check.sh")); err != nil {
		t.Fatal("scripts/check.sh missing — skill resource files not extracted via content_paths")
	}
}

// ---------------------------------------------------------------------------
// Boundary / security tests
// ---------------------------------------------------------------------------

func TestExtractPackContent_StandardPack_RootEscapeBlocked(t *testing.T) {
	// A malicious pack.json with root: "../.." should be rejected because
	// the resolved packRoot falls outside the symlinkBoundary (the clone).
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "evil-pack", Version: "1.0.0",
		Root: "../..",
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)

	_, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err == nil {
		t.Fatal("expected error for pack root escaping boundary")
	}
	if !errors.Is(err, errPackRootEscape) {
		t.Fatalf("expected errPackRootEscape, got: %v", err)
	}
}

func TestExtractPackContent_StandardPack_RootDotIsAllowed(t *testing.T) {
	// root: "." is the normal case — should work fine.
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "normal", Version: "1.0.0", Root: ".",
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)
	os.MkdirAll(filepath.Join(srcDir, "rules"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "rules", "r.md"),
		[]byte("---\nname: r\n---\nbody\n"), 0o644)

	staging, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.RemoveAll(staging)
	if _, err := os.Stat(filepath.Join(staging, "rules", "r.md")); err != nil {
		t.Fatal("rules/r.md missing")
	}
}

func TestExtractPackContent_StandardPack_SubdirRootWithinBoundary(t *testing.T) {
	// A subpath pack has srcRoot = cloneDir/sub and root: "." — the resolved
	// pack root is srcRoot itself, and symlinkBoundary is the clone root. This
	// should work because srcRoot is within the clone root.
	t.Parallel()
	cloneDir := t.TempDir()
	subDir := filepath.Join(cloneDir, "packs", "my-pack")
	os.MkdirAll(subDir, 0o755)
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "sub-pack", Version: "1.0.0", Root: ".",
	}
	config.SavePackManifest(filepath.Join(subDir, "pack.json"), manifest)
	os.MkdirAll(filepath.Join(subDir, "skills", "deploy"), 0o755)
	os.WriteFile(filepath.Join(subDir, "skills", "deploy", "SKILL.md"),
		[]byte("---\nname: deploy\ndescription: d\n---\nbody\n"), 0o644)

	// symlinkBoundary = cloneDir (parent of srcRoot) — correct for subpath packs.
	staging, man, err := extractPackContent(t.TempDir(), subDir, nil, "", cloneDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.RemoveAll(staging)
	if man.Name != "sub-pack" {
		t.Fatalf("name = %q, want sub-pack", man.Name)
	}
	if _, err := os.Stat(filepath.Join(staging, "skills", "deploy", "SKILL.md")); err != nil {
		t.Fatal("skills/deploy/SKILL.md missing")
	}
}

func TestExtractPackContent_StandardPack_RootEscapeFromSubpath(t *testing.T) {
	// Subpath pack with root: "../../.." — escapes the clone boundary.
	t.Parallel()
	cloneDir := t.TempDir()
	subDir := filepath.Join(cloneDir, "packs", "evil")
	os.MkdirAll(subDir, 0o755)
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "evil", Version: "1.0.0",
		Root: "../../..",
	}
	config.SavePackManifest(filepath.Join(subDir, "pack.json"), manifest)

	_, _, err := extractPackContent(t.TempDir(), subDir, nil, "", cloneDir)
	if err == nil {
		t.Fatal("expected error for root escaping clone boundary from subpath")
	}
	if !errors.Is(err, errPackRootEscape) {
		t.Fatalf("expected errPackRootEscape, got: %v", err)
	}
}

func TestExtractPackContent_StandardPack_RegistryPathTraversalBlocked(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "evil-reg", Version: "1.0.0", Root: ".",
		Registries: []string{"../../etc/passwd"},
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)
	// Create the target so the stat check passes.
	os.MkdirAll(filepath.Join(srcDir, "..", "..", "etc"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "..", "..", "etc", "passwd"),
		[]byte("not really"), 0o644)

	_, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err == nil {
		t.Fatal("expected error for registry path escaping boundary")
	}
	if !strings.Contains(err.Error(), "escapes boundary") {
		t.Fatalf("expected boundary escape error, got: %v", err)
	}
}

func TestExtractPackContent_ContentPaths_InvalidCategoryRejected(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	os.MkdirAll(filepath.Join(srcDir, "stuff"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "stuff", "x.md"), []byte("hi"), 0o644)

	paths := map[domain.PackCategory]string{
		domain.PackCategory("garbage"): "stuff",
	}
	_, _, err := extractPackContent(t.TempDir(), srcDir, paths, "bad", "")
	if err == nil {
		t.Fatal("expected error for invalid content_paths key")
	}
	if !strings.Contains(err.Error(), "invalid content_paths key") {
		t.Fatalf("expected validation error, got: %v", err)
	}
}

func TestExtractPackContent_ContentPaths_MCPCategoryRejected(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	os.MkdirAll(filepath.Join(srcDir, "servers"), 0o755)

	paths := map[domain.PackCategory]string{
		domain.CategoryMCP: "servers",
	}
	_, _, err := extractPackContent(t.TempDir(), srcDir, paths, "bad", "")
	if err == nil {
		t.Fatal("expected error for mcp as content_paths key")
	}
}

// ---------------------------------------------------------------------------
// Staging cleanup tests
// ---------------------------------------------------------------------------

func TestExtractPackContent_StagingCleanedOnError(t *testing.T) {
	// When extraction fails, the staging directory should be cleaned up.
	t.Parallel()
	parentDir := t.TempDir()
	srcDir := t.TempDir()
	// No pack.json → extractStandardPack will fail.

	entries, _ := os.ReadDir(parentDir)
	beforeCount := len(entries)

	_, _, err := extractPackContent(parentDir, srcDir, nil, "", "")
	if err == nil {
		t.Fatal("expected error for missing pack.json")
	}

	entries, _ = os.ReadDir(parentDir)
	if len(entries) != beforeCount {
		t.Fatalf("staging directory leaked: had %d entries before, %d after", beforeCount, len(entries))
	}
}

func TestExtractPackContent_ContentPaths_StagingCleanedOnMissingSrc(t *testing.T) {
	t.Parallel()
	parentDir := t.TempDir()
	srcDir := t.TempDir()

	paths := map[domain.PackCategory]string{
		domain.CategoryRules: "nonexistent/path",
	}

	entries, _ := os.ReadDir(parentDir)
	beforeCount := len(entries)

	_, _, err := extractPackContent(parentDir, srcDir, paths, "bad", "")
	if err == nil {
		t.Fatal("expected error for missing content path")
	}

	entries, _ = os.ReadDir(parentDir)
	if len(entries) != beforeCount {
		t.Fatalf("staging directory leaked: had %d entries before, %d after", beforeCount, len(entries))
	}
}

// ---------------------------------------------------------------------------
// Content fidelity tests
// ---------------------------------------------------------------------------

func TestExtractPackContent_StandardPack_FilesNormalisedTo0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permissions not applicable on Windows")
	}
	// CopyDirResolvingSymlinks normalises file permissions to 0600. Verify
	// that extracted files (including scripts) use the normalised mode, not
	// the source mode. This is a security property: installed packs don't
	// inherit arbitrary permissions from the source repo.
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "perms-pack", Version: "1.0.0", Root: ".",
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)
	skillDir := filepath.Join(srcDir, "skills", "ops")
	os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: ops\ndescription: o\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(skillDir, "scripts", "run.sh"),
		[]byte("#!/bin/bash\necho run"), 0o755)

	staging, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	info, err := os.Stat(filepath.Join(staging, "skills", "ops", "scripts", "run.sh"))
	if err != nil {
		t.Fatal("run.sh missing")
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 (normalised), got %o", info.Mode().Perm())
	}
	// Content intact despite permission normalisation.
	data, _ := os.ReadFile(filepath.Join(staging, "skills", "ops", "scripts", "run.sh"))
	if !strings.Contains(string(data), "echo run") {
		t.Fatalf("run.sh content lost: %q", data)
	}
}

func TestExtractPackContent_StandardPack_NestedDirStructurePreserved(t *testing.T) {
	// A skill with deeply nested resource directories should survive extraction
	// with the full tree intact.
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "deep", Version: "1.0.0", Root: ".",
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)

	deep := filepath.Join(srcDir, "skills", "deploy", "resources", "templates", "k8s")
	os.MkdirAll(deep, 0o755)
	os.WriteFile(filepath.Join(srcDir, "skills", "deploy", "SKILL.md"),
		[]byte("---\nname: deploy\ndescription: d\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(deep, "deployment.yaml"),
		[]byte("kind: Deployment"), 0o644)
	os.WriteFile(filepath.Join(deep, "service.yaml"),
		[]byte("kind: Service"), 0o644)

	staging, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	for _, f := range []string{"deployment.yaml", "service.yaml"} {
		p := filepath.Join(staging, "skills", "deploy", "resources", "templates", "k8s", f)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s missing from deeply nested extraction", f)
		}
	}
}

func TestExtractPackContent_ContentPaths_OverlappingDirNamesIsolated(t *testing.T) {
	// Source repo has "rules/" and "config/rules/" — when content_paths maps
	// only "config/rules" as the rules source, the top-level "rules/" content
	// should not leak into the extracted pack.
	t.Parallel()
	srcDir := t.TempDir()
	// Top-level rules/ with a decoy file.
	os.MkdirAll(filepath.Join(srcDir, "rules"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "rules", "decoy.md"),
		[]byte("---\nname: decoy\n---\nshould not appear\n"), 0o644)
	// Actual source under config/rules/.
	os.MkdirAll(filepath.Join(srcDir, "config", "rules"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "config", "rules", "real.md"),
		[]byte("---\nname: real\n---\nthe real rule\n"), 0o644)

	paths := map[domain.PackCategory]string{
		domain.CategoryRules: "config/rules",
	}

	staging, manifest, err := extractPackContent(t.TempDir(), srcDir, paths, "isolated", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	// Real rule present.
	if _, err := os.Stat(filepath.Join(staging, "rules", "real.md")); err != nil {
		t.Fatal("rules/real.md missing")
	}
	// Decoy must not appear.
	if _, err := os.Stat(filepath.Join(staging, "rules", "decoy.md")); !os.IsNotExist(err) {
		t.Fatal("decoy.md from top-level rules/ leaked into extracted pack")
	}
	if len(manifest.Rules) != 1 || manifest.Rules[0] != "real" {
		t.Fatalf("manifest.Rules = %v, want [real]", manifest.Rules)
	}
}

func TestExtractPackContent_ContentPaths_AllFourTypes(t *testing.T) {
	// Map all four content types simultaneously — each from a different
	// non-standard path — and verify the standard layout is correct.
	t.Parallel()
	srcDir := t.TempDir()
	os.MkdirAll(filepath.Join(srcDir, "a", "my-rules"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "a", "my-rules", "r1.md"),
		[]byte("---\nname: r1\n---\nbody\n"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "b", "my-skills", "s1"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "b", "my-skills", "s1", "SKILL.md"),
		[]byte("---\nname: s1\ndescription: d\n---\nbody\n"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "c", "my-agents"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "c", "my-agents", "a1.md"),
		[]byte("---\nname: a1\n---\nbody\n"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "d", "my-workflows"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "d", "my-workflows", "w1.md"),
		[]byte("---\nname: w1\n---\nbody\n"), 0o644)

	paths := map[domain.PackCategory]string{
		domain.CategoryRules:     "a/my-rules",
		domain.CategorySkills:    "b/my-skills",
		domain.CategoryAgents:    "c/my-agents",
		domain.CategoryWorkflows: "d/my-workflows",
	}

	staging, manifest, err := extractPackContent(t.TempDir(), srcDir, paths, "quad", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	checks := []struct {
		path   string
		field  []string
		expect string
	}{
		{"rules/r1.md", manifest.Rules, "r1"},
		{"skills/s1/SKILL.md", manifest.Skills, "s1"},
		{"agents/a1.md", manifest.Agents, "a1"},
		{"workflows/w1.md", manifest.Workflows, "w1"},
	}
	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(staging, c.path)); err != nil {
			t.Fatalf("%s missing", c.path)
		}
		if len(c.field) != 1 || c.field[0] != c.expect {
			t.Fatalf("manifest for %s: got %v, want [%s]", c.path, c.field, c.expect)
		}
	}
}

func TestExtractPackContent_StandardPack_EmptyContentDirsNotCreated(t *testing.T) {
	// A pack with only rules should not produce empty skills/, agents/, etc.
	// directories in the extracted output.
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "rules-only", Version: "1.0.0", Root: ".",
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)
	os.MkdirAll(filepath.Join(srcDir, "rules"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "rules", "only.md"),
		[]byte("---\nname: only\n---\nbody\n"), 0o644)

	staging, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	if _, err := os.Stat(filepath.Join(staging, "rules", "only.md")); err != nil {
		t.Fatal("rules/only.md missing")
	}
	// Non-existent source dirs should not produce empty dirs.
	for _, d := range []string{"skills", "agents", "workflows", "mcp", "configs"} {
		if _, err := os.Stat(filepath.Join(staging, d)); !os.IsNotExist(err) {
			t.Fatalf("%s/ should not exist (pack has no %s content)", d, d)
		}
	}
}

func TestExtractPackContent_StandardPack_SymlinkWithinBoundaryResolved(t *testing.T) {
	// A pack where rules/ contains a symlink to a shared file elsewhere in
	// the same clone. The symlink should be resolved to a regular file in
	// the extracted output (no dangling symlinks).
	t.Parallel()
	cloneDir := t.TempDir()
	// Shared file at clone root level.
	os.MkdirAll(filepath.Join(cloneDir, "shared"), 0o755)
	os.WriteFile(filepath.Join(cloneDir, "shared", "common.md"),
		[]byte("---\nname: common\n---\nshared content\n"), 0o644)
	// Pack at clone root with a symlink in rules/.
	os.MkdirAll(filepath.Join(cloneDir, "rules"), 0o755)
	os.WriteFile(filepath.Join(cloneDir, "rules", "local.md"),
		[]byte("---\nname: local\n---\nlocal rule\n"), 0o644)
	os.Symlink(filepath.Join(cloneDir, "shared", "common.md"),
		filepath.Join(cloneDir, "rules", "common.md"))

	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "symlink-pack", Version: "1.0.0", Root: ".",
	}
	config.SavePackManifest(filepath.Join(cloneDir, "pack.json"), manifest)

	staging, _, err := extractPackContent(t.TempDir(), cloneDir, nil, "", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	// Both files present.
	for _, f := range []string{"local.md", "common.md"} {
		p := filepath.Join(staging, "rules", f)
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatalf("rules/%s missing", f)
		}
		// Must be a regular file, not a symlink.
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("rules/%s is still a symlink — should be resolved", f)
		}
	}
	// Verify content was actually copied (not just an empty file).
	data, _ := os.ReadFile(filepath.Join(staging, "rules", "common.md"))
	if !strings.Contains(string(data), "shared content") {
		t.Fatalf("common.md content wrong: %q", data)
	}
}

// ---------------------------------------------------------------------------
// Profile + registry integration tests
// ---------------------------------------------------------------------------

// TestExtractPackContent_StandardPack_NonDotRoot_RewritesRootToFlat verifies that
// when a pack has Root != "." (content lives in a subdirectory), extraction
// flattens content to the staging root and the resulting pack.json reflects
// the new layout. Without this, the installed pack resolves to a non-existent
// subdirectory and ResolveProfile / DiscoverContent fail.
func TestExtractPackContent_StandardPack_NonDotRoot_RewritesRootToFlat(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()

	// Repo layout: pack.json at root with Root: "src", content under src/.
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "nested-root", Version: "1.0.0", Root: "src",
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)
	os.MkdirAll(filepath.Join(srcDir, "src", "rules"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "src", "rules", "one.md"),
		[]byte("---\nname: one\n---\nbody\n"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "src", "skills", "deploy"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "src", "skills", "deploy", "SKILL.md"),
		[]byte("---\nname: deploy\ndescription: d\n---\nbody\n"), 0o644)

	staging, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	// Content must be present at flat staging paths (not nested under src/).
	if _, err := os.Stat(filepath.Join(staging, "rules", "one.md")); err != nil {
		t.Fatal("rules/one.md missing from extracted pack")
	}
	if _, err := os.Stat(filepath.Join(staging, "skills", "deploy", "SKILL.md")); err != nil {
		t.Fatal("skills/deploy/SKILL.md missing from extracted pack")
	}

	// Critical: the extracted pack.json must resolve correctly from the staging
	// directory. If Root is still "src", ResolvePackRoot will look for staging/src/
	// which doesn't exist — content was flattened to staging/.
	extractedManifest, err := config.LoadPackManifest(filepath.Join(staging, "pack.json"))
	if err != nil {
		t.Fatalf("LoadPackManifest: %v", err)
	}
	resolvedRoot := config.ResolvePackRoot(filepath.Join(staging, "pack.json"), extractedManifest.Root)
	if _, err := os.Stat(resolvedRoot); err != nil {
		t.Fatalf("extracted pack.json Root %q resolves to %q which doesn't exist — "+
			"Root was not rewritten to match flattened layout", extractedManifest.Root, resolvedRoot)
	}

	// Verify DiscoverContent works on the extracted pack (this is what
	// ResolveProfile does at sync time).
	if err := config.DiscoverContent(&extractedManifest, resolvedRoot); err != nil {
		t.Fatalf("DiscoverContent on extracted pack failed: %v — Root mismatch", err)
	}
	if len(extractedManifest.Rules) == 0 {
		t.Fatal("DiscoverContent found no rules — content not at expected path relative to Root")
	}
}

func TestExtractPackContent_StandardPack_WithProfiles(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 1, Name: "with-profiles", Version: "1.0.0", Root: ".",
		Profiles:   []string{"profiles/team.yaml"},
		Registries: []string{"registry.yaml"},
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)
	os.MkdirAll(filepath.Join(srcDir, "profiles"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "profiles", "team.yaml"),
		[]byte("schema_version: 2\npacks: []\n"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "registry.yaml"),
		[]byte("schema_version: 1\npacks: {}\n"), 0o644)

	staging, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	if _, err := os.Stat(filepath.Join(staging, "profiles", "team.yaml")); err != nil {
		t.Fatal("profiles/team.yaml missing")
	}
	if _, err := os.Stat(filepath.Join(staging, "registry.yaml")); err != nil {
		t.Fatal("registry.yaml missing")
	}
}
