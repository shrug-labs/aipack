package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/util"
)

func TestOrdinarySkillRecoveryProtectsChanges(t *testing.T) {
	for _, scenario := range []string{"restore", "unowned-path", "changed-snapshot", "unsafe-active-path"} {
		t.Run(scenario, func(t *testing.T) {
			cfgDir, home, outside := t.TempDir(), t.TempDir(), t.TempDir()
			path := filepath.Join(home, "skills/probe")
			writeFile(t, filepath.Join(path, "SKILL.md"), "original instructions")
			writeFile(t, filepath.Join(path, ".git/HEAD"), "owned local addition")
			writeFile(t, filepath.Join(path, "asset.sh"), "original executable asset")
			if err := os.Chmod(filepath.Join(path, "asset.sh"), 0o755); err != nil {
				t.Fatal(err)
			}
			fs := engine.OSFS{}
			before, err := fs.ReadPackage(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := fs.WritePackage(operationSkillBackup(cfgDir, path), before); err != nil {
				t.Fatal(err)
			}
			op := nativeOperation{Touched: map[string]domain.NativePluginRecord{"probe@owned": {Harness: domain.HarnessClaudeCode, SettingsPath: filepath.Join(home, "settings.json")}}, Skills: map[string]nativeViewSnapshot{path: {Exists: true, Digest: domain.SingleFileDigest(domain.PackageManifest(before))}}}
			writeFile(t, filepath.Join(path, "SKILL.md"), "interrupted instructions")
			writeFile(t, filepath.Join(path, "notes.txt"), "retain user edit")
			writeFile(t, filepath.Join(outside, "SKILL.md"), "foreign instructions")
			switch scenario {
			case "unowned-path":
				op.Skills[outside] = nativeViewSnapshot{}
			case "changed-snapshot":
				writeFile(t, filepath.Join(operationSkillBackup(cfgDir, path), "asset.sh"), "changed backup")
			case "unsafe-active-path":
				if err := fs.RemovePackage(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			}
			err = validateOperationSkills(cfgDir, op)
			if scenario != "restore" {
				if err == nil || string(mustRead(t, filepath.Join(outside, "SKILL.md"))) != "foreign instructions" {
					t.Fatal("unsafe recovery was accepted or changed foreign content", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := restoreOperationSkills(cfgDir, op); err != nil {
				t.Fatal(err)
			}
			after, err := fs.ReadPackage(path)
			if err != nil || domain.SingleFileDigest(domain.PackageManifest(after)) != op.Skills[path].Digest {
				t.Fatal("rollback did not restore complete instructions/assets/modes/local additions", err)
			}
			retained := filepath.Join(nativeOperationDir(cfgDir), "interrupted-skills", util.ContentDigest([]byte(path)))
			if string(mustRead(t, filepath.Join(retained, "notes.txt"))) != "retain user edit" || string(mustRead(t, filepath.Join(retained, "SKILL.md"))) != "interrupted instructions" {
				t.Fatal("rollback discarded interrupted output or user edits")
			}
			if err := restoreOperationSkills(cfgDir, op); err != nil {
				t.Fatal("repeat recovery failed", err)
			}
		})
	}
}

func TestCodexPolicyRefusesNativePreflight(t *testing.T) {
	configDir, home := t.TempDir(), t.TempDir()
	plan := domain.Plan{Ledger: filepath.Join(configDir, "ledger/global/codex.json"), NativePlugins: []domain.NativePluginAction{{Package: domain.NativePlugin{Name: "probe", Marketplace: "market", Harness: domain.HarnessCodex, MarketplaceEntry: map[string]any{"policy": map[string]any{"installation": "NOT_AVAILABLE"}}}}}}
	err := preflightNativePlugins(context.Background(), engine.New(nil, nil), plan, SyncRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: home}})
	entries, readErr := os.ReadDir(configDir)
	if err == nil || !strings.Contains(err.Error(), "policy") || readErr != nil || len(entries) != 0 {
		t.Fatalf("rejected policy changed native state: %v entries=%v", err, entries)
	}
}

func TestNativePayloadLayoutCleanup(t *testing.T) {
	for _, paths := range []struct{ old, next string }{
		{"plugins/probe", "plugins/source"},
		{"plugins/probe", "plugins/probe/selected"},
		{"plugins/probe/selected", "plugins/probe"},
		{"plugins/probe", "."},
		{".", "plugins/probe"},
	} {
		t.Run(paths.old+"->"+paths.next, func(t *testing.T) {
			configDir, home := t.TempDir(), t.TempDir()
			eng := engine.New(nil, nil)
			view := filepath.Join(configDir, "rendered-plugins/claudecode/market")
			binding := "probe@market"
			prior := domain.NativePluginRecord{Harness: domain.HarnessClaudeCode, ConfigHome: home, MarketplaceDir: view, CachePath: filepath.Join(home, "plugins/cache/market/probe/version"), SettingsPath: filepath.Join(home, "settings.json"), PayloadPath: paths.old}
			current := prior
			current.PayloadPath = paths.next
			oldRoot := filepath.Join(view, prior.PayloadPath)
			newRoot := filepath.Join(view, paths.next)
			obsolete := filepath.Join(oldRoot, "obsolete/marker")
			writeFile(t, obsolete, "obsolete")
			writeFile(t, filepath.Join(newRoot, "marker"), "current")
			catalog := nativeCatalogPath(current.Harness, view)
			writeFile(t, catalog, `{"name":"market","plugins":[]}`)
			outside := filepath.Join(t.TempDir(), "marker")
			writeFile(t, outside, "outside")
			if err := os.Symlink(filepath.Dir(outside), filepath.Join(oldRoot, "external")); err != nil {
				t.Fatal(err)
			}
			kept := filepath.Join(oldRoot, "owned-sibling")
			sibling := prior
			sibling.CachePath = filepath.Join(home, "plugins/cache/market/sibling/version")
			sibling.SettingsPath = filepath.Join(home, "project/.claude/settings.local.json")
			sibling.PayloadPath, _ = filepath.Rel(view, kept)
			writeFile(t, filepath.Join(kept, "marker"), "sibling")
			other := domain.NewLedger()
			other.NativePlugins = map[string]domain.NativePluginRecord{"sibling@market": sibling}
			if err := eng.SaveLedger(filepath.Join(configDir, "ledger/project/claudecode.json"), other, false); err != nil {
				t.Fatal(err)
			}
			cleanup := func() error {
				return cleanupNativePayloadChanges(eng, configDir, filepath.Join(configDir, "ledger/global/claudecode.json"), map[string]domain.NativePluginRecord{binding: prior}, map[string]domain.NativePluginRecord{binding: current})
			}
			if err := cleanup(); err != nil {
				t.Fatal(err)
			}
			if string(mustRead(t, filepath.Join(newRoot, "marker"))) != "current" || string(mustRead(t, outside)) != "outside" || string(mustRead(t, filepath.Join(kept, "marker"))) != "sibling" || !util.PathExists(catalog) {
				t.Fatal("layout cleanup changed live payload, sibling, catalog or outside data")
			}
			if !util.IsWithinDir(obsolete, newRoot) && util.PathExists(obsolete) {
				t.Fatal("layout cleanup left obsolete payload entries")
			}
			if err := cleanup(); err != nil {
				t.Fatalf("repeated layout cleanup: %v", err)
			}
		})
	}
}

func TestNativeSharedRootScopeTransition(t *testing.T) {
	configDir, home := t.TempDir(), t.TempDir()
	view := filepath.Join(configDir, "rendered-plugins/claudecode/market")
	action := domain.NativePluginAction{Package: domain.NativePlugin{Name: "probe", Marketplace: "market", Format: "claude", Harness: domain.HarnessClaudeCode, MarketplaceEntry: map[string]any{"source": "./"}}, MarketplaceDir: view, ConfigHome: home, SettingsPath: filepath.Join(home, "settings.json")}
	plan := domain.Plan{Ledger: filepath.Join(configDir, "ledger/global/claudecode.json"), NativePlugins: []domain.NativePluginAction{action}}
	req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: home}}
	eng := engine.New(nil, nil)
	prior := domain.NewLedger()
	prior.NativePlugins = map[string]domain.NativePluginRecord{"sibling@market": {Harness: domain.HarnessClaudeCode,
		ConfigHome: home, MarketplaceDir: view, CachePath: filepath.Join(home, "plugins/cache/market/sibling/1.0.0"), SettingsPath: filepath.Join(home, "settings.json"), PayloadPath: ".", SharedRoot: strings.Repeat("a", 64)}}
	if err := eng.SaveLedger(plan.Ledger, prior, false); err != nil {
		t.Fatal(err)
	}
	if err := preflightNativePlugins(context.Background(), eng, plan, req); err != nil {
		t.Fatalf("removing this scope's shared-root sibling rejected: %v", err)
	}
	if err := eng.SaveLedger(filepath.Join(configDir, "ledger/project/claudecode.json"), prior, false); err != nil {
		t.Fatal(err)
	}
	if err := preflightNativePlugins(context.Background(), eng, plan, req); err == nil || !strings.Contains(err.Error(), "another scope") {
		t.Fatalf("shared-root sibling owned by another scope was replaced: %v", err)
	}
}

func TestNativePayloadSourceOwnership(t *testing.T) {
	configDir, home := t.TempDir(), t.TempDir()
	view := filepath.Join(configDir, "rendered-plugins/claudecode/market")
	action := domain.NativePluginAction{Package: domain.NativePlugin{Name: "probe", Marketplace: "market", Harness: domain.HarnessClaudeCode, MarketplaceEntry: map[string]any{"source": "./"}}, MarketplaceDir: view, ConfigHome: home, SettingsPath: filepath.Join(home, "settings.json")}
	req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: home}}
	plan := domain.Plan{Ledger: filepath.Join(configDir, "ledger/global.json"), NativePlugins: []domain.NativePluginAction{action}}
	eng := engine.New(nil, nil)
	if err := preflightNativePlugins(context.Background(), eng, plan, req); err != nil {
		t.Fatalf("valid cold root payload rejected: %v", err)
	}
	for _, source := range []string{"../outside", "./../outside", "/outside", "./plugins/../outside", "./plugins\\outside", "./.aipack-marketplace"} {
		plan.NativePlugins[0].Package.MarketplaceEntry = map[string]any{"source": source}
		if err := preflightNativePlugins(context.Background(), eng, plan, req); err == nil {
			t.Fatalf("unsafe/conflicting payload path accepted: %s", source)
		}
	}
	plan.NativePlugins[0] = action
	sibling := action
	sibling.Package.Name = "sibling"
	sibling.Package.MarketplaceEntry = map[string]any{"source": "./plugins/sibling"}
	plan.NativePlugins = append(plan.NativePlugins, sibling)
	if err := preflightNativePlugins(context.Background(), eng, plan, req); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("root/sibling source overlap accepted: %v", err)
	}
	plan.NativePlugins = []domain.NativePluginAction{action}
	plan.NativePlugins[0].Files = []domain.NativePluginFile{{Path: ".aipack-marketplace/marketplace.json", Mode: 0o644}}
	if err := preflightNativePlugins(context.Background(), eng, plan, req); err == nil {
		t.Fatal("root source colliding with generated metadata accepted")
	}
	shared := action
	shared.Package.Format, shared.SharedRoot = "claude", true
	sibling = shared
	sibling.Package.Name = "sibling"
	plan.NativePlugins = []domain.NativePluginAction{shared, sibling}
	if err := preflightNativePlugins(context.Background(), eng, plan, req); err != nil {
		t.Fatalf("consistent selected shared roots rejected: %v", err)
	}
	plan.NativePlugins[1].Files = []domain.NativePluginFile{{Path: "changed", Content: []byte("different"), Mode: 0o644}}
	if err := preflightNativePlugins(context.Background(), eng, plan, req); err == nil || !strings.Contains(err.Error(), "inconsistent selected payloads") {
		t.Fatalf("inconsistent shared-root plans accepted: %v", err)
	}
	plan.NativePlugins[1] = sibling
	sharedDigest, _ := nativeSharedRootGeneration(shared)
	generation, _ := nativeGeneration(sibling)
	other := domain.NewLedger()
	other.NativePlugins = map[string]domain.NativePluginRecord{"sibling@market": {Harness: domain.HarnessClaudeCode,
		ConfigHome: home, MarketplaceDir: view, CachePath: filepath.Join(home, "plugins/cache/market/sibling/1.0.0"), SettingsPath: filepath.Join(home, "settings.json"), PayloadPath: ".", SharedRoot: sharedDigest, Generation: generation}}
	otherLedger := filepath.Join(configDir, "ledger/project/claudecode.json")
	if err := eng.SaveLedger(otherLedger, other, false); err != nil {
		t.Fatal(err)
	}
	if err := preflightNativePlugins(context.Background(), eng, plan, req); err != nil {
		t.Fatalf("matching shared-root ownership in another scope rejected: %v", err)
	}
	foreign := other.NativePlugins["sibling@market"]
	foreign.SharedRoot = strings.Repeat("f", 64)
	other.NativePlugins["sibling@market"] = foreign
	if err := eng.SaveLedger(otherLedger, other, false); err != nil {
		t.Fatal(err)
	}
	if err := preflightNativePlugins(context.Background(), eng, plan, req); err == nil || !strings.Contains(err.Error(), "overlaps a source owned by another scope") {
		t.Fatalf("different shared-root scope selection accepted: %v", err)
	}
	record := domain.NativePluginRecord{Harness: domain.HarnessClaudeCode, ConfigHome: home, MarketplaceDir: view, CachePath: filepath.Join(home, "plugins/cache/market/probe/version"), SettingsPath: filepath.Join(home, "settings.json"), PayloadPath: "."}
	if err := validateNativeRecord(configDir, "probe@market", record); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "../outside", "plugins/../outside", "/outside"} {
		record.PayloadPath = path
		if err := validateNativeRecord(configDir, "probe@market", record); err == nil {
			t.Fatalf("escaping payload ownership accepted: %s", path)
		}
	}
	record.PayloadPath = "."
	for _, name := range []string{"market", "Mixed...Root__with  spaces__😀", "..", "../outside", "a/b", "a\\b", ".", "\x00"} {
		record.RootDirectoryName = name
		record.MarketplaceDir = domain.NativeMarketplaceDir(configDir, record.Harness, "market", name)
		valid := name == "market" || name == "Mixed...Root__with  spaces__😀"
		if err := validateNativeRecord(configDir, "probe@market", record); (err == nil) != valid {
			t.Fatalf("local root directory ownership %q: %v", name, err)
		}
		target, managed := nativeSetupTarget(record, "probe@market")
		if managed != valid || (managed && target.Path != filepath.Join(configDir, "native-setup.json")) {
			t.Fatalf("local root setup ownership %q: %+v %t", name, target, managed)
		}
	}
	record.RootDirectoryName, record.MarketplaceDir = "", view
	for _, source := range []struct {
		kind, path string
		owned      bool
	}{
		{"directory", view, true},
		{"file", nativeCatalogPath(record.Harness, view), true},
		{"file", filepath.Join(home, "foreign.json"), false},
		{"directory", home, false},
	} {
		entry := map[string]any{"source": map[string]any{"source": source.kind, "path": source.path}}
		if claudeMarketplaceSourceOwned(entry, record) != source.owned {
			t.Fatalf("marketplace source ownership differs: %+v", source)
		}
	}
}

