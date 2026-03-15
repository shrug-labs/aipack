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
	return planCodex(f, ctx, ctx.TargetDir, ctx.TargetDir, SettingsProjectPath(ctx.TargetDir))
}

func planGlobal(f *domain.Fragment, ctx engine.SyncContext) error {
	codexHome := filepath.Join(ctx.TargetDir, ".codex")
	return planCodex(f, ctx, codexHome, ctx.TargetDir, SettingsGlobalPath(ctx.TargetDir))
}

// planCodex is the shared implementation for both project and global scope.
// overrideBase is where AGENTS.override.md lives; skillsBase is where .agents/skills/ lives.
func planCodex(f *domain.Fragment, ctx engine.SyncContext, overrideBase, skillsBase, settingsPath string) error {
	skillsSubDir := filepath.Join(".agents", "skills")

	if rules := ctx.Profile.AllRules(); len(rules) > 0 {
		baseAgents := filepath.Join(overrideBase, "AGENTS.md")
		var existing string
		b, err := os.ReadFile(baseAgents)
		if err == nil {
			existing = string(b)
		}
		override := buildAgentsOverride(rules, existing)
		dst := filepath.Join(overrideBase, "AGENTS.override.md")
		f.Writes = append(f.Writes, domain.WriteAction{Dst: dst, Content: []byte(override)})
		f.Desired = append(f.Desired, dst)
	}
	f.AddSkillCopies(skillsBase, skillsSubDir, ctx.Profile.AllSkills())
	addPromotedWorkflows(f, skillsBase, skillsSubDir, ctx.Profile.AllWorkflows())
	addPromotedAgents(f, skillsBase, skillsSubDir, ctx.Profile.AllAgents())

	sp := ctx.Profile.SettingsPackName(domain.HarnessCodex)
	hasMCP := len(ctx.Profile.MCPServers) > 0

	decision := engine.ClassifySettings(hasMCP, hasMCP, ctx.SkipSettings)
	var mcpRendered []byte
	if decision.EmitSettings {
		base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessCodex, "config.toml")
		out, _, err := RenderBytes(base, ctx.Profile.MCPServers)
		if err != nil {
			return err
		}
		mcpRendered = out
		f.Settings = append(f.Settings, domain.SettingsAction{
			Dst: settingsPath, Desired: out, Harness: domain.HarnessCodex,
			Label: "config.toml", SourcePack: sp, MergeMode: true,
		})
		f.Desired = append(f.Desired, filepath.Clean(settingsPath))
	} else if decision.EmitMCP {
		managed, _, err := RenderMCPOnly(ctx.Profile.MCPServers)
		if err != nil {
			return err
		}
		mcpRendered = managed
		f.MCP = append(f.MCP, domain.SettingsAction{
			Dst: settingsPath, Desired: managed, Harness: domain.HarnessCodex,
			Label: "config.toml (managed keys)", SourcePack: sp, MergeMode: decision.MergeMode,
		})
		f.Desired = append(f.Desired, filepath.Clean(settingsPath))
	}
	if hasMCP && len(mcpRendered) > 0 {
		planned := map[string]domain.MCPServer{}
		parseCodexSettings(planned, map[string][]string{}, mcpRendered)
		mcpActions, err := domain.BuildMCPActions(
			settingsPath,
			domain.HarnessCodex,
			harness.PlannedMCPServers(ctx.Profile.MCPServers, planned),
			true,
		)
		if err != nil {
			return err
		}
		f.MCPServers = append(f.MCPServers, mcpActions...)
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
	out, _, err := RenderBytes(base, ctx.Profile.MCPServers)
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

// CleanActions returns operations to reset Codex managed state.
func (Harness) CleanActions(scope domain.Scope, baseDir, _ string) []harness.CleanAction {
	var actions []harness.CleanAction
	var configPath string
	if scope == domain.ScopeProject {
		configPath = SettingsProjectPath(baseDir)
		actions = append(actions,
			harness.CleanAction{Path: filepath.Join(baseDir, ".agents", "skills")},
			harness.CleanAction{Path: filepath.Join(baseDir, "AGENTS.override.md")},
		)
	} else {
		configPath = SettingsGlobalPath(baseDir)
		codexHome := filepath.Join(baseDir, ".codex")
		actions = append(actions,
			harness.CleanAction{Path: filepath.Join(baseDir, ".agents", "skills")},
			harness.CleanAction{Path: filepath.Join(codexHome, "rules")},
			harness.CleanAction{Path: filepath.Join(codexHome, "AGENTS.override.md")},
		)
	}
	actions = append(actions, harness.CleanAction{
		Path:   configPath,
		Format: harness.CleanTOML,
		Edit: func(root map[string]any) {
			delete(root, "mcp_servers")
			if m, ok := root["mcp"].(map[string]any); ok {
				delete(m, "servers")
				if len(m) == 0 {
					delete(root, "mcp")
				} else {
					root["mcp"] = m
				}
			}
		},
	})
	return actions
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
	harness.CapturePromotedContent(
		filepath.Join(projectDir, ".agents", "skills"),
		&res,
	)
	// AGENTS.override.md is fully generated by sync — not captured.
	// Rules are flattened into AGENTS.override.md during Plan and cannot be
	// individually recovered. CaptureResult.Rules will be empty for Codex.
	// This is a one-way transform: rules must be round-tripped through the
	// pack source, not through the harness.

	settingsPath := SettingsProjectPath(projectDir)
	if b, ok, err := util.ReadFileIfExists(settingsPath); err != nil {
		return res, fmt.Errorf("capture codex project settings: %w", err)
	} else if ok {
		res.Writes = append(res.Writes, domain.WriteAction{
			Dst: filepath.Join("configs", "codex", "config.toml"), Content: b, Src: settingsPath,
		})
		res.Warnings = append(res.Warnings, parseCodexSettings(res.MCPServers, res.AllowedTools, b)...)
	}
	res.MaterializeCapturedMCP(settingsPath)
	return res, nil
}

func captureGlobal(home string, res harness.CaptureResult) (harness.CaptureResult, error) {
	codexHome := filepath.Join(home, ".codex")

	harness.CapturePromotedContent(
		filepath.Join(home, ".agents", "skills"),
		&res,
	)

	// Capture user-authored .rules files (flat Dst).
	// These are Codex-native format (not markdown+frontmatter), so they're
	// captured as CopyActions rather than parsed into typed domain.Rule values.
	// Other harnesses (claudecode, cline, opencode) use .md rules that get
	// parsed; Codex rules stay as opaque files for round-trip fidelity.
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
	// AGENTS.override.md is fully generated by sync — not captured (see captureProject).

	settingsPath := SettingsGlobalPath(home)
	if b, ok, err := util.ReadFileIfExists(settingsPath); err != nil {
		return res, fmt.Errorf("capture codex global settings: %w", err)
	} else if ok {
		res.Writes = append(res.Writes, domain.WriteAction{
			Dst: filepath.Join("configs", "codex", "config.toml"), Content: b, Src: settingsPath,
		})
		res.Warnings = append(res.Warnings, parseCodexSettings(res.MCPServers, res.AllowedTools, b)...)
	}
	res.MaterializeCapturedMCP(settingsPath)
	return res, nil
}

type codexSettingsCapture struct {
	MCPServers map[string]codexCapturedServer `toml:"mcp_servers"`
}

type codexCapturedServer struct {
	Type          string            `toml:"type"`
	Command       string            `toml:"command"`
	Args          []string          `toml:"args"`
	EnabledTools  []string          `toml:"enabled_tools"`
	DisabledTools []string          `toml:"disabled_tools"`
	Env           map[string]string `toml:"env"`
	URL           string            `toml:"url"`
	Headers       map[string]string `toml:"headers"`
}

func parseCodexSettings(servers map[string]domain.MCPServer, allowed map[string][]string, b []byte) []domain.Warning {
	var cfg codexSettingsCapture
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return []domain.Warning{{Message: fmt.Sprintf("failed to parse Codex config.toml: %v", err)}}
	}
	for name, entry := range cfg.MCPServers {
		transport := entry.Type
		if transport == "" {
			transport = domain.TransportStdio
		}
		srv := domain.MCPServer{
			Name:      name,
			Transport: transport,
		}
		if srv.IsStdio() {
			if entry.Command == "" {
				continue
			}
			argv := []string{entry.Command}
			argv = append(argv, entry.Args...)
			srv.Command = argv
			env := entry.Env
			if env == nil {
				env = map[string]string{}
			}
			srv.Env = env
		} else {
			srv.URL = entry.URL
			if len(entry.Headers) > 0 {
				srv.Headers = entry.Headers
			}
		}
		if len(entry.DisabledTools) > 0 {
			srv.DisabledTools = append([]string{}, entry.DisabledTools...)
			sort.Strings(srv.DisabledTools)
		}
		servers[name] = srv
		if len(entry.EnabledTools) > 0 {
			allowed[name] = append([]string{}, entry.EnabledTools...)
			sort.Strings(allowed[name])
		}
	}
	return nil
}
