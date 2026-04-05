package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"

	"gopkg.in/yaml.v3"
)

const SyncConfigSchemaVersion = 1

// RegistrySourceEntry describes a remote registry source for fetching.
// Ref presence implies git-based fetch; otherwise HTTP GET.
type RegistrySourceEntry struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	Ref  string `yaml:"ref,omitempty"`  // git ref (branch/tag); presence implies git-based fetch
	Path string `yaml:"path,omitempty"` // file path within repo (git only); default: registry.yaml
}

// Install method constants for InstalledPackMeta.Method.
const (
	MethodLink        = "link"
	MethodCopy        = "copy"
	MethodClone       = "clone"
	MethodArchive     = "archive"
	MethodHTTPTarball = "http-tarball"
	MethodLocal       = "local" // pack already resides in the packs directory; registered in-place
)

// InstalledPackMeta records the origin and install method for a pack.
type InstalledPackMeta struct {
	Origin       string                         `yaml:"origin"`                  // abs path or URL
	Method       string                         `yaml:"method"`                  // MethodLink, MethodCopy, MethodClone, MethodArchive, MethodLocal
	InstalledAt  string                         `yaml:"installed_at"`            // RFC3339
	Ref          string                         `yaml:"ref,omitempty"`           // git ref (URL only)
	SubPath      string                         `yaml:"sub_path,omitempty"`      // subdirectory within cloned repo
	CommitHash   string                         `yaml:"commit_hash,omitempty"`   // git HEAD SHA at install/update time
	ContentPaths map[domain.PackCategory]string `yaml:"content_paths,omitempty"` // content type -> directory path within clone (nil = standard layout)
	Approved     []domain.BundledCategory       `yaml:"approved,omitempty"`      // bundled categories the user accepted
	Declined     []domain.BundledCategory       `yaml:"declined,omitempty"`      // bundled categories the user declined
}

// SyncConfig is user-level configuration (one level above profiles).
// It stores defaults that apply across profiles/packs/syncs.
type SyncConfig struct {
	SchemaVersion int `yaml:"schema_version"`
	Defaults      struct {
		Profile     string   `yaml:"profile"`
		Harnesses   []string `yaml:"harnesses"`
		Scope       string   `yaml:"scope"`
		Registry    string   `yaml:"registry,omitempty"`
		RegistryURL string   `yaml:"registry_url,omitempty"`
	} `yaml:"defaults"`
	InstalledPacks  map[string]InstalledPackMeta `yaml:"installed_packs,omitempty"`
	RegistrySources []RegistrySourceEntry        `yaml:"registry_sources,omitempty"`
}

// HomeDir returns the current user's home directory.
// It uses os.UserHomeDir which works cross-platform (HOME on Unix, USERPROFILE on Windows).
func HomeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// DefaultConfigDir returns the default config directory.
// On Windows it uses <home>\AppData\Roaming\aipack; on Unix it uses ~/.config/aipack.
// Callers must pass the home value explicitly to keep paths deterministic
// (important for test isolation and non-default home directories).
func DefaultConfigDir(home string) (string, error) {
	if home == "" {
		return "", os.ErrNotExist
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Roaming", "aipack"), nil
	}
	return filepath.Join(home, ".config", "aipack"), nil
}

// FallbackConfigDir returns configDir if non-empty, or the default config
// directory derived from home. Returns empty string only when both inputs
// are empty (caller should treat this as an error). Use
// cmdutil.ResolveConfigDir at CLI boundaries where the error matters.
func FallbackConfigDir(configDir, home string) string {
	if configDir != "" {
		return configDir
	}
	d, _ := DefaultConfigDir(home)
	return d
}

func SyncConfigPath(configDir string) string {
	return filepath.Join(configDir, "sync-config.yaml")
}

func LoadSyncConfig(path string) (SyncConfig, error) {
	cfg := SyncConfig{SchemaVersion: SyncConfigSchemaVersion}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return SyncConfig{}, err
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return SyncConfig{}, err
	}
	if cfg.SchemaVersion != SyncConfigSchemaVersion {
		return SyncConfig{}, fmt.Errorf("unsupported sync-config schema_version %d (expected %d)", cfg.SchemaVersion, SyncConfigSchemaVersion)
	}
	// Normalize defaults.
	cfg.Defaults.Profile = strings.TrimSpace(cfg.Defaults.Profile)
	for i := range cfg.Defaults.Harnesses {
		cfg.Defaults.Harnesses[i] = strings.TrimSpace(cfg.Defaults.Harnesses[i])
	}
	cfg.Defaults.Scope = strings.TrimSpace(cfg.Defaults.Scope)
	return cfg, nil
}

// SaveSyncConfig marshals cfg to YAML and writes it atomically to path.
func SaveSyncConfig(path string, cfg SyncConfig) error {
	out, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshalling sync-config: %w", err)
	}
	return util.WriteFileAtomicWithPerms(path, out, 0o700, 0o600)
}
