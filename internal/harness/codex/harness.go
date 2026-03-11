package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"
)

// Harness implements the v2 harness.Harness interface for Codex.
type Harness struct{}

func (Harness) ID() domain.Harness { return domain.HarnessCodex }

func (Harness) PackRelativePaths() []string { return []string{"codex/config.toml"} }

func (Harness) SettingsPaths(scope domain.Scope, baseDir, _ string) []string {
	if scope == domain.ScopeProject {
		return []string{SettingsProjectPath(baseDir)}
	}
	p := SettingsGlobalPath(baseDir)
	if p == "" {
		return nil
	}
	return []string{p}
}

func (Harness) ManagedRoots(scope domain.Scope, baseDir, _ string) []string {
	if scope == domain.ScopeProject {
		return ManagedRootsProject(baseDir)
	}
	return ManagedRootsGlobal(baseDir)
}

func (Harness) StrictExtraDirs(_ domain.Scope, _, _ string) []string { return nil }

// Plan produces a Fragment from typed content.
func (Harness) Plan(ctx engine.SyncContext) (domain.Fragment, error) {
	var f domain.Fragment

	switch ctx.Scope {
	case domain.ScopeProject:
		if err := planProject(&f, ctx); err != nil {
			return domain.Fragment{}, err
		}
	case domain.ScopeGlobal:
		if err := planGlobal(&f, ctx); err != nil {
			return domain.Fragment{}, err
		}
	}

	return f, nil
}

func planProject(f *domain.Fragment, ctx engine.SyncContext) error {
	skillsSubDir := filepath.Join(".agents", "skills")

	if rules := ctx.Profile.AllRules(); len(rules) > 0 {
		baseAgents := filepath.Join(ctx.TargetDir, "AGENTS.md")
		var existing string
		b, err := os.ReadFile(baseAgents)
		if err == nil {
			existing = string(b)
		}
		override := buildAgentsOverride(rules, existing)
		dst := filepath.Join(ctx.TargetDir, "AGENTS.override.md")
		f.Writes = append(f.Writes, domain.WriteAction{Dst: dst, Content: []byte(override)})
		f.Desired = append(f.Desired, dst)
	}
	f.AddSkillCopies(ctx.TargetDir, skillsSubDir, ctx.Profile.AllSkills())
	addPromotedWorkflows(f, ctx.TargetDir, skillsSubDir, ctx.Profile.AllWorkflows())
	addPromotedAgents(f, ctx.TargetDir, skillsSubDir, ctx.Profile.AllAgents())

	sp := ctx.Profile.SettingsPackName(domain.HarnessCodex)
	dst := SettingsProjectPath(ctx.TargetDir)
	hasMCP := len(ctx.Profile.MCPServers) > 0

	decision := engine.ClassifySettings(hasMCP, hasMCP, ctx.SkipSettings)
	if decision.EmitSettings {
		base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessCodex, "config.toml")
		out, _, err := RenderBytes(base, ctx.Profile.MCPServers, true)
		if err != nil {
			return err
		}
		f.Settings = append(f.Settings, domain.SettingsAction{
			Dst: dst, Desired: out, Harness: domain.HarnessCodex,
			Label: "config.toml", SourcePack: sp,
		})
		f.Desired = append(f.Desired, filepath.Clean(dst))
	} else if decision.EmitMCPPlugin {
		managed, _, err := RenderMCPOnly(ctx.Profile.MCPServers, true)
		if err != nil {
			return err
		}
		f.Plugins = append(f.Plugins, domain.SettingsAction{
			Dst: dst, Desired: managed, Harness: domain.HarnessCodex,
			Label: "config.toml (managed keys)", SourcePack: sp, MergeMode: decision.MergeMode,
		})
		f.Desired = append(f.Desired, filepath.Clean(dst))
	}

	return nil
}

