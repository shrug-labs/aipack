package main

import (
	"fmt"
	"os"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

type CleanCmd struct {
	ConfigDir  string  `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Scope      string  `help:"Where to clean: 'project' cleans project directory, 'global' cleans ~/ config locations (default: sync-config defaults.scope, then 'project')" default:"default" enum:"project,global,default"`
	ProjectDir *string `help:"Project directory for scope=project (default: current working directory)" name:"project-dir" type:"path"`
	Yes        bool    `help:"Skip confirmation prompt and proceed immediately"`
	DryRun     bool    `help:"Preview what would be removed without deleting anything" name:"dry-run"`
	Ledger     bool    `help:"Also delete the .aipack/ ledger directory"`
	Harness    string  `help:"Optional harness filter: claudecode|cline|codex|opencode|all (default: sync-config defaults.harnesses, then all harnesses)" name:"harness"`
}

func (c *CleanCmd) Help() string {
	return `Removes all sync-managed content from harness file locations: rules, agents,
workflows, skills, MCP server configs, and tool allowlists. Preserves unrelated
harness settings (model choice, provider config, etc.) by only deleting files
tracked in the sync ledger. Prompts for confirmation unless --yes is set.

Examples:
  # Clean managed files from the current project directory
  aipack clean

  # Preview what would be removed without deleting
  aipack clean --dry-run

  # Clean only the cline harness globally, skip confirmation
  aipack clean --scope global --harness cline --yes

  # Clean and also remove the .aipack/ ledger directory
  aipack clean --ledger --yes

See also: sync, save`
}

func (c *CleanCmd) Validate() error {
	if c.Scope == string(domain.ScopeGlobal) && c.ProjectDir != nil {
		return fmt.Errorf("--project-dir is not valid for --scope global")
	}
	return nil
}

func (c *CleanCmd) Run(g *Globals) error {
	// Load sync-config for scope and harness resolution.
	var syncCfg config.SyncConfig
	if cfgDir, err := cmdutil.ResolveConfigDir(c.ConfigDir, os.Getenv("HOME")); err == nil {
		if sc, serr := config.LoadSyncConfig(config.SyncConfigPath(cfgDir)); serr == nil {
			syncCfg = sc
		}
	}

	scope, err := cmdutil.ResolveScopeDefault(c.Scope, syncCfg.Defaults.Scope)
	if err != nil {
		fmt.Fprintln(g.Stderr, "ERROR:", err)
		return ExitError{Code: cmdutil.ExitUsage}
	}
	if err := validateProjectDirForScope(scope, c.ProjectDir); err != nil {
		fmt.Fprintln(g.Stderr, "ERROR:", err)
		return ExitError{Code: cmdutil.ExitUsage}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	projectDir := cwd
	if scope == domain.ScopeProject && c.ProjectDir != nil {
		projectDir = *c.ProjectDir
	}
	projectAbs, err := cmdutil.ResolveProjectDir(cwd, projectDir)
	if err != nil {
		return err
	}

	harnesses, err := cmdutil.ResolveHarnessesOptional(c.Harness, syncCfg.Defaults.Harnesses)
	if err != nil {
		return err
	}

	if err := app.RunClean(app.CleanRequest{
		TargetSpec: app.TargetSpec{
			Scope:      scope,
			ProjectDir: projectAbs,
			Harnesses:  harnesses,
			Home:       os.Getenv("HOME"),
		},
		WipeLedger: c.Ledger,
		Yes:        c.Yes,
		DryRun:     c.DryRun,
		Stdin:      g.Stdin,
		Stderr:     g.Stderr,
		StdinIsTerminal: func() bool {
			return g.StdinTTY
		},
	}, g.Registry); err != nil {
		return err
	}
	if !c.DryRun {
		fmt.Fprintln(g.Stdout, "clean OK")
	}
	return nil
}
