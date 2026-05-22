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
	writeFile(t, filepath.Join(srcDir, "src", "skills", "deploy", "SKILL.md"),
		"---\nname: deploy\ndescription: deploy\n---\nbody\n")
	writeFile(t, filepath.Join(srcDir, "config", "rules", "style.md"),
		"---\nname: style\n---\nbody\n")
	// Repo debris.
	writeFile(t, filepath.Join(srcDir, "README.md"), "# Repo")
	if err := os.MkdirAll(filepath.Join(srcDir, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}

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

func TestExtractPackContent_ContentPaths_PathTraversalRejected(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	srcDir := filepath.Join(parent, "repo")
	outsideDir := filepath.Join(parent, "outside")
	writeFile(t, filepath.Join(srcDir, "allowed", "ok.md"), "---\nname: ok\n---\nok\n")
	writeFile(t, filepath.Join(outsideDir, "secret.md"), "---\nname: secret\n---\nsecret\n")

	paths := map[domain.PackCategory]string{
		domain.CategoryRules: "../outside",
	}

	_, _, err := extractPackContent(t.TempDir(), srcDir, paths, "bad", parent)
	if err == nil {
		t.Fatal("expected error for content_paths traversal")
	}
	if !strings.Contains(err.Error(), "escapes source boundary") {
		t.Fatalf("expected boundary escape error, got: %v", err)
	}
}

func TestExtractPackContent_ContentPaths_AbsolutePathRejected(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	outsideDir := t.TempDir()
	writeFile(t, filepath.Join(outsideDir, "secret.md"), "---\nname: secret\n---\nsecret\n")

	paths := map[domain.PackCategory]string{
		domain.CategoryRules: outsideDir,
	}

	_, _, err := extractPackContent(t.TempDir(), srcDir, paths, "bad", srcDir)
	if err == nil {
		t.Fatal("expected error for absolute content_paths source")
	}
	if !strings.Contains(err.Error(), "must be relative") {
		t.Fatalf("expected relative-path error, got: %v", err)
	}
}

func TestExtractPackContent_StandardPack(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 2, Name: "std-pack", Version: "1.0.0", Root: ".",
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
		SchemaVersion: 2, Name: "rich-skills", Version: "1.0.0", Root: ".",
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
		SchemaVersion: 2, Name: "mcp-pack", Version: "1.0.0", Root: ".",
		MCP: []string{"my-server"},
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
		SchemaVersion: 2, Name: "evil-pack", Version: "1.0.0",
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
		SchemaVersion: 2, Name: "normal", Version: "1.0.0", Root: ".",
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
		SchemaVersion: 2, Name: "sub-pack", Version: "1.0.0", Root: ".",
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
		SchemaVersion: 2, Name: "evil", Version: "1.0.0",
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

// Registry path traversal test removed: registries are now ID-based
// (discovered from registries/ directory), so user-supplied paths are no
// longer a vector. The directory copy uses CopyDirResolvingSymlinks with
// boundary checks, which prevents symlink escapes.

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
		SchemaVersion: 2, Name: "perms-pack", Version: "1.0.0", Root: ".",
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
		SchemaVersion: 2, Name: "deep", Version: "1.0.0", Root: ".",
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

func TestExtractPackContent_ContentPaths_AllContentTypes(t *testing.T) {
	// Map all directory-style content types simultaneously — each from a different
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
	os.MkdirAll(filepath.Join(srcDir, "e", "my-hooks", "h1"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "e", "my-hooks", "h1", "HOOK.yaml"),
		[]byte("name: h1\nevents:\n  - on: tool.after\n    handler:\n      type: command\n      command: echo ok\n"), 0o644)

	paths := map[domain.PackCategory]string{
		domain.CategoryRules:     "a/my-rules",
		domain.CategorySkills:    "b/my-skills",
		domain.CategoryAgents:    "c/my-agents",
		domain.CategoryWorkflows: "d/my-workflows",
		domain.CategoryHooks:     "e/my-hooks",
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
		{"hooks/h1/HOOK.yaml", manifest.Hooks, "h1"},
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
		SchemaVersion: 2, Name: "rules-only", Version: "1.0.0", Root: ".",
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
		SchemaVersion: 2, Name: "symlink-pack", Version: "1.0.0", Root: ".",
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
		SchemaVersion: 2, Name: "nested-root", Version: "1.0.0", Root: "src",
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

// ---------------------------------------------------------------------------
// User story tests
// ---------------------------------------------------------------------------

// "I install a pack from a git clone. The source has .git/, tests/,
// README.md, and a Makefile. Only pack content should survive extraction."
func TestExtractPackContent_StandardPack_ExcludesNonContent(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 2, Name: "real-repo", Version: "1.0.0", Root: ".",
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)

	// Pack content that should survive.
	os.MkdirAll(filepath.Join(srcDir, "rules"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "rules", "security.md"),
		[]byte("---\nname: security\n---\nbody\n"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "skills", "deploy"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "skills", "deploy", "SKILL.md"),
		[]byte("---\nname: deploy\ndescription: d\n---\nbody\n"), 0o644)

	// Non-content that should NOT survive.
	os.MkdirAll(filepath.Join(srcDir, ".git", "objects"), 0o755)
	os.WriteFile(filepath.Join(srcDir, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "tests"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "tests", "pack_test.go"), []byte("package tests"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "README.md"), []byte("# My Pack"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "Makefile"), []byte("all: build"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "docs"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "docs", "usage.md"), []byte("Usage guide"), 0o644)

	staging, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	// Content present.
	for _, want := range []string{"pack.json", "rules/security.md", "skills/deploy/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(staging, want)); err != nil {
			t.Errorf("expected %s in extracted pack", want)
		}
	}
	// Non-content excluded.
	for _, reject := range []string{".git", "tests", "README.md", "Makefile", "docs"} {
		if _, err := os.Stat(filepath.Join(staging, reject)); !os.IsNotExist(err) {
			t.Errorf("%s should not survive extraction", reject)
		}
	}
}

// "I install a pack that has both an MCP server and extras. After install,
// I can resolve a profile and the MCP server's {pack:root} reference points
// to the INSTALLED location, not the source."
func TestExtractPackContent_StandardPack_ExtrasPreservedForPackRoot(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 2, Name: "wrapper-pack", Version: "1.0.0", Root: ".",
		Extras: []string{"wrappers"},
		MCP:    []string{"my-server"},
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)

	os.MkdirAll(filepath.Join(srcDir, "wrappers"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "wrappers", "proxy.py"),
		[]byte("import sys; print(sys.argv)"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "mcp"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "mcp", "wrapped.json"), []byte(`{
		"name": "my-server",
		"command": ["python3", "{pack:root}/wrappers/proxy.py"]
	}`), 0o644)

	// Also add noise that should be excluded.
	os.WriteFile(filepath.Join(srcDir, "README.md"), []byte("# wrapper pack"), 0o644)

	staging, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	// Both the MCP inventory and the extras directory must be present.
	for _, want := range []string{
		"mcp/wrapped.json",
		"wrappers/proxy.py",
		"pack.json",
	} {
		if _, err := os.Stat(filepath.Join(staging, want)); err != nil {
			t.Errorf("expected %s in extracted pack", want)
		}
	}
	// README should not be present.
	if _, err := os.Stat(filepath.Join(staging, "README.md")); !os.IsNotExist(err) {
		t.Error("README.md should not survive extraction")
	}

	// The MCP inventory JSON still has {pack:root} — it's a template.
	// Verify it's preserved, not prematurely expanded.
	data, _ := os.ReadFile(filepath.Join(staging, "mcp", "wrapped.json"))
	if !strings.Contains(string(data), "{pack:root}") {
		t.Error("MCP inventory should preserve {pack:root} template — expanded at resolve time, not install time")
	}
}

func TestExtractPackContent_StandardPack_Extras(t *testing.T) {
	// Proves: extras (dirs, nested dirs, and single files) survive
	// extraction with correct content, alongside standard content.
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 2, Name: "with-extras", Version: "1.0.0", Root: ".",
		Extras: []string{"wrappers", "mcp-servers/cloud-api", "bootstrap.sh"},
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)

	os.MkdirAll(filepath.Join(srcDir, "wrappers"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "wrappers", "proxy.py"),
		[]byte("proxy content"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "mcp-servers", "cloud-api"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "mcp-servers", "cloud-api", "main.py"),
		[]byte("api content"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "bootstrap.sh"),
		[]byte("#!/bin/sh\necho hello\n"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "rules"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "rules", "r.md"),
		[]byte("---\nname: r\n---\nbody\n"), 0o644)

	staging, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	// Verify content, not just existence.
	checks := map[string]struct {
		wantContent string
		wantDir     bool
	}{
		"wrappers/proxy.py":             {"proxy content", false},
		"mcp-servers/cloud-api/main.py": {"api content", false},
		"bootstrap.sh":                  {"#!/bin/sh\necho hello\n", false},
		"rules/r.md":                    {"---\nname: r\n---\nbody\n", false},
		"wrappers":                      {"", true},
		"mcp-servers/cloud-api":         {"", true},
	}
	for path, want := range checks {
		full := filepath.Join(staging, path)
		info, err := os.Stat(full)
		if err != nil {
			t.Fatalf("%s missing from extracted pack", path)
		}
		if want.wantDir {
			if !info.IsDir() {
				t.Fatalf("%s should be a directory", path)
			}
			continue
		}
		if info.IsDir() {
			t.Fatalf("%s should be a file, not a directory", path)
		}
		got, _ := os.ReadFile(full)
		if string(got) != want.wantContent {
			t.Fatalf("%s content = %q, want %q", path, got, want.wantContent)
		}
	}
}

