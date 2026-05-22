package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// PackSchemaVersion is the manifest schema version aipack emits for new
// packs. Schema 2 shipped with v0.23: `mcp` is a flat array of server IDs,
// and tool policy lives entirely in profiles. Schema 1 is still accepted for
// backward compat — see ParsePackManifest for routing details.
const PackSchemaVersion = 2

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
	Plugins       []string `json:"plugins,omitempty"`
	// MCP holds server IDs — the runtime never sees v1's nested shape.
	// parseV1Manifest extracts server keys from the legacy object into this
	// slice so downstream code has a single representation to work with.
	MCP []string `json:"mcp,omitempty"`

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

	// Hooks lists hook IDs discovered from the hooks/ directory. Each ID
	// corresponds to hooks/<id>/HOOK.yaml. Handler scripts and assets live
	// alongside the descriptor inside the hook directory.
	Hooks []string `json:"hooks,omitempty"`

	// resolvedPaths maps id → pack-relative path, populated by DiscoverContent.
	// For agents/workflows/skills, where authoring permits organizational
	// subdirectories under the category root, the on-disk path may differ
	// from the canonical PrimaryRelPath. RelPath consults this first and
	// falls back to PrimaryRelPath when no override is recorded.
	resolvedPaths map[domain.PackCategory]map[string]string
}

// RelPath returns the pack-relative path for a content item. Agents,
// workflows, and skills may live under organizational subdirectories within
// their category root (e.g. `agents/team-a/foo.md` for id `foo`); when
// DiscoverContent has recorded the actual path, it wins. Otherwise the
// canonical PackCategory.PrimaryRelPath is returned.
func (m PackManifest) RelPath(cat domain.PackCategory, id string) string {
	if m.resolvedPaths != nil {
		if catMap, ok := m.resolvedPaths[cat]; ok {
			if p, ok := catMap[id]; ok {
				return p
			}
		}
	}
	return cat.PrimaryRelPath(id)
}

// SetResolvedPath records the actual on-disk path for a content id (test
// helper / discovery use).
func (m *PackManifest) SetResolvedPath(cat domain.PackCategory, id, relPath string) {
	if m.resolvedPaths == nil {
		m.resolvedPaths = map[domain.PackCategory]map[string]string{}
	}
	if m.resolvedPaths[cat] == nil {
		m.resolvedPaths[cat] = map[string]string{}
	}
	m.resolvedPaths[cat][id] = relPath
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

// ContentIDsPtr returns a pointer to the content ID slice for the given
// category, or nil for unknown categories. Use this when you need to
// mutate the manifest's content list in place.
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
	case domain.CategoryHooks:
		return &m.Hooks
	case domain.CategoryPrompts:
		return &m.Prompts
	case domain.CategoryMCP:
		return &m.MCP
	case domain.CategoryPlugins:
		return &m.Plugins
	}
	return nil
}

