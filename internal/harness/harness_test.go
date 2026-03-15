package harness

import (
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

// stubHarness implements Harness for testing the registry.
type stubHarness struct {
	id domain.Harness
}

func (s stubHarness) ID() domain.Harness                                      { return s.id }
func (s stubHarness) Plan(engine.SyncContext) (domain.Fragment, error)        { return domain.Fragment{}, nil }
func (s stubHarness) Render(RenderContext) (domain.Fragment, error)           { return domain.Fragment{}, nil }
func (s stubHarness) ManagedRoots(domain.Scope, string, string) []string      { return nil }
func (s stubHarness) SettingsPaths(domain.Scope, string, string) []string     { return nil }
func (s stubHarness) StrictExtraDirs(domain.Scope, string, string) []string   { return nil }
func (s stubHarness) PackRelativePaths() []string                             { return nil }
func (s stubHarness) StripManagedSettings([]byte, string) ([]byte, error)     { return nil, nil }
func (s stubHarness) Capture(CaptureContext) (CaptureResult, error)           { return CaptureResult{}, nil }
func (s stubHarness) CleanActions(domain.Scope, string, string) []CleanAction { return nil }

func TestNewRegistry_LookupAll(t *testing.T) {
	t.Parallel()
	cc := stubHarness{id: domain.HarnessClaudeCode}
	oc := stubHarness{id: domain.HarnessOpenCode}

	r := NewRegistry(cc, oc)

	h, err := r.Lookup(domain.HarnessClaudeCode)
	if err != nil {
		t.Fatalf("Lookup claudecode: %v", err)
	}
	if h.ID() != domain.HarnessClaudeCode {
		t.Errorf("got %q want %q", h.ID(), domain.HarnessClaudeCode)
	}

	_, err = r.Lookup(domain.HarnessCline)
	if err == nil {
		t.Error("expected error for unregistered harness")
	}
}

func TestRegistry_All(t *testing.T) {
	t.Parallel()
	cc := stubHarness{id: domain.HarnessClaudeCode}
	oc := stubHarness{id: domain.HarnessOpenCode}
	cx := stubHarness{id: domain.HarnessCodex}

	r := NewRegistry(cc, oc, cx)

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("All: got %d want 3", len(all))
	}
	// AllHarnesses returns canonical order: cline, claudecode, codex, opencode.
	if all[0].ID() != domain.HarnessClaudeCode {
		t.Errorf("all[0]: got %q want claudecode", all[0].ID())
	}
}

func TestRegistry_AsPlanners(t *testing.T) {
	t.Parallel()
	cc := stubHarness{id: domain.HarnessClaudeCode}
	oc := stubHarness{id: domain.HarnessOpenCode}

	r := NewRegistry(cc, oc)

	planners, err := r.AsPlanners([]domain.Harness{domain.HarnessClaudeCode, domain.HarnessOpenCode})
	if err != nil {
		t.Fatalf("AsPlanners: %v", err)
	}
	if len(planners) != 2 {
		t.Errorf("planners: got %d want 2", len(planners))
	}

	_, err = r.AsPlanners([]domain.Harness{domain.HarnessCline})
	if err == nil {
		t.Error("expected error for unregistered harness in AsPlanners")
	}
}

func TestManagedRoots_AggregatesHarnesses(t *testing.T) {
	t.Parallel()
	h1 := stubHarnessWithRoots{stubHarness: stubHarness{id: domain.HarnessClaudeCode}, roots: []string{"/a", "/b"}}
	h2 := stubHarnessWithRoots{stubHarness: stubHarness{id: domain.HarnessOpenCode}, roots: []string{"/c"}}

	r := NewRegistry(h1, h2)

	roots := ManagedRoots(r, domain.ScopeProject, "/proj", "/home", []domain.Harness{domain.HarnessClaudeCode, domain.HarnessOpenCode})
	if len(roots) != 3 {
		t.Errorf("managed roots: got %d want 3", len(roots))
	}
}

// stubHarnessWithRoots adds ManagedRoots return to the stub.
type stubHarnessWithRoots struct {
	stubHarness
	roots []string
}

func (s stubHarnessWithRoots) ManagedRoots(domain.Scope, string, string) []string { return s.roots }

