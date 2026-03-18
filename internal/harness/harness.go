package harness

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/util"
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
	Plan(ctx engine.SyncContext) (domain.Fragment, error)

	// Render produces a Fragment for pack rendering (portable output).
	Render(ctx RenderContext) (domain.Fragment, error)

	// Capture extracts harness-native content for round-trip save.
	Capture(ctx CaptureContext) (CaptureResult, error)
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

// OwnedFile describes a file where the harness manages specific keys.
// Strip and Reset express different operations on the same ownership:
// Strip selectively removes managed content (for capture/save),
// Reset aggressively clears managed sections (for clean).
type OwnedFile struct {
	Path   string
	Format FileFormat
	Strip  func(root map[string]any) // capture: selectively remove managed content
	Reset  func(root map[string]any) // clean: reset managed sections
}

// RenderContext provides typed data for pack rendering.
type RenderContext struct {
	OutDir  string
	Profile domain.Profile
}

// CaptureContext provides context for reverse capture (save).
type CaptureContext struct {
	Scope      domain.Scope
	ProjectDir string
	Home       string
}

// CaptureResult holds captured content from a harness.
type CaptureResult struct {
	Copies []domain.CopyAction
	Writes []domain.WriteAction

	MCPServers   map[string]domain.MCPServer
	AllowedTools map[string][]string
	MCP          []domain.CapturedMCP

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
		MCPServers:   map[string]domain.MCPServer{},
		AllowedTools: map[string][]string{},
	}
}

// MaterializeCapturedMCP builds per-server MCP records from the captured maps
// using the given harness config path.
func (r *CaptureResult) MaterializeCapturedMCP(harnessPath string) {
	if harnessPath == "" {
		return
	}
	names := make([]string, 0, len(r.MCPServers))
	for name := range r.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	r.MCP = r.MCP[:0]
	for _, name := range names {
		server := r.MCPServers[name]
		r.MCP = append(r.MCP, domain.CapturedMCP{
			Server:       server,
			HarnessPath:  filepath.Clean(harnessPath),
			AllowedTools: append([]string{}, r.AllowedTools[name]...),
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
	names := make([]string, 0, len(captured))
	for name := range captured {
		names = append(names, name)
	}
	sort.Strings(names)
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

// CaptureSkills scans skillsDir for sub-directories and returns CopyActions
// (with dst prefixed by dstPrefix) and Skill values for each.
func CaptureSkills(skillsDir, dstPrefix string) ([]domain.CopyAction, []domain.Skill) {
	dirs := util.ListSubDirs(skillsDir)
	var copies []domain.CopyAction
	var skills []domain.Skill
	for _, d := range dirs {
		name := filepath.Base(d)
		copies = append(copies, domain.CopyAction{
			Src: d, Dst: filepath.Join(dstPrefix, name), Kind: domain.CopyKindDir,
		})
		skills = append(skills, domain.Skill{Name: name, DirPath: d})
	}
	return copies, skills
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
		MCPServers:   map[string]domain.MCPServer{},
		AllowedTools: map[string][]string{},
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
			dst[k] = append([]string{}, tools...)
			sort.Strings(dst[k])
			continue
		}
		set := map[string]struct{}{}
		for _, t := range dst[k] {
			set[t] = struct{}{}
		}
		for _, t := range tools {
			set[t] = struct{}{}
		}
		out := make([]string, 0, len(set))
		for t := range set {
			out = append(out, t)
		}
		sort.Strings(out)
		dst[k] = out
	}
}

func serversEqual(a, b domain.MCPServer) bool {
	if a.Transport != b.Transport || a.Timeout != b.Timeout || a.URL != b.URL {
		return false
	}
	if !stringSliceEqual(a.Command, b.Command) {
		return false
	}
	if !stringSliceEqual(a.AllowedTools, b.AllowedTools) {
		return false
	}
	if !stringSliceEqual(a.DisabledTools, b.DisabledTools) {
		return false
	}
	if !stringMapEqual(a.Headers, b.Headers) {
		return false
	}
	return stringMapEqual(a.Env, b.Env)
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stringMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
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
	transformAgent func(domain.Agent) (domain.Agent, error),
) error {
	f.AddRuleWrites(dirs.Rules, "", p.AllRules())

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
	f.AddAgentWrites(dirs.Agents, "", agents)

	f.AddWorkflowWrites(dirs.Workflows, "", p.AllWorkflows())
	f.AddSkillCopies(dirs.Skills, "", p.AllSkills())
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
	var entries []rootEntry
	for _, h := range reg.All() {
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