// ContentIDs returns the resource IDs for the given category.
func (m PackManifest) ContentIDs(cat domain.PackCategory) []string {
	if p := m.ContentIDsPtr(cat); p != nil {
		return *p
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
	case domain.CategoryHooks:
		return &pe.Hooks.VectorSelector
	case domain.CategoryPlugins:
		return &pe.Plugins
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

// ParsePackManifest unmarshals and validates a pack manifest from raw JSON
// bytes. schema_version routes the parse: `1` expects the legacy nested
// `mcp` object form; `2` expects the flat `mcp` array. Shape is strictly
// tied to version — a v1 manifest with a flat `mcp` array (or vice versa)
// is rejected at parse time rather than silently coerced.
func ParsePackManifest(data []byte) (PackManifest, error) {
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	_ = json.Unmarshal(data, &probe)

	var (
		m   PackManifest
		err error
	)
	switch probe.SchemaVersion {
	case 0:
		return PackManifest{}, fmt.Errorf("pack manifest schema_version must be set")
	case 1:
		m, err = parseV1Manifest(data)
	case 2:
		m, err = parseV2Manifest(data)
	default:
		return PackManifest{}, fmt.Errorf("unsupported pack manifest schema_version %d (aipack supports 1 and 2)", probe.SchemaVersion)
	}
	if err != nil {
		return PackManifest{}, err
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

// parseV1Manifest parses a schema_version: 1 manifest where `mcp` is the
// legacy nested object (`{"servers": {...}, "default_allowed_tools": [...]}`).
// Server IDs are extracted into m.MCP so the rest of the codebase can stay
// single-shape. Pack-level tool policy fields (default_allowed_tools,
// default_always_allowed_tools) and per-server entries under `servers` are
// read but not enforced by the v0.23+ runtime — authors who want tool policy
// should upgrade to schema_version: 2 and express it in profiles. A v1
// manifest whose `mcp` is a flat array (or any non-object shape) is rejected.
func parseV1Manifest(data []byte) (PackManifest, error) {
	if err := rejectMCPShapeMismatch(data, 1); err != nil {
		return PackManifest{}, err
	}
	type v1MCP struct {
		Servers map[string]json.RawMessage `json:"servers"`
	}
	// Embed PackManifest so every other field still populates; the outer
	// MCP (v1MCP) shadows PackManifest.MCP during unmarshal.
	type v1Wrapper struct {
		PackManifest
		MCP v1MCP `json:"mcp"`
	}
	var w v1Wrapper
	if err := json.Unmarshal(data, &w); err != nil {
		return PackManifest{}, err
	}
	m := w.PackManifest
	ids := make([]string, 0, len(w.MCP.Servers))
	for id := range w.MCP.Servers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	m.MCP = ids
	return m, nil
}

// parseV2Manifest parses a schema_version: 2 manifest where `mcp` is a flat
// array of server IDs. Tool policy lives entirely in profiles. A v2
// manifest whose `mcp` is a nested object is rejected.
func parseV2Manifest(data []byte) (PackManifest, error) {
	if err := rejectMCPShapeMismatch(data, 2); err != nil {
		return PackManifest{}, err
	}
	var m PackManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return PackManifest{}, err
	}
	return m, nil
}

// rejectMCPShapeMismatch enforces the one-shape-per-schema contract: v1 pairs
// with an object, v2 with an array. Absent `mcp` is accepted under either
// version (auto-discovery from mcp/*.json populates it).
func rejectMCPShapeMismatch(data []byte, schemaVersion int) error {
	var probe struct {
		MCP json.RawMessage `json:"mcp"`
	}
	_ = json.Unmarshal(data, &probe)
	raw := bytes.TrimSpace(probe.MCP)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	first := raw[0]
	switch schemaVersion {
	case 1:
		if first != '{' {
			return fmt.Errorf("schema_version: 1 expects `mcp` as a nested object with a `servers` map; got a %s (bump to schema_version: 2 to use the flat-array form)", shapeName(first))
		}
	case 2:
		if first != '[' {
			return fmt.Errorf("schema_version: 2 expects `mcp` as a flat array of server IDs; got a %s (use schema_version: 1 for the legacy nested form, or convert `mcp` to `[...]`)", shapeName(first))
		}
	}
	return nil
}

// shapeName labels the JSON shape that was found so error messages point at
// the mismatch rather than asking the author to guess.
func shapeName(first byte) string {
	switch first {
	case '{':
		return "nested object"
	case '[':
		return "flat array"
	case '"':
		return "string"
	}
	return "scalar"
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
		paths = append(paths, m.RelPath(domain.CategoryRules, id))
	}
	for _, id := range m.Agents {
		paths = append(paths, m.RelPath(domain.CategoryAgents, id))
	}
	for _, id := range m.Workflows {
		paths = append(paths, m.RelPath(domain.CategoryWorkflows, id))
	}
	for _, id := range m.Skills {
		// Trailing "/" so git archive fetches the entire skill directory.
		// RelPath returns the SKILL.md path; strip it to get the dir.
		skillFile := m.RelPath(domain.CategorySkills, id)
		paths = append(paths, strings.TrimSuffix(skillFile, "/"+domain.SkillEntryFile)+"/")
	}
	for _, id := range m.Prompts {
		paths = append(paths, filepath.ToSlash(filepath.Join("prompts", id+".md")))
	}
	for _, id := range m.Plugins {
		paths = append(paths, m.RelPath(domain.CategoryPlugins, id))
	}
	for _, name := range m.MCP {
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
	for _, id := range m.Hooks {
		hookFile := m.RelPath(domain.CategoryHooks, id)
		paths = append(paths, strings.TrimSuffix(hookFile, "/"+domain.HookEntryFile)+"/")
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
	"mcp": {}, "plugins": {}, "configs": {}, "hooks": {}, "prompts": {}, "profiles": {},
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
//
// SchemaVersion is always stamped to PackSchemaVersion before serialization.
// The in-memory struct's MCP field is a flat []string (parseV1Manifest
// normalizes v1's nested shape into it), so the serialized form is always
// v2. Without this stamp, round-tripping a v1 pack through load → save
// produces schema_version: 1 + flat mcp array, which ParsePackManifest then
// rejects as a shape/version mismatch.
func SavePackManifest(path string, m PackManifest) error {
	m.SchemaVersion = PackSchemaVersion
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return util.WriteFileAtomic(path, b)
}
