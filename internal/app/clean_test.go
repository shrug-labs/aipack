package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	ccharness "github.com/shrug-labs/aipack/internal/harness/claudecode"
	clharness "github.com/shrug-labs/aipack/internal/harness/cline"
	cxharness "github.com/shrug-labs/aipack/internal/harness/codex"
	ocharness "github.com/shrug-labs/aipack/internal/harness/opencode"
)

func testRegistry() *harness.Registry {
	return harness.NewRegistry(
		ccharness.Harness{}, clharness.Harness{}, cxharness.Harness{}, ocharness.Harness{},
	)
}

func TestRemovePathOp_Nonexistent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	op := removePathOp{Path: filepath.Join(dir, "does-not-exist")}
	ctx := cleanRunContext{Yes: true}

	if err := op.run(context.Background(), ctx); err != nil {
		t.Fatalf("expected no error for non-existent path, got: %v", err)
	}
}

func TestRemovePathOp_ExistingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "remove-me.txt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	op := removePathOp{Path: path}
	ctx := cleanRunContext{Yes: true}

	if err := op.run(context.Background(), ctx); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed, but it still exists")
	}
}

func TestEditFileOp_EmptyTOMLFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	op := editFileOp{
		FilePath: path,
		Format:   harness.FormatTOML,
		Edit: func(root map[string]any) {
			delete(root, "mcp_servers")
		},
	}

	if err := op.run(context.Background(), cleanRunContext{Yes: true}); err != nil {
		t.Fatalf("expected no error cleaning empty TOML file, got: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "\n" {
		t.Fatalf("cleaned empty TOML = %q, want newline", string(got))
	}
}

func TestBuildCleanOps_ProjectScope(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	home := t.TempDir()

	harnesses := domain.AllHarnesses()
	ops := buildCleanOps(cleanTarget{eng: engine.New(nil, nil), scope: domain.ScopeProject, home: home, projectDir: dir, harnesses: harnesses, reg: testRegistry()})

	if len(ops) == 0 {
		t.Fatal("expected non-empty ops for project scope with all harnesses")
	}

	// Verify all returned paths are non-empty and absolute.
	for i, op := range ops {
		p := op.path()
		if p == "" {
			t.Errorf("ops[%d]: path is empty", i)
			continue
		}
		if !filepath.IsAbs(p) {
			t.Errorf("ops[%d]: path %q is not absolute", i, p)
		}
	}

	// Verify at least some paths are rooted under the project directory.
	foundProject := false
	for _, op := range ops {
		p := op.path()
		rel, err := filepath.Rel(dir, p)
		if err == nil && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, "..") {
			foundProject = true
			break
		}
	}
	if !foundProject {
		t.Error("expected at least one op path rooted under the project directory")
	}
}

func TestCleanCline_ProjectScope_DoesNotRemoveUnmanagedDotClineSkills(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	reg := testRegistry()
	h, err := reg.Lookup(domain.HarnessCline)
	if err != nil {
		t.Fatal(err)
	}
	layout := h.Layout(domain.ScopeProject, projectDir, t.TempDir())

	for _, root := range layout.ValidationRoots {
		if root == filepath.Join(projectDir, ".cline", "skills") {
			t.Fatalf("unexpected validation root for unmanaged path %q", root)
		}
	}
}

func TestCleanCline_GlobalScope_RemovesManagedPaths(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	reg := testRegistry()
	h, err := reg.Lookup(domain.HarnessCline)
	if err != nil {
		t.Fatal(err)
	}
	layout := h.Layout(domain.ScopeGlobal, home, home)

	gp := clharness.GlobalPathsFor(home)
	want := map[string]bool{
		filepath.Join(home, ".agents", "skills"): false,
		gp.RulesDir:                              false,
		gp.WorkflowsDir:                          false,
	}

	for _, root := range layout.RemovePaths {
		if _, ok := want[root]; ok {
			want[root] = true
		}
	}

	for path, found := range want {
		if !found {
			t.Fatalf("expected remove path for global path %q", path)
		}
	}
}

