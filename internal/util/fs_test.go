package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/testutil"
)

func TestCopyDir_RejectsSymlinks(t *testing.T) {
	src := t.TempDir()

	// Create a regular file and a symlink pointing to it.
	regular := filepath.Join(src, "real.txt")
	if err := os.WriteFile(regular, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(src, "link.txt")
	testutil.Symlink(t, regular, link)

	dst := t.TempDir()
	err := CopyDir(src, dst)
	if err == nil {
		t.Fatal("expected error for symlink, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected error to mention 'symlink', got: %s", err.Error())
	}
}

func TestCopyDir_RejectsSymlinkDir(t *testing.T) {
	src := t.TempDir()

	// Create a real subdirectory and a symlink to it.
	sub := filepath.Join(src, "realdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "file.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(src, "linkdir")
	testutil.Symlink(t, sub, linkDir)

	dst := t.TempDir()
	err := CopyDir(src, dst)
	if err == nil {
		t.Fatal("expected error for symlink directory, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected error to mention 'symlink', got: %s", err.Error())
	}
}

func TestCopyDir_RegularFiles(t *testing.T) {
	src := t.TempDir()

	// Create a nested directory structure with files.
	sub := filepath.Join(src, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"root.txt":         "root content",
		"subdir/child.txt": "child content",
	}
	for rel, content := range files {
		p := filepath.Join(src, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dst := t.TempDir()
	if err := CopyDir(src, dst); err != nil {
		t.Fatalf("CopyDir failed: %v", err)
	}

	// Verify all files were copied with correct content.
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Errorf("missing copied file %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("file %s: got %q, want %q", rel, string(got), want)
		}
	}

	// Verify destination directory exists.
	info, err := os.Stat(filepath.Join(dst, "subdir"))
	if err != nil {
		t.Fatalf("subdir not copied: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("subdir should be a directory")
	}
}

func TestReplaceDirAtomic_FreshInstall(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	staging := filepath.Join(parent, "staging")
	os.Mkdir(staging, 0o755)
	os.WriteFile(filepath.Join(staging, "file.txt"), []byte("new"), 0o644)

	dest := filepath.Join(parent, "dest")
	if err := ReplaceDirAtomic(dest, staging); err != nil {
		t.Fatalf("ReplaceDirAtomic: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "file.txt"))
	if err != nil {
		t.Fatalf("expected file in dest: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("got %q, want %q", string(got), "new")
	}
}

func TestReplaceDirAtomic_ReplaceExisting(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()

	dest := filepath.Join(parent, "dest")
	os.MkdirAll(dest, 0o755)
	os.WriteFile(filepath.Join(dest, "old.txt"), []byte("old"), 0o644)

	staging := filepath.Join(parent, "staging")
	os.Mkdir(staging, 0o755)
	os.WriteFile(filepath.Join(staging, "new.txt"), []byte("new"), 0o644)

	if err := ReplaceDirAtomic(dest, staging); err != nil {
		t.Fatalf("ReplaceDirAtomic: %v", err)
	}

	// New content present.
	got, err := os.ReadFile(filepath.Join(dest, "new.txt"))
	if err != nil {
		t.Fatalf("new.txt missing: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("got %q, want %q", string(got), "new")
	}
	// Old content gone.
	if _, err := os.Stat(filepath.Join(dest, "old.txt")); !os.IsNotExist(err) {
		t.Error("old.txt should not exist after replace")
	}
	// No leftover backup.
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			t.Errorf("leftover backup: %s", e.Name())
		}
	}
}

func TestReplaceDirAtomic_StagingMissing_PreservesExisting(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()

	dest := filepath.Join(parent, "dest")
	os.MkdirAll(dest, 0o755)
	os.WriteFile(filepath.Join(dest, "keep.txt"), []byte("keep"), 0o644)

	err := ReplaceDirAtomic(dest, filepath.Join(parent, "nonexistent"))
	if err == nil {
		t.Fatal("expected error for missing staging dir")
	}
	// Original content must be restored.
	got, rerr := os.ReadFile(filepath.Join(dest, "keep.txt"))
	if rerr != nil {
		t.Fatalf("dest should be restored after failed replace: %v", rerr)
	}
	if string(got) != "keep" {
		t.Errorf("got %q, want %q", string(got), "keep")
	}
}

func TestIsBackupDir(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want bool
	}{
		{".my-pack.bak-20260405T120000Z-deadbeef", true},
		{".foo.bak-20260101T000000Z-00000000", true},
		{".hidden", false},
		{"my-pack.bak-20260405T120000Z-deadbeef", false}, // no dot prefix
		{"", false},
		{".bak-only", false}, // no timestamp-hex suffix
		{".x.bak-y", false},  // no timestamp-hex suffix
		{".a.bak-not-a-date", false},
	}
	for _, tt := range tests {
		if got := IsBackupDir(tt.name); got != tt.want {
			t.Errorf("IsBackupDir(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestIsWithinDir(t *testing.T) {
	tests := []struct {
		path, dir string
		want      bool
	}{
		{"/a/b/c", "/a/b", true},
		{"/a/b", "/a/b", true},
		{"/a/bc", "/a/b", false},
		{"/a", "/a/b", false},
		{"/x/y", "/a/b", false},
	}
	for _, tt := range tests {
		got := IsWithinDir(tt.path, tt.dir)
		if got != tt.want {
			t.Errorf("IsWithinDir(%q, %q) = %v, want %v", tt.path, tt.dir, got, tt.want)
		}
	}
}

func TestValidateSymlinkTarget_WithinBoundary(t *testing.T) {
	boundary := t.TempDir()
	target := filepath.Join(boundary, "file.txt")
	if err := os.WriteFile(target, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	// In real usage, resolvedTarget comes from filepath.EvalSymlinks.
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSymlinkTarget(resolved, boundary); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateSymlinkTarget_EscapesBoundary(t *testing.T) {
	boundary := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "escape.txt")
	if err := os.WriteFile(target, []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := ValidateSymlinkTarget(target, boundary)
	if err == nil {
		t.Fatal("expected error for target outside boundary")
	}
	if !strings.Contains(err.Error(), "escapes boundary") {
		t.Errorf("error %q should mention 'escapes boundary'", err)
	}
}

func TestValidateSymlinkTarget_GitDir(t *testing.T) {
	boundary := t.TempDir()
	gitDir := filepath.Join(boundary, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(gitDir, "config")
	if err := os.WriteFile(target, []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := ValidateSymlinkTarget(target, boundary)
	if err == nil {
		t.Fatal("expected error for .git traversal")
	}
	if !strings.Contains(err.Error(), ".git") {
		t.Errorf("error %q should mention '.git'", err)
	}
}

func TestValidateSymlinkTarget_DirectoryRejected(t *testing.T) {
	boundary := t.TempDir()
	sub := filepath.Join(boundary, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, ferr := filepath.EvalSymlinks(sub)
	if ferr != nil {
		t.Fatal(ferr)
	}
	err := ValidateSymlinkTarget(resolved, boundary)
	if err == nil {
		t.Fatal("expected error for directory target")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error %q should mention 'directory'", err)
	}
}

func TestFindRepoRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got := FindRepoRoot(sub)
	if got != root {
		t.Errorf("FindRepoRoot(%q) = %q, want %q", sub, got, root)
	}
}

func TestFindRepoRoot_NoGit(t *testing.T) {
	dir := t.TempDir()
	got := FindRepoRoot(dir)
	if got != "" {
		t.Errorf("FindRepoRoot(%q) = %q, want empty", dir, got)
	}
}

func TestCopyDirResolvingSymlinks_ResolvesFileSymlink(t *testing.T) {
	// Create a repo-like structure with shared content.
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "base.md"), []byte("shared content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pack directory with a symlink to shared content.
	packDir := filepath.Join(root, "packs", "team")
	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "rules", "local.md"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Symlink: packs/team/rules/shared.md -> ../../../shared/base.md
	testutil.Symlink(t, filepath.Join(shared, "base.md"), filepath.Join(packDir, "rules", "shared.md"))

	dst := t.TempDir()
	if err := CopyDirResolvingSymlinks(packDir, dst, root); err != nil {
		t.Fatalf("CopyDirResolvingSymlinks failed: %v", err)
	}

	// Verify the symlink was resolved to a regular file with correct content.
	got, err := os.ReadFile(filepath.Join(dst, "rules", "shared.md"))
	if err != nil {
		t.Fatalf("missing resolved file: %v", err)
	}
	if string(got) != "shared content" {
		t.Errorf("resolved content = %q, want %q", string(got), "shared content")
	}

	// Verify the file is a regular file, not a symlink.
	info, err := os.Lstat(filepath.Join(dst, "rules", "shared.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("expected regular file, got symlink")
	}

	// Verify regular files are also copied.
	got, err = os.ReadFile(filepath.Join(dst, "rules", "local.md"))
	if err != nil {
		t.Fatalf("missing regular file: %v", err)
	}
	if string(got) != "local" {
		t.Errorf("regular file content = %q, want %q", string(got), "local")
	}
}

func TestCopyDirResolvingSymlinks_RejectsEscapingBoundary(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	packDir := filepath.Join(root, "pack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Symlink(t, filepath.Join(outside, "secret.txt"), filepath.Join(packDir, "evil.md"))

	dst := t.TempDir()
	err := CopyDirResolvingSymlinks(packDir, dst, root)
	if err == nil {
		t.Fatal("expected error for escaping symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error %q should mention 'symlink'", err)
	}
}

func TestCopyDirResolvingSymlinks_EmptyBoundaryRejects(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "real.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Symlink(t, filepath.Join(src, "real.txt"), filepath.Join(src, "link.txt"))

	dst := t.TempDir()
	err := CopyDirResolvingSymlinks(src, dst, "")
	if err == nil {
		t.Fatal("expected error for symlink with empty boundary")
	}
	if !strings.Contains(err.Error(), "no git repository found") {
		t.Errorf("error %q should mention 'no git repository found'", err)
	}
}

func TestCopyDirResolvingSymlinks_RejectsDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "file.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	packDir := filepath.Join(root, "pack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Symlink(t, sub, filepath.Join(packDir, "linked-dir"))

	dst := t.TempDir()
	err := CopyDirResolvingSymlinks(packDir, dst, root)
	if err == nil {
		t.Fatal("expected error for directory symlink")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error %q should mention 'directory'", err)
	}
}

func TestResolveSymlinksInDir(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "base.md"), []byte("resolved"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "regular.md"), []byte("normal"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create symlink at root level pointing to shared/base.md
	testutil.Symlink(t, filepath.Join(shared, "base.md"), filepath.Join(root, "link.md"))

	if err := ResolveSymlinksInDir(root); err != nil {
		t.Fatalf("ResolveSymlinksInDir failed: %v", err)
	}

	// Verify symlink was replaced with regular file.
	info, err := os.Lstat(filepath.Join(root, "link.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("expected regular file after resolution, got symlink")
	}
	got, err := os.ReadFile(filepath.Join(root, "link.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "resolved" {
		t.Errorf("resolved content = %q, want %q", string(got), "resolved")
	}

	// Regular file should be unchanged.
	got, err = os.ReadFile(filepath.Join(root, "regular.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "normal" {
		t.Errorf("regular content = %q, want %q", string(got), "normal")
	}
}

func TestResolveSymlinksInDir_SkipsGitDir(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Symlink inside .git should be ignored.
	if err := os.WriteFile(filepath.Join(gitDir, "target"), []byte("git"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Symlink(t, filepath.Join(gitDir, "target"), filepath.Join(gitDir, "link"))

	// Should succeed without touching .git.
	if err := ResolveSymlinksInDir(root); err != nil {
		t.Fatalf("ResolveSymlinksInDir failed: %v", err)
	}
}

func TestCopyDir_SkipsIgnoredNames(t *testing.T) {
	src := t.TempDir()

	if err := os.WriteFile(filepath.Join(src, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".DS_Store"), []byte("ignore"), 0o644); err != nil {
		t.Fatal(err)
	}
	pycache := filepath.Join(src, "__pycache__")
	if err := os.Mkdir(pycache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pycache, "mod.pyc"), []byte("bytecode"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create ignored directories: .venv, .git, node_modules
	for _, dir := range []string{".venv", ".git", "node_modules"} {
		d := filepath.Join(src, dir)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "file.txt"), []byte("should be ignored"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dst := t.TempDir()
	if err := CopyDir(src, dst); err != nil {
		t.Fatalf("CopyDir failed: %v", err)
	}

	// keep.txt should exist.
	if _, err := os.Stat(filepath.Join(dst, "keep.txt")); err != nil {
		t.Error("keep.txt should have been copied")
	}
	// .DS_Store should not exist.
	if _, err := os.Stat(filepath.Join(dst, ".DS_Store")); err == nil {
		t.Error(".DS_Store should have been skipped")
	}
	// __pycache__ directory should not exist.
	if _, err := os.Stat(filepath.Join(dst, "__pycache__")); err == nil {
		t.Error("__pycache__ should have been skipped")
	}
	// .venv, .git, node_modules should not exist.
	for _, dir := range []string{".venv", ".git", "node_modules"} {
		if _, err := os.Stat(filepath.Join(dst, dir)); err == nil {
			t.Errorf("%s should have been skipped", dir)
		}
	}
}
