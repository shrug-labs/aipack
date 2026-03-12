package config

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
	err := ExtractArchive(archive, dest, ArchiveOpts{})
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

func TestExtractArchive_RejectsSymlinks(t *testing.T) {
	archive := buildTar(t, []tarEntry{
		{Name: "evil-link", Type: tar.TypeSymlink, Link: "/etc/passwd"},
	})

	dest := t.TempDir()
	err := ExtractArchive(archive, dest, ArchiveOpts{})
	if err == nil {
		t.Fatal("expected error for symlink, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error %q should contain 'symlink'", err)
	}
}

func TestExtractArchive_RejectsHardLinks(t *testing.T) {
	archive := buildTar(t, []tarEntry{
		{Name: "hard-link", Type: tar.TypeLink, Link: "/etc/passwd"},
	})

	dest := t.TempDir()
	err := ExtractArchive(archive, dest, ArchiveOpts{})
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
	err := ExtractArchive(archive, dest, ArchiveOpts{})
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
	err := ExtractArchive(archive, dest, ArchiveOpts{MaxFileSize: 1024})
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
	err := ExtractArchive(archive, dest, ArchiveOpts{MaxFileSize: 1024, MaxTotalSize: 1000})
	if err == nil {
		t.Fatal("expected error for total size limit, got nil")
	}
	if !strings.Contains(err.Error(), "total extraction size exceeds limit") {
		t.Errorf("error %q should contain 'total extraction size exceeds limit'", err)
	}
}
