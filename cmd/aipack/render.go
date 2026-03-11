package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
)

type RenderCmd struct {
	Profile     string `help:"Profile name (default: sync-config defaults.profile, then 'default')" name:"profile"`
	ProfilePath string `help:"Direct path to a profile YAML file (overrides --profile)" name:"profile-path" type:"path"`
	ConfigDir   string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	OutDir      string `help:"Output directory (default: auto-generated temporary directory under $TMPDIR)" name:"out-dir" type:"path"`
}

func (c *RenderCmd) Help() string {
	return `Resolves the profile and renders all pack content (rules, agents, workflows,
skills, MCP configs) into a self-contained output directory. The output is
harness-independent — it contains merged pack content without targeting any
specific harness's file layout. Prints the output directory path to stdout.

Examples:
  # Render to an auto-generated temp directory
  aipack render --profile default

  # Render to a specific directory
  aipack render --profile default --out-dir ./rendered-output

  # Render using a direct profile file path
  aipack render --profile-path /path/to/profile.yaml --out-dir ./out

See also: sync, pack show`
}

func (c *RenderCmd) Run(g *Globals) error {
	loaded, exitCode := loadProfile(c.Profile, c.ProfilePath, c.ConfigDir, g.Stderr)
	if exitCode >= 0 {
		return ExitError{Code: exitCode}
	}

	cmdutil.PrintWarnings(g.Stderr, loaded.warnings)

	out := strings.TrimSpace(c.OutDir)
	if out == "" {
		var err error
		out, err = os.MkdirTemp("", "aipack-render-")
		if err != nil {
			return err
		}
	} else {
		var err error
		out, err = filepath.Abs(out)
		if err != nil {
			return err
		}
	}

	if err := app.RunRender(loaded.profile, out, g.Registry); err != nil {
		return err
	}
	fmt.Fprintln(g.Stdout, out)
	return nil
}
