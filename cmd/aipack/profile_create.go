package main

import (
	"fmt"
	"os"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
)

type ProfileCreateCmd struct {
	Name      string `arg:"" help:"Profile name to create"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
}

func (c *ProfileCreateCmd) Help() string {
	return `Creates a new empty profile YAML file under ~/.config/aipack/profiles/.
The profile is initialized with the current schema version and an empty
packs list. Use 'pack enable' to add packs to it.

Examples:
  # Create a new profile
  aipack profile create staging

See also: profile delete, profile list, pack enable`
}

func (c *ProfileCreateCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
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
