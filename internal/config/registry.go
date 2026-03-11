package config

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const RegistrySchemaVersion = 1

// Default registry source coordinates. Used when no URL is configured in
// sync-config or passed as a flag. Git-based fetch piggybacks on the user's
// existing git credentials (SSH keys, credential helpers, etc.).
const (
	DefaultRegistryRepo = "https://github.com/shrug-labs/aipack.git"
	DefaultRegistryRef  = "main"
	DefaultRegistryPath = "registry.yaml"
)

// Registry is a catalog of packs available for installation.
type Registry struct {
	SchemaVersion int                      `yaml:"schema_version"`
	Packs         map[string]RegistryEntry `yaml:"packs"`
}

// RegistryEntry describes a pack available in the registry.
type RegistryEntry struct {
	Repo        string `yaml:"repo" json:"repo"`
	Path        string `yaml:"path,omitempty" json:"path,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Ref         string `yaml:"ref,omitempty" json:"ref,omitempty"`
	Owner       string `yaml:"owner,omitempty" json:"owner,omitempty"`
	Contact     string `yaml:"contact,omitempty" json:"contact,omitempty"`
}

// LoadRegistry loads a registry from a local YAML file.
func LoadRegistry(path string) (Registry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, fmt.Errorf("reading registry: %w", err)
	}
	return ParseRegistry(b)
}

// ParseRegistry parses raw YAML bytes into a Registry, validating schema_version.
func ParseRegistry(data []byte) (Registry, error) {
	var reg Registry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return Registry{}, fmt.Errorf("parsing registry: %w", err)
	}
	if reg.SchemaVersion != RegistrySchemaVersion {
		return Registry{}, fmt.Errorf("unsupported registry schema_version %d (expected %d)", reg.SchemaVersion, RegistrySchemaVersion)
	}
	if reg.Packs == nil {
		reg.Packs = make(map[string]RegistryEntry)
	}
	return reg, nil
}

// FetchRegistryFromURL fetches raw YAML bytes from a remote URL.
func FetchRegistryFromURL(rawURL string) ([]byte, error) {
	resp, err := http.Get(rawURL) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("fetching registry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching registry: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// FetchFileViaGit clones a repo (shallow, single-branch) into a temp directory,
// reads the file at filePath, and returns its contents. The temp directory
// is cleaned up before returning. Uses --branch for the ref to avoid the
// fetch+checkout two-step that fails with shallow clones on some servers.
func FetchFileViaGit(repoURL, ref, filePath string) ([]byte, error) {
	return fetchFileViaGit(repoURL, ref, filePath, RunGit)
}

// FetchFileViaGitWith is like FetchFileViaGit but accepts a custom git runner for testing.
func FetchFileViaGitWith(repoURL, ref, filePath string, runGitFn func(args ...string) error) ([]byte, error) {
	return fetchFileViaGit(repoURL, ref, filePath, runGitFn)
}

func fetchFileViaGit(repoURL, ref, filePath string, runGitFn func(args ...string) error) ([]byte, error) {
	if err := CheckGit(); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "aipack-fetch-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, repoURL, tmp)
	if err := runGitFn(args...); err != nil {
		return nil, fmt.Errorf("cloning %s: %w", repoURL, err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, filePath))
	if err != nil {
		return nil, fmt.Errorf("reading %s from clone: %w", filePath, err)
	}
	return data, nil
}

// ResolveRegistryPath returns the registry file path to use.
// Priority: explicit flag > sync-config defaults.registry > <configDir>/registry.yaml.
func ResolveRegistryPath(flagVal, scDefault, configDir string) string {
	if flagVal != "" {
		return flagVal
	}
	if scDefault != "" {
		return scDefault
	}
	return filepath.Join(configDir, "registry.yaml")
}

// RegistriesCacheDir returns the directory for cached remote registries.
func RegistriesCacheDir(configDir string) string {
	return filepath.Join(configDir, "registries")
}

// SourceCachePath returns the cache file path for a named registry source.
func SourceCachePath(configDir, name string) string {
	return filepath.Join(RegistriesCacheDir(configDir), name+".yaml")
}

// LoadMergedRegistry loads the local registry and all cached source registries,
// producing a unified in-memory view. Local entries have highest priority,
// followed by sources in registry_sources list order. First-seen wins for
// pack name conflicts.
func LoadMergedRegistry(configDir string) (Registry, error) {
	merged := Registry{
		SchemaVersion: RegistrySchemaVersion,
		Packs:         make(map[string]RegistryEntry),
	}

	// 1. Local registry (highest priority).
	localPath := filepath.Join(configDir, "registry.yaml")
	if local, err := LoadRegistry(localPath); err == nil {
		for name, entry := range local.Packs {
			merged.Packs[name] = entry
		}
	}

	// 2. Cached source registries in list order.
	sc, _ := LoadSyncConfig(SyncConfigPath(configDir))
	for _, src := range sc.RegistrySources {
		cachePath := SourceCachePath(configDir, src.Name)
		cached, err := LoadRegistry(cachePath)
		if err != nil {
			continue
		}
		for name, entry := range cached.Packs {
			if _, exists := merged.Packs[name]; !exists {
				merged.Packs[name] = entry
			}
		}
	}

	return merged, nil
}

// DeriveSourceName extracts a short source name from a URL.
// Strips trailing .git/.yaml/.yml suffixes. If the result is "registry"
// or a hostname, walks up the path to find a meaningful component.
func DeriveSourceName(rawURL string) string {
	u := strings.TrimSuffix(rawURL, "/")

	// Handle SCP-style SSH URLs: git@host:org/repo.git → org/repo.git
	if strings.HasPrefix(u, "git@") {
		if idx := strings.Index(u, ":"); idx >= 0 {
			u = u[idx+1:]
		}
	} else if idx := strings.Index(u, "://"); idx >= 0 {
		// Strip scheme (https://, ssh://, etc.)
		u = u[idx+3:]
	}

	parts := strings.Split(u, "/")
	// Walk from the end to find a meaningful path component.
	for i := len(parts) - 1; i >= 0; i-- {
		name := parts[i]
		name = strings.TrimSuffix(name, ".git")
		name = strings.TrimSuffix(name, ".yaml")
		name = strings.TrimSuffix(name, ".yml")
		if name == "" || name == "registry" {
			continue
		}
		// If this is the hostname (first component), extract a meaningful
		// domain label. Skip labels that would produce "registry" (the
		// string we're trying to avoid) — e.g. "registry.example.com".
		if i == 0 && strings.Contains(name, ".") {
			hostParts := strings.Split(name, ".")
			for _, label := range hostParts {
				if label != "" && label != "registry" {
					return label
				}
			}
		}
		return name
	}
	return "registry"
}

// UniqueSourceName returns a source name that doesn't collide with existing
// source names (unless the URL matches). If the derived name collides with
// a source that has a different URL, a numeric suffix is appended.
func UniqueSourceName(derived, url string, existing []RegistrySourceEntry) string {
	for _, src := range existing {
		if src.URL == url {
			return src.Name
		}
	}
	taken := make(map[string]bool)
	for _, src := range existing {
		taken[src.Name] = true
	}
	if !taken[derived] {
		return derived
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", derived, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

// IsGitURL returns true if the URL should use git-based fetch.
// A URL is considered a git URL if it ends with ".git", uses an SSH scheme
// or SCP-style syntax (git@host:path), or if a ref is provided.
func IsGitURL(rawURL, ref string) bool {
	if ref != "" {
		return true
	}
	if strings.HasSuffix(rawURL, ".git") {
		return true
	}
	// SCP-style SSH: git@host:org/repo
	if strings.HasPrefix(rawURL, "git@") && strings.Contains(rawURL, ":") {
		return true
	}
	// Explicit SSH scheme.
	if strings.HasPrefix(rawURL, "ssh://") {
		return true
	}
	return false
}