func TestNativeJSONSnapshots(t *testing.T) {
	configDir := t.TempDir()
	op := nativeOperation{}
	for _, item := range []struct {
		file, section, key, before, after string
	}{
		{"settings.json", "enabledPlugins", "probe@market", `{"permissions":{"allow":["Read"]},"enabledPlugins":{"probe@market":false}}`, `{"permissions":{"allow":["Read","Write"]},"enabledPlugins":{"probe@market":true,"external@other":true}}`},
		{"marketplaces.json", "", "market", `{"market":{"source":{"source":"directory","path":"/before"}}}`, `{"market":{"source":{"source":"directory","path":"/after"}},"external":{"keep":true}}`},
		{"installed_plugins.json", "plugins", "probe@market", `{"version":2,"plugins":{"probe@market":[{"scope":"user","version":"1.0.0","counter":9007199254740993},{"scope":"local","projectPath":"/project"}]}}`, `{"version":2,"counter":9007199254740995,"plugins":{"probe@market":[{"scope":"user","version":"2.0.0"}],"external@other":[{"scope":"user"}]}}`},
		{"new-settings.json", "enabledPlugins", "probe@market", `{}`, `{"enabledPlugins":{"probe@market":true,"external@other":true}}`},
		{".claude.json", "mcpServers", "probe.dot", `{"mcpServers":{"probe.dot":{"command":"before","counter":9007199254740993}}}`, `{"mcpServers":{"probe.dot":{"command":"after"},"probe_dot":{"command":"foreign"}},"unrelated":true}`},
	} {
		path := filepath.Join(configDir, item.file)
		writeFile(t, path, item.before)
		root, err := readNativeConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		entries := root
		if item.section != "" {
			entries, _ = root[item.section].(map[string]any)
		}
		entry, present := entries[item.key]
		op.Activations = append(op.Activations, nativeActivationSnapshot{Path: path, Section: item.section, Binding: item.key, Entry: entry, Present: present})
		writeFile(t, path, item.after)
	}
	if err := saveNativeOperation(configDir, &op); err != nil {
		t.Fatal(err)
	}
	var restored nativeOperation
	if err := json.Unmarshal(mustRead(t, filepath.Join(nativeOperationDir(configDir), "operation.json")), &restored); err != nil {
		t.Fatal(err)
	}
	if err := restoreNativeActivations(restored.Activations); err != nil {
		t.Fatal(err)
	}
	settings, err := readNativeConfig(filepath.Join(configDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if settings["enabledPlugins"].(map[string]any)["probe@market"] != false || settings["enabledPlugins"].(map[string]any)["external@other"] != true || len(settings["permissions"].(map[string]any)["allow"].([]any)) != 2 {
		t.Fatalf("JSON rollback changed user settings or lost false: %+v", settings)
	}
	markets, err := readNativeConfig(filepath.Join(configDir, "marketplaces.json"))
	if err != nil || markets["external"] == nil || markets["market"].(map[string]any)["source"].(map[string]any)["path"] != "/before" {
		t.Fatalf("root marketplace rollback: %+v %v", markets, err)
	}
	installed, err := readNativeConfig(filepath.Join(configDir, "installed_plugins.json"))
	if err != nil {
		t.Fatal(err)
	}
	entries := installed["plugins"].(map[string]any)
	if installed["version"] != json.Number("2") || len(entries["probe@market"].([]any)) != 2 || entries["external@other"] == nil || installed["counter"] != json.Number("9007199254740995") || entries["probe@market"].([]any)[0].(map[string]any)["counter"] != json.Number("9007199254740993") {
		t.Fatalf("scoped installation records changed: %+v", installed)
	}
	settings, err = readNativeConfig(filepath.Join(configDir, "new-settings.json"))
	if err != nil || settings["enabledPlugins"].(map[string]any)["probe@market"] != nil || settings["enabledPlugins"].(map[string]any)["external@other"] != true {
		t.Fatalf("first install rollback: %+v %v", settings, err)
	}
	mcp, err := readNativeConfig(filepath.Join(configDir, ".claude.json"))
	if err != nil || mcp["unrelated"] != true {
		t.Fatal("ordinary MCP rollback changed unrelated config", err)
	}
	servers := mcp["mcpServers"].(map[string]any)
	if servers["probe.dot"].(map[string]any)["command"] != "before" || servers["probe.dot"].(map[string]any)["counter"] != json.Number("9007199254740993") || servers["probe_dot"].(map[string]any)["command"] != "foreign" {
		t.Fatal("ordinary MCP rollback confused exact IDs or changed prior values", servers)
	}
	for _, invalid := range []string{`null`, `[]`, `{} {}`, `{"enabledPlugins":null}`, `{"enabledPlugins":["external@other"]}`} {
		path := filepath.Join(configDir, "invalid.json")
		writeFile(t, path, invalid)
		if err := restoreNativeConfigEntry(path, "enabledPlugins", "probe@market", true, true); err == nil {
			t.Fatalf("accepted invalid native JSON: %s", invalid)
		}
		if string(mustRead(t, path)) != invalid {
			t.Fatal("invalid native config was overwritten")
		}
	}
}

func TestNativeConfigHomeChangeRequiresCleanup(t *testing.T) {
	configDir, home := t.TempDir(), t.TempDir()
	view := filepath.Join(configDir, "rendered-plugins/codex/market")
	oldHome, newHome := filepath.Join(home, "old"), filepath.Join(home, "new")
	plan := domain.Plan{Ledger: engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessCodex), NativePlugins: []domain.NativePluginAction{{
		Package:        domain.NativePlugin{Name: "probe", Marketplace: "market", Harness: domain.HarnessCodex},
		MarketplaceDir: view, ConfigHome: newHome, SettingsPath: filepath.Join(newHome, "config.toml"),
	}}}
	ledger := domain.NewLedger()
	ledger.NativePlugins = map[string]domain.NativePluginRecord{"probe@market": {Harness: domain.HarnessCodex,
		ConfigHome: oldHome, MarketplaceDir: view, PayloadPath: "plugins/probe", CachePath: filepath.Join(oldHome, "plugins/cache/market/probe"), SettingsPath: filepath.Join(oldHome, "config.toml")}}
	eng := engine.New(nil, nil)
	if err := eng.SaveLedger(plan.Ledger, ledger, false); err != nil {
		t.Fatal(err)
	}
	if err := preflightNativePlugins(context.Background(), eng, plan, SyncRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: home}}); err == nil || !strings.Contains(err.Error(), "clean this scope") {
		t.Fatalf("config home change accepted: %v", err)
	}
	if _, err := os.Stat(newHome); !os.IsNotExist(err) {
		t.Fatal("preflight changed the new home")
	}
}

