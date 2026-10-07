package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/plugin"
)

func TestImportedDeletionRefusesPreservedSourceDependencies(t *testing.T) {
	for _, target := range []domain.Harness{domain.HarnessClaudeCode, domain.HarnessOpenCode, domain.HarnessCline} {
		for _, edited := range []string{"skill", "settings", "relocated-skill", "skill-custom", "clean-custom"} {
			if target == domain.HarnessCline && strings.HasSuffix(edited, "custom") {
				continue
			}
			t.Run(string(target)+"/"+edited, func(t *testing.T) {
				if runtime.GOOS == "windows" {
					t.Skip("generic stdio MCP delivery requires POSIX")
				}
				for _, key := range []string{"CLAUDE_CONFIG_DIR", "OPENCODE_CONFIG_DIR", "CLINE_DIR", "CLINE_DATA_DIR"} {
					t.Setenv(key, "")
				}
				dir, home, project, src := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
				writeEmptyProfile(t, dir, "default")
				writeFile(t, filepath.Join(src, ".codex-plugin/plugin.json"), `{"name":"probe"}`)
				writeFile(t, filepath.Join(src, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Source dependency fixture\n---\nRun ${PLUGIN_ROOT}/script.js.\n")
				writeFile(t, filepath.Join(src, ".mcp.json"), `{"mcpServers":{"probe":{"command":"node","args":["script.js"],"cwd":"."}}}`)
				writeFile(t, filepath.Join(src, "script.js"), `console.log("fixture")`)
				if err := PackInstall(context.Background(), PackInstallRequest{ConfigDir: dir, PackPath: src, Add: true}, io.Discard); err != nil {
					t.Fatal(err)
				}
				eng := engine.New(nil, nil)
				p, _, err := eng.Resolve(config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe"}}}, "", dir, config.CollisionError, nil)
				if err != nil {
					t.Fatal(err)
				}
				req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: dir, Home: home, ProjectDir: project, Scope: domain.ScopeProject, Harnesses: []domain.Harness{target}}, Yes: true}
				nativeDir := filepath.Join(home, "custom-native")
				if strings.HasSuffix(edited, "custom") {
					req.Scope = domain.ScopeGlobal
					req.Env = map[string]string{"CLAUDE_CONFIG_DIR": nativeDir, "OPENCODE_CONFIG_DIR": nativeDir}
				}
				result, _, err := RunSync(context.Background(), eng, p, req, testRegistry(), io.Discard, io.Discard)
				if err != nil {
					t.Fatal(err)
				}
				skillDir := map[domain.Harness]string{domain.HarnessClaudeCode: ".claude", domain.HarnessOpenCode: ".opencode", domain.HarnessCline: ".agents"}[target]
				skill := filepath.Join(project, skillDir, "skills/probe/SKILL.md")
				if strings.HasSuffix(edited, "custom") {
					skill = filepath.Join(nativeDir, "skills/probe/SKILL.md")
				}
				path := skill
				root := filepath.Join(PacksDir(dir), "probe")
				if edited == "settings" {
					path = result.Plan.MCPServers[0].ConfigPath
					settings := map[string]any{}
					if err := json.Unmarshal(mustRead(t, path), &settings); err != nil {
						t.Fatal(err)
					}
					settings["localAsset"] = filepath.Join(root, "upstream/script.js")
					body, _ := json.Marshal(settings)
					writeFile(t, path, string(body))
				} else if edited == "relocated-skill" {
					retained := t.TempDir()
					writeFile(t, filepath.Join(retained, "upstream/script.js"), `console.log("fixture")`)
					body := harness.RelocatePackPath(string(mustRead(t, skill)), root, retained)
					writeFile(t, skill, body+"\nLocal instructions.\n")
				} else if edited != "clean-custom" {
					writeFile(t, skill, string(mustRead(t, skill))+"\nLocal instructions.\n")
				}
				before := map[string][]byte{}
				for _, file := range []string{path, result.Plan.Ledger, config.LockfilePath(dir), filepath.Join(dir, "profiles/default.yaml")} {
					before[file] = mustRead(t, file)
				}
				for _, dryRun := range []bool{true, false} {
					_, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: dir, Name: "probe", Registry: testRegistry(), DryRun: dryRun}, io.Discard)
					if edited == "clean-custom" {
						if err != nil {
							t.Fatal(err)
						}
						_, err := os.Stat(skill)
						if (dryRun && err != nil) || (!dryRun && !os.IsNotExist(err)) {
							t.Fatalf("custom-root cleanup disagrees with preview: %v", err)
						}
						continue
					}
					if edited == "relocated-skill" {
						if err != nil || !bytes.Equal(before[skill], mustRead(t, skill)) {
							t.Fatalf("independent local edits were rejected or changed: %v", err)
						}
						continue
					}
					if err == nil || !strings.Contains(err.Error(), "still depends on its installed source") {
						t.Fatalf("dependent local content accepted (dry=%t): %v", dryRun, err)
					}
					for file, content := range before {
						if !bytes.Equal(content, mustRead(t, file)) {
							t.Fatalf("refused deletion changed %s", file)
						}
					}
					if _, err := os.Stat(filepath.Join(root, "upstream/script.js")); err != nil {
						t.Fatal("refused deletion lost source assets", err)
					}
				}
			})
		}
	}
}

