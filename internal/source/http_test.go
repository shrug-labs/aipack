package source

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func buildTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildTestTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	keys := slices.Sorted(maps.Keys(files))

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

func TestFetchHTTPArchive_ZipHappyPath(t *testing.T) {
	t.Parallel()
	zipData := buildTestZip(t, map[string]string{
		"repo-main/pack.json":    `{"name":"zip-pack"}`,
		"repo-main/rules/foo.md": "# Rule Foo",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipData)
	}))
	defer srv.Close()

	dest := t.TempDir()
	if err := FetchHTTPArchive(context.Background(), srv.URL+"/pack.zip", dest, HTTPArchiveOptions{}); err != nil {
		t.Fatalf("FetchHTTPArchive: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "repo-main", "rules", "foo.md"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(got) != "# Rule Foo" {
		t.Fatalf("extracted = %q", got)
	}
}

func TestFetchHTTPArchiveObserved_ConditionalValidators(t *testing.T) {
	t.Parallel()
	const (
		etag         = `"archive-v1"`
		lastModified = "Wed, 22 Jul 2026 12:00:00 GMT"
	)
	tests := []struct {
		name             string
		etag             string
		wantRequestETag  string
		wantRequestSince string
	}{
		{name: "etag takes precedence", etag: etag, wantRequestETag: etag},
		{name: "last modified fallback", wantRequestSince: lastModified},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			zipData := buildTestZip(t, map[string]string{"pack.json": `{"name":"conditional"}`})
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if test.etag != "" {
					w.Header().Set("ETag", test.etag)
				}
				w.Header().Set("Last-Modified", lastModified)
				if requests == 2 {
					if got := r.Header.Get("If-None-Match"); got != test.wantRequestETag {
						t.Errorf("If-None-Match = %q, want %q", got, test.wantRequestETag)
					}
					if got := r.Header.Get("If-Modified-Since"); got != test.wantRequestSince {
						t.Errorf("If-Modified-Since = %q, want %q", got, test.wantRequestSince)
					}
					w.WriteHeader(http.StatusNotModified)
					return
				}
				w.Write(zipData)
			}))
			defer srv.Close()

			first, err := FetchHTTPArchiveObserved(
				context.Background(), srv.URL+"/pack.zip", t.TempDir(), HTTPArchiveOptions{},
			)
			if err != nil {
				t.Fatalf("first fetch: %v", err)
			}
			second, err := FetchHTTPArchiveObserved(
				context.Background(), srv.URL+"/pack.zip", t.TempDir(),
				HTTPArchiveOptions{Validator: first.Validator},
			)
			if err != nil {
				t.Fatalf("conditional fetch: %v", err)
			}
			if !second.NotModified || requests != 2 {
				t.Fatalf("second observation = %+v, requests = %d", second, requests)
			}
		})
	}
}

func TestFetchHTTPArchive_UsesNetrc(t *testing.T) {
	t.Parallel()
	zipData := buildTestZip(t, map[string]string{"pack.json": `{"name":"auth-pack"}`})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "alice" || pass != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Write(zipData)
	}))
	defer srv.Close()

	netrcPath := filepath.Join(t.TempDir(), ".netrc")
	host := strings.TrimPrefix(srv.URL, "http://")
	if err := os.WriteFile(netrcPath, []byte("machine "+host+" login alice password secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	err := FetchHTTPArchive(context.Background(), srv.URL+"/pack.zip", dest, HTTPArchiveOptions{NetrcPath: netrcPath})
	if err != nil {
		t.Fatalf("FetchHTTPArchive with netrc: %v", err)
	}
}

func TestFetchHTTPArchive_RejectsEscapingTarSymlink(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "repo/link", Typeflag: tar.TypeSymlink, Linkname: "../outside"}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	err := FetchHTTPArchive(context.Background(), srv.URL+"/pack.tar.gz", t.TempDir(), HTTPArchiveOptions{})
	if err == nil || !strings.Contains(err.Error(), "escapes archive boundary") {
		t.Fatalf("expected escaping symlink error, got %v", err)
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

// buildTestTarGzEntries wraps buildTar output in gzip compression.
func buildTestTarGzEntries(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	tarBuf := buildTar(t, entries)
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(tarBuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	gw.Close()
	return buf.Bytes()
}

func TestFetchHTTPTarball_ResolvesSymlinksNoSubPath(t *testing.T) {
	tgz := buildTestTarGzEntries(t, []tarEntry{
		{Name: "repo-main/", Type: tar.TypeDir},
		{Name: "repo-main/shared/", Type: tar.TypeDir},
		{Name: "repo-main/shared/base.md", Type: tar.TypeReg, Body: "shared content"},
		{Name: "repo-main/rules/", Type: tar.TypeDir},
		{Name: "repo-main/rules/local.md", Type: tar.TypeReg, Body: "local"},
		{Name: "repo-main/rules/shared.md", Type: tar.TypeSymlink, Link: "../shared/base.md"},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tgz)
	}))
	defer srv.Close()

	dest := t.TempDir()
	err := FetchHTTPTarball(context.Background(), srv.URL, dest, "", ArchiveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "rules", "shared.md"))
	if err != nil {
		t.Fatalf("reading resolved symlink: %v", err)
	}
	if string(got) != "shared content" {
		t.Errorf("resolved content = %q, want %q", string(got), "shared content")
	}
}

