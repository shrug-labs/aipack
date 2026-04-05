package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

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

	// Profiles lists profile IDs discovered from the profiles/ directory.
	// Each ID corresponds to profiles/<id>.yaml. When a pack is installed
	// with -w profiles, these are installed to the config profiles directory.
	Profiles []string `json:"profiles,omitempty"`

	// Registries lists registry IDs discovered from the registries/ directory.
	// Each ID corresponds to registries/<id>.yaml. On install with
	// -w registries, entries are merged into the user's local registry.
	Registries []string `json:"registries,omitempty"`

	// Extras lists relative paths for bundled assets (scripts, data files,
	// helper source) that must be preserved through install. These stay in the
	// pack directory and are referenced via {pack:root} in MCP command arrays
	// and env values.
	Extras []string `json:"extras,omitempty"`

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
		names := slices.Sorted(maps.Keys(m.MCP.Servers))
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
	// Normalize legacy path-style entries to bare IDs.
	// Old manifests may contain "profiles/team.yaml" or "registry.yaml";
	// the current schema expects bare IDs like "team" or "registry".
	m.Profiles = normalizeIDs(m.Profiles, ".yaml")
	m.Registries = normalizeIDs(m.Registries, ".yaml")
	return m, nil
}

// normalizeIDs converts path-style entries to bare IDs by stripping any
// directory prefix and the given file extension. Entries that are already
// bare IDs pass through unchanged.
func normalizeIDs(entries []string, ext string) []string {
	if len(entries) == 0 {
		return entries
	}
	for i, e := range entries {
		e = filepath.Base(e)
		entries[i] = strings.TrimSuffix(e, ext)
	}
	return entries
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
	for _, id := range m.Profiles {
		paths = append(paths, filepath.ToSlash(filepath.Join("profiles", id+".yaml")))
	}
	for _, id := range m.Registries {
		paths = append(paths, filepath.ToSlash(filepath.Join("registries", id+".yaml")))
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
	for _, ext := range m.Extras {
		paths = append(paths, filepath.ToSlash(ext))
	}

	return paths
}

// ExtrasReservedDirs is the set of top-level directory names that extras
// entries must not collide with. Shared between validation and extraction.
var ExtrasReservedDirs = map[string]struct{}{
	"rules": {}, "agents": {}, "workflows": {}, "skills": {},
	"mcp": {}, "configs": {}, "prompts": {}, "profiles": {},
	"registries": {},
}

// ExtrasStagingName returns the staging-relative path for an extras entry.
// Leading ".." segments are stripped so that repo-relative extras (e.g.
// "../shared-scripts") land inside the staging directory as "shared-scripts".
// Paths without ".." are returned cleaned but otherwise unchanged.
func ExtrasStagingName(ext string) string {
	clean := filepath.Clean(ext)
	parts := strings.Split(filepath.ToSlash(clean), "/")
	i := 0
	for i < len(parts) && parts[i] == ".." {
		i++
	}
	if i >= len(parts) {
		return "." // all segments were ".." — degenerate, caught by validation
	}
	return strings.Join(parts[i:], "/")
}

// MaxExtras is the maximum number of extras entries allowed in a manifest.
const MaxExtras = 50

// ExtrasEntryError describes a validation failure for a single extras entry.
type ExtrasEntryError struct {
	Path    string
	Message string
}

func (e ExtrasEntryError) Error() string {
	return fmt.Sprintf("extras path %q: %s", e.Path, e.Message)
}

// ExtrasValidator accumulates state across multiple ValidateExtrasEntry calls
// to detect cross-entry issues (duplicates, prefix containment).
type ExtrasValidator struct {
	seenStagingNames map[string]string // stagingName → original extras path
}

// NewExtrasValidator creates a validator for a batch of extras entries.
func NewExtrasValidator() *ExtrasValidator {
	return &ExtrasValidator{seenStagingNames: map[string]string{}}
}

// ValidateEntry checks a single extras entry for structural validity:
// empty/dot paths, absolute paths, staging name collisions with reserved dirs,
// duplicate staging names, and prefix containment. It does NOT check filesystem
// existence or resolve symlinks — those require a concrete root and belong in
// the caller.
//
// Returns the computed staging name and nil on success, or an ExtrasEntryError.
func (v *ExtrasValidator) ValidateEntry(ext string) (string, error) {
	clean := filepath.Clean(ext)
	if clean == "" || clean == "." {
		return "", ExtrasEntryError{Path: ext, Message: "must not be empty or '.'"}
	}
	if filepath.IsAbs(ext) || strings.HasPrefix(ext, "/") {
		return "", ExtrasEntryError{Path: ext, Message: "must be relative"}
	}
	staging := ExtrasStagingName(ext)
	if staging == "." || staging == "" {
		return "", ExtrasEntryError{Path: ext, Message: "resolves to empty staging name"}
	}
	top := strings.SplitN(filepath.ToSlash(staging), "/", 2)[0]
	if _, ok := ExtrasReservedDirs[top]; ok {
		return "", ExtrasEntryError{Path: ext, Message: fmt.Sprintf("conflicts with standard content directory %q", top)}
	}
	if prev, dup := v.seenStagingNames[staging]; dup {
		return "", ExtrasEntryError{Path: ext, Message: fmt.Sprintf("collides with another extras entry %q (staging name %q)", prev, staging)}
	}
	// Prefix containment: one entry nests inside another.
	stagingSlash := filepath.ToSlash(staging) + "/"
	for prevStaging, prevExt := range v.seenStagingNames {
		prevSlash := filepath.ToSlash(prevStaging) + "/"
		if strings.HasPrefix(stagingSlash, prevSlash) || strings.HasPrefix(prevSlash, stagingSlash) {
			return "", ExtrasEntryError{Path: ext, Message: fmt.Sprintf("overlaps with %q (prefix containment)", prevExt)}
		}
	}
	v.seenStagingNames[staging] = ext
	return staging, nil
}

// SavePackManifest writes a pack manifest to disk as formatted JSON.
// The manifest describes what content exists on disk. User preferences
// (approved/declined categories) are stored in sync-config, not here.
func SavePackManifest(path string, m PackManifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return util.WriteFileAtomic(path, b)
}
