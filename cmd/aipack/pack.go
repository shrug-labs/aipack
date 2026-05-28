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
	"github.com/shrug-labs/aipack/internal/source"
	"github.com/shrug-labs/aipack/internal/util"
)

type PackCmd struct {
	Create   PackCreateCmd   `cmd:"" help:"Scaffold a new pack directory with pack.json manifest"`
	Import   PackImportCmd   `cmd:"" help:"Import one markdown file into a new or existing pack"`
	Install  PackInstallCmd  `cmd:"" help:"Install a pack from a path, URL, registry, or active profile"`
	Delete   PackDeleteCmd   `cmd:"" help:"Delete an installed pack and rendered output"`
	Update   PackUpdateCmd   `cmd:"" help:"Update installed pack(s) to latest version from their origin"`
	Rename   PackRenameCmd   `cmd:"" help:"Rename an installed pack across all config"`
	Add      PackAddCmd      `cmd:"" help:"Add an installed pack to a profile"`
	Remove   PackRemoveCmd   `cmd:"" help:"Remove a pack from a profile"`
	Enable   PackEnableCmd   `cmd:"" help:"Enable a pack in a profile"`
	Disable  PackDisableCmd  `cmd:"" help:"Disable a pack in a profile without removing it"`
	List     PackListCmd     `cmd:"" help:"List all installed packs with their install method and origin"`
	Show     PackShowCmd     `cmd:"" help:"Show detailed metadata and content inventory for an installed pack"`
	Inspect  PackInspectCmd  `cmd:"" help:"Inspect a pack source without installing it"`
	Versions PackVersionsCmd `cmd:"" help:"List available semver versions for a pack from its remote origin"`
	Validate ValidateCmd     `cmd:"" help:"Validate a pack source tree"`
}

func (c *PackCmd) Help() string {
	return fmt.Sprintf(`Manage installed packs. Packs are portable, versioned bundles of AI agent
configuration containing rules, agents, workflows, skills, hooks, plugin references,
MCP server definitions, and harness base configs.

Packs are installed under %s.`,
		configPathDisplay("packs", "<name>"),
	)
}

// --- pack import ---

