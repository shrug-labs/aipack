package main

import (
	"fmt"
	"os"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/domain"
)

type CleanCmd struct {
	Scope      string  `help:"Where to clean: 'project' cleans project directory, 'global' cleans ~/ config locations" default:"project" enum:"project,global"`
	ProjectDir string  `help:"Project directory for scope=project" name:"project-dir" default:"." type:"path"`
	Yes        bool    `help:"Skip confirmation prompt and proceed immediately"`
	DryRun     bool    `help:"Preview what would be removed without deleting anything" name:"dry-run"`
	Ledger     bool    `help:"Also delete the .aipack/ ledger directory"`
	Harness    *string `help:"Clean only this harness: claudecode|cline|codex|opencode (default: clean all harnesses)" name:"harness"`
}

func (c *CleanCmd) Help() string {
	return `Removes all sync-managed content from harness file locations: rules, agents,
workflows, skills, MCP server configs, and tool allowlists. Preserves unrelated
harness settings (model choice, provider config, etc.) by only deleting files
tracked in the sync ledger. Prompts for confirmation unless --yes is set.

If --harness is not specified, cleans all harnesses (claudecode, cline, codex, opencode).

Examples:
  # Clean managed files from the current project directory (all harnesses)
  aipack clean --scope project

  # Preview what would be removed without deleting
  aipack clean --scope project --dry-run

  # Clean only the cline harness globally, skip confirmation
  aipack clean --scope global --harness cline --yes

  # Clean and also remove the .aipack/ ledger directory
  aipack clean --scope project --ledger --yes

See also: sync, save`
}

func (c *CleanCmd) Run(g *Globals) error {
	sc, err := cmdutil.NormalizeScope(c.Scope)
	if err != nil {
		fmt.Fprintln(g.Stderr, "ERROR:", err)
		return ExitError{Code: cmdutil.ExitUsage}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	projectAbs, err := cmdutil.ResolveProjectDir(cwd, c.ProjectDir)
	if err != nil {
		return err
	}

	var harnesses []domain.Harness
	if c.Harness != nil {
		h, err := cmdutil.NormalizeHarness(*c.Harness)
		if err != nil {
			return err
		}
		harnesses = []domain.Harness{h}
	}

	if err := app.RunClean(app.CleanRequest{
		TargetSpec: app.TargetSpec{
			Scope:      sc,
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
	}); err != nil {
		return err
	}
	if !c.DryRun {
		fmt.Fprintln(g.Stdout, "clean OK")
	}
	return nil
}