func TestImportedRenameRestoresStateOnFailure(t *testing.T) {
	for _, failure := range []string{"ledger", "profile"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("OPENCODE_CONFIG_DIR", "")
			dir, home, src := t.TempDir(), t.TempDir(), t.TempDir()
			writeEmptyProfile(t, dir, "default")
			writeFile(t, filepath.Join(src, ".codex-plugin/plugin.json"), `{"name":"probe"}`)
			writeFile(t, filepath.Join(src, "skills/probe/SKILL.md"), "---\nname: probe\ndescription: Rename fixture\n---\nRun ${PLUGIN_ROOT}/script.js.\n")
			writeFile(t, filepath.Join(src, "hooks/hooks.json"), `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"node $PLUGIN_ROOT/script.js"}]}]}}`)
			writeFile(t, filepath.Join(src, "script.js"), `console.log("fixture")`)
			if err := PackInstall(context.Background(), PackInstallRequest{ConfigDir: dir, PackPath: src, Add: true}, io.Discard); err != nil {
				t.Fatal(err)
			}
			eng := engine.New(nil, nil)
			p, _, err := eng.Resolve(config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe"}}}, "", dir, config.CollisionError, nil)
			if err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			modes := map[string]os.FileMode{}
			var lastLedger string
			for _, name := range []string{"a-project", "z-project"} {
				project := filepath.Join(home, name)
				req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: dir, Home: home, ProjectDir: project, Scope: domain.ScopeProject, Harnesses: []domain.Harness{domain.HarnessOpenCode}}, Yes: true}
				result, _, err := RunSync(context.Background(), eng, p, req, testRegistry(), io.Discard, io.Discard)
				if err != nil {
					t.Fatal(err)
				}
				lastLedger = result.Plan.Ledger
				for _, path := range []string{result.Plan.Ledger, filepath.Join(project, ".opencode/skills/probe/SKILL.md"), filepath.Join(project, ".opencode/plugins/aipack-hooks.js")} {
					before[path] = mustRead(t, path)
				}
			}
			if failure == "ledger" {
				eng.FS = nativeLedgerFailure{Path: lastLedger}
			} else {
				writeFile(t, filepath.Join(dir, "profiles/z-broken.yaml"), "packs: [invalid")
				before[filepath.Join(dir, "profiles/z-broken.yaml")] = mustRead(t, filepath.Join(dir, "profiles/z-broken.yaml"))
			}
			for _, path := range []string{config.LockfilePath(dir), filepath.Join(dir, "profiles/default.yaml"), filepath.Join(PacksDir(dir), "probe/pack.json")} {
				before[path] = mustRead(t, path)
			}
			for path := range before {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				modes[path] = info.Mode().Perm()
			}
			var progress bytes.Buffer
			if err := PackRename(eng, dir, "probe", "renamed", &progress); err == nil {
				t.Fatal("expected rename persistence failure")
			}
			if progress.Len() != 0 {
				t.Fatal("failed rename reported committed progress", progress.String())
			}
			for path, content := range before {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != modes[path] || !bytes.Equal(content, mustRead(t, path)) {
					t.Fatalf("failed rename changed %s: %v", path, err)
				}
			}
			if _, err := os.Lstat(filepath.Join(PacksDir(dir), "renamed")); !os.IsNotExist(err) {
				t.Fatal("failed rename retained the new source path", err)
			}
			if string(mustRead(t, filepath.Join(PacksDir(dir), "probe/upstream/script.js"))) != `console.log("fixture")` {
				t.Fatal("failed rename lost source assets")
			}
		})
	}
}

