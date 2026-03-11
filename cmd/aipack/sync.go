package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/domain"
)

type SyncCmd struct {
	Profile      string  `help:"Profile name (default: sync-config defaults.profile, then 'default')" name:"profile"`
	ProfilePath  string  `help:"Direct path to a profile YAML file (overrides --profile)" name:"profile-path" type:"path"`
	ConfigDir    string  `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Scope        string  `help:"Where to apply: 'project' writes to project directory, 'global' writes to ~/ config locations (default: sync-config defaults.scope, then 'project')" default:"default" enum:"project,global,default"`
	ProjectDir   *string `help:"Project directory for scope=project (default: current working directory)" name:"project-dir" type:"path"`
	Harness      string  `help:"Target harness: claudecode|cline|codex|opencode (default: sync-config defaults.harnesses, then AIPACK_DEFAULT_HARNESS)" name:"harness"`
	Force        bool    `help:"Override file conflicts and enable pruning of stale managed files"`
	SkipSettings bool    `help:"Skip harness settings file sync (plugins and MCP routing still sync)" name:"skip-settings"`
	Yes          bool    `help:"Auto-confirm prune deletions without prompting"`
	DryRun       bool    `help:"Preview planned changes without writing any files" name:"dry-run"`
	Verbose      bool    `help:"Show content diffs for changed files (use with --dry-run)" short:"v"`
}

func (c *SyncCmd) Help() string {
	return `Resolves the named profile, plans file writes, directory copies, and settings
merges for the target harness(es), then applies them. A ledger
(.aipack/ledger.json) tracks which files are managed. On subsequent runs, only
changed files are updated. With --force, conflicts are overridden and stale
managed files are pruned.

Profile resolution: --profile-path > --profile > sync-config defaults.profile > "default"
Scope resolution:   --scope > sync-config defaults.scope > "project"
Harness resolution: --harness > sync-config defaults.harnesses > AIPACK_DEFAULT_HARNESS

Examples:
  # Sync default profile to the current project directory
  aipack sync --profile default

  # Preview what would change without writing files
  aipack sync --profile default --dry-run

  # Preview with content diffs for changed files
  aipack sync --profile default --dry-run --verbose

  # Force-sync globally, overriding conflicts and pruning stale files
  aipack sync --profile prod --scope global --force --yes

  # Sync only to the opencode harness
  aipack sync --profile default --harness opencode

  # Sync using a profile file outside the config directory
  aipack sync --profile-path /path/to/custom-profile.yaml

See also: render, save, doctor, clean`
}

func (c *SyncCmd) Validate() error {
	if c.Scope == "global" && c.ProjectDir != nil {
		return fmt.Errorf("--project-dir is not valid for --scope global")
	}
	return nil
}

func (c *SyncCmd) Run(g *Globals) error {
	loaded, exitCode := loadProfile(c.Profile, c.ProfilePath, c.ConfigDir, g.Stderr)
	if exitCode >= 0 {
		return ExitError{Code: exitCode}
	}

	// Scope resolution: --scope flag > sync-config defaults.scope > "project"
	var scopeRaw string
	if c.Scope != "default" {
		scopeRaw = c.Scope
	} else if loaded.syncCfg.Defaults.Scope != "" {
		scopeRaw = loaded.syncCfg.Defaults.Scope
	} else {
		scopeRaw = "project"
	}
	scope, err := cmdutil.NormalizeScope(scopeRaw)
	if err != nil {
		return err
	}

	callerDir, err := os.Getwd()
	if err != nil {
		return err
	}

	projectDirValue := callerDir
	if scope == domain.ScopeProject {
		if c.ProjectDir != nil {
			projectDirValue = *c.ProjectDir
		}
		projectDirValue, err = filepath.Abs(projectDirValue)
		if err != nil {
			return err
		}
	}

	// Harness resolution: --harness flag > sync-config defaults > env var
	var harnessRaw []string
	if strings.TrimSpace(c.Harness) != "" {
		harnessRaw = append(harnessRaw, c.Harness)
	} else {
		harnessRaw = loaded.syncCfg.Defaults.Harnesses
	}
	hs, err := cmdutil.ResolveHarnesses(harnessRaw)
	if err != nil {
		return err
	}

	cmdutil.PrintWarnings(g.Stderr, loaded.warnings)

	res, err := app.RunSync(loaded.profile, app.SyncRequest{
		TargetSpec: app.TargetSpec{
			Scope:      scope,
			ProjectDir: projectDirValue,
			Harnesses:  hs,
			Home:       os.Getenv("HOME"),
		},
		Force:        c.Force,
		SkipSettings: c.SkipSettings,
		Yes:          c.Yes,
		DryRun:       c.DryRun,
		Verbose:      c.Verbose,
	}, g.Registry, g.Stdout, g.Stderr)
	if err != nil {
		return err
	}
	if !c.DryRun {
		p := res.Plan
		fmt.Fprintf(g.Stdout, "sync OK: %d writes, %d copies, %d settings\n",
			len(p.Writes), len(p.Copies), len(p.Settings))
	}
	return nil
}
