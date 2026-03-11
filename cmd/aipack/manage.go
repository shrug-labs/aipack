package main

import (
	"fmt"
	"os"

	"github.com/shrug-labs/aipack/cmd/aipack/tui"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

// ManageCmd is the Kong command struct for `aipack manage`.
type ManageCmd struct {
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
}

func (c *ManageCmd) Help() string {
	return `Interactive TUI for managing profiles and packs. Provides a tabbed interface
for viewing and editing profile content selections, toggling pack items,
checking sync status, and managing installed packs.

Requires an interactive terminal (TTY).

Key bindings:
  tab           Switch between Profiles and Packs tabs
  j/k, up/down  Navigate lists
  enter         Toggle focus / expand tree
  space         Toggle items on/off
  l             List profiles (jump to selection)
  n             Create new profile
  d             Delete profile
  D             Duplicate profile
  a             Activate profile (set as default)
  p             Add pack to profile
  r             Remove pack from profile
  s             Sync profile to harness (auto-saves)
  esc           Quit (auto-saves changes)

Examples:
  # Open the management TUI
  aipack manage

See also: profile edit, profile list, pack list`
}

func (c *ManageCmd) Run(g *Globals) error {
	if !g.StdinTTY {
		fmt.Fprintln(g.Stderr, "manage requires an interactive terminal")
		return ExitError{Code: cmdutil.ExitUsage}
	}

	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(cfgDir))
	if err != nil {
		return err
	}

	_, err = tui.Run(tui.RunConfig{
		ConfigDir: cfgDir,
		SyncCfg:   syncCfg,
		Registry:  g.Registry,
	})
	return err
}
