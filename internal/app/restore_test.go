package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

func TestRunRestore_RestoresAllHarnesses(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	claudePath := filepath.Join(home, ".claude", "settings.local.json")
	codexPath := filepath.Join(home, ".codex", "config.toml")
	for _, path := range []string{claudePath, codexPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("current"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := engine.SnapshotSettingsFiles([]domain.SettingsAction{{
		Dst:     claudePath,
		Desired: []byte("managed"),
		Harness: domain.HarnessClaudeCode,
	}}, engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, string(domain.HarnessClaudeCode)), false); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.SnapshotSettingsFiles([]domain.SettingsAction{{
		Dst:     codexPath,
		Desired: []byte("managed"),
		Harness: domain.HarnessCodex,
	}}, engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, string(domain.HarnessCodex)), false); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{claudePath, codexPath} {
		if err := os.WriteFile(path, []byte("overwritten"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := RunRestore(RestoreRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Harnesses:  []domain.Harness{domain.HarnessClaudeCode, domain.HarnessCodex},
			Home:       home,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RestoredFiles) != 2 {
		t.Fatalf("expected 2 restored files, got %d", len(result.RestoredFiles))
	}

	for _, tc := range []struct {
		path string
		want string
	}{
		{path: claudePath, want: "current"},
		{path: codexPath, want: "current"},
	} {
		got, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Fatalf("%s = %q, want %q", tc.path, got, tc.want)
		}
	}
}
