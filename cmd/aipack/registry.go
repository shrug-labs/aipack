package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

type RegistryCmd struct {
	List     RegistryListCmd     `cmd:"" help:"List all packs available in the registry"`
	Fetch    RegistryFetchCmd    `cmd:"" help:"Fetch remote registry sources and cache them locally"`
	Sources  RegistrySourcesCmd  `cmd:"" help:"List configured registry sources"`
	Delete   RegistryDeleteCmd   `cmd:"" help:"Delete a registry source"`
	Validate RegistryValidateCmd `cmd:"" help:"Validate a registry YAML file"`
}

func (c *RegistryCmd) Help() string {
	return fmt.Sprintf(`Browse and manage pack registry sources. The registry maps pack names to
source repositories, enabling discovery and installation.

The registry view merges cached remote sources in %s
in source order (first-seen wins for name conflicts).

Common workflow:
  aipack registry fetch <url>     # add and fetch a source
  aipack registry validate ./registry.yaml
  aipack registry list            # see available packs
  aipack pack install <name>      # install a pack by name`,
		configPathDisplay("registries"),
	)
}

// --- registry validate ---

type RegistryValidateCmd struct {
	Path string `arg:"" help:"Registry YAML file to validate" type:"path"`
	JSON bool   `help:"Emit machine-readable JSON" name:"json"`
}

func (c *RegistryValidateCmd) Help() string {
	return `Validates a registry YAML file's schema and pack source entries.

Examples:
  aipack registry validate ./registry.yaml
  aipack registry validate ./registry.yaml --json

See also: registry fetch, registry list`
}

func (c *RegistryValidateCmd) Run(ctx context.Context, g *Globals) error {
	result, err := app.RegistryValidate(c.Path)
	if err != nil {
		return err
	}
	if c.JSON {
		if result.Errors == nil {
			result.Errors = []string{}
		}
		if err := cmdutil.WriteJSON(g.Stdout, result); err != nil {
			return err
		}
		if !result.Valid {
			return ExitError{Code: cmdutil.ExitFail}
		}
		return nil
	}
	if result.Valid {
		fmt.Fprintf(g.Stdout, "Registry OK: %s\n", result.Path)
		return nil
	}
	fmt.Fprintf(g.Stdout, "Registry invalid: %s\n", result.Path)
	for _, msg := range result.Errors {
		fmt.Fprintf(g.Stdout, "  - %s\n", msg)
	}
	return ExitError{Code: cmdutil.ExitFail}
}

// --- registry list ---

type RegistryListCmd struct {
	Registry string `help:"Path to registry YAML file (single-file mode)" name:"registry" type:"path"`
	JSON     bool   `help:"Emit machine-readable JSON array" name:"json"`
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

func (c *RegistryListCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
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
	URL  string `arg:"" optional:"" help:"URL to fetch registry from (git repo or HTTP)"`
	Ref  string `help:"Git ref (branch/tag) — implies git-based fetch" name:"ref"`
	Path string `help:"File path within git repo (default: registry.yaml)" name:"path"`
	Name string `help:"Source name for caching (default: derived from URL)" name:"name"`
	Deep bool   `help:"Clone each registry pack and index resource-level frontmatter for search" name:"deep"`
}

func (c *RegistryFetchCmd) Help() string {
	return fmt.Sprintf(`Fetches a remote registry and caches it locally. Each source is cached as a
separate file in %s and saved to sync-config for
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
    --ref team/ops-tools --path ops-tools/registry.yaml

  # Fetch from an HTTP URL
  aipack registry fetch https://example.com/registry.yaml

  # Fetch all configured sources
  aipack registry fetch

See also: registry list, registry sources`,
		configPathDisplay("registries"),
	)
}

func (c *RegistryFetchCmd) Run(ctx context.Context, g *Globals) error {
	// Validate: --path requires git mode.
	if c.Path != "" && c.URL != "" && !config.IsGitURL(c.URL, c.Ref) {
		return fmt.Errorf("--path requires a git URL (ending in .git) or --ref")
	}

	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	if err := app.RegistryFetch(ctx, app.RegistryFetchRequest{
		ConfigDir: cfgDir,
		URL:       c.URL,
		Ref:       c.Ref,
		Path:      c.Path,
		Name:      c.Name,
	}, g.Stdout); err != nil {
		return err
	}

	if c.Deep {
		return app.RegistryDeepIndex(ctx, app.RegistryDeepIndexRequest{
			ConfigDir: cfgDir,
		}, g.Stdout)
	}
	return nil
}

// --- registry delete ---

type RegistryDeleteCmd struct {
	Name string `arg:"" help:"Name of the registry source to delete" predictor:"registry-source"`
}

func (c *RegistryDeleteCmd) Help() string {
	return `Deletes a registry source from sync-config and removes its cached file.

Examples:
  # List configured sources
  aipack registry sources

  # Delete a source by name
  aipack registry delete my-tools

See also: registry sources, registry fetch`
}

func (c *RegistryDeleteCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	return app.RegistryDelete(app.RegistryDeleteRequest{
		ConfigDir: cfgDir,
		Name:      c.Name,
	}, g.Stdout)
}

// --- registry sources ---

type RegistrySourcesCmd struct {
	JSON bool `help:"Emit machine-readable JSON array" name:"json"`
}

func (c *RegistrySourcesCmd) Help() string {
	return `Lists all configured registry sources from sync-config, showing name, URL,
git ref, and whether a cached copy exists.

Examples:
  # List configured sources
  aipack registry sources

  # Machine-readable JSON output
  aipack registry sources --json

See also: registry fetch, registry delete`
}

func (c *RegistrySourcesCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
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
	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(g.Stdout)
		}
		status := ""
		if r.Installed {
			status = " [installed]"
		}
		fmt.Fprintf(g.Stdout, "Name:        %s%s\n", r.Name, status)
		if r.Description != "" {
			fmt.Fprintf(g.Stdout, "Description: %s\n", r.Description)
		}
		if r.Owner != "" {
			fmt.Fprintf(g.Stdout, "Owner:       %s\n", r.Owner)
		}
		if r.Method != "" {
			fmt.Fprintf(g.Stdout, "Method:      %s\n", r.Method)
		}
		if r.Method == config.MethodArchive {
			fmt.Fprintf(g.Stdout, "URL:         %s\n", r.URL)
		} else {
			fmt.Fprintf(g.Stdout, "Repo:        %s\n", r.Repo)
		}
		if r.Path != "" {
			fmt.Fprintf(g.Stdout, "Path:        %s\n", r.Path)
		}
		if r.Ref != "" {
			fmt.Fprintf(g.Stdout, "Ref:         %s\n", r.Ref)
		}
		if r.Contact != "" {
			fmt.Fprintf(g.Stdout, "Contact:     %s\n", r.Contact)
		}
		if r.Provenance != nil {
			fmt.Fprintf(g.Stdout, "Source:      %s\n", r.Provenance.Winner.Name)
		}
		if r.Provenance != nil && len(r.Provenance.Shadowed) > 0 {
			names := make([]string, len(r.Provenance.Shadowed))
			for i, source := range r.Provenance.Shadowed {
				names[i] = source.Name
			}
			fmt.Fprintf(g.Stdout, "Shadowed:    %s\n", strings.Join(names, ", "))
		}
	}
}
