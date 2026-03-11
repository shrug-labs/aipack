package main

import (
	"fmt"
	"os"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
)

type ProfileDeleteCmd struct {
	Name      string `arg:"" help:"Profile name to delete"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
}

func (c *ProfileDeleteCmd) Help() string {
	return `Deletes a profile YAML file from ~/.config/aipack/profiles/. If the deleted
profile is the active profile, the active profile setting is cleared.

Examples:
  # Delete a profile
  aipack profile delete staging

See also: profile create, profile list`
}

func (c *ProfileDeleteCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	if err := app.ProfileDelete(app.ProfileDeleteRequest{
		ConfigDir: cfgDir,
		Name:      c.Name,
	}); err != nil {
		return err
	}

	fmt.Fprintf(g.Stdout, "Deleted profile %q\n", c.Name)
	return nil
}
