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
	List    RegistryListCmd    `cmd:"" help:"List all packs available in the registry"`
	Fetch   RegistryFetchCmd   `cmd:"" help:"Fetch remote registry sources and cache them locally"`
	Sources RegistrySourcesCmd `cmd:"" help:"List configured registry sources"`
	Remove  RegistryRemoveCmd  `cmd:"" help:"Remove a registry source"`
}

func (c *RegistryCmd) Help() string {
	return `Browse and manage pack registry sources. The registry maps pack names to
source repositories, enabling discovery and installation.

The registry view merges cached remote sources in ~/.config/aipack/registries/
in source order (first-seen wins for name conflicts).

Common workflow:
  aipack registry fetch <url>     # add and fetch a source
  aipack registry list            # see available packs
  aipack pack install <name>      # install a pack by name`
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
  # List all registry packs (from cached sources)
  aipack registry list

  # Use a specific registry file (single-file mode)
  aipack registry list --registry /path/to/registry.yaml

  # Machine-readable JSON output
  aipack registry list --json

See also: registry fetch, pack install`
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

// --- registry fetch ---

type RegistryFetchCmd struct {
	URL       string `arg:"" optional:"" help:"URL to fetch registry from (git repo or HTTP)"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Ref       string `help:"Git ref (branch/tag) — implies git-based fetch" name:"ref"`
	Path      string `help:"File path within git repo (default: registry.yaml)" name:"path"`
	Name      string `help:"Source name for caching (default: derived from URL)" name:"name"`
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
  - git@host:path or ssh:// → git mode
  - --ref provided → git mode
  - Otherwise → HTTP GET

Examples:
  # Fetch from a git repo (HTTPS)
  aipack registry fetch https://bitbucket.example.com/scm/TEAM/tools.git

  # Fetch from a git repo (SSH — avoids HTTPS credential prompts)
  aipack registry fetch git@bitbucket.example.com:TEAM/tools.git

  # Fetch with explicit ref and path
  aipack registry fetch https://bitbucket.example.com/scm/TEAM/tools.git \
    --ref team/ai-runbooks --path ai-runbooks/registry.yaml

  # Fetch from an HTTP URL
  aipack registry fetch https://example.com/registry.yaml

  # Fetch all configured sources
  aipack registry fetch

See also: registry list, registry sources`
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
  # List configured sources
  aipack registry sources

  # Remove a source by name
  aipack registry remove my-tools

See also: registry sources, registry fetch`
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

// --- registry sources ---

type RegistrySourcesCmd struct {
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	JSON      bool   `help:"Emit machine-readable JSON array" name:"json"`
}

func (c *RegistrySourcesCmd) Help() string {
	return `Lists all configured registry sources from sync-config, showing name, URL,
git ref, and whether a cached copy exists.

Examples:
  # List configured sources
  aipack registry sources

  # Machine-readable JSON output
  aipack registry sources --json

See also: registry fetch, registry remove`
}

func (c *RegistrySourcesCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	sources, err := app.RegistrySources(cfgDir)
	if err != nil {
		return err
	}

	if c.JSON {
		if sources == nil {
			sources = []app.RegistrySourceInfo{}
		}
		return cmdutil.WriteJSON(g.Stdout, sources)
	}

	if len(sources) == 0 {
		fmt.Fprintln(g.Stdout, "No registry sources configured.")
		fmt.Fprintln(g.Stdout, "Add one with: aipack registry fetch <url>")
		return nil
	}

	for _, src := range sources {
		status := "not fetched"
		if src.Cached {
			status = "cached"
		}
		fmt.Fprintf(g.Stdout, "  %s (%s)\n", src.Name, status)
		details := []string{src.URL}
		if src.Ref != "" {
			details = append(details, "ref: "+src.Ref)
		}
		if src.Path != "" {
			details = append(details, "path: "+src.Path)
		}
		fmt.Fprintf(g.Stdout, "    %s\n", strings.Join(details, ", "))
	}
	return nil
}

func printRegistryResults(g *Globals, results []app.RegistrySearchResult) {
	for _, r := range results {
		installed := ""
		if r.Installed {
			installed = " [installed]"
		}
		desc := ""
		if r.Description != "" {
			desc = " — " + r.Description
		}
		fmt.Fprintf(g.Stdout, "  %s%s%s\n", r.Name, installed, desc)
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
