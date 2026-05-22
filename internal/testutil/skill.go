package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

// SkillDir creates a minimal skill directory with SKILL.md.
func SkillDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, domain.SkillEntryFile), []byte("---\nname: "+name+"\ndescription: Test skill\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
