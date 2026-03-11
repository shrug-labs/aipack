package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/util"
)

type PackCmd struct {
	Create  PackCreateCmd  `cmd:"" help:"Scaffold a new pack directory with pack.json manifest"`
	Install PackInstallCmd `cmd:"" help:"Install a pack from a local directory path or remote URL"`
	Delete  PackDeleteCmd  `cmd:"" help:"Delete an installed pack from the packs directory"`
	Update  PackUpdateCmd  `cmd:"" help:"Update installed pack(s) to latest version from their origin"`
	Add     PackAddCmd     `cmd:"" help:"Add an installed pack to the active profile"`
	Remove  PackRemoveCmd  `cmd:"" help:"Remove a pack from the active profile"`
	List    PackListCmd    `cmd:"" help:"List all installed packs with their install method and origin"`
	Show    PackShowCmd    `cmd:"" help:"Show detailed metadata and content inventory for an installed pack"`
}

func (c *PackCmd) Help() string {
	return `Manage installed packs. Packs are portable, versioned bundles of AI agent
configuration containing rules, agents, workflows, skills, MCP server
definitions, and harness base configs.

Packs are installed under ~/.config/aipack/packs/<name>/.`
}

// --- pack create ---

type PackCreateCmd struct {
	Dir  string `arg:"" help:"Directory path to create the pack in"`
	Name string `help:"Pack name for pack.json (default: directory basename)" name:"name"`
}

func (c *PackCreateCmd) Help() string {
	return `Scaffolds a new pack directory with a pack.json manifest and standard
subdirectories: rules/, agents/, workflows/, skills/, mcp/, configs/.

Examples:
  # Create a new pack
  aipack pack create ./my-new-pack

  # Create with a custom name (overrides directory basename)
  aipack pack create ./path/to/dir --name custom-pack-name

See also: pack install, pack show`
}

func (c *PackCreateCmd) Run(g *Globals) error {
	if err := app.PackCreate(app.PackCreateRequest{Dir: c.Dir, Name: c.Name}); err != nil {
		return err
	}
	fmt.Fprintf(g.Stdout, "Created pack at %s\n", c.Dir)
	return nil
}

// --- pack install ---

type PackInstallCmd struct {
	Path       string `arg:"" optional:"" help:"Local directory path or registry pack name"`
	URL        string `help:"Clone pack from a git-accessible repository or pack.json URL" name:"url"`
	Ref        string `help:"Git ref (branch/tag) to checkout when cloning" name:"ref"`
	Name       string `help:"Override the pack name from pack.json" name:"name"`
	ConfigDir  string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Registry   string `help:"Path to registry YAML file (for registry name lookups)" name:"registry" type:"path"`
	Profile    string `help:"Profile to register pack source in (default: sync-config defaults.profile, then 'default')" name:"profile"`
	NoRegister bool   `help:"Do not auto-register pack as a source in any profile" name:"no-register"`
	Copy       bool   `help:"Copy pack files instead of symlinking (local paths only; not valid with --url)"`
}

func (c *PackInstallCmd) Help() string {
	return `Installs a pack into ~/.config/aipack/packs/<name>/. Local directory packs are
symlinked by default; use --copy to make a full copy instead. URL packs
are cloned via git.

If the argument is not a local directory path and not a URL, it is treated as
a registry pack name. The registry is consulted to resolve the name to a
repository URL (and optional subdirectory path), then the pack is cloned.

By default, the pack is registered as a source in the profile specified by
--profile (or the sync-config default profile, or "default"). Use --no-register
to skip auto-registration.

Examples:
  # Install a local pack via symlink
  aipack pack install ./my-pack

  # Install a local pack via copy with a custom name
  aipack pack install ./my-pack --copy --name custom-name

  # Clone a pack from a URL
  aipack pack install --url https://github.com/org/pack-repo

  # Clone from a specific branch
  aipack pack install --url git@host:org/repo.git --ref feature-branch

  # Install a pack by registry name
  aipack pack install my-team-pack

  # Install a registry pack from a specific branch
  aipack pack install my-team-pack --ref develop

  # Install without registering in any profile
  aipack pack install ./my-pack --no-register

  # Register in a specific profile
  aipack pack install ./my-pack --profile production

See also: pack delete, pack list, pack update, registry search`
}

