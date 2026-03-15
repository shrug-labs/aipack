package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

type PackManifest struct {
	SchemaVersion int      `json:"schema_version"`
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Root          string   `json:"root"`
	Rules         []string `json:"rules,omitempty"`
	Agents        []string `json:"agents,omitempty"`
	Workflows     []string `json:"workflows,omitempty"`
	Skills        []string `json:"skills,omitempty"`
	Prompts       []string `json:"prompts,omitempty"`
	MCP           MCPPack  `json:"mcp,omitzero"`

	// Profiles lists relative paths to seed profile YAML files within the pack.
	// When a pack is installed, these profiles are copied to the config directory
	// if they don't already exist there.
	Profiles []string `json:"profiles,omitempty"`

	// Registries lists relative paths to registry YAML files within the pack.
	// On sync, entries from these registries are merged into the user's local
	// registry, enabling packs to surface related packs for discovery.
	Registries []string `json:"registries,omitempty"`

	// Configs inventories harness settings files shipped with the pack.
	// This is used for validation and deterministic settings pack selection.
	Configs PackConfigs `json:"configs,omitzero"`
}

type PackConfigs struct {
	// HarnessSettings lists base harness config files per harness.
	// These are templates that get MCP/tools/instructions merged via RenderBytes.
	// Example:
	//   { "opencode": ["opencode.json"], "codex": ["config.toml"] }
	HarnessSettings map[string][]string `json:"harness_settings"`

	// HarnessPlugins lists harness plugin config files per harness.
	// These are pure copies (no transformation). Not gated by --skip-settings.
	// Example:
	//   { "opencode": ["oh-my-opencode.json"] }
	HarnessPlugins map[string][]string `json:"harness_plugins,omitempty"`
}

// HasAnyConfigs reports whether this pack provides any harness config files
// (settings or plugins).
func (c PackConfigs) HasAnyConfigs() bool {
	return len(c.HarnessSettings) > 0 || len(c.HarnessPlugins) > 0
}

type MCPPack struct {
	Servers map[string]MCPDefaults `json:"servers"`
}

type MCPDefaults struct {
	DefaultAllowedTools []string `json:"default_allowed_tools"`
}

// ContentIDsPtr returns a pointer to the content ID slice for the given
// authored category, or nil for MCP and unknown categories.
// Use this when you need to mutate the manifest's content list in place.
func (m *PackManifest) ContentIDsPtr(cat domain.PackCategory) *[]string {
	switch cat {
	case domain.CategoryRules:
		return &m.Rules
	case domain.CategoryAgents:
		return &m.Agents
	case domain.CategoryWorkflows:
		return &m.Workflows
	case domain.CategorySkills:
		return &m.Skills
	}
	return nil
}

// ContentIDs returns the resource IDs for the given category.
// For MCP, returns sorted server names.
func (m PackManifest) ContentIDs(cat domain.PackCategory) []string {
	if p := m.ContentIDsPtr(cat); p != nil {
		return *p
	}
	if cat == domain.CategoryMCP {
		names := make([]string, 0, len(m.MCP.Servers))
		for n := range m.MCP.Servers {
			names = append(names, n)
		}
		sort.Strings(names)
		return names
	}
	return nil
}

// VectorSelectorFor returns a pointer to the VectorSelector for the given
// authored category. Returns nil for MCP or unknown categories.
func (pe *PackEntry) VectorSelectorFor(cat domain.PackCategory) *VectorSelector {
	switch cat {
	case domain.CategoryRules:
		return &pe.Rules
	case domain.CategoryAgents:
		return &pe.Agents
	case domain.CategoryWorkflows:
		return &pe.Workflows
	case domain.CategorySkills:
		return &pe.Skills
	}
	return nil
}

func LoadPackManifest(path string) (PackManifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return PackManifest{}, err
	}
	return ParsePackManifest(b)
}

// ParsePackManifest unmarshals and validates a pack manifest from raw JSON bytes.
func ParsePackManifest(data []byte) (PackManifest, error) {
	var m PackManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return PackManifest{}, err
	}
	if m.SchemaVersion <= 0 {
		return PackManifest{}, fmt.Errorf("pack manifest schema_version must be set")
	}
	if m.Name == "" {
		return PackManifest{}, fmt.Errorf("pack manifest name must be set")
	}
	if m.Root == "" {
		return PackManifest{}, fmt.Errorf("pack manifest root must be set")
	}
	return m, nil
}

// ContentPaths returns all file and directory paths declared by the manifest,
// relative to the pack root. Skills use trailing "/" to indicate directories.
// Always includes "pack.json" itself.
func (m PackManifest) ContentPaths() []string {
	paths := []string{"pack.json"}

	for _, id := range m.Rules {
		paths = append(paths, domain.CategoryRules.PrimaryRelPath(id))
	}
	for _, id := range m.Agents {
		paths = append(paths, domain.CategoryAgents.PrimaryRelPath(id))
	}
	for _, id := range m.Workflows {
		paths = append(paths, domain.CategoryWorkflows.PrimaryRelPath(id))
	}
	for _, id := range m.Skills {
		// Trailing "/" so git archive fetches the entire directory recursively.
		paths = append(paths, filepath.ToSlash(filepath.Join(domain.CategorySkills.DirName(), id))+"/")
	}
	for _, id := range m.Prompts {
		paths = append(paths, filepath.ToSlash(filepath.Join("prompts", id+".md")))
	}
	for name := range m.MCP.Servers {
		paths = append(paths, filepath.ToSlash(filepath.Join("mcp", name+".json")))
	}
	for _, p := range m.Profiles {
		paths = append(paths, p)
	}
	for _, p := range m.Registries {
		paths = append(paths, p)
	}
	for harness, files := range m.Configs.HarnessSettings {
		for _, f := range files {
			paths = append(paths, filepath.ToSlash(filepath.Join("configs", harness, f)))
		}
	}
	for harness, files := range m.Configs.HarnessPlugins {
		for _, f := range files {
			paths = append(paths, filepath.ToSlash(filepath.Join("configs", harness, f)))
		}
	}

	return paths
}

// SavePackManifest writes a pack manifest to disk as formatted JSON.
func SavePackManifest(path string, m PackManifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return util.WriteFileAtomic(path, b)
}
