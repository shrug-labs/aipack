package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/plugin"
	"github.com/shrug-labs/aipack/internal/util"
)

func TestPortableRecoveryProtectsOwnedState(t *testing.T) {
	for _, scenario := range []string{"cold-partial-package", "update-partial-package", "user-edits", "edited-activation", "changed-snapshot", "unowned-path", "unsafe-active-path"} {
		t.Run(scenario, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = util.RemoveOwnedTree(root) })
			cfgDir, home, target := filepath.Join(root, "config"), filepath.Join(root, "home"), filepath.Join(root, "native")
			nativePolicy := map[string]config.MCPServerConfig{"probe": {DisabledTools: []string{"hidden"}}}
			cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "one", MCP: nativePolicy}, {Name: "two", MCP: nativePolicy}}}
			eng := engine.New(nil, nil)
			req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: root, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessOpenCode}, Env: map[string]string{"OPENCODE_CONFIG_DIR": target}}, Yes: true, Quiet: true}
			install := func(body string) {
				t.Helper()
				for _, name := range []string{"one", "two"} {
					source := filepath.Join(root, "source", name)
					writeFile(t, filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"`+name+`","version":"1.0.0"}`)
					writeFile(t, filepath.Join(source, "skills/first/SKILL.md"), "---\nname: first\ndescription: Owned fixture\n---\n"+body+"\n")
					writeFile(t, filepath.Join(source, ".mcp.json"), `{"mcpServers":{"probe":{"command":"false","cwd":"."}}}`)
					if err := PackInstall(context.Background(), PackInstallRequest{PackPath: source, ConfigDir: cfgDir, Name: name, Plugin: &domain.PluginSource{Format: plugin.CodexLegacy, Name: name, Marketplace: "market"}}, nil); err != nil {
						t.Fatal(err)
					}
				}
			}
			resolve := func() domain.Profile {
				t.Helper()
				p, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
			run := func() (SyncResult, error) {
				result, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil)
				return result, err
			}
			settings := filepath.Join(target, "opencode.json")
			writeFile(t, settings, `{"theme":"initial","user_deleted":true,"skills":{"paths":["user-path"]}}`)
			install("BEFORE")
			ledgerPath := engine.LedgerPath(cfgDir, domain.ScopeGlobal, root, domain.HarnessOpenCode)
			var previous SyncResult
			if scenario != "cold-partial-package" {
				previous, err = run()
				if err != nil {
					t.Fatal(err)
				}
				install("AFTER")
			}
			beforeSettings := mustRead(t, settings)
			beforeLedger, _ := os.ReadFile(ledgerPath)
			if strings.Contains(scenario, "partial-package") {
				eng.FS = portablePackageFailure{Binding: "two@market"}
				if _, err := run(); err == nil {
					t.Fatal("second package failure was ignored")
				}
				eng.FS = engine.OSFS{}
			} else {
				eng.FS = nativeLedgerInterruption{Path: ledgerPath}
				func() {
					defer func() {
						if got := recover(); got != "native ledger interruption" {
							t.Fatalf("expected interrupted apply, got %v", got)
						}
					}()
					_, _ = run()
				}()
				eng.FS = engine.OSFS{}
				var op nativeOperation
				if err := json.Unmarshal(mustRead(t, filepath.Join(nativeOperationDir(cfgDir), "operation.json")), &op); err != nil {
					t.Fatal(err)
				}
				newPath := ""
				for path, snapshot := range op.Portable.Payloads {
					if !snapshot.Before.Exists && snapshot.Delivery.Binding == "one@market" {
						newPath = path
					}
				}
				if newPath == "" {
					t.Fatal("journal omitted the new payload")
				}
				interruptedSettings := mustRead(t, settings)
				user := map[string]any{}
				_ = util.UnmarshalJSON(interruptedSettings, &user)
				switch scenario {
				case "user-edits":
					user["theme"] = "changed"
					delete(user, "user_deleted")
					writeFile(t, filepath.Join(newPath, "notes.txt"), "retain interrupted user payload edit")
				case "edited-activation":
					user["mcp"].(map[string]any)["one@market:probe"].(map[string]any)["command"] = []string{"user-edited"}
				case "changed-snapshot":
					for path, snapshot := range op.Portable.Payloads {
						if snapshot.Before.Exists {
							writeFile(t, filepath.Join(portableBackupPath(cfgDir, path), "skills/first/SKILL.md"), "corrupted backup")
							break
						}
					}
				case "unowned-path":
					op.Portable.Payloads[filepath.Join(root, "outside")] = op.Portable.Payloads[newPath]
					if err := saveNativeOperation(cfgDir, &op); err != nil {
						t.Fatal(err)
					}
				case "unsafe-active-path":
					outside := filepath.Join(root, "outside")
					writeFile(t, filepath.Join(outside, "retained.txt"), "outside payload ownership")
					if err := util.RemoveOwnedTree(newPath); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, newPath); err != nil {
						t.Fatal(err)
					}
				}
				body, _ := util.MarshalPrettyJSON(user)
				writeFile(t, settings, string(body))
				err := recoverNativeOperation(cfgDir)
				if scenario == "changed-snapshot" || scenario == "unowned-path" || scenario == "edited-activation" || scenario == "unsafe-active-path" {
					if err == nil || !bytes.Equal(body, mustRead(t, settings)) {
						t.Fatalf("unsafe recovery changed activation: %v", err)
					}
					if _, err := os.Stat(newPath); err != nil {
						t.Fatal("refused recovery removed active payload", err)
					}
					if scenario != "edited-activation" {
						return
					}
					writeFile(t, settings, string(interruptedSettings))
					if err := recoverNativeOperation(cfgDir); err != nil {
						t.Fatal("recovery retry failed", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if scenario == "user-edits" {
					retained := filepath.Join(cfgDir, ".tmp/native-recoveries", op.ID, "interrupted-portable", util.ContentDigest([]byte(newPath)), "notes.txt")
					if string(mustRead(t, retained)) != "retain interrupted user payload edit" {
						t.Fatal("recovery discarded edited payload")
					}
					var restored map[string]any
					_ = util.UnmarshalJSON(mustRead(t, settings), &restored)
					if restored["theme"] != "changed" || restored["user_deleted"] != nil {
						t.Fatalf("recovery overwrote unrelated user edits: %+v", restored)
					}
				}
			}
			ledger, _ := os.ReadFile(ledgerPath)
			if !bytes.Equal(beforeLedger, ledger) {
				t.Fatal("rollback changed the prior ledger")
			}
			if scenario != "user-edits" && !bytes.Equal(beforeSettings, mustRead(t, settings)) {
				// A cold file was user-authored compact JSON; compare its values.
				var before, after map[string]any
				_ = util.UnmarshalJSON(beforeSettings, &before)
				_ = util.UnmarshalJSON(mustRead(t, settings), &after)
				encodedBefore, _ := json.Marshal(before)
				encodedAfter, _ := json.Marshal(after)
				if !bytes.Equal(encodedBefore, encodedAfter) {
					t.Fatal("rollback changed prior settings values")
				}
			}
			for _, action := range previous.Plan.Writes {
				if action.Delivery != nil && !bytes.Contains(mustRead(t, filepath.Join(action.Dst, "skills/first/SKILL.md")), []byte("BEFORE")) {
					t.Fatal("rollback failed to restore a prior payload")
				}
			}
			if _, err := run(); err != nil {
				t.Fatal("retry failed", err)
			}
		})
	}
}

type portablePackageFailure struct {
	engine.OSFS
	Binding string
}

func (f portablePackageFailure) WritePackage(path string, files []domain.NativePluginFile) error {
	if strings.Contains(filepath.ToSlash(path), "/"+f.Binding+"/") {
		return fmt.Errorf("owned second-package write failure")
	}
	return f.OSFS.WritePackage(path, files)
}

func TestPortableRemovalRecovery(t *testing.T) {
	for _, kind := range []string{"clean", "delete"} {
		for _, phase := range []string{"settings", "payload", "ledger", "before-ledger", "after-ledger", "payload-edit", "activation-edit", "foreign", "missing-owner", "native-owner", "sibling-edit", "handover"} {
			if kind == "clean" && phase == "sibling-edit" {
				continue
			}
			t.Run(kind+"/"+phase, func(t *testing.T) {
				root, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = util.RemoveOwnedTree(root) })
				cfgDir, home, target := filepath.Join(root, "config"), filepath.Join(root, "home"), filepath.Join(root, "native")
				eng := engine.New(nil, nil)
				nativePolicy := map[string]config.MCPServerConfig{"probe": {DisabledTools: []string{"hidden"}}}
				cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "one", MCP: nativePolicy}, {Name: "two", MCP: nativePolicy}}}
				for _, name := range []string{"one", "two"} {
					source := filepath.Join(root, "source", name)
					format, manifest, mcp := plugin.CodexLegacy, ".codex-plugin/plugin.json", ".mcp.json"
					body := `{"name":"one","version":"1.0.0"}`
					server := `{"mcpServers":{"probe":{"command":"false","cwd":"."}}}`
					if name == "two" {
						format, manifest, mcp = plugin.AgentPlugins, "plugin.json", "mcp.json"
						body = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"two","version":"1.0.0"}`
						server = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"probe":{"type":"stdio","command":"false"}}}`
					}
					writeFile(t, filepath.Join(source, manifest), body)
					writeFile(t, filepath.Join(source, mcp), server)
					writeFile(t, filepath.Join(source, "skills/first/SKILL.md"), "---\nname: first\ndescription: Owned fixture\n---\nOWNED_BODY\n")
					if err := PackInstall(context.Background(), PackInstallRequest{PackPath: source, ConfigDir: cfgDir, Name: name, Plugin: &domain.PluginSource{Format: format, Name: name, Marketplace: "market"}}, nil); err != nil {
						t.Fatal(err)
					}
				}
				p, _, err := eng.Resolve(cfg, "", cfgDir, config.CollisionError, nil)
				if err != nil {
					t.Fatal(err)
				}
				spec := TargetSpec{ConfigDir: cfgDir, Home: home, ProjectDir: root, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessOpenCode}, Env: map[string]string{"OPENCODE_CONFIG_DIR": target}}
				settings := filepath.Join(target, "opencode.json")
				writeFile(t, settings, `{"theme":"user"}`)
				result, _, err := RunSync(context.Background(), eng, p, SyncRequest{TargetSpec: spec, Yes: true, Quiet: true}, testRegistry(), nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				payloads := map[string]string{}
				for _, action := range result.Plan.Writes {
					if action.Delivery != nil {
						payloads[action.SourcePack] = action.Dst
						writeFile(t, filepath.Join(action.Delivery.DataDir, "retained.txt"), "retained runtime data")
					}
				}
				foreign := filepath.Join(target, "aipack-imports", "outside@market", strings.Repeat("a", 64), "payload")
				user := map[string]any{}
				_ = util.UnmarshalJSON(mustRead(t, settings), &user)
				switch phase {
				case "payload-edit":
					writeFile(t, filepath.Join(payloads["one"], "notes.txt"), "preserve edited owned payload")
				case "activation-edit":
					user["mcp"].(map[string]any)["one@market:probe"].(map[string]any)["command"] = []string{"user-edited"}
				case "foreign":
					writeFile(t, filepath.Join(foreign, "skills/foreign/SKILL.md"), "FOREIGN_BODY")
					paths := user["skills"].(map[string]any)["paths"].([]any)
					user["skills"].(map[string]any)["paths"] = append(paths, filepath.Join(foreign, "skills/foreign"))
					user["mcp"].(map[string]any)["outside@market:probe"] = map[string]any{"type": "local", "command": []string{"false"}, "enabled": false}
				case "missing-owner":
					ledger, _, _ := eng.LoadLedger(result.Plan.Ledger)
					entry := ledger.Managed[settings]
					entry.ManagedOverlay = nil
					ledger.Managed[settings] = entry
					if err := eng.SaveLedger(result.Plan.Ledger, ledger, false); err != nil {
						t.Fatal(err)
					}
				case "native-owner":
					ledger, _, _ := eng.LoadLedger(result.Plan.Ledger)
					ledger.NativePlugins = map[string]domain.NativePluginRecord{"foreign@market": {Harness: domain.HarnessCodex, SourcePack: "one"}}
					if err := eng.SaveLedger(result.Plan.Ledger, ledger, false); err != nil {
						t.Fatal(err)
					}
				case "sibling-edit":
					writeFile(t, filepath.Join(payloads["two"], "notes.txt"), "preserve sibling edit")
					user["mcp"].(map[string]any)["two@market:probe"].(map[string]any)["command"] = []string{"user-edited"}
				}
				body, _ := util.MarshalPrettyJSON(user)
				writeFile(t, settings, string(body))
				if err := os.Chmod(settings, 0o600); err != nil {
					t.Fatal(err)
				}
				beforeSettings, beforeLedger := mustRead(t, settings), mustRead(t, result.Plan.Ledger)
				if phase == "native-owner" {
					for _, dry := range []bool{true, false} {
						if _, _, err := RunSync(context.Background(), eng, p, SyncRequest{TargetSpec: spec, Yes: true, Quiet: true, DryRun: dry}, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "ledger harness") {
							t.Fatalf("sync accepted cross-harness ownership (dry=%v): %v", dry, err)
						}
					}
				}
				remove := func(dry bool) error {
					if kind == "clean" {
						return RunClean(context.Background(), eng, CleanRequest{TargetSpec: spec, Yes: true, DryRun: dry, Stderr: io.Discard}, testRegistry())
					}
					_, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: cfgDir, Name: "one", DryRun: dry, Registry: testRegistry()}, nil)
					return err
				}
				refusal := phase == "payload-edit" || phase == "activation-edit" || phase == "missing-owner" || phase == "native-owner"
				if err := remove(true); (err != nil) != refusal {
					t.Fatalf("dry-run refusal=%v: %v", refusal, err)
				}
				switch phase {
				case "settings":
					eng.FS = nativeLedgerFailure{Path: settings}
				case "ledger":
					eng.FS = nativeLedgerFailure{Path: result.Plan.Ledger}
				case "payload":
					n := 1
					if kind == "clean" {
						n = 2 // Fail after one of the two owned packages was removed.
					}
					calls := 0
					eng.FS = portableRemovalFailure{Calls: &calls, FailAt: n}
				case "before-ledger", "after-ledger":
					eng.FS = nativeLedgerInterruption{Path: result.Plan.Ledger, After: phase == "after-ledger"}
				}
				if phase == "before-ledger" || phase == "after-ledger" {
					func() {
						defer func() {
							if got := recover(); got != "native ledger interruption" {
								t.Fatalf("expected interrupted removal, got %v", got)
							}
						}()
						_ = remove(false)
					}()
					eng.FS = engine.OSFS{}
					if err := remove(true); err == nil || !strings.Contains(err.Error(), "pending") {
						t.Fatalf("preview bypassed pending recovery: %v", err)
					}
					if err := recoverNativeOperation(cfgDir); err != nil {
						t.Fatal(err)
					}
				} else if err := remove(false); err == nil && phase != "foreign" && phase != "sibling-edit" && phase != "handover" {
					t.Fatal("removal ignored the controlled failure or local edit")
				} else if err != nil && (phase == "foreign" || phase == "sibling-edit" || phase == "handover") {
					t.Fatal(err)
				}
				eng.FS = engine.OSFS{}
				if phase != "after-ledger" && phase != "foreign" && phase != "sibling-edit" && phase != "handover" {
					if !bytes.Equal(beforeSettings, mustRead(t, settings)) || !bytes.Equal(beforeLedger, mustRead(t, result.Plan.Ledger)) {
						t.Fatal("failed removal changed activation or ownership")
					}
					for _, path := range payloads {
						if _, err := os.Stat(path); err != nil {
							t.Fatal("failed removal lost active payload", err)
						}
					}
				}
				if refusal {
					return
				}
				if phase != "foreign" && phase != "sibling-edit" && phase != "handover" {
					if err := remove(false); err != nil {
						t.Fatal("removal retry failed", err)
					}
				}
				ledger, _, _ := eng.LoadLedger(result.Plan.Ledger)
				for name, path := range payloads {
					retained := kind == "delete" && name == "two"
					_, statErr := os.Stat(path)
					if retained != (statErr == nil) {
						t.Fatalf("payload retained=%v: %s %v", retained, name, statErr)
					}
					if _, tracked := ledger.Managed[path]; tracked != retained {
						t.Fatal("stale portable payload receipt survived removal")
					}
					mcpKey := domain.MCPLedgerKey(settings, name+"@market:probe")
					if _, tracked := ledger.Managed[mcpKey]; tracked != retained {
						t.Fatal("portable MCP receipt does not match retained ownership")
					}
					if string(mustRead(t, filepath.Join(target, "aipack-data", name+"@market", "retained.txt"))) != "retained runtime data" {
						t.Fatal("removal lost runtime data")
					}
				}
				if phase == "foreign" && (!bytes.Contains(mustRead(t, settings), []byte("outside@market:probe")) || string(mustRead(t, filepath.Join(foreign, "skills/foreign/SKILL.md"))) != "FOREIGN_BODY") {
					t.Fatal("removal discarded foreign activation or payload")
				}
				if phase == "sibling-edit" && (string(mustRead(t, filepath.Join(payloads["two"], "notes.txt"))) != "preserve sibling edit" || !bytes.Contains(mustRead(t, settings), []byte("user-edited"))) {
					t.Fatal("targeted deletion discarded sibling edits")
				}
				if info, err := os.Stat(settings); err != nil || info.Mode().Perm() != 0o600 {
					t.Fatal("removal widened settings permissions", err)
				}
				if phase == "handover" {
					otherConfig := filepath.Join(root, "other-config")
					if err := PackInstall(context.Background(), PackInstallRequest{PackPath: filepath.Join(root, "source/one"), ConfigDir: otherConfig, Name: "one", Plugin: &domain.PluginSource{Format: plugin.CodexLegacy, Name: "one", Marketplace: "market"}}, nil); err != nil {
						t.Fatal(err)
					}
					otherProfile, _, err := eng.Resolve(config.ProfileConfig{Packs: []config.PackEntry{{Name: "one", MCP: nativePolicy}}}, "", otherConfig, config.CollisionError, nil)
					if err != nil {
						t.Fatal(err)
					}
					otherSpec := spec
					otherSpec.ConfigDir = otherConfig
					otherResult, _, err := RunSync(context.Background(), eng, otherProfile, SyncRequest{TargetSpec: otherSpec, Yes: true, Quiet: true}, testRegistry(), nil, nil)
					if err != nil {
						t.Fatal("removed owner prevented handover", err)
					}
					otherSettings, otherLedger := mustRead(t, settings), mustRead(t, otherResult.Plan.Ledger)
					otherPayload := ""
					for _, action := range otherResult.Plan.Writes {
						if action.Delivery != nil {
							otherPayload = filepath.Join(action.Dst, "skills/first/SKILL.md")
						}
					}
					otherBody := mustRead(t, otherPayload)
					if err := RunClean(context.Background(), eng, CleanRequest{TargetSpec: spec, Yes: true, Stderr: io.Discard}, testRegistry()); err != nil {
						t.Fatal("previous owner clean failed", err)
					}
					if kind == "clean" {
						for _, dry := range []bool{true, false} {
							if _, _, err := RunSync(context.Background(), eng, p, SyncRequest{TargetSpec: spec, Yes: true, Quiet: true, DryRun: dry}, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "outside this scope") {
								t.Fatalf("previous owner reclaimed delivery (dry=%v): %v", dry, err)
							}
						}
					}
					// Targeted deletion leaves the sibling active until the old clean.
					if kind == "clean" && !bytes.Equal(otherSettings, mustRead(t, settings)) {
						t.Fatal("previous owner clean changed the new activation")
					}
					if !bytes.Equal(otherLedger, mustRead(t, otherResult.Plan.Ledger)) || !bytes.Equal(otherBody, mustRead(t, otherPayload)) {
						t.Fatal("previous owner changed the new receipt or payload")
					}
					if summary, err := PlanWithDiffs(context.Background(), eng, otherProfile, SyncRequest{TargetSpec: otherSpec}, testRegistry()); err != nil || summary.TotalChanges() != 0 {
						t.Fatalf("handover did not settle: %+v %v", summary, err)
					}
				}
			})
		}
	}
}

type portableRemovalFailure struct {
	engine.OSFS
	Calls  *int
	FailAt int
}

func (f portableRemovalFailure) RemovePackage(path string) error {
	*f.Calls++
	if *f.Calls == f.FailAt {
		return fmt.Errorf("owned package removal failure")
	}
	return f.OSFS.RemovePackage(path)
}