func TestFetchHTTPTarball_ResolvesSymlinksWithSubPath(t *testing.T) {
	// Repo structure: shared/ at root, pack in packs/team-ops/
	// Symlink in pack points to ../../shared/base.md
	tgz := buildTestTarGzEntries(t, []tarEntry{
		{Name: "repo-main/", Type: tar.TypeDir},
		{Name: "repo-main/shared/", Type: tar.TypeDir},
		{Name: "repo-main/shared/base.md", Type: tar.TypeReg, Body: "shared from repo root"},
		{Name: "repo-main/packs/", Type: tar.TypeDir},
		{Name: "repo-main/packs/team-ops/", Type: tar.TypeDir},
		{Name: "repo-main/packs/team-ops/pack.json", Type: tar.TypeReg, Body: `{"name":"team-ops"}`},
		{Name: "repo-main/packs/team-ops/rules/", Type: tar.TypeDir},
		{Name: "repo-main/packs/team-ops/rules/local.md", Type: tar.TypeReg, Body: "local rule"},
		{Name: "repo-main/packs/team-ops/rules/shared.md", Type: tar.TypeSymlink, Link: "../../../shared/base.md"},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tgz)
	}))
	defer srv.Close()

	dest := t.TempDir()
	err := FetchHTTPTarball(context.Background(), srv.URL, dest, "packs/team-ops", ArchiveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Symlink should be resolved via second pass (target is outside subPath).
	got, err := os.ReadFile(filepath.Join(dest, "rules", "shared.md"))
	if err != nil {
		t.Fatalf("reading resolved symlink: %v", err)
	}
	if string(got) != "shared from repo root" {
		t.Errorf("resolved content = %q, want %q", string(got), "shared from repo root")
	}

	// Regular file should also be present.
	got, err = os.ReadFile(filepath.Join(dest, "rules", "local.md"))
	if err != nil {
		t.Fatalf("reading regular file: %v", err)
	}
	if string(got) != "local rule" {
		t.Errorf("regular content = %q, want %q", string(got), "local rule")
	}

	// Shared directory should NOT be in dest (it's outside subPath).
	if _, err := os.Stat(filepath.Join(dest, "shared")); !os.IsNotExist(err) {
		t.Error("shared/ directory should not exist in dest")
	}
}

func TestFetchHTTPTarball_RejectsEscapingSymlink(t *testing.T) {
	tgz := buildTestTarGzEntries(t, []tarEntry{
		{Name: "repo-main/", Type: tar.TypeDir},
		{Name: "repo-main/rules/", Type: tar.TypeDir},
		{Name: "repo-main/rules/evil.md", Type: tar.TypeSymlink, Link: "../../etc/passwd"},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tgz)
	}))
	defer srv.Close()

	dest := t.TempDir()
	err := FetchHTTPTarball(context.Background(), srv.URL, dest, "", ArchiveOpts{})
	if err == nil {
		t.Fatal("expected error for escaping symlink")
	}
	if !strings.Contains(err.Error(), "escapes archive boundary") {
		t.Errorf("error %q should mention 'escapes archive boundary'", err)
	}
}

func TestFetchHTTPTarball_RejectsSymlinkToGitDir(t *testing.T) {
	tgz := buildTestTarGzEntries(t, []tarEntry{
		{Name: "repo-main/", Type: tar.TypeDir},
		{Name: "repo-main/.git/", Type: tar.TypeDir},
		{Name: "repo-main/.git/config", Type: tar.TypeReg, Body: "git config"},
		{Name: "repo-main/rules/", Type: tar.TypeDir},
		{Name: "repo-main/rules/gitcfg.md", Type: tar.TypeSymlink, Link: "../.git/config"},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tgz)
	}))
	defer srv.Close()

	dest := t.TempDir()
	err := FetchHTTPTarball(context.Background(), srv.URL, dest, "", ArchiveOpts{})
	if err == nil {
		t.Fatal("expected error for .git symlink target")
	}
	if !strings.Contains(err.Error(), ".git") {
		t.Errorf("error %q should mention '.git'", err)
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
