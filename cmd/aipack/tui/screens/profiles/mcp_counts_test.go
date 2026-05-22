package profiles

import (
	"testing"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
)

func TestComputeMCPCountsUsesInclusiveMapSemantics(t *testing.T) {
	t.Parallel()

	packs := []app.ProfilePackInfo{{
		Index: 0,
		Name:  "tools",
		Root:  "/tmp/tools",
		Manifest: config.PackManifest{
			MCP: []string{"jira", "slack"},
		},
	}}
	cfg := config.ProfileConfig{
		Packs: []config.PackEntry{{
			Name: "tools",
			MCP: map[string]config.MCPServerConfig{
				"slack": {Enabled: config.BoolPtr(true)},
			},
		}},
	}
	probeCache := map[mcpProbeKey]mcpProbeEntry{
		{packRoot: "/tmp/tools", server: "jira"}:  {tools: []string{"get_issue"}},
		{packRoot: "/tmp/tools", server: "slack"}: {tools: []string{"send", "read"}},
	}

	got := computeMCPCountsWithProbeCache(packs, cfg, probeCache)

	if got.TotalAvailable != 2 || got.TotalEnabled != 2 {
		t.Fatalf("totals = %d/%d, want 2/2 for slack only", got.TotalEnabled, got.TotalAvailable)
	}
	jira := got.ByServer[mcpCountsKey{PackIdx: 0, Server: "jira"}]
	if jira.Enabled != 0 || jira.ContributesToTotal {
		t.Fatalf("jira counts = %+v, want disabled and non-contributing", jira)
	}
}

func TestComputeMCPCountsDisabledPackDoesNotContribute(t *testing.T) {
	t.Parallel()

	packs := []app.ProfilePackInfo{{
		Index: 0,
		Name:  "tools",
		Root:  "/tmp/tools",
		Manifest: config.PackManifest{
			MCP: []string{"jira"},
		},
	}}
	cfg := config.ProfileConfig{
		Packs: []config.PackEntry{{
			Name:    "tools",
			Enabled: config.BoolPtr(false),
		}},
	}
	probeCache := map[mcpProbeKey]mcpProbeEntry{
		{packRoot: "/tmp/tools", server: "jira"}: {tools: []string{"get_issue"}},
	}

	got := computeMCPCountsWithProbeCache(packs, cfg, probeCache)

	if got.TotalAvailable != 0 || got.TotalEnabled != 0 {
		t.Fatalf("totals = %d/%d, want 0/0 for disabled pack", got.TotalEnabled, got.TotalAvailable)
	}
	jira := got.ByServer[mcpCountsKey{PackIdx: 0, Server: "jira"}]
	if jira.Enabled != 0 || jira.ContributesToTotal {
		t.Fatalf("jira counts = %+v, want disabled and non-contributing", jira)
	}
}