func TestSingleImportedHookDeletion(t *testing.T) {
	for _, target := range []domain.Harness{domain.HarnessOpenCode, domain.HarnessCline} {
		for _, action := range []string{"delete", "modified", "ledger-failure", "keep-rendered", "keep-relocated"} {
			t.Run(string(target)+"/"+action, func(t *testing.T) {
				cfg, home, src := t.TempDir(), t.TempDir(), t.TempDir()
				t.Setenv("OPENCODE_CONFIG_DIR", "")
				t.Setenv("CLINE_DIR", "")
				t.Setenv("CLINE_DATA_DIR", "")
				writeEmptyProfile(t, cfg, "default")
				writeFile(t, filepath.Join(src, ".codex-plugin/plugin.json"), `{"name":"probe"}`)
				writeFile(t, filepath.Join(src, "hooks/hooks.json"), `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"node $PLUGIN_ROOT/hook.js"}]}]}}`)
				writeFile(t, filepath.Join(src, "hook.js"), `console.log("fixture")`)
				if err := PackInstall(context.Background(), PackInstallRequest{ConfigDir: cfg, PackPath: src, Name: "probe", Add: true}, io.Discard); err != nil {
					t.Fatal(err)
				}
				eng := engine.New(nil, nil)
				profile, _, err := eng.Resolve(config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe"}}}, "", cfg, config.CollisionError, nil)
				if err != nil {
					t.Fatal(err)
				}
				req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfg, Home: home, ProjectDir: home, Scope: domain.ScopeProject, Harnesses: []domain.Harness{target}}, Yes: true}
				if target == domain.HarnessOpenCode {
					req.Scope = domain.ScopeGlobal
					req.Env = map[string]string{"OPENCODE_CONFIG_DIR": filepath.Join(home, "custom-native")}
				}
				result, _, err := RunSync(context.Background(), eng, profile, req, testRegistry(), nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				var wrapper string
				for _, write := range result.Plan.Writes {
					if write.Category == domain.CategoryHooks {
						wrapper = write.Dst
					}
				}
				if wrapper == "" {
					t.Fatal("fixture has no hook wrapper")
				}
				if action == "modified" {
					writeFile(t, wrapper, string(mustRead(t, wrapper))+"\n# local change\n")
				}
				if action == "keep-relocated" {
					retained := t.TempDir()
					writeFile(t, filepath.Join(retained, "upstream/hook.js"), `console.log("fixture")`)
					body, _, changed, err := harness.RewriteHookPack(mustRead(t, wrapper), "probe", "probe", filepath.Join(PacksDir(cfg), "probe"), retained)
					if err != nil || !changed {
						t.Fatalf("could not relocate hook assets: %v", err)
					}
					writeFile(t, wrapper, string(body))
				}
				beforeHook := mustRead(t, wrapper)
				beforeLedger := mustRead(t, result.Plan.Ledger)
				beforeProfile := mustRead(t, filepath.Join(cfg, "profiles/default.yaml"))
				beforeLock := mustRead(t, config.LockfilePath(cfg))
				if action == "ledger-failure" {
					eng.FS = nativeLedgerFailure{Path: result.Plan.Ledger}
				}
				deletion := PackDeleteRequest{ConfigDir: cfg, Name: "probe", Registry: testRegistry(), KeepRendered: strings.HasPrefix(action, "keep-")}
				preview, previewErr := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: cfg, Name: "probe", Registry: testRegistry(), KeepRendered: deletion.KeepRendered, DryRun: true}, io.Discard)
				actual, deleteErr := PackDeleteWithOptions(eng, deletion, io.Discard)
				if action == "keep-relocated" {
					if previewErr != nil || deleteErr != nil || !bytes.Equal(beforeHook, mustRead(t, wrapper)) {
						t.Fatalf("independent retained hook was rejected or changed: %v %v", previewErr, deleteErr)
					}
					return
				}
				if action == "delete" {
					if previewErr != nil || deleteErr != nil || preview.RenderedRemoved != actual.RenderedRemoved || preview.LedgerCleared != actual.LedgerCleared {
						t.Fatalf("preview disagrees with deletion: %+v %v; %+v %v", preview, previewErr, actual, deleteErr)
					}
					if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
						t.Fatalf("clean wrapper survived deletion: %v", err)
					}
					return
				}
				if deleteErr == nil || action != "ledger-failure" && previewErr == nil {
					t.Fatalf("unsafe deletion succeeded: preview=%v delete=%v", previewErr, deleteErr)
				}
				if !bytes.Equal(beforeHook, mustRead(t, wrapper)) || !bytes.Equal(beforeLedger, mustRead(t, result.Plan.Ledger)) || !bytes.Equal(beforeProfile, mustRead(t, filepath.Join(cfg, "profiles/default.yaml"))) || !bytes.Equal(beforeLock, mustRead(t, config.LockfilePath(cfg))) {
					t.Fatal("refused deletion changed rendered output or ownership")
				}
				if string(mustRead(t, filepath.Join(PacksDir(cfg), "probe/upstream/hook.js"))) != `console.log("fixture")` {
					t.Fatal("refused deletion lost source assets")
				}
			})
		}
	}
}

func TestPortableDeleteFailurePreservesSharedHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("portable stdio MCP translation requires POSIX")
	}
	for _, phase := range []string{"payload-edit", "hook-write", "ledger"} {
		t.Run(phase, func(t *testing.T) {
			cfgDir, home, project := t.TempDir(), t.TempDir(), t.TempDir()
			cfg := config.ProfileConfig{}
			for _, name := range []string{"first", "second"} {
				src := t.TempDir()
				writeFile(t, filepath.Join(src, ".codex-plugin/plugin.json"), `{"name":"`+name+`"}`)
				writeFile(t, filepath.Join(src, ".mcp.json"), `{"mcpServers":{"probe":{"command":"false","cwd":"."}}}`)
				writeFile(t, filepath.Join(src, "hooks/hooks.json"), `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"true"}]}]}}`)
				if err := PackInstall(context.Background(), PackInstallRequest{ConfigDir: cfgDir, PackPath: src, Name: name}, io.Discard); err != nil {
					t.Fatal(err)
				}
				cfg.Packs = append(cfg.Packs, config.PackEntry{Name: name, MCP: map[string]config.MCPServerConfig{"probe": {DisabledTools: []string{"hidden"}}}})
			}
			eng := engine.New(nil, nil)
			profile, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
			if err != nil {
				t.Fatal(err)
			}
			req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: project, Scope: domain.ScopeProject, Harnesses: []domain.Harness{domain.HarnessOpenCode}, Env: map[string]string{"OPENCODE_CONFIG_DIR": ""}}, Yes: true}
			result, _, err := RunSync(context.Background(), eng, profile, req, testRegistry(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			wrapper := filepath.Join(project, ".opencode/plugins/aipack-hooks.js")
			settings := filepath.Join(project, ".opencode/opencode.json")
			var payload string
			for _, action := range result.Plan.Writes {
				if action.Delivery != nil && action.SourcePack == "first" {
					payload = action.Dst
				}
			}
			if payload == "" {
				t.Fatal("fixture did not deliver a portable payload")
			}
			beforeHook, beforeSettings, beforeLedger := mustRead(t, wrapper), mustRead(t, settings), mustRead(t, result.Plan.Ledger)
			if phase == "payload-edit" {
				writeFile(t, filepath.Join(payload, "notes.txt"), "local edit")
				for _, dryRun := range []bool{true, false} {
					_, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: cfgDir, Name: "first", Registry: testRegistry(), DryRun: dryRun}, io.Discard)
					if err == nil || !strings.Contains(err.Error(), "local changes") {
						t.Fatalf("expected ownership refusal (dry=%v): %v", dryRun, err)
					}
				}
			} else {
				failurePath := result.Plan.Ledger
				if phase == "hook-write" {
					failurePath = wrapper
				}
				eng.FS = nativeLedgerFailure{Path: failurePath}
				if _, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: cfgDir, Name: "first", Registry: testRegistry()}, io.Discard); err == nil {
					t.Fatal("expected persistence failure")
				}
				eng.FS = engine.OSFS{}
			}
			if !bytes.Equal(beforeHook, mustRead(t, wrapper)) || !bytes.Equal(beforeSettings, mustRead(t, settings)) || !bytes.Equal(beforeLedger, mustRead(t, result.Plan.Ledger)) {
				t.Fatal("failed deletion changed shared hooks, activation or ownership")
			}
			if _, err := os.Stat(payload); err != nil {
				t.Fatal("failed deletion lost the payload", err)
			}
		})
	}
}

