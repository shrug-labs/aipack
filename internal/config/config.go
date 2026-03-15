package config

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const ProfileSchemaVersion = 2

type ProfileConfig struct {
	SchemaVersion int               `yaml:"schema_version"`
	Params        map[string]string `yaml:"params"`
	Globals       map[string]string `yaml:"globals"` // Deprecated: use params. Accepted for backwards compat.
	Global        map[string]string `yaml:"global"`  // Deprecated: use params. Accepted for backwards compat.
	Packs         []PackEntry       `yaml:"packs"`
}

type PackEntry struct {
	Name    string `yaml:"name"`
	Enabled *bool  `yaml:"enabled"`

	// Settings controls whether this pack's harness settings files are synced.
	Settings PackSettingsConfig `yaml:"settings"`

	Rules     VectorSelector             `yaml:"rules"`
	Agents    VectorSelector             `yaml:"agents"`
	Workflows VectorSelector             `yaml:"workflows"`
	Skills    VectorSelector             `yaml:"skills"`
	MCP       map[string]MCPServerConfig `yaml:"mcp"`

	Overrides Overrides `yaml:"overrides"`
}

// PackSettingsConfig controls per-pack harness settings sync.
// nil Enabled = false (opt-in).
type PackSettingsConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type VectorSelector struct {
	Include *[]string `yaml:"include"`
	Exclude *[]string `yaml:"exclude"`
}

type MCPServerConfig struct {
	Enabled       *bool    `yaml:"enabled"`
	AllowedTools  []string `yaml:"allowed_tools"`
	DisabledTools []string `yaml:"disabled_tools"`
}

type Overrides struct {
	Rules     []string `yaml:"rules"`
	Agents    []string `yaml:"agents"`
	Workflows []string `yaml:"workflows"`
	Skills    []string `yaml:"skills"`
	MCP       []string `yaml:"mcp"`
}

func LoadProfile(path string) (ProfileConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ProfileConfig{}, err
	}
	var cfg ProfileConfig
	dec := yaml.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&cfg); err != nil {
		return ProfileConfig{}, err
	}
	cfg.mergeDeprecatedParams()
	return cfg, nil
}

// mergeDeprecatedParams folds deprecated globals/global into Params and clears the legacy fields.
func (c *ProfileConfig) mergeDeprecatedParams() {
	if len(c.Params) == 0 {
		if len(c.Globals) > 0 {
			c.Params = c.Globals
		} else if len(c.Global) > 0 {
			c.Params = c.Global
		}
	}
	c.Globals = nil
	c.Global = nil
}

// ValidateProfileConfig checks a parsed profile for structural issues.
func ValidateProfileConfig(cfg ProfileConfig) []string {
	var errs []string
	if cfg.SchemaVersion != ProfileSchemaVersion {
		errs = append(errs, fmt.Sprintf("unsupported schema_version %d (expected %d)", cfg.SchemaVersion, ProfileSchemaVersion))
	}
	seen := map[string]struct{}{}
	for i, p := range cfg.Packs {
		if p.Name == "" {
			errs = append(errs, fmt.Sprintf("packs[%d]: empty name", i))
			continue
		}
		if _, ok := seen[p.Name]; ok {
			errs = append(errs, fmt.Sprintf("packs[%d]: duplicate name %q", i, p.Name))
		}
		seen[p.Name] = struct{}{}
	}
	return errs
}
