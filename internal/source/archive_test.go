package source

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	Name string
	Type byte
	Body string
	Link string
}

// helper to create a tar archive in memory
func buildTar(t *testing.T, entries []tarEntry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.Name,
			Size:     int64(len(e.Body)),
			Mode:     0o644,
			Typeflag: e.Type,
		}
		if e.Type == tar.TypeSymlink {
			hdr.Linkname = e.Link
			hdr.Size = 0
		}
		if e.Type == tar.TypeLink {
			hdr.Linkname = e.Link
			hdr.Size = 0
		}
		if e.Type == tar.TypeDir {
			hdr.Mode = 0o755
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if len(e.Body) > 0 {
			if _, err := tw.Write([]byte(e.Body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	tw.Close()
	return &buf
}

func TestExtractArchive_RegularFiles(t *testing.T) {
	archive := buildTar(t, []tarEntry{
		{Name: "subdir/", Type: tar.TypeDir},
		{Name: "subdir/hello.txt", Type: tar.TypeReg, Body: "hello world"},
		{Name: "root.txt", Type: tar.TypeReg, Body: "root content"},
	})

	dest := t.TempDir()
	_, err := ExtractArchive(archive, dest, ArchiveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify directory was created.
	info, err := os.Stat(filepath.Join(dest, "subdir"))
	if err != nil {
		t.Fatalf("subdir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("subdir is not a directory")
	}

	// Verify file contents.
	got, err := os.ReadFile(filepath.Join(dest, "subdir", "hello.txt"))
	if err != nil {
		t.Fatalf("reading hello.txt: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("hello.txt = %q, want %q", got, "hello world")
	}

	got, err = os.ReadFile(filepath.Join(dest, "root.txt"))
	if err != nil {
		t.Fatalf("reading root.txt: %v", err)
	}
	if string(got) != "root content" {
		t.Errorf("root.txt = %q, want %q", got, "root content")
	}
}

func TestExtractArchive_RejectsHardLinks(t *testing.T) {
	archive := buildTar(t, []tarEntry{
		{Name: "hard-link", Type: tar.TypeLink, Link: "/etc/passwd"},
	})

	dest := t.TempDir()
	_, err := ExtractArchive(archive, dest, ArchiveOpts{})
	if err == nil {
		t.Fatal("expected error for hard link, got nil")
	}
	if !strings.Contains(err.Error(), "hard link") {
		t.Errorf("error %q should contain 'hard link'", err)
	}
}

func TestExtractArchive_PathTraversal(t *testing.T) {
	archive := buildTar(t, []tarEntry{
		{Name: "../evil.txt", Type: tar.TypeReg, Body: "pwned"},
	})

	dest := t.TempDir()
	_, err := ExtractArchive(archive, dest, ArchiveOpts{})
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("error %q should contain 'traversal'", err)
	}
}

func TestExtractArchive_FileSizeLimit(t *testing.T) {
	bigBody := strings.Repeat("x", 2048)
	archive := buildTar(t, []tarEntry{
		{Name: "big.txt", Type: tar.TypeReg, Body: bigBody},
	})

	dest := t.TempDir()
	_, err := ExtractArchive(archive, dest, ArchiveOpts{MaxFileSize: 1024})
	if err == nil {
		t.Fatal("expected error for file size limit, got nil")
	}
	if !strings.Contains(err.Error(), "size limit") {
		t.Errorf("error %q should contain 'size limit'", err)
	}
}

func TestExtractArchive_TotalSizeLimit(t *testing.T) {
	body := strings.Repeat("a", 600)
	archive := buildTar(t, []tarEntry{
		{Name: "a.txt", Type: tar.TypeReg, Body: body},
		{Name: "b.txt", Type: tar.TypeReg, Body: body},
	})

	dest := t.TempDir()
	// Each file is 600 bytes; total = 1200 which exceeds 1000.
	_, err := ExtractArchive(archive, dest, ArchiveOpts{MaxFileSize: 1024, MaxTotalSize: 1000})
	if err == nil {
		t.Fatal("expected error for total size limit, got nil")
	}
	if !strings.Contains(err.Error(), "total extraction size exceeds limit") {
		t.Errorf("error %q should contain 'total extraction size exceeds limit'", err)
	}
}

func TestExtractArchive_ResolvesSymlinks(t *testing.T) {
	archive := buildTar(t, []tarEntry{
		{Name: "shared/", Type: tar.TypeDir},
		{Name: "shared/base.md", Type: tar.TypeReg, Body: "shared content"},
		{Name: "rules/", Type: tar.TypeDir},
		{Name: "rules/local.md", Type: tar.TypeReg, Body: "local content"},
		{Name: "rules/shared.md", Type: tar.TypeSymlink, Link: "../shared/base.md"},
	})

	dest := t.TempDir()
	result, err := ExtractArchive(archive, dest, ArchiveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.PendingSymlinks) != 0 {
		t.Fatalf("expected no pending symlinks, got %d", len(result.PendingSymlinks))
	}

	// Symlink should be resolved to a regular file.
	got, err := os.ReadFile(filepath.Join(dest, "rules", "shared.md"))
	if err != nil {
		t.Fatalf("reading resolved symlink: %v", err)
	}
	if string(got) != "shared content" {
		t.Errorf("resolved content = %q, want %q", string(got), "shared content")
	}

	// Verify it's a regular file, not a symlink.
	info, err := os.Lstat(filepath.Join(dest, "rules", "shared.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("expected regular file, got symlink")
	}
}

func TestExtractArchive_SymlinkTargetNotFound(t *testing.T) {
	// Symlink points to a file that doesn't exist in the archive.
	archive := buildTar(t, []tarEntry{
		{Name: "rules/", Type: tar.TypeDir},
		{Name: "rules/missing.md", Type: tar.TypeSymlink, Link: "../shared/gone.md"},
	})

	dest := t.TempDir()
	result, err := ExtractArchive(archive, dest, ArchiveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Target not in archive → returned as pending.
	if len(result.PendingSymlinks) != 1 {
		t.Fatalf("expected 1 pending symlink, got %d", len(result.PendingSymlinks))
	}
	if result.PendingSymlinks[0].TargetTarPath != filepath.Clean("shared/gone.md") {
		t.Errorf("pending target = %q, want %q", result.PendingSymlinks[0].TargetTarPath, "shared/gone.md")
	}
}

func TestExtractArchive_RejectsAbsoluteSymlinkTarget(t *testing.T) {
	archive := buildTar(t, []tarEntry{
		{Name: "evil-link", Type: tar.TypeSymlink, Link: "/etc/passwd"},
	})

	dest := t.TempDir()
	_, err := ExtractArchive(archive, dest, ArchiveOpts{})
	if err == nil {
		t.Fatal("expected error for absolute symlink target")
	}
	if !strings.Contains(err.Error(), "absolute symlink target") {
		t.Errorf("error %q should contain 'absolute symlink target'", err)
	}
}

func TestExtractArchive_RejectsEscapingSymlink(t *testing.T) {
	archive := buildTar(t, []tarEntry{
		{Name: "rules/escape.md", Type: tar.TypeSymlink, Link: "../../etc/passwd"},
	})

	dest := t.TempDir()
	_, err := ExtractArchive(archive, dest, ArchiveOpts{})
	if err == nil {
		t.Fatal("expected error for escaping symlink")
	}
	if !strings.Contains(err.Error(), "escapes archive boundary") {
		t.Errorf("error %q should contain 'escapes archive boundary'", err)
	}
}

func TestExtractArchive_RejectsSymlinkToGitDir(t *testing.T) {
	archive := buildTar(t, []tarEntry{
		{Name: ".git/", Type: tar.TypeDir},
		{Name: ".git/config", Type: tar.TypeReg, Body: "git config"},
		{Name: "rules/gitcfg.md", Type: tar.TypeSymlink, Link: "../.git/config"},
	})

	dest := t.TempDir()
	_, err := ExtractArchive(archive, dest, ArchiveOpts{})
	if err == nil {
		t.Fatal("expected error for .git symlink target")
	}
	if !strings.Contains(err.Error(), ".git") {
		t.Errorf("error %q should mention '.git'", err)
	}
}

func TestExtractArchive_RejectsChainedSymlinks(t *testing.T) {
	// Symlink A -> B where B is also a symlink.
	archive := buildTar(t, []tarEntry{
		{Name: "rules/", Type: tar.TypeDir},
		{Name: "rules/a.md", Type: tar.TypeSymlink, Link: "b.md"},
		{Name: "rules/b.md", Type: tar.TypeSymlink, Link: "../shared/base.md"},
	})

	dest := t.TempDir()
	_, err := ExtractArchive(archive, dest, ArchiveOpts{})
	if err == nil {
		t.Fatal("expected error for chained symlinks")
	}
	if !strings.Contains(err.Error(), "chained symlinks") {
		t.Errorf("error %q should contain 'chained symlinks'", err)
	}
}