func TestGenericImportedPackLifecycleReferences(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	if runtime.GOOS == "windows" {
		t.Skip("generic stdio MCP translation requires POSIX")
	}
	for _, target := range []domain.Harness{domain.HarnessOpenCode} {
		for _, scope := range []domain.Scope{domain.ScopeProject, domain.ScopeGlobal} {
			for _, action := range []string{"rename", "delete", "modified-rename", "modified-delete"} {
				if target == domain.HarnessClaudeCode && strings.Contains(action, "delete") {
					continue
				}
				t.Run(fmt.Sprintf("%s/%s/%s", target, scope, action), func(t *testing.T) {
					t.Setenv("CLINE_DIR", "")
					t.Setenv("CLINE_DATA_DIR", "")
					t.Setenv("OPENCODE_CONFIG_DIR", "")
					dir, home, project := t.TempDir(), t.TempDir(), t.TempDir()
					cfg := config.ProfileConfig{}
					for _, name := range []string{"first", "second"} {
						src := t.TempDir()
						writeFile(t, filepath.Join(src, ".codex-plugin/plugin.json"), `{"name":"`+name+`","version":"1.0.0"}`)
						writeFile(t, filepath.Join(src, "skills", name+"-review", "SKILL.md"), "---\nname: "+name+"-review\ndescription: Check source assets\n---\nRun ${PLUGIN_ROOT}/scripts/server.js\n")
						writeFile(t, filepath.Join(src, ".mcp.json"), `{"mcpServers":{"`+name+`-server":{"command":"node","args":["scripts/server.js"],"cwd":"./"}}}`)
						writeFile(t, filepath.Join(src, "scripts/server.js"), `console.log("`+name+`-server-ran")`)
						if target != domain.HarnessClaudeCode {
							writeFile(t, filepath.Join(src, "hooks/hooks.json"), `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"node -e \"require(process.env.PLUGIN_ROOT+'/scripts/hook.js')\""}]}]}}`)
							writeFile(t, filepath.Join(src, "scripts/hook.js"), `console.log(JSON.stringify({additional_context:"`+name+`-hook-ran"}))`)
						}
						if err := PackInstall(context.Background(), PackInstallRequest{ConfigDir: dir, PackPath: src, Name: name, Plugin: &domain.PluginSource{Format: plugin.CodexLegacy, Name: name, Marketplace: "owned"}}, nil); err != nil {
							t.Fatal(err)
						}
						cfg.Packs = append(cfg.Packs, config.PackEntry{Name: name})
					}
					eng := engine.New(nil, nil)
					profile, _, err := eng.Resolve(cfg, "", dir, config.CollisionError, nil)
					if err != nil {
						t.Fatal(err)
					}
					env := map[string]string{"CLAUDE_CONFIG_DIR": "", "OPENCODE_CONFIG_DIR": ""}
					if scope == domain.ScopeGlobal && strings.HasSuffix(action, "rename") {
						key := map[domain.Harness]string{domain.HarnessClaudeCode: "CLAUDE_CONFIG_DIR", domain.HarnessOpenCode: "OPENCODE_CONFIG_DIR", domain.HarnessCline: "CLINE_DIR"}[target]
						root := filepath.Join(home, "custom-config")
						env[key] = root
					}
					req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: dir, Home: home, ProjectDir: project, Scope: scope, Harnesses: []domain.Harness{target}, Env: env}, Yes: true}
					result, _, err := RunSync(context.Background(), eng, profile, req, testRegistry(), nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					var wrapper, skill, settings string
					for _, write := range result.Plan.Writes {
						if write.Category == domain.CategoryHooks && (filepath.Base(write.Dst) == "aipack-hooks.js" || strings.TrimSuffix(filepath.Base(write.Dst), ".ps1") == "UserPromptSubmit") {
							wrapper = write.Dst
						}
						if write.Category == domain.CategorySkills && write.SourcePack == "first" {
							skill = write.Dst
						}
					}
					for _, server := range result.Plan.MCPServers {
						if server.Name == "first-server" {
							settings = server.ConfigPath
						}
					}
					if skill == "" || settings == "" || target != domain.HarnessClaudeCode && wrapper == "" {
						t.Fatal("fixture did not use ordinary delivery")
					}
					oldRoot, newRoot := filepath.Join(PacksDir(dir), "first"), filepath.Join(PacksDir(dir), "renamed")
					if strings.HasPrefix(action, "modified-") {
						path := skill
						if strings.HasSuffix(action, "delete") {
							path = wrapper
						}
						writeFile(t, path, string(mustRead(t, path))+"\nLOCAL_EDIT\n")
					}
					beforeSkill, beforeSettings := mustRead(t, skill), mustRead(t, settings)
					if strings.HasSuffix(action, "rename") {
						err = PackRename(eng, dir, "first", "renamed", io.Discard)
					} else {
						preview, previewErr := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: dir, Name: "first", Registry: testRegistry(), DryRun: true}, io.Discard)
						if action == "delete" && (previewErr != nil || preview.LedgerCleared == 0 || !bytes.Equal(beforeSettings, mustRead(t, settings))) {
							t.Fatalf("delete preview: %+v %v", preview, previewErr)
						}
						_, err = PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: dir, Name: "first", Registry: testRegistry()}, io.Discard)
					}
					if strings.HasPrefix(action, "modified-") {
						if err == nil || !strings.Contains(err.Error(), "local changes") || !bytes.Equal(beforeSkill, mustRead(t, skill)) || !bytes.Equal(beforeSettings, mustRead(t, settings)) {
							t.Fatalf("local edit was not preserved: %v", err)
						}
						if _, err := os.Stat(oldRoot); err != nil {
							t.Fatal("refused mutation moved the source", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if _, err := os.Stat(oldRoot); !os.IsNotExist(err) {
						t.Fatalf("old source remains: %v", err)
					}
					if action == "rename" {
						actual := mustRead(t, skill)
						if bytes.Contains(actual, []byte(oldRoot)) || !bytes.Contains(actual, []byte(newRoot)) {
							t.Fatalf("skill retained stale source paths: %s", actual)
						}
						root := map[string]any{}
						if err := json.Unmarshal(mustRead(t, settings), &root); err != nil {
							t.Fatal(err)
						}
						key := "mcpServers"
						if target == domain.HarnessOpenCode {
							key = "mcp"
						}
						servers := root[key].(map[string]any)
						server := servers["first-server"].(map[string]any)
						argv := []string{}
						if command, ok := server["command"].([]any); ok {
							for _, arg := range command {
								argv = append(argv, arg.(string))
							}
						} else {
							argv = append(argv, server["command"].(string))
							for _, arg := range server["args"].([]any) {
								argv = append(argv, arg.(string))
							}
						}
						cmd := exec.Command(argv[0], argv[1:]...)
						if out, err := cmd.CombinedOutput(); err != nil || !bytes.Contains(out, []byte("first-server-ran")) {
							t.Fatalf("renamed MCP launcher failed: %s %v", out, err)
						}
						cfg.Packs[0].Name = "renamed"
					} else {
						cfg.Packs = cfg.Packs[1:]
					}
					if wrapper != "" {
						actual := mustRead(t, wrapper)
						if bytes.Contains(actual, []byte(oldRoot)) || !bytes.Contains(actual, []byte(filepath.Join(PacksDir(dir), "second"))) {
							t.Fatalf("shared wrapper retained deleted references or lost sibling: %s", actual)
						}
						var cmd *exec.Cmd
						if target == domain.HarnessOpenCode {
							cmd = exec.Command("node", "--input-type=module", "-e", fmt.Sprintf(`const p=await import(%q); const s=await p.default.server({directory:%q}); const out={parts:[]}; await s["chat.message"]({sessionID:"probe",messageID:"probe"},out); console.log(JSON.stringify(out));`, "file://"+filepath.ToSlash(wrapper), home))
						} else if runtime.GOOS != "windows" {
							cmd = exec.Command("node", wrapper)
							cmd.Stdin = strings.NewReader(`{"taskId":"probe","userPromptSubmit":{"prompt":"run"}}`)
						}
						if cmd != nil {
							cmd.Dir = home
							out, err := cmd.CombinedOutput()
							if err != nil || !bytes.Contains(out, []byte("second-hook-ran")) || action == "delete" && bytes.Contains(out, []byte("first-hook-ran")) || action == "rename" && !bytes.Contains(out, []byte("first-hook-ran")) {
								t.Fatalf("hook execution after %s: %s %v", action, out, err)
							}
						}
					}
					profile, _, err = eng.Resolve(cfg, "", dir, config.CollisionError, nil)
					if err != nil {
						t.Fatal(err)
					}
					_, warnings, err := RunSync(context.Background(), eng, profile, req, testRegistry(), nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					for _, warning := range warnings {
						if strings.Contains(warning.Message, "conflict") {
							t.Fatal("lifecycle lost managed baseline", warning)
						}
					}
				})
			}
		}
	}
}

