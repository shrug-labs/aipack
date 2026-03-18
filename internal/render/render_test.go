package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestValidateOutDir_RejectsExactPackRoot(t *testing.T) {
	t.Parallel()
	packRoot := t.TempDir()
	profile := domain.Profile{
		Packs: []domain.Pack{{Root: packRoot}},
	}
	err := validateOutDir(profile, packRoot)
	if err == nil {
		t.Fatal("expected error when outDir equals pack root")
	}
}

func TestValidateOutDir_RejectsSubdirOfPackRoot(t *testing.T) {
	t.Parallel()
	packRoot := t.TempDir()
	subdir := filepath.Join(packRoot, "output")
	profile := domain.Profile{
		Packs: []domain.Pack{{Root: packRoot}},
	}
	err := validateOutDir(profile, subdir)
	if err == nil {
		t.Fatal("expected error when outDir is under pack root")
	}
}

func TestValidateOutDir_AllowsSiblingDir(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	packRoot := filepath.Join(parent, "pack-source")
	outDir := filepath.Join(parent, "render-output")
	if err := os.MkdirAll(packRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	profile := domain.Profile{
		Packs: []domain.Pack{{Root: packRoot}},
	}
	if err := validateOutDir(profile, outDir); err != nil {
		t.Fatalf("sibling dir should be allowed: %v", err)
	}
}

func TestValidateOutDir_AllowsParentOfPackRoot(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	packRoot := filepath.Join(parent, "nested", "pack")
	if err := os.MkdirAll(packRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	profile := domain.Profile{
		Packs: []domain.Pack{{Root: packRoot}},
	}
	if err := validateOutDir(profile, parent); err != nil {
		t.Fatalf("parent of pack root should be allowed: %v", err)
	}
}

func TestValidateOutDir_RejectsPackRootWithTrailingSlash(t *testing.T) {
	t.Parallel()
	packRoot := t.TempDir()
	profile := domain.Profile{
		Packs: []domain.Pack{{Root: packRoot}},
	}
	// filepath.Clean normalizes trailing slashes, so this should still reject.
	err := validateOutDir(profile, packRoot+"/")
	if err == nil {
		t.Fatal("expected error when outDir is pack root with trailing slash")
	}
}
