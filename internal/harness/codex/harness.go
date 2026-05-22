package codex

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
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

// Layout describes Codex's filesystem footprint for a given scope.
func (Harness) Layout(scope domain.Scope, baseDir, _ string) harness.Layout {
	paths := PathsForScope(scope)
	configPath := filepath.Join(baseDir, paths.SettingsFile)
	hooksPath := filepath.Join(baseDir, paths.HooksFile)
	agentsDir := filepath.Join(baseDir, paths.AgentsDir)
	l := harness.Layout{
		ValidationRoots: []string{
			filepath.Join(baseDir, paths.SkillsDir),
			agentsDir,
			filepath.Join(baseDir, paths.OverrideFile),
			configPath,
			hooksPath,
		},
		RemovePaths: []string{
			filepath.Join(baseDir, paths.SkillsDir),
			agentsDir,
			filepath.Join(baseDir, paths.OverrideFile),
			hooksPath,
		},
	}
	if configPath != "" {
		l.OwnedFiles = []harness.OwnedFile{{
			Path: configPath, Format: harness.FormatTOML,
			Strip: func(root map[string]any) {
				delete(root, "mcp_servers")
				delete(root, "agents")
				stripManagedHookState(root, hooksPath)
			},
			Reset: func(root map[string]any) {
				delete(root, "mcp_servers")
				delete(root, "agents")
				stripManagedHookState(root, hooksPath)
				if m, ok := root["mcp"].(map[string]any); ok {
					delete(m, "servers")
					if len(m) == 0 {
						delete(root, "mcp")
					} else {
						root["mcp"] = m
					}
				}
			},
		}}
	}
	return l
}

// Plan produces a Fragment from typed content.
func (Harness) Plan(_ context.Context, ctx engine.SyncContext) (domain.Fragment, error) {
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
	paths := ProjectPaths
	return planCodex(f, ctx, ctx.TargetDir, ctx.TargetDir, filepath.Join(ctx.TargetDir, paths.SettingsFile))
}

