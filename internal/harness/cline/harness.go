package cline

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"
)

// Harness implements the v2 harness.Harness interface for Cline.
type Harness struct{}

func (Harness) ID() domain.Harness { return domain.HarnessCline }

// Layout describes Cline's filesystem footprint for a given scope.
func (Harness) Layout(scope domain.Scope, baseDir, home string) harness.Layout {
	paths := PathsForScope(scope)
	var l harness.Layout
	l.ValidationRoots = []string{
		filepath.Join(baseDir, paths.RulesDir),
		filepath.Join(baseDir, paths.WorkflowsDir),
		filepath.Join(baseDir, paths.SkillsDir),
	}
	if scope == domain.ScopeProject {
		// Project: RulesDir (.clinerules) is the parent of workflows/skills, don't remove it.
		l.RemovePaths = []string{
			filepath.Join(baseDir, paths.WorkflowsDir),
			filepath.Join(baseDir, paths.SkillsDir),
		}
	} else {
		// Global: RulesDir, WorkflowsDir, SkillsDir are all independent directories.
		l.RemovePaths = []string{
			filepath.Join(baseDir, paths.RulesDir),
			filepath.Join(baseDir, paths.WorkflowsDir),
			filepath.Join(baseDir, paths.SkillsDir),
		}
	}
	// Cline MCP settings are always global.
	mcpHome := home
	if scope == domain.ScopeGlobal {
		mcpHome = baseDir
	}
	if p := MCPSettingsPath(mcpHome); p != "" && filepath.Clean(p) != "." {
		l.ValidationRoots = append(l.ValidationRoots, p)
		l.OwnedFiles = append(l.OwnedFiles, mcpOwnedFile(p))
	}
	return l
}

func mcpOwnedFile(path string) harness.OwnedFile {
	strip := func(root map[string]any) { delete(root, "mcpServers") }
	return harness.OwnedFile{
		Path: path, Format: harness.FormatJSON,
		Strip: strip,
		Reset: func(root map[string]any) { root["mcpServers"] = map[string]any{} },
	}
}

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
	paths := ProjectPaths
	pd := ctx.TargetDir
	rulesDir := filepath.Join(pd, paths.RulesDir)
	skillsDir := filepath.Join(pd, paths.SkillsDir)

	f.AddRuleWrites(rulesDir, "", ctx.Profile.AllRules())
	f.AddWorkflowWrites(filepath.Join(pd, paths.WorkflowsDir), "", ctx.Profile.AllWorkflows())
	f.AddSkillCopies(skillsDir, "", ctx.Profile.AllSkills())
	addPromotedAgents(f, skillsDir, ctx.Profile.AllAgents())

	// Cline MCP settings are always global — sync them even in project scope.
	return planGlobalMCP(f, ctx)
}

func planGlobal(f *domain.Fragment, ctx engine.SyncContext) error {
	paths := GlobalPaths
	home := ctx.TargetDir
	skillsDir := filepath.Join(home, paths.SkillsDir)

	f.AddRuleWrites(filepath.Join(home, paths.RulesDir), "", ctx.Profile.AllRules())
	f.AddWorkflowWrites(filepath.Join(home, paths.WorkflowsDir), "", ctx.Profile.AllWorkflows())
	f.AddSkillCopies(skillsDir, "", ctx.Profile.AllSkills())
	addPromotedAgents(f, skillsDir, ctx.Profile.AllAgents())

	return planGlobalMCP(f, ctx)
}

// planGlobalMCP syncs Cline MCP settings to the global location.
// Called from both project and global scope — Cline MCP is always global.
func planGlobalMCP(f *domain.Fragment, ctx engine.SyncContext) error {
	sp := ctx.Profile.SettingsPackName(domain.HarnessCline)
	home := ctx.TargetDir
	if ctx.Scope == domain.ScopeProject {
		home = ctx.Home
		if home == "" {
			return fmt.Errorf("resolving home for Cline MCP settings: HOME is not set")
		}
	}
	dst := MCPSettingsPath(home)
	if dst == "" || len(ctx.Profile.MCPServers) == 0 {
		return nil
	}

	out, _, err := RenderBytes(nil, ctx.Profile.MCPServers)
	if err != nil {
		return fmt.Errorf("render cline MCP settings: %w", err)
	}
	planned := map[string]domain.MCPServer{}
	parseClineSettings(planned, map[string][]string{}, out)
	mcpActions, err := domain.BuildMCPActions(
		dst,
		domain.HarnessCline,
		harness.PlannedMCPServers(ctx.Profile.MCPServers, planned),
		false,
	)
	if err != nil {
		return fmt.Errorf("build cline MCP actions: %w", err)
	}
	f.MCP = append(f.MCP, domain.SettingsAction{
		Dst: dst, Desired: out, Harness: domain.HarnessCline,
		Label: "cline_mcp_settings.json", SourcePack: sp,
	})
	f.MCPServers = append(f.MCPServers, mcpActions...)
	f.Desired = append(f.Desired, filepath.Clean(dst))
	return nil
}

// Render produces a Fragment for pack rendering.
func (Harness) Render(ctx harness.RenderContext) (domain.Fragment, error) {
	if len(ctx.Profile.MCPServers) == 0 {
		return domain.Fragment{}, nil
	}
	out, _, err := RenderBytes(nil, ctx.Profile.MCPServers)
	if err != nil {
		return domain.Fragment{}, err
	}
	dst := filepath.Join(ctx.OutDir, "cline", "cline_mcp_settings.json")
	return domain.Fragment{
		Writes:  []domain.WriteAction{{Dst: dst, Content: out}},
		Desired: []string{dst},
	}, nil
}

