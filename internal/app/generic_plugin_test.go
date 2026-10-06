package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/plugin"
)

func TestGenericDeliveryRetainsTargetData(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("foreign stdio MCP delivery requires POSIX")
	}
	for _, hid := range []domain.Harness{domain.HarnessOpenCode, domain.HarnessClaudeCode} {
		if hid == domain.HarnessClaudeCode && os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
			continue
		}
		for _, format := range []string{plugin.CodexLegacy, plugin.AgentPlugins} {
			t.Run(string(hid)+"/"+format, func(t *testing.T) {
				source, cfgDir, home, project := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
				nativeHome := filepath.Join(home, "native")
				manifest, mcp, variable := ".codex-plugin/plugin.json", ".mcp.json", "${CODEX_PLUGIN_DATA}"
				body, servers := `{"name":"probe","version":"1.0.0"}`, `{"mcpServers":{"probe":{"command":"false","cwd":"."}}}`
				if format == plugin.AgentPlugins {
					manifest, mcp, variable = "plugin.json", "mcp.json", "${PLUGIN_DATA}"
					body = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`
					servers = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"probe":{"type":"stdio","command":"false"}}}`
				}
				writeFile(t, filepath.Join(source, manifest), body)
				writeFile(t, filepath.Join(source, mcp), servers)
				writeFile(t, filepath.Join(source, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Owned data continuity fixture\n---\nRead "+variable+"/retained.txt.\n")
				writeFile(t, filepath.Join(source, "skills/probe/asset.txt"), "owned skill asset")
				if err := PackInstall(context.Background(), PackInstallRequest{PackPath: source, ConfigDir: cfgDir, Name: "alias", Plugin: &domain.PluginSource{Format: format, Name: "probe", Marketplace: "owned"}}, nil); err != nil {
					t.Fatal(err)
				}
				eng := engine.New(nil, nil)
				nativePolicy := map[string]config.MCPServerConfig{"probe": {DisabledTools: []string{"hidden"}}}
				cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "alias", MCP: nativePolicy}}}
				req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: project, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{hid}, Env: map[string]string{"CLAUDE_CONFIG_DIR": nativeHome, "OPENCODE_CONFIG_DIR": nativeHome}}, Yes: true, Quiet: true}
				resolve := func() domain.Profile {
					t.Helper()
					p, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
					if err != nil {
						t.Fatal(err)
					}
					return p
				}
				run := func() SyncResult {
					t.Helper()
					result, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					return result
				}
				result := run()
				interrupt := func(after bool) {
					t.Helper()
					eng.FS = nativeLedgerInterruption{Path: result.Plan.Ledger, After: after}
					func() {
						defer func() {
							if got := recover(); got != "native ledger interruption" {
								t.Fatalf("expected interrupted transition, got %v", got)
							}
						}()
						run()
					}()
					eng.FS = engine.OSFS{}
					req.DryRun = true
					if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "pending") {
						t.Fatalf("preview accepted an interrupted transition: %v", err)
					}
					req.DryRun = false
					if err := recoverNativeOperation(cfgDir); err != nil {
						t.Fatal(err)
					}
				}
				data := filepath.Join(nativeHome, "aipack-data/probe@owned")
				if hid == domain.HarnessClaudeCode {
					data = filepath.Join(nativeHome, "plugins/data/probe-owned")
				}
				writeFile(t, filepath.Join(data, "retained.txt"), "retained target data")
				activationPath := filepath.Join(nativeHome, "opencode.json")
				if hid == domain.HarnessClaudeCode {
					activationPath = filepath.Join(nativeHome, "settings.json")
				}
				activation := func() map[string]any {
					t.Helper()
					state, err := readNativeConfig(activationPath)
					if err != nil {
						t.Fatal(err)
					}
					// Native removal can omit an empty marketplace section.
					if entries, ok := state["extraKnownMarketplaces"].(map[string]any); ok && len(entries) == 0 {
						delete(state, "extraKnownMarketplaces")
					}
					return state
				}
				disabled := false
				cfg.Packs[0].MCP = map[string]config.MCPServerConfig{"probe": {Enabled: &disabled}}
				toggled := run()
				var ordinaryPath string
				for _, action := range toggled.Plan.Writes {
					if action.Category == domain.CategorySkills && action.Delivery == nil {
						ordinaryPath = action.Dst
					}
				}
				if ordinaryPath == "" || len(toggled.Plan.NativePlugins) != 0 || !bytes.Contains(mustRead(t, ordinaryPath), []byte(filepath.Join(data, "retained.txt"))) {
					t.Fatal("MCP exclusion did not convert selected skills with retained data")
				}
				cfg.Packs[0].MCP = nativePolicy
				result = run()
				if _, err := os.Stat(ordinaryPath); !os.IsNotExist(err) {
					t.Fatal("MCP re-enable retained duplicate ordinary skills", err)
				}
				priorActivation := activation()
				priorLedger := mustRead(t, result.Plan.Ledger)
				if err := os.Remove(filepath.Join(source, mcp)); err != nil {
					t.Fatal(err)
				}
				cfg.Packs[0].MCP = nil
				if _, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: cfgDir, Name: "alias"}, nil, nil); err != nil {
					t.Fatal(err)
				}
				req.DryRun = true
				preview := run()
				var skill domain.WriteAction
				for _, action := range preview.Plan.Writes {
					if action.Category == domain.CategorySkills && action.SourcePack == "alias" {
						skill = action
					}
				}
				if skill.Dst == "" || !bytes.Contains(skill.Content, []byte(filepath.Join(data, "retained.txt"))) {
					t.Fatalf("ordinary conversion stopped addressing existing target data: %s", skill.Content)
				}
				if !bytes.Equal(priorLedger, mustRead(t, result.Plan.Ledger)) || string(mustRead(t, filepath.Join(data, "retained.txt"))) != "retained target data" {
					t.Fatal("preview changed prior ownership or runtime data")
				}
				req.DryRun = false
				failing := engine.New(nativeLedgerFailure{Path: result.Plan.Ledger}, nil)
				if _, _, err := RunSync(context.Background(), failing, resolve(), req, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "injected native ledger") {
					t.Fatalf("transition did not reach injected persistence failure: %v", err)
				}
				if _, err := os.Stat(skill.Dst); !os.IsNotExist(err) {
					t.Fatalf("failed transition left an ordinary skill active beside restored native delivery: %v", err)
				}
				if !bytes.Equal(priorLedger, mustRead(t, result.Plan.Ledger)) || !reflect.DeepEqual(priorActivation, activation()) || string(mustRead(t, filepath.Join(data, "retained.txt"))) != "retained target data" {
					t.Fatal("failed transition changed prior ownership or runtime data")
				}
				interrupt(false)
				if _, err := os.Stat(skill.Dst); !os.IsNotExist(err) || !bytes.Equal(priorLedger, mustRead(t, result.Plan.Ledger)) || !reflect.DeepEqual(priorActivation, activation()) {
					t.Fatal("interrupted transition did not restore native-only activation")
				}
				interrupt(true)
				run()
				if !bytes.Contains(mustRead(t, skill.Dst), []byte(filepath.Join(data, "retained.txt"))) {
					t.Fatal("ordinary delivered instructions lost the retained data path")
				}
				ledger, _, err := eng.LoadLedger(result.Plan.Ledger)
				if err != nil || len(ledger.NativePlugins) != 0 {
					t.Fatal("ordinary transition retained native activation ownership", err)
				}
				for _, entry := range ledger.Managed {
					if entry.Delivery != nil {
						t.Fatal("ordinary transition retained portable package ownership")
					}
				}
				writeFile(t, filepath.Join(source, mcp), servers)
				if _, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: cfgDir, Name: "alias"}, nil, nil); err != nil {
					t.Fatal(err)
				}
				cfg.Packs[0].MCP = nativePolicy
				ordinaryLedger, ordinarySkill := mustRead(t, result.Plan.Ledger), mustRead(t, skill.Dst)
				ordinaryActivation := activation()
				if _, _, err := RunSync(context.Background(), failing, resolve(), req, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "injected native ledger") {
					t.Fatalf("reverse transition did not reach injected persistence failure: %v", err)
				}
				assertOrdinary := func() {
					t.Helper()
					if current := activation(); !reflect.DeepEqual(ordinaryActivation, current) {
						t.Fatalf("reverse rollback changed ordinary activation: before=%v after=%v", ordinaryActivation, current)
					}
					if !bytes.Equal(ordinaryLedger, mustRead(t, result.Plan.Ledger)) || !bytes.Equal(ordinarySkill, mustRead(t, skill.Dst)) || string(mustRead(t, filepath.Join(filepath.Dir(skill.Dst), "asset.txt"))) != "owned skill asset" {
						t.Fatal("reverse rollback lost ordinary ownership, activation or skill assets")
					}
				}
				assertOrdinary()
				interrupt(false)
				assertOrdinary()
				interrupt(true)
				run()
				if _, err := os.Stat(skill.Dst); !os.IsNotExist(err) || string(mustRead(t, filepath.Join(data, "retained.txt"))) != "retained target data" {
					t.Fatal("returning to native delivery retained a duplicate or lost target data")
				}
			})
		}
	}
}

