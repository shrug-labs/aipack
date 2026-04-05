package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/util"
)

type PackCmd struct {
	Create   PackCreateCmd  `cmd:"" help:"Scaffold a new pack directory with pack.json manifest"`
	Install  PackInstallCmd `cmd:"" help:"Install a pack from a local directory path or remote URL"`
	Delete   PackDeleteCmd  `cmd:"" help:"Delete an installed pack from the packs directory"`
	Update   PackUpdateCmd  `cmd:"" help:"Update installed pack(s) to latest version from their origin"`
	Rename   PackRenameCmd  `cmd:"" help:"Rename an installed pack across all config"`
	Enable   PackEnableCmd  `cmd:"" help:"Enable an installed pack in the active profile"`
	Disable  PackDisableCmd `cmd:"" help:"Disable a pack in the active profile (does not delete from disk)"`
	List     PackListCmd    `cmd:"" help:"List all installed packs with their install method and origin"`
	Show     PackShowCmd    `cmd:"" help:"Show detailed metadata and content inventory for an installed pack"`
	Validate ValidateCmd    `cmd:"" help:"Validate a pack source tree"`
}

func (c *PackCmd) Help() string {
	return fmt.Sprintf(`Manage installed packs. Packs are portable, versioned bundles of AI agent
configuration containing rules, agents, workflows, skills, MCP server
definitions, and harness base configs.

Packs are installed under %s.`,
		configPathDisplay("packs", "<name>"),
	)
}

// withAliases maps single-letter shorthands to bundled categories.
var withAliases = map[string]domain.BundledCategory{
	"p": domain.BundledProfiles,
	"r": domain.BundledRegistries,
	"e": domain.BundledExtras,
}

// withUsageHint lists valid --with values for error messages.
var withUsageHint = func() string {
	var parts []string
	for _, c := range domain.AllBundledCategories {
		var s strings.Builder
		s.WriteString(string(c))
		for alias, cat := range withAliases {
			if cat == c {
				s.WriteString("(" + alias + ")")
				break
			}
		}
		parts = append(parts, s.String())
	}
	return strings.Join(append(parts, "all"), ", ")
}()

// parseWithFlag normalizes and validates --with/-w flag values, returning a
// BundledSet. Returns nil when the flag was not provided (empty input).
func parseWithFlag(vals []string) (domain.BundledSet, error) {
	if len(vals) == 0 {
		return nil, nil
	}
	cats := make([]domain.BundledCategory, 0, len(vals))
	for _, v := range vals {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "all" {
			return domain.BundledAll(), nil
		}
		if alias, ok := withAliases[v]; ok {
			cats = append(cats, alias)
			continue
		}
		cat, ok := domain.ParseBundledCategory(v)
		if !ok {
			return nil, fmt.Errorf("invalid --with value %q; valid: %s", v, withUsageHint)
		}
		cats = append(cats, cat)
	}
	return domain.NewBundledSet(cats...), nil
}

// buildContentPaths builds a content type map from CLI flag values.
// Returns nil when all inputs are empty.
func buildContentPaths(rules, skills, agents, workflows, prompts string) map[domain.PackCategory]string {
	pairs := []struct {
		cat domain.PackCategory
		val string
	}{
		{domain.CategoryRules, rules},
		{domain.CategorySkills, skills},
		{domain.CategoryAgents, agents},
		{domain.CategoryWorkflows, workflows},
		{domain.CategoryPrompts, prompts},
	}
	var cp map[domain.PackCategory]string
	for _, p := range pairs {
		if p.val != "" {
			if cp == nil {
				cp = map[domain.PackCategory]string{}
			}
			cp[p.cat] = p.val
		}
	}
	return cp
}

// --- pack create ---

type PackCreateCmd struct {
	Name      string `arg:"" help:"Pack name"`
	Local     bool   `help:"Create pack inside the packs directory instead of the current directory" name:"local"`
	Rules     string `help:"Symlink rules/ to this directory" name:"rules" type:"path"`
	Skills    string `help:"Symlink skills/ to this directory" name:"skills" type:"path"`
	Agents    string `help:"Symlink agents/ to this directory" name:"agents" type:"path"`
	Workflows string `help:"Symlink workflows/ to this directory" name:"workflows" type:"path"`
	Prompts   string `help:"Symlink prompts/ to this directory" name:"prompts" type:"path"`
}