func TestExtractPackContent_StandardPack_ExtrasSymlinkEscapeBlocked(t *testing.T) {
	// Proves: a symlink in extras that resolves outside the clone
	// boundary is rejected during extraction.
	t.Parallel()
	srcDir := t.TempDir()
	outsideDir := t.TempDir()
	os.WriteFile(filepath.Join(outsideDir, "secret.sh"), []byte("secret"), 0o644)
	os.Symlink(filepath.Join(outsideDir, "secret.sh"), filepath.Join(srcDir, "escape.sh"))

	manifest := config.PackManifest{
		SchemaVersion: 2, Name: "escape", Version: "1.0.0", Root: ".",
		Extras: []string{"escape.sh"},
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)

	_, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err == nil {
		t.Fatal("expected error for symlink escaping boundary")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got: %v", err)
	}
}

func TestExtractPackContent_StandardPack_ExtrasRepoRelative(t *testing.T) {
	// Proves: extras with leading ".." (repo-relative paths) are copied
	// into the staging directory with the ".." prefix stripped, and the
	// installed manifest uses the staging-relative name.
	t.Parallel()
	repoDir := t.TempDir()
	packDir := filepath.Join(repoDir, "packs", "my-pack")
	os.MkdirAll(packDir, 0o755)

	// Shared directory at repo root, sibling to the pack.
	os.MkdirAll(filepath.Join(repoDir, "shared-scripts"), 0o755)
	os.WriteFile(filepath.Join(repoDir, "shared-scripts", "auth.sh"),
		[]byte("#!/bin/sh\nauth"), 0o644)
	// Also a local extras dir inside the pack.
	os.MkdirAll(filepath.Join(packDir, "wrappers"), 0o755)
	os.WriteFile(filepath.Join(packDir, "wrappers", "proxy.py"),
		[]byte("my-server"), 0o644)

	manifest := config.PackManifest{
		SchemaVersion: 2, Name: "repo-rel", Version: "1.0.0", Root: ".",
		Extras: []string{"../../shared-scripts", "wrappers"},
	}
	config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest)

	staging, m, err := extractPackContent(t.TempDir(), packDir, nil, "", repoDir)
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	// The repo-relative extra should land at its staging name (no "..").
	got, err := os.ReadFile(filepath.Join(staging, "shared-scripts", "auth.sh"))
	if err != nil {
		t.Fatalf("shared-scripts/auth.sh missing from staging: %v", err)
	}
	if string(got) != "#!/bin/sh\nauth" {
		t.Fatalf("content mismatch: %q", got)
	}
	// Local extra should be present too.
	if _, err := os.Stat(filepath.Join(staging, "wrappers", "proxy.py")); err != nil {
		t.Fatalf("wrappers/proxy.py missing: %v", err)
	}

	// The installed manifest should have rewritten extras without "..".
	for _, e := range m.Extras {
		if strings.Contains(e, "..") {
			t.Errorf("installed manifest extras should not contain '..', got %q", e)
		}
	}
	if len(m.Extras) != 2 {
		t.Fatalf("expected 2 extras, got %v", m.Extras)
	}
	// Verify via the on-disk manifest too.
	diskManifest, err := config.LoadPackManifest(filepath.Join(staging, "pack.json"))
	if err != nil {
		t.Fatalf("loading staged manifest: %v", err)
	}
	for _, e := range diskManifest.Extras {
		if strings.Contains(e, "..") {
			t.Errorf("on-disk manifest extras should not contain '..', got %q", e)
		}
	}
}

