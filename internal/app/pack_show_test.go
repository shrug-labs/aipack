package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"

	"gopkg.in/yaml.v3"
)

func writeShowTestProfile(t *testing.T, configDir, name string, packNames []string) {
	t.Helper()
	packs := make([]map[string]any, len(packNames))
	for i, n := range packNames {
		packs[i] = map[string]any{"name": n}
	}
	cfg := map[string]any{"schema_version": 2, "packs": packs}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(configDir, "profiles")
	os.MkdirAll(dir, 0o700)
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeShowTestProfileWithDisabled(t *testing.T, configDir, name string, enabled, disabled []string) {
	t.Helper()
	var packs []map[string]any
	for _, n := range enabled {
		packs = append(packs, map[string]any{"name": n})
	}
	for _, n := range disabled {
		packs = append(packs, map[string]any{"name": n, "enabled": false})
	}
	cfg := map[string]any{"schema_version": 2, "packs": packs}
	b, _ := yaml.Marshal(cfg)
	dir := filepath.Join(configDir, "profiles")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, name+".yaml"), b, 0o600)
}

func writeShowTestRegistry(t *testing.T, configDir string, packs map[string]config.RegistryEntry) {
	t.Helper()
	reg := config.Registry{SchemaVersion: 1, Packs: packs}
	b, _ := yaml.Marshal(reg)
	cacheDir := config.RegistriesCacheDir(configDir)
	os.MkdirAll(cacheDir, 0o700)
	os.WriteFile(config.SourceCachePath(configDir, "test-source"), b, 0o600)
	sc, _ := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	sc.SchemaVersion = 1
	sc.RegistrySources = append(sc.RegistrySources, config.RegistrySourceEntry{
		Name: "test-source", URL: "https://example.com/test-registry.yaml",
	})
	config.SaveSyncConfig(config.SyncConfigPath(configDir), sc)
}

func installShowPack(t *testing.T, configDir, name string) {
	t.Helper()
	writePackManifest(t, filepath.Join(configDir, "packs", name), name)
}

