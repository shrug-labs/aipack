package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/util"

	"gopkg.in/yaml.v3"
)

const SyncConfigSchemaVersion = 1

// InstalledPackMeta records the origin and install method for a pack.
type InstalledPackMeta struct {
	Origin           string `yaml:"origin"`                      // abs path or URL
	Method           string `yaml:"method"`                      // "link", "copy", "clone"
	InstalledAt      string `yaml:"installed_at"`                // RFC3339
	Ref        string `yaml:"ref,omitempty"`         // git ref (URL only)
	SubPath    string `yaml:"sub_path,omitempty"`    // subdirectory within cloned repo
	CommitHash string `yaml:"commit_hash,omitempty"` // git HEAD SHA at install/update time
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
	InstalledPacks map[string]InstalledPackMeta `yaml:"installed_packs,omitempty"`
}

// DefaultConfigDir returns the default config directory (~/.config/aipack).
// Callers must pass the HOME value explicitly to avoid deep os.Getenv calls.
func DefaultConfigDir(home string) (string, error) {
	if home == "" {
		return "", os.ErrNotExist
	}
	return filepath.Join(home, ".config", "aipack"), nil
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
