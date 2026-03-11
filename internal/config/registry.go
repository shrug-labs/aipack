package config

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

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
