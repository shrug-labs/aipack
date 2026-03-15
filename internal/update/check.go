package update

import (
	"encoding/json"
	"fmt"
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
	// cacheTTL controls how often we hit the GitHub API.
	cacheTTL = 6 * time.Hour

	// cacheFile is stored under the aipack config dir.
	cacheFile = "update-check.json"

	// envDisable disables the update check when set to any non-empty value.
	envDisable = "AIPACK_NO_UPDATE_CHECK"

	// requestTimeout caps the HTTP round-trip.
	requestTimeout = 5 * time.Second
)

// releaseURL is the GitHub API endpoint for the latest release.
// Var (not const) so tests can override it.
var releaseURL = "https://api.github.com/repos/shrug-labs/aipack/releases/latest"

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
	if r.UpdateURL != "" {
		s += fmt.Sprintf("Update: brew upgrade aipack  or  %s\n", r.UpdateURL)
	}
	return s
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
func Check(currentVersion, configDir string) *Result {
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
	latest, url, err := fetchLatest()
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

// ghRelease is the subset of the GitHub releases response we care about.
type ghRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

func fetchLatest() (version, url string, err error) {
	client := &http.Client{Timeout: requestTimeout}
	resp, err := client.Get(releaseURL)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", "", err
	}
	tag := strings.TrimPrefix(rel.TagName, "v")
	return tag, rel.HTMLURL, nil
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
func CheckAsync(currentVersion, home string) <-chan *Result {
	ch := make(chan *Result, 1)
	go func() {
		configDir, err := config.DefaultConfigDir(home)
		if err != nil {
			ch <- nil
			return
		}
		ch <- Check(currentVersion, configDir)
	}()
	return ch
}
