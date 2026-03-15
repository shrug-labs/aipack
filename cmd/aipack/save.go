package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

type saveEnv struct {
	scope      domain.Scope
	projectDir string
	harnesses  []domain.Harness
}

func (c *SaveCmd) resolveSaveEnv(optionalHarness bool) (saveEnv, error) {
	// Load sync-config for scope and harness resolution.
	var syncCfg config.SyncConfig
	if cfgDir, err := cmdutil.ResolveConfigDir(c.ConfigDir, os.Getenv("HOME")); err == nil {
		if sc, serr := config.LoadSyncConfig(config.SyncConfigPath(cfgDir)); serr == nil {
			syncCfg = sc
		}
	}

	sc, err := cmdutil.ResolveScopeDefault(c.Scope, syncCfg.Defaults.Scope)
	if err != nil {
		return saveEnv{}, err
	}
	if err := validateProjectDirForScope(sc, c.ProjectDir); err != nil {
		return saveEnv{}, err
	}

	rootDir, err := os.Getwd()
	if err != nil {
		return saveEnv{}, err
	}
	projectDir := rootDir
	if sc == domain.ScopeProject && c.ProjectDir != nil {
		projectDir = *c.ProjectDir
	}
	projectAbs, err := cmdutil.ResolveProjectDir(rootDir, projectDir)
	if err != nil {
		return saveEnv{}, err
	}

	var hs []domain.Harness
	if optionalHarness {
		hs, err = cmdutil.ResolveHarnessesOptional(c.Harness, syncCfg.Defaults.Harnesses)
	} else {
		hs, err = cmdutil.ResolveHarnessesDefault(c.Harness, syncCfg.Defaults.Harnesses)
	}
	if err != nil {
		return saveEnv{}, err
	}
	return saveEnv{scope: sc, projectDir: projectAbs, harnesses: hs}, nil
}