func TestNativeOperationRecovery(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-ledger", true: "after-ledger"}[committed], func(t *testing.T) {
			configDir, home := t.TempDir(), t.TempDir()
			view := filepath.Join(configDir, "rendered-plugins/codex/market")
			writeFile(t, filepath.Join(view, "marker"), "before")
			configHome := filepath.Join(home, ".codex")
			configPath := filepath.Join(configHome, "config.toml")
			writeFile(t, configPath, "unrelated = true\n[plugins.\"probe@market\"]\nenabled = false\ninteger = 42\n")
			plan := domain.Plan{Ledger: engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessCodex), NativePlugins: []domain.NativePluginAction{{
				Package:        domain.NativePlugin{Name: "probe", Marketplace: "market", Harness: domain.HarnessCodex},
				MarketplaceDir: view, ConfigHome: configHome, SettingsPath: configPath,
			}}}
			req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: configDir, Home: home}}
			op, err := beginNativeOperation(configDir, plan, req, domain.NewLedger())
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(view, "marker"), "after")
			writeFile(t, filepath.Join(view, "user-edit"), "retain me")
			writeFile(t, configPath, "unrelated = true\n[plugins.\"probe@market\"]\nenabled = true\ninteger = 0\n")
			if committed {
				ledger := domain.NewLedger()
				ledger.NativeOperation = op.ID
				if err := engine.New(nil, nil).SaveLedger(plan.Ledger, ledger, false); err != nil {
					t.Fatal(err)
				}
			}
			if err := recoverNativeOperation(configDir); err != nil {
				t.Fatal(err)
			}
			want := "before"
			if committed {
				want = "after"
			}
			if string(mustRead(t, filepath.Join(view, "marker"))) != want {
				t.Fatal("recovery chose the wrong rendering")
			}
			if !committed {
				retained := filepath.Join(configDir, ".tmp/native-recoveries", op.ID, "interrupted/market/user-edit")
				if string(mustRead(t, retained)) != "retain me" {
					t.Fatal("rollback discarded a concurrent user edit")
				}
				root, err := readNativeConfig(configPath)
				if err != nil {
					t.Fatal(err)
				}
				entry := root["plugins"].(map[string]any)["probe@market"].(map[string]any)
				if root["unrelated"] != true || entry["enabled"] != false || entry["integer"] != int64(42) {
					t.Fatalf("recovery changed config values or their types: %+v", root)
				}
			}
			if _, err := os.Stat(nativeOperationDir(configDir)); !os.IsNotExist(err) {
				t.Fatal("completed recovery remained pending")
			}
			if err := recoverNativeOperation(configDir); err != nil {
				t.Fatalf("second recovery: %v", err)
			}
		})
	}
}

