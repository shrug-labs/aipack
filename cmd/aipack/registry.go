package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

type RegistryCmd struct {
	List   RegistryListCmd   `cmd:"" help:"List all packs available in the registry"`
	Search RegistrySearchCmd `cmd:"" help:"Search for packs by name or description"`
	Fetch  RegistryFetchCmd  `cmd:"" help:"Fetch remote registry sources and cache them locally"`
	Remove RegistryRemoveCmd `cmd:"" help:"Remove a registry source"`
}

func (c *RegistryCmd) Help() string {
	return `Browse, search, and manage pack registry sources. The registry maps pack
names to source repositories, enabling discovery and installation.

The unified registry view merges:
  1. Local entries from ~/.config/aipack/registry.yaml (highest priority)
  2. Cached remote sources in ~/.config/aipack/registries/ (in source order)`
}

// --- registry list ---

type RegistryListCmd struct {
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Registry  string `help:"Path to registry YAML file (single-file mode)" name:"registry" type:"path"`
	JSON      bool   `help:"Emit machine-readable JSON array" name:"json"`
}

func (c *RegistryListCmd) Help() string {
	return `Lists all packs available in the registry with their name, description,
source repo, and subdirectory path (if applicable).

Examples:
  # List all registry packs (local + cached sources)
  aipack registry list

  # Use a specific registry file (single-file mode)
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
	Registry  string `help:"Path to registry YAML file (single-file mode)" name:"registry" type:"path"`
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
	URL       string `arg:"" optional:"" help:"URL to fetch registry from (git repo or HTTP)"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Ref       string `help:"Git ref (branch/tag) — implies git-based fetch" name:"ref"`
	Path      string `help:"File path within git repo (default: registry.yaml)" name:"path"`
	Name      string `help:"Source name for caching (default: derived from URL)" name:"name"`
	Prune     bool   `help:"Deprecated: cached registries are always kept in sync" name:"prune"`
	Deep      bool   `help:"Clone each registry pack and index resource-level frontmatter for search" name:"deep"`
}

func (c *RegistryFetchCmd) Help() string {
	return `Fetches a remote registry and caches it locally. Each source is cached as a
separate file in ~/.config/aipack/registries/ and saved to sync-config for
future fetches.

With an explicit URL, fetches that single source. Without a URL, fetches all
sources in registry_sources (or the compiled-in default).

Git detection:
  - URL ending in .git → git mode (defaults: ref=main, path=registry.yaml)
  - --ref provided → git mode
  - Otherwise → HTTP GET

Examples:
  # Fetch from a git repo (auto-detected from .git suffix)
  aipack registry fetch https://bitbucket.example.com/scm/TEAM/tools.git

  # Fetch from a git repo with explicit ref and path
  aipack registry fetch https://bitbucket.example.com/scm/TEAM/tools.git \
    --ref team/ai-runbooks --path ai-runbooks/registry.yaml

  # Fetch from an HTTP URL
  aipack registry fetch https://example.com/registry.yaml

  # Fetch all configured sources
  aipack registry fetch

See also: registry list, registry search, registry remove`
}

func (c *RegistryFetchCmd) Run(g *Globals) error {
	// Validate: --path requires git mode.
	if c.Path != "" && c.URL != "" && !config.IsGitURL(c.URL, c.Ref) {
		return fmt.Errorf("--path requires a git URL (ending in .git) or --ref")
	}

	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	if err := app.RegistryFetch(app.RegistryFetchRequest{
		ConfigDir: cfgDir,
		URL:       c.URL,
		Ref:       c.Ref,
		Path:      c.Path,
		Name:      c.Name,
		Prune:     c.Prune,
	}, g.Stdout); err != nil {
		return err
	}

	if c.Deep {
		return app.RegistryDeepIndex(app.RegistryDeepIndexRequest{
			ConfigDir: cfgDir,
		}, g.Stdout)
	}
	return nil
}

// --- registry remove ---

type RegistryRemoveCmd struct {
	Name      string `arg:"" help:"Name of the registry source to remove"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
}

func (c *RegistryRemoveCmd) Help() string {
	return `Removes a registry source from sync-config and deletes its cached file.

Examples:
  # Remove a source
  aipack registry remove ocm-ops-tools

  # List sources to see available names
  aipack registry fetch  (sources are shown in output)

See also: registry fetch, registry list`
}

func (c *RegistryRemoveCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	return app.RegistryRemove(app.RegistryRemoveRequest{
		ConfigDir: cfgDir,
		Name:      c.Name,
	}, g.Stdout)
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
