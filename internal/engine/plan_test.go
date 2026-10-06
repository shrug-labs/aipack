package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/plugin"
)

// mockPlanner is a test double for Planner that returns a fixed Fragment or error.
type mockPlanner struct {
	id        domain.Harness
	frag      domain.Fragment
	err       error
	targetDir string
}

func (m *mockPlanner) ID() domain.Harness { return m.id }
func (m *mockPlanner) Plan(_ context.Context, ctx SyncContext) (domain.Fragment, error) {
	m.targetDir = ctx.TargetDir
	return m.frag, m.err
}

func TestPlanSync_ReportsAllUnsupportedSelectionsBeforePlanning(t *testing.T) {
	t.Parallel()
	profile := domain.NewProfile()
	profile.Packs = []domain.Pack{
		{Name: "first", NativePlugin: &domain.NativePluginSelection{
			Package: domain.NativePlugin{Harness: domain.HarnessCodex, SettingsFiles: []string{"native.json"}},
			Selected: map[domain.PackCategory][]string{
				domain.CategorySkills: {"review"}, domain.CategoryHooks: {"stop"},
			}, SettingsEnabled: true,
		}},
		{Name: "second", NativePlugin: &domain.NativePluginSelection{
			Package:  domain.NativePlugin{Harness: domain.HarnessCodex},
			Selected: map[domain.PackCategory][]string{domain.CategoryMCP: {"search"}},
		}},
	}
	planners := []*mockPlanner{{id: domain.HarnessClaudeCode}, {id: domain.HarnessOpenCode}}
	_, err := PlanSync(context.Background(), profile, PlanRequest{Scope: domain.ScopeProject, ProjectDir: t.TempDir()}, []Planner{planners[0], planners[1]})
	if err == nil {
		t.Fatal("unsupported selection produced an activation plan")
	}
	for _, text := range []string{`pack "first"`, `pack "second"`, string(domain.HarnessClaudeCode), string(domain.HarnessOpenCode), "hooks/stop, settings/native.json, skills/review", "mcp/search"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("missing %q in compatibility refusal: %v", text, err)
		}
	}
	for _, planner := range planners {
		if planner.targetDir != "" {
			t.Fatal("harness planning ran before all target incompatibilities were reported")
		}
	}
}

func TestPlanSync_PortablePayloadFailureBeforeNativePlanning(t *testing.T) {
	t.Parallel()
	profile := domain.NewProfile()
	profile.Packs = []domain.Pack{{Name: "imported", NativePlugin: &domain.NativePluginSelection{
		Root: t.TempDir(), Package: domain.NativePlugin{Harness: domain.HarnessCodex, Format: plugin.CodexLegacy, ConverterVersion: plugin.ConverterVersion, Manifest: ".codex-plugin/plugin.json"},
	}}}
	native, portable := &mockPlanner{id: domain.HarnessCodex}, &mockPlanner{id: domain.HarnessClaudeCode}
	_, err := PlanSync(context.Background(), profile, PlanRequest{Scope: domain.ScopeProject, ProjectDir: t.TempDir()}, []Planner{native, portable})
	if err == nil || !strings.Contains(err.Error(), "delivery to claudecode") || native.targetDir != "" || portable.targetDir != "" {
		t.Fatalf("portable payload failure was not checked before native planning: %v", err)
	}
}

func TestPlanSync_SingleHarness(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	profile := domain.NewProfile()
	req := PlanRequest{
		Scope:      domain.ScopeProject,
		Harnesses:  []domain.Harness{domain.HarnessClaudeCode},
		ProjectDir: dir,
	}

	aPath := filepath.Join(dir, "a.md")
	bPath := filepath.Join(dir, "b.md")
	planner := &mockPlanner{
		id: domain.HarnessClaudeCode,
		frag: domain.Fragment{
			Writes: []domain.WriteAction{
				{Dst: aPath, Content: []byte("alpha")},
				{Dst: bPath, Content: []byte("bravo")},
			},
			Desired: []string{aPath, bPath},
		},
	}

	plan, err := PlanSync(context.Background(), profile, req, []Planner{planner})
	if err != nil {
		t.Fatalf("PlanSync: %v", err)
	}

	if got := len(plan.Writes); got != 2 {
		t.Errorf("len(Writes) = %d, want 2", got)
	}
	if got := len(plan.Desired); got != 2 {
		t.Errorf("len(Desired) = %d, want 2", got)
	}
	for _, dst := range []string{aPath, bPath} {
		if _, ok := plan.Desired[dst]; !ok {
			t.Errorf("Desired missing %q", dst)
		}
	}
}

func TestPlanSync_RejectsMissingNativeDelivery(t *testing.T) {
	t.Parallel()
	p := domain.NativePlugin{Name: "probe", Marketplace: "market", Harness: domain.HarnessClaudeCode}
	profile := domain.NewProfile()
	profile.Packs = []domain.Pack{{Name: "alias", NativePlugin: &domain.NativePluginSelection{Package: p}}}
	planner := &mockPlanner{id: domain.HarnessClaudeCode}
	req := PlanRequest{Scope: domain.ScopeProject, ProjectDir: t.TempDir()}
	if _, err := PlanSync(context.Background(), profile, req, []Planner{planner}); err == nil || !strings.Contains(err.Error(), "does not implement native delivery") {
		t.Fatalf("missing native delivery silently succeeded: %v", err)
	}
	planner.frag.NativePlugins = []domain.NativePluginAction{{Package: p, SourcePack: "alias"}}
	if _, err := PlanSync(context.Background(), profile, req, []Planner{planner}); err != nil {
		t.Fatal(err)
	}
}