// Profile policies that need native delivery exercise transitions to and from
// the ordinary converter without changing the installed source.
func TestGenericMCPTransitionRollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("foreign stdio MCP delivery requires POSIX")
	}
	for _, hid := range []domain.Harness{domain.HarnessOpenCode, domain.HarnessClaudeCode} {
		if hid == domain.HarnessClaudeCode && os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
			continue
		}
		for _, scope := range []domain.Scope{domain.ScopeGlobal, domain.ScopeProject} {
			for _, format := range []string{plugin.CodexLegacy, plugin.AgentPlugins} {
				t.Run(string(hid)+"/"+string(scope)+"/"+format, func(t *testing.T) {
					source, cfgDir, home, project := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
					nativeHome := filepath.Join(home, "native")
					manifest, mcp := ".codex-plugin/plugin.json", ".mcp.json"
					body := `{"name":"probe","version":"1.0.0"}`
					servers := `{"mcpServers":{"probe.dot":{"command":"false","cwd":"."}}}`
					if format == plugin.AgentPlugins {
						manifest, mcp = "plugin.json", "mcp.json"
						body = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`
						servers = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"probe.dot":{"type":"stdio","command":"false"}}}`
					}
					writeFile(t, filepath.Join(source, manifest), body)
					writeFile(t, filepath.Join(source, mcp), servers)
					if err := PackInstall(context.Background(), PackInstallRequest{PackPath: source, ConfigDir: cfgDir, Name: "alias", Plugin: &domain.PluginSource{Format: format, Name: "probe", Marketplace: "owned"}}, nil); err != nil {
						t.Fatal(err)
					}
					eng := engine.New(nil, nil)
					ordinary := false
					resolve := func() domain.Profile {
						t.Helper()
						cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "alias"}}}
						if !ordinary {
							cfg.Packs[0].MCP = map[string]config.MCPServerConfig{"probe.dot": {DisabledTools: []string{"hidden"}}}
						}
						p, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
						if err != nil {
							t.Fatal(err)
						}
						return p
					}
					req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: project, Scope: scope, Harnesses: []domain.Harness{hid}, Env: map[string]string{"CLAUDE_CONFIG_DIR": nativeHome, "OPENCODE_CONFIG_DIR": nativeHome}}, Yes: true, Quiet: true}
					run := func() SyncResult {
						t.Helper()
						result, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil)
						if err != nil {
							t.Fatal(err)
						}
						return result
					}
					settings, section := filepath.Join(nativeHome, "opencode.json"), "mcp"
					if hid == domain.HarnessClaudeCode {
						settings, section = filepath.Join(nativeHome, ".claude.json"), "mcpServers"
					}
					if scope == domain.ScopeProject {
						settings = filepath.Join(project, ".opencode/opencode.json")
						if hid == domain.HarnessClaudeCode {
							settings = filepath.Join(project, ".mcp.json")
						}
					}
					writeFile(t, settings, `{"`+section+`":{"foreign":{"command":"false"}},"unrelated":"retain"}`)
					activation := func() map[string]any {
						t.Helper()
						state, err := readNativeConfig(settings)
						if err != nil {
							t.Fatal(err)
						}
						return state
					}
					result := run()
					for _, toOrdinary := range []bool{true, false} {
						prior, priorLedger := activation(), mustRead(t, result.Plan.Ledger)
						ordinary = toOrdinary
						eng.FS = nativeLedgerFailure{Path: result.Plan.Ledger}
						if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "injected native ledger") {
							t.Fatalf("transition did not reach persistence failure: %v", err)
						}
						assertPrior := func() {
							t.Helper()
							if !reflect.DeepEqual(prior, activation()) || !bytes.Equal(priorLedger, mustRead(t, result.Plan.Ledger)) {
								t.Fatalf("failed transition to ordinary=%v changed server activation or ledger: before=%v after=%v", toOrdinary, prior, activation())
							}
						}
						assertPrior()
						for _, after := range []bool{false, true} {
							eng.FS = nativeLedgerInterruption{Path: result.Plan.Ledger, After: after}
							func() {
								defer func() {
									if got := recover(); got != "native ledger interruption" {
										t.Fatalf("expected interrupted transition, got %v", got)
									}
								}()
								run()
							}()
							eng.FS = engine.OSFS{}
							if err := recoverNativeOperation(cfgDir); err != nil {
								t.Fatal(err)
							}
							if !after {
								assertPrior()
							}
						}
						result = run()
						current := activation()
						entries, _ := current[section].(map[string]any)
						if (entries["probe.dot"] != nil) != toOrdinary || entries["foreign"] == nil || current["unrelated"] != "retain" {
							t.Fatalf("transition lost exact server identity, unrelated content or native/ordinary exclusivity: %v", current)
						}
					}
				})
			}
		}
	}
}

