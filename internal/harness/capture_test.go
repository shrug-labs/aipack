package harness_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
)

func TestCaptureContentDir_Basic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "alpha.md"), []byte("alpha-body"), 0o644)
	os.WriteFile(filepath.Join(dir, "beta.md"), []byte("beta-body"), 0o644)
	os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("not md"), 0o644)
	os.Mkdir(filepath.Join(dir, "subdir.md"), 0o755) // directory, should be skipped

	var parsed []string
	copies, warnings := harness.CaptureContentDir(dir, "rules", ".md",
		func(raw []byte, name, srcPath string) error {
			parsed = append(parsed, name)
			return nil
		})

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(copies) != 2 {
		t.Fatalf("expected 2 copies, got %d", len(copies))
	}
	// Verify flat Dst paths.
	for _, c := range copies {
		if c.Kind != domain.CopyKindFile {
			t.Fatalf("expected CopyKindFile, got %v", c.Kind)
		}
		base := filepath.Base(c.Dst)
		if filepath.Dir(c.Dst) != "rules" {
			t.Fatalf("expected Dst dir 'rules', got %q", c.Dst)
		}
		if base != "alpha.md" && base != "beta.md" {
			t.Fatalf("unexpected Dst filename %q", base)
		}
	}
	if len(parsed) != 2 {
		t.Fatalf("expected 2 parsed items, got %d", len(parsed))
	}
}

func TestCaptureContentDir_ParseError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad.md"), []byte("bad"), 0o644)
	os.WriteFile(filepath.Join(dir, "good.md"), []byte("good"), 0o644)

	var parsed []string
	copies, warnings := harness.CaptureContentDir(dir, "agents", ".md",
		func(raw []byte, name, srcPath string) error {
			if name == "bad" {
				return fmt.Errorf("invalid content")
			}
			parsed = append(parsed, name)
			return nil
		})

	if len(copies) != 2 {
		t.Fatalf("expected 2 copies (both files), got %d", len(copies))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if len(parsed) != 1 || parsed[0] != "good" {
		t.Fatalf("expected only 'good' parsed, got %v", parsed)
	}
}

func TestCaptureContentDir_NonexistentDir(t *testing.T) {
	t.Parallel()
	copies, warnings := harness.CaptureContentDir("/nonexistent/path", "rules", ".md",
		func(raw []byte, name, srcPath string) error { return nil })

	if len(copies) != 0 {
		t.Fatalf("expected 0 copies, got %d", len(copies))
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for nonexistent dir, got %v", warnings)
	}
}

func TestCaptureContentDir_CustomExt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "rule-a.rules"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "skip.md"), []byte("b"), 0o644)

	var parsed []string
	copies, _ := harness.CaptureContentDir(dir, "rules", ".rules",
		func(raw []byte, name, srcPath string) error {
			parsed = append(parsed, name)
			return nil
		})

	if len(copies) != 1 {
		t.Fatalf("expected 1 copy, got %d", len(copies))
	}
	if len(parsed) != 1 || parsed[0] != "rule-a" {
		t.Fatalf("expected 'rule-a' parsed, got %v", parsed)
	}
}
