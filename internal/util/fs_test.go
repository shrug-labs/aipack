package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyDir_RejectsSymlinks(t *testing.T) {
	src := t.TempDir()

	// Create a regular file and a symlink pointing to it.
	regular := filepath.Join(src, "real.txt")
	if err := os.WriteFile(regular, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(src, "link.txt")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}

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
	if err := os.Symlink(sub, linkDir); err != nil {
		t.Fatal(err)
	}

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
}
