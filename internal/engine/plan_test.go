package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

// mockPlanner is a test double for Planner that returns a fixed Fragment or error.
type mockPlanner struct {
	id   domain.Harness
	frag domain.Fragment
	err  error
}

func (m *mockPlanner) ID() domain.Harness                          { return m.id }
func (m *mockPlanner) Plan(_ SyncContext) (domain.Fragment, error) { return m.frag, m.err }

func TestPlanSync_SingleHarness(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	profile := domain.NewProfile()
	req := PlanRequest{
		Scope:      domain.ScopeProject,
		Harnesses:  []domain.Harness{domain.HarnessClaudeCode},
		ProjectDir: dir,
	}

	planner := &mockPlanner{
		id: domain.HarnessClaudeCode,
		frag: domain.Fragment{
			Writes: []domain.WriteAction{
				{Dst: dir + "/a.md", Content: []byte("alpha")},
				{Dst: dir + "/b.md", Content: []byte("bravo")},
			},
			Desired: []string{dir + "/a.md", dir + "/b.md"},
		},
	}

	plan, err := PlanSync(profile, req, []Planner{planner})
	if err != nil {
		t.Fatalf("PlanSync: %v", err)
	}

	if got := len(plan.Writes); got != 2 {
		t.Errorf("len(Writes) = %d, want 2", got)
	}
	if got := len(plan.Desired); got != 2 {
		t.Errorf("len(Desired) = %d, want 2", got)
	}
	for _, dst := range []string{dir + "/a.md", dir + "/b.md"} {
		if _, ok := plan.Desired[dst]; !ok {
			t.Errorf("Desired missing %q", dst)
		}
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

	p1 := &mockPlanner{
		id: domain.HarnessClaudeCode,
		frag: domain.Fragment{
			Writes: []domain.WriteAction{
				{Dst: dir + "/claude.md", Content: []byte("claude")},
			},
			Desired: []string{dir + "/claude.md"},
		},
	}
	p2 := &mockPlanner{
		id: domain.HarnessCline,
		frag: domain.Fragment{
			Writes: []domain.WriteAction{
				{Dst: dir + "/cline.md", Content: []byte("cline")},
			},
			Desired: []string{dir + "/cline.md"},
		},
	}

	plan, err := PlanSync(profile, req, []Planner{p1, p2})
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

	_, err := PlanSync(profile, req, []Planner{planner})
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

	_, err := PlanSync(profile, req, nil)
	if err == nil {
		t.Fatal("expected error for empty HOME, got nil")
	}
	if !strings.Contains(err.Error(), "HOME") {
		t.Errorf("error %q should contain %q", err, "HOME")
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

	plan, err := PlanSync(profile, req, nil)
	if err != nil {
		t.Fatalf("PlanSync: %v", err)
	}

	if !strings.Contains(plan.Ledger, ".config/aipack/ledger/") {
		t.Errorf("Ledger %q should contain %q", plan.Ledger, ".config/aipack/ledger/")
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

	plan, err := PlanSync(profile, req, []Planner{p1})
	if err != nil {
		t.Fatalf("PlanSync: %v", err)
	}

	if !strings.HasPrefix(plan.Ledger, home) {
		t.Errorf("Ledger %q should be under Home %q", plan.Ledger, home)
	}
	want := filepath.Join(home, ".config", "aipack", "ledger", "claudecode.json")
	if plan.Ledger != want {
		t.Errorf("Ledger = %q, want %q", plan.Ledger, want)
	}
}