func TestNativeRecoveryPreservesGitMetadata(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"cache", "view"} {
		t.Run(kind, func(t *testing.T) {
			configDir, home := t.TempDir(), t.TempDir()
			record := domain.NativePluginRecord{Harness: domain.HarnessCodex, ConfigHome: home}
			root := nativeCacheRoot(record, "probe@market")
			backup := filepath.Join(nativeOperationDir(configDir), "caches", "probe@market")
			if kind == "view" {
				root = filepath.Join(configDir, "rendered-plugins", "codex", "market")
				backup = filepath.Join(nativeOperationDir(configDir), "views", "market")
			}
			gitHead := filepath.Join(root, ".git", "HEAD")
			writeFile(t, gitHead, "prior history")
			before, err := snapshotNativeTree(root, backup)
			if err != nil {
				t.Fatal(err)
			}
			if string(mustRead(t, filepath.Join(backup, ".git", "HEAD"))) != "prior history" {
				t.Fatal("snapshot lost Git metadata")
			}
			writeFile(t, gitHead, "interrupted history")
			op := nativeOperation{
				Touched: map[string]domain.NativePluginRecord{"probe@market": record},
				Caches:  map[string]nativeViewSnapshot{"probe@market": before},
				Views:   map[string]nativeViewSnapshot{"market": before},
			}
			if kind == "cache" {
				if err := retainNativeCaches(configDir, op); err != nil {
					t.Fatal(err)
				}
				err = restoreNativeCaches(configDir, op)
			} else {
				err = restoreNativeViews(configDir, op)
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(mustRead(t, gitHead)) != "prior history" {
				t.Fatal("restore lost Git metadata")
			}
			retained := filepath.Join(nativeOperationDir(configDir), "interrupted", "market")
			if kind == "cache" {
				dir := filepath.Join(nativeOperationDir(configDir), "interrupted-caches")
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 1 {
					t.Fatalf("interrupted cache was not retained: %v %v", entries, err)
				}
				retained = filepath.Join(dir, entries[0].Name())
			}
			if string(mustRead(t, filepath.Join(retained, ".git", "HEAD"))) != "interrupted history" {
				t.Fatal("recovery discarded interrupted Git metadata")
			}
		})
	}
}

