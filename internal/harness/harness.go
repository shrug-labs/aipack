package harness

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

// Harness is the v2 harness interface. Each harness adapter implements this to
// convert typed content into harness-native format.
//
// Scope determines project vs global paths. Unlike v1 which splits PlanProject/
// PlanGlobal, v2 uses a single Plan method where harnesses switch on ctx.Scope.
type Harness interface {
	// ID returns the harness identifier.
	ID() domain.Harness

	// Layout describes the harness's filesystem footprint for a given scope:
	// which paths it owns, which files it partially manages, and how to
	// strip or reset its managed content.
	Layout(scope domain.Scope, baseDir, home string) Layout

	// Plan produces a Fragment of writes/copies/settings from typed content.
	// Satisfies engine.Planner.
	Plan(ctx context.Context, sctx engine.SyncContext) (domain.Fragment, error)

	// Render produces a Fragment for pack rendering (portable output).
	Render(ctx context.Context, rctx RenderContext) (domain.Fragment, error)

	// Capture extracts harness-native content for round-trip save.
	Capture(ctx context.Context, cctx CaptureContext) (CaptureResult, error)
}

// MCPManagedOverlayEditor is implemented by harness adapters that can rewrite
// their own managed settings overlay during targeted pack deletion.
type MCPManagedOverlayEditor interface {
	EmptyManagedOverlay() []byte
	PruneMCPServersFromManagedOverlay(overlay []byte, serverNames map[string]struct{}) ([]byte, bool, error)
	RetainMCPServersInManagedOverlay(overlay []byte, serverNames map[string]struct{}) ([]byte, error)
}

// LookupMCPManagedOverlayEditor returns the optional managed-overlay editor for
// a harness in the registry.
func LookupMCPManagedOverlayEditor(registry *Registry, id domain.Harness) (MCPManagedOverlayEditor, bool) {
	if registry == nil {
		return nil, false
	}
	h, err := registry.Lookup(id)
	if err != nil {
		return nil, false
	}
	editor, ok := h.(MCPManagedOverlayEditor)
	return editor, ok
}

// FileFormat identifies the serialization format of an OwnedFile.
type FileFormat int

const (
	FormatJSON FileFormat = iota
	FormatTOML
)

// Layout describes a harness's filesystem footprint for a given deployment
// context (scope + directories). ValidationRoots, RemovePaths, and OwnedFiles
// express distinct ownership semantics and must not be conflated.
type Layout struct {
	// ValidationRoots are paths this harness is allowed to write under.
	// Used for destination validation, stale-file scoping, and path ownership.
	ValidationRoots []string

	// RemovePaths are fully-owned paths safe to delete wholesale during clean.
	// These may be directories or leaf files.
	RemovePaths []string

	// OwnedFiles are files where the harness manages specific keys.
	// Both clean and capture derive their behavior from these entries.
	OwnedFiles []OwnedFile
}

// EditContext carries ledger-derived context for surgical OwnedFile edits.
type EditContext struct {
	// ManagedMCPServers are the aipack-managed MCP server names tracked for
	// this file. Harnesses use this set to preserve user-added MCP servers.
	ManagedMCPServers map[string]struct{}

	// PreviousManagedOverlay is the managed overlay recorded for this file on
	// the prior sync. Shared settings that contain object arrays can use this
	// to remove only aipack-managed objects during save and clean.
	PreviousManagedOverlay []byte
}

// OwnedFile describes a file where the harness manages specific keys.
// Strip and Reset express different operations on the same ownership:
// Strip selectively removes managed content (for capture/save),
// Reset clears managed sections (for clean).
type OwnedFile struct {
	Path   string
	Format FileFormat
	Strip  func(root map[string]any, ctx EditContext) // capture: selectively remove managed content
	Reset  func(root map[string]any, ctx EditContext) // clean: reset managed sections
}

// RenderContext provides typed data for pack rendering.
type RenderContext struct {
	OutDir  string
	Profile domain.Profile
}

// CaptureContext provides context for reverse capture (save).
type CaptureContext struct {
	Scope           domain.Scope
	ProjectDir      string
	Home            string
	TargetDir       string
	TargetConfigDir bool
	// KnownPacks is the installed/profile pack-name set. Capture only strips
	// rendered <id>__aipack__<pack> identities when the pack is present here.
	KnownPacks map[string]struct{}
}

