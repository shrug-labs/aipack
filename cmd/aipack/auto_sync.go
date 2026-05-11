package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

func maybeAutoSyncProfile(ctx context.Context, g *Globals, cfgDir, profileName string) error {
	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(cfgDir))
	if err != nil {
		return fmt.Errorf("auto-sync: loading sync-config: %w", err)
	}
	if !syncCfg.Defaults.AutoSync {
		return nil
	}
	active := cmdutil.ResolveProfileName("", syncCfg)
	if profileName != active {
		return nil
	}
	loaded, exitCode := loadProfileWithSyncConfig("", "", cfgDir, syncCfg, g.Stderr)
	if exitCode >= 0 {
		return ExitError{Code: exitCode}
	}
	return runAutoSync(ctx, g, loaded)
}

func maybeAutoSyncAfterInstallMissing(ctx context.Context, g *Globals, cfgDir, profileName string, results []app.PackInstallMissingResult) error {
	for _, result := range results {
		if result.Status == app.MissingStatusInstalled {
			return maybeAutoSyncProfile(ctx, g, cfgDir, profileName)
		}
	}
	return nil
}

func maybeAutoSyncAfterPackUpdate(ctx context.Context, g *Globals, cfgDir string, results []app.PackUpdateResult) error {
	updated := map[string]bool{}
	for _, result := range results {
		if result.Status == app.StatusUpdated {
			updated[result.Name] = true
		}
	}
	if len(updated) == 0 {
		return nil
	}

	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(cfgDir))
	if err != nil {
		return fmt.Errorf("auto-sync: loading sync-config: %w", err)
	}
	if !syncCfg.Defaults.AutoSync {
		return nil
	}

	loaded, exitCode := loadProfileWithSyncConfig("", "", cfgDir, syncCfg, g.Stderr)
	if exitCode >= 0 {
		return ExitError{Code: exitCode}
	}
	for _, pe := range loaded.profileCfg.Packs {
		name := strings.TrimSpace(pe.Name)
		if name == "" || (pe.Enabled != nil && !*pe.Enabled) {
			continue
		}
		if updated[name] {
			return runAutoSync(ctx, g, loaded)
		}
	}
	return nil
}

func runAutoSync(ctx context.Context, g *Globals, loaded loadedProfile) error {
	if err := runLoadedSync(ctx, g, loaded, loadedSyncOptions{Scope: "default", AutoSync: true}); err != nil {
		return fmt.Errorf("auto-sync failed: %w", err)
	}
	return nil
}
