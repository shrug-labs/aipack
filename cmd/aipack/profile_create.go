package main

import (
	"context"
	"fmt"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

type ProfileCreateCmd struct {
	Name string `arg:"" help:"Profile name to create"`
}

func (c *ProfileCreateCmd) Help() string {
	return fmt.Sprintf(`Creates a new empty profile YAML file under %s.
The profile is initialized with the current schema version and an empty
packs list. Use 'pack enable' to add packs to it.

Examples:
  # Create a new profile
  aipack profile create staging

See also: profile delete, profile list, pack enable`,
		configPathDisplay("profiles"),
	)
}

func (c *ProfileCreateCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	if err := app.ProfileCreate(app.ProfileCreateRequest{
		ConfigDir: cfgDir,
		Name:      c.Name,
	}); err != nil {
		return err
	}

	fmt.Fprintf(g.Stdout, "Created profile %q\n", c.Name)
	return nil
}
