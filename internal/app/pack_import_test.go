package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/plugin"

	"gopkg.in/yaml.v3"
)

func TestPackImport_LocalSkillCreatesPackAndAddsProfile(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	setupMinimalConfig(t, configDir)
	writeEmptyProfile(t, configDir, "default")

	source := filepath.Join(t.TempDir(), "review.md")
	if err := os.WriteFile(source, []byte("Review the diff before summarizing.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := PackImport(context.Background(), PackImportRequest{
		Source:    source,
		Category:  domain.CategorySkills,
		Name:      "imported-pack",
		ID:        "review",
		ConfigDir: configDir,
		Add:       true,
		Profile:   "default",
	}, &out)
	if err != nil {
		t.Fatalf("PackImport: %v", err)
	}

	packDir := filepath.Join(configDir, "packs", "imported-pack")
	manifest, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "imported-pack" {
		t.Fatalf("manifest name = %q, want imported-pack", manifest.Name)
	}
	if len(manifest.Skills) != 1 || manifest.Skills[0] != "review" {
		t.Fatalf("manifest skills = %v, want [review]", manifest.Skills)
	}

	raw, err := os.ReadFile(filepath.Join(packDir, "skills", "review", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"---\nname: review\ndescription: Imported from review.md\n---", "Review the diff before summarizing."} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("imported skill missing %q:\n%s", want, raw)
		}
	}

	profile, err := config.LoadProfile(filepath.Join(configDir, "profiles", "default.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.Packs) != 1 || profile.Packs[0].Name != "imported-pack" {
		t.Fatalf("profile packs = %+v, want imported-pack", profile.Packs)
	}

	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := lf.Packs["imported-pack"]
	if !ok {
		t.Fatal("imported pack missing from lockfile")
	}
	if meta.Method != config.MethodLocal {
		t.Fatalf("lockfile method = %q, want %q", meta.Method, config.MethodLocal)
	}
}

