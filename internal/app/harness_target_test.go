package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

func TestTargetDirForHarness_GlobalEnvOverrides(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	projectDir := t.TempDir()
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	claudeHome := filepath.Join(t.TempDir(), "claude-home")
	opencodeDir := filepath.Join(t.TempDir(), "opencode-config")

	global := TargetSpec{
		Scope:      domain.ScopeGlobal,
		ProjectDir: projectDir,
		Home:       home,
		Env: map[string]string{
			"CLAUDE_CONFIG_DIR":   claudeHome,
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
	if claudeTarget.Dir != claudeHome || !claudeTarget.IsConfigDir {
		t.Fatalf("claudecode target = %+v, want config directory %q", claudeTarget, claudeHome)
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
	claudePlan := planRequestForTarget(project, "", false, domain.HarnessClaudeCode)
	if claudePlan.TargetDir != projectDir || claudePlan.TargetConfigDir || claudePlan.NativeConfigDir != claudeHome {
		t.Fatalf("project Claude target lost its native config home: %+v", claudePlan)
	}
}

func TestConfigHomeLifecycle(t *testing.T) {
	for _, hid := range []domain.Harness{domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessOpenCode} {
		for _, useHome := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/same-home-%t", hid, useHome), func(t *testing.T) {
				home, project, configDir := t.TempDir(), t.TempDir(), t.TempDir()
				target := filepath.Join(t.TempDir(), "native-config")
				if useHome {
					target = home
				}
				env := map[string]string{"CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "", "OPENCODE_CONFIG_DIR": ""}
				key := map[domain.Harness]string{domain.HarnessClaudeCode: "CLAUDE_CONFIG_DIR", domain.HarnessCodex: "CODEX_HOME", domain.HarnessOpenCode: "OPENCODE_CONFIG_DIR"}[hid]
				env[key] = target
				req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: home, ProjectDir: project, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{hid}, Env: env}, Yes: true, Quiet: true}
				p := testProfile(t, t.TempDir())
				p.Packs[0].Workflows[0].Body = []byte("Deploy to staging.\n")
				p.MCPServers = []domain.MCPServer{{Name: "fixture", Command: []string{"echo"}}}
				eng, reg := engine.New(nil, nil), testRegistry()
				if _, _, err := RunSync(context.Background(), eng, p, req, reg, nil, nil); err != nil {
					t.Fatal(err)
				}
				h, err := reg.Lookup(hid)
				if err != nil {
					t.Fatal(err)
				}
				captured, err := h.Capture(context.Background(), captureContextForHarness(req.TargetSpec, hid, nil))
				wantRules := 2
				if hid == domain.HarnessCodex {
					wantRules = 0 // Codex's flattened rules cannot be reverse-captured.
				}
				if err != nil || len(captured.Rules) != wantRules || len(captured.Skills) != 1 || len(captured.Agents) != 1 || len(captured.Workflows) != 1 || len(captured.MCPServers) != 1 {
					t.Fatalf("custom-home capture: rules=%d skills=%d agents=%d workflows=%d mcp=%d: %v", len(captured.Rules), len(captured.Skills), len(captured.Agents), len(captured.Workflows), len(captured.MCPServers), err)
				}
				if err := RunClean(context.Background(), eng, CleanRequest{TargetSpec: req.TargetSpec, Yes: true}, reg); err != nil {
					t.Fatal(err)
				}
				captured, err = h.Capture(context.Background(), captureContextForHarness(req.TargetSpec, hid, nil))
				if err != nil || len(captured.Rules)+len(captured.Skills)+len(captured.Agents)+len(captured.Workflows)+len(captured.MCPServers) != 0 {
					t.Fatalf("custom-home clean: rules=%d skills=%d agents=%d workflows=%d mcp=%d: %v", len(captured.Rules), len(captured.Skills), len(captured.Agents), len(captured.Workflows), len(captured.MCPServers), err)
				}
				if _, err := os.Stat(filepath.Join(home, ".claude")); hid == domain.HarnessClaudeCode && !os.IsNotExist(err) {
					t.Fatalf("custom-home delivery touched the default Claude home: %v", err)
				}
			})
		}
	}
}