func (c *PackCreateCmd) Help() string {
	return `Scaffolds a new pack directory with a pack.json manifest and standard
subdirectories, then installs and registers it so it is immediately available
for profiles, sync, and save.

By default, the pack is created in the current directory and symlinked into
the packs directory (--link behavior). Use --local to create the pack directly
inside the packs directory instead.

Examples:
  # Create a pack in CWD, symlink into packs dir (default)
  aipack pack create my-new-pack

  # Create a pack directly in the packs dir
  aipack pack create my-new-pack --local

See also: pack install, pack show`
}

func (c *PackCreateCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	req := app.PackCreateRequest{
		Name:      c.Name,
		ConfigDir: cfgDir,
		Local:     c.Local,
	}
	if cs := buildContentPaths(c.Rules, c.Skills, c.Agents, c.Workflows, c.Prompts); cs != nil {
		req.ContentSources = cs
	}
	if err := app.PackCreate(req); err != nil {
		return err
	}
	if c.Local {
		fmt.Fprintf(g.Stdout, "Created pack %q in %s\n", c.Name, filepath.Join(cfgDir, "packs", c.Name))
	} else {
		fmt.Fprintf(g.Stdout, "Created pack %q in ./%s (linked)\n", c.Name, c.Name)
	}
	return nil
}

// --- pack install ---