// TargetBaseDir returns the base directory a harness should capture from.
// Project scope is always project-local. Global scope defaults to Home unless
// the caller supplied a harness-specific TargetDir override.
func (c CaptureContext) TargetBaseDir() string {
	if c.Scope == domain.ScopeProject {
		return c.ProjectDir
	}
	if c.TargetDir != "" {
		return c.TargetDir
	}
	return c.Home
}

// CaptureResult holds captured content from a harness.
type CaptureResult struct {
	Copies []domain.CopyAction
	Writes []domain.WriteAction

	MCPServers         map[string]domain.MCPServer
	AllowedTools       map[string][]string
	AlwaysAllowedTools map[string][]string
	MCP                []domain.CapturedMCP

	// Typed content populated during capture.
	Rules     []domain.Rule
	Agents    []domain.Agent
	Workflows []domain.Workflow
	Skills    []domain.Skill

	// Warnings collects non-fatal issues found during capture (e.g., parse failures).
	Warnings []domain.Warning
}

// NewCaptureResult returns a CaptureResult with initialized maps.
func NewCaptureResult() CaptureResult {
	return CaptureResult{
		MCPServers:         map[string]domain.MCPServer{},
		AllowedTools:       map[string][]string{},
		AlwaysAllowedTools: map[string][]string{},
	}
}

// MaterializeCapturedMCP builds per-server MCP records from the captured maps
// using the given harness config path.
func (r *CaptureResult) MaterializeCapturedMCP(harnessPath string) {
	if harnessPath == "" {
		return
	}
	names := slices.Sorted(maps.Keys(r.MCPServers))
	r.MCP = r.MCP[:0]
	for _, name := range names {
		server := r.MCPServers[name]
		r.MCP = append(r.MCP, domain.CapturedMCP{
			Server:             server,
			HarnessPath:        filepath.Clean(harnessPath),
			AllowedTools:       append([]string{}, r.AllowedTools[name]...),
			AlwaysAllowedTools: append([]string{}, r.AlwaysAllowedTools[name]...),
		})
	}
}

// PlannedMCPServers projects rendered/captured MCP servers back onto the
// resolved profile metadata needed for sync tracking.
func PlannedMCPServers(source []domain.MCPServer, captured map[string]domain.MCPServer) []domain.MCPServer {
	sourceByName := make(map[string]domain.MCPServer, len(source))
	for _, server := range source {
		sourceByName[server.Name] = server
	}
	names := slices.Sorted(maps.Keys(captured))
	out := make([]domain.MCPServer, 0, len(names))
	for _, name := range names {
		server := captured[name]
		if src, ok := sourceByName[name]; ok {
			server.SourcePack = src.SourcePack
		}
		out = append(out, server)
	}
	return out
}

// listSkillDirs reads skillsDir non-recursively and invokes visit(abs, name)
// for each direct child directory containing a regular SKILL.md. Bundled
// assets (including a nested SKILL.md fixture) stay part of that skill.
// Non-ENOENT stat errors on the root or on a child's SKILL.md surface as
// warnings; ENOENT is silent.
func listSkillDirs(skillsDir string, visit func(abs, name string)) []domain.Warning {
	var warnings []domain.Warning
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			warnings = append(warnings, domain.Warning{
				Path: skillsDir, Message: fmt.Sprintf("reading skills dir: %v", err),
			})
		}
		return warnings
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		entryPath := filepath.Join(skillsDir, e.Name(), domain.SkillEntryFile)
		entryStat, statErr := os.Stat(entryPath)
		if statErr != nil {
			if !os.IsNotExist(statErr) {
				warnings = append(warnings, domain.Warning{
					Path: entryPath, Message: fmt.Sprintf("stat SKILL.md: %v", statErr),
				})
			}
			continue
		}
		if !entryStat.Mode().IsRegular() {
			continue
		}
		visit(filepath.Join(skillsDir, e.Name()), e.Name())
	}
	return warnings
}

