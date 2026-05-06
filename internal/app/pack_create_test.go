package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestPackCreate_Local_ScaffoldsValidPack(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	setupMinimalConfig(t, cfgDir)

	if err := PackCreate(PackCreateRequest{Name: "my-pack", ConfigDir: cfgDir, Local: true}); err != nil {
		t.Fatalf("PackCreate: %v", err)
	}

	packDir := filepath.Join(cfgDir, "packs", "my-pack")

	// Verify pack.json round-trips through LoadPackManifest.
	m, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
	if err != nil {
		t.Fatalf("LoadPackManifest: %v", err)
	}
	if m.Name != "my-pack" {
		t.Fatalf("name = %q, want %q", m.Name, "my-pack")
	}
	if m.SchemaVersion != config.PackSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", m.SchemaVersion, config.PackSchemaVersion)
	}
	if m.Root != "." {
		t.Fatalf("root = %q, want %q", m.Root, ".")
	}

	// Content vector fields must be empty so DiscoverContent auto-discovers.
	if len(m.Rules) != 0 {
		t.Fatalf("Rules = %v, want empty (auto-discovery friendly)", m.Rules)
	}
	if len(m.Agents) != 0 {
		t.Fatalf("Agents = %v, want empty (auto-discovery friendly)", m.Agents)
	}
	if len(m.Workflows) != 0 {
		t.Fatalf("Workflows = %v, want empty (auto-discovery friendly)", m.Workflows)
	}
	if len(m.Skills) != 0 {
		t.Fatalf("Skills = %v, want empty (auto-discovery friendly)", m.Skills)
	}

	// Verify all vector dirs exist.
	for _, sub := range []string{"rules", "agents", "workflows", "skills", "plugins", "mcp", "configs", "profiles"} {
		d := filepath.Join(packDir, sub)
		st, err := os.Stat(d)
		if err != nil {
			t.Fatalf("missing dir %s: %v", sub, err)
		}
		if !st.IsDir() {
			t.Fatalf("%s is not a directory", sub)
		}
	}

	// Verify starter profile exists and is referenced in manifest.
	if len(m.Profiles) != 1 || m.Profiles[0] != "my-pack" {
		t.Fatalf("Profiles = %v, want [my-pack]", m.Profiles)
	}
	profileCfg, err := config.LoadProfile(filepath.Join(packDir, "profiles", "my-pack.yaml"))
	if err != nil {
		t.Fatalf("parse starter profile: %v", err)
	}
	if len(profileCfg.Packs) != 1 || profileCfg.Packs[0].Name != "my-pack" {
		t.Fatalf("starter profile packs = %+v, want [{Name:my-pack}]", profileCfg.Packs)
	}

	// Verify registered in lockfile.
	lf, err := config.LoadLockfile(config.LockfilePath(cfgDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta, ok := lf.Packs["my-pack"]
	if !ok {
		t.Fatal("pack not registered in lockfile")
	}
	if meta.Method != config.MethodLocal {
		t.Fatalf("method = %q, want %q", meta.Method, config.MethodLocal)
	}
}

func TestPackCreate_Link_CreatesInCWDAndSymlinks(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	setupMinimalConfig(t, cfgDir)

	workDir := t.TempDir()

	if err := PackCreate(PackCreateRequest{Name: "linked-pack", ConfigDir: cfgDir, Local: false, WorkDir: workDir}); err != nil {
		t.Fatalf("PackCreate: %v", err)
	}

	// Content should exist in work dir.
	contentDir := filepath.Join(workDir, "linked-pack")
	if _, err := os.Stat(filepath.Join(contentDir, "pack.json")); err != nil {
		t.Fatalf("pack.json missing in CWD: %v", err)
	}

	// Symlink should exist in packs dir.
	linkPath := filepath.Join(cfgDir, "packs", "linked-pack")
	fi, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("symlink missing in packs dir: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink at %s, got %v", linkPath, fi.Mode())
	}

	// Verify registered with link method.
	lf, err := config.LoadLockfile(config.LockfilePath(cfgDir))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	meta, ok := lf.Packs["linked-pack"]
	if !ok {
		t.Fatal("pack not registered in lockfile")
	}
	if meta.Method != config.MethodLink {
		t.Fatalf("method = %q, want %q", meta.Method, config.MethodLink)
	}
}

