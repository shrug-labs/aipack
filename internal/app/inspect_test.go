package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

func TestInspectHarness_UsesPerServerMCPPath(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	configPath := filepath.Join(projectDir, ".mcp.json")
	settingsPath := filepath.Join(projectDir, ".claude", "settings.local.json")

	server := domain.MCPServer{
		Name:      "jira",
		Transport: domain.TransportStdio,
		Command:   []string{"uvx", "jira-mcp"},
	}
	content, err := domain.MCPTrackedBytes(server)
	if err != nil {
		t.Fatalf("MCPTrackedBytes: %v", err)
	}

	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		domain.MCPLedgerKey(configPath, "jira"): {
			SourcePack: "test-pack",
			Digest:     domain.SingleFileDigest(content),
		},
	})

	stub := pipelineStub{
		id: "claudecode",
		capture: harness.CaptureResult{
			Writes: []domain.WriteAction{{
				Dst:     filepath.Join("configs", "claudecode", "settings.local.json"),
				Src:     settingsPath,
				Content: []byte(`{"permissions":{"allow":["mcp__jira__get_issue"]}}`),
			}},
			MCP: []domain.CapturedMCP{{
				Server:      server,
				HarnessPath: configPath,
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	result, err := InspectHarness(context.Background(), InspectRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Home:       home,
			Harnesses:  []domain.Harness{"claudecode"},
		},
		PackRoots: map[string]string{"test-pack": filepath.Join(home, "packs", "test-pack")},
	}, reg)
	if err != nil {
		t.Fatalf("InspectHarness: %v", err)
	}

	if len(result.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(result.Files))
	}
	for _, file := range result.Files {
		if file.Category != domain.CategoryMCP {
			continue
		}
		if file.HarnessPath != configPath {
			t.Fatalf("HarnessPath = %q, want %q", file.HarnessPath, configPath)
		}
		if file.State != FileClean {
			t.Fatalf("State = %v, want FileClean", file.State)
		}
		return
	}
	t.Fatal("expected MCP file in inspect result")
}

func TestInspectHarness_MCPMetadataDoesNotCauseConflictAfterSync(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	projectDir := filepath.Join(home, "project")
	packRoot := filepath.Join(home, "packs", "test-pack")
	configPath := filepath.Join(projectDir, ".mcp.json")
	settingsPath := filepath.Join(projectDir, ".claude", "settings.local.json")
	packPath := filepath.Join(packRoot, "mcp", "jira.json")

	server := domain.MCPServer{
		Name:           "jira",
		Transport:      domain.TransportStdio,
		Command:        []string{"uvx", "jira-mcp"},
		AvailableTools: []string{"get_issue"},
		Links:          []string{"https://example.invalid/jira"},
		Auth:           "oauth",
		Notes:          "metadata only",
	}
	content, err := domain.MCPTrackedBytes(server)
	if err != nil {
		t.Fatalf("MCPTrackedBytes: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(packPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packPath, []byte("{\n  \"name\": \"jira\",\n  \"transport\": \"stdio\",\n  \"command\": [\"uvx\", \"jira-mcp\"],\n  \"available_tools\": [\"get_issue\"],\n  \"links\": [\"https://example.invalid/jira\"],\n  \"auth\": \"oauth\",\n  \"notes\": \"metadata only\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, domain.HarnessClaudeCode)
	writeLedger(t, ledgerPath, map[string]domain.Entry{
		domain.MCPLedgerKey(configPath, "jira"): {
			SourcePack: "test-pack",
			Digest:     domain.SingleFileDigest(content),
		},
	})

	stub := pipelineStub{
		id: "claudecode",
		capture: harness.CaptureResult{
			Writes: []domain.WriteAction{{
				Dst:     filepath.Join("configs", "claudecode", "settings.local.json"),
				Src:     settingsPath,
				Content: []byte(`{"permissions":{"allow":["mcp__jira__get_issue"]}}`),
			}},
			MCP: []domain.CapturedMCP{{
				Server: domain.MCPServer{
					Name:      "jira",
					Transport: domain.TransportStdio,
					Command:   []string{"uvx", "jira-mcp"},
				},
				HarnessPath: configPath,
			}},
		},
	}
	reg := harness.NewRegistry(stub)

	result, err := InspectHarness(context.Background(), InspectRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: projectDir,
			Home:       home,
			Harnesses:  []domain.Harness{"claudecode"},
		},
		PackRoots: map[string]string{"test-pack": packRoot},
	}, reg)
	if err != nil {
		t.Fatalf("InspectHarness: %v", err)
	}

	for _, file := range result.Files {
		if file.Category == domain.CategoryMCP {
			if file.State != FileClean {
				t.Fatalf("State = %v, want FileClean", file.State)
			}
			return
		}
	}
	t.Fatal("expected MCP file in inspect result")
}

func TestRelPathFromDst(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cat  domain.PackCategory
		dst  string
		kind domain.CopyKind
		want string
	}{
		{
			name: "rule file strips extension",
			cat:  domain.CategoryRules, dst: "rules/triage.md",
			kind: domain.CopyKindFile, want: "triage",
		},
		{
			name: "skill directory returns dir name",
			cat:  domain.CategorySkills, dst: "skills/agent-configuration",
			kind: domain.CopyKindDir, want: "agent-configuration",
		},
		{
			name: "promoted skill SKILL.md returns parent dir name",
			cat:  domain.CategorySkills, dst: "skills/execute-plan/SKILL.md",
			kind: domain.CopyKindFile, want: "execute-plan",
		},
		{
			name: "settings returns basename as-is",
			cat:  domain.CategorySettings, dst: "settings.json",
			kind: domain.CopyKindFile, want: "settings.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := relPathFromDst(tt.cat, tt.dst, tt.kind)
			if got != tt.want {
				t.Errorf("relPathFromDst(%q, %q, %v) = %q, want %q",
					tt.cat, tt.dst, tt.kind, got, tt.want)
			}
		})
	}
}
