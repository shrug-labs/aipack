package main

import (
	"os"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
)

type InitCmd struct {
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Force     bool   `help:"Overwrite existing sync-config and default profile files"`
}

func (c *InitCmd) Help() string {
	return `Creates ~/.config/aipack/sync-config.yaml and ~/.config/aipack/profiles/default.yaml
with starter content. Skips files that already exist unless --force is set.

Examples:
  # Create default config files
  aipack init

  # Overwrite existing config files
  aipack init --force

  # Use a custom config directory
  aipack init --config-dir /path/to/config

See also: doctor, sync`
}

func (c *InitCmd) Run(g *Globals) error {
	configDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	return app.RunInit(app.InitRequest{
		ConfigDir: configDir,
		Force:     c.Force,
	}, g.Stdout)
}