func TestNativeCacheRecoveryRejectsChangedSnapshots(t *testing.T) {
	configDir, home := t.TempDir(), t.TempDir()
	configHome := filepath.Join(home, ".codex")
	cache := filepath.Join(configHome, "plugins/cache/market/probe/version/marker")
	writeFile(t, cache, "prior cache")
	view := filepath.Join(configDir, "rendered-plugins/codex/market")
	writeFile(t, filepath.Join(view, "marker"), "prior view")
	plan := domain.Plan{Ledger: engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessCodex), NativePlugins: []domain.NativePluginAction{{
		Package:        domain.NativePlugin{Name: "probe", Marketplace: "market", Harness: domain.HarnessCodex},
		MarketplaceDir: view, ConfigHome: configHome, SettingsPath: filepath.Join(configHome, "config.toml"),
	}}}
	op, err := beginNativeOperation(configDir, plan, SyncRequest{TargetSpec: TargetSpec{Home: home}}, domain.NewLedger())
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(nativeOperationDir(configDir), "caches/probe@market/version/marker")
	writeFile(t, backup, "changed snapshot")
	if err := recoverNativeOperation(configDir); err == nil {
		t.Fatal("recovery accepted a changed cache snapshot")
	}
	writeFile(t, backup, "prior cache")
	op.Caches["another@market"] = op.Caches["probe@market"]
	if err := saveNativeOperation(configDir, op); err != nil {
		t.Fatal(err)
	}
	if err := recoverNativeOperation(configDir); err == nil {
		t.Fatal("recovery accepted an unowned cache")
	}
	if string(mustRead(t, cache)) != "prior cache" || string(mustRead(t, filepath.Join(view, "marker"))) != "prior view" {
		t.Fatal("rejected recovery changed owned cache or rendering")
	}
}