func (c *PackInstallCmd) Validate() error {
	hasPath := c.Path != ""
	hasURL := c.URL != ""
	if hasURL && hasPath {
		return fmt.Errorf("--url and path argument are mutually exclusive")
	}
	if !hasURL && !hasPath {
		return fmt.Errorf("pack install requires a path argument, --url, or a registry pack name")
	}
	if hasURL && c.Copy {
		return fmt.Errorf("--copy is not valid with --url (URL packs are always cloned)")
	}
	return nil
}

// isRegistryName delegates to cmdutil.IsRegistryName.
var isRegistryName = cmdutil.IsRegistryName

// effectiveProfile returns the profile name to use, loading sync-config defaults
// if the explicit value is empty.
func effectiveProfile(explicit, cfgDir string) string {
	if explicit != "" {
		return explicit
	}
	sc, _ := config.LoadSyncConfig(config.SyncConfigPath(cfgDir))
	return resolveProfileName("", sc)
}

func (c *PackInstallCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	profile := ""
	if !c.NoRegister {
		profile = effectiveProfile(c.Profile, cfgDir)
	}

	req := app.PackAddRequest{
		ConfigDir: cfgDir,
		Name:      c.Name,
		Register:  !c.NoRegister,
		Profile:   profile,
	}
	if c.URL != "" {
		req.URL = c.URL
		req.Ref = c.Ref
	} else if c.Path != "" && isRegistryName(c.Path) {
		// Not a local path — try registry lookup.
		regReq := app.RegistryListRequest{
			ConfigDir:    cfgDir,
			RegistryPath: c.Registry,
		}
		entry, err := app.RegistryLookup(regReq, c.Path)
		if err != nil {
			// Auto-fetch registry and retry once.
			fmt.Fprintln(g.Stderr, "Fetching registry...")
			fetchErr := app.RegistryFetch(app.RegistryFetchRequest{
				ConfigDir: cfgDir,
			}, io.Discard)
			if fetchErr == nil {
				entry, err = app.RegistryLookup(regReq, c.Path)
			}
		}
		if err != nil {
			return fmt.Errorf("registry lookup for %q: %w\n\nHint: use --url for a direct URL install, or check 'aipack registry list'", c.Path, err)
		}
		req.URL = entry.Repo
		req.SubPath = entry.Path
		if c.Ref != "" {
			req.Ref = c.Ref
		} else {
			req.Ref = entry.Ref
		}
		if req.Name == "" {
			req.Name = c.Path // use the registry key as the pack name
		}
	} else {
		req.PackPath = c.Path
		req.Link = !c.Copy
	}

	if err := app.PackAdd(req, g.Stdout); err != nil {
		return err
	}
	return nil
}

// --- pack list ---

type PackListCmd struct {
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	JSON      bool   `help:"Emit machine-readable JSON array" name:"json"`
}

func (c *PackListCmd) Help() string {
	return `Lists all packs installed under ~/.config/aipack/packs/, showing name, install
method (link/copy/clone), version, origin URL, and broken-link status.

Examples:
  # List installed packs
  aipack pack list

  # Machine-readable JSON output
  aipack pack list --json

See also: pack show, pack install`
}