func TestPlanSync_MultipleHarnesses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	profile := domain.NewProfile()
	req := PlanRequest{
		Scope:      domain.ScopeProject,
		Harnesses:  []domain.Harness{domain.HarnessClaudeCode, domain.HarnessCline},
		ProjectDir: dir,
	}

	claudePath := filepath.Join(dir, "claude.md")
	clinePath := filepath.Join(dir, "cline.md")
	p1 := &mockPlanner{
		id: domain.HarnessClaudeCode,
		frag: domain.Fragment{
			Writes: []domain.WriteAction{
				{Dst: claudePath, Content: []byte("claude")},
			},
			Desired: []string{claudePath},
		},
	}
	p2 := &mockPlanner{
		id: domain.HarnessCline,
		frag: domain.Fragment{
			Writes: []domain.WriteAction{
				{Dst: clinePath, Content: []byte("cline")},
			},
			Desired: []string{clinePath},
		},
	}

	plan, err := PlanSync(context.Background(), profile, req, []Planner{p1, p2})
	if err != nil {
		t.Fatalf("PlanSync: %v", err)
	}

	if got := len(plan.Writes); got != 2 {
		t.Errorf("len(Writes) = %d, want 2", got)
	}
}

func TestPlanSync_HarnessError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	profile := domain.NewProfile()
	req := PlanRequest{
		Scope:      domain.ScopeProject,
		Harnesses:  []domain.Harness{domain.HarnessClaudeCode},
		ProjectDir: dir,
	}

	planner := &mockPlanner{
		id:  domain.HarnessClaudeCode,
		err: errBoom{},
	}

	_, err := PlanSync(context.Background(), profile, req, []Planner{planner})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), string(domain.HarnessClaudeCode)) {
		t.Errorf("error %q should contain harness ID %q", err, domain.HarnessClaudeCode)
	}
}

// errBoom is a simple error for testing.
type errBoom struct{}

func (errBoom) Error() string { return "boom" }

func TestPlanSync_GlobalRequiresHome(t *testing.T) {
	t.Parallel()

	profile := domain.NewProfile()
	req := PlanRequest{
		Scope:      domain.ScopeGlobal,
		Harnesses:  []domain.Harness{domain.HarnessClaudeCode},
		ProjectDir: t.TempDir(),
		Home:       "", // intentionally empty
	}

	_, err := PlanSync(context.Background(), profile, req, nil)
	if err == nil {
		t.Fatal("expected error for empty HOME, got nil")
	}
	if !strings.Contains(err.Error(), "HOME") {
		t.Errorf("error %q should contain %q", err, "HOME")
	}
}

func TestPlanSync_UsesTargetDirOverride(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	target := t.TempDir()
	planner := &mockPlanner{id: domain.HarnessCodex}
	req := PlanRequest{
		Scope:     domain.ScopeGlobal,
		Harnesses: []domain.Harness{domain.HarnessCodex},
		Home:      home,
		TargetDir: target,
	}

	if _, err := PlanSync(context.Background(), domain.NewProfile(), req, []Planner{planner}); err != nil {
		t.Fatalf("PlanSync: %v", err)
	}
	if planner.targetDir != target {
		t.Fatalf("planner TargetDir = %q, want %q", planner.targetDir, target)
	}
}

func TestPlanSync_LedgerPath_Project(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	profile := domain.NewProfile()
	req := PlanRequest{
		Scope:      domain.ScopeProject,
		Harnesses:  []domain.Harness{domain.HarnessClaudeCode},
		ProjectDir: dir,
	}

	plan, err := PlanSync(context.Background(), profile, req, nil)
	if err != nil {
		t.Fatalf("PlanSync: %v", err)
	}

	// The ledger path should be inside the aipack config dir.
	if !strings.Contains(plan.Ledger, "aipack") || !strings.Contains(plan.Ledger, "ledger") {
		t.Errorf("Ledger %q should contain aipack config ledger path", plan.Ledger)
	}
	if !strings.HasSuffix(plan.Ledger, ".json") {
		t.Errorf("Ledger %q should end with .json", plan.Ledger)
	}
}

func TestPlanSync_LedgerPath_Global(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	profile := domain.NewProfile()
	req := PlanRequest{
		Scope:     domain.ScopeGlobal,
		Harnesses: []domain.Harness{domain.HarnessClaudeCode},
		Home:      home,
	}

	p1 := &mockPlanner{id: domain.HarnessClaudeCode}

	plan, err := PlanSync(context.Background(), profile, req, []Planner{p1})
	if err != nil {
		t.Fatalf("PlanSync: %v", err)
	}

	if !strings.HasPrefix(plan.Ledger, home) {
		t.Errorf("Ledger %q should be under Home %q", plan.Ledger, home)
	}
	cfgDir, _ := config.DefaultConfigDir(home)
	want := filepath.Join(cfgDir, "ledger", "claudecode.json")
	if plan.Ledger != want {
		t.Errorf("Ledger = %q, want %q", plan.Ledger, want)
	}
}

func TestPlanSync_LedgerPath_UsesConfigDir(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	configDir := t.TempDir()
	req := PlanRequest{
		ConfigDir:  configDir,
		Scope:      domain.ScopeProject,
		Harnesses:  []domain.Harness{domain.HarnessCodex},
		ProjectDir: filepath.Join(home, "project"),
		Home:       home,
	}

	plan, err := PlanSync(context.Background(), domain.NewProfile(), req, nil)
	if err != nil {
		t.Fatalf("PlanSync: %v", err)
	}

	want := filepath.Join(configDir, "ledger", EncodeProjectPath(req.ProjectDir), "codex.json")
	if plan.Ledger != want {
		t.Fatalf("Ledger = %q, want %q", plan.Ledger, want)
	}
}
