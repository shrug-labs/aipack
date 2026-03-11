package main

import (
	"fmt"
	"io"
	"os"

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
		if d, err := config.DefaultConfigDir(os.Getenv("HOME")); err == nil {
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

	profile := resolveProfileName(profileFlag, syncCfg)

	path, err := config.ResolveProfilePath(profilePathFlag, configDir, profile, os.Getenv("HOME"))
	if err != nil {
		fmt.Fprintln(stderr, "ERROR:", err)
		return loadedProfile{}, cmdutil.ExitFail
	}
	profileCfg, err := config.LoadProfile(path)
	if err != nil {
		fmt.Fprintln(stderr, "ERROR:", err)
		return loadedProfile{}, cmdutil.ExitFail
	}
	prof, warnings, err := engine.Resolve(profileCfg, path, configDir)
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

// resolveProfileName returns the effective profile name from an explicit flag value
// and sync-config defaults, falling back to "default".
func resolveProfileName(explicit string, sc config.SyncConfig) string {
	if explicit != "" {
		return explicit
	}
	if sc.Defaults.Profile != "" {
		return sc.Defaults.Profile
	}
	return "default"
}