func TestGenericContentRefusesForeignDelivery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("foreign stdio MCP delivery requires POSIX")
	}
	for _, hid := range []domain.Harness{domain.HarnessOpenCode, domain.HarnessCline} {
		for _, format := range []string{plugin.CodexLegacy, plugin.AgentPlugins} {
			t.Run(string(hid)+"/"+format, func(t *testing.T) {
				source, cfgDir, foreignCfg, home, project := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
				nativeHome := filepath.Join(home, "native")
				manifest, mcp := ".codex-plugin/plugin.json", ".mcp.json"
				body, server := `{"name":"probe","version":"1.0.0"}`, `{"mcpServers":{"probe":{"command":"false","cwd":"."}}}`
				if format == plugin.AgentPlugins {
					manifest, mcp = "plugin.json", "mcp.json"
					body = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`
					server = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"probe":{"type":"stdio","command":"false"}}}`
				}
				writeFile(t, filepath.Join(source, manifest), body)
				writeFile(t, filepath.Join(source, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Owned foreign-delivery fixture\n---\nOWNED_BODY\n")
				eng := engine.New(nil, nil)
				cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "alias"}}}
				spec := TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: project, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{hid}, Env: map[string]string{"OPENCODE_CONFIG_DIR": nativeHome}}
				install := func(configDir string) {
					t.Helper()
					if err := PackInstall(context.Background(), PackInstallRequest{PackPath: source, ConfigDir: configDir, Name: "alias", Plugin: &domain.PluginSource{Format: format, Name: "probe", Marketplace: "owned"}}, nil); err != nil {
						t.Fatal(err)
					}
				}
				var foreignPath, foreignBody string
				if hid == domain.HarnessOpenCode {
					writeFile(t, filepath.Join(source, mcp), server)
					install(foreignCfg)
					cfg.Packs[0].MCP = map[string]config.MCPServerConfig{"probe": {DisabledTools: []string{"hidden"}}}
					p, _, err := eng.Resolve(cfg, "", foreignCfg, config.CollisionError, nil)
					if err != nil {
						t.Fatal(err)
					}
					foreignSpec := spec
					foreignSpec.ConfigDir = foreignCfg
					result, _, err := RunSync(context.Background(), eng, p, SyncRequest{TargetSpec: foreignSpec, Yes: true, Quiet: true}, testRegistry(), nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					for _, action := range result.Plan.Writes {
						if action.Delivery != nil {
							foreignPath = filepath.Join(action.Dst, "skills/probe/SKILL.md")
						}
					}
					foreignBody = "OWNED_BODY"
					if err := os.Remove(filepath.Join(source, mcp)); err != nil {
						t.Fatal(err)
					}
					cfg.Packs[0].MCP = nil
					if os.Getenv("AIPACK_TEST_OPENCODE_NATIVE") == "1" {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						cmd := exec.CommandContext(ctx, "opencode", "debug", "skill")
						cmd.Dir, cmd.WaitDelay = project, time.Second
						cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_DATA_HOME=" + filepath.Join(home, ".local/share"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "XDG_STATE_HOME=" + filepath.Join(home, ".state"), "OPENCODE_CONFIG_DIR=" + nativeHome, "OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_DISABLE_MODELS_FETCH=1", "OPENCODE_DISABLE_EXTERNAL_SKILLS=1", "OPENCODE_DISABLE_PROJECT_CONFIG=1", "HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1", "NO_PROXY=127.0.0.1,localhost"}
						out, err := cmd.CombinedOutput()
						if err != nil || !bytes.Contains(out, []byte(foreignPath)) || !bytes.Contains(out, []byte("probe@owned:probe")) {
							t.Fatalf("native OpenCode did not load foreign delivery: %s %v", out, err)
						}
					}
				} else {
					foreignRoot := filepath.Join(home, ".agents/plugins/unrelated-directory-name")
					writeFile(t, filepath.Join(foreignRoot, "plugin.json"), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`)
					foreignPath, foreignBody = filepath.Join(foreignRoot, "skills/probe/SKILL.md"), "NATIVE_BODY"
					writeFile(t, foreignPath, "---\nname: probe\ndescription: Owned native Cline fixture\n---\n"+foreignBody+"\n")
					if sdk := os.Getenv("AIPACK_TEST_CLINE_SDK"); sdk != "" {
						code := `import assert from 'node:assert/strict'; import {pathToFileURL} from 'node:url';
const sdk = await import(pathToFileURL(process.argv[1]).href);
const loaded = await sdk.loadAgentPluginPackages({searchPaths:[process.argv[2]],pluginDataRoot:process.argv[3]});
assert.equal(loaded.plugins.length,1); assert.equal(loaded.plugins[0].manifest.name,'probe');
const service=sdk.createUserInstructionConfigService({skills:{directories:loaded.skills.map(s=>s.directoryPath),agentPluginSkills:loaded.skills,includePluginSkills:false},rules:{directories:[]},workflows:{directories:[]}});
try { await service.start(); const records=service.listRecords('skill'); assert.equal(records.length,1); assert.equal(records[0].id,'probe:probe'); assert(records[0].item.instructions.includes('NATIVE_BODY')); }
finally { service.stop(); }`
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						out, err := exec.CommandContext(ctx, "node", "--input-type=module", "-e", code, sdk, filepath.Dir(foreignRoot), filepath.Join(home, "cline-data")).CombinedOutput()
						if err != nil {
							t.Fatalf("native Cline did not load foreign package: %s %v", out, err)
						}
					}
				}
				before := mustRead(t, foreignPath)
				var beforeSettings []byte
				if hid == domain.HarnessOpenCode {
					beforeSettings = mustRead(t, filepath.Join(nativeHome, "opencode.json"))
				}
				install(cfgDir)
				resolve := func() domain.Profile {
					t.Helper()
					p, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
					if err != nil {
						t.Fatal(err)
					}
					return p
				}
				req := SyncRequest{TargetSpec: spec, Yes: true, Quiet: true}
				check := func() {
					t.Helper()
					for _, scope := range []domain.Scope{domain.ScopeGlobal, domain.ScopeProject} {
						req.Scope = scope
						if _, err := PlanWithDiffs(context.Background(), eng, resolve(), req, testRegistry()); err == nil || !strings.Contains(err.Error(), "outside") {
							t.Fatalf("management preview accepted foreign delivery (%s): %v", scope, err)
						}
						for _, dryRun := range []bool{true, false} {
							req.DryRun = dryRun
							if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "outside") {
								t.Fatalf("generic sync accepted foreign delivery (%s preview=%t): %v", scope, dryRun, err)
							}
						}
					}
				}
				check()
				if hid == domain.HarnessCline {
					writeFile(t, filepath.Join(source, mcp), server)
					if _, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: cfgDir, Name: "alias"}, nil, nil); err != nil {
						t.Fatal(err)
					}
					exclude := []string{"probe"}
					cfg.Packs[0].Skills.Exclude = &exclude
					check() // A server-only selection must not inherit uncontrolled native skills.
				}
				if !bytes.Equal(before, mustRead(t, foreignPath)) || !bytes.Contains(before, []byte(foreignBody)) {
					t.Fatal("refusal changed foreign plugin content")
				}
				if hid == domain.HarnessOpenCode && !bytes.Equal(beforeSettings, mustRead(t, filepath.Join(nativeHome, "opencode.json"))) {
					t.Fatal("refusal changed foreign activation settings")
				}
				for _, root := range []string{filepath.Join(nativeHome, "skills"), filepath.Join(home, ".agents/skills"), filepath.Join(project, ".opencode/skills"), filepath.Join(project, ".agents/skills")} {
					if _, err := os.Stat(filepath.Join(root, "probe/SKILL.md")); !os.IsNotExist(err) {
						t.Fatal("refusal left an ordinary skill copy active", err)
					}
				}
				if _, _, err := RunSync(context.Background(), eng, domain.NewProfile(), req, testRegistry(), nil, nil); err != nil {
					t.Fatal("inactive import blocked foreign native content", err)
				}
				if hid == domain.HarnessCline {
					foreignRoot := filepath.Join(home, ".agents/plugins/unrelated-directory-name")
					writeFile(t, filepath.Join(foreignRoot, "plugin.json"), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"unrelated","version":"1.0.0"}`)
					loose := filepath.Join(home, "loose.js")
					writeFile(t, loose, "// Owned non-package fixture")
					if err := os.Symlink(loose, filepath.Join(filepath.Dir(foreignRoot), "loose-link")); err != nil {
						t.Fatal(err)
					}
					if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err != nil {
						t.Fatal("unrelated native identity or non-package link blocked ordinary delivery", err)
					}
				}
			})
		}
	}
}