type PackInstallCmd struct {
	Path       string   `arg:"" optional:"" help:"Local directory path or registry pack name"`
	URL        string   `help:"Install pack from a git-accessible repository URL (HTTPS or SSH)" name:"url"`
	Ref        string   `help:"Git ref (branch/tag) to fetch" name:"ref"`
	SubPath    string   `help:"Subdirectory within the repo where the pack lives" name:"path"`
	Name       string   `help:"Override the pack name from pack.json" name:"name"`
	Registry   string   `help:"Path to registry YAML file (for registry name lookups)" name:"registry" type:"path"`
	Profile    string   `help:"Profile to register pack source in (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
	NoRegister bool     `help:"Do not auto-register pack as a source in any profile" name:"no-register"`
	Copy       bool     `help:"Copy pack files instead of symlinking (local paths only; not valid with --url)"`
	With       []string `help:"Accept bundled content: profiles(p), registries(r), extras(e), all" short:"w" name:"with" sep:","`
	Missing    bool     `help:"Install all missing packs from the active profile" short:"m"`
	Quiet      bool     `help:"Register as quiet (omitted vector selectors include nothing)" short:"q"`
	Rules      string   `help:"Extract rules from this directory within the repo" name:"rules"`
	Skills     string   `help:"Extract skills from this directory within the repo" name:"skills"`
	Agents     string   `help:"Extract agents from this directory within the repo" name:"agents"`
	Workflows  string   `help:"Extract workflows from this directory within the repo" name:"workflows"`
	Prompts    string   `help:"Extract prompts from this directory within the repo" name:"prompts"`
}

func (c *PackInstallCmd) Help() string {
	return fmt.Sprintf(`Installs a pack into %s. Local directory packs are
symlinked by default; use --copy to make a full copy instead. Remote packs
are fetched via git archive (selective fetch of declared files only) with
automatic fallback to shallow clone when the remote doesn't support archive.

If the argument is not a local directory path and not a URL, it is treated as
a registry pack name. The registry is consulted to resolve the name to a
repository URL (and optional subdirectory path), then the pack is fetched.

With -m/--missing, installs all missing packs from the active profile by
looking them up in the registry. Useful after 'profile set' to install
dependency packs declared in the profile.

Both HTTPS and SSH URLs are supported. SSH URLs work with git archive on
servers that support it (e.g. Bitbucket Server).

By default, the pack is registered as a source in the profile specified by
--profile (or the sync-config default profile, or "default"). Use --no-register
to skip auto-registration.

Examples:
  # Install all missing packs from the active profile
  aipack pack install -m

  # Install a local pack via symlink
  aipack pack install ./my-pack

  # Install a local pack via copy with a custom name
  aipack pack install ./my-pack --copy --name custom-name

  # Install from an HTTPS URL
  aipack pack install --url https://github.com/org/pack-repo

  # Install from an SSH URL
  aipack pack install --url ssh://git@bitbucket.example.com:7999/proj/repo.git --ref main

  # Install a pack from a subdirectory within a repo
  aipack pack install --url ssh://git@bb:7999/proj/repo.git --path my-pack

  # Install a pack by registry name
  aipack pack install my-team-pack

  # Install without registering in any profile
  aipack pack install ./my-pack --no-register

See also: pack delete, pack list, pack update, registry list`,
		configPathDisplay("packs", "<name>"),
	)
}

func (c *PackInstallCmd) Validate() error {
	hasPath := c.Path != ""
	hasURL := c.URL != ""
	hasContentFlags := c.Rules != "" || c.Skills != "" || c.Agents != "" || c.Workflows != "" || c.Prompts != ""
	if hasURL && hasPath {
		return fmt.Errorf("--url and path argument are mutually exclusive")
	}
	if hasURL && c.Copy {
		return fmt.Errorf("--copy is not valid with --url (URL packs are fetched remotely)")
	}
	if c.Missing && (hasPath || hasURL) {
		return fmt.Errorf("-m/--missing cannot be combined with a path or --url")
	}
	if c.Missing && len(c.With) > 0 {
		return fmt.Errorf("-m/--missing cannot be combined with --with")
	}
	if !hasPath && !hasURL && !c.Missing {
		return fmt.Errorf("provide a pack path, --url, or -m to install missing packs from the active profile")
	}
	if hasContentFlags && !hasURL {
		return fmt.Errorf("content flags (--rules, --skills, --agents, --workflows, --prompts) require --url")
	}
	if hasContentFlags && c.Name == "" {
		return fmt.Errorf("content flags require --name (no pack.json to derive name from)")
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

func (c *PackInstallCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	// Profile reconciliation mode: -m flag.
	if c.Missing {
		profileName := effectiveProfile(c.Profile, cfgDir)
		fmt.Fprintf(g.Stdout, "Resolving profile %q...\n", profileName)
		results, err := app.PackInstallMissing(ctx, app.PackInstallMissingRequest{
			ConfigDir:   cfgDir,
			ProfileName: profileName,
		}, g.Stdout)
		if err != nil {
			return err
		}
		printInstallMissingResults(results, profileName, g.Stdout)
		return nil
	}

	profile := ""
	if !c.NoRegister {
		profile = effectiveProfile(c.Profile, cfgDir)
	}

	with, err := parseWithFlag(c.With)
	if err != nil {
		return err
	}

	req := app.PackInstallRequest{
		ConfigDir: cfgDir,
		Name:      c.Name,
		Register:  !c.NoRegister,
		Profile:   profile,
		With:      with,
		Quiet:     c.Quiet,
	}
	// CLI content flags take precedence over registry entry content_paths.
	if cp := buildContentPaths(c.Rules, c.Skills, c.Agents, c.Workflows, c.Prompts); cp != nil {
		req.ContentPaths = cp
	}

	if c.URL != "" {
		req.URL = c.URL
		req.Ref = c.Ref
		req.SubPath = c.SubPath
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
			fetchErr := app.RegistryFetch(ctx, app.RegistryFetchRequest{
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
		if entry.Quiet {
			req.Quiet = true
		}
		if len(entry.ContentPaths) > 0 {
			req.ContentPaths = entry.ContentPaths
		}
	} else {
		req.PackPath = c.Path
		req.Link = !c.Copy
	}

	if err := app.PackInstall(ctx, req, g.Stdout); err != nil {
		return err
	}
	fmt.Fprintln(g.Stdout, "\nNext: run 'aipack sync' to sync pack content to your harness.")
	return nil
}

// --- pack list ---

type PackListCmd struct {
	JSON bool `help:"Emit machine-readable JSON array" name:"json"`
}

func (c *PackListCmd) Help() string {
	return fmt.Sprintf(`Lists all packs installed under %s, showing name, install
method (link/copy/clone/archive), version, origin URL, and broken-link status.

Examples:
  # List installed packs
  aipack pack list

  # Machine-readable JSON output
  aipack pack list --json

See also: pack show, pack install`,
		configPathDisplay("packs"),
	)
}

func (c *PackListCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
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
			isLink := e.Method == config.MethodLink
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
	printPackListEntries(g.Stdout, entries)
	return nil
}

func printPackListEntries(w io.Writer, entries []app.PackShowEntry) {
	for i, e := range entries {
		if i > 0 {
			fmt.Fprintln(w)
		}
		ver := ""
		if e.Version != "" {
			ver = " v" + e.Version
		}
		broken := ""
		if e.Method == config.MethodLink && !util.PathExists(e.Path) {
			broken = " [BROKEN LINK]"
		}
		fmt.Fprintf(w, "Name:    %s (%s)%s%s\n", e.Name, e.Method, ver, broken)
		if e.Origin != "" && e.Origin != e.Path {
			fmt.Fprintf(w, "Origin:  %s\n", e.Origin)
		}
		fmt.Fprintf(w, "Path:    %s\n", e.Path)
		if e.Ref != "" {
			fmt.Fprintf(w, "Ref:     %s\n", e.Ref)
		}
		if summary := packContentSummary(e); summary != "" {
			fmt.Fprintf(w, "Content: %s\n", summary)
		}
	}
}

func packContentSummary(e app.PackShowEntry) string {
	c := e.Counts()
	if c.IsZero() {
		return ""
	}
	return c.String()
}

// --- pack delete ---

type PackDeleteCmd struct {
	Name string `arg:"" help:"Name of the installed pack to delete" predictor:"pack"`
	Yes  bool   `help:"Skip confirmation prompt for bundled profile removal"`
}

func (c *PackDeleteCmd) Help() string {
	return fmt.Sprintf(`Deletes an installed pack directory from %s and
deregisters it from all profiles.

Examples:
  # Delete an installed pack
  aipack pack delete my-pack

See also: pack install, pack list`,
		configPathDisplay("packs", "<name>"),
	)
}

func (c *PackDeleteCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	result, err := app.PackRemove(cfgDir, c.Name, g.Stdout)
	if err != nil {
		return err
	}
	if len(result.BundledProfiles) == 0 {
		return nil
	}
	fmt.Fprintf(g.Stderr, "This pack installed profiles that still exist: %s\n", strings.Join(result.BundledProfiles, ", "))
	if !c.Yes {
		fmt.Fprint(g.Stderr, "Remove them? [y/N] ")
		if !g.StdinTTY {
			fmt.Fprintln(g.Stderr, "Skipped (non-interactive, use --yes to confirm).")
			return nil
		}
		type promptResult struct {
			answer string
			err    error
		}
		ch := make(chan promptResult, 1)
		go func() {
			var answer string
			_, err := fmt.Fscan(g.Stdin, &answer)
			ch <- promptResult{answer, err}
		}()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case res := <-ch:
			if res.err != nil || strings.ToLower(strings.TrimSpace(res.answer)) != "y" {
				return nil
			}
		}
	}
	app.RemoveBundledProfiles(cfgDir, result.BundledProfiles, g.Stdout)
	return nil
}

// --- pack rename ---

type PackRenameCmd struct {
	OldName string `arg:"" help:"Current name of the installed pack" predictor:"pack"`
	NewName string `arg:"" help:"New name for the pack"`
}

func (c *PackRenameCmd) Help() string {
	return `Renames an installed pack across all configuration: the pack directory,
pack.json manifest, sync-config, all profiles, and all ledger files.

Examples:
  aipack pack rename old-name new-name

See also: pack list, pack show`
}

func (c *PackRenameCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	eng := engine.New(nil, nil)
	return app.PackRename(eng, cfgDir, c.OldName, c.NewName, g.Stdout)
}

// --- pack enable (profile) ---

type PackEnableCmd struct {
	Name    string `arg:"" help:"Name of the installed pack to enable in the profile" predictor:"pack"`
	Profile string `help:"Profile to enable the pack in (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
	Quiet   bool   `help:"Register as quiet (omitted vector selectors include nothing)" short:"q"`
}