func TestExtractPackContent_StandardPack_ExtrasRepoEscapeBlocked(t *testing.T) {
	// Proves: extras with ".." that resolve outside the repo boundary are rejected.
	t.Parallel()
	repoDir := t.TempDir()
	packDir := filepath.Join(repoDir, "packs", "my-pack")
	os.MkdirAll(packDir, 0o755)

	// The extras path tries to escape the repo entirely.
	manifest := config.PackManifest{
		SchemaVersion: 2, Name: "escape", Version: "1.0.0", Root: ".",
		Extras: []string{"../../../outside"},
	}
	config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest)
	// Create the target outside the repo so it won't fail on stat.
	os.MkdirAll(filepath.Join(repoDir, "..", "..", "outside"), 0o755)

	_, _, err := extractPackContent(t.TempDir(), packDir, nil, "", repoDir)
	if err == nil {
		t.Fatal("expected error for extras escaping repo boundary")
	}
	if !strings.Contains(err.Error(), "escapes repo boundary") {
		t.Fatalf("expected repo boundary error, got: %v", err)
	}
}

func TestExtractPackContent_StandardPack_ExtrasSymlinkOverlapBlocked(t *testing.T) {
	// Two extras that point to different manifest paths but resolve to the
	// same real directory via symlink must be rejected.
	t.Parallel()
	repoDir := t.TempDir()
	packDir := filepath.Join(repoDir, "packs", "my-pack")
	os.MkdirAll(packDir, 0o755)

	// Create a real directory and a symlink to it.
	realDir := filepath.Join(repoDir, "real-scripts")
	os.MkdirAll(realDir, 0o755)
	os.WriteFile(filepath.Join(realDir, "run.sh"), []byte("run"), 0o644)
	os.Symlink(realDir, filepath.Join(repoDir, "alias-scripts"))

	manifest := config.PackManifest{
		SchemaVersion: 2, Name: "sym-overlap", Version: "1.0.0", Root: ".",
		Extras: []string{"../../real-scripts", "../../alias-scripts"},
	}
	config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest)

	_, _, err := extractPackContent(t.TempDir(), packDir, nil, "", repoDir)
	if err == nil {
		t.Fatal("expected error for symlink-resolved overlap")
	}
	if !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("expected overlap error, got: %v", err)
	}
}