// The generated inventory is also used by Cline's Windows wrapper.
func TestRewritePowerShellHookPack(t *testing.T) {
	entries := `[ {"label":"first/hook","pluginRoot":"/packs/first/upstream"}, {"label":"second/hook","pluginRoot":"/packs/second/upstream"} ]`
	content := []byte(`$handlersJson = [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String("` + base64.StdEncoding.EncodeToString([]byte(entries)) + `"))`)
	body, owner, changed, err := harness.RewriteHookPack(content, "first", "", "", "")
	if err != nil || !changed || owner != "second" {
		t.Fatalf("PowerShell removal: %s %s %v %v", body, owner, changed, err)
	}
	if _, _, changed, err := harness.RewriteHookPack(body, "first", "", "", ""); err != nil || changed {
		t.Fatalf("PowerShell removal did not converge: %v %v", changed, err)
	}
}

func TestGenericHooksRefuseForeignInstallation(t *testing.T) {
	for _, target := range []domain.Harness{domain.HarnessCline, domain.HarnessOpenCode} {
		for _, composite := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/composite=%t", target, composite), func(t *testing.T) {
				dir, home, project := t.TempDir(), t.TempDir(), t.TempDir()
				names := []string{"probe"}
				if composite {
					names = append(names, "other")
				}
				cfg := config.ProfileConfig{}
				for _, name := range names {
					src := t.TempDir()
					writeFile(t, filepath.Join(src, ".codex-plugin/plugin.json"), `{"name":"`+name+`","version":"1.0.0"}`)
					writeFile(t, filepath.Join(src, "hooks/hooks.json"), `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"true"}]}]}}`)
					if err := PackInstall(context.Background(), PackInstallRequest{ConfigDir: dir, PackPath: src, Name: name, Plugin: &domain.PluginSource{Format: plugin.CodexLegacy, Name: name, Marketplace: "owned"}}, nil); err != nil {
						t.Fatal(err)
					}
					cfg.Packs = append(cfg.Packs, config.PackEntry{Name: name})
				}
				var foreignPath, wrapper string
				if target == domain.HarnessCline {
					foreignPath = filepath.Join(home, ".agents/plugins/foreign/plugin.json")
					writeFile(t, foreignPath, `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe","version":"1.0.0"}`)
					wrapper = filepath.Join(project, ".clinerules/hooks/UserPromptSubmit")
				} else {
					foreignPath = filepath.Join(home, ".config/opencode/aipack-imports/probe@owned/foreign/payload/asset.txt")
					writeFile(t, foreignPath, "foreign plugin asset")
					wrapper = filepath.Join(project, ".opencode/plugins/aipack-hooks.js")
				}
				before := mustRead(t, foreignPath)
				eng := engine.New(nil, nil)
				p, _, err := eng.Resolve(cfg, "", dir, config.CollisionError, nil)
				if err != nil {
					t.Fatal(err)
				}
				req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: dir, Home: home, ProjectDir: project, Scope: domain.ScopeProject, Harnesses: []domain.Harness{target}}, Quiet: true, Yes: true}
				if _, err := PlanWithDiffs(context.Background(), eng, p, req, testRegistry()); err == nil || !strings.Contains(err.Error(), "outside") {
					t.Fatalf("management preview accepted foreign installation: %v", err)
				}
				for _, dryRun := range []bool{true, false} {
					req.DryRun = dryRun
					if _, _, err := RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "outside") {
						t.Fatalf("hooks-only conversion accepted foreign installation (preview=%t): %v", dryRun, err)
					}
				}
				if !bytes.Equal(before, mustRead(t, foreignPath)) {
					t.Fatal("refusal changed foreign content")
				}
				if runtime.GOOS == "windows" && target == domain.HarnessCline {
					wrapper += ".ps1"
				}
				if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
					t.Fatalf("refusal left imported hooks active: %v", err)
				}
				if _, _, err := RunSync(context.Background(), eng, domain.NewProfile(), req, testRegistry(), nil, nil); err != nil {
					t.Fatal("inactive import blocked foreign installation", err)
				}
			})
		}
	}
}

