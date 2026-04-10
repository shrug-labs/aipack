package opencode

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"
)

// Harness implements the v2 harness.Harness interface for OpenCode.
type Harness struct{}

func (Harness) ID() domain.Harness { return domain.HarnessOpenCode }

// Layout describes OpenCode's filesystem footprint for a given scope.
func (Harness) Layout(scope domain.Scope, baseDir, _ string) harness.Layout {
	paths := PathsForScope(scope)
	configPath := filepath.Join(baseDir, paths.SettingsFile)
	return harness.Layout{
		ValidationRoots: []string{
			filepath.Join(baseDir, paths.ConfigBase),
		},
		RemovePaths: []string{
			filepath.Join(baseDir, paths.AgentsDir),
			filepath.Join(baseDir, paths.WorkflowsDir),
			filepath.Join(baseDir, paths.RulesDir),
			filepath.Join(baseDir, paths.SkillsDir),
		},
		OwnedFiles: []harness.OwnedFile{{
			Path: configPath, Format: harness.FormatJSON,
			Strip: func(root map[string]any) {
				delete(root, "mcp")
				delete(root, "tools")
				delete(root, "instructions")
				delete(root, "skills")
			},
			Reset: func(root map[string]any) {
				root["mcp"] = map[string]any{}
				root["tools"] = map[string]any{}
				delete(root, "instructions")
				delete(root, "skills")
			},
		}},
	}
}

// Plan produces a Fragment from typed content.
func (Harness) Plan(_ context.Context, ctx engine.SyncContext) (domain.Fragment, error) {
	var f domain.Fragment

	if err := harness.PlanStandardContent(&f, ctx.Profile, contentDirsForScope(ctx.Scope, ctx.TargetDir), opencodeAgentTransform); err != nil {
		return domain.Fragment{}, err
	}
	if err := planSettings(&f, ctx); err != nil {
		return domain.Fragment{}, err
	}

	return f, nil
}

// opencodeAgentTransform transforms an agent for OpenCode's native schema.
func opencodeAgentTransform(a domain.Agent) (domain.Agent, error) {
	transformed, err := TransformAgent(a)
	if err != nil {
		return a, fmt.Errorf("opencode agent transform failed for %s: %w", a.Name, err)
	}
	a.Raw = transformed
	return a, nil
}

func contentDirsForScope(scope domain.Scope, targetDir string) harness.ContentDirs {
	paths := PathsForScope(scope)
	return harness.ContentDirs{
		Rules:     filepath.Join(targetDir, paths.RulesDir),
		Agents:    filepath.Join(targetDir, paths.AgentsDir),
		Workflows: filepath.Join(targetDir, paths.WorkflowsDir),
		Skills:    filepath.Join(targetDir, paths.SkillsDir),
	}
}