func planGlobal(f *domain.Fragment, ctx engine.SyncContext) error {
	codexHome := filepath.Join(ctx.TargetDir, ".codex")
	skillsSubDir := filepath.Join(".agents", "skills")

	if rules := ctx.Profile.AllRules(); len(rules) > 0 {
		baseAgents := filepath.Join(codexHome, "AGENTS.md")
		var existing string
		b, err := os.ReadFile(baseAgents)
		if err == nil {
			existing = string(b)
		}
		override := buildAgentsOverride(rules, existing)
		dst := filepath.Join(codexHome, "AGENTS.override.md")
		f.Writes = append(f.Writes, domain.WriteAction{Dst: dst, Content: []byte(override)})
		f.Desired = append(f.Desired, dst)
	}
	f.AddSkillCopies(ctx.TargetDir, skillsSubDir, ctx.Profile.AllSkills())
	addPromotedWorkflows(f, ctx.TargetDir, skillsSubDir, ctx.Profile.AllWorkflows())
	addPromotedAgents(f, ctx.TargetDir, skillsSubDir, ctx.Profile.AllAgents())

	sp := ctx.Profile.SettingsPackName(domain.HarnessCodex)
	codexConfigPath := SettingsGlobalPath(ctx.TargetDir)
	hasMCP := len(ctx.Profile.MCPServers) > 0

	decision := engine.ClassifySettings(hasMCP, hasMCP, ctx.SkipSettings)
	if decision.EmitSettings {
		base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessCodex, "config.toml")
		out, _, err := RenderBytes(base, ctx.Profile.MCPServers, true)
		if err != nil {
			return err
		}
		f.Settings = append(f.Settings, domain.SettingsAction{
			Dst: codexConfigPath, Desired: out, Harness: domain.HarnessCodex,
			Label: "config.toml", SourcePack: sp,
		})
		f.Desired = append(f.Desired, filepath.Clean(codexConfigPath))
	} else if decision.EmitMCPPlugin {
		managed, _, err := RenderMCPOnly(ctx.Profile.MCPServers, true)
		if err != nil {
			return err
		}
		f.Plugins = append(f.Plugins, domain.SettingsAction{
			Dst: codexConfigPath, Desired: managed, Harness: domain.HarnessCodex,
			Label: "config.toml (managed keys)", SourcePack: sp, MergeMode: decision.MergeMode,
		})
		f.Desired = append(f.Desired, filepath.Clean(codexConfigPath))
	}

	return nil
}

func buildAgentsOverride(rules []domain.Rule, existingAgents string) string {
	out := BuildManagedContent("# aipack managed rules (flattened)", engine.FlattenRules(rules))
	if strings.TrimSpace(existingAgents) != "" {
		out += "\n---\n\n<!-- preserved from existing AGENTS.md -->\n\n"
		out += strings.TrimRight(existingAgents, "\n")
		out += "\n"
	}
	return out
}

// Render produces a Fragment for pack rendering.
func (Harness) Render(ctx harness.RenderContext) (domain.Fragment, error) {
	base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessCodex, "config.toml")
	out, _, err := RenderBytes(base, ctx.Profile.MCPServers, false)
	if err != nil {
		return domain.Fragment{}, err
	}
	p := filepath.Join(ctx.OutDir, "codex", "config.toml")
	return domain.Fragment{
		Writes:  []domain.WriteAction{{Dst: p, Content: out}},
		Desired: []string{p},
	}, nil
}

// StripManagedSettings removes sync-managed keys from rendered settings.
func (Harness) StripManagedSettings(rendered []byte, _ string) ([]byte, error) {
	return StripManagedKeys(rendered)
}

// Capture extracts Codex content for round-trip save.
func (Harness) Capture(ctx harness.CaptureContext) (harness.CaptureResult, error) {
	res := harness.NewCaptureResult()

	if ctx.Scope == domain.ScopeProject {
		return captureProject(ctx.ProjectDir, res)
	}
	return captureGlobal(ctx.Home, res)
}

