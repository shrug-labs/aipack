package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"
)

// Harness implements the v2 harness.Harness interface for OpenCode.
type Harness struct{}

func (Harness) ID() domain.Harness { return domain.HarnessOpenCode }

func (Harness) PackRelativePaths() []string {
	return []string{"opencode/" + baseSettingsFile}
}

func (Harness) SettingsPaths(scope domain.Scope, baseDir, _ string) []string {
	if scope == domain.ScopeProject {
		return []string{SettingsProjectPath(baseDir)}
	}
	return []string{SettingsGlobalPath(baseDir)}
}

func (Harness) ManagedRoots(scope domain.Scope, baseDir, _ string) []string {
	if scope == domain.ScopeProject {
		return ManagedRootsProject(baseDir)
	}
	return ManagedRootsGlobal(baseDir)
}

func (Harness) StrictExtraDirs(scope domain.Scope, baseDir, _ string) []string {
	if scope == domain.ScopeProject {
		return StrictExtraDirsProject(baseDir)
	}
	return nil
}

// Plan produces a Fragment from typed content.
func (Harness) Plan(ctx engine.SyncContext) (domain.Fragment, error) {
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
	var base string
	if scope == domain.ScopeProject {
		base = filepath.Join(targetDir, ".opencode")
	} else {
		base = filepath.Join(targetDir, ".config", "opencode")
	}
	return harness.ContentDirs{
		Rules:     filepath.Join(base, "rules"),
		Agents:    filepath.Join(base, "agents"),
		Workflows: filepath.Join(base, "commands"),
		Skills:    filepath.Join(base, "skills"),
	}
}

