package app

import (
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

func TestInspectHarness_ClassifiesPromotedContentWritesAsAgents(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	skillFile := filepath.Join(projectDir, ".agents", "skills", "demo-agent", "SKILL.md")
	packRoot := filepath.Join(home, "packs", "test-pack")
	sourceDigest := domain.SingleFileDigest([]byte("promoted skill"))

	writeLedger(t, engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "codex"), map[string]domain.Entry{
		skillFile: {SourcePack: "test-pack", Digest: sourceDigest},
	})

	reg := harness.NewRegistry(pipelineStub{
		id: "codex",
		capture: harness.CaptureResult{
			Writes: []domain.WriteAction{{
				Src:          skillFile,
				Dst:          filepath.Join("agents", "demo-agent.md"),
				Content:      []byte("---\nname: demo-agent\n---\nbody\n"),
				IsContent:    true,
				SourceDigest: sourceDigest,
			}},
		},
	})

	result, err := InspectHarness(InspectRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Home:       home,
			Harnesses:  []domain.Harness{"codex"},
		},
		PackRoots: map[string]string{"test-pack": packRoot},
	}, reg)
	if err != nil {
		t.Fatalf("InspectHarness: %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result.Files))
	}
	if result.Files[0].Category != domain.CategoryAgents {
		t.Fatalf("category = %q, want %q", result.Files[0].Category, domain.CategoryAgents)
	}
	if result.Files[0].State != FileClean {
		t.Fatalf("state = %v, want %v", result.Files[0].State, FileClean)
	}
}
