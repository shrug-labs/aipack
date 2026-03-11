package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestRemovePathOp_Nonexistent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	op := removePathOp{Path: filepath.Join(dir, "does-not-exist")}
	ctx := cleanRunContext{Yes: true}

	if err := op.run(ctx); err != nil {
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

	if err := op.run(ctx); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed, but it still exists")
	}
}

func TestBuildCleanOps_ProjectScope(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	home := t.TempDir()

	harnesses := domain.AllHarnesses()
	ops := buildCleanOps(domain.ScopeProject, home, dir, harnesses, false)

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

func TestCleanClineOps_ProjectScope_DoesNotRemoveUnmanagedDotClineSkills(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	ops := cleanClineOps(domain.ScopeProject, t.TempDir(), projectDir)

	for _, op := range ops {
		if op.path() == filepath.Join(projectDir, ".cline", "skills") {
			t.Fatalf("unexpected clean op for unmanaged path %q", op.path())
		}
	}
}

func TestCleanClineOps_GlobalScope_RemovesManagedPaths(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	ops := cleanClineOps(domain.ScopeGlobal, home, t.TempDir())

	want := map[string]bool{
		filepath.Join(home, ".cline", "skills"):                          false,
		filepath.Join(home, "Documents", "Cline", "Rules", "aipack"):     false,
		filepath.Join(home, "Documents", "Cline", "Agents", "aipack"):    false,
		filepath.Join(home, "Documents", "Cline", "Workflows", "aipack"): false,
	}

	for _, op := range ops {
		if _, ok := want[op.path()]; ok {
			want[op.path()] = true
		}
	}

	for path, found := range want {
		if !found {
			t.Fatalf("expected clean op for managed global path %q", path)
		}
	}
}

func TestRunClean_InvalidHarness(t *testing.T) {
	t.Parallel()

	err := RunClean(CleanRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: t.TempDir(),
			Harnesses:  []domain.Harness{"not-a-real-harness"},
		},
		Yes: true,
	})
	if err == nil {
		t.Fatal("expected error for invalid harness, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "unknown harness") {
		t.Fatalf("expected unknown harness error, got %q", got)
	}
}
