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
	return []string{"opencode/opencode.json", "opencode/oh-my-opencode.json"}
}

func (Harness) SettingsPaths(scope domain.Scope, baseDir, _ string) []string {
	if scope == domain.ScopeProject {
		a, b := SettingsProjectPaths(baseDir)
		return []string{a, b}
	}
	a, b := SettingsGlobalPaths(baseDir)
	return []string{a, b}
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

	switch ctx.Scope {
	case domain.ScopeProject:
		planContent(&f, ctx.TargetDir, ctx.Profile)
		if err := planSettings(&f, ctx); err != nil {
			return domain.Fragment{}, err
		}
	case domain.ScopeGlobal:
		planContentGlobal(&f, ctx.TargetDir, ctx.Profile)
		if err := planSettings(&f, ctx); err != nil {
			return domain.Fragment{}, err
		}
	}

	return f, nil
}

func planContent(f *domain.Fragment, projectDir string, p domain.Profile) {
	f.AddRuleWrites(projectDir, filepath.Join(".opencode", "rules"), p.AllRules())
	// OpenCode expects agent frontmatter `tools` to be a YAML record/map,
	// but packs use the harness-neutral schema where `tools` is a YAML list.
	// Transform agents on write so generated `.opencode/agents/*.md` validate.
	var outAgents []domain.Agent
	for _, a := range p.AllAgents() {
		transformed, err := TransformAgent(a)
		if err != nil {
			outAgents = append(outAgents, a)
			continue
		}
		a.Raw = transformed
		outAgents = append(outAgents, a)
	}
	f.AddAgentWrites(projectDir, filepath.Join(".opencode", "agents"), outAgents)
	f.AddWorkflowWrites(projectDir, filepath.Join(".opencode", "commands"), p.AllWorkflows())
	f.AddSkillCopies(projectDir, filepath.Join(".opencode", "skills"), p.AllSkills())
}

func planContentGlobal(f *domain.Fragment, home string, p domain.Profile) {
	base := filepath.Join(home, ".config", "opencode")
	f.AddRuleWrites(base, "rules", p.AllRules())
	// Apply same OpenCode agent schema transform in global scope.
	var outAgents []domain.Agent
	for _, a := range p.AllAgents() {
		transformed, err := TransformAgent(a)
		if err != nil {
			outAgents = append(outAgents, a)
			continue
		}
		a.Raw = transformed
		outAgents = append(outAgents, a)
	}
	f.AddAgentWrites(base, "agents", outAgents)
	f.AddWorkflowWrites(base, "commands", p.AllWorkflows())
	f.AddSkillCopies(base, "skills", p.AllSkills())
}

func planSettings(f *domain.Fragment, ctx engine.SyncContext) error {
	sp := ctx.Profile.SettingsPackName(domain.HarnessOpenCode)

	var configPath, omoPath string
	if ctx.Scope == domain.ScopeProject {
		configPath, omoPath = SettingsProjectPaths(ctx.TargetDir)
	} else {
		configPath, omoPath = SettingsGlobalPaths(ctx.TargetDir)
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
	if decision.EmitSettings {
		base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessOpenCode, "opencode.json")
		out, _, err := RenderBytes(base, ctx.Profile.MCPServers, instr, skills, true)
		if err != nil {
			return fmt.Errorf("render opencode settings: %w", err)
		}
		f.Settings = append(f.Settings, domain.SettingsAction{
			Dst: configPath, Desired: out, Harness: domain.HarnessOpenCode,
			Label: "opencode.json", SourcePack: sp,
		})
		f.Desired = append(f.Desired, filepath.Clean(configPath))
	} else if decision.EmitMCPPlugin {
		managed, _, err := RenderManagedKeysOnly(ctx.Profile.MCPServers, instr, skills, true)
		if err != nil {
			return fmt.Errorf("render opencode managed keys: %w", err)
		}
		f.Plugins = append(f.Plugins, domain.SettingsAction{
			Dst: configPath, Desired: managed, Harness: domain.HarnessOpenCode,
			Label: "opencode.json (managed keys)", SourcePack: sp, MergeMode: decision.MergeMode,
		})
		f.Desired = append(f.Desired, filepath.Clean(configPath))
	}

	if omoBytes := ctx.Profile.Plugins.FileBytes(domain.HarnessOpenCode, "oh-my-opencode.json"); len(omoBytes) > 0 {
		f.Plugins = append(f.Plugins, domain.SettingsAction{
			Dst: omoPath, Desired: omoBytes, Harness: domain.HarnessOpenCode,
			Label: "oh-my-opencode.json", SourcePack: sp,
		})
		f.Desired = append(f.Desired, filepath.Clean(omoPath))
	}
	return nil
}

// Render produces a Fragment for pack rendering.
func (Harness) Render(ctx harness.RenderContext) (domain.Fragment, error) {
	base := ctx.Profile.BaseSettings.FileBytes(domain.HarnessOpenCode, "opencode.json")
	instr := InstructionsSpec{Manage: false}
	skills := SkillsSpec{Manage: false}
	out, _, err := RenderBytes(base, ctx.Profile.MCPServers, instr, skills, false)
	if err != nil {
		return domain.Fragment{}, err
	}
	p1 := filepath.Join(ctx.OutDir, "opencode", "opencode.json")
	f := domain.Fragment{
		Writes:  []domain.WriteAction{{Dst: p1, Content: out}},
		Desired: []string{p1},
	}
	if omoBytes := ctx.Profile.Plugins.FileBytes(domain.HarnessOpenCode, "oh-my-opencode.json"); len(omoBytes) > 0 {
		p2 := filepath.Join(ctx.OutDir, "opencode", "oh-my-opencode.json")
		f.Writes = append(f.Writes, domain.WriteAction{Dst: p2, Content: omoBytes})
		f.Desired = append(f.Desired, p2)
	}
	return f, nil
}