func (c *PackListCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	entries, err := app.PackList(cfgDir)
	if err != nil {
		return err
	}

	if c.JSON {
		type jsonEntry struct {
			Name       string `json:"name"`
			Path       string `json:"path"`
			Method     string `json:"method"`
			Version    string `json:"version,omitempty"`
			Origin     string `json:"origin,omitempty"`
			IsLink     bool   `json:"is_link"`
			BrokenLink bool   `json:"broken_link,omitempty"`
		}
		var out []jsonEntry
		for _, e := range entries {
			isLink := e.Method == "link"
			brokenLink := isLink && !util.PathExists(e.Path)
			out = append(out, jsonEntry{
				Name:       e.Name,
				Path:       e.Path,
				Method:     e.Method,
				Version:    e.Version,
				Origin:     e.Origin,
				IsLink:     isLink,
				BrokenLink: brokenLink,
			})
		}
		if out == nil {
			out = []jsonEntry{}
		}
		return cmdutil.WriteJSON(g.Stdout, out)
	}

	if len(entries) == 0 {
		fmt.Fprintln(g.Stdout, "No packs installed.")
		return nil
	}
	for _, e := range entries {
		ver := ""
		if e.Version != "" {
			ver = " v" + e.Version
		}
		origin := ""
		if e.Origin != "" {
			origin = " (from " + e.Origin + ")"
		}
		broken := ""
		if e.Method == "link" && !util.PathExists(e.Path) {
			broken = " [BROKEN LINK]"
		}
		fmt.Fprintf(g.Stdout, "  %s (%s)%s -> %s%s%s\n", e.Name, e.Method, ver, e.Path, origin, broken)
	}
	return nil
}

// --- pack delete ---

type PackDeleteCmd struct {
	Name      string `arg:"" help:"Name of the installed pack to delete"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
}

func (c *PackDeleteCmd) Help() string {
	return `Deletes an installed pack directory from ~/.config/aipack/packs/<name>/ and
deregisters it from all profiles.

Examples:
  # Delete an installed pack
  aipack pack delete my-pack

See also: pack install, pack list`
}

func (c *PackDeleteCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}
	if err := app.PackRemove(cfgDir, c.Name, g.Stdout); err != nil {
		return err
	}
	return nil
}

// --- pack add (profile) ---

type PackAddCmd struct {
	Name      string `arg:"" help:"Name of the installed pack to add to the profile"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Profile   string `help:"Profile to add the pack to (default: sync-config defaults.profile, then 'default')" name:"profile"`
}

func (c *PackAddCmd) Help() string {
	return `Adds an already-installed pack to the active profile. The pack must be
installed under ~/.config/aipack/packs/<name>/ first (see pack install).

Examples:
  # Add a pack to the default profile
  aipack pack add my-pack

  # Add a pack to a specific profile
  aipack pack add my-pack --profile production

See also: pack remove, pack install, pack list`
}

func (c *PackAddCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	// Verify the pack is installed.
	packsDir := app.PacksDir(cfgDir)
	packDir := filepath.Join(packsDir, c.Name)
	if _, err := os.Stat(packDir); os.IsNotExist(err) {
		return fmt.Errorf("pack %q is not installed (run 'aipack pack install' first)", c.Name)
	}

	if err := app.PackRegister(cfgDir, effectiveProfile(c.Profile, cfgDir), c.Name, g.Stdout); err != nil {
		return err
	}
	return nil
}

// --- pack remove (profile) ---

type PackRemoveCmd struct {
	Name      string `arg:"" help:"Name of the pack to remove from the profile"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	Profile   string `help:"Profile to remove the pack from (default: sync-config defaults.profile, then 'default')" name:"profile"`
}

func (c *PackRemoveCmd) Help() string {
	return `Removes a pack from the active profile. This does NOT delete the pack from
disk — use pack delete for that.

Examples:
  # Remove a pack from the default profile
  aipack pack remove my-pack

  # Remove from a specific profile
  aipack pack remove my-pack --profile production

See also: pack add, pack delete, pack list`
}

func (c *PackRemoveCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	if err := app.PackDeregister(cfgDir, effectiveProfile(c.Profile, cfgDir), c.Name, g.Stdout); err != nil {
		return err
	}
	return nil
}

// --- pack update ---

type PackUpdateCmd struct {
	Name      string `arg:"" optional:"" help:"Name of the pack to update"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	All       bool   `help:"Update all installed packs" name:"all"`
}