func TestNativeRecoveryRejectsUnownedConfig(t *testing.T) {
	configDir, home := t.TempDir(), t.TempDir()
	view := filepath.Join(configDir, "rendered-plugins/codex/market")
	configPath := filepath.Join(home, ".codex/config.toml")
	plan := domain.Plan{Ledger: engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessCodex), NativePlugins: []domain.NativePluginAction{{
		Package:        domain.NativePlugin{Name: "probe", Marketplace: "market", Harness: domain.HarnessCodex},
		MarketplaceDir: view, ConfigHome: filepath.Dir(configPath), SettingsPath: configPath,
	}}}
	op, err := beginNativeOperation(configDir, plan, SyncRequest{TargetSpec: TargetSpec{Home: home}}, domain.NewLedger())
	if err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(t.TempDir(), "keep.toml")
	writeFile(t, unrelated, "keep = true\n")
	op.Activations[0].Path = unrelated
	if err := saveNativeOperation(configDir, op); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(view, "marker"), "current")
	if err := recoverNativeOperation(configDir); err == nil {
		t.Fatal("recovery accepted an unowned config path")
	}
	if string(mustRead(t, unrelated)) != "keep = true\n" || string(mustRead(t, filepath.Join(view, "marker"))) != "current" {
		t.Fatal("invalid recovery changed config or rendering")
	}
	op.Activations[0].Path = configPath
	op.Activations[0].Section = "unrelated"
	if err := saveNativeOperation(configDir, op); err != nil {
		t.Fatal(err)
	}
	if err := recoverNativeOperation(configDir); err == nil {
		t.Fatal("recovery accepted an unowned config section")
	}
}