func (c *PackEnableCmd) Help() string {
	return fmt.Sprintf(`Enables an already-installed pack in the active profile. The pack must be
installed under %s first (see pack install).

Examples:
  # Enable a pack in the default profile
  aipack pack enable my-pack

  # Enable a pack in a specific profile
  aipack pack enable my-pack --profile production

See also: pack disable, pack install, pack list`,
		configPathDisplay("packs", "<name>"),
	)
}

func (c *PackEnableCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	// Verify the pack is installed.
	packsDir := app.PacksDir(cfgDir)
	packDir := filepath.Join(packsDir, c.Name)
	if _, err := os.Stat(packDir); os.IsNotExist(err) {
		return fmt.Errorf("pack %q is not installed (run 'aipack pack install' first)", c.Name)
	}

	if err := app.PackRegister(cfgDir, effectiveProfile(c.Profile, cfgDir), c.Name, c.Quiet, g.Stdout); err != nil {
		return err
	}
	return nil
}

// --- pack disable (profile) ---

type PackDisableCmd struct {
	Name    string `arg:"" help:"Name of the pack to disable in the profile" predictor:"pack"`
	Profile string `help:"Profile to disable the pack in (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
}

func (c *PackDisableCmd) Help() string {
	return `Disables a pack in the active profile. This does NOT delete the pack from
disk — use pack delete for that.

Examples:
  # Disable a pack in the default profile
  aipack pack disable my-pack

  # Disable in a specific profile
  aipack pack disable my-pack --profile production

See also: pack enable, pack delete, pack list`
}

func (c *PackDisableCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
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
	Name string   `arg:"" optional:"" help:"Name of the pack to update" predictor:"pack"`
	All  bool     `help:"Update all installed packs" name:"all"`
	With []string `help:"Accept bundled content: profiles(p), registries(r), extras(e), all" short:"w" name:"with" sep:","`
}

func (c *PackUpdateCmd) Help() string {
	return `Updates installed pack(s) to the latest version from their origin. For
remote packs, re-fetches from the origin (archive or clone). For symlinked
packs, re-validates the link target. For copied packs, re-copies from the
recorded origin.

Exactly one of <name> or --all is required.

Examples:
  # Update a specific pack
  aipack pack update my-pack

  # Update all installed packs
  aipack pack update --all

  # Update and accept all bundled content
  aipack pack update my-pack -w all

  # Update and accept only profiles
  aipack pack update --all -w p

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

func (c *PackUpdateCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	with, err := parseWithFlag(c.With)
	if err != nil {
		return err
	}

	results, err := app.PackUpdate(ctx, app.PackUpdateRequest{
		ConfigDir: cfgDir,
		Name:      c.Name,
		All:       c.All,
		With:      with,
	}, g.Stdout)
	if err != nil {
		return err
	}

	hasError := false
	for _, r := range results {
		if r.Status == app.StatusError {
			hasError = true
		}
		if cats := r.BundledCandidates.Categories(); len(cats) > 0 {
			names := make([]string, len(cats))
			for i, c := range cats {
				names[i] = string(c)
			}
			fmt.Fprintf(g.Stdout, "New content available in %s: %s\n", r.Name, strings.Join(names, ", "))
			fmt.Fprintf(g.Stdout, "  To approve: aipack pack update %s -w %s\n", r.Name, strings.Join(names, ","))
		}
	}
	if hasError {
		return ExitError{Code: cmdutil.ExitFail}
	}
	return nil
}

// --- pack show ---

type PackShowCmd struct {
	Name string `arg:"" help:"Name of the installed pack to show" predictor:"pack"`
	JSON bool   `help:"Emit machine-readable JSON" name:"json"`
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

See also: pack list, pack validate`
}

func (c *PackShowCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
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
	if len(entry.Extras) > 0 {
		fmt.Fprintf(g.Stdout, "Extras:      %s\n", joinComma(entry.Extras))
	}
	return nil
}

func joinComma(items []string) string {
	return strings.Join(items, ", ")
}

// printInstallMissingResults formats PackInstallMissing results for CLI output.
func printInstallMissingResults(results []app.PackInstallMissingResult, profileName string, w io.Writer) {
	var installed, present, notFound, errored int
	for _, r := range results {
		switch r.Status {
		case "installed":
			installed++
		case "present":
			present++
		case "not-in-registry":
			notFound++
		case "error":
			errored++
		}
	}

	if installed == 0 && notFound == 0 && errored == 0 {
		fmt.Fprintf(w, "All packs in profile %q are installed.\n", profileName)
		return
	}

	parts := []string{}
	if installed > 0 {
		parts = append(parts, fmt.Sprintf("%d installed", installed))
	}
	if present > 0 {
		parts = append(parts, fmt.Sprintf("%d already present", present))
	}
	if notFound > 0 {
		parts = append(parts, fmt.Sprintf("%d not in registry", notFound))
	}
	if errored > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", errored))
	}
	fmt.Fprintln(w, strings.Join(parts, ", "))
}