func captureProject(projectDir string, res harness.CaptureResult) (harness.CaptureResult, error) {
	skillCopies, skills := harness.CaptureSkills(
		filepath.Join(projectDir, ".agents", "skills"),
		"skills",
	)
	res.Copies = append(res.Copies, skillCopies...)
	res.Skills = append(res.Skills, skills...)
	// AGENTS.override.md is fully generated by sync — not captured.

	settingsPath := SettingsProjectPath(projectDir)
	if b, ok, err := util.ReadFileIfExists(settingsPath); err != nil {
		return res, fmt.Errorf("capture codex project settings: %w", err)
	} else if ok {
		res.Writes = append(res.Writes, domain.WriteAction{
			Dst: filepath.Join("configs", "codex", "config.toml"), Content: b, Src: settingsPath,
		})
		res.Warnings = append(res.Warnings, parseCodexSettings(res.MCPServers, res.AllowedTools, b)...)
	}
	return res, nil
}

func captureGlobal(home string, res harness.CaptureResult) (harness.CaptureResult, error) {
	codexHome := filepath.Join(home, ".codex")

	skillCopies, skills := harness.CaptureSkills(
		filepath.Join(home, ".agents", "skills"),
		"skills",
	)
	res.Copies = append(res.Copies, skillCopies...)
	res.Skills = append(res.Skills, skills...)

	// Capture user-authored .rules files (flat Dst).
	rulesDir := filepath.Join(codexHome, "rules")
	if entries, err := os.ReadDir(rulesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.HasSuffix(strings.ToLower(e.Name()), ".rules") {
				src := filepath.Join(rulesDir, e.Name())
				res.Copies = append(res.Copies, domain.CopyAction{
					Src: src, Dst: filepath.Join("rules", e.Name()), Kind: domain.CopyKindFile,
				})
			}
		}
	} else if !os.IsNotExist(err) {
		res.Warnings = append(res.Warnings, domain.Warning{Path: rulesDir, Message: fmt.Sprintf("reading directory: %v", err)})
	}
	// AGENTS.override.md is fully generated by sync — not captured.

	settingsPath := SettingsGlobalPath(home)
	if b, ok, err := util.ReadFileIfExists(settingsPath); err != nil {
		return res, fmt.Errorf("capture codex global settings: %w", err)
	} else if ok {
		res.Writes = append(res.Writes, domain.WriteAction{
			Dst: filepath.Join("configs", "codex", "config.toml"), Content: b, Src: settingsPath,
		})
		res.Warnings = append(res.Warnings, parseCodexSettings(res.MCPServers, res.AllowedTools, b)...)
	}
	return res, nil
}

type codexSettingsCapture struct {
	MCPServers map[string]codexCapturedServer `toml:"mcp_servers"`
}

type codexCapturedServer struct {
	Command      string            `toml:"command"`
	Args         []string          `toml:"args"`
	EnabledTools []string          `toml:"enabled_tools"`
	Env          map[string]string `toml:"env"`
}

func parseCodexSettings(servers map[string]domain.MCPServer, allowed map[string][]string, b []byte) []domain.Warning {
	var cfg codexSettingsCapture
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return []domain.Warning{{Message: fmt.Sprintf("failed to parse Codex config.toml: %v", err)}}
	}
	for name, entry := range cfg.MCPServers {
		cmd := entry.Command
		if cmd == "" {
			continue
		}
		argv := []string{cmd}
		argv = append(argv, entry.Args...)
		env := entry.Env
		if env == nil {
			env = map[string]string{}
		}
		servers[name] = domain.MCPServer{
			Name:      name,
			Transport: domain.TransportStdio,
			Command:   argv,
			Env:       env,
		}
		if len(entry.EnabledTools) > 0 {
			allowed[name] = append([]string{}, entry.EnabledTools...)
			sort.Strings(allowed[name])
		}
	}
	return nil
}