func TestPackCreate_AutoDiscoveryWorksWithScaffold(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	setupMinimalConfig(t, cfgDir)

	if err := PackCreate(PackCreateRequest{Name: "disco-pack", ConfigDir: cfgDir, Local: true}); err != nil {
		t.Fatalf("PackCreate: %v", err)
	}

	dir := filepath.Join(cfgDir, "packs", "disco-pack")

	// Add content files to the scaffolded directories.
	if err := os.WriteFile(filepath.Join(dir, "rules", "my-rule.md"), []byte("# Rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(dir, "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Load manifest and run auto-discovery — should find the new content.
	m, err := config.LoadPackManifest(filepath.Join(dir, "pack.json"))
	if err != nil {
		t.Fatalf("LoadPackManifest: %v", err)
	}
	if err := config.DiscoverContent(&m, dir); err != nil {
		t.Fatalf("DiscoverContent: %v", err)
	}

	if len(m.Rules) != 1 || m.Rules[0] != "my-rule" {
		t.Fatalf("Rules = %v, want [my-rule]", m.Rules)
	}
	if len(m.Skills) != 1 || m.Skills[0] != "my-skill" {
		t.Fatalf("Skills = %v, want [my-skill]", m.Skills)
	}
}

func TestPackCreate_ErrorOnExistingPackJSON(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	setupMinimalConfig(t, cfgDir)

	// Pre-create the pack directory with a pack.json.
	packDir := filepath.Join(cfgDir, "packs", "test")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := PackCreate(PackCreateRequest{Name: "test", ConfigDir: cfgDir, Local: true})
	if err == nil {
		t.Fatal("expected error on existing pack.json")
	}
}

func TestPackCreate_ErrorOnEmptyName(t *testing.T) {
	t.Parallel()
	err := PackCreate(PackCreateRequest{Name: "", ConfigDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestPackCreate_ErrorOnInvalidName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"../evil", "foo/bar", `foo\bar`} {
		if err := PackCreate(PackCreateRequest{Name: name, ConfigDir: t.TempDir()}); err == nil {
			t.Errorf("expected error for name %q", name)
		}
	}
}

func TestPackCreate_ErrorOnEmptyConfigDir(t *testing.T) {
	t.Parallel()
	err := PackCreate(PackCreateRequest{Name: "test", ConfigDir: ""})
	if err == nil {
		t.Fatal("expected error for empty config dir")
	}
}

func TestPackCreate_ContentFlags_SymlinksDirectories(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	setupMinimalConfig(t, cfgDir)

	rulesDir := filepath.Join(t.TempDir(), "my-rules")
	os.MkdirAll(rulesDir, 0o755)
	os.WriteFile(filepath.Join(rulesDir, "style.md"),
		[]byte("---\nname: style\n---\nbody\n"), 0o644)

	skillsDir := filepath.Join(t.TempDir(), "my-skills")
	os.MkdirAll(filepath.Join(skillsDir, "deploy"), 0o755)
	os.WriteFile(filepath.Join(skillsDir, "deploy", "SKILL.md"),
		[]byte("---\nname: deploy\ndescription: d\n---\nbody\n"), 0o644)

	err := PackCreate(PackCreateRequest{
		Name:      "composed",
		ConfigDir: cfgDir,
		Local:     true,
		ContentSources: map[domain.PackCategory]string{
			domain.CategoryRules:  rulesDir,
			domain.CategorySkills: skillsDir,
		},
	})
	if err != nil {
		t.Fatalf("PackCreate: %v", err)
	}

	packDir := filepath.Join(cfgDir, "packs", "composed")

	// rules/ should be a symlink to the source.
	link, err := os.Readlink(filepath.Join(packDir, "rules"))
	if err != nil {
		t.Fatalf("rules/ is not a symlink: %v", err)
	}
	if link != rulesDir {
		t.Fatalf("rules/ target = %q, want %q", link, rulesDir)
	}

	// skills/ should be a symlink.
	link, err = os.Readlink(filepath.Join(packDir, "skills"))
	if err != nil {
		t.Fatalf("skills/ is not a symlink: %v", err)
	}
	if link != skillsDir {
		t.Fatalf("skills/ target = %q, want %q", link, skillsDir)
	}

	// pack.json discovers content through symlinks.
	manifest, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
	if err != nil {
		t.Fatalf("LoadPackManifest: %v", err)
	}
	if len(manifest.Rules) != 1 || manifest.Rules[0] != "style" {
		t.Fatalf("rules = %v, want [style]", manifest.Rules)
	}
	if len(manifest.Skills) != 1 || manifest.Skills[0] != "deploy" {
		t.Fatalf("skills = %v, want [deploy]", manifest.Skills)
	}

	// Unmapped types should have scaffold dirs (not symlinks).
	agentsInfo, err := os.Lstat(filepath.Join(packDir, "agents"))
	if err != nil {
		t.Fatalf("agents/ missing: %v", err)
	}
	if agentsInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatal("agents/ should be a regular dir, not a symlink")
	}
}

func TestPackCreate_ContentFlags_NonexistentSourceErrors(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	setupMinimalConfig(t, cfgDir)

	err := PackCreate(PackCreateRequest{
		Name:      "bad-source",
		ConfigDir: cfgDir,
		Local:     true,
		ContentSources: map[domain.PackCategory]string{
			domain.CategoryRules: "/no/such/path",
		},
	})
	if err == nil {
		t.Fatal("expected error for nonexistent content source path")
	}
	if !strings.Contains(err.Error(), "/no/such/path") {
		t.Fatalf("error should reference the bad path, got: %v", err)
	}
}

// setupMinimalConfig creates sync-config.yaml so packRecordOrigin can load it.
func setupMinimalConfig(t *testing.T, cfgDir string) {
	t.Helper()
	sc := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	if err := config.SaveSyncConfig(config.SyncConfigPath(cfgDir), sc); err != nil {
		t.Fatalf("setup sync-config: %v", err)
	}
}