func (c *PackUpdateCmd) Help() string {
	return `Updates installed pack(s) to the latest version from their origin. For
git-cloned packs, runs git pull. For symlinked packs, re-validates the link
target. For copied packs, re-copies from the recorded origin.

Exactly one of <name> or --all is required.

Examples:
  # Update a specific pack
  aipack pack update my-pack

  # Update all installed packs
  aipack pack update --all

See also: pack install, pack show`
}

func (c *PackUpdateCmd) Validate() error {
	if c.Name != "" && c.All {
		return fmt.Errorf("<name> and --all are mutually exclusive")
	}
	if c.Name == "" && !c.All {
		return fmt.Errorf("pack update requires a name argument or --all")
	}
	return nil
}

func (c *PackUpdateCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	results, err := app.PackUpdate(app.PackUpdateRequest{
		ConfigDir: cfgDir,
		Name:      c.Name,
		All:       c.All,
	}, g.Stdout)
	if err != nil {
		return err
	}

	hasError := false
	for _, r := range results {
		if r.Status == "error" {
			hasError = true
		}
	}
	if hasError {
		return ExitError{Code: cmdutil.ExitFail}
	}
	return nil
}

// --- pack show ---

type PackShowCmd struct {
	Name      string `arg:"" help:"Name of the installed pack to show"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	JSON      bool   `help:"Emit machine-readable JSON" name:"json"`
}

func (c *PackShowCmd) Help() string {
	return `Displays detailed metadata for an installed pack: name, version, path, install
method, origin URL, git ref, install timestamp, and content inventory (rules,
agents, workflows, skills, MCP servers).

Examples:
  # Show pack details
  aipack pack show my-pack

  # Machine-readable JSON output
  aipack pack show my-pack --json

See also: pack list, validate`
}

func (c *PackShowCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
	if err != nil {
		return err
	}

	entry, err := app.PackShow(cfgDir, c.Name)
	if err != nil {
		return err
	}

	if c.JSON {
		return cmdutil.WriteJSON(g.Stdout, entry)
	}

	fmt.Fprintf(g.Stdout, "Name:        %s\n", entry.Name)
	fmt.Fprintf(g.Stdout, "Version:     %s\n", entry.Version)
	fmt.Fprintf(g.Stdout, "Path:        %s\n", entry.Path)
	fmt.Fprintf(g.Stdout, "Method:      %s\n", entry.Method)
	if entry.Origin != "" {
		fmt.Fprintf(g.Stdout, "Origin:      %s\n", entry.Origin)
	}
	if entry.Ref != "" {
		fmt.Fprintf(g.Stdout, "Ref:         %s\n", entry.Ref)
	}
	if entry.CommitHash != "" {
		fmt.Fprintf(g.Stdout, "Commit:      %s\n", entry.CommitHash)
	}
	if entry.InstalledAt != "" {
		fmt.Fprintf(g.Stdout, "Installed:   %s\n", entry.InstalledAt)
	}
	if len(entry.Rules) > 0 {
		fmt.Fprintf(g.Stdout, "Rules:       %s\n", joinComma(entry.Rules))
	}
	if len(entry.Agents) > 0 {
		fmt.Fprintf(g.Stdout, "Agents:      %s\n", joinComma(entry.Agents))
	}
	if len(entry.Workflows) > 0 {
		fmt.Fprintf(g.Stdout, "Workflows:   %s\n", joinComma(entry.Workflows))
	}
	if len(entry.Skills) > 0 {
		fmt.Fprintf(g.Stdout, "Skills:      %s\n", joinComma(entry.Skills))
	}
	if len(entry.MCPServers) > 0 {
		fmt.Fprintf(g.Stdout, "MCP:         %s\n", joinComma(entry.MCPServers))
	}
	return nil
}

func joinComma(items []string) string {
	return strings.Join(items, ", ")
}