func planSettings(f *domain.Fragment, ctx engine.SyncContext) error {
	sp := ctx.Profile.SettingsPackName(domain.HarnessOpenCode)

	paths := PathsForScope(ctx.Scope)
	configPath := filepath.Join(ctx.TargetDir, paths.SettingsFile)
	configBase := filepath.Join(ctx.TargetDir, paths.ConfigBase)

	// Point instructions/skills at the managed rendered directories, not at
	// pack source, so profile enable/disable takes effect at runtime.
	var renderedRulesDir, renderedSkillsDir string
	for _, pk := range ctx.Profile.Packs {
		if renderedRulesDir == "" && len(pk.Rules) > 0 {
			renderedRulesDir = filepath.Join(ctx.TargetDir, paths.RulesDir)
		}
		if renderedSkillsDir == "" && len(pk.Skills) > 0 {
			renderedSkillsDir = filepath.Join(ctx.TargetDir, paths.SkillsDir)
		}
		if renderedRulesDir != "" && renderedSkillsDir != "" {
			break
		}
	}
	instr := BuildInstructionsSpec(renderedRulesDir)
	skills := BuildSkillsSpec(renderedSkillsDir)

	hasMCP := len(ctx.Profile.MCPServers) > 0
	hasManagedContent := hasMCP || instr.Manage || skills.Manage
	decision := engine.ClassifySettings(hasMCP, hasManagedContent, ctx.SkipSettings)
	var mcpRendered []byte
	if decision.EmitSettings {
		base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessOpenCode, BaseSettingsFile)
		out, _, err := RenderBytes(base, ctx.Profile.MCPServers, instr, skills)
		if err != nil {
			return fmt.Errorf("render opencode settings: %w", err)
		}
		mcpRendered = out
		f.Settings = append(f.Settings, domain.SettingsAction{
			Dst: configPath, Desired: out, Harness: domain.HarnessOpenCode,
			Label: BaseSettingsFile, SourcePack: sp, MergeMode: true,
		})
		f.Desired = append(f.Desired, filepath.Clean(configPath))
	} else if decision.EmitMCP {
		managed, _, err := RenderManagedKeysOnly(ctx.Profile.MCPServers, instr, skills)
		if err != nil {
			return fmt.Errorf("render opencode managed keys: %w", err)
		}
		mcpRendered = managed
		f.MCP = append(f.MCP, domain.SettingsAction{
			Dst: configPath, Desired: managed, Harness: domain.HarnessOpenCode,
			Label: BaseSettingsFile + " (managed keys)", SourcePack: sp, MergeMode: decision.MergeMode,
		})
		f.Desired = append(f.Desired, filepath.Clean(configPath))
	}
	if hasMCP && len(mcpRendered) > 0 {
		planned := map[string]domain.MCPServer{}
		parseOpenCodeSettings(planned, map[string][]string{}, mcpRendered)
		mcpActions, err := domain.BuildMCPActions(
			configPath,
			domain.HarnessOpenCode,
			harness.PlannedMCPServers(ctx.Profile.MCPServers, planned),
			true,
		)
		if err != nil {
			return fmt.Errorf("build opencode MCP actions: %w", err)
		}
		f.MCPServers = append(f.MCPServers, mcpActions...)
	}

	// Deploy any drop-in config files from the bundle as-is.
	for _, df := range ctx.Profile.BaseSettings.DropInFiles(domain.HarnessOpenCode, BaseSettingsFile) {
		dst := filepath.Join(configBase, df.Filename)
		f.Settings = append(f.Settings, domain.SettingsAction{
			Dst: dst, Desired: df.Content, Harness: domain.HarnessOpenCode,
			Label: df.Filename, SourcePack: sp,
		})
		f.Desired = append(f.Desired, filepath.Clean(dst))
	}
	return nil
}

// Render produces a Fragment for pack rendering.
func (Harness) Render(_ context.Context, ctx harness.RenderContext) (domain.Fragment, error) {
	base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessOpenCode, BaseSettingsFile)
	instr := InstructionsSpec{Manage: false}
	skills := SkillsSpec{Manage: false}
	out, _, err := RenderBytes(base, ctx.Profile.MCPServers, instr, skills)
	if err != nil {
		return domain.Fragment{}, err
	}
	p1 := filepath.Join(ctx.OutDir, "opencode", BaseSettingsFile)
	f := domain.Fragment{
		Writes:  []domain.WriteAction{{Dst: p1, Content: out}},
		Desired: []string{p1},
	}
	for _, df := range ctx.Profile.BaseSettings.DropInFiles(domain.HarnessOpenCode, BaseSettingsFile) {
		p := filepath.Join(ctx.OutDir, "opencode", df.Filename)
		f.Writes = append(f.Writes, domain.WriteAction{Dst: p, Content: df.Content})
		f.Desired = append(f.Desired, p)
	}
	return f, nil
}

// Capture extracts OpenCode content for round-trip save.
// Only the base settings file (opencode.json) is captured. Drop-in config
// files are pack-provided and should not be round-tripped from the harness.
func (Harness) Capture(_ context.Context, ctx harness.CaptureContext) (harness.CaptureResult, error) {
	res := harness.NewCaptureResult()

	paths := PathsForScope(ctx.Scope)
	var baseDir string
	if ctx.Scope == domain.ScopeProject {
		baseDir = ctx.ProjectDir
	} else {
		baseDir = ctx.Home
	}
	configPath := filepath.Join(baseDir, paths.SettingsFile)

	dirs := contentDirsForScope(ctx.Scope, baseDir)
	harness.CaptureContent(&res, dirs, func(raw []byte, name, src string) (domain.Agent, error) {
		return ReverseTransformAgent(raw, name+".md")
	})

	rulesFile := filepath.Join(baseDir, paths.AGENTSFile)

	if util.ExistsFile(rulesFile) && !util.ExistsFile(filepath.Join(dirs.Rules, "AGENTS.md")) {
		res.Copies = append(res.Copies, domain.CopyAction{
			Src: rulesFile, Dst: filepath.Join("rules", "AGENTS.md"), Kind: domain.CopyKindFile,
		})
		if raw, readErr := os.ReadFile(rulesFile); readErr != nil {
			res.Warnings = append(res.Warnings, domain.Warning{Path: rulesFile, Message: fmt.Sprintf("reading file: %v", readErr)})
		} else {
			if r, parseErr := engine.ParseRuleBytes(raw, "AGENTS", ""); parseErr != nil {
				res.Warnings = append(res.Warnings, domain.Warning{Path: rulesFile, Message: fmt.Sprintf("parse error: %v", parseErr)})
			} else {
				r.SourcePath = rulesFile
				res.Rules = append(res.Rules, r)
			}
		}
	}

	if b, ok, err := util.ReadFileIfExists(configPath); err != nil {
		return res, fmt.Errorf("capture opencode config: %w", err)
	} else if ok {
		res.Writes = append(res.Writes, domain.WriteAction{
			Dst: filepath.Join("configs", "opencode", BaseSettingsFile), Content: b, Src: configPath,
		})
		res.Warnings = append(res.Warnings, parseOpenCodeSettings(res.MCPServers, res.AllowedTools, b)...)
	}
	res.MaterializeCapturedMCP(configPath)

	return res, nil
}