func TestPackShowEntry_PinLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		pin  string
		want string
	}{
		{"empty pin returns empty", "", ""},
		{"semver with v prefix", "v1.2.3", "v1.2.3 (pinned)"},
		{"semver without v prefix", "1.2.3", "v1.2.3 (pinned)"},
		{"prerelease", "v1.0.0-beta.1", "v1.0.0-beta.1 (pinned)"},
		{"commit hash", "abc1234", "@abc1234 (pinned)"},
		{"long commit hash", "aabbccdd11223344556677889900aabbccddeeff", "@aabbccdd11223344556677889900aabbccddeeff (pinned)"},
		// Anything that isn't semver or commit hash falls through — branch
		// names shouldn't reach here in practice (isPinned filters them out
		// before Pin is set), but defensive fallback prevents panic.
		{"unknown format falls through", "weird-ref", "weird-ref (pinned)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := PackShowEntry{Pin: tt.pin}.PinLabel()
			if got != tt.want {
				t.Errorf("PinLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPackShow(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		packDir := t.TempDir()
		configDir := t.TempDir()
		writePackManifest(t, packDir, "test-pack")
		writeTestSyncConfig(t, configDir)

		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			PackPath: packDir, ConfigDir: configDir, Link: true,
			NowFn: func() time.Time { return fixedNow },
		}, &out)

		entry, err := PackShow(configDir, "test-pack")
		if err != nil {
			t.Fatalf("PackShow: %v", err)
		}
		if entry.Name != "test-pack" || entry.Version != "1.0.0" || entry.Method != config.MethodLink {
			t.Errorf("got name=%q version=%q method=%q", entry.Name, entry.Version, entry.Method)
		}
		if entry.Origin != packDir {
			t.Errorf("origin = %q, want %q", entry.Origin, packDir)
		}
	})

	t.Run("discovers content from disk", func(t *testing.T) {
		t.Parallel()
		packDir := t.TempDir()
		configDir := t.TempDir()
		writePackManifest(t, packDir, "discover-pack")
		os.MkdirAll(filepath.Join(packDir, "rules"), 0o755)
		os.WriteFile(filepath.Join(packDir, "rules", "my-rule.md"), []byte("# rule"), 0o600)
		os.MkdirAll(filepath.Join(packDir, "skills", "my-skill"), 0o755)
		os.WriteFile(filepath.Join(packDir, "skills", "my-skill", "SKILL.md"), []byte("# skill"), 0o600)

		writeTestSyncConfig(t, configDir)
		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			PackPath: packDir, ConfigDir: configDir, Link: true,
			NowFn: func() time.Time { return fixedNow },
		}, &out)

		entry, err := PackShow(configDir, "discover-pack")
		if err != nil {
			t.Fatal(err)
		}
		if len(entry.Rules) != 1 || entry.Rules[0] != "my-rule" {
			t.Errorf("Rules = %v", entry.Rules)
		}
		if len(entry.Skills) != 1 || entry.Skills[0] != "my-skill" {
			t.Errorf("Skills = %v", entry.Skills)
		}
	})

	t.Run("not installed", func(t *testing.T) {
		t.Parallel()
		_, err := PackShow(t.TempDir(), "nonexistent")
		if err == nil || !strings.Contains(err.Error(), "not installed") {
			t.Fatalf("expected 'not installed' error, got: %v", err)
		}
	})

	t.Run("includes commit hash", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		writeTestSyncConfig(t, configDir)
		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			URL: "https://github.com/example/my-pack", ConfigDir: configDir,
			RunGitFn:  fakeCloneGitFn(t, "my-pack"),
			URLOKFn:   func(context.Context, string) (bool, error) { return true, nil },
			NowFn:     func() time.Time { return fixedNow },
			GitHashFn: func(context.Context, string) (string, error) { return fakeHash1, nil },
		}, &out)
		entry, err := PackShow(configDir, "my-pack")
		if err != nil {
			t.Fatal(err)
		}
		if entry.CommitHash != fakeHash1 {
			t.Errorf("commit_hash = %q, want %q", entry.CommitHash, fakeHash1)
		}
	})
}

func TestPackList(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		entries, err := PackList(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("got %d entries, want 0", len(entries))
		}
	})

	t.Run("after install", func(t *testing.T) {
		t.Parallel()
		packDir := t.TempDir()
		configDir := t.TempDir()
		writePackManifest(t, packDir, "test-pack")
		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			PackPath: packDir, ConfigDir: configDir, Link: true,
		}, &out)

		entries, err := PackList(configDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name != "test-pack" {
			t.Fatalf("got %+v", entries)
		}
		if entries[0].Method != config.MethodLink || entries[0].Version != "1.0.0" {
			t.Errorf("method=%q version=%q", entries[0].Method, entries[0].Version)
		}
	})

	t.Run("shows origin info", func(t *testing.T) {
		t.Parallel()
		packDir := t.TempDir()
		configDir := t.TempDir()
		writePackManifest(t, packDir, "test-pack")
		writeTestSyncConfig(t, configDir)
		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			PackPath: packDir, ConfigDir: configDir, Link: true,
			NowFn: func() time.Time { return fixedNow },
		}, &out)
		entries, _ := PackList(configDir)
		if len(entries) != 1 || entries[0].Origin != packDir || entries[0].Method != config.MethodLink {
			t.Errorf("got origin=%q method=%q", entries[0].Origin, entries[0].Method)
		}
	})

	t.Run("broken symlink", func(t *testing.T) {
		t.Parallel()
		packDir := t.TempDir()
		configDir := t.TempDir()
		writePackManifest(t, packDir, "test-pack")
		var out bytes.Buffer
		PackInstall(context.Background(), PackInstallRequest{
			PackPath: packDir, ConfigDir: configDir, Link: true,
		}, &out)
		os.RemoveAll(packDir) // break the symlink

		entries, err := PackList(configDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Method != config.MethodLink {
			t.Fatalf("got %+v", entries)
		}
		if _, err := os.Stat(entries[0].Path); err == nil {
			t.Fatal("expected broken symlink target")
		}
	})
}