// Capture extracts Cline content for round-trip save.
func (Harness) Capture(ctx harness.CaptureContext) (harness.CaptureResult, error) {
	res := harness.NewCaptureResult()

	if ctx.Scope == domain.ScopeProject {
		return captureProject(ctx.ProjectDir, ctx.Home, res)
	}
	return captureGlobal(ctx.Home, res)
}

func captureProject(projectDir string, home string, res harness.CaptureResult) (harness.CaptureResult, error) {
	paths := ProjectPaths
	// Rules and workflows have their own directories; agents are promoted
	// into skills, so we capture rules/workflows individually and scan the
	// skills directory for both plain skills and promoted agents.
	captureRulesAndWorkflows(&res, harness.ContentDirs{
		Rules:     filepath.Join(projectDir, paths.RulesDir),
		Workflows: filepath.Join(projectDir, paths.WorkflowsDir),
	})
	harness.CapturePromotedContent(filepath.Join(projectDir, paths.SkillsDir), &res)

	// Cline MCP settings are always global — capture them in project scope too
	// to maintain round-trip symmetry with Plan.
	if home == "" {
		res.Warnings = append(res.Warnings, domain.Warning{
			Field: "home", Message: "HOME not set, skipping MCP settings capture",
		})
		return res, nil
	}
	settingsPath := MCPSettingsPath(home)
	if b, ok, err := util.ReadFileIfExists(settingsPath); err != nil {
		return res, fmt.Errorf("capture cline settings: %w", err)
	} else if ok {
		res.Writes = append(res.Writes, domain.WriteAction{
			Dst: filepath.Join("configs", "cline", "cline_mcp_settings.json"), Content: b, Src: settingsPath,
		})
		res.Warnings = append(res.Warnings, parseClineSettings(res.MCPServers, res.AllowedTools, b)...)
	}
	res.MaterializeCapturedMCP(settingsPath)

	return res, nil
}

func captureGlobal(home string, res harness.CaptureResult) (harness.CaptureResult, error) {
	if home == "" {
		return res, fmt.Errorf("HOME not set (required for global-scope capture)")
	}
	paths := GlobalPaths
	captureRulesAndWorkflows(&res, harness.ContentDirs{
		Rules:     filepath.Join(home, paths.RulesDir),
		Workflows: filepath.Join(home, paths.WorkflowsDir),
	})
	harness.CapturePromotedContent(filepath.Join(home, paths.SkillsDir), &res)

	settingsPath := MCPSettingsPath(home)
	if b, ok, err := util.ReadFileIfExists(settingsPath); err != nil {
		return res, fmt.Errorf("capture cline settings: %w", err)
	} else if ok {
		res.Writes = append(res.Writes, domain.WriteAction{
			Dst: filepath.Join("configs", "cline", "cline_mcp_settings.json"), Content: b, Src: settingsPath,
		})
		res.Warnings = append(res.Warnings, parseClineSettings(res.MCPServers, res.AllowedTools, b)...)
	}
	res.MaterializeCapturedMCP(settingsPath)

	return res, nil
}

// captureRulesAndWorkflows captures rules and workflows from their separate
// directories. Agents are not captured here — they live as promoted skills
// and are handled by capturePromotedContent.
func captureRulesAndWorkflows(res *harness.CaptureResult, dirs harness.ContentDirs) {
	copies, warnings := harness.CaptureContentDir(dirs.Rules, "rules", ".md",
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

	copies, warnings = harness.CaptureContentDir(dirs.Workflows, "workflows", ".md",
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
}

type clineSettingsCapture struct {
	MCPServers map[string]clineCapturedServer `json:"mcpServers"`
}

type clineCapturedServer struct {
	Disabled    bool              `json:"disabled"`
	Timeout     int               `json:"timeout"`
	Type        string            `json:"type"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
	Env         map[string]string `json:"env"`
	AlwaysAllow []string          `json:"alwaysAllow"`
}

func parseClineSettings(servers map[string]domain.MCPServer, allowed map[string][]string, b []byte) []domain.Warning {
	var cfg clineSettingsCapture
	if err := json.Unmarshal(b, &cfg); err != nil {
		return []domain.Warning{{Message: fmt.Sprintf("failed to parse Cline MCP settings: %v", err)}}
	}
	for name, entry := range cfg.MCPServers {
		if entry.Disabled {
			continue
		}
		srv := domain.MCPServer{Name: name, Timeout: entry.Timeout}
		transport := entry.Type
		if transport == "" {
			transport = domain.TransportStdio
		}
		srv.Transport = transport
		switch transport {
		case domain.TransportStdio:
			if entry.Command == "" {
				continue
			}
			srv.Command = append([]string{entry.Command}, entry.Args...)
			srv.Env = entry.Env
			if srv.Env == nil {
				srv.Env = map[string]string{}
			}
		case domain.TransportSSE, domain.TransportStreamableHTTP:
			if entry.URL == "" {
				continue
			}
			srv.URL = entry.URL
			srv.Headers = entry.Headers
		default:
			continue
		}
		servers[name] = srv
		if len(entry.AlwaysAllow) > 0 {
			allowed[name] = append([]string{}, entry.AlwaysAllow...)
		}
	}
	return nil
}