func TestNativeRecoveryRejectsUnownedOrdinaryMCP(t *testing.T) {
	for _, scenario := range []string{"path", "section", "name", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			cfgDir, home, outside := t.TempDir(), t.TempDir(), t.TempDir()
			path := filepath.Join(home, ".claude.json")
			plan := domain.Plan{Ledger: engine.LedgerPath(cfgDir, domain.ScopeGlobal, "", domain.HarnessClaudeCode),
				NativePlugins: []domain.NativePluginAction{{Package: domain.NativePlugin{Harness: domain.HarnessClaudeCode, Name: "probe", Marketplace: "owned"}, ConfigHome: home, SettingsPath: filepath.Join(home, "settings.json"), MarketplaceDir: domain.NativeMarketplaceDir(cfgDir, domain.HarnessClaudeCode, "owned", ""), SourcePack: "alias"}},
				MCPServers:    []domain.MCPAction{{Name: "probe.dot", ConfigPath: path, SourcePack: "alias", Harness: domain.HarnessClaudeCode}},
			}
			op, err := beginNativeOperation(cfgDir, plan, SyncRequest{TargetSpec: TargetSpec{Home: home}}, domain.NewLedger())
			if err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(outside, ".claude.json")
			writeFile(t, foreign, `{"retain":true}`)
			found := false
			for i := range op.Activations {
				snapshot := &op.Activations[i]
				if snapshot.Section != "mcpServers" {
					continue
				}
				found = true
				switch scenario {
				case "path":
					snapshot.Path = foreign
				case "section":
					snapshot.Section = "unrelated"
				case "name":
					snapshot.Binding = "../foreign"
				case "symlink":
					if err := os.Symlink(foreign, path); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !found {
				t.Fatal("ordinary MCP snapshot missing")
			}
			if err := saveNativeOperation(cfgDir, op); err != nil {
				t.Fatal(err)
			}
			if err := recoverNativeOperation(cfgDir); err == nil || !strings.Contains(err.Error(), "unowned config") || string(mustRead(t, foreign)) != `{"retain":true}` {
				t.Fatal("unsafe ordinary MCP recovery was accepted or changed foreign content", err)
			}
		})
	}
}

