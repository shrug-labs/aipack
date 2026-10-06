package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/plugin"
	"github.com/shrug-labs/aipack/internal/util"
)

func TestPortableOpenCodeScopes(t *testing.T) {
	for _, format := range []string{plugin.CodexLegacy, plugin.AgentPlugins} {
		for _, mode := range []string{"default", "custom"} {
			t.Run(format+"/"+mode, func(t *testing.T) {
				root, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = util.RemoveOwnedTree(root) })
				source, cfgDir, home, project := filepath.Join(root, "source"), filepath.Join(root, "config"), filepath.Join(root, "home"), filepath.Join(root, "project")
				global := filepath.Join(home, ".config/opencode")
				env := map[string]string{"OPENCODE_CONFIG_DIR": ""}
				if mode == "custom" {
					global, env["OPENCODE_CONFIG_DIR"] = filepath.Join(root, "native"), filepath.Join(root, "native")
				}
				manifest, mcp := ".codex-plugin/plugin.json", ".mcp.json"
				body, server := `{"name":"probe","version":"1.0.0"}`, `{"mcpServers":{"server":{"command":"false","cwd":"."}}}`
				if format == plugin.AgentPlugins {
					manifest, mcp = "plugin.json", "mcp.json"
					body = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`
					server = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"server":{"type":"stdio","command":"false"}}}`
				}
				writeFile(t, filepath.Join(source, manifest), body)
				writeFile(t, filepath.Join(source, mcp), server)
				writeFile(t, filepath.Join(source, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Owned scope fixture\n---\nSCOPE_BODY\n")
				if err := PackInstall(context.Background(), PackInstallRequest{PackPath: source, ConfigDir: cfgDir, Name: "probe", Plugin: &domain.PluginSource{Format: format, Name: "probe", Marketplace: "scope"}}, nil); err != nil {
					t.Fatal(err)
				}
				eng := engine.New(nil, nil)
				nativePolicy := map[string]config.MCPServerConfig{"server": {DisabledTools: []string{"hidden"}}}
				cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe", MCP: nativePolicy}}}
				resolve := func() domain.Profile {
					t.Helper()
					p, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
					if err != nil {
						t.Fatal(err)
					}
					return p
				}
				globalSpec := TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: project, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessOpenCode}, Env: env}
				projectSpec := globalSpec
				projectSpec.Scope = domain.ScopeProject
				run := func(spec TargetSpec) SyncResult {
					t.Helper()
					result, _, err := RunSync(context.Background(), eng, resolve(), SyncRequest{TargetSpec: spec, Yes: true, Quiet: true}, testRegistry(), nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					for _, action := range result.Plan.Writes {
						if action.Delivery != nil {
							writeFile(t, filepath.Join(action.Delivery.DataDir, "retained.txt"), "retained scope data")
						}
					}
					return result
				}
				g, p := run(globalSpec), run(projectSpec)
				payload := func(result SyncResult) string {
					for _, action := range result.Plan.Writes {
						if action.Delivery != nil {
							return action.Dst
						}
					}
					t.Fatal("missing scoped payload")
					return ""
				}
				globalSettings, projectSettings := filepath.Join(global, "opencode.json"), filepath.Join(project, ".opencode/opencode.json")
				var native func() map[string]any
				if os.Getenv("AIPACK_TEST_OPENCODE_NATIVE") == "1" {
					proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
					t.Cleanup(proxy.Close)
					native = func() map[string]any {
						t.Helper()
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						cmd := exec.CommandContext(ctx, "opencode", "debug", "config")
						cmd.Dir, cmd.WaitDelay = project, time.Second
						cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_DATA_HOME=" + filepath.Join(home, ".local/share"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "XDG_STATE_HOME=" + filepath.Join(home, ".state"), "OPENCODE_CONFIG_DIR=" + env["OPENCODE_CONFIG_DIR"], "OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_DISABLE_MODELS_FETCH=1", "OPENCODE_DISABLE_EXTERNAL_SKILLS=1", "HTTP_PROXY=" + proxy.URL, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=127.0.0.1,localhost"}
						out, err := cmd.Output()
						if err != nil {
							t.Fatalf("native combined config: %s %v", out, err)
						}
						value := map[string]any{}
						if err := json.Unmarshal(out, &value); err != nil {
							t.Fatalf("native combined config: %s %v", out, err)
						}
						servers, _ := value["mcp"].(map[string]any)
						return servers
					}
					// Native omission inherits the globally declared server. An
					// independent project profile cannot promise to exclude it.
					before := mustRead(t, projectSettings)
					control := map[string]any{}
					_ = json.Unmarshal(before, &control)
					delete(control["mcp"].(map[string]any), "probe@scope:server")
					encoded, _ := json.Marshal(control)
					writeFile(t, projectSettings, string(encoded))
					inherited, _ := native()["probe@scope:server"].(map[string]any)
					inheritedCwd, _ := inherited["cwd"].(string)
					if filepath.Clean(inheritedCwd) != payload(g) {
						t.Fatalf("native omission did not inherit the global server: %+v", inherited)
					}
					writeFile(t, projectSettings, string(before))
					winner, _ := native()["probe@scope:server"].(map[string]any)
					expected := payload(p)
					if mode == "custom" {
						expected = payload(g)
					}
					winnerCwd, _ := winner["cwd"].(string)
					if filepath.Clean(winnerCwd) != expected {
						t.Fatalf("native scoped precedence: %+v expected cwd %s", winner, expected)
					}
				}
				snapshots := map[string][]byte{}
				for _, path := range []string{globalSettings, projectSettings, g.Plan.Ledger, p.Plan.Ledger} {
					snapshots[path] = mustRead(t, path)
				}
				off := false
				cfg.Packs[0].MCP = map[string]config.MCPServerConfig{"server": {Enabled: &off}}
				for _, dry := range []bool{true, false} {
					if _, _, err := RunSync(context.Background(), eng, resolve(), SyncRequest{TargetSpec: projectSpec, Yes: true, Quiet: true, DryRun: dry}, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "outside this scope") {
						t.Fatalf("conflicting scopes accepted (dry=%v): %v", dry, err)
					}
					for path, before := range snapshots {
						if !bytes.Equal(before, mustRead(t, path)) {
							t.Fatalf("refusal changed %s", path)
						}
					}
				}
				legacy, _, err := eng.LoadLedger(g.Plan.Ledger)
				if err != nil {
					t.Fatal(err)
				}
				for path, entry := range legacy.Managed {
					if entry.Delivery != nil {
						entry.Delivery.Home = ""
						legacy.Managed[path] = entry
					}
				}
				if err := eng.SaveLedger(g.Plan.Ledger, legacy, false); err != nil {
					t.Fatal(err)
				}
				for _, dry := range []bool{true, false} {
					if _, _, err := RunSync(context.Background(), eng, resolve(), SyncRequest{TargetSpec: projectSpec, Yes: true, Quiet: true, DryRun: dry}, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "outside this scope") {
						t.Fatalf("unknown legacy scope accepted (dry=%v): %v", dry, err)
					}
				}
				writeFile(t, g.Plan.Ledger, string(snapshots[g.Plan.Ledger]))
				if err := RunClean(context.Background(), eng, CleanRequest{TargetSpec: projectSpec, Yes: true, Stderr: io.Discard}, testRegistry()); err != nil {
					t.Fatal(err)
				}
				run(globalSpec)
				if native != nil && native()["probe@scope:server"] != nil {
					t.Fatal("single-owner exclusion left a native server active")
				}
				cfg.Packs[0].MCP = nativePolicy
				run(globalSpec)
				run(projectSpec)
				if err := RunClean(context.Background(), eng, CleanRequest{TargetSpec: globalSpec, Yes: true, Stderr: io.Discard}, testRegistry()); err != nil {
					t.Fatal(err)
				}
				if native != nil && native()["probe@scope:server"] == nil {
					t.Fatal("global clean removed project activation")
				}
				if summary, err := PlanWithDiffs(context.Background(), eng, resolve(), SyncRequest{TargetSpec: projectSpec}, testRegistry()); err != nil || summary.TotalChanges() != 0 {
					t.Fatalf("remaining owner did not converge: %+v %v", summary, err)
				}
				otherSpec := projectSpec
				otherSpec.ProjectDir = filepath.Join(root, "other-project")
				cfg.Packs[0].MCP = map[string]config.MCPServerConfig{"server": {Enabled: &off}}
				run(otherSpec)
				if native != nil {
					if native()["probe@scope:server"] == nil {
						t.Fatal("independent project changed the first activation")
					}
					firstProject := project
					project = otherSpec.ProjectDir
					if native()["probe@scope:server"] != nil {
						t.Fatal("independent project inherited the first activation")
					}
					project = firstProject
				}
				if err := RunClean(context.Background(), eng, CleanRequest{TargetSpec: otherSpec, Yes: true, Stderr: io.Discard}, testRegistry()); err != nil {
					t.Fatal(err)
				}
				cfg.Packs[0].MCP = nativePolicy
				for _, base := range []string{global, filepath.Join(project, ".opencode")} {
					if string(mustRead(t, filepath.Join(base, "aipack-data/probe@scope/retained.txt"))) != "retained scope data" {
						t.Fatal("scope cleanup lost retained data")
					}
				}
			})
		}
	}
}
