package config

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/domain"
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

	// Quiet makes omitted or empty vector selectors resolve to nothing instead
	// of all content. Useful for large catalogs where you want opt-in inclusion.
	Quiet bool `yaml:"quiet,omitempty"`

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
// Packs with config files contribute by default. Set enabled: false to opt out.
type PackSettingsConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type VectorSelector struct {
	Include *[]string `yaml:"include"`
	Exclude *[]string `yaml:"exclude"`
}

type MCPServerConfig struct {
	Enabled            *bool    `yaml:"enabled"`
	AllowedTools       []string `yaml:"allowed_tools,omitempty"`
	AlwaysAllowedTools []string `yaml:"always_allowed_tools,omitempty"`
	DisabledTools      []string `yaml:"disabled_tools,omitempty"`
}

type mcpServerConfigYAML struct {
	Enabled            *bool     `yaml:"enabled"`
	AllowedTools       *[]string `yaml:"allowed_tools,omitempty"`
	AlwaysAllowedTools *[]string `yaml:"always_allowed_tools,omitempty"`
	DisabledTools      []string  `yaml:"disabled_tools,omitempty"`
}

// MarshalYAML omits empty tool-list slices so they fall back to manifest
// defaults on reload. A nil or empty slice both serialize as "field absent";
// only non-empty slices are written. Users who want no tools from a server
// should disable it (enabled: false) rather than relying on an explicit
// empty list.
func (c MCPServerConfig) MarshalYAML() (any, error) {
	out := mcpServerConfigYAML{
		Enabled:       c.Enabled,
		DisabledTools: c.DisabledTools,
	}
	if len(c.AllowedTools) > 0 {
		tools := append([]string{}, c.AllowedTools...)
		out.AllowedTools = &tools
	}
	if len(c.AlwaysAllowedTools) > 0 {
		tools := append([]string{}, c.AlwaysAllowedTools...)
		out.AlwaysAllowedTools = &tools
	}
	return out, nil
}

type Overrides struct {
	Rules     []string `yaml:"rules"`
	Agents    []string `yaml:"agents"`
	Workflows []string `yaml:"workflows"`
	Skills    []string `yaml:"skills"`
	MCP       []string `yaml:"mcp"`
}

// OverridesForCategory returns a pointer to the override slice for the given
// category, allowing callers to read or mutate overrides generically.
// Returns nil for unknown categories.
func (pe *PackEntry) OverridesForCategory(cat domain.PackCategory) *[]string {
	switch cat {
	case domain.CategoryRules:
		return &pe.Overrides.Rules
	case domain.CategoryAgents:
		return &pe.Overrides.Agents
	case domain.CategoryWorkflows:
		return &pe.Overrides.Workflows
	case domain.CategorySkills:
		return &pe.Overrides.Skills
	case domain.CategoryMCP:
		return &pe.Overrides.MCP
	}
	return nil
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
