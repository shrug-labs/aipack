package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/plugin"
)

func TestClaudeNativeOverlapLifecycle(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for overlapping native content lifecycle")
	}
	for _, kind := range []string{"default-command", "default-command-link", "default-command-dir", "default-command-file-alias", "default-command-directory-alias", "default-agent", "default-agent-link", "default-agent-dir", "explicit-agent-link", "explicit-agent-dir", "explicit-skill", "command-alias-original", "command-alias-file-only", "command-alias-directory-only", "catalog-default-command", "catalog-default-agent", "catalog-default-command-dir", "catalog-default-agent-dir"} {
		t.Run(kind, func(t *testing.T) {
			scanKind := strings.TrimPrefix(kind, "catalog-")
			catalogOnly := scanKind != kind
			keepCommand := strings.HasPrefix(scanKind, "default-command") && scanKind != "default-command-link"
			keepAgent := scanKind == "default-agent" || scanKind == "default-agent-dir"
			retainedAlias := kind == "default-command-file-alias" || kind == "default-command-directory-alias"
			root := t.TempDir()
			configDir, home := filepath.Join(root, "config"), filepath.Join(root, "home")
			market, source := filepath.Join(root, "market"), filepath.Join(root, "market/probe")
			path, commandID, agentID, skillID := "commands/ops/shared.md", "ops:shared", "Shared", ""
			manifest := `{"name":"scan-probe","version":"1.0.0","agents":["./commands/ops/shared.md"]}`
			if kind == "default-command-file-alias" || kind == "default-command-directory-alias" {
				manifest = `{"name":"scan-probe","version":"1.0.0","agents":["./links/shared.md"]}`
			}
			if strings.HasPrefix(scanKind, "default-agent") {
				path, commandID, agentID = "agents/ops/shared.md", "command-alias", "ops:Shared"
				manifest = `{"name":"scan-probe","version":"1.0.0","commands":{"command-alias":{"source":"./agents/ops/shared.md"}}}`
				keep := "agents/ops/keep.md"
				if kind == "default-agent-link" {
					keep = "custom/keep.md"
				}
				writeFile(t, filepath.Join(source, keep), "---\nname: Keep\ndescription: Retained native agent.\n---\nKEEP_BODY\n")
			} else if kind == "explicit-skill" {
				path, commandID, agentID, skillID = "custom/skill/SKILL.md", "command-alias", "", "Shared"
				manifest = `{"name":"scan-probe","version":"1.0.0","commands":{"command-alias":{"source":"./custom/skill/SKILL.md"}},"skills":["./custom/skill"],"agents":[]}`
			} else if strings.HasPrefix(kind, "explicit-agent-") {
				path, commandID, agentID = "custom/shared.md", "command-alias", "Shared"
				manifest = `{"name":"scan-probe","version":"1.0.0","commands":{"command-alias":{"source":"./links/shared.md"}},"agents":["./links/shared.md"]}`
			} else if strings.HasPrefix(kind, "command-alias-") {
				path, commandID, agentID = "custom/shared.md", "original", "Shared"
				commands := `"original":{"source":"./custom/shared.md"},"file-link":{"source":"./links/shared.md"},"directory-link":{"source":"./directory/shared.md"}`
				if kind == "command-alias-file-only" {
					commandID = "file-link"
					commands = `"file-link":{"source":"./links/shared.md"},"directory-link":{"source":"./directory/shared.md"}`
				} else if kind == "command-alias-directory-only" {
					commandID = "directory-link"
					commands = `"directory-link":{"source":"./directory/shared.md"},"file-link":{"source":"./links/shared.md"}`
				}
				manifest = `{"name":"scan-probe","version":"1.0.0","commands":{` + commands + `},"agents":["./custom/shared.md"]}`
			}
			if !catalogOnly {
				writeFile(t, filepath.Join(source, ".claude-plugin/plugin.json"), manifest)
			}
			writeFile(t, filepath.Join(source, path), "---\nname: Shared\ndescription: Native shared source.\n---\nORIGINAL_BODY\n")
			if strings.HasPrefix(scanKind, "default-command") {
				keep := "commands/ops/keep.md"
				if kind == "default-command-link" {
					keep = "custom/keep.md"
				}
				writeFile(t, filepath.Join(source, keep), "---\ndescription: Retained native command.\n---\nKEEP_BODY\n")
			}
			link, target := "", ""
			switch kind {
			case "default-command-link":
				link, target = "commands/ops/keep.md", "../../custom/keep.md"
			case "default-agent-link":
				link, target = "agents/ops/keep.md", "../../custom/keep.md"
			case "explicit-agent-link":
				link, target = "links/shared.md", "../custom/shared.md"
			case "default-command-file-alias":
				link, target = "links/shared.md", "../commands/ops/shared.md"
			case "default-command-directory-alias":
				link, target = "links", "commands/ops"
			}
			if strings.HasPrefix(kind, "command-alias-") {
				link, target = "links/shared.md", "../custom/shared.md"
				if err := os.Symlink("custom", filepath.Join(source, "directory")); err != nil {
					t.Fatal(err)
				}
			}
			if link != "" {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(source, link)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(source, link)); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasSuffix(kind, "-dir") {
				dir, target := "links", "custom"
				if scanKind == "default-command-dir" {
					dir = "commands"
				} else if scanKind == "default-agent-dir" {
					dir = "agents"
				}
				if dir != "links" {
					if err := os.Rename(filepath.Join(source, dir), filepath.Join(source, target)); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, filepath.Join(source, dir)); err != nil {
					t.Fatal(err)
				}
			}
			catalog := filepath.Join(market, ".claude-plugin/marketplace.json")
			catalogEntry := `{"name":"scan-probe","source":"./probe"}`
			if catalogOnly {
				catalogEntry = strings.TrimSuffix(manifest, "}") + `,"source":"./probe"}`
			}
			writeFile(t, catalog, `{"name":"scan-market","owner":{"name":"shrug-labs"},"plugins":[`+catalogEntry+`]}`)
			if err := RegistryFetch(context.Background(), RegistryFetchRequest{ConfigDir: configDir, URL: catalog}, nil); err != nil {
				t.Fatal(err)
			}
			entry, err := RegistryLookup(RegistryListRequest{ConfigDir: configDir}, "scan-probe")
			if err != nil {
				t.Fatal(err)
			}
			if err := PackInstall(context.Background(), PackInstallRequestFromRegistryEntry(configDir, "alias", entry), nil); err != nil {
				t.Fatal(err)
			}
			shown, err := PackShow(configDir, "alias")
			if err != nil || !slices.Contains(shown.Workflows, commandID) || (agentID != "" && !slices.Contains(shown.Agents, agentID)) || (skillID != "" && !slices.Contains(shown.Skills, skillID)) {
				t.Fatalf("named overlap installation lost inventory: %+v %v", shown, err)
			}
			if (kind == "default-command-link" && slices.Contains(shown.Workflows, "ops:keep")) || (kind == "default-agent-link" && slices.Contains(shown.Agents, "ops:Keep")) {
				t.Fatal("named install lists a symlink ignored by native scanning")
			}
			eng := engine.New(nil, nil)
			cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "alias"}}}
			req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: home, ProjectDir: root, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessClaudeCode}, Env: map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(home, "native")}}, Yes: true, Quiet: true}
			resolve := func() domain.Profile {
				t.Helper()
				profile, _, err := eng.Resolve(cfg, "", configDir, config.CollisionError, nil)
				if err != nil {
					t.Fatal(err)
				}
				return profile
			}
			run := func() (SyncResult, domain.NativePluginRecord) {
				t.Helper()
				result, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				ledger, _, err := eng.LoadLedger(result.Plan.Ledger)
				if err != nil {
					t.Fatal(err)
				}
				return result, ledger.NativePlugins["scan-probe@scan-market"]
			}
			selectContent := func(command, other bool) {
				cfg.Packs[0].Workflows = config.VectorSelector{}
				cfg.Packs[0].Agents = config.VectorSelector{}
				cfg.Packs[0].Skills = config.VectorSelector{}
				if !command {
					exclude := []string{commandID}
					if strings.HasPrefix(kind, "command-alias-") {
						exclude = slices.Clone(shown.Workflows)
					}
					cfg.Packs[0].Workflows.Exclude = &exclude
				} else if !other && strings.HasPrefix(kind, "command-alias-") {
					cfg.Packs[0].Workflows = config.SelectionsToVector(shown.Workflows, []string{commandID})
				}
				if !other && agentID != "" {
					exclude := []string{agentID}
					cfg.Packs[0].Agents.Exclude = &exclude
				}
				if !other && skillID != "" {
					cfg.Packs[0].Skills = config.SelectionsToVector(shown.Skills, nil)
				}
			}
			check := func(record domain.NativePluginRecord, command, other bool) {
				t.Helper()
				spec := *entry.Plugin
				if catalogOnly {
					entries, err := plugin.CatalogEntries(mustRead(t, nativeRecordCatalogPath(record)))
					if err != nil || len(entries) != 1 {
						t.Fatalf("selected catalog entry missing: %v", err)
					}
					spec.Entry = domain.NativeJSONMap(entries[0])
				}
				for _, payload := range []string{filepath.Join(record.MarketplaceDir, record.PayloadPath), record.CachePath} {
					loaded, err := plugin.ReadClaude(payload, spec)
					if strings.HasPrefix(kind, "command-alias-") {
						want := shown.Workflows
						if !command {
							want = nil
						} else if !other {
							want = []string{commandID}
						}
						if err != nil || !slices.Equal(loaded.Workflows, want) {
							t.Fatalf("named alias declaration selection differs: %+v %v", loaded, err)
						}
					}
					if keepCommand && (err != nil || !slices.Contains(loaded.Workflows, "ops:keep")) {
						t.Fatalf("nested command identity changed: %+v %v", loaded, err)
					}
					if err != nil || slices.Contains(loaded.Workflows, commandID) != command || (agentID != "" && slices.Contains(loaded.Agents, agentID) != other) || (skillID != "" && slices.Contains(loaded.Skills, skillID) != other) || (keepAgent && !slices.Contains(loaded.Agents, "ops:Keep")) {
						t.Fatalf("selected view/cache differs: %+v %v", loaded, err)
					}
					if (kind == "default-command-link" && slices.Contains(loaded.Workflows, "ops:keep")) || (kind == "default-agent-link" && slices.Contains(loaded.Agents, "ops:Keep")) {
						t.Fatal("selected view/cache lists an inert symlink")
					}
				}
			}
			result, record := run()
			if strings.HasSuffix(kind, "-dir") {
				trace := func() TraceResult {
					t.Helper()
					traced, err := RunTrace(context.Background(), eng, resolve(), TraceRequest{TargetSpec: req.TargetSpec, ProfileName: "default", ProfileConfig: cfg, ResourceType: "workflow", ResourceName: commandID, PackName: "alias"}, testRegistry())
					if err != nil || !traced.Found || len(traced.Destinations) != 3 || len(traced.Blockers) != 0 {
						t.Fatalf("directory-link trace lost destinations: %+v %v", traced, err)
					}
					return traced
				}
				for _, destination := range trace().Destinations {
					if destination.DiffKind != domain.DiffIdentical {
						t.Fatalf("unchanged directory-link trace: %+v", destination)
					}
				}
				componentPath := shown.NativePlugin.Components[domain.CategoryWorkflows][commandID][0]
				cacheFile := filepath.Join(record.CachePath, componentPath)
				original := mustRead(t, cacheFile)
				writeFile(t, cacheFile, "changed native cache body")
				foundConflict := false
				for _, destination := range trace().Destinations {
					if destination.Path == cacheFile {
						foundConflict = destination.DiffKind == domain.DiffConflict
					}
				}
				if !foundConflict {
					t.Fatal("trace ignored changed bytes beneath a directory link")
				}
				writeFile(t, cacheFile, string(original))
			}
			data := filepath.Join(record.ConfigHome, "plugins/data/scan-probe-scan-market/marker")
			writeFile(t, data, "retained data")
			for _, selected := range [][2]bool{{false, true}, {true, false}, {false, false}, {true, true}} {
				selectContent(selected[0], selected[1])
				result, record = run()
				check(record, selected[0], selected[1])
			}
			prior, err := plugin.ReadFiles(record.MarketplaceDir)
			if err != nil {
				t.Fatal(err)
			}
			priorLedger := mustRead(t, result.Plan.Ledger)
			selectContent(false, true)
			dryReq := req
			dryReq.DryRun = true
			if _, _, err := RunSync(context.Background(), eng, resolve(), dryReq, testRegistry(), nil, nil); err != nil {
				t.Fatal(err)
			}
			eng.FS = nativeLedgerFailure{OSFS: engine.OSFS{}, Path: result.Plan.Ledger}
			if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err == nil {
				t.Fatal("failed persistence reported success")
			}
			eng.FS = engine.OSFS{}
			restored, err := plugin.ReadFiles(record.MarketplaceDir)
			if err != nil || !reflect.DeepEqual(prior, restored) || !bytes.Equal(priorLedger, mustRead(t, result.Plan.Ledger)) {
				t.Fatal("dry-run or rollback changed prior view/ledger")
			}
			check(record, true, true)
			if keepAgent || retainedAlias || catalogOnly {
				for _, after := range []bool{false, true} {
					selectContent(true, true)
					result, record = run()
					selectContent(keepAgent, !keepAgent)
					eng.FS = nativeLedgerInterruption{OSFS: engine.OSFS{}, Path: result.Plan.Ledger, After: after}
					interrupted := false
					func() {
						defer func() {
							if recovered := recover(); recovered != nil {
								if recovered != "native ledger interruption" {
									panic(recovered)
								}
								interrupted = true
							}
						}()
						if _, _, err := RunSync(context.Background(), eng, resolve(), req, testRegistry(), nil, nil); err != nil {
							t.Fatal(err)
						}
					}()
					eng.FS = engine.OSFS{}
					if !interrupted {
						t.Fatal("shared-source selection interruption did not run")
					}
					unlock, err := lockPackMutation(configDir, false)
					if err != nil {
						t.Fatal(err)
					}
					if err := unlock(); err != nil {
						t.Fatal(err)
					}
					check(record, !after || keepAgent, !after || !keepAgent)
				}
			}
			selectContent(true, false)
			writeFile(t, filepath.Join(source, path), "---\nname: Shared\ndescription: Native shared source.\n---\nUPDATED_BODY\n")
			updated, err := PackUpdate(context.Background(), PackUpdateRequest{ConfigDir: configDir, Name: "alias"}, nil, nil)
			if err != nil || len(updated) != 1 || updated[0].Status != StatusUpdated {
				t.Fatalf("overlap source update failed: %+v %v", updated, err)
			}
			_, record = run()
			check(record, true, false)
			if !bytes.Contains(mustRead(t, filepath.Join(record.CachePath, path)), []byte("UPDATED_BODY")) {
				t.Fatal("updated source did not reach native cache")
			}
			if retainedAlias || (catalogOnly && keepCommand) {
				selectContent(false, true)
				_, record = run()
				check(record, false, true)
				aliasPath := "links/shared.md"
				if catalogOnly {
					aliasPath = path
				}
				if !bytes.Contains(mustRead(t, filepath.Join(record.CachePath, aliasPath)), []byte("UPDATED_BODY")) {
					t.Fatal("selected alias lost the updated source body")
				}
			}
			if _, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: configDir, Name: "alias", Registry: testRegistry()}, nil); err != nil {
				t.Fatal(err)
			}
			if string(mustRead(t, data)) != "retained data" {
				t.Fatal("overlap lifecycle removed runtime data")
			}
		})
	}
}
