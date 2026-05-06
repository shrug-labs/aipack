package tui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestSavedFilePlanOps_ExpandsSkillDirectoryFiles(t *testing.T) {
	t.Parallel()
	harnessDir := filepath.Join(t.TempDir(), "deploy")
	if err := os.MkdirAll(harnessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		domain.SkillEntryFile: "# Deploy\n",
		"notes.md":            "notes\n",
	} {
		if err := os.WriteFile(filepath.Join(harnessDir, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	packDir := filepath.Join(t.TempDir(), "pack", "skills", "deploy")
	ops, err := savedFilePlanOps([]app.SavedFile{{
		HarnessPath: harnessDir,
		PackName:    "team-pack",
		PackPath:    packDir,
	}})
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, op := range ops {
		if op.Kind != app.PlanOpSkill {
			t.Fatalf("op kind = %q, want skill", op.Kind)
		}
		if op.SourcePack != "team-pack" {
			t.Fatalf("SourcePack = %q, want team-pack", op.SourcePack)
		}
		got = append(got, filepath.Base(op.Dst))
	}
	slices.Sort(got)
	want := []string{domain.SkillEntryFile, "notes.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("expanded files = %v, want %v", got, want)
	}
}