func TestGenericSkillsRefuseForeignClaudeInstallation(t *testing.T) {
	source, cfgDir, home, project := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	nativeHome := filepath.Join(home, "native")
	writeFile(t, filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"probe","version":"1.0.0"}`)
	writeFile(t, filepath.Join(source, ".claude-plugin/plugin.json"), `{"name":"probe","version":"1.0.0"}`)
	writeFile(t, filepath.Join(source, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Owned overlap fixture\n---\nOWNED_BODY\n")
	writeFile(t, filepath.Join(source, ".claude-plugin/marketplace.json"), `{"name":"owned","owner":{"name":"shrug-labs"},"plugins":[{"name":"probe","source":"./"}]}`)
	if err := PackInstall(context.Background(), PackInstallRequest{PackPath: source, ConfigDir: cfgDir, Name: "alias", Plugin: &domain.PluginSource{Format: plugin.CodexLegacy, Name: "probe", Marketplace: "owned"}}, nil); err != nil {
		t.Fatal(err)
	}
	foreign := domain.NativePluginRecord{Harness: domain.HarnessClaudeCode, ConfigHome: nativeHome, MarketplaceDir: source, SettingsPath: filepath.Join(nativeHome, "settings.json")}
	installedPath := filepath.Join(nativeHome, "plugins/installed_plugins.json")
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") == "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if err := addNativeMarketplace(ctx, home, foreign); err != nil {
			t.Fatal(err)
		}
		if _, err := installNativePlugin(ctx, home, foreign, "probe@owned"); err != nil {
			t.Fatal(err)
		}
	} else {
		writeFile(t, installedPath, `{"version":2,"plugins":{"probe@owned":[{"scope":"user","installPath":"/owned/native-cache"}]}}`)
		writeFile(t, foreign.SettingsPath, `{"enabledPlugins":{"probe@owned":true}}`)
	}
	beforeInstalled, beforeSettings := mustRead(t, installedPath), mustRead(t, foreign.SettingsPath)
	cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "alias"}}}
	eng := engine.New(nil, nil)
	req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: project, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessClaudeCode}, Env: map[string]string{"CLAUDE_CONFIG_DIR": nativeHome}}, Yes: true, Quiet: true}
	resolve := func() domain.Profile {
		t.Helper()
		p, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := PlanWithDiffs(context.Background(), eng, resolve(), req, testRegistry()); err == nil || !strings.Contains(err.Error(), "outside AIPack") {
		t.Fatalf("management preview accepted a foreign native installation: %v", err)
	}
	for _, dryRun := range []bool{true, false} {
		req.DryRun = dryRun
		if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "outside AIPack") {
			t.Fatalf("ordinary conversion accepted a foreign native installation (preview=%t): %v", dryRun, err)
		}
	}
	if !bytes.Equal(beforeInstalled, mustRead(t, installedPath)) || !bytes.Equal(beforeSettings, mustRead(t, foreign.SettingsPath)) {
		t.Fatal("refusal changed foreign native installation or activation")
	}
	if _, err := os.Stat(filepath.Join(nativeHome, "skills/probe/SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("refusal activated an ordinary duplicate")
	}
	if _, _, err := RunSync(context.Background(), eng, domain.NewProfile(), req, testRegistry(), nil, nil); err != nil {
		t.Fatal("inactive import blocked unrelated native content", err)
	}
}

func TestGenericMCPUsesOrdinaryPackDelivery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("foreign stdio MCP delivery requires POSIX")
	}
	for _, hid := range []domain.Harness{domain.HarnessCline, domain.HarnessClaudeCode, domain.HarnessOpenCode} {
		for _, format := range []string{plugin.AgentPlugins} {
			t.Run(string(hid)+"/"+format, func(t *testing.T) {
				source, cfgDir, home, project := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
				cfgDir, err := filepath.EvalSymlinks(cfgDir)
				if err != nil {
					t.Fatal(err)
				}
				manifest, mcpPath := ".codex-plugin/plugin.json", ".mcp.json"
				body := `{"name":"probe","version":"1.0.0"}`
				data := filepath.Join(cfgDir, "plugin-data/probe@owned")
				nativeHome := filepath.Join(home, "native")
				if hid == domain.HarnessClaudeCode {
					data = filepath.Join(nativeHome, "plugins/data/probe-owned")
				} else if hid == domain.HarnessOpenCode {
					data = filepath.Join(nativeHome, "aipack-data/probe@owned")
				}
				literal := "literal ${TOKEN} $(touch SHOULD_NOT_EXIST)"
				if hid == domain.HarnessClaudeCode {
					literal = "literal $TOKEN $(touch SHOULD_NOT_EXIST)"
				}
				entry := map[string]any{"command": "python3", "args": []string{"scripts/server.py", literal}, "cwd": ".", "env": map[string]string{"DATA_DIR": data}}
				if format == plugin.AgentPlugins {
					manifest, mcpPath = "plugin.json", "mcp.json"
					body = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`
					entry = map[string]any{"type": "stdio", "command": "./scripts/server.py", "args": []string{literal}, "env": map[string]string{"DATA_DIR": "${PLUGIN_DATA}"}}
				}
				writeFile(t, filepath.Join(source, manifest), body)
				writeFile(t, filepath.Join(source, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Generic MCP fixture\n---\nRead marker.txt from ${PLUGIN_ROOT}; retain ${PLUGIN_DATA}.\n")
				writeFile(t, filepath.Join(source, "marker.txt"), "initial")
				writeFile(t, filepath.Join(source, "scripts/server.py"), `#!/usr/bin/env python3
import json, os, pathlib, sys
for line in sys.stdin:
    request = json.loads(line)
    if "id" not in request: continue
    method = request.get("method")
    if method == "initialize":
        result = {"protocolVersion": request["params"]["protocolVersion"], "capabilities": {"tools": {}}, "serverInfo": {"name": "owned", "version": "1"}}
    elif method == "tools/list":
        result = {"tools": [{"name": "observe", "description": "Owned fixture", "inputSchema": {"type": "object", "properties": {}}}]}
    elif method == "tools/call":
        result = {"content": [{"type": "text", "text": json.dumps({"asset": pathlib.Path("marker.txt").read_text(), "cwd": os.getcwd(), "data": os.environ["DATA_DIR"], "literal": sys.argv[1], "root": os.environ.get("PLUGIN_ROOT", "")})}]}
    else: result = {}
    print(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}), flush=True)
`)
				if err := os.Chmod(filepath.Join(source, "scripts/server.py"), 0o755); err != nil {
					t.Fatal(err)
				}
				writeMCP := func() {
					t.Helper()
					servers := map[string]any{"mcpServers": map[string]any{"probe": entry}}
					if format == plugin.AgentPlugins {
						servers["$schema"] = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"
					}
					raw, err := json.Marshal(servers)
					if err != nil {
						t.Fatal(err)
					}
					writeFile(t, filepath.Join(source, mcpPath), string(raw))
				}
				writeMCP()
				if err := PackInstall(context.Background(), PackInstallRequest{PackPath: source, ConfigDir: cfgDir, Name: "alias", Plugin: &domain.PluginSource{Format: format, Name: "probe", Marketplace: "owned"}}, nil); err != nil {
					t.Fatal(err)
				}
				cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "alias"}}}
				eng, reg := engine.New(nil, nil), testRegistry()
				resolve := func() domain.Profile {
					t.Helper()
					p, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
					if err != nil {
						t.Fatal(err)
					}
					return p
				}
				req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: project, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{hid}, Env: map[string]string{"CLAUDE_CONFIG_DIR": nativeHome, "OPENCODE_CONFIG_DIR": nativeHome}}, Yes: true, Quiet: true, DryRun: true}
				run := func() SyncResult {
					t.Helper()
					result, _, err := RunSync(context.Background(), eng, resolve(), req, reg, nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					return result
				}
				preview := run()
				if len(preview.Plan.NativePlugins) != 0 || len(preview.Plan.MCPServers) != 1 || preview.Plan.MCPServers[0].SourcePack != "alias" {
					t.Fatalf("generic server did not use ordinary MCP planning: native=%d servers=%d", len(preview.Plan.NativePlugins), len(preview.Plan.MCPServers))
				}
				settings := preview.Plan.MCPServers[0].ConfigPath
				if _, err := os.Stat(settings); !os.IsNotExist(err) {
					t.Fatal("preview wrote native settings")
				}
				req.DryRun = false
				run()
				if _, err := os.Stat(data); !os.IsNotExist(err) {
					t.Fatal("sync ran server setup")
				}
				observe := func(marker string) {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					var output []byte
					var err error
					if sdk := os.Getenv("AIPACK_TEST_CLINE_SDK"); sdk != "" && hid == domain.HarnessCline {
						code := `import {pathToFileURL} from 'node:url';
const sdk = await import(pathToFileURL(process.argv[2]).href);
const registration = sdk.resolveMcpServerRegistration('probe', {filePath:process.argv[1]});
const client = await sdk.createDefaultMcpServerClientFactory({settingsPath:process.argv[1]})(registration);
try { await client.connect(); console.log(JSON.stringify(await client.callTool({name:'observe',arguments:{}}))); }
finally { await client.disconnect(); }`
						output, err = exec.CommandContext(ctx, "node", "--input-type=module", "-e", code, settings, sdk).CombinedOutput()
					} else if hid == domain.HarnessOpenCode {
						var native struct {
							Servers map[string]struct {
								Command []string          `json:"command"`
								Env     map[string]string `json:"environment"`
							} `json:"mcp"`
						}
						if err := json.Unmarshal(mustRead(t, settings), &native); err != nil {
							t.Fatal(err)
						}
						server := native.Servers["probe"]
						cmd := exec.CommandContext(ctx, server.Command[0], server.Command[1:]...)
						cmd.Env = os.Environ()
						for key, value := range server.Env {
							cmd.Env = append(cmd.Env, key+"="+value)
						}
						cmd.Stdin = strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"observe\",\"arguments\":{}}}\n")
						output, err = cmd.CombinedOutput()
						var response struct {
							Result json.RawMessage `json:"result"`
						}
						if err == nil {
							err = json.Unmarshal(output, &response)
							output = response.Result
						}
					} else {
						var native struct {
							Servers map[string]struct {
								Command string            `json:"command"`
								Args    []string          `json:"args"`
								Env     map[string]string `json:"env"`
							} `json:"mcpServers"`
						}
						if err := json.Unmarshal(mustRead(t, settings), &native); err != nil {
							t.Fatal(err)
						}
						server := native.Servers["probe"]
						cmd := exec.CommandContext(ctx, server.Command, server.Args...)
						cmd.Env = os.Environ()
						for key, value := range server.Env {
							cmd.Env = append(cmd.Env, key+"="+value)
						}
						cmd.Stdin = strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"observe\",\"arguments\":{}}}\n")
						output, err = cmd.CombinedOutput()
						var response struct {
							Result json.RawMessage `json:"result"`
						}
						if err == nil {
							err = json.Unmarshal(output, &response)
							output = response.Result
						}
					}
					if err != nil {
						t.Fatal("ordinary server invocation failed", err, string(output))
					}
					var result struct {
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
					}
					if err := json.Unmarshal(output, &result); err != nil || len(result.Content) != 1 {
						t.Fatal("missing native result", string(output), err)
					}
					var observed map[string]string
					if err := json.Unmarshal([]byte(result.Content[0].Text), &observed); err != nil || observed["asset"] != marker || observed["cwd"] != filepath.Join(cfgDir, "packs/alias/upstream") || observed["data"] != data || observed["literal"] != literal {
						t.Fatal("generic conversion changed native arguments/assets/cwd/data", observed, err)
					}
					if format == plugin.AgentPlugins && observed["root"] != observed["cwd"] {
						t.Fatal("native automatic root variable was lost", observed)
					}
				}
				observe("initial")
				if hid != domain.HarnessCline {
					name := "mcp__probe__observe"
					if hid == domain.HarnessOpenCode {
						name = "probe_observe"
					}
					traced, err := RunTrace(context.Background(), eng, resolve(), TraceRequest{TargetSpec: req.TargetSpec, ResourceType: "mcp", ResourceName: name, MCPTool: true}, reg)
					if err != nil || !traced.Found || traced.ResourceName != "probe" || traced.Source.Pack != "alias" || traced.Source.NativeBinding != "probe@owned" || len(traced.Destinations) != 1 {
						t.Fatal("observed MCP tool lost its original source", traced, err)
					}
				}
				checkNative := func() {
					if (hid == domain.HarnessClaudeCode && os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") == "1") || (hid == domain.HarnessOpenCode && os.Getenv("AIPACK_TEST_OPENCODE_NATIVE") == "1") {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						binary := "claude"
						if hid == domain.HarnessOpenCode {
							binary = "opencode"
						}
						cmd := exec.CommandContext(ctx, binary, "mcp", "list")
						cmd.Dir, cmd.WaitDelay = project, time.Second
						cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + nativeHome, "OPENCODE_CONFIG_DIR=" + nativeHome, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_DATA_HOME=" + filepath.Join(home, ".local/share"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "XDG_STATE_HOME=" + filepath.Join(home, ".state"), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_DISABLE_MODELS_FETCH=1", "OPENCODE_DISABLE_EXTERNAL_SKILLS=1", "HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1", "NO_PROXY=127.0.0.1,localhost"}
						out, err := cmd.CombinedOutput()
						if err != nil || !bytes.Contains(out, []byte("probe")) || !bytes.Contains(bytes.ToLower(out), []byte("connected")) {
							t.Fatalf("native ordinary MCP loading/initialization failed: %v\n%s", err, out)
						}
					}
				}
				checkNative()
				info, err := os.Stat(data)
				if err != nil || info.Mode().Perm() != 0o700 {
					t.Fatal("first native launch did not create private runtime data", err)
				}
				writeFile(t, filepath.Join(data, "retained.txt"), "retained")
				before := mustRead(t, settings)
				run()
				if !bytes.Equal(before, mustRead(t, settings)) {
					t.Fatal("unchanged generic MCP sync rewrote settings")
				}
				trace, err := RunTrace(context.Background(), eng, resolve(), TraceRequest{TargetSpec: req.TargetSpec, ResourceType: "mcp", ResourceName: "probe"}, reg)
				if err != nil || trace.Source == nil || trace.Source.NativeBinding != "probe@owned" || len(trace.Destinations) != 1 || trace.Destinations[0].Path != settings || len(trace.Blockers) != 0 {
					t.Fatalf("generic MCP trace lost original identity: %+v, %v", trace, err)
				}
				writeFile(t, filepath.Join(source, "marker.txt"), "updated")
				if _, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: cfgDir, Name: "alias"}, nil, nil); err != nil {
					t.Fatal(err)
				}
				run()
				observe("updated")
				ordinary := t.TempDir()
				writeFile(t, filepath.Join(ordinary, "pack.json"), `{"schema_version":2,"name":"ordinary","version":"1.0.0","root":".","mcp":["probe"]}`)
				writeFile(t, filepath.Join(ordinary, "mcp/probe.json"), `{"name":"probe","transport":"stdio","command":["echo","ordinary"]}`)
				if err := PackInstall(context.Background(), PackInstallRequest{ConfigDir: cfgDir, PackPath: ordinary}, nil); err != nil {
					t.Fatal(err)
				}
				cfg.Packs = append(cfg.Packs, config.PackEntry{Name: "ordinary"})
				before = mustRead(t, settings)
				if _, _, err := RunSync(context.Background(), eng, resolve(), req, reg, nil, nil); err == nil || !strings.Contains(err.Error(), "content collision") || !bytes.Equal(before, mustRead(t, settings)) {
					t.Fatal("generic server silently replaced ordinary content", err)
				}
				for _, strategy := range []config.CollisionStrategy{config.CollisionFirstWins, config.CollisionLastWins} {
					p, _, err := eng.Resolve(cfg, "", cfgDir, strategy, nil)
					if err != nil {
						t.Fatal(err)
					}
					previewReq := req
					previewReq.DryRun = true
					result, _, err := RunSync(context.Background(), eng, p, previewReq, reg, nil, nil)
					winner := "alias"
					if strategy == config.CollisionLastWins {
						winner = "ordinary"
					}
					if err != nil || len(result.Plan.MCPServers) != 1 || result.Plan.MCPServers[0].SourcePack != winner || len(p.MCPServers) != 1 {
						t.Fatal("generic MCP policy lost profile order or mutated the source", strategy, err)
					}
				}
				cfg.Packs[1].Overrides.MCP = []string{"probe"}
				run()
				trace, err = RunTrace(context.Background(), eng, resolve(), TraceRequest{TargetSpec: req.TargetSpec, ResourceType: "mcp", ResourceName: "probe"}, reg)
				if err != nil || trace.Source == nil || trace.Source.Pack != "ordinary" || len(trace.Destinations) != 1 || len(trace.Blockers) != 0 {
					t.Fatalf("generic MCP trace did not follow ordinary override: %+v, %v", trace, err)
				}
				trace, err = RunTrace(context.Background(), eng, resolve(), TraceRequest{TargetSpec: req.TargetSpec, ResourceType: "mcp", ResourceName: "probe", PackName: "alias"}, reg)
				if err != nil || !trace.Found || len(trace.Destinations) != 0 || len(trace.Blockers) != 1 || !strings.Contains(trace.Blockers[0], "suppressed") {
					t.Fatalf("generic MCP trace attributed the winner to the suppressed import: %+v, %v", trace, err)
				}
				cfg.Packs = cfg.Packs[:1]
				run()
				for _, enabled := range []bool{false, true} {
					cfg.Packs[0].MCP = map[string]config.MCPServerConfig{"probe": {Enabled: &enabled}}
					run()
					native, err := readNativeConfig(settings)
					section := "mcpServers"
					if hid == domain.HarnessOpenCode {
						section = "mcp"
					}
					servers, _ := native[section].(map[string]any)
					if err != nil || (servers["probe"] != nil) != enabled {
						t.Fatal("server selection was not applied", enabled, err)
					}
					if enabled {
						observe("updated")
					}
				}
				cfg.Packs[0].MCP = map[string]config.MCPServerConfig{"probe": {AllowedTools: []string{"observe"}}}
				if hid == domain.HarnessCline {
					if _, _, err := RunSync(context.Background(), eng, resolve(), req, reg, nil, nil); err == nil || !strings.Contains(err.Error(), "tool controls") {
						t.Fatal("unmapped tool policy was dropped", err)
					}
				} else {
					previewReq := req
					previewReq.DryRun = true
					fallback, _, err := RunSync(context.Background(), eng, resolve(), previewReq, reg, nil, nil)
					native := len(fallback.Plan.NativePlugins) == 1
					for _, action := range fallback.Plan.Writes {
						native = native || action.Delivery != nil
					}
					if err != nil || !native {
						t.Fatal("tool policy lost verified native fallback", err)
					}
				}
				cfg.Packs[0].MCP = nil
				if format == plugin.CodexLegacy {
					cfg.Packs[0].MCP = map[string]config.MCPServerConfig{"probe": {StartupTimeout: "strict"}}
				}
				entry["startup_timeout_sec"] = 600
				writeMCP()
				if _, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: cfgDir, Name: "alias"}, nil, nil); err != nil {
					t.Fatal(err)
				}
				req.Harnesses = []domain.Harness{domain.HarnessCodex, hid}
				before = mustRead(t, settings)
				if _, _, err := RunSyncEach(context.Background(), eng, resolve(), req, reg, nil, nil); err == nil || !strings.Contains(err.Error(), "startup_timeout_sec") {
					t.Fatal("unmapped server field was accepted", err)
				}
				if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) || !bytes.Equal(before, mustRead(t, settings)) {
					t.Fatal("unsupported target wrote before refusal")
				}
				if string(mustRead(t, filepath.Join(data, "retained.txt"))) != "retained" {
					t.Fatal("source update or selection removed runtime data")
				}
				if format == plugin.CodexLegacy {
					policy := "unified"
					if hid == domain.HarnessClaudeCode {
						policy = "host"
					}
					cfg.Packs[0].MCP = map[string]config.MCPServerConfig{"probe": {StartupTimeout: policy}}
					writeProfileContentProfile(t, cfgDir, "default", cfg)
					cfg = loadProfileContentProfile(t, cfgDir, "default")
					req.Harnesses = []domain.Harness{hid}
					result := run()
					if len(result.Plan.NativePlugins) != 0 || len(result.Plan.MCPServers) != 1 {
						t.Fatal("explicit timeout policy lost ordinary delivery")
					}
					if !slices.ContainsFunc(result.Plan.Warnings, func(w domain.Warning) bool { return strings.Contains(w.Message, "startup_timeout="+policy) }) {
						t.Fatal("changed timeout semantics were not reported", result.Plan.Warnings)
					}
					native, err := readNativeConfig(settings)
					section := "mcpServers"
					if hid == domain.HarnessOpenCode {
						section = "mcp"
					}
					servers, _ := native[section].(map[string]any)
					server, _ := servers["probe"].(map[string]any)
					want := any(json.Number("600"))
					if hid == domain.HarnessOpenCode {
						want = json.Number("600000")
					} else if hid == domain.HarnessClaudeCode {
						want = nil
					}
					if err != nil || server["timeout"] != want {
						t.Fatal("startup timing mislabeled or missing", server, err)
					}
					observe("updated")
					for _, enabled := range []bool{false, true} {
						packs, errs := ResolveProfilePacks(cfgDir, cfg.Packs)
						if len(errs) != 0 {
							t.Fatal(errs)
						}
						tree := BuildContentTree(packs, cfg.Packs)
						for i := range tree.Items {
							if tree.Items[i].Category == domain.CategoryMCP {
								tree.Items[i].Enabled = enabled
							}
						}
						ApplyContentTree(tree, cfg.Packs)
						writeProfileContentProfile(t, cfgDir, "default", cfg)
						cfg = loadProfileContentProfile(t, cfgDir, "default")
						if cfg.Packs[0].MCP["probe"].StartupTimeout != policy {
							t.Fatal("component toggle lost timeout policy")
						}
						run()
						if enabled {
							checkNative()
						}
					}
					trace, err = RunTrace(context.Background(), eng, resolve(), TraceRequest{TargetSpec: req.TargetSpec, ResourceType: "mcp", ResourceName: "probe"}, reg)
					if err != nil || trace.Source == nil || trace.Source.NativeBinding != "probe@owned" || len(trace.Destinations) != 1 {
						t.Fatal("timeout policy changed provenance", trace, err)
					}
					profileBytes := mustRead(t, filepath.Join(cfgDir, "profiles/default.yaml"))
					entry["startup_timeout_sec"] = 30
					writeMCP()
					writeFile(t, filepath.Join(source, "marker.txt"), "policy-updated")
					if _, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: cfgDir, Name: "alias"}, nil, nil); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(profileBytes, mustRead(t, filepath.Join(cfgDir, "profiles/default.yaml"))) {
						t.Fatal("source refresh changed target timeout policy")
					}
					result = run()
					if !slices.ContainsFunc(result.Plan.Warnings, func(w domain.Warning) bool { return strings.Contains(w.Message, "startup_timeout_sec=30") }) {
						t.Fatal("source refresh did not update accepted timeout", result.Plan.Warnings)
					}
					updated, err := readNativeConfig(settings)
					servers, _ = updated[section].(map[string]any)
					server, _ = servers["probe"].(map[string]any)
					if hid == domain.HarnessCline {
						want = json.Number("30")
					} else if hid == domain.HarnessOpenCode {
						want = json.Number("30000")
					}
					if err != nil || server["timeout"] != want {
						t.Fatal("source deadline update did not reach target configuration", server, err)
					}
					observe("policy-updated")
				}
				if _, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: cfgDir, Name: "alias", Registry: reg}, nil); err != nil {
					t.Fatal(err)
				}
				native, err := readNativeConfig(settings)
				section := "mcpServers"
				if hid == domain.HarnessOpenCode {
					section = "mcp"
				}
				servers, _ := native[section].(map[string]any)
				if err != nil || servers["probe"] != nil || string(mustRead(t, filepath.Join(data, "retained.txt"))) != "retained" {
					t.Fatal("pack deletion retained activation or removed runtime data", err)
				}
			})
		}
	}
}