type PackImportCmd struct {
	Source  string `arg:"" help:"Local markdown file path or HTTP(S) raw-file URL"`
	Type    string `help:"Content type to import: skill, rule, or prompt" name:"type" required:""`
	Name    string `help:"Name of the new pack to create" name:"name"`
	Pack    string `help:"Existing installed pack to import into" name:"pack" predictor:"pack"`
	ID      string `help:"Content ID; defaults to source filename without .md" name:"id"`
	Add     bool   `help:"Add the imported pack to the active profile after importing" name:"add"`
	Profile string `help:"Profile to add pack to (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
}

func (c *PackImportCmd) Help() string {
	return fmt.Sprintf(`Imports one markdown file as pack content. Use --name
to create a new local pack under %s, or --pack to add the content to an
existing installed pack.

The imported file is written as the requested content type. If it does not
already start with YAML frontmatter, aipack adds minimal frontmatter.

Examples:
  aipack pack import ./review.md --type skill --name review-pack
  aipack pack import ./triage.md --type skill --pack team-ops
  aipack pack import https://example.com/rule.md --type rule --name rules --id incident-rule
  aipack pack import ./prompt.md --type prompt --name prompts --add

See also: pack create, pack install, prompt list`,
		configPathDisplay("packs", "<name>"),
	)
}

func (c *PackImportCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	cat, err := parseImportType(c.Type)
	if err != nil {
		return err
	}
	profile := ""
	if c.Add {
		profile = effectiveProfile(c.Profile, cfgDir)
	}
	if err := app.PackImport(ctx, app.PackImportRequest{
		Source:     c.Source,
		Category:   cat,
		Name:       c.Name,
		TargetPack: c.Pack,
		ID:         c.ID,
		ConfigDir:  cfgDir,
		Add:        c.Add,
		Profile:    profile,
	}, g.Stdout); err != nil {
		return err
	}
	if c.Add && cat != domain.CategoryPrompts {
		if err := maybeAutoSyncProfile(ctx, g, cfgDir, profile); err != nil {
			return err
		}
	}
	if cat == domain.CategoryPrompts {
		fmt.Fprintln(g.Stdout, "Next: run 'aipack prompt list' to see imported prompts.")
	} else {
		fmt.Fprintln(g.Stdout, "Next: run 'aipack sync' to sync pack content to your harness.")
	}
	return nil
}

func parseImportType(raw string) (domain.PackCategory, error) {
	cat, ok := domain.ParseSingularLabel(strings.ToLower(strings.TrimSpace(raw)))
	if !ok {
		return "", fmt.Errorf("invalid --type %q (valid: skill, rule, prompt)", raw)
	}
	switch cat {
	case domain.CategorySkills, domain.CategoryRules, domain.CategoryPrompts:
		return cat, nil
	default:
		return "", fmt.Errorf("invalid --type %q (valid: skill, rule, prompt)", raw)
	}
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

type contentPathFlags struct {
	Rules     string `help:"Use this directory for rules content" name:"rules"`
	Skills    string `help:"Use this directory for skills content" name:"skills"`
	Agents    string `help:"Use this directory for agents content" name:"agents"`
	Workflows string `help:"Use this directory for workflows content" name:"workflows"`
	Hooks     string `help:"Use this directory for hooks content" name:"hooks"`
	Prompts   string `help:"Use this directory for prompts content" name:"prompts"`
}

type localContentPathFlags struct {
	Rules     string `help:"Use this directory for rules content" name:"rules" type:"path"`
	Skills    string `help:"Use this directory for skills content" name:"skills" type:"path"`
	Agents    string `help:"Use this directory for agents content" name:"agents" type:"path"`
	Workflows string `help:"Use this directory for workflows content" name:"workflows" type:"path"`
	Hooks     string `help:"Use this directory for hooks content" name:"hooks" type:"path"`
	Prompts   string `help:"Use this directory for prompts content" name:"prompts" type:"path"`
}

func (f contentPathFlags) HasAny() bool {
	return f.Rules != "" || f.Skills != "" || f.Agents != "" || f.Workflows != "" || f.Hooks != "" || f.Prompts != ""
}

func (f localContentPathFlags) HasAny() bool {
	return f.Rules != "" || f.Skills != "" || f.Agents != "" || f.Workflows != "" || f.Hooks != "" || f.Prompts != ""
}

func buildContentPaths(rules, skills, agents, workflows, hooks, prompts string) map[domain.PackCategory]string {
	pairs := []struct {
		cat domain.PackCategory
		val string
	}{
		{domain.CategoryRules, rules},
		{domain.CategorySkills, skills},
		{domain.CategoryAgents, agents},
		{domain.CategoryWorkflows, workflows},
		{domain.CategoryHooks, hooks},
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

// ContentPaths builds a repo-relative content type map from CLI flag values.
// Returns nil when all inputs are empty.
func (f contentPathFlags) ContentPaths() map[domain.PackCategory]string {
	return buildContentPaths(f.Rules, f.Skills, f.Agents, f.Workflows, f.Hooks, f.Prompts)
}

// ContentPaths builds a local filesystem content type map from CLI flag values.
// Returns nil when all inputs are empty.
func (f localContentPathFlags) ContentPaths() map[domain.PackCategory]string {
	return buildContentPaths(f.Rules, f.Skills, f.Agents, f.Workflows, f.Hooks, f.Prompts)
}

// --- pack create ---

type PackCreateCmd struct {
	Name  string `arg:"" help:"Pack name"`
	Local bool   `help:"Create pack inside the packs directory instead of the current directory" name:"local"`
	localContentPathFlags
}

func (c *PackCreateCmd) Help() string {
	return `Scaffolds a new pack directory with a pack.json manifest and standard
subdirectories, then installs and records it so it is immediately available
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
	if cs := c.ContentPaths(); cs != nil {
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
	Sources  []string `arg:"" optional:"" help:"Local directory path, registry pack name, name@ref, URL, or archive path"`
	URL      string   `help:"Install pack from a git repository URL or static archive URL/path" name:"url"`
	Archive  bool     `help:"Treat source as a static zip/tar archive instead of a git repository; inferred for archive URLs/files" name:"archive"`
	Ref      string   `help:"Git ref to checkout: semver (1.2.3, v1.2, latest), commit hash, branch, or namespaced tag (my-pack/v1.2.3). --version is an alias." name:"ref" aliases:"version"`
	SubPath  string   `help:"Subdirectory within the repo where the pack lives" name:"path"`
	Name     string   `help:"Override the pack name from pack.json" name:"name"`
	Registry string   `help:"Path to registry YAML file (for registry name lookups)" name:"registry" type:"path"`
	Profile  string   `help:"Profile to add pack to (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
	Add      bool     `help:"Add pack to the active profile after installing" name:"add"`
	Copy     bool     `help:"Copy pack files instead of symlinking (local paths only; not valid with --url)"`
	With     []string `help:"Accept bundled content: profiles(p), registries(r), extras(e), all" short:"w" name:"with" sep:","`
	Missing  bool     `help:"Install all missing packs from the active profile" short:"m"`
	Quiet    bool     `help:"Install as quiet-by-nature — omitted profile-entry selectors include nothing; persists via lockfile so future profile adds default to quiet" short:"q"`
	NoQuiet  bool     `help:"Force non-quiet install even if a prior lockfile entry says quiet. Mutually exclusive with -q." name:"no-quiet"`
	contentPathFlags
}

func (c *PackInstallCmd) Help() string {
	return fmt.Sprintf(`Installs a pack into %s. Bare 'pack install' (no
arguments) reconciles the active profile — any packs referenced by the
profile that aren't already installed are fetched via the registry. Pass a
path, URL, or registry name to target a specific pack instead. Pass multiple
registry names to install them in one command.

Local directory packs are symlinked by default; use --copy to make a full
copy instead. Remote packs are fetched via shallow git clone unless the source
is a static zip/tar archive or --archive is set. Both HTTPS and SSH URLs are
supported for git installs.

If the positional argument is not a local directory path and not a URL, it
is treated as a registry pack name. The registry is consulted to resolve the
name to a repository URL (and optional subdirectory path), then the pack is
fetched.

-m/--missing is kept as an explicit alias for the bare form — useful in
scripts where the intent is worth stating even when the default matches.

By default, the pack is installed to disk but not added to any profile.
Use --add to also add it to the active profile (or --profile to target
a specific one). Use 'aipack pack add <name>' to add it later.

Examples:
  # Reconcile the active profile — install any missing packs (the default)
  aipack pack install
  aipack pack install -m                     # explicit equivalent

  # Install a local pack via symlink
  aipack pack install ./my-pack

  # Install and add to the active profile
  aipack pack install ./my-pack --add

  # Install and add to a specific profile
  aipack pack install ./my-pack --add --profile other-profile

  # Install a local pack via copy with a custom name
  aipack pack install ./my-pack --copy --name custom-name

  # Install from an HTTPS URL
  aipack pack install --url https://github.com/org/pack-repo

  # Install from a static archive URL
  aipack pack install https://example.com/pack.zip

  # Install from a local static archive
  aipack pack install ./pack.zip

  # Install from an SSH URL
  aipack pack install --url ssh://git@bitbucket.example.com:7999/proj/repo.git --ref main

  # Install a pack from a subdirectory within a repo
  aipack pack install --url ssh://git@bb:7999/proj/repo.git --path my-pack

  # Install a pack by registry name
  aipack pack install my-team-pack

  # Install several registry packs
  aipack pack install essentials aipack-core memory --add -w all

  # Install a specific version
  aipack pack install my-team-pack@1.2.3
  aipack pack install --url https://github.com/org/repo --ref 1.2.3

  # Install the latest stable v1.x.x (resolves to exact tag, pins to that)
  aipack pack install my-team-pack@v1
  aipack pack install my-team-pack --ref v1.2

  # Install a namespaced tag from a multi-pack monorepo
  aipack pack install my-team-pack --ref my-team-pack/v0.3.0
  aipack pack install my-team-pack@my-team-pack/v0.3.0

  # Track a branch (no pin — follows upstream HEAD of that branch)
  aipack pack install my-team-pack --ref main

See also: pack delete, pack list, pack update, registry list`,
		configPathDisplay("packs", "<name>"),
	)
}

func (c *PackInstallCmd) Validate() error {
	hasPath := len(c.Sources) > 0
	hasMultipleSources := len(c.Sources) > 1
	hasURL := c.URL != ""
	hasContentFlags := c.HasAny()
	path := ""
	if len(c.Sources) == 1 {
		path = c.Sources[0]
	}
	if shouldTreatPackSourceAsArchive(c.URL) || shouldTreatPackSourceAsArchive(path) {
		c.Archive = true
	}

	// Bare `pack install` with no target implies profile reconciliation —
	// the same as passing -m/--missing. Setting Missing here lets the rest
	// of the checks treat implicit-missing and explicit-missing identically.
	if !hasPath && !hasURL && !c.Missing && !hasContentFlags && c.Ref == "" {
		c.Missing = true
	}

	if hasURL && hasPath {
		return fmt.Errorf("--url and path argument are mutually exclusive")
	}
	if hasMultipleSources {
		if c.Archive || c.Copy || c.Ref != "" || c.SubPath != "" || c.Name != "" || hasContentFlags {
			return fmt.Errorf("multiple pack install only supports registry names with shared --add, --profile, --with, and quiet flags")
		}
		for _, source := range c.Sources {
			if _, _, ok, err := splitRegistrySourceRef(source, ""); err != nil {
				return err
			} else if !ok {
				return fmt.Errorf("multiple pack install only supports registry names; got %q", source)
			}
		}
	}
	if hasURL && c.Copy {
		return fmt.Errorf("--copy is not valid with --url (URL packs are fetched remotely)")
	}
	if c.Archive && !hasURL && !hasPath {
		return fmt.Errorf("--archive requires a path or --url")
	}
	if c.Archive && c.Ref != "" {
		return fmt.Errorf("--archive cannot be combined with --ref/--version")
	}
	if c.Archive && c.Copy {
		return fmt.Errorf("--copy is not valid with archive installs")
	}
	if c.Missing && (hasPath || hasURL) {
		return fmt.Errorf("-m/--missing cannot be combined with a path or --url")
	}
	if c.Missing && len(c.With) > 0 {
		return fmt.Errorf("profile reconciliation (bare `pack install` or -m/--missing) cannot be combined with --with; target a specific pack to use --with")
	}
	if c.Missing && c.Ref != "" {
		return fmt.Errorf("profile reconciliation cannot be combined with --ref/--version; target a specific pack")
	}
	if hasContentFlags && !hasURL {
		return fmt.Errorf("content flags (--rules, --skills, --agents, --workflows, --hooks, --prompts) require --url")
	}
	if hasContentFlags && c.Name == "" {
		return fmt.Errorf("content flags require --name (no pack.json to derive name from)")
	}
	return nil
}

var isRegistryName = cmdutil.IsRegistryName

func splitRegistrySourceRef(input, flagRef string) (name, ref string, ok bool, err error) {
	candidate := input
	suffix := ""
	hasRef := false
	if n, s, found := strings.Cut(input, "@"); found && s != "" {
		candidate = n
		suffix = s
		hasRef = true
	}
	if !isRegistryName(candidate) {
		return input, flagRef, false, nil
	}
	ref = flagRef
	if hasRef {
		if flagRef != "" {
			return "", "", false, fmt.Errorf("cannot combine @<ref> in name with --ref/--version")
		}
		ref = suffix
	}
	return candidate, ref, true, nil
}

func shouldTreatPackSourceAsArchive(input string) bool {
	if !source.LooksLikeArchive(input) {
		return false
	}
	if source.IsHTTPArchiveURL(input) {
		return true
	}
	if source.IsRemoteInstallInput(input) {
		return false
	}
	info, err := os.Stat(input)
	return err == nil && !info.IsDir()
}

// effectiveProfile returns the profile name to use, loading sync-config defaults
// if the explicit value is empty.
func effectiveProfile(explicit, cfgDir string) string {
	if explicit != "" {
		return explicit
	}
	sc, _ := config.LoadSyncConfig(config.SyncConfigPath(cfgDir))
	return cmdutil.ResolveProfileName("", sc)
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
		if err := installMissingExitCode(results); err != nil {
			return err
		}
		return maybeAutoSyncAfterInstallMissing(ctx, g, cfgDir, profileName, results)
	}

	// Parse name@<ref> from the positional arg. Only applies to non-local,
	// non-URL paths (i.e. registry names). Local paths and URLs can contain @
	// for other reasons (e.g. git SSH URLs). The suffix is a ref spec
	// (semver, partial semver, commit, namespaced tag, branch name, or
	// latest) and feeds the same dispatch as --ref/--version.
	profile := ""
	if c.Add {
		profile = effectiveProfile(c.Profile, cfgDir)
	}

	with, err := parseWithFlag(c.With)
	if err != nil {
		return err
	}

	quietOverride, err := resolveQuietFlags(c.Quiet, c.NoQuiet)
	if err != nil {
		return err
	}

	if len(c.Sources) > 1 {
		if err := c.runBatchInstall(ctx, g, cfgDir, c.Sources, profile, with, quietOverride); err != nil {
			return err
		}
		return finishPackContentChange(ctx, g, cfgDir, profile, c.Add)
	}

	ref := c.Ref
	path := ""
	if len(c.Sources) == 1 {
		path = c.Sources[0]
		if parsedName, parsedRef, ok, err := splitRegistrySourceRef(path, ref); err != nil {
			return err
		} else if ok {
			path = parsedName
			ref = parsedRef
		}
	}
	req := app.PackInstallRequest{
		ConfigDir: cfgDir,
		Name:      c.Name,
		Add:       c.Add,
		Profile:   profile,
		With:      with,
		Quiet:     quietOverride,
		Ref:       ref,
		Archive:   c.Archive,
	}
	// CLI content flags take precedence over registry entry content_paths.
	cliContentPaths := c.ContentPaths()
	if cliContentPaths != nil {
		req.ContentPaths = cliContentPaths
	}

	if c.URL != "" {
		req.URL = c.URL
		req.SubPath = c.SubPath
	} else if path != "" && shouldTreatPackSourceAsArchive(path) {
		req.URL = path
		req.SubPath = c.SubPath
		req.Archive = true
	} else if path != "" && source.IsRemoteInstallInput(path) {
		req.URL = path
		req.SubPath = c.SubPath
	} else if path != "" && isRegistryName(path) {
		// Not a local path — try registry lookup.
		entry, err := lookupRegistryPackForInstall(ctx, g, cfgDir, c.Registry, path)
		if err != nil {
			return fmt.Errorf("registry lookup for %q: %w\n\nHint: use --url for a direct URL install, or check 'aipack registry list'", path, err)
		}
		req = app.PackInstallRequestFromRegistryEntry(cfgDir, path, entry)
		req.Add = c.Add
		req.Profile = profile
		req.With = with
		// User's explicit ref wins; fall back to the registry entry's
		// default ref only when the user didn't specify one.
		if ref != "" {
			req.Ref = ref
		}
		if c.Name != "" {
			req.Name = c.Name
		}
		// A registry hint of `quiet: true` only applies when the user
		// didn't pass either flag; explicit -q / --no-quiet always wins.
		if quietOverride != nil {
			req.Quiet = quietOverride
		}
		if cliContentPaths != nil {
			req.ContentPaths = cliContentPaths
		}
	} else {
		req.PackPath = path
		req.Link = !c.Copy
	}

	if err := app.PackInstall(ctx, req, g.Stdout); err != nil {
		return err
	}
	return finishPackContentChange(ctx, g, cfgDir, profile, c.Add)
}

func (c *PackInstallCmd) runBatchInstall(ctx context.Context, g *Globals, cfgDir string, sources []string, profile string, with domain.BundledSet, quietOverride *bool) error {
	parsed, err := parseBatchRegistrySources(sources)
	if err != nil {
		return err
	}
	reg, err := loadBatchInstallRegistry(ctx, g, cfgDir, c.Registry, parsed)
	if err != nil {
		return err
	}
	failed := 0
	for i, source := range parsed {
		fmt.Fprintf(g.Stdout, "\n[%d/%d] %s\n", i+1, len(parsed), source.Raw)
		entry, ok := reg.Packs[source.Name]
		if !ok {
			failed++
			fmt.Fprintf(g.Stdout, "error: registry lookup for %q: pack not found in registry\n", source.Name)
			continue
		}
		req := app.PackInstallRequestFromRegistryEntry(cfgDir, source.Name, entry)
		req.Add = c.Add
		req.Profile = profile
		req.With = with
		if source.Ref != "" {
			req.Ref = source.Ref
		}
		if quietOverride != nil {
			req.Quiet = quietOverride
		}
		if err := app.PackInstall(ctx, req, g.Stdout); err != nil {
			failed++
			continue
		}
	}
	if failed > 0 {
		return fmt.Errorf("batch install: %d pack(s) failed", failed)
	}
	return nil
}

type batchRegistrySource struct {
	Raw  string
	Name string
	Ref  string
}

func parseBatchRegistrySources(sources []string) ([]batchRegistrySource, error) {
	out := make([]batchRegistrySource, 0, len(sources))
	for _, raw := range sources {
		name, ref, ok, err := splitRegistrySourceRef(raw, "")
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("multiple pack install only supports registry names; got %q", raw)
		}
		out = append(out, batchRegistrySource{Raw: raw, Name: name, Ref: ref})
	}
	return out, nil
}

func loadBatchInstallRegistry(ctx context.Context, g *Globals, cfgDir, registryPath string, sources []batchRegistrySource) (config.Registry, error) {
	reg, err := loadRegistryForInstall(cfgDir, registryPath)
	if err != nil {
		return config.Registry{}, err
	}
	if registryPath != "" || allBatchSourcesExist(reg, sources) {
		return reg, nil
	}
	fmt.Fprintln(g.Stderr, "Fetching registry...")
	if fetchErr := app.RegistryFetch(ctx, app.RegistryFetchRequest{ConfigDir: cfgDir}, nil); fetchErr != nil {
		return reg, nil
	}
	return loadRegistryForInstall(cfgDir, registryPath)
}

func loadRegistryForInstall(cfgDir, registryPath string) (config.Registry, error) {
	if registryPath != "" {
		return config.LoadRegistry(registryPath)
	}
	return config.LoadMergedRegistry(cfgDir)
}

func allBatchSourcesExist(reg config.Registry, sources []batchRegistrySource) bool {
	for _, source := range sources {
		if _, ok := reg.Packs[source.Name]; !ok {
			return false
		}
	}
	return true
}

func lookupRegistryPackForInstall(ctx context.Context, g *Globals, cfgDir, registryPath, name string) (config.RegistryEntry, error) {
	regReq := app.RegistryListRequest{
		ConfigDir:    cfgDir,
		RegistryPath: registryPath,
	}
	entry, err := app.RegistryLookup(regReq, name)
	if err == nil {
		return entry, nil
	}
	if registryPath != "" {
		return config.RegistryEntry{}, err
	}
	fmt.Fprintln(g.Stderr, "Fetching registry...")
	fetchErr := app.RegistryFetch(ctx, app.RegistryFetchRequest{ConfigDir: cfgDir}, nil)
	if fetchErr != nil {
		return config.RegistryEntry{}, err
	}
	return app.RegistryLookup(regReq, name)
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
			Pin        string `json:"pin,omitempty"`
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
				Pin:        e.Pin,
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
		ver := formatVersionLabel(e)
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

// formatVersionLabel returns a human-readable version label for an entry,
// preferring the lockfile pin (with a "(pinned)" indicator) over the
// pack.json version. Returns an empty string if neither is set.
func formatVersionLabel(e app.PackShowEntry) string {
	if pin := e.PinLabel(); pin != "" {
		return " " + pin
	}
	if e.Version != "" {
		return " v" + source.StripVersionPrefix(e.Version)
	}
	return ""
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
	Name         string `arg:"" help:"Name of the installed pack to delete" predictor:"pack"`
	KeepRendered bool   `help:"Keep rendered harness files and only stop tracking them" name:"keep-rendered"`
	DryRun       bool   `help:"Preview deletion without writing" name:"dry-run"`
	JSON         bool   `help:"Emit machine-readable JSON" name:"json"`
	Yes          bool   `help:"Skip confirmation prompt for bundled profile removal"`
}

func (c *PackDeleteCmd) Help() string {
	return fmt.Sprintf(`Deletes an installed pack from %s, removes it from all
profiles, clears its lockfile and ledger entries, and removes rendered harness
files that aipack can attribute to the pack.

Use --keep-rendered to stop managing the pack while leaving rendered harness
files in place as unmanaged content.

Examples:
  # Delete an installed pack and its rendered output
  aipack pack delete my-pack

  # Stop tracking a pack but keep rendered harness files
  aipack pack delete my-pack --keep-rendered

See also: pack install, pack list`,
		configPathDisplay("packs", "<name>"),
	)
}

func (c *PackDeleteCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	progressOut := g.Stdout
	if c.JSON {
		progressOut = io.Discard
	}
	result, err := app.PackDeleteWithOptions(engine.New(nil, nil), app.PackDeleteRequest{
		ConfigDir:    cfgDir,
		Name:         c.Name,
		KeepRendered: c.KeepRendered,
		DryRun:       c.DryRun,
		Registry:     g.Registry,
	}, progressOut)
	if err != nil {
		return err
	}
	if c.JSON {
		return cmdutil.WriteJSON(g.Stdout, result)
	}
	if c.DryRun {
		return nil
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

// --- pack add (profile) ---

type PackAddCmd struct {
	Name    string `arg:"" help:"Name of the installed pack to add to the profile" predictor:"pack"`
	Profile string `help:"Profile to add the pack to (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
	Quiet   bool   `help:"Force quiet profile entry (omitted vector selectors include nothing). Overrides the lockfile default." short:"q"`
	NoQuiet bool   `help:"Force non-quiet profile entry even if the pack was installed quietly. Mutually exclusive with -q." name:"no-quiet"`
}

func (c *PackAddCmd) Help() string {
	return fmt.Sprintf(`Adds an installed pack to a profile. The pack must be installed under
%s first (see pack install).

Use --add on pack install to combine both steps.

Examples:
  # Add a pack to the default profile
  aipack pack add my-pack

  # Add a pack to a specific profile
  aipack pack add my-pack --profile other-profile

See also: pack remove, pack enable, pack disable, pack install`,
		configPathDisplay("packs", "<name>"),
	)
}

func (c *PackAddCmd) Run(ctx context.Context, g *Globals) error {
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

	quietOverride, err := resolveQuietFlags(c.Quiet, c.NoQuiet)
	if err != nil {
		return err
	}
	profileName := effectiveProfile(c.Profile, cfgDir)
	if err := app.PackAdd(cfgDir, profileName, c.Name, quietOverride, g.Stdout); err != nil {
		return err
	}
	return maybeAutoSyncProfileWithHint(ctx, g, cfgDir, profileName)
}

// resolveQuietFlags converts the CLI pair (-q / --no-quiet) to the tri-state
// *bool that app.PackAdd and app.PackInstall expect. nil means "no explicit
// flag, inherit from the lockfile." Both flags true is a usage error.
func resolveQuietFlags(quiet, noQuiet bool) (*bool, error) {
	if quiet && noQuiet {
		return nil, fmt.Errorf("-q and --no-quiet are mutually exclusive")
	}
	if quiet {
		t := true
		return &t, nil
	}
	if noQuiet {
		f := false
		return &f, nil
	}
	return nil, nil
}

// --- pack remove (profile) ---

type PackRemoveCmd struct {
	Name    string `arg:"" help:"Name of the pack to remove from the profile" predictor:"pack"`
	Profile string `help:"Profile to remove the pack from (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
}

func (c *PackRemoveCmd) Help() string {
	return `Removes a pack entry from a profile. This does NOT delete the pack from
disk — use pack delete for that. To temporarily disable a pack without
removing it, use pack disable instead.

Examples:
  # Remove a pack from the default profile
  aipack pack remove my-pack

  # Remove from a specific profile
  aipack pack remove my-pack --profile other-profile

See also: pack add, pack disable, pack delete`
}

func (c *PackRemoveCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	profileName := effectiveProfile(c.Profile, cfgDir)
	if err := app.PackRemove(cfgDir, profileName, c.Name, g.Stdout); err != nil {
		return err
	}
	return maybeAutoSyncProfileWithHint(ctx, g, cfgDir, profileName)
}

// --- pack enable (profile) ---

type PackEnableCmd struct {
	Name    string `arg:"" help:"Name of the pack to enable in the profile" predictor:"pack"`
	Profile string `help:"Profile to enable the pack in (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
}

func (c *PackEnableCmd) Help() string {
	return `Enables a pack in a profile. The pack must already be in the profile
— use pack add to add it first.

Examples:
  # Enable a previously disabled pack
  aipack pack enable my-pack

  # Enable in a specific profile
  aipack pack enable my-pack --profile other-profile

See also: pack disable, pack add, pack remove`
}

func (c *PackEnableCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	profileName := effectiveProfile(c.Profile, cfgDir)
	if err := app.PackEnable(cfgDir, profileName, c.Name, g.Stdout); err != nil {
		return err
	}
	return maybeAutoSyncProfileWithHint(ctx, g, cfgDir, profileName)
}

// --- pack disable (profile) ---

type PackDisableCmd struct {
	Name    string `arg:"" help:"Name of the pack to disable in the profile" predictor:"pack"`
	Profile string `help:"Profile to disable the pack in (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
}

func (c *PackDisableCmd) Help() string {
	return `Disables a pack in a profile without removing it. The pack entry
stays in the profile so its selectors and overrides are preserved. Use
pack remove to fully remove the entry.

Examples:
  # Disable a pack in the default profile
  aipack pack disable my-pack

  # Disable in a specific profile
  aipack pack disable my-pack --profile other-profile

See also: pack enable, pack remove, pack delete`
}

func (c *PackDisableCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	profileName := effectiveProfile(c.Profile, cfgDir)
	if err := app.PackDisable(cfgDir, profileName, c.Name, g.Stdout); err != nil {
		return err
	}
	return maybeAutoSyncProfileWithHint(ctx, g, cfgDir, profileName)
}

// --- pack update ---

type PackUpdateCmd struct {
	Name   string   `arg:"" optional:"" help:"Name of the pack to update" predictor:"pack"`
	All    bool     `help:"Update all installed packs" name:"all"`
	Ref    string   `help:"Git ref to checkout: semver (1.2.3, v1.2, latest), commit hash, branch, or namespaced tag (my-pack/v1.2.3). --version is an alias." name:"ref" aliases:"version"`
	With   []string `help:"Accept bundled content: profiles(p), registries(r), extras(e), all" short:"w" name:"with" sep:","`
	DryRun bool     `help:"Preview changes without writing to disk, lockfile, or bundled content" name:"dry-run"`
}

func (c *PackUpdateCmd) Help() string {
	return `Updates installed pack(s) to the latest version from their origin. By
default, updates all installed packs. Pass a pack name to target a specific
one. For remote packs, re-fetches from the origin (clone). For symlinked
packs, re-validates the link target and re-installs bundled content. For
copied packs, re-copies from the recorded origin.

Pinned packs (installed with --ref) stay pinned on a bare update and
report available newer versions. Use --ref (or its alias --version) to
move or clear a pin.

Examples:
  # Update all installed packs
  aipack pack update

  # Update a specific pack
  aipack pack update my-pack

  # Update all and accept bundled content
  aipack pack update -w all

  # Update a specific pack to a version
  aipack pack update my-pack --ref 2.0.0

  # Update to the latest stable v1.x.x (resolves and pins to the exact tag)
  aipack pack update my-pack --ref v1

  # Update a namespaced-tag pack (multi-pack monorepo)
  aipack pack update my-pack --ref my-pack/v0.3.1

  # Unpin a pack (resume tracking HEAD)
  aipack pack update my-pack --ref latest

  # --all is an explicit alias for the bare form (useful in scripts)
  aipack pack update --all

See also: pack install, pack show`
}

func (c *PackUpdateCmd) Validate() error {
	if c.Name != "" && c.All {
		return fmt.Errorf("<name> and --all are mutually exclusive")
	}
	if c.Ref != "" && c.Name == "" {
		return fmt.Errorf("--ref/--version requires a pack name argument")
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

	// Bare `pack update` (no name, no --all) resolves to all packs. --all
	// remains as an explicit alias for scripts and muscle memory.
	all := c.All || c.Name == ""

	results, err := app.PackUpdate(ctx, app.PackUpdateRequest{
		ConfigDir: cfgDir,
		Name:      c.Name,
		All:       all,
		Ref:       c.Ref,
		With:      with,
		DryRun:    c.DryRun,
		Quiet:     all,
	}, g.Stdout, nil)
	if err != nil {
		return err
	}

	hasError := false
	for _, r := range results {
		if r.Status == app.StatusError {
			fmt.Fprintf(g.Stderr, "error: %s: %s\n", r.Name, r.Message)
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
	return maybeAutoSyncAfterPackUpdate(ctx, g, cfgDir, results)
}

// --- pack show ---

type PackShowCmd struct {
	Name string `arg:"" help:"Name of the installed pack to show" predictor:"pack"`
	JSON bool   `help:"Emit machine-readable JSON" name:"json"`
}

func (c *PackShowCmd) Help() string {
	return `Displays detailed metadata for an installed pack: name, version, path, install
method, origin URL, git ref, install timestamp, and content inventory (rules,
agents, workflows, skills, plugins, MCP servers).

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
	if pin := entry.PinLabel(); pin != "" {
		fmt.Fprintf(g.Stdout, "Pin:         %s\n", pin)
	}
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
	printContentList(g.Stdout, "Rules", entry.Rules)
	printContentList(g.Stdout, "Agents", entry.Agents)
	printContentList(g.Stdout, "Workflows", entry.Workflows)
	printContentList(g.Stdout, "Skills", entry.Skills)
	printContentList(g.Stdout, "Hooks", entry.Hooks)
	printContentList(g.Stdout, "Plugins", entry.Plugins)
	printContentList(g.Stdout, "Prompts", entry.Prompts)
	printContentList(g.Stdout, "MCP", entry.MCPServers)
	printContentList(g.Stdout, "Extras", entry.Extras)
	return nil
}

func printContentList(w io.Writer, label string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(w, "%-12s %s\n", label+":", strings.Join(items, ", "))
}

// --- pack inspect ---

type PackInspectCmd struct {
	Input    string `arg:"" optional:"" help:"Pack name, local path, or URL to inspect" predictor:"pack"`
	URL      string `help:"Inspect a git repository URL or static archive URL/path" name:"url"`
	Archive  bool   `help:"Treat --url/input as a static zip/tar archive; inferred for archive URLs/files" name:"archive"`
	Ref      string `help:"Git ref to inspect" name:"ref" aliases:"version"`
	SubPath  string `help:"Subdirectory within the repo/archive where pack.json lives" name:"path"`
	Name     string `help:"Override pack name for content_paths sources" name:"name"`
	Registry string `help:"Path to registry YAML file (for registry name lookups)" name:"registry" type:"path"`
	contentPathFlags
	Clear bool `help:"Remove all inspected packs from the search index and exit" name:"clear"`
	JSON  bool `help:"Emit machine-readable JSON" name:"json"`
}

func (c *PackInspectCmd) Help() string {
	return `Inspects a local path, registry pack name, git URL, or archive URL/path without
installing it. Inspected resources are added to the search index with status
"inspected" so they can be searched before trust/install decisions. Inspected
rows older than 30 days are dropped automatically on the next inspect; use
--clear to wipe them on demand.

Examples:
  aipack pack inspect ./my-pack
  aipack pack inspect team-pack
  aipack pack inspect https://example.com/pack.zip
  aipack pack inspect ./pack.zip
  aipack pack inspect --clear
  aipack search --status inspected

See also: pack install, registry list, search`
}

func (c *PackInspectCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	if c.Clear {
		if c.URL != "" || strings.TrimSpace(c.Input) != "" {
			return fmt.Errorf("--clear is mutually exclusive with input and --url")
		}
		result, err := app.PackInspectClear(app.PackInspectClearRequest{ConfigDir: cfgDir})
		if err != nil {
			return err
		}
		if c.JSON {
			return cmdutil.WriteJSON(g.Stdout, result)
		}
		fmt.Fprintf(g.Stdout, "Cleared %d inspected pack(s) from the search index.\n", result.Removed)
		return nil
	}
	if c.URL != "" && c.Input != "" {
		return fmt.Errorf("--url and input argument are mutually exclusive")
	}
	if c.URL == "" && strings.TrimSpace(c.Input) == "" {
		return fmt.Errorf("pack name, path, or URL is required")
	}
	req := app.PackInspectRequest{
		ConfigDir:    cfgDir,
		Input:        c.Input,
		URL:          c.URL,
		Archive:      c.Archive,
		RegistryPath: c.Registry,
		Name:         c.Name,
		Ref:          c.Ref,
		SubPath:      c.SubPath,
	}
	if cp := c.ContentPaths(); cp != nil {
		req.ContentPaths = cp
	}
	result, err := app.PackInspect(ctx, req)
	if err != nil {
		return err
	}
	if c.JSON {
		return cmdutil.WriteJSON(g.Stdout, result)
	}
	fmt.Fprintf(g.Stdout, "Name:        %s\n", result.Name)
	if result.Version != "" {
		fmt.Fprintf(g.Stdout, "Version:     %s\n", result.Version)
	}
	fmt.Fprintf(g.Stdout, "Source:      %s\n", result.Source)
	fmt.Fprintf(g.Stdout, "Status:      inspected\n")
	if result.Method != "" {
		fmt.Fprintf(g.Stdout, "Method:      %s\n", result.Method)
	}
	if result.Ref != "" {
		fmt.Fprintf(g.Stdout, "Ref:         %s\n", result.Ref)
	}
	fmt.Fprintf(g.Stdout, "Content:     %s\n", result.Counts.String())
	printContentList(g.Stdout, "Rules", result.Rules)
	printContentList(g.Stdout, "Agents", result.Agents)
	printContentList(g.Stdout, "Workflows", result.Workflows)
	printContentList(g.Stdout, "Skills", result.Skills)
	printContentList(g.Stdout, "Hooks", result.Hooks)
	printContentList(g.Stdout, "Plugins", result.Plugins)
	printContentList(g.Stdout, "Prompts", result.Prompts)
	printContentList(g.Stdout, "MCP", result.MCPServers)
	printContentList(g.Stdout, "Profiles", result.Profiles)
	printContentList(g.Stdout, "Registries", result.Registries)
	printContentList(g.Stdout, "Extras", result.Extras)
	printContentList(g.Stdout, "Warnings", result.Warnings)
	fmt.Fprintln(g.Stdout, "\nNext: run 'aipack search --status inspected' or 'aipack pack install' when ready.")
	return nil
}

// --- pack versions ---

type PackVersionsCmd struct {
	Name string `arg:"" help:"Name of the pack" predictor:"pack"`
	JSON bool   `help:"Emit machine-readable JSON" name:"json"`
}

func (c *PackVersionsCmd) Help() string {
	return `Lists available semver versions for a pack by querying the remote git
repository's tags. Resolves the pack's origin URL from the lockfile (if
installed) or from the registry (if not installed).

Only tags that parse as valid semver are shown. The currently installed
version is marked with a star when applicable.

Examples:
  # List versions for an installed pack
  aipack pack versions my-pack

  # List versions for a registry pack (not yet installed)
  aipack pack versions some-team-pack

  # Machine-readable JSON
  aipack pack versions my-pack --json

See also: pack install, pack update, pack show`
}

func (c *PackVersionsCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	result, err := app.PackListVersions(ctx, app.PackListVersionsRequest{
		ConfigDir: cfgDir,
		Name:      c.Name,
	})
	if err != nil {
		return err
	}

	if c.JSON {
		return cmdutil.WriteJSON(g.Stdout, result)
	}

	fmt.Fprintf(g.Stdout, "Pack:    %s\n", result.Name)
	fmt.Fprintf(g.Stdout, "Origin:  %s\n", result.Origin)
	if result.InstalledVersion != "" {
		fmt.Fprintf(g.Stdout, "Pinned:  %s\n", result.InstalledVersion)
	}
	fmt.Fprintln(g.Stdout)

	if len(result.Versions) == 0 {
		fmt.Fprintln(g.Stdout, "No semver tags found in remote.")
		return nil
	}

	fmt.Fprintln(g.Stdout, "Available versions:")
	for _, v := range result.Versions {
		marker := "  "
		if v.Installed {
			marker = "* "
		}
		fmt.Fprintf(g.Stdout, "%s%s\n", marker, v.Version)
	}
	return nil
}

// installMissingExitCode surfaces per-pack reconciliation failures as a
// non-zero exit. PackInstallMissing reports these in results, not via err,
// so callers must inspect results to fail the command.
func installMissingExitCode(results []app.PackInstallMissingResult) error {
	for _, r := range results {
		if r.Status == app.MissingStatusError || r.Status == app.MissingStatusNotInRegistry {
			return ExitError{Code: cmdutil.ExitFail}
		}
	}
	return nil
}

// printInstallMissingResults formats PackInstallMissing results for CLI output.
func printInstallMissingResults(results []app.PackInstallMissingResult, profileName string, w io.Writer) {
	var installed, present, notFound, errored int
	for _, r := range results {
		switch r.Status {
		case app.MissingStatusInstalled:
			installed++
		case app.MissingStatusPresent:
			present++
		case app.MissingStatusNotInRegistry:
			notFound++
		case app.MissingStatusError:
			errored++
		}
	}

	if installed == 0 && notFound == 0 && errored == 0 {
		fmt.Fprintf(w, "All packs in profile %q are installed.\n", profileName)
		return
	}

	for _, r := range results {
		switch r.Status {
		case app.MissingStatusNotInRegistry:
			fmt.Fprintf(w, "  %s: not in registry — %s\n", r.Pack, r.Detail)
		case app.MissingStatusError:
			fmt.Fprintf(w, "  %s: install failed — %s\n", r.Pack, r.Detail)
		}
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
	if notFound > 0 {
		fmt.Fprintln(w, "Hint: install manually or check 'aipack registry list'.")
	}
}