type SaveCmd struct {
	Scope       string  `help:"Where to capture from: 'project' reads project directory, 'global' reads ~/ config locations (default: sync-config defaults.scope, then 'project')" default:"default" enum:"project,global,default"`
	ProjectDir  *string `help:"Project directory for scope=project (default: current working directory)" name:"project-dir" type:"path"`
	Harness     string  `help:"Harness to save from: claudecode|cline|codex|opencode|all (default: sync-config defaults.harnesses, then AIPACK_DEFAULT_HARNESS)" name:"harness"`
	ToPack      string  `help:"Save content to this installed pack (creates pack if it does not exist)" name:"to-pack"`
	Types       string  `help:"Content types to save: rules,agents,workflows,skills,mcp,settings (comma-separated; default: all types)" name:"types"`
	Profile     string  `help:"Profile name for round-trip mode (default: sync-config defaults.profile, then 'default')" name:"profile"`
	ProfilePath string  `help:"Direct path to a profile YAML file for round-trip mode" name:"profile-path" type:"path"`
	ConfigDir   string  `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Force       bool    `help:"Auto-approve settings saves and overwrite file conflicts"`
	DryRun      bool    `help:"Preview changes without writing files" name:"dry-run"`
}

func (c *SaveCmd) Help() string {
	return `Operates in two modes:

Round-trip (default): Requires a profile. Compares current harness files against
the ledger written by the last sync. Changed files are written back to their
source pack. Settings files require --force or are reported as "pending".

To-pack (--to-pack NAME): Captures harness content and saves it to the named
installed pack. If the pack does not exist, scaffolds a new pack directory and
registers it in sync-config. Optionally filter with --types and --harness.

Harness resolution: --harness > sync-config defaults.harnesses > AIPACK_DEFAULT_HARNESS

Examples:
  # Round-trip: save changed files back to source packs
  aipack save --profile default

  # Round-trip: preview changes without writing
  aipack save --profile default --dry-run

  # Round-trip: force-save settings changes
  aipack save --profile default --force

  # To-pack: save all harness content to an existing pack
  aipack save --to-pack my-pack

  # To-pack: save only rules and skills from claudecode
  aipack save --to-pack my-pack --harness claudecode --types rules,skills

  # To-pack: create a new pack from current harness state
  aipack save --to-pack new-pack --scope global

  # To-pack: preview what would be saved
  aipack save --to-pack my-pack --dry-run

See also: sync, clean, pack enable`
}

func (c *SaveCmd) Validate() error {
	if c.Types != "" && c.ToPack == "" {
		return fmt.Errorf("--types requires --to-pack")
	}
	if c.Scope == string(domain.ScopeGlobal) && c.ProjectDir != nil {
		return fmt.Errorf("--project-dir is not valid for --scope global")
	}
	return nil
}

func (c *SaveCmd) Run(g *Globals) error {
	if c.ToPack != "" {
		return c.runToPack(g)
	}
	return c.runRoundTrip(g)
}

func (c *SaveCmd) runRoundTrip(g *Globals) error {
	loaded, code := loadProfile(c.Profile, c.ProfilePath, c.ConfigDir, g.Stderr)
	if code >= 0 {
		return ExitError{Code: code}
	}
	cmdutil.PrintWarnings(g.Stderr, loaded.warnings)

	env, err := c.resolveSaveEnv(false)
	if err != nil {
		fmt.Fprintln(g.Stderr, "ERROR:", err)
		return ExitError{Code: cmdutil.ExitUsage}
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

func (c *SaveCmd) runToPack(g *Globals) error {
	env, err := c.resolveSaveEnv(true)
	if err != nil {
		fmt.Fprintln(g.Stderr, "ERROR:", err)
		return ExitError{Code: cmdutil.ExitUsage}
	}
	configDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	// Resolve categories: explicit --types or all categories + settings.
	var categories []domain.PackCategory
	if c.Types != "" {
		categories, err = parseCategories(c.Types)
		if err != nil {
			return err
		}
	} else {
		categories = append(domain.AllPackCategories(), domain.CategorySettings)
	}

	if len(env.harnesses) == 0 {
		return fmt.Errorf("no harness resolved — specify --harness or configure defaults.harnesses in sync-config")
	}

	home := os.Getenv("HOME")
	packRoot := filepath.Join(configDir, "packs", c.ToPack)
	createPack := false
	if _, statErr := os.Stat(packRoot + "/pack.json"); os.IsNotExist(statErr) {
		createPack = true
	}

	var aggregated app.SavePipelineResult
	foundCandidates := false
	for _, harnessID := range env.harnesses {
		candidates, discoverWarnings, err := app.DiscoverSaveFiles(app.DiscoverSaveRequest{
			HarnessID:  harnessID,
			Categories: categories,
			Scope:      env.scope,
			ProjectDir: env.projectDir,
			Home:       home,
			ConfigDir:  configDir,
		}, g.Registry)
		if err != nil {
			return err
		}
		for _, w := range discoverWarnings {
			fmt.Fprintf(g.Stderr, "warning: %s\n", w)
		}
		if len(candidates) == 0 {
			continue
		}
		foundCandidates = true

		result, err := app.RunSavePipeline(app.SavePipelineRequest{
			Candidates: candidates,
			PackName:   c.ToPack,
			ConfigDir:  configDir,
			Scope:      env.scope,
			ProjectDir: env.projectDir,
			Home:       home,
			HarnessID:  harnessID,
			CreatePack: createPack,
			Force:      c.Force,
			DryRun:     c.DryRun,
		}, g.Registry)
		if err != nil {
			return err
		}

		if !c.DryRun {
			createPack = false
		}
		aggregated.PackCreated = aggregated.PackCreated || result.PackCreated
		aggregated.SavedFiles = append(aggregated.SavedFiles, result.SavedFiles...)
		aggregated.Conflicts = append(aggregated.Conflicts, result.Conflicts...)
		aggregated.SecretFindings = append(aggregated.SecretFindings, result.SecretFindings...)
		aggregated.Warnings = append(aggregated.Warnings, result.Warnings...)
	}

	if !foundCandidates {
		fmt.Fprintln(g.Stderr, "no files found for the specified harness and types")
		return nil
	}

	prefix := ""
	if c.DryRun {
		prefix = "(dry-run) "
	}
	if aggregated.PackCreated {
		fmt.Fprintf(g.Stderr, "%screated new pack: %s\n", prefix, c.ToPack)
	}
	for _, f := range aggregated.SavedFiles {
		fmt.Fprintf(g.Stderr, "  %ssaved: %s → %s\n", prefix, f.HarnessPath, f.PackPath)
	}
	for _, cf := range aggregated.Conflicts {
		fmt.Fprintf(g.Stderr, "  conflict: %s (use --force to overwrite)\n", cf.HarnessPath)
	}
	for _, sf := range aggregated.SecretFindings {
		fmt.Fprintf(g.Stderr, "  warning: %s\n", sf)
	}
	fmt.Fprintf(g.Stdout, "%s%d saved, %d conflicts\n", prefix, len(aggregated.SavedFiles), len(aggregated.Conflicts))
	return nil
}

func parseCategories(raw string) ([]domain.PackCategory, error) {
	var cats []domain.PackCategory
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		cat, ok := domain.ParseSingularLabel(p)
		if !ok {
			// Try plural form.
			cat = domain.PackCategory(p)
			switch cat {
			case domain.CategoryRules, domain.CategoryAgents, domain.CategoryWorkflows,
				domain.CategorySkills, domain.CategoryMCP, domain.CategorySettings:
			default:
				return nil, fmt.Errorf("unknown content type: %q (expected: rules, agents, workflows, skills, mcp, settings)", p)
			}
		}
		cats = append(cats, cat)
	}
	return cats, nil
}