func planSettings(f *domain.Fragment, ctx engine.SyncContext) error {
	sp := ctx.Profile.SettingsPackName(domain.HarnessOpenCode)

	var configPath string
	var configBase string
	if ctx.Scope == domain.ScopeProject {
		configPath = SettingsProjectPath(ctx.TargetDir)
		configBase = configBaseProject(ctx.TargetDir)
	} else {
		configPath = SettingsGlobalPath(ctx.TargetDir)
		configBase = configBaseGlobal(ctx.TargetDir)
	}

	var ruleFilePaths []string
	for _, r := range ctx.Profile.AllRules() {
		if r.SourcePath != "" {
			ruleFilePaths = append(ruleFilePaths, r.SourcePath)
		}
	}

	ruleDirs := ctx.Profile.RuleDirs()
	skillRoots := ctx.Profile.SkillRoots()
	manageRules := len(ruleDirs) > 0
	manageSkills := len(skillRoots) > 0
	instr := BuildInstructionsSpec(ruleDirs, ruleFilePaths, manageRules)
	skills := BuildSkillsSpec(skillRoots, skillRoots, manageSkills)

	hasMCP := len(ctx.Profile.MCPServers) > 0
	hasManagedContent := hasMCP || instr.Manage || skills.Manage
	decision := engine.ClassifySettings(hasMCP, hasManagedContent, ctx.SkipSettings)
	var mcpRendered []byte
	if decision.EmitSettings {
		base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessOpenCode, baseSettingsFile)
		out, _, err := RenderBytes(base, ctx.Profile.MCPServers, instr, skills)
		if err != nil {
			return fmt.Errorf("render opencode settings: %w", err)
		}
		mcpRendered = out
		f.Settings = append(f.Settings, domain.SettingsAction{
			Dst: configPath, Desired: out, Harness: domain.HarnessOpenCode,
			Label: baseSettingsFile, SourcePack: sp, MergeMode: true,
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
			Label: baseSettingsFile + " (managed keys)", SourcePack: sp, MergeMode: decision.MergeMode,
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
	for _, df := range ctx.Profile.BaseSettings.DropInFiles(domain.HarnessOpenCode, baseSettingsFile) {
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
func (Harness) Render(ctx harness.RenderContext) (domain.Fragment, error) {
	base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessOpenCode, baseSettingsFile)
	instr := InstructionsSpec{Manage: false}
	skills := SkillsSpec{Manage: false}
	out, _, err := RenderBytes(base, ctx.Profile.MCPServers, instr, skills)
	if err != nil {
		return domain.Fragment{}, err
	}
	p1 := filepath.Join(ctx.OutDir, "opencode", baseSettingsFile)
	f := domain.Fragment{
		Writes:  []domain.WriteAction{{Dst: p1, Content: out}},
		Desired: []string{p1},
	}
	for _, df := range ctx.Profile.BaseSettings.DropInFiles(domain.HarnessOpenCode, baseSettingsFile) {
		p := filepath.Join(ctx.OutDir, "opencode", df.Filename)
		f.Writes = append(f.Writes, domain.WriteAction{Dst: p, Content: df.Content})
		f.Desired = append(f.Desired, p)
	}
	return f, nil
}

// StripManagedSettings removes sync-managed keys from rendered settings.
func (Harness) StripManagedSettings(rendered []byte, filename string) ([]byte, error) {
	return StripManagedKeys(rendered, filename)
}

// CleanActions returns operations to reset OpenCode managed state.
func (Harness) CleanActions(scope domain.Scope, baseDir, _ string) []harness.CleanAction {
	var base string
	var configPath string
	if scope == domain.ScopeProject {
		base = configBaseProject(baseDir)
		configPath = SettingsProjectPath(baseDir)
	} else {
		base = configBaseGlobal(baseDir)
		configPath = SettingsGlobalPath(baseDir)
	}
	return []harness.CleanAction{
		{Path: filepath.Join(base, "agents")},
		{Path: filepath.Join(base, "commands")},
		{Path: filepath.Join(base, "skills")},
		{Path: filepath.Join(base, "rules")},
		{
			Path:   configPath,
			Format: harness.CleanJSON,
			Edit: func(root map[string]any) {
				root["mcp"] = map[string]any{}
				root["tools"] = map[string]any{}
				delete(root, "instructions")
				delete(root, "skills")
			},
		},
	}
}

// Capture extracts OpenCode content for round-trip save.
// Only the base settings file (opencode.json) is captured. Drop-in config
// files are pack-provided and should not be round-tripped from the harness.
func (Harness) Capture(ctx harness.CaptureContext) (harness.CaptureResult, error) {
	res := harness.NewCaptureResult()

	var baseDir string
	var configPath string
	if ctx.Scope == domain.ScopeProject {
		baseDir = ctx.ProjectDir
		configPath = SettingsProjectPath(baseDir)
	} else {
		baseDir = ctx.Home
		configPath = SettingsGlobalPath(baseDir)
	}

	dirs := contentDirsForScope(ctx.Scope, baseDir)
	harness.CaptureContent(&res, dirs, func(raw []byte, name, src string) (domain.Agent, error) {
		return ReverseTransformAgent(raw, name+".md")
	})

	var rulesFile string
	if ctx.Scope == domain.ScopeProject {
		rulesFile = filepath.Join(baseDir, "AGENTS.md")
	} else {
		rulesFile = filepath.Join(baseDir, ".config", "opencode", "AGENTS.md")
	}

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
			Dst: filepath.Join("configs", "opencode", baseSettingsFile), Content: b, Src: configPath,
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
	for name := range cfg.MCP {
		normalizedNames = append(normalizedNames, engine.NormalizeServerName(name))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(normalizedNames))) // longest first

	for tool, enabled := range cfg.Tools {
		if !enabled {
			continue
		}
		if strings.HasSuffix(tool, "_*") {
			continue
		}
		prefix, toolName := matchServerPrefix(tool, normalizedNames)
		if prefix == "" || toolName == "" {
			continue
		}
		allowed[prefix] = append(allowed[prefix], toolName)
	}
	for k := range allowed {
		sort.Strings(allowed[k])
	}
	return nil
}

// matchServerPrefix finds the longest normalized server name that is a prefix
// of the tool key, returning the prefix and the remaining tool name.
// names must be sorted longest-first.
func matchServerPrefix(tool string, names []string) (prefix, toolName string) {
	for _, n := range names {
		if strings.HasPrefix(tool, n+"_") {
			return n, tool[len(n)+1:]
		}
	}
	return "", ""
}