func TestPackImport_HTTPRuleInfersIDAndPreservesFrontmatter(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	setupMinimalConfig(t, configDir)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("---\nname: incident\ndescription: Incident rule\n---\nKeep timelines factual.\n"))
	}))
	t.Cleanup(server.Close)

	err := PackImport(context.Background(), PackImportRequest{
		Source:    server.URL + "/incident.md",
		Category:  domain.CategoryRules,
		Name:      "rules-pack",
		ConfigDir: configDir,
	}, nil)
	if err != nil {
		t.Fatalf("PackImport: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(configDir, "packs", "rules-pack", "rules", "incident.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "---\nname: incident\ndescription: Incident rule\n---\nKeep timelines factual.\n" {
		t.Fatalf("imported rule changed content:\n%s", raw)
	}
}

func TestPackImport_ExistingPackAppendsExplicitManifest(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	setupMinimalConfig(t, configDir)

	packDir := filepath.Join(configDir, "packs", "team-pack")
	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := config.PackManifest{
		SchemaVersion: config.PackSchemaVersion,
		Name:          "team-pack",
		Version:       "0.1.0",
		Root:          ".",
		Rules:         []string{"existing"},
	}
	if err := config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "rules", "existing.md"), []byte("---\nname: existing\ndescription: Existing\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(t.TempDir(), "incident.md")
	if err := os.WriteFile(source, []byte("Keep timelines factual.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := PackImport(context.Background(), PackImportRequest{
		Source:     source,
		Category:   domain.CategoryRules,
		TargetPack: "team-pack",
		ConfigDir:  configDir,
	}, nil); err != nil {
		t.Fatalf("PackImport: %v", err)
	}

	updated, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(updated.Rules, ",") != "existing,incident" {
		t.Fatalf("manifest rules = %v, want [existing incident]", updated.Rules)
	}
	if _, err := os.Stat(filepath.Join(packDir, "rules", "incident.md")); err != nil {
		t.Fatalf("imported rule missing: %v", err)
	}
}

func TestPackImport_ExistingPackPreservesAutoDiscovery(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	setupMinimalConfig(t, configDir)

	packDir := filepath.Join(configDir, "packs", "auto-pack")
	if err := os.MkdirAll(filepath.Join(packDir, "skills", "existing"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := config.PackManifest{
		SchemaVersion: config.PackSchemaVersion,
		Name:          "auto-pack",
		Version:       "0.1.0",
		Root:          ".",
	}
	if err := config.SavePackManifest(filepath.Join(packDir, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "skills", "existing", "SKILL.md"), []byte("---\nname: existing\ndescription: Existing\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(t.TempDir(), "review.md")
	if err := os.WriteFile(source, []byte("Review before responding.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := PackImport(context.Background(), PackImportRequest{
		Source:     source,
		Category:   domain.CategorySkills,
		TargetPack: "auto-pack",
		ConfigDir:  configDir,
	}, nil); err != nil {
		t.Fatalf("PackImport: %v", err)
	}

	loaded, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Skills) != 0 {
		t.Fatalf("manifest skills = %v, want omitted auto-discovery field", loaded.Skills)
	}
	if err := config.DiscoverContent(&loaded, packDir); err != nil {
		t.Fatal(err)
	}
	if strings.Join(loaded.Skills, ",") != "existing,review" {
		t.Fatalf("discovered skills = %v, want [existing review]", loaded.Skills)
	}
}

func TestPackImportRejectsNativePack(t *testing.T) {
	t.Parallel()
	for _, format := range []string{plugin.Claude, plugin.CodexLegacy, plugin.AgentPlugins} {
		t.Run(format, func(t *testing.T) {
			source, configDir := t.TempDir(), t.TempDir()
			manifest, body := ".codex-plugin/plugin.json", `{"name":"probe"}`
			if format == plugin.Claude {
				manifest = ".claude-plugin/plugin.json"
			} else if format == plugin.AgentPlugins {
				manifest, body = "plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"probe"}`
			}
			writeFile(t, filepath.Join(source, manifest), body)
			writeFile(t, filepath.Join(source, "skills/original/SKILL.md"), "---\nname: original\ndescription: Fixture\n---\nOriginal\n")
			if err := PackInstall(context.Background(), PackInstallRequest{PackPath: source, ConfigDir: configDir, Name: "alias", Plugin: &domain.PluginSource{Format: format, Name: "probe", Marketplace: "market"}}, nil); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(PacksDir(configDir), "alias")
			before, err := packTreeDigest(root)
			if err != nil {
				t.Fatal(err)
			}
			lockBefore := mustRead(t, config.LockfilePath(configDir))
			markdown := filepath.Join(t.TempDir(), "extra.md")
			writeFile(t, markdown, "Extra\n")
			for _, category := range []domain.PackCategory{domain.CategorySkills, domain.CategoryRules, domain.CategoryPrompts} {
				err := PackImport(context.Background(), PackImportRequest{Source: markdown, TargetPack: "alias", Category: category, ConfigDir: configDir}, nil)
				if err == nil || !strings.Contains(err.Error(), "plugin-derived pack") {
					t.Fatalf("%s import should reject native pack: %v", category, err)
				}
			}
			after, err := packTreeDigest(root)
			if err != nil || before != after || !bytes.Equal(lockBefore, mustRead(t, config.LockfilePath(configDir))) {
				t.Fatalf("refused import changed source or metadata: %v", err)
			}
			if _, _, err := engine.New(nil, nil).Resolve(config.ProfileConfig{Packs: []config.PackEntry{{Name: "alias"}}}, "", configDir, config.CollisionError, nil); err != nil {
				t.Fatalf("refused import broke profile resolution: %v", err)
			}
		})
	}
}

func TestPackImportGeneratedFrontmatterEscapesYAMLScalars(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test exercises ':' in source filename and skill directory name; ':' is reserved on Windows filesystems")
	}
	t.Parallel()
	configDir := t.TempDir()
	setupMinimalConfig(t, configDir)

	source := filepath.Join(t.TempDir(), "ops: west.md")
	if err := os.WriteFile(source, []byte("Run regional checks.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := PackImport(context.Background(), PackImportRequest{
		Source:    source,
		Category:  domain.CategorySkills,
		Name:      "yaml-pack",
		ConfigDir: configDir,
	}, nil)
	if err != nil {
		t.Fatalf("PackImport: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(configDir, "packs", "yaml-pack", "skills", "ops: west", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	fm, _, err := domain.SplitFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	var parsed domain.SkillFrontmatter
	if err := yaml.Unmarshal(fm, &parsed); err != nil {
		t.Fatalf("generated frontmatter is invalid YAML: %v\n%s", err, raw)
	}
	if parsed.Name != "ops: west" || parsed.Description != "Imported from ops: west.md" {
		t.Fatalf("frontmatter = %+v", parsed)
	}
}