func TestExtractPackContent_StandardPack_ExtrasPrefixContainmentBlocked(t *testing.T) {
	// An extras entry that is a parent directory of another extras entry
	// must be rejected — the parent copy would include the child, then
	// the child copy would silently overwrite.
	t.Parallel()
	repoDir := t.TempDir()
	packDir := filepath.Join(repoDir, "packs", "my-pack")
	os.MkdirAll(packDir, 0o755)

	os.MkdirAll(filepath.Join(packDir, "shared", "sub"), 0o755)
	os.WriteFile(filepath.Join(packDir, "shared", "top.txt"), []byte("top"), 0o644)
	os.WriteFile(filepath.Join(packDir, "shared", "sub", "deep.txt"), []byte("deep"), 0o644)

	manifest := config.PackManifest{
		SchemaVersion: 2, Name: "prefix", Version: "1.0.0", Root: ".",
		Extras: []string{"shared", "shared/sub"},
	}
	config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest)

	_, _, err := extractPackContent(t.TempDir(), packDir, nil, "", repoDir)
	if err == nil {
		t.Fatal("expected error for prefix containment overlap")
	}
	if !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("expected overlap error, got: %v", err)
	}
}

func TestExtractPackContent_StandardPack_BundledProfiles(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	manifest := config.PackManifest{
		SchemaVersion: 2, Name: "with-profiles", Version: "1.0.0", Root: ".",
		Profiles:   []string{"dev"},
		Registries: []string{"popular-packs"},
	}
	config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest)
	os.MkdirAll(filepath.Join(srcDir, "profiles"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "profiles", "dev.yaml"),
		[]byte("schema_version: 2\npacks: []\n"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "registries"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "registries", "popular-packs.yaml"),
		[]byte("schema_version: 1\npacks: {}\n"), 0o644)

	staging, _, err := extractPackContent(t.TempDir(), srcDir, nil, "", "")
	if err != nil {
		t.Fatalf("extractPackContent: %v", err)
	}
	defer os.RemoveAll(staging)

	if _, err := os.Stat(filepath.Join(staging, "profiles", "dev.yaml")); err != nil {
		t.Fatal("profiles/dev.yaml missing")
	}
	if _, err := os.Stat(filepath.Join(staging, "registries", "popular-packs.yaml")); err != nil {
		t.Fatal("registries/popular-packs.yaml missing")
	}
}