// CaptureSkills reads skillsDir for direct child directories containing
// SKILL.md and returns CopyActions (dst prefixed by dstPrefix), Skill
// values, and any walk warnings. Skill.Name is the directory's leaf name.
func CaptureSkills(skillsDir, dstPrefix string) ([]domain.CopyAction, []domain.Skill, []domain.Warning) {
	var copies []domain.CopyAction
	var skills []domain.Skill
	warnings := listSkillDirs(skillsDir, func(abs, name string) {
		copies = append(copies, domain.CopyAction{
			Src: abs, Dst: filepath.Join(dstPrefix, name), Kind: domain.CopyKindDir,
		})
		skills = append(skills, domain.Skill{Name: name, DirPath: abs})
	})
	return copies, skills, warnings
}

// Registry manages harness adapter instances.
type Registry struct {
	byID map[domain.Harness]Harness
}

// NewRegistry creates a registry from harness implementations.
func NewRegistry(harnesses ...Harness) *Registry {
	r := &Registry{byID: map[domain.Harness]Harness{}}
	for _, h := range harnesses {
		r.byID[h.ID()] = h
	}
	return r
}

// Lookup returns a harness by ID.
func (r *Registry) Lookup(id domain.Harness) (Harness, error) {
	h, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("unknown harness: %s", id)
	}
	return h, nil
}

// All returns all registered harnesses in canonical order.
func (r *Registry) All() []Harness {
	all := domain.AllHarnesses()
	out := make([]Harness, 0, len(all))
	for _, id := range all {
		if h, ok := r.byID[id]; ok {
			out = append(out, h)
		}
	}
	return out
}

