package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/plugin"
)

func TestProfilePluginSelectionsSharedAcrossHarnesses(t *testing.T) {
	src, dir, home, project := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, ".codex-plugin/plugin.json"), `{"name":"probe","version":"1.0.0"}`)
	writeFile(t, filepath.Join(src, ".mcp.json"), `{"mcpServers":{"probe":{"command":"false","cwd":".","startup_timeout_sec":60}}}`)
	writeFile(t, filepath.Join(src, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Owned target fixture\n---\nversion one\n")
	writeFile(t, filepath.Join(src, "hooks/hooks.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"true"}]}]}}`)
	if err := PackInstall(context.Background(), PackInstallRequest{ConfigDir: dir, PackPath: src, Name: "alias", Plugin: &domain.PluginSource{Format: plugin.CodexLegacy, Name: "probe", Marketplace: "owned"}}, nil); err != nil {
		t.Fatal(err)
	}
	cfg := config.ProfileConfig{SchemaVersion: config.ProfileSchemaVersion, Packs: []config.PackEntry{{Name: "alias"}}}
	writeProfileContentProfile(t, dir, "default", cfg)
	eng := engine.New(nil, nil)
	for _, v := range []struct {
		id  string
		cat domain.PackCategory
	}{{"probe", domain.CategoryMCP}, {"codex-stop", domain.CategoryHooks}} {
		if _, err := ProfileContentExclude(ProfileContentRequest{ConfigDir: dir, ProfileName: "default", PackName: "alias", IDs: []string{v.id}, Kind: v.cat}); err != nil {
			t.Fatal(err)
		}
	}
	cfg = loadProfileContentProfile(t, dir, "default")
	resolve := func() domain.Profile {
		t.Helper()
		p, _, err := eng.Resolve(cfg, "", dir, config.CollisionError, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := resolve()
	for _, h := range domain.AllHarnesses() {
		effective, _, err := engine.ProfileForHarness(p, h, engine.PlanRequest{ConfigDir: dir, Home: home, ProjectDir: project, Scope: domain.ScopeGlobal})
		if err != nil {
			t.Fatal(err)
		}
		pack := effective.Packs[0]
		if h == domain.HarnessCodex {
			if pack.NativePlugin == nil || len(pack.NativePlugin.Selected[domain.CategorySkills]) != 1 || len(pack.NativePlugin.Selected[domain.CategoryHooks]) != 0 || len(pack.NativePlugin.Selected[domain.CategoryMCP]) != 0 {
				t.Fatal("native delivery ignored shared selection")
			}
		} else if pack.NativePlugin != nil || len(pack.Skills) != 1 || len(pack.Hooks) != 0 || len(effective.MCPServers) != 0 {
			t.Fatalf("target %s not projected: %+v", h, effective)
		}
	}
	req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: dir, Home: home, ProjectDir: project, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessCline, domain.HarnessClaudeCode, domain.HarnessOpenCode}}, Yes: true, Quiet: true}
	req.DryRun = true
	if _, _, err := RunSyncEach(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil {
		t.Fatal(err)
	}
	req.DryRun = false
	if _, _, err := RunSyncEach(context.Background(), eng, p, req, testRegistry(), nil, nil); err != nil {
		t.Fatal(err)
	}
	profileBytes, err := os.ReadFile(filepath.Join(dir, "profiles/default.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Owned target fixture\n---\nversion two\n")
	if _, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: dir, Name: "alias"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(profileBytes, mustRead(t, filepath.Join(dir, "profiles/default.yaml"))) {
		t.Fatal("update changed profile choices")
	}
	p = resolve()
	results, _, err := RunSyncEach(context.Background(), eng, p, req, testRegistry(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	priorLedgers := map[string][]byte{}
	for _, result := range results {
		priorLedgers[result.Plan.Ledger] = mustRead(t, result.Plan.Ledger)
	}
	for _, h := range req.Harnesses {
		target := req.TargetSpec
		target.Harnesses = []domain.Harness{h}
		trace, err := RunTrace(context.Background(), eng, p, TraceRequest{TargetSpec: target, ProfileName: "default", ProfileConfig: cfg, ResourceType: "skill", ResourceName: "probe"}, testRegistry())
		if err != nil || !trace.Found || len(trace.Destinations) != 1 || trace.Source.NativeBinding != "probe@owned" {
			t.Fatalf("target trace %s: %+v %v", h, trace, err)
		}
	}
	// Restoring an unsupported server must fail before any requested target writes.
	if _, err := ProfileContentInclude(ProfileContentRequest{ConfigDir: dir, ProfileName: "default", IDs: []string{"probe"}, Kind: domain.CategoryMCP}); err != nil {
		t.Fatal(err)
	}
	cfg = loadProfileContentProfile(t, dir, "default")
	req.DryRun = false
	server := cfg.Packs[0].MCP["probe"]
	server.StartupTimeout = "strict"
	cfg.Packs[0].MCP["probe"] = server
	if _, _, err := RunSyncEach(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err == nil {
		t.Fatal("unsupported target selection accepted")
	}
	for path, prior := range priorLedgers {
		if !reflect.DeepEqual(prior, mustRead(t, path)) {
			t.Fatal("preflight failure changed delivery ledger")
		}
	}
}
