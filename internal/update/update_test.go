package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssetName(t *testing.T) {
	name := AssetName()
	if !strings.HasPrefix(name, "aipack-") {
		t.Fatalf("expected aipack- prefix, got %s", name)
	}
	parts := strings.Split(name, "-")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts (aipack-os-arch), got %d: %s", len(parts), name)
	}
}

func TestFetchChecksum(t *testing.T) {
	sums := "abc123def456  aipack-linux-amd64\n789012fed345  aipack-darwin-arm64\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sums))
	}))
	defer srv.Close()

	got, err := fetchChecksum(context.Background(), srv.URL, "aipack-darwin-arm64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "789012fed345" {
		t.Fatalf("expected 789012fed345, got %s", got)
	}
}

func TestFetchChecksum_Missing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("abc123  aipack-linux-amd64\n"))
	}))
	defer srv.Close()

	_, err := fetchChecksum(context.Background(), srv.URL, "aipack-darwin-arm64")
	if err == nil || !strings.Contains(err.Error(), "no checksum found") {
		t.Fatalf("expected 'no checksum found' error, got: %v", err)
	}
}

func TestFetchChecksum_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := fetchChecksum(context.Background(), srv.URL, "anything")
	if err == nil {
		t.Fatal("expected error on HTTP 404")
	}
}

func TestUpdate_DevVersion(t *testing.T) {
	var buf bytes.Buffer
	err := Update(context.Background(), "dev", &buf)
	if err == nil || !strings.Contains(err.Error(), "dev build") {
		t.Fatalf("expected dev build error, got: %v", err)
	}
}

func TestUpdate_EmptyVersion(t *testing.T) {
	var buf bytes.Buffer
	err := Update(context.Background(), "", &buf)
	if err == nil || !strings.Contains(err.Error(), "dev build") {
		t.Fatalf("expected dev build error, got: %v", err)
	}
}

// Tests below mutate package-level vars — do NOT add t.Parallel().

func TestUpdate_AlreadyLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://github.com/shrug-labs/aipack/releases/tag/v0.9.0", http.StatusFound)
	}))
	defer srv.Close()

	origURL := releaseURL
	releaseURL = srv.URL
	t.Cleanup(func() { releaseURL = origURL })

	var buf bytes.Buffer
	if err := Update(context.Background(), "0.9.0", &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "already the latest") {
		t.Fatalf("expected 'already the latest', got: %s", buf.String())
	}
}

func TestUpdate_HappyPath(t *testing.T) {
	dir := t.TempDir()
	currentBin := filepath.Join(dir, "aipack")
	os.WriteFile(currentBin, []byte("old"), 0o755)

	newBinary := []byte("new binary content for testing")
	h := sha256.Sum256(newBinary)
	hashStr := hex.EncodeToString(h[:])
	asset := AssetName()
	sums := fmt.Sprintf("%s  %s\n", hashStr, asset)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			http.Redirect(w, r, "https://github.com/shrug-labs/aipack/releases/tag/v1.0.0", http.StatusFound)
		case strings.HasSuffix(r.URL.Path, "/"+asset):
			w.Write(newBinary)
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			w.Write([]byte(sums))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	origURL := releaseURL
	origBase := ghDownloadBase
	origExec := executablePath
	releaseURL = srv.URL + "/releases/latest"
	ghDownloadBase = srv.URL + "/releases/download"
	executablePath = func() (string, error) { return currentBin, nil }
	t.Cleanup(func() {
		releaseURL = origURL
		ghDownloadBase = origBase
		executablePath = origExec
	})

	var buf bytes.Buffer
	if err := Update(context.Background(), "0.9.0", &buf); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(currentBin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newBinary) {
		t.Fatalf("binary not replaced: got %q", got)
	}
	if !strings.Contains(buf.String(), "Updated to aipack 1.0.0") {
		t.Fatalf("unexpected output: %s", buf.String())
	}
}

func TestUpdate_ChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	currentBin := filepath.Join(dir, "aipack")
	os.WriteFile(currentBin, []byte("old"), 0o755)

	asset := AssetName()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			http.Redirect(w, r, "https://github.com/shrug-labs/aipack/releases/tag/v1.0.0", http.StatusFound)
		case strings.HasSuffix(r.URL.Path, "/"+asset):
			w.Write([]byte("binary content"))
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			w.Write([]byte(fmt.Sprintf("0000000000000000000000000000000000000000000000000000000000000000  %s\n", asset)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	origURL := releaseURL
	origBase := ghDownloadBase
	origExec := executablePath
	releaseURL = srv.URL + "/releases/latest"
	ghDownloadBase = srv.URL + "/releases/download"
	executablePath = func() (string, error) { return currentBin, nil }
	t.Cleanup(func() {
		releaseURL = origURL
		ghDownloadBase = origBase
		executablePath = origExec
	})

	var buf bytes.Buffer
	err := Update(context.Background(), "0.9.0", &buf)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got: %v", err)
	}

	// Original binary must be untouched.
	got, _ := os.ReadFile(currentBin)
	if string(got) != "old" {
		t.Fatal("binary should not have been replaced on checksum failure")
	}
}

func TestUpdate_BinaryNotFound(t *testing.T) {
	dir := t.TempDir()
	currentBin := filepath.Join(dir, "aipack")
	os.WriteFile(currentBin, []byte("old"), 0o755)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			http.Redirect(w, r, "https://github.com/shrug-labs/aipack/releases/tag/v1.0.0", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	origURL := releaseURL
	origBase := ghDownloadBase
	origExec := executablePath
	releaseURL = srv.URL + "/releases/latest"
	ghDownloadBase = srv.URL + "/releases/download"
	executablePath = func() (string, error) { return currentBin, nil }
	t.Cleanup(func() {
		releaseURL = origURL
		ghDownloadBase = origBase
		executablePath = origExec
	})

	var buf bytes.Buffer
	err := Update(context.Background(), "0.9.0", &buf)
	if err == nil || !strings.Contains(err.Error(), "no release binary found") {
		t.Fatalf("expected 'no release binary found' error, got: %v", err)
	}
}
