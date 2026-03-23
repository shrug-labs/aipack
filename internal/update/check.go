package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/util"
)

const (
	// cacheTTL controls how often we check for updates.
	cacheTTL = 6 * time.Hour

	// cacheFile is stored under the aipack config dir.
	cacheFile = "update-check.json"

	// envDisable disables the update check when set to any non-empty value.
	envDisable = "AIPACK_NO_UPDATE_CHECK"

	// requestTimeout caps the HTTP round-trip.
	requestTimeout = 5 * time.Second
)

// distribution controls where update checks look. Override at build time:
//
//	-X github.com/shrug-labs/aipack/internal/update.distribution=internal
var distribution = "github"

// releaseURL is the GitHub releases page used to resolve the latest tag via
// HTTP redirect (no API rate limit). Var so tests can override it.
var releaseURL = "https://github.com/shrug-labs/aipack/releases/latest"

// internalVersionURL points to a plain text file containing the latest version.
// Set at build time or overridden in tests.
var internalVersionURL = ""

// Result describes the outcome of an update check.
type Result struct {
	Latest    string `json:"latest"`
	Current   string `json:"current"`
	UpdateURL string `json:"update_url,omitempty"`
}

// Newer returns true if the latest version is strictly greater than the current
// version. Normalizes the "v" prefix so "v0.8.0" and "0.8.0" compare as equal.
// Falls back to string inequality if either version is not a valid MAJOR.MINOR.PATCH.
func (r *Result) Newer() bool {
	if r == nil || r.Latest == "" {
		return false
	}
	latest := strings.TrimPrefix(r.Latest, "v")
	current := strings.TrimPrefix(r.Current, "v")
	if latest == current {
		return false
	}
	lp, lok := parseVersion(latest)
	cp, cok := parseVersion(current)
	if !lok || !cok {
		// Unparseable — can't compare reliably. Suppress the notice
		// rather than risk false positives from non-semver tags.
		return false
	}
	for i := 0; i < 3; i++ {
		if lp[i] != cp[i] {
			return lp[i] > cp[i]
		}
	}
	return false
}

// parseVersion splits "1.2.3" into [1,2,3]. Returns false if malformed.
func parseVersion(s string) ([3]int, bool) {
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var v [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

// Notice returns a human-readable update notice suitable for printing to stderr.
// Returns empty string if r is nil.
func (r *Result) Notice() string {
	if r == nil {
		return ""
	}
	s := fmt.Sprintf("\nA new version of aipack is available: %s (current: %s)\n", r.Latest, r.Current)
	s += "Update: aipack update\n"
	return s
}

// Distribution returns the current distribution channel.
func Distribution() string {
	return distribution
}

// cache is the on-disk shape of the cached check result.
type cache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
	UpdateURL string    `json:"update_url,omitempty"`
}

// Check queries GitHub for the latest release and compares it to currentVersion.
// It caches the result under configDir to avoid repeated API calls. Returns nil
// when no update is available, the check is disabled, or any error occurs.
func Check(ctx context.Context, currentVersion, configDir string) *Result {
	if currentVersion == "dev" || currentVersion == "" {
		return nil
	}
	if os.Getenv(envDisable) != "" {
		return nil
	}

	cachePath := filepath.Join(configDir, cacheFile)

	// Try the cache first.
	if c, err := loadCache(cachePath); err == nil {
		if time.Since(c.CheckedAt) < cacheTTL {
			r := &Result{Latest: c.Latest, Current: strings.TrimPrefix(currentVersion, "v"), UpdateURL: c.UpdateURL}
			if r.Newer() {
				return r
			}
			return nil
		}
	}

	// Cache is stale or missing — fetch from GitHub.
	latest, url, err := fetchLatest(ctx)
	if err != nil {
		return nil // fail silently
	}

	// Persist the result.
	_ = saveCache(cachePath, cache{
		CheckedAt: time.Now(),
		Latest:    latest,
		UpdateURL: url,
	})

	r := &Result{Latest: latest, Current: strings.TrimPrefix(currentVersion, "v"), UpdateURL: url}
	if r.Newer() {
		return r
	}
	return nil
}

func fetchLatest(ctx context.Context) (version, url string, err error) {
	if distribution == "internal" {
		return fetchLatestInternal(ctx)
	}
	return fetchLatestGitHub(ctx)
}

func fetchLatestGitHub(ctx context.Context) (version, url string, err error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, releaseURL, nil)
	if err != nil {
		return "", "", err
	}
	// Don't follow redirects — we parse the tag from the Location header.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	resp.Body.Close()

	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", "", fmt.Errorf("no redirect from %s (status %d)", releaseURL, resp.StatusCode)
	}
	// Location is https://github.com/<owner>/<repo>/releases/tag/<tag>
	idx := strings.LastIndex(loc, "/")
	if idx < 0 {
		return "", "", fmt.Errorf("unexpected redirect URL: %s", loc)
	}
	tag := strings.TrimPrefix(loc[idx+1:], "v")
	if tag == "" {
		return "", "", fmt.Errorf("empty tag in redirect URL: %s", loc)
	}
	return tag, loc, nil
}

// fetchLatestInternal fetches the latest version from a plain text file
// at internalVersionURL. The file should contain just the version string.
func fetchLatestInternal(ctx context.Context) (version, url string, err error) {
	if internalVersionURL == "" {
		return "", "", fmt.Errorf("internal version URL not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, internalVersionURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("version check returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return "", "", err
	}
	tag := strings.TrimSpace(strings.TrimPrefix(string(body), "v"))
	if tag == "" {
		return "", "", fmt.Errorf("empty version response")
	}
	return tag, "", nil
}

func loadCache(path string) (cache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cache{}, err
	}
	var c cache
	if err := json.Unmarshal(data, &c); err != nil {
		return cache{}, err
	}
	return c, nil
}

func saveCache(path string, c cache) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return util.WriteFileAtomic(path, data)
}

// CheckAsync starts a background update check and returns a channel that
// will receive exactly one *Result (possibly nil). Callers should read the
// channel after their main work completes to avoid blocking on network I/O.
func CheckAsync(ctx context.Context, currentVersion, home string) <-chan *Result {
	ch := make(chan *Result, 1)
	go func() {
		configDir, err := config.DefaultConfigDir(home)
		if err != nil {
			ch <- nil
			return
		}
		ch <- Check(ctx, currentVersion, configDir)
	}()
	return ch
}
