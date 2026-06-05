package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/update"
)

type SyncCmd struct {
	Profile      string  `help:"Profile name (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
	ProfilePath  string  `help:"Direct path to a profile YAML file (overrides --profile)" name:"profile-path" type:"path"`
	Scope        string  `help:"Where to apply: 'project' writes to project directory, 'global' writes to ~/ config locations (default: sync-config defaults.scope, then 'global')" default:"default" enum:"project,global,default"`
	ProjectDir   *string `help:"Project directory for scope=project (default: current working directory)" name:"project-dir" type:"path"`
	Harness      string  `help:"Target harness(es): claudecode|cline|codex|opencode|all (default: sync-config defaults.harnesses, then AIPACK_DEFAULT_HARNESS); each harness syncs independently" name:"harness" predictor:"harness"`
	Force        bool    `help:"Override file conflicts"`
	SkipSettings bool    `help:"Skip harness settings file sync (MCP configs still sync)" name:"skip-settings"`
	Yes          bool    `help:"Auto-confirm deletions and overwrites without prompting"`
	DryRun       bool    `help:"Preview planned destination changes without writing any files" name:"dry-run"`
	Verbose      bool    `help:"With --dry-run, show source packs and content diffs for changed files" short:"v"`
	Watch        bool    `help:"Watch pack source directories and re-sync on changes"`
	JSON         bool    `help:"Emit machine-readable JSON output" name:"json"`
}

type loadedSyncOptions struct {
	Scope        string
	ProjectDir   *string
	Harness      string
	Force        bool
	SkipSettings bool
	Yes          bool
	DryRun       bool
	Verbose      bool
	JSON         bool
	AutoSync     bool
}

func (c *SyncCmd) Help() string {
	return `Resolves the named profile, plans file writes, directory copies, and settings
merges for each target harness, then applies them. Every harness syncs
independently — its own plan, apply, and ledger — with no cross-harness
aggregation. A ledger tracks which files are managed. On subsequent runs, only
changed files are updated and files no longer in the profile are removed. Use
--force to override conflicts.

Profile resolution: --profile-path > --profile > sync-config defaults.profile > "default"
Scope resolution:   --scope > sync-config defaults.scope > "global"
Harness resolution: --harness > sync-config defaults.harnesses > AIPACK_DEFAULT_HARNESS
Multiple harnesses (including --harness all) sync one after another; the first
harness that fails stops the run.

Examples:
  # Sync default profile to the configured harness(es) (default: global scope)
  aipack sync --profile default

  # Sync to every supported harness, each independently
  aipack sync --profile default --harness all

  # Preview what would change and where without writing files
  aipack sync --profile default --dry-run

  # Preview with source packs and content diffs for changed files
  aipack sync --profile default --dry-run --verbose

  # Force-sync globally, overriding conflicts
  aipack sync --profile prod --scope global --force

  # Sync only to the opencode harness
  aipack sync --profile default --harness opencode

  # Sync using a profile file outside the config directory
  aipack sync --profile-path /path/to/custom-profile.yaml

  # Watch pack sources and re-sync on every change
  aipack sync --watch

See also: render, save, doctor, clean`
}

func (c *SyncCmd) Validate() error {
	if c.Scope == string(domain.ScopeGlobal) && c.ProjectDir != nil {
		return fmt.Errorf("--project-dir is not valid for --scope global")
	}
	if c.Watch && c.DryRun {
		return fmt.Errorf("--watch and --dry-run cannot be combined")
	}
	return nil
}

func (c *SyncCmd) Run(ctx context.Context, g *Globals) error {
	watchDirsForFlags := func() []string {
		dirs, err := resolveWatchDirs(c.Profile, c.ProfilePath, g.ConfigDir)
		if err != nil {
			return nil
		}
		return dirs
	}

	// resolveAndSync performs a single sync iteration (profile load + sync).
	// Returns the pack source dirs to watch for the next iteration.
	resolveAndSync := func() ([]string, error) {
		loaded, exitCode := loadProfile(c.Profile, c.ProfilePath, g.ConfigDir, g.Stderr)
		if exitCode >= 0 {
			return watchDirsForFlags(), ExitError{Code: exitCode}
		}
		watchDirs := app.PackSourceDirs(loaded.profile)
		err := runLoadedSync(ctx, g, loaded, loadedSyncOptions{
			Scope:        c.Scope,
			ProjectDir:   c.ProjectDir,
			Harness:      c.Harness,
			Force:        c.Force,
			SkipSettings: c.SkipSettings,
			Yes:          c.Yes,
			DryRun:       c.DryRun,
			Verbose:      c.Verbose,
			JSON:         c.JSON,
		})
		return watchDirs, err
	}

	if c.Watch {
		// Resolve config file paths to watch for changes.
		configDir := g.ConfigDir
		if configDir == "" {
			if d, derr := config.DefaultConfigDir(config.HomeDir()); derr == nil {
				configDir = d
			}
		}
		// Load sync-config to resolve the effective profile name.
		var watchSyncCfg config.SyncConfig
		if configDir != "" {
			if sc, serr := config.LoadSyncConfig(config.SyncConfigPath(configDir)); serr == nil {
				watchSyncCfg = sc
			}
		}
		var configFiles []string
		if configDir != "" {
			configFiles = append(configFiles, config.SyncConfigPath(configDir))
		}
		if c.ProfilePath != "" {
			configFiles = append(configFiles, c.ProfilePath)
		} else if configDir != "" {
			profileName := cmdutil.ResolveProfileName(c.Profile, watchSyncCfg)
			if p, perr := config.ResolveProfilePath("", configDir, profileName, config.HomeDir()); perr == nil {
				configFiles = append(configFiles, p)
			}
		}

		return app.RunWatch(ctx, resolveAndSync, configFiles, g.Stderr)
	}

	// Non-watch: single sync.
	updateCh := update.CheckAsync(ctx, version, config.HomeDir())
	_, err := resolveAndSync()
	if err != nil {
		return err
	}

	// Print update notice if it arrived promptly (don't stall on slow networks).
	select {
	case res := <-updateCh:
		if notice := res.Notice(); notice != "" {
			fmt.Fprint(g.Stderr, notice)
		}
	case <-time.After(2 * time.Second):
	}
	return nil
}

func runLoadedSync(ctx context.Context, g *Globals, loaded loadedProfile, opts loadedSyncOptions) error {
	scope, err := cmdutil.ResolveScopeDefault(opts.Scope, loaded.syncCfg.Defaults.Scope)
	if err != nil {
		return err
	}
	if err := validateProjectDirForScope(scope, opts.ProjectDir); err != nil {
		fmt.Fprintln(g.Stderr, "ERROR:", err)
		return ExitError{Code: cmdutil.ExitUsage}
	}

	callerDir, err := os.Getwd()
	if err != nil {
		return err
	}

	projectDirValue := callerDir
	if scope == domain.ScopeProject {
		if opts.ProjectDir != nil {
			projectDirValue = *opts.ProjectDir
		}
		projectDirValue, err = filepath.Abs(projectDirValue)
		if err != nil {
			return err
		}
	}

	hs, err := cmdutil.ResolveHarnessesDefault(opts.Harness, loaded.syncCfg.Defaults.Harnesses)
	if err != nil {
		return err
	}
	if err := app.ValidateSyncHarnesses(hs); err != nil {
		fmt.Fprintln(g.Stderr, "ERROR:", err)
		return ExitError{Code: cmdutil.ExitUsage}
	}

	cmdutil.PrintWarnings(g.Stderr, loaded.warnings)
	if opts.AutoSync {
		fmt.Fprintf(g.Stdout, "\nauto-sync: syncing active profile %q...\n", loaded.profileName)
	}

	syncStdout := g.Stdout
	if opts.JSON {
		syncStdout = io.Discard
	}

	eng := engine.NewWithStderr(g.Stderr)
	results, syncWarnings, err := app.RunSyncEach(ctx, eng, loaded.profile, app.SyncRequest{
		TargetSpec: app.TargetSpec{
			ConfigDir:  loaded.configDir,
			Scope:      scope,
			ProjectDir: projectDirValue,
			Harnesses:  hs,
			Home:       config.HomeDir(),
			Namespaced: loaded.syncCfg.Defaults.Namespaced,
		},
		Force:        opts.Force,
		SkipSettings: opts.SkipSettings,
		Yes:          opts.Yes,
		DryRun:       opts.DryRun,
		Verbose:      opts.Verbose,
	}, g.Registry, syncStdout, g.Stderr)
	cmdutil.PrintWarnings(g.Stderr, syncWarnings)
	counts := app.CountProfileContent(loaded.profile)
	if opts.JSON {
		// JSON output is atomic: a mid-run failure emits no partial array, just
		// the error — a consumer must not mistake a truncated run for a complete
		// one.
		if err != nil {
			return err
		}
		// Each harness syncs independently, so emit one self-contained result
		// object per harness. Profile-resolution warnings are attached to the
		// first harness's result.
		out := make([]map[string]any, 0, len(results))
		for i, res := range results {
			w := res.Warnings
			if i == 0 {
				w = append(append([]domain.Warning{}, loaded.warnings...), w...)
			}
			out = append(out, map[string]any{
				"harness":   string(res.Harness),
				"dry_run":   opts.DryRun,
				"rules":     counts.Rules,
				"workflows": counts.Workflows,
				"agents":    counts.Agents,
				"skills":    counts.Skills,
				"hooks":     counts.Hooks,
				"plugins":   counts.Plugins,
				"settings":  len(res.Plan.Settings),
				"mcp":       counts.MCP,
				"warnings":  syncJSONWarnings(w),
			})
		}
		return cmdutil.WriteJSON(g.Stdout, out)
	}
	if opts.DryRun {
		// Non-verbose dry-run: plan line already printed by printDryRun
		// with source counts. Verbose dry-run: already returned above.
		return err
	}
	// Report each harness that completed. RunSyncEach stops at the first
	// failure and returns the harnesses processed so far with the failed one
	// last, so on a partial failure we still show every harness that synced
	// before surfacing the error — the user can see exactly how far the run got.
	done := results
	if err != nil && len(done) > 0 {
		done = done[:len(done)-1]
	}
	for _, res := range done {
		fmt.Fprintf(g.Stdout, "sync OK [%s]: %s\n", res.Harness, counts.String())
	}
	return err
}

func syncJSONWarnings(warnings []domain.Warning) []map[string]string {
	out := make([]map[string]string, 0, len(warnings))
	for _, w := range warnings {
		entry := map[string]string{"message": w.Message}
		if w.Path != "" {
			entry["path"] = w.Path
		}
		if w.Field != "" {
			entry["field"] = w.Field
		}
		out = append(out, entry)
	}
	return out
}

func resolveWatchDirs(profileFlag, profilePathFlag, configDirFlag string) ([]string, error) {
	configDir := configDirFlag
	if configDir == "" {
		var err error
		configDir, err = config.DefaultConfigDir(config.HomeDir())
		if err != nil {
			return nil, err
		}
	}

	syncCfg := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	if loaded, err := config.LoadSyncConfig(config.SyncConfigPath(configDir)); err == nil {
		syncCfg = loaded
	}

	profileName := cmdutil.ResolveProfileName(profileFlag, syncCfg)
	profilePath, err := config.ResolveProfilePath(profilePathFlag, configDir, profileName, config.HomeDir())
	if err != nil {
		return nil, err
	}

	profileCfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return nil, err
	}

	prevInventories, _ := loadLockfileInventories(configDir)
	resolved, err := config.ResolveProfileWithOptions(profileCfg, profilePath, configDir, config.ResolveOptions{
		CollisionStrategy: syncCfg.Defaults.CollisionStrategy,
		Namespaced:        syncCfg.Defaults.Namespaced,
		PrevInventories:   prevInventories,
	})
	if err != nil {
		return resolveWatchDirsFallback(profileCfg, configDir), nil
	}
	resolvedPacks := resolved.Packs

	seen := map[string]bool{}
	var dirs []string
	for _, pack := range resolvedPacks {
		if pack.Root == "" {
			continue
		}
		abs, err := filepath.Abs(pack.Root)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		dirs = append(dirs, abs)
	}
	return dirs, nil
}

func resolveWatchDirsFallback(profileCfg config.ProfileConfig, configDir string) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, entry := range profileCfg.Packs {
		if entry.Enabled != nil && !*entry.Enabled {
			continue
		}
		packDir := filepath.Join(configDir, "packs", entry.Name)
		watchDir := packDir
		if manifest, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json")); err == nil {
			if root := config.ResolvePackRoot(filepath.Join(packDir, "pack.json"), manifest.Root); root != "" {
				watchDir = root
			}
		}
		abs, err := filepath.Abs(watchDir)
		if err != nil || seen[abs] {
			continue
		}
		seen[abs] = true
		dirs = append(dirs, abs)
	}
	return dirs
}
