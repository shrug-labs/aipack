package main

import (
	"context"
	"fmt"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

type InitCmd struct {
	Force bool `help:"Overwrite existing sync-config and default profile files"`
}

func (c *InitCmd) Help() string {
	return fmt.Sprintf(`Creates %s and %s
with starter content. Skips files that already exist unless --force is set.

Examples:
  # Create default config files
  aipack init

  # Overwrite existing config files
  aipack init --force

  # Use a custom config directory
  aipack init --config-dir /path/to/config

See also: doctor, sync`,
		configPathDisplay("sync-config.yaml"),
		configPathDisplay("profiles", "default.yaml"),
	)
}

func (c *InitCmd) Run(ctx context.Context, g *Globals) error {
	configDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	return app.RunInit(ctx, app.InitRequest{
		ConfigDir: configDir,
		Force:     c.Force,
	}, g.Stdout)
}