func planGlobal(f *domain.Fragment, ctx engine.SyncContext) error {
	paths := GlobalPaths
	overrideBase := filepath.Dir(filepath.Join(ctx.TargetDir, paths.OverrideFile))
	return planCodex(f, ctx, overrideBase, ctx.TargetDir, filepath.Join(ctx.TargetDir, paths.SettingsFile))
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
		override, err := buildAgentsOverride(rules, existing, ctx.Namespaced)
		if err != nil {
			return err
		}
		dst := filepath.Join(overrideBase, "AGENTS.override.md")
		f.Writes = append(f.Writes, domain.WriteAction{
			Dst:        dst,
			Content:    []byte(override),
			SourcePack: compositeSourcePack(rules, func(r domain.Rule) string { return r.SourcePack }),
		})
		f.Desired = append(f.Desired, dst)
	}
	skills := ctx.Profile.AllSkills()
	workflows := ctx.Profile.AllWorkflows()
	agents := ctx.Profile.AllAgents()

	if err := harness.CheckPromotionCollisions(ctx.Namespaced, skills, workflows, nil); err != nil {
		return fmt.Errorf("codex: %w", err)
	}

	harness.AddRenderedSkillCopies(f, skillsBase, skillsSubDir, ctx.Namespaced, skills)
	addPromotedWorkflows(f, skillsBase, skillsSubDir, ctx.Namespaced, workflows)

	// Render agents as native Codex TOML files and collect registration entries.
	paths := PathsForScope(ctx.Scope)
	agentsDir := filepath.Join(skillsBase, paths.AgentsDir)
	agentRegs, agentWarnings := addNativeAgents(f, agents, nativeAgentRenderContext{
		AgentsDir:     agentsDir,
		Skills:        skills,
		AllMCPServers: ctx.Profile.MCPServers,
		SkillsBase:    skillsBase,
		SkillsSubDir:  skillsSubDir,
		Namespaced:    ctx.Namespaced,
	})
	f.Warnings = append(f.Warnings, agentWarnings...)

	sp := ctx.Profile.SettingsPackName(domain.HarnessCodex)
	hasMCP := len(ctx.Profile.MCPServers) > 0
	hasAgents := len(agentRegs) > 0
	plugins := ctx.Profile.AllPlugins()
	hasPlugins := len(plugins) > 0
	hooksPath := filepath.Join(skillsBase, paths.HooksFile)
	renderedHooks, err := RenderHooksJSON(ctx.Profile.AllHooks(), hooksPath)
	if err != nil {
		return err
	}
	if len(renderedHooks.JSON) > 0 {
		f.Writes = append(f.Writes, domain.WriteAction{
			Dst:        hooksPath,
			Content:    renderedHooks.JSON,
			SourcePack: renderedHooks.SourcePack,
		})
		f.Desired = append(f.Desired, filepath.Clean(hooksPath))
	}
	hasHooks := len(renderedHooks.TrustState) > 0
	if sp == "" && hasHooks {
		sp = renderedHooks.SourcePack
	}
	hasManagedKeys := hasMCP || hasAgents || hasPlugins || hasHooks
	base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessCodex, "config.toml")

	decision := engine.ClassifySettings(hasManagedKeys, len(base) > 0, ctx.SkipSettings)
	var mcpRendered []byte
	if decision.EmitSettings {
		out, _, err := RenderBytesWithOptions(RenderOptions{
			Base:      base,
			Servers:   ctx.Profile.MCPServers,
			AgentRegs: agentRegs,
			Plugins:   plugins,
			HookState: renderedHooks.TrustState,
		})
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
		managed, _, err := RenderManagedKeysOnlyWithPluginsAndHookState(ctx.Profile.MCPServers, agentRegs, plugins, renderedHooks.TrustState)
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
		parseCodexSettings(planned, map[string][]string{}, nil, mcpRendered)
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

// compositeSourcePack returns the SourcePack for a composite file built from
// multiple items. If all items share the same pack, that name is returned;
// otherwise "(composite)" signals multi-pack provenance.
func compositeSourcePack[T any](items []T, sourcePack func(T) string) string {
	if len(items) == 0 {
		return ""
	}
	first := sourcePack(items[0])
	for _, item := range items[1:] {
		if sourcePack(item) != first {
			return "(composite)"
		}
	}
	return first
}

func buildAgentsOverride(rules []domain.Rule, existingAgents string, namespaced bool) (string, error) {
	renderedRules, err := codexRulesForFlatten(rules, namespaced)
	if err != nil {
		return "", err
	}
	out := BuildManagedContent("# aipack managed rules (flattened)", engine.FlattenRules(renderedRules))
	if strings.TrimSpace(existingAgents) != "" {
		out += "\n---\n\n<!-- preserved from existing AGENTS.md -->\n\n"
		out += strings.TrimRight(existingAgents, "\n")
		out += "\n"
	}
	return out, nil
}

func codexRulesForFlatten(rules []domain.Rule, namespaced bool) ([]domain.Rule, error) {
	if !namespaced {
		return rules, nil
	}
	out := make([]domain.Rule, 0, len(rules))
	for _, r := range rules {
		rendered, _, _, err := harness.RenderedRuleForHarness(r, namespaced)
		if err != nil {
			return nil, fmt.Errorf("render codex rule identity %s: %w", r.Name, err)
		}
		out = append(out, rendered)
	}
	return out, nil
}

// Render produces a Fragment for pack rendering.
func (Harness) Render(_ context.Context, ctx harness.RenderContext) (domain.Fragment, error) {
	base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessCodex, "config.toml")
	configPath := filepath.Join(ctx.OutDir, "codex", "config.toml")
	hooksPath := filepath.Join(ctx.OutDir, "codex", "hooks.json")
	renderedHooks, err := RenderHooksJSON(ctx.Profile.AllHooks(), hooksPath)
	if err != nil {
		return domain.Fragment{}, err
	}
	out, _, err := RenderBytesWithOptions(RenderOptions{
		Base:      base,
		Servers:   ctx.Profile.MCPServers,
		HookState: renderedHooks.TrustState,
	})
	if err != nil {
		return domain.Fragment{}, err
	}
	f := domain.Fragment{
		Writes:  []domain.WriteAction{{Dst: configPath, Content: out}},
		Desired: []string{configPath},
	}
	if len(renderedHooks.JSON) > 0 {
		f.Writes = append(f.Writes, domain.WriteAction{Dst: hooksPath, Content: renderedHooks.JSON, SourcePack: renderedHooks.SourcePack})
		f.Desired = append(f.Desired, hooksPath)
	}
	return f, nil
}

