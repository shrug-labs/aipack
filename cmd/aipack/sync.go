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
	Harness      string  `help:"Target harness: claudecode|cline|codex|opencode|all (default: sync-config defaults.harnesses, then AIPACK_DEFAULT_HARNESS)" name:"harness" predictor:"harness"`
	Force        bool    `help:"Override file conflicts"`
	SkipSettings bool    `help:"Skip harness settings file sync (MCP configs still sync)" name:"skip-settings"`
	Yes          bool    `help:"Auto-confirm deletions and overwrites without prompting"`
	DryRun       bool    `help:"Preview planned changes without writing any files" name:"dry-run"`
	Verbose      bool    `help:"Show content diffs for changed files" short:"v"`
	Watch        bool    `help:"Watch pack source directories and re-sync on changes"`
	JSON         bool    `help:"Emit machine-readable JSON output" name:"json"`
}

func (c *SyncCmd) Help() string {
	return `Resolves the named profile, plans file writes, directory copies, and settings
merges for the target harness(es), then applies them. A ledger tracks which
files are managed. On subsequent runs, only changed files are updated and files
no longer in the profile are removed. Use --force to override conflicts.

Profile resolution: --profile-path > --profile > sync-config defaults.profile > "default"
Scope resolution:   --scope > sync-config defaults.scope > "global"
Harness resolution: --harness > sync-config defaults.harnesses > AIPACK_DEFAULT_HARNESS

Examples:
  # Sync default profile to all harness locations (default: global scope)
  aipack sync --profile default

  # Preview what would change without writing files
  aipack sync --profile default --dry-run

  # Preview with content diffs for changed files
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

		scope, err := cmdutil.ResolveScopeDefault(c.Scope, loaded.syncCfg.Defaults.Scope)
		if err != nil {
			return watchDirs, err
		}
		if err := validateProjectDirForScope(scope, c.ProjectDir); err != nil {
			fmt.Fprintln(g.Stderr, "ERROR:", err)
			return watchDirs, ExitError{Code: cmdutil.ExitUsage}
		}

		callerDir, err := os.Getwd()
		if err != nil {
			return watchDirs, err
		}

		projectDirValue := callerDir
		if scope == domain.ScopeProject {
			if c.ProjectDir != nil {
				projectDirValue = *c.ProjectDir
			}
			projectDirValue, err = filepath.Abs(projectDirValue)
			if err != nil {
				return watchDirs, err
			}
		}

		hs, err := cmdutil.ResolveHarnessesDefault(c.Harness, loaded.syncCfg.Defaults.Harnesses)
		if err != nil {
			return watchDirs, err
		}

		cmdutil.PrintWarnings(g.Stderr, loaded.warnings)

		syncStdout := g.Stdout
		if c.JSON {
			syncStdout = io.Discard
		}

		eng := engine.NewWithStderr(g.Stderr)
		res, syncWarnings, err := app.RunSync(ctx, eng, loaded.profile, app.SyncRequest{
			TargetSpec: app.TargetSpec{
				ConfigDir:  loaded.configDir,
				Scope:      scope,
				ProjectDir: projectDirValue,
				Harnesses:  hs,
				Home:       config.HomeDir(),
			},
			Force:        c.Force,
			SkipSettings: c.SkipSettings,
			Yes:          c.Yes,
			DryRun:       c.DryRun,
			Verbose:      c.Verbose,
		}, g.Registry, syncStdout, g.Stderr)
		cmdutil.PrintWarnings(g.Stderr, syncWarnings)
		allWarnings := append(loaded.warnings, syncWarnings...)
		if err != nil {
			return watchDirs, err
		}
		p := res.Plan
		counts := app.CountProfileContent(loaded.profile)
		if c.JSON {
			jsonWarnings := make([]map[string]string, 0, len(allWarnings))
			for _, w := range allWarnings {
				entry := map[string]string{"message": w.Message}
				if w.Path != "" {
					entry["path"] = w.Path
				}
				if w.Field != "" {
					entry["field"] = w.Field
				}
				jsonWarnings = append(jsonWarnings, entry)
			}
			return watchDirs, cmdutil.WriteJSON(g.Stdout, map[string]any{
				"dry_run":   c.DryRun,
				"rules":     counts.Rules,
				"workflows": counts.Workflows,
				"agents":    counts.Agents,
				"skills":    counts.Skills,
				"settings":  len(p.Settings),
				"mcp":       counts.MCP,
				"warnings":  jsonWarnings,
			})
		}
		if c.DryRun {
			// Non-verbose dry-run: plan line already printed by printDryRun
			// with source counts. Verbose dry-run: already returned above.
			return watchDirs, nil
		}
		fmt.Fprintf(g.Stdout, "sync OK: %s\n", counts.String())

		return watchDirs, nil
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
			profileName := resolveProfileName(c.Profile, watchSyncCfg)
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

	profileName := resolveProfileName(profileFlag, syncCfg)
	profilePath, err := config.ResolveProfilePath(profilePathFlag, configDir, profileName, config.HomeDir())
	if err != nil {
		return nil, err
	}

	profileCfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return nil, err
	}

	prevInventories, _ := loadLockfileInventories(configDir)
	resolved, err := config.ResolveProfile(profileCfg, profilePath, configDir, syncCfg.Defaults.CollisionStrategy, prevInventories)
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