func TestMergeCaptureResults_Disjoint(t *testing.T) {
	t.Parallel()
	a := CaptureResult{
		Copies: []domain.CopyAction{{Src: "/a", Dst: "rules/a.md"}},
		Rules:  []domain.Rule{{Name: "a"}},
		MCPServers: map[string]domain.MCPServer{
			"srv1": {Name: "srv1", Command: []string{"cmd1"}},
		},
		AllowedTools: map[string][]string{"srv1": {"tool1"}},
	}
	b := CaptureResult{
		Copies: []domain.CopyAction{{Src: "/b", Dst: "agents/b.md"}},
		Agents: []domain.Agent{{Name: "b"}},
		MCPServers: map[string]domain.MCPServer{
			"srv2": {Name: "srv2", Command: []string{"cmd2"}},
		},
		AllowedTools: map[string][]string{"srv2": {"tool2"}},
	}

	merged, err := MergeCaptureResults(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Copies) != 2 {
		t.Errorf("Copies = %d, want 2", len(merged.Copies))
	}
	if len(merged.Rules) != 1 || len(merged.Agents) != 1 {
		t.Errorf("typed: Rules=%d Agents=%d", len(merged.Rules), len(merged.Agents))
	}
	if len(merged.MCPServers) != 2 {
		t.Errorf("MCPServers = %d, want 2", len(merged.MCPServers))
	}
	if len(merged.AllowedTools) != 2 {
		t.Errorf("AllowedTools = %d, want 2", len(merged.AllowedTools))
	}
}

func TestMergeCaptureResults_ConflictingMCP(t *testing.T) {
	t.Parallel()
	a := CaptureResult{
		MCPServers:   map[string]domain.MCPServer{"srv": {Name: "srv", Command: []string{"cmd1"}}},
		AllowedTools: map[string][]string{},
	}
	b := CaptureResult{
		MCPServers:   map[string]domain.MCPServer{"srv": {Name: "srv", Command: []string{"cmd2"}}},
		AllowedTools: map[string][]string{},
	}

	_, err := MergeCaptureResults(a, b)
	if err == nil {
		t.Error("expected error for conflicting MCP servers")
	}
}

func TestMergeCaptureResults_ToolDedup(t *testing.T) {
	t.Parallel()
	a := CaptureResult{
		MCPServers:   map[string]domain.MCPServer{},
		AllowedTools: map[string][]string{"srv": {"b", "a"}},
	}
	b := CaptureResult{
		MCPServers:   map[string]domain.MCPServer{},
		AllowedTools: map[string][]string{"srv": {"a", "c"}},
	}

	merged, err := MergeCaptureResults(a, b)
	if err != nil {
		t.Fatal(err)
	}
	tools := merged.AllowedTools["srv"]
	if len(tools) != 3 {
		t.Fatalf("tools = %v, want 3 entries", tools)
	}
	// Should be sorted.
	if tools[0] != "a" || tools[1] != "b" || tools[2] != "c" {
		t.Errorf("tools = %v, want [a b c]", tools)
	}
}

// ---------------------------------------------------------------------------
// IdentifyHarness tests
// ---------------------------------------------------------------------------

func TestIdentifyHarness_ExactMatch(t *testing.T) {
	t.Parallel()
	h := stubHarnessWithRoots{stubHarness: stubHarness{id: domain.HarnessClaudeCode}, roots: []string{"/proj/.claude"}}
	r := NewRegistry(h)
	got := IdentifyHarness(r, domain.ScopeProject, "/proj", "/home", "/proj/.claude")
	if got != domain.HarnessClaudeCode {
		t.Errorf("got %q, want claudecode", got)
	}
}

func TestIdentifyHarness_PrefixMatch(t *testing.T) {
	t.Parallel()
	h := stubHarnessWithRoots{stubHarness: stubHarness{id: domain.HarnessClaudeCode}, roots: []string{"/proj/.claude"}}
	r := NewRegistry(h)
	got := IdentifyHarness(r, domain.ScopeProject, "/proj", "/home", "/proj/.claude/rules/foo.md")
	if got != domain.HarnessClaudeCode {
		t.Errorf("got %q, want claudecode", got)
	}
}

func TestIdentifyHarness_NoMatch(t *testing.T) {
	t.Parallel()
	h := stubHarnessWithRoots{stubHarness: stubHarness{id: domain.HarnessClaudeCode}, roots: []string{"/proj/.claude"}}
	r := NewRegistry(h)
	got := IdentifyHarness(r, domain.ScopeProject, "/proj", "/home", "/proj/.other/file.md")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestIdentifyHarness_NoPrefixFalsePositive(t *testing.T) {
	t.Parallel()
	h := stubHarnessWithRoots{stubHarness: stubHarness{id: domain.HarnessClaudeCode}, roots: []string{"/proj/.claude"}}
	r := NewRegistry(h)
	// /proj/.claude-extra should NOT match /proj/.claude (not a separator-aware prefix)
	got := IdentifyHarness(r, domain.ScopeProject, "/proj", "/home", "/proj/.claude-extra/file.md")
	if got != "" {
		t.Errorf("got %q, want empty (should not match partial dir name)", got)
	}
}
