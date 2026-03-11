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

func (Harness) PackRelativePaths() []string { return []string{"cline/cline_mcp_settings.json"} }

func (Harness) SettingsPaths(scope domain.Scope, baseDir, home string) []string {
	// Cline MCP settings are always global regardless of scope.
	h := baseDir
	if scope == domain.ScopeProject {
		h = home
	}
	if p := SettingsGlobalPath(h); p != "" {
		return []string{p}
	}
	return nil
}

func (Harness) ManagedRoots(scope domain.Scope, baseDir, home string) []string {
	if scope == domain.ScopeProject {
		roots := ManagedRootsProject(baseDir)
		// Cline MCP settings are always global — include in managed roots.
		if p := SettingsGlobalPath(home); p != "" {
			roots = append(roots, p)
		}
		return roots
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
	projectDir := ctx.TargetDir
	f.AddRuleWrites(projectDir, ".clinerules", ctx.Profile.AllRules())
	f.AddAgentWrites(projectDir, filepath.Join(".clinerules", "agents"), ctx.Profile.AllAgents())
	f.AddWorkflowWrites(projectDir, filepath.Join(".clinerules", "workflows"), ctx.Profile.AllWorkflows())
	f.AddSkillCopies(projectDir, filepath.Join(".clinerules", "skills"), ctx.Profile.AllSkills())

	// Cline MCP settings are always global — sync them even in project scope.
	return planGlobalMCP(f, ctx)
}

func planGlobal(f *domain.Fragment, ctx engine.SyncContext) error {
	home := ctx.TargetDir

	rulesDir := RulesGlobalDir(home)
	f.AddRuleWrites(rulesDir, "", ctx.Profile.AllRules())
	f.AddAgentWrites(AgentsGlobalDir(home), "", ctx.Profile.AllAgents())
	wfDir := WorkflowsGlobalDir(home)
	f.AddWorkflowWrites(wfDir, "", ctx.Profile.AllWorkflows())
	f.AddSkillCopies(home, filepath.Join(".cline", "skills"), ctx.Profile.AllSkills())

	return planGlobalMCP(f, ctx)
}

// planGlobalMCP syncs Cline MCP settings to the global location.
// Called from both project and global scope — Cline MCP is always global.
func planGlobalMCP(f *domain.Fragment, ctx engine.SyncContext) error {
	home := ctx.TargetDir
	if ctx.Scope == domain.ScopeProject {
		home = ctx.Home
		if home == "" {
			return fmt.Errorf("resolving home for Cline MCP settings: HOME is not set")
		}
	}
	dst := SettingsGlobalPath(home)
	if dst == "" || len(ctx.Profile.MCPServers) == 0 {
		return nil
	}

	out, _, err := RenderBytes(nil, ctx.Profile.MCPServers, true)
	if err != nil {
		return fmt.Errorf("render cline MCP settings: %w", err)
	}
	f.Plugins = append(f.Plugins, domain.SettingsAction{
		Dst: dst, Desired: out, Harness: domain.HarnessCline,
		Label: "cline_mcp_settings.json",
	})
	f.Desired = append(f.Desired, filepath.Clean(dst))
	return nil
}

// Render produces a Fragment for pack rendering.
func (Harness) Render(ctx harness.RenderContext) (domain.Fragment, error) {
	if len(ctx.Profile.MCPServers) == 0 {
		return domain.Fragment{}, nil
	}
	out, _, err := RenderBytes(nil, ctx.Profile.MCPServers, false)
	if err != nil {
		return domain.Fragment{}, err
	}
	dst := filepath.Join(ctx.OutDir, "cline", "cline_mcp_settings.json")
	return domain.Fragment{
		Writes:  []domain.WriteAction{{Dst: dst, Content: out}},
		Desired: []string{dst},
	}, nil
}

// StripManagedSettings removes sync-managed keys from rendered settings.
func (Harness) StripManagedSettings(rendered []byte, _ string) ([]byte, error) {
	return StripManagedKeys(rendered)
}

// Capture extracts Cline content for round-trip save.
func (Harness) Capture(ctx harness.CaptureContext) (harness.CaptureResult, error) {
	res := harness.NewCaptureResult()

	if ctx.Scope == domain.ScopeProject {
		return captureProject(ctx.ProjectDir, res)
	}
	return captureGlobal(ctx.Home, res)
}

func captureProject(projectDir string, res harness.CaptureResult) (harness.CaptureResult, error) {
	copies, warnings := harness.CaptureContentDir(
		filepath.Join(projectDir, ".clinerules"), "rules", ".md",
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

	copies, warnings = harness.CaptureContentDir(
		filepath.Join(projectDir, ".clinerules", "agents"), "agents", ".md",
		func(raw []byte, name, src string) error {
			a, err := engine.ParseAgentBytes(raw, name, "")
			if err != nil {
				return err
			}
			a.SourcePath = src
			res.Agents = append(res.Agents, a)
			return nil
		})
	res.Copies = append(res.Copies, copies...)
	res.Warnings = append(res.Warnings, warnings...)

	copies, warnings = harness.CaptureContentDir(
		filepath.Join(projectDir, ".clinerules", "workflows"), "workflows", ".md",
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

	skillCopies, skills := harness.CaptureSkills(
		filepath.Join(projectDir, ".clinerules", "skills"),
		"skills",
	)
	res.Copies = append(res.Copies, skillCopies...)
	res.Skills = append(res.Skills, skills...)

	return res, nil
}

func captureGlobal(home string, res harness.CaptureResult) (harness.CaptureResult, error) {
	copies, warnings := harness.CaptureContentDir(
		RulesGlobalDir(home), "rules", ".md",
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

	copies, warnings = harness.CaptureContentDir(
		AgentsGlobalDir(home), "agents", ".md",
		func(raw []byte, name, src string) error {
			a, err := engine.ParseAgentBytes(raw, name, "")
			if err != nil {
				return err
			}
			a.SourcePath = src
			res.Agents = append(res.Agents, a)
			return nil
		})
	res.Copies = append(res.Copies, copies...)
	res.Warnings = append(res.Warnings, warnings...)

	copies, warnings = harness.CaptureContentDir(
		WorkflowsGlobalDir(home), "workflows", ".md",
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

	skillCopies, skills := harness.CaptureSkills(
		filepath.Join(home, ".cline", "skills"),
		"skills",
	)
	res.Copies = append(res.Copies, skillCopies...)
	res.Skills = append(res.Skills, skills...)

	settingsPath := SettingsGlobalPath(home)
	if b, ok, err := util.ReadFileIfExists(settingsPath); err != nil {
		return res, fmt.Errorf("capture cline settings: %w", err)
	} else if ok {
		res.Writes = append(res.Writes, domain.WriteAction{
			Dst: filepath.Join("configs", "cline", "cline_mcp_settings.json"), Content: b, Src: settingsPath,
		})
		res.Warnings = append(res.Warnings, parseClineSettings(res.MCPServers, res.AllowedTools, b)...)
	}

	return res, nil
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
