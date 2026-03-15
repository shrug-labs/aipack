package update

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheck_DevVersion(t *testing.T) {
	r := Check("dev", t.TempDir())
	if r != nil {
		t.Fatal("expected nil for dev version")
	}
}

func TestCheck_EmptyVersion(t *testing.T) {
	r := Check("", t.TempDir())
	if r != nil {
		t.Fatal("expected nil for empty version")
	}
}

func TestCheck_Disabled(t *testing.T) {
	t.Setenv(envDisable, "1")
	r := Check("0.1.0", t.TempDir())
	if r != nil {
		t.Fatal("expected nil when disabled via env")
	}
}

func TestCheck_CachedFresh(t *testing.T) {
	dir := t.TempDir()
	c := cache{CheckedAt: time.Now(), Latest: "0.9.0", UpdateURL: "https://example.com"}
	data, _ := json.Marshal(c)
	os.WriteFile(filepath.Join(dir, cacheFile), data, 0644)

	r := Check("0.8.0", dir)
	if r == nil {
		t.Fatal("expected non-nil result from fresh cache")
	}
	if r.Latest != "0.9.0" || r.Current != "0.8.0" {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestCheck_CachedSameVersion(t *testing.T) {
	dir := t.TempDir()
	c := cache{CheckedAt: time.Now(), Latest: "0.8.0"}
	data, _ := json.Marshal(c)
	os.WriteFile(filepath.Join(dir, cacheFile), data, 0644)

	r := Check("0.8.0", dir)
	if r != nil {
		t.Fatal("expected nil when cached version matches current")
	}
}

// Tests below mutate package-level releaseURL — do NOT add t.Parallel().
func TestCheck_FetchesWhenCacheStale(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ghRelease{TagName: "v1.2.3", HTMLURL: "https://example.com/v1.2.3"})
	}))
	defer srv.Close()

	// Override the release URL for testing.
	origURL := releaseURL
	releaseURL = srv.URL
	t.Cleanup(func() { releaseURL = origURL })

	dir := t.TempDir()
	// Write a stale cache entry.
	c := cache{CheckedAt: time.Now().Add(-48 * time.Hour), Latest: "0.5.0"}
	data, _ := json.Marshal(c)
	os.WriteFile(filepath.Join(dir, cacheFile), data, 0644)

	r := Check("0.8.0", dir)
	if r == nil {
		t.Fatal("expected non-nil result after stale cache refresh")
	}
	if r.Latest != "1.2.3" {
		t.Fatalf("expected latest=1.2.3, got %s", r.Latest)
	}

	// Verify cache was updated.
	updated, err := loadCache(filepath.Join(dir, cacheFile))
	if err != nil {
		t.Fatalf("loading updated cache: %v", err)
	}
	if updated.Latest != "1.2.3" {
		t.Fatalf("cache not updated: %+v", updated)
	}
}

func TestCheck_NetworkFailureSilent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	origURL := releaseURL
	releaseURL = srv.URL
	t.Cleanup(func() { releaseURL = origURL })

	r := Check("0.8.0", t.TempDir())
	if r != nil {
		t.Fatal("expected nil on network failure")
	}
}

func TestResult_Newer(t *testing.T) {
	tests := []struct {
		name string
		r    *Result
		want bool
	}{
		{"nil", nil, false},
		{"same", &Result{Latest: "1.0", Current: "1.0"}, false},
		{"empty latest", &Result{Latest: "", Current: "1.0"}, false},
		{"newer semver", &Result{Latest: "2.0.0", Current: "1.0.0"}, true},
		{"newer minor", &Result{Latest: "1.1.0", Current: "1.0.0"}, true},
		{"older", &Result{Latest: "0.9.0", Current: "1.0.0"}, false},
		{"unparseable latest", &Result{Latest: "2.0", Current: "1.0.0"}, false},
		{"unparseable both", &Result{Latest: "2.0", Current: "1.0"}, false},
		{"v prefix mismatch", &Result{Latest: "0.8.0", Current: "v0.8.0"}, false},
		{"both v prefix", &Result{Latest: "v1.0", Current: "v1.0"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.Newer(); got != tt.want {
				t.Fatalf("Newer()=%v, want %v", got, tt.want)
			}
		})
	}
}