func TestImportedSkillsUseOrdinaryPackDelivery(t *testing.T) {
	for _, format := range []string{plugin.AgentPlugins} {
		for _, hid := range []domain.Harness{domain.HarnessClaudeCode, domain.HarnessOpenCode, domain.HarnessCline} {
			t.Run(format+"/"+string(hid), func(t *testing.T) {
				source, cfgDir, home, project := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
				manifest, body := ".codex-plugin/plugin.json", `{"name":"probe","version":"1.0.0"}`
				if format == plugin.AgentPlugins {
					manifest, body = "plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`
				}
				writeFile(t, filepath.Join(source, manifest), body)
				mcp := ".mcp.json"
				servers := `{"mcpServers":{"unused":{"command":"false","startup_timeout_sec":600}}}`
				if format == plugin.AgentPlugins {
					mcp = "mcp.json"
					servers = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"unused":{"type":"stdio","command":"false","startup_timeout_sec":600}}}`
				}
				writeFile(t, filepath.Join(source, mcp), servers)
				entry := "---\nname: first\ndescription: Trigger preserved\n---\nFIRST_BODY ${CODEX_PLUGIN_ROOT}/scripts/probe.sh ${CODEX_PLUGIN_DATA}\nRead [shared](../second/shared.txt).\n"
				writeFile(t, filepath.Join(source, "skills/first/SKILL.md"), entry)
				writeFile(t, filepath.Join(source, "skills/first/helper.sh"), "#!/bin/sh\nexit 0\n")
				if err := os.Chmod(filepath.Join(source, "skills/first/helper.sh"), 0o755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(source, "skills/second/SKILL.md"), "---\nname: second\ndescription: Excluded fixture\n---\nSECOND_BODY\n")
				writeFile(t, filepath.Join(source, "skills/second/shared.txt"), "shared asset")
				writeFile(t, filepath.Join(source, "scripts/probe.sh"), "#!/bin/sh\nexit 0\n")
				clone := func(_ context.Context, args ...string) error {
					if len(args) > 0 && args[0] == "clone" && !slices.Contains(args, "--bare") {
						files, err := plugin.ReadFiles(source)
						if err != nil {
							return err
						}
						return plugin.WriteFiles(args[len(args)-1], files)
					}
					return nil
				}
				install := PackInstallRequest{URL: "https://fixture.invalid/plugin.git", ConfigDir: cfgDir, Name: "alias", Ref: fakeHash1, RunGitFn: clone, GitHashFn: fakeHashFn(fakeHash1), Plugin: &domain.PluginSource{Format: format, Name: "probe", Marketplace: "owned"}}
				if err := PackInstall(context.Background(), install, nil); err != nil {
					t.Fatal(err)
				}
				ordinary := t.TempDir()
				writeFile(t, filepath.Join(ordinary, "pack.json"), `{"schema_version":2,"name":"ordinary","version":"1.0.0","root":"."}`)
				writeFile(t, filepath.Join(ordinary, "skills/plain/SKILL.md"), "---\nname: plain\ndescription: Ordinary fixture\n---\nORDINARY_BODY\n")
				if err := PackInstall(context.Background(), PackInstallRequest{PackPath: ordinary, ConfigDir: cfgDir}, nil); err != nil {
					t.Fatal(err)
				}
				exclude := []string{"second"}
				disabled := false
				cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "ordinary"}, {Name: "alias", Skills: config.VectorSelector{Exclude: &exclude}, MCP: map[string]config.MCPServerConfig{"unused": {Enabled: &disabled}}}}}
				eng, reg := engine.New(nil, nil), testRegistry()
				req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: project, Scope: domain.ScopeProject, Harnesses: []domain.Harness{hid}, Env: map[string]string{}}, Yes: true, Quiet: true, DryRun: true}
				req.Namespaced = format == plugin.AgentPlugins
				resolve := func() domain.Profile {
					t.Helper()
					p, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
					if err != nil {
						t.Fatal(err)
					}
					return p
				}
				run := func() SyncResult {
					t.Helper()
					result, _, err := RunSync(context.Background(), eng, resolve(), req, reg, nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					return result
				}
				preview := run()
				if len(preview.Plan.NativePlugins) != 0 {
					t.Fatal("selected skills still require native installation of excluded components")
				}
				disabled = true
				cfg.Packs[1].MCP["unused"] = config.MCPServerConfig{Enabled: &disabled, StartupTimeout: "strict"}
				reason := "startup_timeout_sec"
				if runtime.GOOS == "windows" {
					reason = "POSIX shell"
				}
				if _, _, err := RunSync(context.Background(), eng, resolve(), req, reg, nil, nil); err == nil || !strings.Contains(err.Error(), reason) {
					t.Fatal("selected unsupported MCP content was silently dropped", err)
				}
				disabled = false
				var dst string
				for _, w := range preview.Plan.Writes {
					if w.Category == domain.CategorySkills && w.SourcePack == "alias" && w.Delivery == nil {
						dst = w.Dst
					}
				}
				if dst == "" {
					t.Fatal("ordinary skill adapter received no imported content")
				}
				if _, err := os.Stat(dst); !os.IsNotExist(err) {
					t.Fatal("dry-run wrote skill content")
				}
				req.DryRun = false
				run()
				upstream := filepath.Join(cfgDir, "packs/alias/upstream")
				actual := mustRead(t, dst)
				data := filepath.Join(cfgDir, "plugin-data/probe@owned")
				if hid == domain.HarnessClaudeCode {
					data = filepath.Join(home, ".claude/plugins/data/probe-owned")
				} else if hid == domain.HarnessOpenCode {
					data = filepath.Join(project, ".opencode/aipack-data/probe@owned")
				}
				for _, value := range []string{"FIRST_BODY", "description: Trigger preserved", filepath.Join(upstream, "scripts/probe.sh"), filepath.Join(upstream, "skills/first"), data} {
					if !bytes.Contains(actual, []byte(value)) {
						t.Fatalf("generic skill lost %q", value)
					}
				}
				info, err := os.Stat(filepath.Join(filepath.Dir(dst), "helper.sh"))
				if err != nil || info.Mode().Perm() != 0o755 {
					t.Fatal("skill asset executable mode lost", err)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(dst)), "second/SKILL.md")); !os.IsNotExist(err) {
					t.Fatal("excluded skill was activated")
				}
				if string(mustRead(t, filepath.Join(upstream, "skills/second/shared.txt"))) != "shared asset" {
					t.Fatal("excluded sibling asset lost")
				}
				before, err := os.Stat(dst)
				if err != nil {
					t.Fatal(err)
				}
				run()
				after, err := os.Stat(dst)
				if err != nil || !before.ModTime().Equal(after.ModTime()) {
					t.Fatal("generic sync rewrote unchanged content", err)
				}
				trace, err := RunTrace(context.Background(), eng, resolve(), TraceRequest{TargetSpec: req.TargetSpec, ResourceType: "skill", ResourceName: "first", PackName: "alias"}, reg)
				if err != nil || len(trace.Blockers) > 0 || len(trace.Destinations) != 1 || trace.Destinations[0].Path != dst || trace.Source.NativeBinding != "probe@owned" {
					t.Fatalf("generic trace lost destination or provenance: %+v, %v", trace, err)
				}
				dataFile := filepath.Join(data, "retained.txt")
				writeFile(t, dataFile, "retained data")
				if err := ProfileSave(ProfileSaveRequest{ConfigDir: cfgDir, Name: "generic", Config: cfg}); err != nil {
					t.Fatal(err)
				}
				profilePath := filepath.Join(cfgDir, "profiles/generic.yaml")
				profileBytes := mustRead(t, profilePath)
				lockPath, manifestPath := config.LockfilePath(cfgDir), filepath.Join(cfgDir, "packs/alias/pack.json")
				lock, err := config.LoadLockfile(lockPath)
				if err != nil {
					t.Fatal(err)
				}
				oldManifest, err := config.LoadPackManifest(manifestPath)
				if err != nil {
					t.Fatal(err)
				}
				// Model a legitimately installed prior-converter artifact and baseline.
				oldManifest.NativePlugin.ConverterVersion = plugin.ConverterVersion - 1
				if err := config.SavePackManifest(manifestPath, oldManifest); err != nil {
					t.Fatal(err)
				}
				if _, err := saveIntegrity(filepath.Dir(manifestPath)); err != nil {
					t.Fatal(err)
				}
				meta := lock.Packs["alias"]
				meta.ConverterVersion = oldManifest.NativePlugin.ConverterVersion
				meta.MaterializedDigest, err = packTreeDigest(filepath.Dir(manifestPath))
				if err != nil {
					t.Fatal(err)
				}
				lock.Packs["alias"] = meta
				if err := config.SaveLockfile(lockPath, lock); err != nil {
					t.Fatal(err)
				}
				lockBytes, manifestBytes, ledgerBytes := mustRead(t, lockPath), mustRead(t, manifestPath), mustRead(t, preview.Plan.Ledger)
				if _, _, err := RunSync(context.Background(), eng, resolve(), req, reg, nil, nil); err == nil {
					t.Fatal("stale converter artifact activated without refresh")
				}
				updateReq := PackUpdateRequest{ConfigDir: cfgDir, Name: "alias", RunGitFn: clone, GitHashFn: fakeHashFn(fakeHash1), GitLsRemoteFn: func(context.Context, string, string) (string, error) { return fakeHash1, nil }, DryRun: true}
				refresh, err := PackUpdate(context.Background(), updateReq, nil, nil)
				if err != nil || len(refresh) != 1 || refresh[0].Status != StatusUpdated || !bytes.Equal(lockBytes, mustRead(t, lockPath)) || !bytes.Equal(manifestBytes, mustRead(t, manifestPath)) || !bytes.Equal(ledgerBytes, mustRead(t, preview.Plan.Ledger)) {
					t.Fatal("converter preview changed pinned installation or activation", refresh, err)
				}
				updateReq.DryRun = false
				writeFile(t, filepath.Join(source, manifest), "{")
				failedRefresh, err := PackUpdate(context.Background(), updateReq, nil, nil)
				if err != nil || len(failedRefresh) != 1 || failedRefresh[0].Status != StatusError || !bytes.Equal(manifestBytes, mustRead(t, manifestPath)) || !bytes.Equal(ledgerBytes, mustRead(t, preview.Plan.Ledger)) || !bytes.Equal(actual, mustRead(t, dst)) {
					t.Fatal("failed converter refresh changed prior source or activation", failedRefresh, err)
				}
				writeFile(t, filepath.Join(source, manifest), body)
				refresh, err = PackUpdate(context.Background(), updateReq, nil, nil)
				if err != nil || len(refresh) != 1 || refresh[0].Status != StatusUpdated {
					t.Fatal("pinned generic converter refresh failed", refresh, err)
				}
				lock, err = config.LoadLockfile(lockPath)
				if err != nil || lock.Packs["alias"].Ref != fakeHash1 || lock.Packs["alias"].CommitHash != fakeHash1 || lock.Packs["alias"].ConverterVersion != plugin.ConverterVersion || !bytes.Equal(profileBytes, mustRead(t, profilePath)) {
					t.Fatal("converter refresh changed pin or profile choices", lock, err)
				}
				run()
				if !bytes.Equal(actual, mustRead(t, dst)) || string(mustRead(t, dataFile)) != "retained data" {
					t.Fatal("converter refresh changed effective skills or retained data")
				}
				trace, err = RunTrace(context.Background(), eng, resolve(), TraceRequest{TargetSpec: req.TargetSpec, ResourceType: "skill", ResourceName: "first", PackName: "alias"}, reg)
				if err != nil || trace.Source == nil || trace.Source.CommitHash != fakeHash1 || trace.Source.ConverterVersion != plugin.ConverterVersion || trace.Source.NativeBinding != "probe@owned" || len(trace.Destinations) != 1 || trace.Destinations[0].Path != dst || len(trace.Blockers) != 0 {
					t.Fatal("converter refresh lost source identity or ordinary trace destination", trace, err)
				}
				writeFile(t, filepath.Join(source, "skills/first/SKILL.md"), strings.ReplaceAll(entry, "FIRST_BODY", "UPDATED_BODY"))
				updateReq.Ref, updateReq.GitHashFn = "latest", fakeHashFn(fakeHash2)
				updateReq.GitLsRemoteFn = func(context.Context, string, string) (string, error) { return fakeHash2, nil }
				updateReq.DryRun = true
				installed := mustRead(t, filepath.Join(upstream, "skills/first/SKILL.md"))
				previewUpdate, err := PackUpdate(context.Background(), updateReq, nil, nil)
				if err != nil || len(previewUpdate) != 1 || !bytes.Equal(installed, mustRead(t, filepath.Join(upstream, "skills/first/SKILL.md"))) {
					t.Fatal("update preview changed the installed source", previewUpdate, err)
				}
				updateReq.DryRun = false
				updated, err := PackUpdate(context.Background(), updateReq, nil, nil)
				if err != nil || len(updated) != 1 || updated[0].Status != StatusUpdated {
					t.Fatal("same-version plugin update failed", updated, err)
				}
				run()
				if !bytes.Contains(mustRead(t, dst), []byte("UPDATED_BODY")) {
					t.Fatal("same-version refresh did not reach ordinary delivery")
				}
				exclude = []string{"first", "second"}
				run()
				if _, err := os.Stat(dst); !os.IsNotExist(err) {
					t.Fatal("skill exclusion retained rendered content")
				}
				exclude = []string{"second"}
				run()
				if !bytes.Contains(mustRead(t, dst), []byte("UPDATED_BODY")) {
					t.Fatal("re-enable did not restore content")
				}
				if string(mustRead(t, dataFile)) != "retained data" {
					t.Fatal("update or selection changes removed runtime data")
				}
				writeFile(t, filepath.Join(ordinary, "skills/first/SKILL.md"), "---\nname: first\ndescription: Ordinary collision\n---\nORDINARY_COLLISION\n")
				if err := PackInstall(context.Background(), PackInstallRequest{PackPath: ordinary, ConfigDir: cfgDir}, nil); err != nil {
					t.Fatal(err)
				}
				req.DryRun = true
				_, _, err = RunSync(context.Background(), eng, resolve(), req, reg, nil, nil)
				if req.Namespaced {
					if err != nil {
						t.Fatal("namespaced composition rejected distinct sources", err)
					}
				} else if err == nil || !strings.Contains(err.Error(), "content collision") {
					t.Fatal("generic collision silently overwrote ordinary content", err)
				}
			})
		}
	}
}

func TestGenericSkillsUseProfileCollisionPolicy(t *testing.T) {
	for _, format := range []string{plugin.AgentPlugins} {
		source, cfgDir := t.TempDir(), t.TempDir()
		manifest, body := ".codex-plugin/plugin.json", `{"name":"probe","version":"1.0.0"}`
		if format == plugin.AgentPlugins {
			manifest, body = "plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`
		}
		writeFile(t, filepath.Join(source, manifest), body)
		writeFile(t, filepath.Join(source, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Imported collision fixture\n---\nIMPORTED_BODY\n")
		if err := PackInstall(context.Background(), PackInstallRequest{PackPath: source, ConfigDir: cfgDir, Name: "imported", Plugin: &domain.PluginSource{Format: format, Name: "probe", Marketplace: "owned"}}, nil); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"alpha", "omega"} {
			pack := t.TempDir()
			writeFile(t, filepath.Join(pack, "pack.json"), `{"schema_version":2,"name":"`+name+`","version":"1.0.0","root":"."}`)
			writeFile(t, filepath.Join(pack, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Ordinary collision fixture\n---\n"+name+"_BODY\n")
			if err := PackInstall(context.Background(), PackInstallRequest{PackPath: pack, ConfigDir: cfgDir}, nil); err != nil {
				t.Fatal(err)
			}
		}
		for _, hid := range []domain.Harness{domain.HarnessOpenCode} {
			for _, tc := range []struct {
				name, owner, winner string
				strategy            config.CollisionStrategy
				namespaced, reverse bool
				third               bool
			}{
				{name: "first", strategy: config.CollisionFirstWins, winner: "alpha"},
				{name: "last", strategy: config.CollisionLastWins, winner: "imported"},
				{name: "error", strategy: config.CollisionError},
				{name: "ordinary-override", strategy: config.CollisionError, owner: "alpha", winner: "alpha", reverse: true},
				{name: "imported-override", strategy: config.CollisionError, owner: "imported", winner: "imported"},
				{name: "imported-override-reversed", strategy: config.CollisionError, owner: "imported", winner: "imported", reverse: true},
				{name: "imported-override-three", strategy: config.CollisionError, owner: "imported", winner: "imported", third: true},
				{name: "namespaced", strategy: config.CollisionError, namespaced: true},
				{name: "namespaced-override", strategy: config.CollisionError, namespaced: true, owner: "imported", winner: "imported"},
			} {
				t.Run(format+"/"+string(hid)+"/"+tc.name, func(t *testing.T) {
					cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "alpha"}, {Name: "imported"}}}
					if tc.third {
						cfg.Packs = append([]config.PackEntry{{Name: "omega"}}, cfg.Packs...)
					}
					if tc.reverse {
						slices.Reverse(cfg.Packs)
					}
					for i := range cfg.Packs {
						if cfg.Packs[i].Name == tc.owner {
							cfg.Packs[i].Overrides.Skills = []string{"probe"}
						}
					}
					eng, reg := engine.New(nil, nil), testRegistry()
					p, _, err := eng.ResolveWithOptions(cfg, "", cfgDir, config.ResolveOptions{CollisionStrategy: tc.strategy, Namespaced: tc.namespaced})
					if err != nil {
						t.Fatal(err)
					}
					original := slices.Clone(p.AllSkills())
					project, home := t.TempDir(), t.TempDir()
					req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfgDir, ProjectDir: project, Home: home, Scope: domain.ScopeProject, Harnesses: []domain.Harness{hid}, Namespaced: tc.namespaced, Env: map[string]string{}}, Yes: true, Quiet: true}
					if tc.name == "error" {
						req.Harnesses = []domain.Harness{domain.HarnessCodex, hid}
						_, _, err = RunSyncEach(context.Background(), eng, p, req, reg, nil, nil)
						if err == nil || !strings.Contains(err.Error(), "content collision") {
							t.Fatal("strict collision was accepted", err)
						}
						if _, err := os.Stat(filepath.Join(project, ".codex")); !os.IsNotExist(err) {
							t.Fatal("compatible native target wrote before foreign collision refusal")
						}
						return
					}
					result, _, err := RunSync(context.Background(), eng, p, req, reg, nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					owners := map[string]bool{}
					for _, write := range result.Plan.Writes {
						if write.Category == domain.CategorySkills {
							owners[write.SourcePack] = true
							if !bytes.Equal(write.Content, mustRead(t, write.Dst)) {
								t.Fatal("selected skill did not reach its target")
							}
						}
					}
					for _, copy := range result.Plan.Copies {
						if copy.Kind == domain.CopyKindDir {
							owners[copy.SourcePack] = true
						}
					}
					if tc.winner == "" {
						if len(owners) != 2 || !owners["alpha"] || !owners["imported"] {
							t.Fatal("namespaced composition lost content", owners)
						}
					} else if len(owners) != 1 || !owners[tc.winner] {
						t.Fatal("profile winner was not delivered", owners, tc.winner)
					}
					candidates := FindTraceCandidatesForTargets(p, "probe", req.TargetSpec)
					candidates = slices.DeleteFunc(candidates, func(candidate TraceCandidate) bool { return candidate.ResourceType != "skill" })
					if len(candidates) != len(owners) || !slices.ContainsFunc(candidates, func(candidate TraceCandidate) bool { return owners[candidate.Pack] }) {
						t.Fatal("trace candidates differ from delivered skill owners", candidates, owners)
					}
					if tc.namespaced {
						for owner := range owners {
							aliases := FindTraceCandidatesForTargets(p, "probe"+domain.RenderedIdentitySeparator+owner, req.TargetSpec)
							if len(aliases) != 1 || aliases[0].Pack != owner || aliases[0].ResourceName != "probe" {
								t.Fatal("ordinary rendered skill name lost its source identity", aliases)
							}
						}
					}
					if tc.winner != "" {
						trace, err := RunTrace(context.Background(), eng, p, TraceRequest{TargetSpec: req.TargetSpec, ResourceType: "skill", ResourceName: "probe"}, reg)
						if err != nil || trace.Source == nil || trace.Source.Pack != tc.winner || len(trace.Destinations) != 1 || len(trace.Blockers) != 0 {
							t.Fatalf("plain-name trace lost the selected winner: %+v, %v", trace, err)
						}
						for _, entry := range cfg.Packs {
							if entry.Name == tc.winner {
								continue
							}
							trace, err = RunTrace(context.Background(), eng, p, TraceRequest{TargetSpec: req.TargetSpec, ResourceType: "skill", ResourceName: "probe", PackName: entry.Name}, reg)
							if err != nil || !trace.Found || len(trace.Destinations) != 0 || len(trace.Blockers) != 1 || !strings.Contains(trace.Blockers[0], "suppressed") {
								t.Fatalf("suppressed source was reported as delivered: %+v, %v", trace, err)
							}
						}
						if tc.winner != "imported" {
							aliases := FindTraceCandidatesForTargets(p, "probe@owned:probe", req.TargetSpec)
							if len(aliases) != 1 || aliases[0].Pack != "imported" {
								t.Fatal("blocked native alias no longer locates its original source", aliases)
							}
						}
					}
					if !reflect.DeepEqual(p.AllSkills(), original) || (tc.owner != "" && p.SkillOverrideOwners["probe"] != tc.owner) {
						t.Fatal("target projection mutated the source profile")
					}
				})
			}
		}
		t.Run(format+"/native-override-isolation", func(t *testing.T) {
			cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "alpha"}, {Name: "imported", Overrides: config.Overrides{Skills: []string{"probe"}}}}}
			eng, reg := engine.New(nil, nil), testRegistry()
			p, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
			if err != nil {
				t.Fatal(err)
			}
			planners, err := reg.AsPlanners([]domain.Harness{domain.HarnessCodex})
			if err != nil {
				t.Fatal(err)
			}
			req := engine.PlanRequest{ConfigDir: cfgDir, Home: t.TempDir(), ProjectDir: t.TempDir(), Scope: domain.ScopeProject}
			plan, err := engine.PlanSync(context.Background(), p, req, planners)
			if err != nil || len(plan.NativePlugins) != 1 || !slices.ContainsFunc(plan.Copies, func(copy domain.CopyAction) bool {
				return copy.SourcePack == "alpha" && copy.Kind == domain.CopyKindDir
			}) {
				t.Fatal("native-scoped override suppressed ordinary Codex content", plan, err)
			}
			cfg.Packs = append([]config.PackEntry{{Name: "omega"}}, cfg.Packs...)
			p, _, err = eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := engine.PlanSync(context.Background(), p, req, planners); err == nil || !strings.Contains(err.Error(), "content collision") {
				t.Fatal("native-scoped override hid an ordinary Codex collision", err)
			}
		})
	}
}