func TestGenericImportedHookLifecycle(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	for _, target := range []domain.Harness{domain.HarnessOpenCode, domain.HarnessCline} {
		t.Run(string(target), func(t *testing.T) {
			if runtime.GOOS == "windows" && target == domain.HarnessCline {
				t.Skip("POSIX-authored imported commands; Windows handlers are tested by the PowerShell wrapper suite")
			}
			src, dir, home, project := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(src, ".codex-plugin/plugin.json"), `{"name":"probe","version":"1.0.0"}`)
			writeFile(t, filepath.Join(src, "hooks/hooks.json"), `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"node -e \"require(process.env.PLUGIN_ROOT+'/scripts/hook.js')\"","timeout":5}]}],"UserPromptSubmit":[{"hooks":[{"type":"command","command":"node -e \"require(process.env.CODEX_PLUGIN_ROOT+'/scripts/hook.js')\"","timeout":5}]}],"PreToolUse":[{"hooks":[{"type":"command","command":"node -e \"require(process.env.CLAUDE_PLUGIN_ROOT+'/scripts/hook.js')\"","timeout":5}]}],"Stop":[{"hooks":[{"type":"command","command":"false"}]}]}}`)
			script := `const fs = require("node:fs");
const path = require("node:path");
const p = JSON.parse(fs.readFileSync(0, "utf8"));
if (!p.cwd || !p.session_id || !p.hook_event_name) throw new Error("missing native input");
if (fs.realpathSync(process.cwd()) !== fs.realpathSync(p.cwd)) throw new Error("lost project working directory");
if (p.hook_event_name === "UserPromptSubmit" && p.prompt !== "owned prompt") throw new Error("lost prompt");
if (process.env.PLUGIN_ROOT !== process.env.CODEX_PLUGIN_ROOT || process.env.PLUGIN_ROOT !== process.env.CLAUDE_PLUGIN_ROOT) throw new Error("lost root alias");
fs.appendFileSync(path.join(process.env.PLUGIN_DATA, "events"), p.hook_event_name + "\n");
if (p.hook_event_name === "PreToolUse") { console.error("OWNED_DENIAL"); process.exit(2); }
console.log(JSON.stringify({hookSpecificOutput:{hookEventName:p.hook_event_name,additionalContext:"OWNED_GENERIC_V1"}}));
`
			writeFile(t, filepath.Join(src, "scripts/hook.js"), script)
			install := PackInstallRequest{ConfigDir: dir, PackPath: src, Name: "alias", Plugin: &domain.PluginSource{Format: plugin.CodexLegacy, Name: "probe", Marketplace: "owned"}}
			if err := PackInstall(context.Background(), install, nil); err != nil {
				t.Fatal(err)
			}
			eng := engine.New(nil, nil)
			cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "alias"}}}
			req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: dir, Home: home, ProjectDir: project, Scope: domain.ScopeProject, Harnesses: []domain.Harness{target}}, Quiet: true, Yes: true}
			run := func() {
				t.Helper()
				p, _, err := eng.Resolve(cfg, "", dir, config.CollisionError, nil)
				if err != nil {
					t.Fatal(err)
				}
				var notices []domain.Warning
				_, notices, err = RunSync(context.Background(), eng, p, req, testRegistry(), nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, warning := range notices {
					if bytes.Contains([]byte(warning.Message), []byte("Stop has no equivalent")) {
						found = true
					}
				}
				if cfg.Packs[0].Hooks.Enabled == nil && !found {
					t.Fatal("unavailable target hook was not reported")
				}
				if !bytes.Equal(mustRead(t, filepath.Join(src, "scripts/hook.js")), mustRead(t, filepath.Join(dir, "packs/alias/upstream/scripts/hook.js"))) {
					t.Fatal("hook delivery changed imported commands/assets")
				}
			}
			wrapper := filepath.Join(project, ".clinerules/hooks/UserPromptSubmit")
			if runtime.GOOS == "windows" {
				wrapper += ".ps1"
			}
			data := filepath.Join(dir, "plugin-data/probe@owned")
			if target == domain.HarnessOpenCode {
				wrapper = filepath.Join(project, ".opencode/plugins/aipack-hooks.js")
				data = filepath.Join(project, ".opencode/aipack-data/probe@owned")
			}
			req.DryRun = true
			run()
			if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
				t.Fatal("preview delivered hooks")
			}
			req.DryRun = false
			run()
			before := mustRead(t, wrapper)
			p, _, err := eng.Resolve(cfg, "", dir, config.CollisionError, nil)
			if err != nil {
				t.Fatal(err)
			}
			traced, err := RunTrace(context.Background(), eng, p, TraceRequest{TargetSpec: req.TargetSpec, ResourceType: "hook", ResourceName: "codex-user-prompt-submit", PackName: "alias"}, testRegistry())
			if err != nil || traced.Source == nil || traced.Source.NativeBinding != "probe@owned" || len(traced.Destinations) == 0 {
				t.Fatalf("imported hook lost original source/delivery provenance: %+v %v", traced, err)
			}
			run()
			if !bytes.Equal(before, mustRead(t, wrapper)) {
				t.Fatal("repeat sync changed hook wrapper")
			}
			dispatch := func(marker string) {
				t.Helper()
				var cmd *exec.Cmd
				if target == domain.HarnessCline {
					cmd = exec.Command("node", wrapper)
					if runtime.GOOS == "windows" {
						cmd = exec.Command("powershell.exe", "-NoProfile", "-File", wrapper)
					}
					payload, _ := json.Marshal(map[string]any{"taskId": "owned-session", "workspaceRoots": []string{project}, "userPromptSubmit": map[string]any{"prompt": "owned prompt"}})
					cmd.Stdin = bytes.NewReader(payload)
				} else {
					cmd = exec.Command("node", "--input-type=module", "-e", `import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
const [file, cwd, marker] = process.argv.slice(1);
const plugin = (await import(pathToFileURL(file))).default;
const hooks = await plugin.server({directory:cwd});
const system = {system:[]};
await hooks['experimental.chat.system.transform']({sessionID:'owned-session'}, system);
assert(system.system.join('\n').includes(marker));
const out = {parts:[{type:'text',text:'owned prompt'}]};
await hooks['chat.message']({sessionID:'owned-session',messageID:'owned-message'}, out);
assert(out.parts.some(part => part.text === marker));
await assert.rejects(hooks['tool.execute.before']({sessionID:'owned-session',tool:'bash',callID:'owned-call'}, {args:{command:'false'}}), /OWNED_DENIAL/);
console.log(marker);
`, wrapper, project, marker)
				}
				cmd.Dir = project
				if target == domain.HarnessOpenCode {
					cmd.Dir = home // A server's process cwd can differ from the active project.
				}
				out, err := cmd.CombinedOutput()
				if err != nil || !bytes.Contains(out, []byte(marker)) {
					t.Fatalf("imported hook command/input/output: %v\n%s", err, out)
				}
				if target == domain.HarnessCline {
					cmd = exec.Command("node", filepath.Join(project, ".clinerules/hooks/PreToolUse"))
					if runtime.GOOS == "windows" {
						cmd = exec.Command("powershell.exe", "-NoProfile", "-File", filepath.Join(project, ".clinerules/hooks/PreToolUse.ps1"))
					}
					cmd.Dir = project
					cmd.Stdin = bytes.NewBufferString(`{"taskId":"owned-session","preToolUse":{"toolName":"execute_command","parameters":{"command":"false"}}}`)
					out, err = cmd.CombinedOutput()
					if err != nil || !bytes.Contains(out, []byte(`"cancel":true`)) || !bytes.Contains(out, []byte("OWNED_DENIAL")) {
						t.Fatalf("blocking output lost: %v %s", err, out)
					}
					if module := os.Getenv("AIPACK_TEST_CLINE_CORE_MODULE"); module != "" {
						cmd = exec.Command("node", "--input-type=module", "-e", `import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
const [module, cwd, marker] = process.argv.slice(1);
const {createHookConfigFileHooks} = await import(pathToFileURL(module));
const hooks = createHookConfigFileHooks({cwd,workspacePath:cwd,rootSessionId:'owned-session',blockingRunStartHooks:true,detachAsyncHooks:false});
assert(hooks?.beforeRun, 'native Cline did not discover the managed hooks');
const value = await hooks.beforeRun({snapshot:{agentId:'owned-agent',conversationId:'owned-session',runId:'owned-run',status:'running',iteration:0,messages:[{role:'user',content:[{type:'text',text:'owned prompt'}]}],pendingToolCalls:[],usage:{}}});
assert(value?.appendContext?.includes(marker), 'native Cline discarded imported context');
console.log(marker);
`, module, project, marker)
						cmd.Dir = project
						cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
						out, err = cmd.CombinedOutput()
						if err != nil || !bytes.Contains(out, []byte(marker)) {
							t.Fatalf("native Cline hook discovery/dispatch: %v %s", err, out)
						}
					}
				}
			}
			dispatch("OWNED_GENERIC_V1")
			if target == domain.HarnessOpenCode && os.Getenv("AIPACK_TEST_OPENCODE_NATIVE") == "1" {
				checkNativeOpenCodeHooks(t, project, home)
			}
			disabled := false
			cfg.Packs[0].Hooks.Enabled = &disabled
			run()
			if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
				t.Fatal("hooks-off retained the managed runner")
			}
			cfg.Packs[0].Hooks.Enabled = nil
			run()
			dispatch("OWNED_GENERIC_V1")
			writeFile(t, filepath.Join(src, "scripts/hook.js"), string(bytes.ReplaceAll([]byte(script), []byte("OWNED_GENERIC_V1"), []byte("OWNED_GENERIC_V2"))))
			if err := PackInstall(context.Background(), install, nil); err != nil {
				t.Fatal(err)
			}
			run()
			dispatch("OWNED_GENERIC_V2")
			if _, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: dir, Name: "alias", Registry: testRegistry()}, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
				t.Fatal("deletion retained hook activation")
			}
			if _, err := os.Stat(filepath.Join(data, "events")); err != nil {
				t.Fatal("deletion discarded runtime data")
			}
		})
	}
}

