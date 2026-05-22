package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

func maybeAutoSyncProfile(ctx context.Context, g *Globals, cfgDir, profileName string) error {
	return maybeAutoSyncProfileWithOptions(ctx, g, cfgDir, profileName, autoSyncProfileOptions{})
}

func maybeAutoSyncProfileWithHint(ctx context.Context, g *Globals, cfgDir, profileName string) error {
	return maybeAutoSyncProfileWithOptions(ctx, g, cfgDir, profileName, autoSyncProfileOptions{PrintHint: true})
}

type autoSyncProfileOptions struct {
	PrintHint bool
}

func maybeAutoSyncProfileWithOptions(ctx context.Context, g *Globals, cfgDir, profileName string, opts autoSyncProfileOptions) error {
	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(cfgDir))
	if err != nil {
		return fmt.Errorf("auto-sync: loading sync-config: %w", err)
	}
	active := cmdutil.ResolveProfileName("", syncCfg)
	if !syncCfg.Defaults.AutoSync {
		if opts.PrintHint && profileName == active {
			printProfileSyncHint(g.Stdout)
		}
		return nil
	}
	if profileName != active {
		return nil
	}
	loaded, exitCode := loadProfileWithSyncConfig("", "", cfgDir, syncCfg, g.Stderr)
	if exitCode >= 0 {
		return ExitError{Code: exitCode}
	}
	return runAutoSync(ctx, g, loaded)
}

func printProfileSyncHint(w io.Writer) {
	fmt.Fprintln(w, "\nNext: run 'aipack sync' to sync the active profile to your harness.")
}

func finishPackContentChange(ctx context.Context, g *Globals, cfgDir, profileName string, add bool) error {
	if add {
		if err := maybeAutoSyncProfile(ctx, g, cfgDir, profileName); err != nil {
			return err
		}
	}
	fmt.Fprintln(g.Stdout, "\nNext: run 'aipack sync' to sync pack content to your harness.")
	return nil
}

func maybeAutoSyncAfterInstallMissing(ctx context.Context, g *Globals, cfgDir, profileName string, results []app.PackInstallMissingResult) error {
	for _, result := range results {
		if result.Status == app.MissingStatusInstalled {
			return maybeAutoSyncProfileWithHint(ctx, g, cfgDir, profileName)
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