type opencodeSettings struct {
	MCP   map[string]opencodeSettingsMCP `json:"mcp"`
	Tools map[string]bool                `json:"tools"`
}

type opencodeSettingsMCP struct {
	Enabled     bool              `json:"enabled"`
	Type        string            `json:"type"`
	Command     []string          `json:"command"`
	URL         string            `json:"url"`
	Environment map[string]string `json:"environment"`
	Timeout     int               `json:"timeout"`
}

func parseOpenCodeSettings(servers map[string]domain.MCPServer, allowed map[string][]string, b []byte) []domain.Warning {
	var cfg opencodeSettings
	if err := json.Unmarshal(b, &cfg); err != nil {
		return []domain.Warning{{Message: fmt.Sprintf("failed to parse OpenCode settings: %v", err)}}
	}
	if cfg.MCP != nil {
		for name, entry := range cfg.MCP {
			srv := domain.MCPServer{Name: name}
			switch entry.Type {
			case "", "local":
				srv.Transport = domain.TransportStdio
				srv.Command = entry.Command
				srv.Env = entry.Environment
			case domain.TransportSSE, domain.TransportStreamableHTTP:
				srv.Transport = entry.Type
				srv.URL = entry.URL
			default:
				continue
			}
			if entry.Timeout > 0 {
				srv.Timeout = entry.Timeout / 1000 // opencode.json stores ms, domain stores seconds
			}
			servers[name] = srv
		}
	}
	// Build a set of known server prefixes (normalized) so we can match
	// server names containing underscores (e.g. "foo_bar") against tool keys
	// like "foo_bar_baz" without splitting at the wrong underscore.
	normalizedNames := make([]string, 0, len(cfg.MCP))
	normalizedToOriginal := make(map[string]string, len(cfg.MCP))
	for name := range cfg.MCP {
		norm := engine.NormalizeServerName(name)
		normalizedNames = append(normalizedNames, norm)
		normalizedToOriginal[norm] = name
	}
	slices.SortFunc(normalizedNames, func(a, b string) int { return cmp.Compare(b, a) }) // longest first

	for tool, enabled := range cfg.Tools {
		if strings.HasSuffix(tool, "_*") {
			continue
		}
		prefix, toolName := matchServerPrefix(tool, normalizedNames)
		if prefix == "" || toolName == "" {
			continue
		}
		if enabled {
			allowed[prefix] = append(allowed[prefix], toolName)
		} else if origName, ok := normalizedToOriginal[prefix]; ok {
			if srv, ok := servers[origName]; ok {
				srv.DisabledTools = append(srv.DisabledTools, toolName)
				servers[origName] = srv
			}
		}
	}
	for k := range allowed {
		slices.Sort(allowed[k])
	}
	for name, srv := range servers {
		if len(srv.DisabledTools) > 0 {
			slices.Sort(srv.DisabledTools)
			servers[name] = srv
		}
	}
	return nil
}

// matchServerPrefix finds the longest normalized server name that is a prefix
// of the tool key, returning the prefix and the remaining tool name.
// names must be sorted longest-first.
func matchServerPrefix(tool string, names []string) (prefix, toolName string) {
	for _, n := range names {
		if rest, ok := strings.CutPrefix(tool, n+"_"); ok {
			return n, rest
		}
	}
	return "", ""
}
