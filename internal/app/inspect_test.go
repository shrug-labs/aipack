package app

import (
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

	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "claudecode")
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

	result, err := InspectHarness(InspectRequest{
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

	ledgerPath := engine.LedgerPathForScope(domain.ScopeProject, projectDir, home, "claudecode")
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

	result, err := InspectHarness(InspectRequest{
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