// ---------------------------------------------------------------------------
// applyWithFilter
// ---------------------------------------------------------------------------

// buildFilterStaging creates a staging directory with pack.json, content dirs,
// extras, and registries for applyWithFilter tests.
func buildFilterStaging(t *testing.T, m config.PackManifest) string {
	t.Helper()
	staging := t.TempDir()

	// Content directories.
	for _, dir := range []string{"rules", "agents", "workflows", "skills", "hooks", "prompts", "profiles", "registries", "mcp", "configs"} {
		p := filepath.Join(staging, dir)
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "placeholder.md"), []byte("content"), 0o644)
	}
	// Extras entries.
	for _, ext := range m.Extras {
		p := filepath.Join(staging, ext)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("extra"), 0o644)
	}
	// Registry entries (inside registries/ directory).
	for _, id := range m.Registries {
		p := filepath.Join(staging, "registries", id+".yaml")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("registry"), 0o644)
	}
	if err := config.SavePackManifest(filepath.Join(staging, "pack.json"), m); err != nil {
		t.Fatalf("SavePackManifest: %v", err)
	}
	return staging
}

func TestApplyWithFilter_NilNoOp(t *testing.T) {
	t.Parallel()
	m := config.PackManifest{
		SchemaVersion: 2, Name: "test", Version: "1.0.0", Root: ".",
		Rules:  []string{"style"},
		Skills: []string{"deploy"},
	}
	staging := buildFilterStaging(t, m)

	err := applyWithFilter(staging, &m, nil)
	if err != nil {
		t.Fatalf("applyWithFilter: %v", err)
	}
	// Everything still present.
	if _, err := os.Stat(filepath.Join(staging, "rules", "placeholder.md")); err != nil {
		t.Fatal("rules should still exist after nil domain.BundledSet")
	}
	if _, err := os.Stat(filepath.Join(staging, "skills", "placeholder.md")); err != nil {
		t.Fatal("skills should still exist after nil domain.BundledSet")
	}
	// Manifest unchanged.
	if len(m.Rules) != 1 || m.Rules[0] != "style" {
		t.Fatalf("rules manifest changed: %v", m.Rules)
	}
}

func TestApplyWithFilter_WithAll(t *testing.T) {
	t.Parallel()
	m := config.PackManifest{
		SchemaVersion: 2, Name: "test", Version: "1.0.0", Root: ".",
		Rules:      []string{"style"},
		Skills:     []string{"deploy"},
		Extras:     []string{"scripts/run.sh"},
		Registries: []string{"popular-packs"},
	}
	staging := buildFilterStaging(t, m)

	err := applyWithFilter(staging, &m, domain.BundledAll())
	if err != nil {
		t.Fatalf("applyWithFilter: %v", err)
	}
	// Everything still present.
	for _, dir := range []string{"rules", "skills", "agents", "workflows", "hooks", "prompts", "profiles", "registries", "mcp", "configs"} {
		if _, err := os.Stat(filepath.Join(staging, dir)); err != nil {
			t.Errorf("%s should still exist with WithAll", dir)
		}
	}
	if _, err := os.Stat(filepath.Join(staging, "scripts", "run.sh")); err != nil {
		t.Error("extras should still exist with WithAll")
	}
}

