package app

import (
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestTargetDirForHarness_GlobalEnvOverrides(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	projectDir := t.TempDir()
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	opencodeDir := filepath.Join(t.TempDir(), "opencode-config")

	global := TargetSpec{
		Scope:      domain.ScopeGlobal,
		ProjectDir: projectDir,
		Home:       home,
		Env: map[string]string{
			"CODEX_HOME":          codexHome,
			"OPENCODE_CONFIG_DIR": opencodeDir,
		},
	}
	codexTarget := targetForHarness(global, domain.HarnessCodex)
	if codexTarget.Dir != codexHome {
		t.Fatalf("codex target = %q, want %q", codexTarget.Dir, codexHome)
	}
	if !codexTarget.IsConfigDir {
		t.Fatal("codex target should be marked as config directory")
	}
	opencodeTarget := targetForHarness(global, domain.HarnessOpenCode)
	if opencodeTarget.Dir != opencodeDir {
		t.Fatalf("opencode target = %q, want %q", opencodeTarget.Dir, opencodeDir)
	}
	if !opencodeTarget.IsConfigDir {
		t.Fatal("opencode target should be marked as config directory")
	}
	claudeTarget := targetForHarness(global, domain.HarnessClaudeCode)
	if claudeTarget.Dir != home {
		t.Fatalf("claudecode target = %q, want %q", claudeTarget.Dir, home)
	}

	project := global
	project.Scope = domain.ScopeProject
	projectTarget := targetForHarness(project, domain.HarnessCodex)
	if projectTarget.Dir != projectDir {
		t.Fatalf("project codex target = %q, want %q", projectTarget.Dir, projectDir)
	}
	if projectTarget.IsConfigDir {
		t.Fatal("project codex target should not be marked as config directory")
	}
}
