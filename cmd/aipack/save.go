package main

import (
	"fmt"
	"os"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

type saveEnv struct {
	scope      domain.Scope
	projectDir string
	harnesses  []domain.Harness
}

func (c *SaveCmd) resolveSaveEnv() (saveEnv, error) {
	sc, err := cmdutil.NormalizeScope(c.Scope)
	if err != nil {
		return saveEnv{}, ExitError{Code: cmdutil.ExitUsage}
	}
	rootDir, err := os.Getwd()
	if err != nil {
		return saveEnv{}, err
	}
	projectAbs, err := cmdutil.ResolveProjectDir(rootDir, c.ProjectDir)
	if err != nil {
		return saveEnv{}, err
	}
	hs, err := resolveSaveHarnesses(c.Harness)
	if err != nil {
		return saveEnv{}, err
	}
	return saveEnv{scope: sc, projectDir: projectAbs, harnesses: hs}, nil
}

type SaveCmd struct {
	Scope       string `help:"Where to capture from: 'project' reads project directory, 'global' reads ~/ config locations" default:"project" enum:"project,global"`
	ProjectDir  string `help:"Project directory for scope=project" name:"project-dir" default:"." type:"path"`
	Harness     string `help:"Harness to save from: claudecode|cline|codex|opencode|all (default: sync-config defaults.harnesses, then AIPACK_DEFAULT_HARNESS; error if unresolvable)" name:"harness"`
	ToPack      string `help:"Save content to this installed pack (creates pack if it does not exist)" name:"to-pack"`
	Profile     string `help:"Profile name for round-trip mode (default: sync-config defaults.profile, then 'default')" name:"profile"`
	ProfilePath string `help:"Direct path to a profile YAML file for round-trip mode" name:"profile-path" type:"path"`
	ConfigDir   string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Force       bool   `help:"Auto-approve settings saves and overwrite file conflicts"`
	Snapshot    bool   `help:"Create a timestamped snapshot backup of current harness state"`
	DryRun      bool   `help:"Preview round-trip changes without writing files" name:"dry-run"`
}

func (c *SaveCmd) Help() string {
	return `Operates in one of three mutually exclusive modes:

Round-trip (default): Requires a profile. Compares current harness files against
the ledger written by the last sync. Changed files are written back to their
source pack. Settings files require --force or are reported as "pending".

Snapshot (--snapshot): Captures the current harness state into a timestamped
directory under ~/.config/aipack/saved/ and installs it as a pack.

To-pack (--to-pack NAME): Captures harness content and saves it to the named
installed pack. If the pack does not exist, scaffolds a new pack directory and
registers it in sync-config.

Harness resolution: --harness > sync-config defaults.harnesses > AIPACK_DEFAULT_HARNESS (no fallback — error if unresolvable)

Examples:
  # Round-trip: save changed files back to source packs
  aipack save --profile default

  # Round-trip: preview changes without writing
  aipack save --profile default --dry-run

  # Round-trip: force-save settings changes
  aipack save --profile default --force

  # Snapshot: back up current harness state
  aipack save --snapshot --scope project

  # To-pack: save harness content to an existing pack
  aipack save --to-pack my-pack --harness all

  # To-pack: create a new pack from current harness state
  aipack save --to-pack new-pack --scope global --harness cline

See also: sync, clean, pack add`
}

func (c *SaveCmd) Validate() error {
	if c.Snapshot && c.ToPack != "" {
		return fmt.Errorf("--snapshot and --to-pack are mutually exclusive")
	}
	return nil
}

func (c *SaveCmd) Run(g *Globals) error {
	switch {
	case c.Snapshot:
		return c.runSnapshot(g)
	case c.ToPack != "":
		return c.runToPack(g)
	default:
		return c.runRoundTrip(g)
	}
}

func (c *SaveCmd) runSnapshot(g *Globals) error {
	env, err := c.resolveSaveEnv()
	if err != nil {
		return err
	}

	res, err := app.RunSnapshot(app.SnapshotRequest{
		TargetSpec: app.TargetSpec{
			Scope:      env.scope,
			ProjectDir: env.projectDir,
			Harnesses:  env.harnesses,
			Home:       os.Getenv("HOME"),
		},
	}, g.Registry)
	if err != nil {
		return err
	}

	cmdutil.PrintWarnings(g.Stderr, res.CaptureWarnings)
	if len(res.SecretFindings) > 0 {
		fmt.Fprintln(g.Stderr, "warning: snapshot may contain secrets (review before sharing):")
		for _, f := range res.SecretFindings {
			fmt.Fprintln(g.Stderr, "- "+f)
		}
	}
	fmt.Fprintln(g.Stdout, res.BaseDir)
	return nil
}

func (c *SaveCmd) runToPack(g *Globals) error {
	env, err := c.resolveSaveEnv()
	if err != nil {
		return err
	}
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}
	result, err := app.RunToPack(app.ToPackRequest{
		TargetSpec: app.TargetSpec{
			Scope:      env.scope,
			ProjectDir: env.projectDir,
			Harnesses:  env.harnesses,
			Home:       os.Getenv("HOME"),
		},
		PackName:  c.ToPack,
		ConfigDir: cfgDir,
		DryRun:    c.DryRun,
		Force:     c.Force,
		Stderr:    g.Stderr,
	}, g.Registry)
	if err != nil {
		return err
	}

	prefix := ""
	if c.DryRun {
		prefix = "(dry-run) "
	}
	for _, sf := range result.SavedFiles {
		fmt.Fprintf(g.Stderr, "%ssaved: %s → %s\n", prefix, sf.HarnessPath, sf.PackPath)
	}
	for _, cf := range result.Conflicts {
		if c.Force {
			fmt.Fprintf(g.Stderr, "%sconflict (force-overwritten): %s → %s\n", prefix, cf.HarnessPath, cf.PackPath)
		}
	}
	if c.DryRun && len(result.Conflicts) > 0 {
		fmt.Fprintf(g.Stderr, "  %d conflicts would require --force\n", len(result.Conflicts))
	}
	if result.Skipped > 0 {
		fmt.Fprintf(g.Stderr, "  skipped %d files attributed to other packs\n", result.Skipped)
	}
	fmt.Fprintf(g.Stdout, "%s%d saved to pack %q\n", prefix, len(result.SavedFiles), c.ToPack)
	return nil
}