func TestApplyWithFilter_Partial(t *testing.T) {
	// --with only gates bundled content (profiles, registries, extras).
	// Core content dirs are always kept regardless of the domain.BundledSet.
	t.Parallel()
	m := config.PackManifest{
		SchemaVersion: 2, Name: "test", Version: "1.0.0", Root: ".",
		Rules:      []string{"style"},
		Agents:     []string{"navigator"},
		Workflows:  []string{"deploy-flow"},
		Skills:     []string{"deploy"},
		Prompts:    []string{"greeting"},
		Profiles:   []string{"team"},
		Extras:     []string{"scripts/run.sh"},
		Registries: []string{"reg"},
		MCP:        []string{"my-server"},
		Configs: config.PackConfigs{
			HarnessSettings: map[string][]string{"opencode": {"oc.json"}},
		},
	}
	staging := buildFilterStaging(t, m)

	// Approve only profiles — registries and extras should be removed,
	// but all core dirs are always kept.
	ws := domain.NewBundledSet(domain.BundledProfiles)
	err := applyWithFilter(staging, &m, ws)
	if err != nil {
		t.Fatalf("applyWithFilter: %v", err)
	}

	// Core content dirs always kept regardless of domain.BundledSet.
	for _, dir := range []string{"rules", "skills", "agents", "workflows", "hooks", "prompts", "mcp", "configs"} {
		if _, err := os.Stat(filepath.Join(staging, dir)); err != nil {
			t.Errorf("%s should always be kept (core dir), but was removed", dir)
		}
	}

	// Approved bundled dir still present.
	if _, err := os.Stat(filepath.Join(staging, "profiles")); err != nil {
		t.Fatal("profiles should exist (approved)")
	}

	// Unapproved bundled content removed.
	if _, err := os.Stat(filepath.Join(staging, "scripts", "run.sh")); !os.IsNotExist(err) {
		t.Error("extras should be removed (not in domain.BundledSet)")
	}
	if _, err := os.Stat(filepath.Join(staging, "registries")); !os.IsNotExist(err) {
		t.Error("registries dir should be removed (not in domain.BundledSet)")
	}

	// Manifest: bundled fields cleared for unapproved categories.
	if m.Extras != nil {
		t.Errorf("extras manifest should be nil, got %v", m.Extras)
	}
	if m.Registries != nil {
		t.Errorf("registries manifest should be nil, got %v", m.Registries)
	}

	// Manifest: core fields unchanged — filter does not touch them.
	if len(m.Rules) != 1 || m.Rules[0] != "style" {
		t.Errorf("rules manifest should be unchanged, got %v", m.Rules)
	}
	if len(m.Skills) != 1 || m.Skills[0] != "deploy" {
		t.Errorf("skills manifest should be unchanged, got %v", m.Skills)
	}
	if len(m.Agents) != 1 || m.Agents[0] != "navigator" {
		t.Errorf("agents manifest should be unchanged, got %v", m.Agents)
	}
	if len(m.Workflows) != 1 || m.Workflows[0] != "deploy-flow" {
		t.Errorf("workflows manifest should be unchanged, got %v", m.Workflows)
	}
	if len(m.Prompts) != 1 || m.Prompts[0] != "greeting" {
		t.Errorf("prompts manifest should be unchanged, got %v", m.Prompts)
	}
	if len(m.Profiles) != 1 || m.Profiles[0] != "team" {
		t.Errorf("profiles manifest should be unchanged, got %v", m.Profiles)
	}
	if len(m.MCP) != 1 {
		t.Errorf("MCP should be unchanged, got %v", m.MCP)
	}
	if !m.Configs.HasAnyConfigs() {
		t.Errorf("configs should be unchanged, got %+v", m.Configs)
	}

	// pack.json re-saved with updated manifest.
	reloaded, err := config.LoadPackManifest(filepath.Join(staging, "pack.json"))
	if err != nil {
		t.Fatalf("LoadPackManifest: %v", err)
	}
	// Core content preserved in persisted manifest.
	if len(reloaded.Rules) != 1 {
		t.Errorf("reloaded rules should have 1 entry, got %v", reloaded.Rules)
	}
	if len(reloaded.Agents) != 1 {
		t.Errorf("reloaded agents should have 1 entry, got %v", reloaded.Agents)
	}
	// Unapproved bundled content cleared in persisted manifest.
	if len(reloaded.Extras) != 0 {
		t.Errorf("reloaded extras should be empty, got %v", reloaded.Extras)
	}
	if len(reloaded.Registries) != 0 {
		t.Errorf("reloaded registries should be empty, got %v", reloaded.Registries)
	}
}