func TestNativeSetupRecoveryRejectsInvalidSnapshots(t *testing.T) {
	configDir, home := t.TempDir(), t.TempDir()
	configHome := filepath.Join(home, ".claude")
	plan := domain.Plan{Ledger: engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessClaudeCode), NativePlugins: []domain.NativePluginAction{{
		Package:        domain.NativePlugin{Name: "probe", Marketplace: "market", Harness: domain.HarnessClaudeCode},
		MarketplaceDir: filepath.Join(configDir, "rendered-plugins/claudecode/market"), ConfigHome: configHome, SettingsPath: filepath.Join(configHome, "settings.json"),
	}}}
	op, err := beginNativeOperation(configDir, plan, SyncRequest{TargetSpec: TargetSpec{Home: home}}, domain.NewLedger())
	if err != nil {
		t.Fatal(err)
	}
	for i, snapshot := range op.Activations {
		if snapshot.Section != "setups" {
			continue
		}
		writeFile(t, snapshot.Path, `{}`)
		for _, invalid := range []nativeActivationSnapshot{
			{Path: snapshot.Path, Section: snapshot.Section, Binding: snapshot.Binding, Present: true, Entry: "invalid"},
			{Path: snapshot.Path, Section: snapshot.Section, Binding: snapshot.Binding, Present: true, Entry: map[string]any{"1.0.0": "invalid"}},
			{Path: snapshot.Path, Section: snapshot.Section, Binding: snapshot.Binding, Present: true, Entry: map[string]any{"../outside": true}},
			{Path: snapshot.Path, Section: snapshot.Section, Binding: "another", Present: true, Entry: false},
			{Path: filepath.Join(t.TempDir(), "setup.json"), Section: snapshot.Section, Binding: snapshot.Binding, Present: true, Entry: false},
		} {
			op.Activations[i] = invalid
			if err := saveNativeOperation(configDir, op); err != nil {
				t.Fatal(err)
			}
			if err := recoverNativeOperation(configDir); err == nil {
				t.Fatalf("recovery accepted invalid setup snapshot: %+v", invalid)
			}
			if string(mustRead(t, snapshot.Path)) != `{}` {
				t.Fatal("rejected recovery changed shared setup")
			}
		}
		return
	}
	t.Fatal("native journal omitted shared setup")
}

func TestNativePermissionRecoveryRejectsUnownedRules(t *testing.T) {
	configDir, home := t.TempDir(), t.TempDir()
	configHome := filepath.Join(home, ".claude")
	path := filepath.Join(configHome, "settings.json")
	writeFile(t, path, `{"permissions":{"allow":["mcp__plugin_probe_Probe__observe","Read"]}}`)
	plan := domain.Plan{Ledger: engine.LedgerPath(configDir, domain.ScopeGlobal, "", domain.HarnessClaudeCode), NativePlugins: []domain.NativePluginAction{{
		Package: domain.NativePlugin{Name: "catalog-probe", Marketplace: "market", Harness: domain.HarnessClaudeCode}, Namespace: "probe",
		MarketplaceDir: filepath.Join(configDir, "rendered-plugins/claudecode/market"), ConfigHome: configHome, SettingsPath: path,
		MCPPermissionServers: []string{"plugin_probe_Probe"},
	}}}
	op, err := beginNativeOperation(configDir, plan, SyncRequest{TargetSpec: TargetSpec{Home: home}}, domain.NewLedger())
	if err != nil {
		t.Fatal(err)
	}
	for _, namespace := range []string{"../probe", "other", ""} {
		record := op.Touched["catalog-probe@market"]
		record.Namespace = namespace
		op.Touched["catalog-probe@market"] = record
		if err := saveNativeOperation(configDir, op); err != nil {
			t.Fatal(err)
		}
		if err := recoverNativeOperation(configDir); err == nil {
			t.Fatalf("recovery accepted mismatched or invalid component namespace %q", namespace)
		}
	}
	record := op.Touched["catalog-probe@market"]
	record.Namespace = "probe"
	op.Touched["catalog-probe@market"] = record
	for i := range op.Activations {
		if op.Activations[i].Section != "permissions" || op.Activations[i].Binding != "allow" {
			continue
		}
		for _, value := range []any{[]any{"Bash"}, []any{true}, "not-an-array"} {
			op.Activations[i].Entry = value
			if err := saveNativeOperation(configDir, op); err != nil {
				t.Fatal(err)
			}
			if err := recoverNativeOperation(configDir); err == nil {
				t.Fatalf("recovery accepted unowned/malformed permissions: %+v", value)
			}
			if string(mustRead(t, path)) != `{"permissions":{"allow":["mcp__plugin_probe_Probe__observe","Read"]}}` {
				t.Fatal("rejected recovery changed user permissions")
			}
		}
		op.Activations[i].Entry = []any{"mcp__plugin_probe_Probe__observe"}
		op.Activations[i].PermissionServers = []string{"plugin_other_Probe"}
		if err := saveNativeOperation(configDir, op); err != nil {
			t.Fatal(err)
		}
		if err := recoverNativeOperation(configDir); err == nil {
			t.Fatal("recovery accepted another plugin's namespace")
		}
		return
	}
	t.Fatal("native permission recovery snapshot missing")
}
