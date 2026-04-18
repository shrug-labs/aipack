package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

func TestResolveActiveProfile_UsesLockfileInventoriesForDriftedRefs(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	writeTestSyncConfig(t, configDir)
	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	syncCfg.Defaults.Harnesses = []string{string(domain.HarnessCodex)}
	if err := config.SaveSyncConfig(config.SyncConfigPath(configDir), syncCfg); err != nil {
		t.Fatal(err)
	}

	// Active profile references a rule that no longer exists in current pack content.
	writeEmptyProfile(t, configDir, "default")
	profilePath := filepath.Join(configDir, "profiles", "default.yaml")
	profile := "schema_version: 1\npacks:\n  - name: test-pack\n    rules:\n      include: [removed-rule]\n"
	if err := writeTestFile(profilePath, profile); err != nil {
		t.Fatal(err)
	}

	// Current pack inventory has only kept-rule.
	packRoot := filepath.Join(configDir, "packs", "test-pack")
	if err := writeTestFile(filepath.Join(packRoot, "pack.json"), `{"schema_version":2,"name":"test-pack","version":"1.0.0","root":".","rules":["kept-rule"]}`); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(filepath.Join(packRoot, "rules", "kept-rule.md"), "---\nname: kept-rule\n---\nbody\n"); err != nil {
		t.Fatal(err)
	}

	// Previous lockfile inventory included removed-rule, so this should resolve
	// as a broken ref warning instead of a hard error.
	lf := config.Lockfile{
		LockVersion: config.LockfileVersion,
		Packs: map[string]config.InstalledPackMeta{
			"test-pack": {
				Origin:      packRoot,
				Method:      config.MethodLocal,
				InstalledAt: time.Now().UTC().Format(time.RFC3339),
				Resolved: &domain.PackInventory{
					Rules: []string{"kept-rule", "removed-rule"},
				},
			},
		},
	}
	if err := config.SaveLockfile(config.LockfilePath(configDir), lf); err != nil {
		t.Fatal(err)
	}

	_, warnings, err := ResolveActiveProfile(engine.New(nil, nil), configDir)
	if err != nil {
		t.Fatalf("ResolveActiveProfile should not hard-fail on drifted refs: %v", err)
	}
	foundBrokenRef := false
	for _, w := range warnings {
		if w.Field == "broken-ref" && strings.Contains(w.Message, "removed-rule") {
			foundBrokenRef = true
			break
		}
	}
	if !foundBrokenRef {
		t.Fatalf("expected broken-ref warning mentioning removed-rule, got: %+v", warnings)
	}
}

func writeTestFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
