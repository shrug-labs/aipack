package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/domain"
)

type TraceCmd struct {
	Type        string  `arg:"" enum:"rule,agent,workflow,skill,mcp" help:"Resource type to trace"`
	Name        string  `arg:"" help:"Resource name (rule name, agent name, skill name, MCP server name)"`
	Profile     string  `help:"Profile name (default: sync-config defaults.profile, then 'default')" name:"profile"`
	ProfilePath string  `help:"Direct path to a profile YAML file" name:"profile-path" type:"path"`
	ConfigDir   string  `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Scope       string  `help:"Scope: project|global (default: sync-config defaults.scope, then 'project')" default:"default" enum:"project,global,default"`
	ProjectDir  *string `help:"Project directory for scope=project" name:"project-dir" type:"path"`
	Harness     string  `help:"Filter to specific harness" name:"harness"`
	JSON        bool    `help:"Machine-readable JSON output" name:"json"`
}

func (c *TraceCmd) Help() string {
	return `Traces a pack resource from source through the sync pipeline to its
harness destination(s). Shows the pack source path, planned destination
per harness, and on-disk state (create, identical, managed, conflict,
untracked, error).

Useful for debugging content routing issues — "why didn't my rule appear?"
or "which pack is this agent coming from?"

Examples:
  # Trace a rule
  aipack trace rule anti-slop

  # Trace a skill with JSON output
  aipack trace skill deep-research --json

  # Trace an MCP server
  aipack trace mcp atlassian

  # Trace within a specific harness
  aipack trace rule user-baseline --harness claudecode

See also: sync --dry-run --verbose, status`
}

func (c *TraceCmd) Validate() error {
	if c.Scope == string(domain.ScopeGlobal) && c.ProjectDir != nil {
		return fmt.Errorf("--project-dir is not valid for --scope global")
	}
	return nil
}

func (c *TraceCmd) Run(g *Globals) error {
	loaded, exitCode := loadProfile(c.Profile, c.ProfilePath, c.ConfigDir, g.Stderr)
	if exitCode >= 0 {
		return ExitError{Code: exitCode}
	}

	scope, err := cmdutil.ResolveScopeDefault(c.Scope, loaded.syncCfg.Defaults.Scope)
	if err != nil {
		return err
	}
	if err := validateProjectDirForScope(scope, c.ProjectDir); err != nil {
		fmt.Fprintln(g.Stderr, "ERROR:", err)
		return ExitError{Code: cmdutil.ExitUsage}
	}

	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}
	if scope == domain.ScopeProject && c.ProjectDir != nil {
		projectDir, err = filepath.Abs(*c.ProjectDir)
		if err != nil {
			return err
		}
	}

	hs, err := cmdutil.ResolveHarnessesOptional(c.Harness, loaded.syncCfg.Defaults.Harnesses)
	if err != nil {
		return err
	}

	result, err := app.RunTrace(loaded.profile, app.TraceRequest{
		TargetSpec: app.TargetSpec{
			Scope:      scope,
			ProjectDir: projectDir,
			Harnesses:  hs,
			Home:       os.Getenv("HOME"),
		},
		ResourceType: c.Type,
		ResourceName: c.Name,
	}, g.Registry)
	if err != nil {
		return err
	}

	if c.JSON {
		return cmdutil.WriteJSON(g.Stdout, result)
	}

	printTraceHuman(result, g)
	if !result.Found {
		return ExitError{Code: cmdutil.ExitFail}
	}
	return nil
}

func printTraceHuman(result app.TraceResult, g *Globals) {
	if !result.Found {
		fmt.Fprintf(g.Stderr, "%s %q not found in active profile\n", result.ResourceType, result.ResourceName)
		return
	}

	fmt.Fprintf(g.Stdout, "%s: %s\n", result.ResourceType, result.ResourceName)
	if result.Source != nil {
		fmt.Fprintf(g.Stdout, "  pack: %s\n", result.Source.Pack)
		if result.Source.SourcePath != "" {
			fmt.Fprintf(g.Stdout, "  source: %s\n", result.Source.SourcePath)
		}
	}

	if len(result.Destinations) == 0 {
		fmt.Fprintln(g.Stdout, "  destinations: (none planned)")
		return
	}

	fmt.Fprintln(g.Stdout, "  destinations:")
	for _, d := range result.Destinations {
		harness := d.Harness
		if harness == "" {
			harness = "?"
		}
		fmt.Fprintf(g.Stdout, "    %s: %s [%s]\n", harness, d.Path, d.State)
	}
}