func TestRunClean_InvalidHarness(t *testing.T) {
	t.Parallel()

	err := RunClean(context.Background(), engine.New(nil, nil), CleanRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: t.TempDir(),
			Harnesses:  []domain.Harness{"not-a-real-harness"},
		},
		Yes: true,
	}, testRegistry())
	if err == nil {
		t.Fatal("expected error for invalid harness, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "unknown harness") {
		t.Fatalf("expected unknown harness error, got %q", got)
	}
}

func TestBuildCleanOps_OpenCodeDoesNotRemoveConfigParentDir(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	home := t.TempDir()

	ops := buildCleanOps(cleanTarget{eng: engine.New(nil, nil), scope: domain.ScopeProject, home: home, projectDir: projectDir, harnesses: []domain.Harness{domain.HarnessOpenCode}, reg: testRegistry()})

	configBase := filepath.Join(projectDir, ".opencode")
	for _, op := range ops {
		if op.path() == configBase {
			t.Fatalf(".opencode parent dir should not be a remove target (it contains a partially-owned settings file)")
		}
	}

	// Subdirectories should still be removed.
	wantRemoved := map[string]bool{
		filepath.Join(configBase, "agents"):   false,
		filepath.Join(configBase, "commands"): false,
		filepath.Join(configBase, "rules"):    false,
		filepath.Join(configBase, "skills"):   false,
	}
	for _, op := range ops {
		if _, ok := wantRemoved[op.path()]; ok {
			wantRemoved[op.path()] = true
		}
	}
	for p, found := range wantRemoved {
		if !found {
			t.Errorf("expected remove op for %q", p)
		}
	}
}

func TestBuildCleanOps_OpenCodeRemovesLedgerTrackedDropInFile(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	home := t.TempDir()

	dropIn := filepath.Join(projectDir, ".opencode", "oh-my-opencode.json")
	ledgerPath := testLedgerPath(domain.ScopeProject, projectDir, home, domain.HarnessOpenCode)
	eng := engine.New(nil, nil)
	ledger := domain.NewLedger()
	ledger.Managed[dropIn] = domain.Entry{Digest: "abc123", SourcePack: "test-pack"}
	if err := eng.SaveLedger(ledgerPath, ledger, false); err != nil {
		t.Fatalf("SaveLedger: %v", err)
	}

	ops := buildCleanOps(cleanTarget{eng: engine.New(nil, nil), scope: domain.ScopeProject, home: home, projectDir: projectDir, harnesses: []domain.Harness{domain.HarnessOpenCode}, reg: testRegistry()})
	found := false
	for _, op := range ops {
		if op.path() == dropIn {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected remove op for ledger-tracked drop-in %q", dropIn)
	}
}

func TestBuildCleanOps_ProjectLedgerWipeIncludesLegacyAndPerHarnessLedgers(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	home := t.TempDir()

	ops := buildCleanOps(cleanTarget{eng: engine.New(nil, nil), scope: domain.ScopeProject, home: home, projectDir: projectDir, harnesses: []domain.Harness{domain.HarnessClaudeCode}, wipeLedger: true, reg: testRegistry()})
	paths := map[string]bool{}
	for _, op := range ops {
		paths[op.path()] = true
	}

	cfgDir, _ := config.DefaultConfigDir(home)
	newLedgerPath := filepath.Join(cfgDir, "ledger", engine.EncodeProjectPath(projectDir))
	legacyLedgerPath := filepath.Join(projectDir, ".aipack", "ledger.json")

	if !paths[newLedgerPath] {
		t.Fatalf("expected project ledger wipe for %q", newLedgerPath)
	}
	if !paths[legacyLedgerPath] {
		t.Fatalf("expected legacy project ledger wipe for %q", legacyLedgerPath)
	}
}