// AsPlanners converts a list of harness IDs to engine.Planner instances.
func (r *Registry) AsPlanners(ids []domain.Harness) ([]engine.Planner, error) {
	out := make([]engine.Planner, 0, len(ids))
	for _, id := range ids {
		h, err := r.Lookup(id)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

// ValidationRoots returns all validation roots for the given scope and harness IDs.
func ValidationRoots(r *Registry, scope domain.Scope, baseDir, home string, ids []domain.Harness) []string {
	var roots []string
	for _, id := range ids {
		h, err := r.Lookup(id)
		if err != nil {
			continue
		}
		roots = append(roots, h.Layout(scope, baseDir, home).ValidationRoots...)
	}
	return roots
}

// MergeCaptureResults merges multiple CaptureResults into one.
// Returns an error if MCP servers conflict between results.
func MergeCaptureResults(results ...CaptureResult) (CaptureResult, error) {
	merged := CaptureResult{
		MCPServers:         map[string]domain.MCPServer{},
		AllowedTools:       map[string][]string{},
		AlwaysAllowedTools: map[string][]string{},
	}
	for _, res := range results {
		merged.Copies = append(merged.Copies, res.Copies...)
		merged.Writes = append(merged.Writes, res.Writes...)
		merged.MCP = append(merged.MCP, res.MCP...)
		merged.Rules = append(merged.Rules, res.Rules...)
		merged.Agents = append(merged.Agents, res.Agents...)
		merged.Workflows = append(merged.Workflows, res.Workflows...)
		merged.Skills = append(merged.Skills, res.Skills...)
		if err := mergeServers(merged.MCPServers, res.MCPServers); err != nil {
			return CaptureResult{}, err
		}
		mergeAllowedTools(merged.AllowedTools, res.AllowedTools)
		mergeAllowedTools(merged.AlwaysAllowedTools, res.AlwaysAllowedTools)
		merged.Warnings = append(merged.Warnings, res.Warnings...)
	}
	return merged, nil
}

func mergeServers(dst, src map[string]domain.MCPServer) error {
	for k, v := range src {
		if existing, ok := dst[k]; ok {
			if !serversEqual(existing, v) {
				return fmt.Errorf("conflicting MCP server %s in capture", k)
			}
			continue
		}
		dst[k] = v
	}
	return nil
}

func mergeAllowedTools(dst, src map[string][]string) {
	for k, tools := range src {
		if len(tools) == 0 {
			continue
		}
		if _, ok := dst[k]; !ok {
			dst[k] = slices.Clone(tools)
			slices.Sort(dst[k])
			continue
		}
		set := map[string]struct{}{}
		for _, t := range dst[k] {
			set[t] = struct{}{}
		}
		for _, t := range tools {
			set[t] = struct{}{}
		}
		out := slices.Sorted(maps.Keys(set))
		dst[k] = out
	}
}

func serversEqual(a, b domain.MCPServer) bool {
	if a.Transport != b.Transport || a.Timeout != b.Timeout || a.URL != b.URL {
		return false
	}
	if !slices.Equal(a.Command, b.Command) {
		return false
	}
	if !slices.Equal(a.AllowedTools, b.AllowedTools) {
		return false
	}
	if !slices.Equal(a.AlwaysAllowedTools, b.AlwaysAllowedTools) {
		return false
	}
	if !slices.Equal(a.DisabledTools, b.DisabledTools) {
		return false
	}
	if !maps.Equal(a.Headers, b.Headers) {
		return false
	}
	return maps.Equal(a.Env, b.Env)
}

// ---------------------------------------------------------------------------
// Shared types for plan and capture deduplication
// ---------------------------------------------------------------------------

// ContentDirs describes directory paths for standard content types.
// Used as source paths in CaptureContent and destination paths in PlanStandardContent.
type ContentDirs struct {
	Rules     string
	Agents    string
	Workflows string
	Skills    string
}

// PlanStandardContent populates a Fragment with standard content writes.
// If transformAgent is non-nil, each agent is transformed before writing
// (the callback should return the agent with Raw set to transformed bytes).
func PlanStandardContent(
	f *domain.Fragment,
	p domain.Profile,
	dirs ContentDirs,
	namespaced bool,
	transformAgent func(domain.Agent) (domain.Agent, error),
) error {
	if err := AddRenderedRuleWrites(f, dirs.Rules, "", namespaced, p.AllRules()); err != nil {
		return err
	}

	agents := p.AllAgents()
	if transformAgent != nil {
		out := make([]domain.Agent, 0, len(agents))
		for _, a := range agents {
			transformed, err := transformAgent(a)
			if err != nil {
				return err
			}
			out = append(out, transformed)
		}
		agents = out
	}
	skills := p.AllSkills()
	if err := AddRenderedAgentWrites(f, dirs.Agents, "", namespaced, agents, skills); err != nil {
		return err
	}

	if err := AddRenderedWorkflowWrites(f, dirs.Workflows, "", namespaced, p.AllWorkflows()); err != nil {
		return err
	}
	AddRenderedSkillCopies(f, dirs.Skills, "", namespaced, skills)
	return nil
}

// ---------------------------------------------------------------------------
// Path ownership
// ---------------------------------------------------------------------------

// RootsIndex is a pre-computed mapping from cleaned ValidationRoots to harness IDs.
// Build once with BuildRootsIndex, then call Identify per path.
type RootsIndex struct {
	entries []rootEntry
}

type rootEntry struct {
	root string
	id   domain.Harness
}

// BuildRootsIndex pre-computes ValidationRoots for all harnesses in the registry.
func BuildRootsIndex(reg *Registry, scope domain.Scope, baseDir, home string) RootsIndex {
	return BuildRootsIndexWithBaseDir(reg, scope, home, func(domain.Harness) string {
		return baseDir
	})
}

// BuildRootsIndexWithBaseDir pre-computes ValidationRoots with a per-harness
// base directory resolver. Use this when global harnesses target different
// runtime config roots.
func BuildRootsIndexWithBaseDir(reg *Registry, scope domain.Scope, home string, baseDirFor func(domain.Harness) string) RootsIndex {
	var entries []rootEntry
	for _, h := range reg.All() {
		baseDir := ""
		if baseDirFor != nil {
			baseDir = baseDirFor(h.ID())
		}
		for _, root := range h.Layout(scope, baseDir, home).ValidationRoots {
			entries = append(entries, rootEntry{root: filepath.Clean(root), id: h.ID()})
		}
	}
	return RootsIndex{entries: entries}
}

// Identify returns the harness that manages the given path via prefix matching.
// Returns "" if no harness claims the path.
func (idx RootsIndex) Identify(path string) domain.Harness {
	cleanPath := filepath.Clean(path)
	for _, e := range idx.entries {
		if cleanPath == e.root || strings.HasPrefix(cleanPath, e.root+string(filepath.Separator)) {
			return e.id
		}
	}
	return ""
}