func TestApplyWithFilter_ExtrasRemoval(t *testing.T) {
	t.Parallel()
	m := config.PackManifest{
		SchemaVersion: 2, Name: "test", Version: "1.0.0", Root: ".",
		Extras: []string{"scripts/deploy.sh", "data/config.yaml"},
	}
	staging := buildFilterStaging(t, m)

	// Approve all bundled categories except extras.
	ws := domain.NewBundledSet(domain.BundledProfiles, domain.BundledRegistries)
	err := applyWithFilter(staging, &m, ws)
	if err != nil {
		t.Fatalf("applyWithFilter: %v", err)
	}

	if _, err := os.Stat(filepath.Join(staging, "scripts", "deploy.sh")); !os.IsNotExist(err) {
		t.Error("extras file scripts/deploy.sh should be removed")
	}
	if _, err := os.Stat(filepath.Join(staging, "data", "config.yaml")); !os.IsNotExist(err) {
		t.Error("extras file data/config.yaml should be removed")
	}
	if m.Extras != nil {
		t.Errorf("extras should be nil, got %v", m.Extras)
	}
}

func TestApplyWithFilter_RegistriesRemoval(t *testing.T) {
	t.Parallel()
	m := config.PackManifest{
		SchemaVersion: 2, Name: "test", Version: "1.0.0", Root: ".",
		Registries: []string{"team-tools", "community"},
	}
	staging := buildFilterStaging(t, m)

	// Approve all bundled categories except registries.
	ws := domain.NewBundledSet(domain.BundledProfiles, domain.BundledExtras)
	err := applyWithFilter(staging, &m, ws)
	if err != nil {
		t.Fatalf("applyWithFilter: %v", err)
	}

	if _, err := os.Stat(filepath.Join(staging, "registries")); !os.IsNotExist(err) {
		t.Error("registries/ directory should be removed")
	}
	if m.Registries != nil {
		t.Errorf("registries should be nil, got %v", m.Registries)
	}
}

func TestApplyWithFilter_PackJsonReSaved(t *testing.T) {
	t.Parallel()
	m := config.PackManifest{
		SchemaVersion: 2, Name: "saved-test", Version: "2.0.0", Root: ".",
		Rules:      []string{"alpha"},
		Skills:     []string{"beta"},
		Agents:     []string{"gamma"},
		Profiles:   []string{"dev"},
		Registries: []string{"popular-packs"},
	}
	staging := buildFilterStaging(t, m)

	// Approve only profiles — registries removed, core content unchanged.
	ws := domain.NewBundledSet(domain.BundledProfiles)
	err := applyWithFilter(staging, &m, ws)
	if err != nil {
		t.Fatalf("applyWithFilter: %v", err)
	}

	reloaded, err := config.LoadPackManifest(filepath.Join(staging, "pack.json"))
	if err != nil {
		t.Fatalf("LoadPackManifest: %v", err)
	}
	if reloaded.Name != "saved-test" {
		t.Errorf("name = %q, want saved-test", reloaded.Name)
	}
	// Core content fields preserved in pack.json.
	if len(reloaded.Rules) != 1 || reloaded.Rules[0] != "alpha" {
		t.Errorf("rules = %v, want [alpha]", reloaded.Rules)
	}
	if len(reloaded.Skills) != 1 || reloaded.Skills[0] != "beta" {
		t.Errorf("skills = %v, want [beta]", reloaded.Skills)
	}
	if len(reloaded.Agents) != 1 || reloaded.Agents[0] != "gamma" {
		t.Errorf("agents = %v, want [gamma]", reloaded.Agents)
	}
	// Approved bundled content preserved.
	if len(reloaded.Profiles) != 1 || reloaded.Profiles[0] != "dev" {
		t.Errorf("profiles = %v, want [dev]", reloaded.Profiles)
	}
	// Unapproved bundled content cleared in persisted manifest.
	if len(reloaded.Registries) != 0 {
		t.Errorf("registries should be empty in persisted manifest, got %v", reloaded.Registries)
	}
}