// Capture extracts Codex content for round-trip save.
func (Harness) Capture(_ context.Context, ctx harness.CaptureContext) (harness.CaptureResult, error) {
	res := harness.NewCaptureResult()

	if ctx.Scope == domain.ScopeProject {
		return captureProject(ctx.ProjectDir, ctx.KnownPacks, res)
	}
	return captureGlobal(ctx.Home, ctx.KnownPacks, res)
}

func captureProject(projectDir string, knownPacks map[string]struct{}, res harness.CaptureResult) (harness.CaptureResult, error) {
	paths := ProjectPaths
	harness.CapturePromotedContent(
		filepath.Join(projectDir, paths.SkillsDir),
		knownPacks,
		&res,
	)
	captureNativeAgents(filepath.Join(projectDir, paths.AgentsDir), knownPacks, &res)
	// AGENTS.override.md is fully generated by sync — not captured.
	// Rules are flattened into AGENTS.override.md during Plan and cannot be
	// individually recovered. CaptureResult.Rules will be empty for Codex.
	// This is a one-way transform: rules must be round-tripped through the
	// pack source, not through the harness.

	settingsPath := filepath.Join(projectDir, paths.SettingsFile)
	if b, ok, err := util.ReadFileIfExists(settingsPath); err != nil {
		return res, fmt.Errorf("capture codex project settings: %w", err)
	} else if ok {
		res.Writes = append(res.Writes, domain.WriteAction{
			Dst: filepath.Join("configs", "codex", "config.toml"), Content: b, Src: settingsPath,
		})
		res.Warnings = append(res.Warnings, parseCodexSettings(res.MCPServers, res.AllowedTools, res.AlwaysAllowedTools, b)...)
	}
	res.MaterializeCapturedMCP(settingsPath)
	return res, nil
}

func captureGlobal(home string, knownPacks map[string]struct{}, res harness.CaptureResult) (harness.CaptureResult, error) {
	paths := GlobalPaths
	harness.CapturePromotedContent(
		filepath.Join(home, paths.SkillsDir),
		knownPacks,
		&res,
	)
	captureNativeAgents(filepath.Join(home, paths.AgentsDir), knownPacks, &res)
	// AGENTS.override.md is fully generated by sync — not captured (see captureProject).

	settingsPath := filepath.Join(home, paths.SettingsFile)
	if b, ok, err := util.ReadFileIfExists(settingsPath); err != nil {
		return res, fmt.Errorf("capture codex global settings: %w", err)
	} else if ok {
		res.Writes = append(res.Writes, domain.WriteAction{
			Dst: filepath.Join("configs", "codex", "config.toml"), Content: b, Src: settingsPath,
		})
		res.Warnings = append(res.Warnings, parseCodexSettings(res.MCPServers, res.AllowedTools, res.AlwaysAllowedTools, b)...)
	}
	res.MaterializeCapturedMCP(settingsPath)
	return res, nil
}

// agentTOMLKnownKeys lists the keys that RenderAgentTOML writes to the TOML
// root as pack-neutral fields. Everything else is treated as a harness-specific
// override and captured into Harness["codex"]. Keep in sync with RenderAgentTOML.
var agentTOMLKnownKeys = map[string]bool{
	"name": true, "description": true, "developer_instructions": true,
	"mcp_servers": true, "skills": true,
}