// StripManagedSettings removes sync-managed keys from rendered settings.
func (Harness) StripManagedSettings(rendered []byte, filename string) ([]byte, error) {
	return StripManagedKeys(rendered, filename)
}

// Capture extracts OpenCode content for round-trip save.
func (Harness) Capture(ctx harness.CaptureContext) (harness.CaptureResult, error) {
	res := harness.NewCaptureResult()

	var baseDir string
	var settingsFn func(string) (string, string)
	if ctx.Scope == domain.ScopeProject {
		baseDir = ctx.ProjectDir
		settingsFn = SettingsProjectPaths
	} else {
		baseDir = ctx.Home
		settingsFn = SettingsGlobalPaths
	}

	var agentsDir, commandsDir, skillsDir, rulesDir, rulesFile string
	if ctx.Scope == domain.ScopeProject {
		agentsDir = filepath.Join(baseDir, ".opencode", "agents")
		commandsDir = filepath.Join(baseDir, ".opencode", "commands")
		skillsDir = filepath.Join(baseDir, ".opencode", "skills")
		rulesDir = filepath.Join(baseDir, ".opencode", "rules")
		rulesFile = filepath.Join(baseDir, "AGENTS.md")
	} else {
		ocBase := filepath.Join(baseDir, ".config", "opencode")
		agentsDir = filepath.Join(ocBase, "agents")
		commandsDir = filepath.Join(ocBase, "commands")
		skillsDir = filepath.Join(ocBase, "skills")
		rulesDir = filepath.Join(ocBase, "rules")
		rulesFile = filepath.Join(ocBase, "AGENTS.md")
	}

	copies, warnings := harness.CaptureContentDir(agentsDir, "agents", ".md",
		func(raw []byte, name, src string) error {
			a, err := ReverseTransformAgent(raw, name+".md")
			if err != nil {
				return err
			}
			a.SourcePath = src
			res.Agents = append(res.Agents, a)
			return nil
		})
	res.Copies = append(res.Copies, copies...)
	res.Warnings = append(res.Warnings, warnings...)

	copies, warnings = harness.CaptureContentDir(commandsDir, "workflows", ".md",
		func(raw []byte, name, src string) error {
			w, err := engine.ParseWorkflowBytes(raw, name, "")
			if err != nil {
				return err
			}
			w.SourcePath = src
			res.Workflows = append(res.Workflows, w)
			return nil
		})
	res.Copies = append(res.Copies, copies...)
	res.Warnings = append(res.Warnings, warnings...)

	skillCopies, skills := harness.CaptureSkills(skillsDir, "skills")
	res.Copies = append(res.Copies, skillCopies...)
	res.Skills = append(res.Skills, skills...)

	copies, warnings = harness.CaptureContentDir(rulesDir, "rules", ".md",
		func(raw []byte, name, src string) error {
			r, err := engine.ParseRuleBytes(raw, name, "")
			if err != nil {
				return err
			}
			r.SourcePath = src
			res.Rules = append(res.Rules, r)
			return nil
		})
	res.Copies = append(res.Copies, copies...)
	res.Warnings = append(res.Warnings, warnings...)

	if util.ExistsFile(rulesFile) && !util.ExistsFile(filepath.Join(rulesDir, "AGENTS.md")) {
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

	configPath, omoPath := settingsFn(baseDir)
	if b, ok, err := util.ReadFileIfExists(configPath); err != nil {
		return res, fmt.Errorf("capture opencode config: %w", err)
	} else if ok {
		res.Writes = append(res.Writes, domain.WriteAction{
			Dst: filepath.Join("configs", "opencode", "opencode.json"), Content: b, Src: configPath,
		})
		res.Warnings = append(res.Warnings, parseOpenCodeSettings(res.MCPServers, res.AllowedTools, b)...)
	}
	if b, ok, err := util.ReadFileIfExists(omoPath); err != nil {
		return res, fmt.Errorf("capture opencode oh-my-opencode: %w", err)
	} else if ok {
		res.Writes = append(res.Writes, domain.WriteAction{
			Dst: filepath.Join("configs", "opencode", "oh-my-opencode.json"), Content: b, Src: omoPath,
		})
	}

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
			servers[name] = srv
		}
	}
	for tool, enabled := range cfg.Tools {
		if !enabled {
			continue
		}
		if strings.HasSuffix(tool, "_*") {
			continue
		}
		parts := strings.SplitN(tool, "_", 2)
		if len(parts) != 2 {
			continue
		}
		prefix := engine.NormalizeServerName(parts[0])
		if prefix == "" {
			continue
		}
		allowed[prefix] = append(allowed[prefix], parts[1])
	}
	for k := range allowed {
		sort.Strings(allowed[k])
	}
	return nil
}
