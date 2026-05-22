package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

type CollectionCmd struct {
	List    CollectionListCmd    `cmd:"" help:"List installable pack collections from the registry"`
	Show    CollectionShowCmd    `cmd:"" help:"Show a registry collection's pack recipe"`
	Install CollectionInstallCmd `cmd:"" help:"Install all packs in a registry collection"`
}

func (c *CollectionCmd) Help() string {
	return `Install curated registry-defined pack collections. A collection is an
ordered recipe of registry pack names, refs, and bundled-content choices.

Collections come from fetched registry sources; profiles still control which
installed packs are active for a harness context.

Examples:
  aipack collection list
  aipack collection show team-dev
  aipack collection install team-dev --add -w all`
}

type CollectionListCmd struct {
	Registry string `help:"Path to registry YAML file (single-file mode)" name:"registry" type:"path"`
	JSON     bool   `help:"Emit machine-readable JSON array" name:"json"`
}

func (c *CollectionListCmd) Help() string {
	return `Lists collections from the merged registry view.

Examples:
  aipack collection list
  aipack collection list --registry ./registry.yaml
  aipack collection list --json

See also: collection show, collection install, registry fetch`
}

func (c *CollectionListCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	results, err := app.RegistryCollectionList(app.RegistryListRequest{
		ConfigDir:    cfgDir,
		RegistryPath: c.Registry,
	})
	if err != nil {
		return err
	}
	if c.JSON {
		if results == nil {
			results = []app.RegistryCollectionResult{}
		}
		return cmdutil.WriteJSON(g.Stdout, results)
	}
	if len(results) == 0 {
		fmt.Fprintln(g.Stdout, "No collections in registry.")
		return nil
	}
	for _, result := range results {
		fmt.Fprintf(g.Stdout, "%s", result.Name)
		if result.Description != "" {
			fmt.Fprintf(g.Stdout, " — %s", result.Description)
		}
		fmt.Fprintf(g.Stdout, "\n  packs: %s\n", collectionPackSummary(result.Packs))
	}
	return nil
}

type CollectionShowCmd struct {
	Name     string `arg:"" help:"Collection name"`
	Registry string `help:"Path to registry YAML file (single-file mode)" name:"registry" type:"path"`
	JSON     bool   `help:"Emit machine-readable JSON" name:"json"`
}

func (c *CollectionShowCmd) Help() string {
	return `Shows one registry collection's ordered pack recipe.

Examples:
  aipack collection show team-dev
  aipack collection show team-dev --json

See also: collection list, collection install`
}

func (c *CollectionShowCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	result, err := app.RegistryCollectionLookup(app.RegistryListRequest{
		ConfigDir:    cfgDir,
		RegistryPath: c.Registry,
	}, c.Name)
	if err != nil {
		return err
	}
	if c.JSON {
		return cmdutil.WriteJSON(g.Stdout, app.RegistryCollectionResult{
			Name:               c.Name,
			RegistryCollection: result,
		})
	}
	fmt.Fprintf(g.Stdout, "Name: %s\n", c.Name)
	if result.Description != "" {
		fmt.Fprintf(g.Stdout, "Description: %s\n", result.Description)
	}
	fmt.Fprintln(g.Stdout, "Packs:")
	for _, pack := range result.Packs {
		fmt.Fprintf(g.Stdout, "  - %s", pack.Name)
		if pack.Ref != "" {
			fmt.Fprintf(g.Stdout, " @ %s", pack.Ref)
		}
		if len(pack.With) > 0 {
			fmt.Fprintf(g.Stdout, " (with: %s)", strings.Join(pack.With, ", "))
		}
		fmt.Fprintln(g.Stdout)
	}
	return nil
}

type CollectionInstallCmd struct {
	Name     string   `arg:"" help:"Collection name"`
	Registry string   `help:"Path to registry YAML file (single-file mode)" name:"registry" type:"path"`
	Profile  string   `help:"Profile to add packs to (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
	Add      bool     `help:"Add installed packs to the active profile after installing" name:"add"`
	With     []string `help:"Override bundled content acceptance for every pack: profiles(p), registries(r), extras(e), all" short:"w" name:"with" sep:","`
}

func (c *CollectionInstallCmd) Help() string {
	return `Installs every pack referenced by a registry collection.

By default, packs are installed to disk but not added to a profile. Use --add
to add every installed pack to the active profile, or --profile to target a
specific profile. Use -w/--with to override bundled-content choices for every
pack in the collection.

Examples:
  aipack collection install team-dev
  aipack collection install team-dev --add
  aipack collection install team-dev --add -w all

See also: collection show, pack install`
}

func (c *CollectionInstallCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	profile := ""
	if c.Add {
		profile = effectiveProfile(c.Profile, cfgDir)
	}
	with, err := parseWithFlag(c.With)
	if err != nil {
		return err
	}
	req := app.CollectionInstallRequest{
		ConfigDir:    cfgDir,
		RegistryPath: c.Registry,
		Name:         c.Name,
		Add:          c.Add,
		Profile:      profile,
		With:         with,
	}
	if err := c.runCollectionInstall(ctx, g, cfgDir, req); err != nil {
		return err
	}
	return finishPackContentChange(ctx, g, cfgDir, profile, c.Add)
}

func (c *CollectionInstallCmd) runCollectionInstall(ctx context.Context, g *Globals, cfgDir string, req app.CollectionInstallRequest) error {
	if _, err := app.CollectionInstall(ctx, req, g.Stdout); err != nil {
		var notFound app.RegistryCollectionNotFoundError
		if c.Registry != "" || !errors.As(err, &notFound) {
			return err
		}
		fmt.Fprintln(g.Stderr, "Fetching registry...")
		if fetchErr := app.RegistryFetch(ctx, app.RegistryFetchRequest{ConfigDir: cfgDir}, nil); fetchErr != nil {
			return err
		}
		_, retryErr := app.CollectionInstall(ctx, req, g.Stdout)
		return retryErr
	}
	return nil
}

func collectionPackSummary(packs []config.RegistryCollectionPack) string {
	if len(packs) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(packs))
	for _, pack := range packs {
		label := pack.Name
		if pack.Ref != "" {
			label += "@" + pack.Ref
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}
