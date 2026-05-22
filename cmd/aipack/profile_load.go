package main

import (
	"fmt"
	"io"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

// loadedProfile holds the result of resolving a profile from flags and sync-config defaults.
type loadedProfile struct {
	profile     domain.Profile
	warnings    []domain.Warning
	profileCfg  config.ProfileConfig
	profileName string
	profilePath string
	syncCfg     config.SyncConfig
	configDir   string
}

// loadProfile resolves a profile from flags and sync-config defaults.
// Returns (result, exitCode) where exitCode < 0 means success.
func loadProfile(profileFlag, profilePathFlag, configDirFlag string, stderr io.Writer) (loadedProfile, int) {
	configDir := configDirFlag
	if configDir == "" {
		if d, err := config.DefaultConfigDir(config.HomeDir()); err == nil {
			configDir = d
		}
	}
	syncCfg := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	if configDir != "" {
		var err error
		syncCfg, err = config.LoadSyncConfig(config.SyncConfigPath(configDir))
		if err != nil {
			fmt.Fprintln(stderr, "ERROR:", err)
			return loadedProfile{}, cmdutil.ExitFail
		}
	}

	return loadProfileWithSyncConfig(profileFlag, profilePathFlag, configDir, syncCfg, stderr)
}

func loadProfileWithSyncConfig(profileFlag, profilePathFlag, configDir string, syncCfg config.SyncConfig, stderr io.Writer) (loadedProfile, int) {
	profile := cmdutil.ResolveProfileName(profileFlag, syncCfg)

	path, err := config.ResolveProfilePath(profilePathFlag, configDir, profile, config.HomeDir())
	if err != nil {
		fmt.Fprintln(stderr, "ERROR:", err)
		return loadedProfile{}, cmdutil.ExitFail
	}
	profileCfg, err := config.LoadProfile(path)
	if err != nil {
		fmt.Fprintln(stderr, "ERROR:", err)
		return loadedProfile{}, cmdutil.ExitFail
	}
	eng := engine.New(nil, nil)
	prevInventories, _ := loadLockfileInventories(configDir)
	prof, warnings, err := eng.ResolveWithOptions(profileCfg, path, configDir, config.ResolveOptions{
		CollisionStrategy: syncCfg.Defaults.CollisionStrategy,
		Namespaced:        syncCfg.Defaults.Namespaced,
		PrevInventories:   prevInventories,
	})
	if err != nil {
		fmt.Fprintln(stderr, "ERROR:", err)
		return loadedProfile{}, cmdutil.ExitFail
	}

	return loadedProfile{
		profile:     prof,
		warnings:    warnings,
		profileCfg:  profileCfg,
		profileName: profile,
		profilePath: path,
		syncCfg:     syncCfg,
		configDir:   configDir,
	}, -1
}

// loadLockfileInventories returns the per-pack inventories from the lockfile
// so the resolver can distinguish drifted-out refs from typos. Returns nil
// on any error — the resolver treats nil as "no baseline, unknown refs are
// still typo errors," which matches pre-drift behavior.
func loadLockfileInventories(configDir string) (map[string]domain.PackInventory, error) {
	if configDir == "" {
		return nil, nil
	}
	lf, err := config.EnsureLockfileMigrated(configDir)
	if err != nil {
		return nil, err
	}
	if len(lf.Packs) == 0 {
		return nil, nil
	}
	out := make(map[string]domain.PackInventory, len(lf.Packs))
	for name, meta := range lf.Packs {
		if meta.Resolved != nil {
			out[name] = *meta.Resolved
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