// captureNativeAgents reads agentsDir for direct child .toml files and
// reconstructs domain.Agent objects with the Harness map populated from
// Codex-specific fields. When a TOML omits `name`, the file basename
// (extension stripped) becomes the name.
func captureNativeAgents(agentsDir string, knownPacks map[string]struct{}, res *harness.CaptureResult) {
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return // directory may not exist — not an error
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		src := filepath.Join(agentsDir, e.Name())
		raw, readErr := os.ReadFile(src)
		if readErr != nil {
			res.Warnings = append(res.Warnings, domain.Warning{
				Path:    src,
				Message: fmt.Sprintf("read native agent TOML: %v", readErr),
			})
			continue
		}

		var parsed map[string]any
		if unmarshalErr := toml.Unmarshal(raw, &parsed); unmarshalErr != nil {
			res.Warnings = append(res.Warnings, domain.Warning{
				Path:    src,
				Message: fmt.Sprintf("parse native agent TOML: %v", unmarshalErr),
			})
			continue
		}

		name, _ := parsed["name"].(string)
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".toml")
		}
		if stripped, ok := harness.StripKnownRenderedContentName(name, knownPacks); ok {
			name = stripped
		}
		desc, _ := parsed["description"].(string)
		body, _ := parsed["developer_instructions"].(string)

		// Collect Codex-specific keys into harness.codex map.
		codexCfg := map[string]any{}
		for k, v := range parsed {
			if !agentTOMLKnownKeys[k] {
				codexCfg[k] = v
			}
		}

		// Extract MCP server names from the embedded mcp_servers table.
		var mcpNames []string
		if mcpRaw, ok := parsed["mcp_servers"]; ok {
			if mcpMap, ok := mcpRaw.(map[string]any); ok {
				mcpNames = slices.Sorted(maps.Keys(mcpMap))
			}
		}

		fm := domain.AgentFrontmatter{
			Name:        name,
			Description: desc,
			MCPServers:  mcpNames,
		}
		if len(codexCfg) > 0 {
			fm.Harness = map[string]map[string]any{"codex": codexCfg}
		}

		agent := domain.Agent{
			Name:        name,
			Frontmatter: fm,
			Body:        []byte(body),
			SourcePath:  src,
		}

		res.Writes = append(res.Writes, domain.WriteAction{
			Dst:          filepath.Join("agents", name+".md"),
			Content:      nil, // will be rendered by save pipeline
			Src:          src,
			IsContent:    true,
			SourceDigest: domain.SingleFileDigest(raw),
		})
		res.Agents = append(res.Agents, agent)
	}
}

type codexSettingsCapture struct {
	MCPServers map[string]codexCapturedServer `toml:"mcp_servers"`
}

type codexCapturedServer struct {
	Type          string                             `toml:"type"`
	Command       string                             `toml:"command"`
	Args          []string                           `toml:"args"`
	EnabledTools  []string                           `toml:"enabled_tools"`
	DisabledTools []string                           `toml:"disabled_tools"`
	Env           map[string]string                  `toml:"env"`
	URL           string                             `toml:"url"`
	Headers       map[string]string                  `toml:"headers"`
	Tools         map[string]codexCapturedToolConfig `toml:"tools"`
}

type codexCapturedToolConfig struct {
	ApprovalMode string `toml:"approval_mode"`
}

// parseCodexSettings reads a Codex config.toml byte payload and populates the
// caller-provided maps. alwaysAllowed is optional (may be nil) for callers
// that do not care about per-tool approval round-trip; when supplied, it
// receives tools whose [mcp_servers.<name>.tools.<tool>] approval_mode field
// is codexApprovalModeApprove.
func parseCodexSettings(servers map[string]domain.MCPServer, allowed, alwaysAllowed map[string][]string, b []byte) []domain.Warning {
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
			slices.Sort(srv.DisabledTools)
		}
		servers[name] = srv
		if len(entry.EnabledTools) > 0 {
			allowed[name] = append([]string{}, entry.EnabledTools...)
			slices.Sort(allowed[name])
		}
		if alwaysAllowed != nil && len(entry.Tools) > 0 {
			var approved []string
			for tool, cfg := range entry.Tools {
				if cfg.ApprovalMode == codexApprovalModeApprove {
					approved = append(approved, tool)
				}
			}
			if len(approved) > 0 {
				slices.Sort(approved)
				alwaysAllowed[name] = approved
			}
		}
	}
	return nil
}
