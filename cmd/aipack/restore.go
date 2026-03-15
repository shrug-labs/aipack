package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

type RestoreCmd struct {
	ConfigDir  string  `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Scope      string  `help:"Where to restore: 'project' or 'global' (default: sync-config defaults.scope, then 'project')" default:"default" enum:"project,global,default"`
	ProjectDir *string `help:"Project directory for scope=project (default: current working directory)" name:"project-dir" type:"path"`
	Harness    string  `help:"Target harness: claudecode|cline|codex|opencode|all (default: sync-config defaults.harnesses, then all)" name:"harness"`
	Yes        bool    `help:"Skip confirmation prompt and proceed immediately"`
	DryRun     bool    `help:"Preview what would be restored without writing any files" name:"dry-run"`
	JSON       bool    `help:"Emit machine-readable JSON output" name:"json"`
}

func (c *RestoreCmd) Help() string {
	return `Restores settings files from the pre-sync cache created during sync.
Prompts for confirmation unless --yes or --dry-run is set.

Examples:
  # Undo the last sync's settings changes
  aipack restore --yes

  # Preview what would be restored
  aipack restore --dry-run

  # Restore only claudecode settings
  aipack restore --harness claudecode --yes

See also: sync, clean, save`
}

func (c *RestoreCmd) Validate() error {
	if c.Scope == string(domain.ScopeGlobal) && c.ProjectDir != nil {
		return fmt.Errorf("--project-dir is not valid for --scope global")
	}
	return nil
}

func (c *RestoreCmd) Run(g *Globals) error {
	home := os.Getenv("HOME")

	// Load sync-config for scope and harness resolution.
	cfgDir, err := cmdutil.ResolveConfigDir(c.ConfigDir, home)
	if err != nil {
		return err
	}

	// Restore is a recovery tool — tolerate missing/corrupt sync-config.
	var syncCfg config.SyncConfig
	if sc, scErr := config.LoadSyncConfig(config.SyncConfigPath(cfgDir)); scErr == nil {
		syncCfg = sc
	}

	scope, err := cmdutil.ResolveScopeDefault(c.Scope, syncCfg.Defaults.Scope)
	if err != nil {
		return err
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

	// Harness resolution: flag → sync-config → all known harnesses (recovery fallback).
	var harnessRaw []string
	if strings.TrimSpace(c.Harness) != "" {
		harnessRaw = append(harnessRaw, c.Harness)
	} else {
		harnessRaw = syncCfg.Defaults.Harnesses
	}
	if len(harnessRaw) == 0 {
		harnessRaw = domain.HarnessNames()
	}
	harnesses, err := cmdutil.ResolveHarnesses(harnessRaw)
	if err != nil {
		return err
	}

	// Confirmation prompt unless --yes or --dry-run.
	if !c.Yes && !c.DryRun {
		fmt.Fprintln(g.Stderr, "This will restore settings files from the pre-sync cache.")
		fmt.Fprint(g.Stderr, "Proceed? [y/N] ")
		if g.StdinTTY {
			var answer string
			fmt.Fscan(g.Stdin, &answer)
			if strings.ToLower(strings.TrimSpace(answer)) != "y" {
				fmt.Fprintln(g.Stderr, "Aborted.")
				return nil
			}
		} else {
			fmt.Fprintln(g.Stderr, "Aborted (non-interactive, use --yes to confirm).")
			return nil
		}
	}

	// Derive a cache-level filter when a specific harness was requested.
	var filterHarness string
	if strings.TrimSpace(c.Harness) != "" && strings.ToLower(c.Harness) != "all" {
		if h, herr := cmdutil.NormalizeHarness(c.Harness); herr == nil {
			filterHarness = string(h)
		}
	}

	res, err := app.RunRestore(app.RestoreRequest{
		TargetSpec: app.TargetSpec{
			Scope:      scope,
			ProjectDir: projectAbs,
			Harnesses:  harnesses,
			Home:       home,
		},
		FilterHarness: filterHarness,
		DryRun:        c.DryRun,
		Stderr:        g.Stderr,
	})
	if err != nil {
		return err
	}

	if c.JSON {
		return cmdutil.WriteJSON(g.Stdout, res)
	}

	if len(res.RestoredFiles) == 0 {
		fmt.Fprintln(g.Stdout, "restore: nothing to restore")
	} else if c.DryRun {
		fmt.Fprintf(g.Stdout, "restore dry-run: %d file(s) would be restored\n", len(res.RestoredFiles))
	} else {
		fmt.Fprintf(g.Stdout, "restore OK: %d file(s)\n", len(res.RestoredFiles))
	}
	return nil
}
