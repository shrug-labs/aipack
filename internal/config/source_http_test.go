package config

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func buildTestTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	dirs := make(map[string]bool)
	for _, name := range keys {
		parts := strings.Split(name, "/")
		for i := 1; i < len(parts); i++ {
			dir := strings.Join(parts[:i], "/") + "/"
			if !dirs[dir] {
				tw.WriteHeader(&tar.Header{Name: dir, Typeflag: tar.TypeDir, Mode: 0o755})
				dirs[dir] = true
			}
		}
		content := files[name]
		tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(content)), Typeflag: tar.TypeReg, Mode: 0o644})
		tw.Write([]byte(content))
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func TestGitHubTarballURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		ref     string
		want    string
		wantErr bool
	}{
		{"basic", "https://github.com/acme/pack", "main", "https://github.com/acme/pack/archive/main.tar.gz", false},
		{"with .git", "https://github.com/acme/pack.git", "v1.0", "https://github.com/acme/pack/archive/v1.0.tar.gz", false},
		{"empty ref", "https://github.com/acme/pack", "", "https://github.com/acme/pack/archive/HEAD.tar.gz", false},
		{"not github", "https://example.com/foo", "main", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GitHubTarballURL(tt.url, tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFetchHTTPTarball_HappyPath(t *testing.T) {
	tgz := buildTestTarGz(t, map[string]string{
		"repo-main/rules/foo.md": "# Rule Foo",
		"repo-main/pack.json":    `{"name":"test"}`,
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(tgz)
	}))
	defer srv.Close()

	dest := t.TempDir()
	err := FetchHTTPTarball(context.Background(), srv.URL+"/archive/main.tar.gz", dest, "", ArchiveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "rules", "foo.md"))
	if err != nil {
		t.Fatalf("reading rules/foo.md: %v", err)
	}
	if string(got) != "# Rule Foo" {
		t.Errorf("rules/foo.md = %q, want %q", got, "# Rule Foo")
	}

	got, err = os.ReadFile(filepath.Join(dest, "pack.json"))
	if err != nil {
		t.Fatalf("reading pack.json: %v", err)
	}
	if string(got) != `{"name":"test"}` {
		t.Errorf("pack.json = %q, want %q", got, `{"name":"test"}`)
	}
}

func TestFetchHTTPTarball_SubPath(t *testing.T) {
	tgz := buildTestTarGz(t, map[string]string{
		"repo-main/mypak/pack.json": `{"sub":"pack"}`,
		"repo-main/other/ignore.md": "should be skipped",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tgz)
	}))
	defer srv.Close()

	dest := t.TempDir()
	err := FetchHTTPTarball(context.Background(), srv.URL, dest, "mypak", ArchiveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "pack.json"))
	if err != nil {
		t.Fatalf("reading pack.json: %v", err)
	}
	if string(got) != `{"sub":"pack"}` {
		t.Errorf("pack.json = %q, want %q", got, `{"sub":"pack"}`)
	}

	// "other" should not exist
	if _, err := os.Stat(filepath.Join(dest, "other")); !os.IsNotExist(err) {
		t.Error("expected 'other' directory to not exist")
	}

	// "mypak" subdirectory should not exist — files land directly in dest
	if _, err := os.Stat(filepath.Join(dest, "mypak")); !os.IsNotExist(err) {
		t.Error("expected 'mypak' directory to not exist in dest")
	}
}

func TestFetchHTTPTarball_RejectsSymlinks(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{Name: "repo-main/", Typeflag: tar.TypeDir, Mode: 0o755})
	tw.WriteHeader(&tar.Header{Name: "repo-main/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o644})
	tw.Close()
	gw.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	dest := t.TempDir()
	err := FetchHTTPTarball(context.Background(), srv.URL, dest, "", ArchiveOpts{})
	if err == nil {
		t.Fatal("expected error for symlink")
	}
	if !strings.Contains(err.Error(), "link") {
		t.Errorf("error %q should mention link", err)
	}
}

func TestFetchHTTPTarball_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dest := t.TempDir()
	err := FetchHTTPTarball(context.Background(), srv.URL, dest, "", ArchiveOpts{})
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
	if !errors.Is(err, ErrHTTPTarballFailed) {
		t.Errorf("error should wrap ErrHTTPTarballFailed, got: %v", err)
	}
}

func TestFetchFileHTTPTarball_HappyPath(t *testing.T) {
	tgz := buildTestTarGz(t, map[string]string{
		"repo-main/config.yaml": "key: value",
		"repo-main/other.txt":   "other",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tgz)
	}))
	defer srv.Close()

	got, err := FetchFileHTTPTarball(context.Background(), srv.URL, "config.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "key: value" {
		t.Errorf("got %q, want %q", got, "key: value")
	}
}

func TestFetchFileHTTPTarball_NotFound(t *testing.T) {
	tgz := buildTestTarGz(t, map[string]string{
		"repo-main/exists.txt": "here",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tgz)
	}))
	defer srv.Close()

	_, err := FetchFileHTTPTarball(context.Background(), srv.URL, "missing.txt")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q should contain 'not found'", err)
	}
}