func checkNativeOpenCodeHooks(t *testing.T, project, home string) {
	t.Helper()
	var mu sync.Mutex
	var requests [][]byte
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, delta := range []map[string]any{{"content": "OWNED_RESPONSE"}, {}} {
			var reason any
			if len(delta) == 0 {
				reason = "stop"
			}
			chunk, _ := json.Marshal(map[string]any{"id": "owned", "object": "chat.completion.chunk", "created": 1, "model": "fixture", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer api.Close()
	target := filepath.Join(project, ".opencode")
	config := map[string]any{"autoupdate": false, "share": "disabled", "enabled_providers": []string{"owned"}, "provider": map[string]any{"owned": map[string]any{"npm": "@ai-sdk/openai-compatible", "options": map[string]any{"baseURL": api.URL + "/v1", "apiKey": "owned-offline-fixture"}, "models": map[string]any{"fixture": map[string]any{"name": "fixture", "limit": map[string]int{"context": 32000, "output": 1024}}}}}}
	encoded, _ := json.Marshal(config)
	writeFile(t, filepath.Join(target, "opencode.json"), string(encoded))
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "opencode", "run", "--print-logs", "--log-level", "DEBUG", "--model", "owned/fixture", "--format", "json", "owned prompt")
	cmd.Dir, cmd.WaitDelay = project, time.Second
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_DATA_HOME=" + filepath.Join(home, ".local/share"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "XDG_STATE_HOME=" + filepath.Join(home, ".state"), "OPENCODE_CONFIG_DIR=" + target, "OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_DISABLE_MODELS_FETCH=1", "OPENCODE_DISABLE_EXTERNAL_SKILLS=1", "OPENCODE_DISABLE_PROJECT_CONFIG=1", "HTTP_PROXY=" + api.URL, "HTTPS_PROXY=" + api.URL, "NO_PROXY=127.0.0.1,localhost"}
	// The generated runner uses Node builtins; fail optional host dependency
	// acquisition promptly instead of waiting for an unreachable npm registry.
	cmd.Env = append(cmd.Env, "npm_config_offline=true", "npm_config_fetch_retries=0", "npm_config_fetch_timeout=1000")
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("OWNED_RESPONSE")) {
		t.Fatalf("native OpenCode hook discovery/dispatch: %v %s", err, out)
	}
	mu.Lock()
	joined := bytes.Join(requests, nil)
	mu.Unlock()
	if bytes.Count(joined, []byte("OWNED_GENERIC_V1")) < 2 {
		t.Fatalf("native startup/prompt hook context did not reach the model: %s", joined)
	}
}
