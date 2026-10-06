package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

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
	Hooks     HookSelector               `yaml:"hooks"`
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

// HookSelector is a content selector for executable hook descriptors. It
// behaves like other vector selectors, with an explicit enabled switch for
// users who want to opt out without changing include/exclude lists.
type HookSelector struct {
	VectorSelector `yaml:",inline"`
	Enabled        *bool `yaml:"enabled"`
}

type MCPServerConfig struct {
	Enabled            *bool    `yaml:"enabled"`
	AllowedTools       []string `yaml:"allowed_tools,omitempty"`
	AlwaysAllowedTools []string `yaml:"always_allowed_tools,omitempty"`
	DisabledTools      []string `yaml:"disabled_tools,omitempty"`
	StartupTimeout     string   `yaml:"startup_timeout,omitempty"`
}

// IsZero reports whether the entry has no profile overrides.
func (c MCPServerConfig) IsZero() bool {
	return c.Enabled == nil && c.StartupTimeout == "" && !c.HasToolPolicy()
}

// HasToolPolicy reports whether the profile entry customizes tool visibility.
func (c MCPServerConfig) HasToolPolicy() bool {
	return len(c.AllowedTools) > 0 || len(c.AlwaysAllowedTools) > 0 || len(c.DisabledTools) > 0
}

// IsDisableOnly reports whether the entry only disables the server.
func (c MCPServerConfig) IsDisableOnly() bool {
	return !PackEnabled(c.Enabled) && !c.HasToolPolicy()
}

type mcpServerConfigYAML struct {
	Enabled            *bool     `yaml:"enabled"`
	AllowedTools       *[]string `yaml:"allowed_tools,omitempty"`
	AlwaysAllowedTools *[]string `yaml:"always_allowed_tools,omitempty"`
	DisabledTools      []string  `yaml:"disabled_tools,omitempty"`
	StartupTimeout     string    `yaml:"startup_timeout,omitempty"`
}

// MarshalYAML omits empty tool-list slices so they fall back to manifest
// defaults on reload. A nil or empty slice both serialize as "field absent";
// only non-empty slices are written. Users who want no tools from a server
// should disable it (enabled: false) rather than relying on an explicit
// empty list.
func (c MCPServerConfig) MarshalYAML() (any, error) {
	out := mcpServerConfigYAML{
		Enabled:        c.Enabled,
		DisabledTools:  c.DisabledTools,
		StartupTimeout: c.StartupTimeout,
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
	Hooks     []string `yaml:"hooks"`
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
	case domain.CategoryHooks:
		return &pe.Overrides.Hooks
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
	// Empty legacy selectors were generated in existing profiles. Accept those
	// inert fields, but never silently discard a user's nonempty plugin intent.
	var retired struct {
		Packs []struct {
			Name      string         `yaml:"name"`
			Plugins   VectorSelector `yaml:"plugins"`
			Overrides struct {
				Plugins []string `yaml:"plugins"`
			} `yaml:"overrides"`
		} `yaml:"packs"`
	}
	if err := yaml.Unmarshal(b, &retired); err != nil {
		return ProfileConfig{}, err
	}
	for _, pack := range retired.Packs {
		if (pack.Plugins.Include != nil && len(*pack.Plugins.Include) > 0) || (pack.Plugins.Exclude != nil && len(*pack.Plugins.Exclude) > 0) || len(pack.Overrides.Plugins) > 0 {
			return ProfileConfig{}, fmt.Errorf("pack %q: plugins selectors are no longer supported; import marketplace plugins as packs and select their components", pack.Name)
		}
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
		if strings.Contains(p.Name, domain.RenderedIdentitySeparator) {
			errs = append(errs, fmt.Sprintf("packs[%d]: name %q must not contain %q", i, p.Name, domain.RenderedIdentitySeparator))
		}
		if _, ok := seen[p.Name]; ok {
			errs = append(errs, fmt.Sprintf("packs[%d]: duplicate name %q", i, p.Name))
		}
		seen[p.Name] = struct{}{}
	}
	return errs
}
