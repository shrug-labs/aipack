package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
)

type RegistryCmd struct {
	List   RegistryListCmd   `cmd:"" help:"List all packs available in the registry"`
	Search RegistrySearchCmd `cmd:"" help:"Search for packs by name or description"`
	Fetch  RegistryFetchCmd  `cmd:"" help:"Fetch and merge a remote registry into the local registry"`
}

func (c *RegistryCmd) Help() string {
	return `Browse and search the pack registry. The registry is a YAML file that maps
pack names to their source repositories, enabling discovery and installation.

By default, the registry is loaded from ~/.config/aipack/registry.yaml.
Use --registry to override with a different file path.`
}

// --- registry list ---

type RegistryListCmd struct {
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Registry  string `help:"Path to registry YAML file" name:"registry" type:"path"`
	JSON      bool   `help:"Emit machine-readable JSON array" name:"json"`
}

func (c *RegistryListCmd) Help() string {
	return `Lists all packs available in the registry with their name, description,
source repo, and subdirectory path (if applicable).

Examples:
  # List all registry packs
  aipack registry list

  # Use a specific registry file
  aipack registry list --registry /path/to/registry.yaml

  # Machine-readable JSON output
  aipack registry list --json

See also: registry search, pack add`
}

func (c *RegistryListCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	results, err := app.RegistryList(app.RegistryListRequest{
		ConfigDir:    cfgDir,
		RegistryPath: c.Registry,
	})
	if err != nil {
		return err
	}

	if c.JSON {
		if results == nil {
			results = []app.RegistrySearchResult{}
		}
		return cmdutil.WriteJSON(g.Stdout, results)
	}

	if len(results) == 0 {
		fmt.Fprintln(g.Stdout, "No packs in registry.")
		return nil
	}
	printRegistryResults(g, results)
	return nil
}

// --- registry search ---

type RegistrySearchCmd struct {
	Query     string `arg:"" help:"Search term to match against pack names and descriptions"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Registry  string `help:"Path to registry YAML file" name:"registry" type:"path"`
	JSON      bool   `help:"Emit machine-readable JSON array" name:"json"`
}

func (c *RegistrySearchCmd) Help() string {
	return `Searches the registry for packs whose name or description contains the
given query (case-insensitive substring match).

Examples:
  # Search for packs related to "api"
  aipack registry search api

  # Search with JSON output
  aipack registry search openshift --json

See also: registry list, pack add`
}

func (c *RegistrySearchCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	results, err := app.RegistrySearch(app.RegistryListRequest{
		ConfigDir:    cfgDir,
		RegistryPath: c.Registry,
	}, c.Query)
	if err != nil {
		return err
	}

	if c.JSON {
		if results == nil {
			results = []app.RegistrySearchResult{}
		}
		return cmdutil.WriteJSON(g.Stdout, results)
	}

	if len(results) == 0 {
		fmt.Fprintf(g.Stdout, "No packs matching %q.\n", c.Query)
		return nil
	}
	printRegistryResults(g, results)
	return nil
}

// --- registry fetch ---

type RegistryFetchCmd struct {
	URL       string `arg:"" optional:"" help:"URL to fetch registry YAML from (default: sync-config defaults.registry_url)"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Registry  string `help:"Path to local registry YAML file to merge into" name:"registry" type:"path"`
	Prune     bool   `help:"Remove local entries not present in the fetched registry" name:"prune"`
	Deep      bool   `help:"Clone each registry pack and index resource-level frontmatter for search" name:"deep"`
}

func (c *RegistryFetchCmd) Help() string {
	return `Fetches a registry YAML from a remote URL and merges its entries into the
local registry. Existing entries are preserved (never overwritten). With --prune,
local entries not present in the fetched registry are removed.

Source resolution order:
  1. Explicit URL argument (fetched via HTTP)
  2. defaults.registry_url in sync-config (fetched via HTTP)
  3. Compiled-in default (fetched via git clone, uses your git credentials)

Examples:
  # Fetch from an explicit URL
  aipack registry fetch https://example.com/registry.yaml

  # Fetch using the URL configured in sync-config
  aipack registry fetch

  # Fetch and remove entries no longer in the remote registry
  aipack registry fetch --prune

See also: registry list, registry search`
}

func (c *RegistryFetchCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	if err := app.RegistryFetch(app.RegistryFetchRequest{
		ConfigDir:    cfgDir,
		RegistryPath: c.Registry,
		URL:          c.URL,
		Prune:        c.Prune,
	}, g.Stdout); err != nil {
		return err
	}

	if c.Deep {
		return app.RegistryDeepIndex(app.RegistryDeepIndexRequest{
			ConfigDir:    cfgDir,
			RegistryPath: c.Registry,
		}, g.Stdout)
	}
	return nil
}

func printRegistryResults(g *Globals, results []app.RegistrySearchResult) {
	for _, r := range results {
		desc := ""
		if r.Description != "" {
			desc = " — " + r.Description
		}
		fmt.Fprintf(g.Stdout, "  %s%s\n", r.Name, desc)
		details := []string{r.Repo}
		if r.Path != "" {
			details = append(details, "path: "+r.Path)
		}
		if r.Ref != "" {
			details = append(details, "ref: "+r.Ref)
		}
		if r.Owner != "" {
			details = append(details, "owner: "+r.Owner)
		}
		fmt.Fprintf(g.Stdout, "    %s\n", strings.Join(details, ", "))
	}
}