func (c *SaveCmd) runRoundTrip(g *Globals) error {
	loaded, code := loadProfile(c.Profile, c.ProfilePath, c.ConfigDir, g.Stderr)
	if code >= 0 {
		return ExitError{Code: code}
	}
	cmdutil.PrintWarnings(g.Stderr, loaded.warnings)

	env, err := c.resolveSaveEnv()
	if err != nil {
		return err
	}

	packRoots := map[string]string{}
	for _, p := range loaded.profile.Packs {
		packRoots[p.Name] = p.Root
	}

	result, err := app.RunRoundTrip(app.RoundTripRequest{
		TargetSpec: app.TargetSpec{
			Scope:      env.scope,
			ProjectDir: env.projectDir,
			Harnesses:  env.harnesses,
			Home:       os.Getenv("HOME"),
		},
		PackRoots: packRoots,
		DryRun:    c.DryRun,
		Force:     c.Force,
		Stderr:    g.Stderr,
	}, g.Registry)
	if err != nil {
		return err
	}

	cmdutil.PrintWarnings(g.Stderr, result.CaptureWarnings)

	prefix := ""
	if c.DryRun {
		prefix = "(dry-run) "
	}

	for _, sf := range result.SavedFiles {
		fmt.Fprintf(g.Stderr, "%ssaved: %s → %s (%s)\n", prefix, sf.HarnessPath, sf.PackPath, sf.PackName)
	}

	for _, cf := range result.Conflicts {
		if c.Force {
			fmt.Fprintf(g.Stderr, "%sconflict (force-overwritten): %s ↔ %s (%s)\n", prefix, cf.HarnessPath, cf.PackPath, cf.PackName)
		}
	}
	if c.DryRun && len(result.Conflicts) > 0 {
		fmt.Fprintf(g.Stderr, "  %d conflicts would require --force\n", len(result.Conflicts))
	}

	for _, ps := range result.PendingSettings {
		if c.Force || c.DryRun {
			if !c.DryRun {
				if err := util.WriteFileAtomic(ps.PackPath, ps.Stripped); err != nil {
					return fmt.Errorf("writing settings to %s: %w", ps.PackPath, err)
				}
			}
			fmt.Fprintf(g.Stderr, "%ssaved settings: %s → %s (%s)\n", prefix, ps.HarnessPath, ps.PackPath, ps.PackName)
		} else {
			fmt.Fprintf(g.Stderr, "  pending: %s has unmanaged changes (use --force to save to %s)\n", ps.HarnessPath, ps.PackPath)
		}
	}

	if len(result.UntrackedFiles) > 0 {
		fmt.Fprintf(g.Stderr, "  %d untracked files (not in ledger, run 'aipack sync' to track):\n", len(result.UntrackedFiles))
		for _, uf := range result.UntrackedFiles {
			fmt.Fprintf(g.Stderr, "    %s\n", uf)
		}
	}

	saved := len(result.SavedFiles)
	fmt.Fprintf(g.Stdout, "%s%d saved, %d unchanged\n", prefix, saved, result.UnchangedCount)
	if saved == 0 && result.UnchangedCount == 0 && len(result.UntrackedFiles) == 0 && len(result.PendingSettings) == 0 {
		fmt.Fprintln(g.Stderr, "note: nothing tracked — verify profile packs and harness content, or run 'aipack sync' first")
	}
	return nil
}
