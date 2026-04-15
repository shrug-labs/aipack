package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/source"
	"github.com/shrug-labs/aipack/internal/util"
)

// PackInstallRequest holds the inputs for installing a pack.
type PackInstallRequest struct {
	// PackPath is the local path to the pack directory (mutually exclusive with URL).
	PackPath string
	// URL is a git-accessible repository or pack.json URL to clone/install from (mutually exclusive with PackPath).
	URL string
	// ConfigDir is the config directory (e.g. ~/.config/aipack).
	ConfigDir string
	// Name overrides the pack name from pack.json.
	Name string
	// Link creates a symlink instead of copying (path only; ignored for URL).
	Link bool
	// Add adds a pack entry to the active profile.
	Add bool
	// Profile is the profile to add the pack to (defaults to sync-config's defaults.profile).
	Profile string

	// With specifies which bundled content categories to install. When nil
	// (default for URL installs), available bundled content is previewed
	// and then stripped — registries, extras, and profiles are removed from
	// the installed pack. Use domain.BundledAll() to accept everything. Local path
	// installs default to domain.BundledAll() when With is nil.
	With domain.BundledSet

	// Quiet marks the pack entry in the profile with quiet: true so that
	// omitted vector selectors resolve to nothing. Set from --quiet flag
	// or registry entry quiet hint.
	Quiet bool

	// ContentPaths maps content types to directories within the repo for packs
	// that don't follow the standard pack layout. Stored in sync-config.
	ContentPaths map[domain.PackCategory]string

	// SubPath is the subdirectory within a cloned repo where pack.json lives.
	// Set when installing from a registry entry that specifies a path.
	SubPath string
	// Ref is the git ref spec to checkout. Any ref shape is legitimate —
	// exact semver ("1.2.3", "v1.2.3"), partial semver ("v1", "v1.2"),
	// namespaced semver ("my-pack/v1.2.3"), commit hash, "latest" sentinel
	// (unpin), branch name, or any other git ref. The classifier
	// (source.ClassifyRef) dispatches on the ref's shape to pick between
	// semver resolution and literal checkout. Empty tracks upstream HEAD.
	Ref string

	// Test injection points:
	RunGitFn         func(ctx context.Context, args ...string) error
	URLOKFn          func(ctx context.Context, raw string) (bool, error)
	NowFn            func() time.Time
	GitHashFn        func(ctx context.Context, dir string) (string, error)       // nil = source.GitHeadHash
	ListRemoteTagsFn func(ctx context.Context, repoURL string) ([]string, error) // nil = source.ListRemoteTags
}

// PacksDir returns the canonical pack installation directory.
func PacksDir(configDir string) string {
	return filepath.Join(configDir, "packs")
}

func packStagingDir(configDir string) string {
	return filepath.Join(configDir, ".tmp", "pack-staging")
}

func makePackTempDir(configDir, pattern string) (string, error) {
	stagingDir := packStagingDir(configDir)
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(stagingDir, pattern)
}

func extractLocalPackToStaging(configDir, packDir string) (string, config.PackManifest, error) {
	boundary := util.FindRepoRoot(packDir)
	if boundary == "" {
		boundary = packDir
	}
	stagingDir := packStagingDir(configDir)
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return "", config.PackManifest{}, err
	}
	return extractPackContent(stagingDir, packDir, nil, "", boundary)
}

// PackInstall installs a pack to the canonical location and optionally adds it to a profile.
func PackInstall(ctx context.Context, req PackInstallRequest, stdout io.Writer) error {
	if req.ConfigDir == "" {
		return fmt.Errorf("config dir is required")
	}
	if req.URL != "" && req.PackPath != "" {
		return fmt.Errorf("--url and path argument are mutually exclusive")
	}
	if req.URL == "" && strings.TrimSpace(req.PackPath) == "" {
		return fmt.Errorf("either a path argument or --url is required")
	}

	// Validate profile exists early, before doing any work.
	if req.Add {
		// Auto-create default config files if missing so pack install works
		// without an explicit 'aipack init' first.
		if _, err := config.EnsureInit(req.ConfigDir); err != nil {
			return fmt.Errorf("ensuring config: %w", err)
		}
		profile := packProfileName(req.Profile)
		profilePath := filepath.Join(req.ConfigDir, "profiles", profile+".yaml")
		if _, err := os.Stat(profilePath); os.IsNotExist(err) {
			return fmt.Errorf("profile %q does not exist at %s", profile, profilePath)
		}
	}

	if req.URL != "" {
		return packInstallFromURL(ctx, req, stdout)
	}
	return packInstallFromPath(req, stdout)
}

// packInstallFromPath implements the local-path install flow (existing behavior + origin recording).
func packInstallFromPath(req PackInstallRequest, stdout io.Writer) error {
	packPath, err := filepath.Abs(req.PackPath)
	if err != nil {
		return fmt.Errorf("resolving pack path: %w", err)
	}

	// Locate pack.json — accept either the directory or a direct path to pack.json.
	packDir := packPath
	manifestPath := filepath.Join(packPath, "pack.json")
	if st, err := os.Stat(packPath); err == nil && !st.IsDir() {
		if filepath.Base(packPath) == "pack.json" {
			manifestPath = packPath
			packDir = filepath.Dir(packPath)
		} else {
			return fmt.Errorf("pack path is a file but not pack.json: %s", packPath)
		}
	}

	manifest, err := config.LoadPackManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("loading pack manifest: %w", err)
	}

	name, err := resolvePackName(req.Name, manifest.Name)
	if err != nil {
		return err
	}

	packsDir := PacksDir(req.ConfigDir)
	destDir := filepath.Join(packsDir, name)

	if err := os.MkdirAll(packsDir, 0o700); err != nil {
		return fmt.Errorf("creating packs directory: %w", err)
	}

	// Resolve effective content preferences. Local installs default to
	// domain.BundledAll() when --with is not specified.
	with := req.With
	if with == nil && req.PackPath != "" {
		with = domain.BundledAll()
	}

	// Detect when the source pack already lives inside the packs directory.
	// Without this guard, packRemoveExisting would delete the pack before
	// the symlink/copy could use it. Resolve symlinks for a reliable comparison.
	var method string
	resolvedPackDir := packDir
	if resolved, err := filepath.EvalSymlinks(packDir); err == nil {
		resolvedPackDir = resolved
	}
	resolvedDestDir := destDir
	if resolved, err := filepath.EvalSymlinks(destDir); err == nil {
		resolvedDestDir = resolved
	}
	if resolvedPackDir == resolvedDestDir {
		method = config.MethodLocal
		fmt.Fprintf(stdout, "Already installed: %s (recording in-place)\n", destDir)
	} else if req.Link {
		method = config.MethodLink
		packRemoveExisting(destDir, stdout)
		if err := createLink(packDir, destDir); err != nil {
			return fmt.Errorf("creating symlink: %w", err)
		}
		fmt.Fprintf(stdout, "Linked: %s -> %s\n", destDir, packDir)
	} else {
		method = config.MethodCopy
		// Extract to a temp dir first so repo-relative extras are
		// materialized and rewritten before the atomic install swap.
		staging, extractedManifest, err := extractLocalPackToStaging(req.ConfigDir, packDir)
		if err != nil {
			return fmt.Errorf("extracting pack: %w", err)
		}
		defer os.RemoveAll(staging)
		if err := applyWithFilter(staging, &extractedManifest, with); err != nil {
			return fmt.Errorf("applying content filter: %w", err)
		}
		if err := util.ReplaceDirAtomic(destDir, staging); err != nil {
			return fmt.Errorf("installing pack to %s: %w", destDir, err)
		}
		manifest = extractedManifest
		fmt.Fprintf(stdout, "Copied: %s -> %s\n", packDir, destDir)
	}

	now := time.Now()
	if req.NowFn != nil {
		now = req.NowFn()
	}

	approvedList, declinedList := buildPrefsLists(with)
	meta := config.InstalledPackMeta{
		Origin: packDir, Method: method, InstalledAt: now.UTC().Format(time.RFC3339),
		Approved: approvedList, Declined: declinedList,
	}
	// Populate drift-detection baseline so doctor broken_refs and
	// sync drift reports work before the first sync runs.
	meta.Resolved = buildResolvedInventory(req.ConfigDir, name, destDir, "", now, stdout)
	if err := packRecordOrigin(req.ConfigDir, name, meta); err != nil {
		fmt.Fprintf(stdout, "Warning: failed to record pack origin: %v\n", err)
	}

	// Record content integrity hashes for copy installs.
	if method == config.MethodCopy {
		if _, err := saveIntegrity(destDir); err != nil {
			fmt.Fprintf(stdout, "Warning: failed to record integrity: %v\n", err)
		}
	}

	installBundledContent(req.ConfigDir, destDir, manifest, with, stdout)

	if req.Add {
		if err := PackAdd(req.ConfigDir, packProfileName(req.Profile), name, req.Quiet, stdout); err != nil {
			return fmt.Errorf("adding pack to profile: %w", err)
		}
	}

	// Index the pack so search works immediately without requiring sync.
	_ = indexInstalledPack(req.ConfigDir, name, destDir)

	return nil
}

// packWarnMCPServers prints a prominent warning when a pack defines MCP servers.
func packWarnMCPServers(manifest config.PackManifest, stdout io.Writer) {
	servers := make([]string, 0, len(manifest.MCP.Servers))
	for name := range manifest.MCP.Servers {
		servers = append(servers, name)
	}
	if len(servers) == 0 {
		return
	}
	slices.Sort(servers)
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "WARNING: This pack defines MCP servers (external tool access):")
	for _, s := range servers {
		tools := manifest.MCP.Servers[s].DefaultAllowedTools
		noun := "tools"
		if len(tools) == 1 {
			noun = "tool"
		}
		fmt.Fprintf(stdout, "  %s (%d %s)\n", s, len(tools), noun)
	}
	fmt.Fprintln(stdout, "Review MCP server definitions before running 'aipack sync'.")
	fmt.Fprintln(stdout, "")
}

// resolveSemverRef expands an exact or partial semver classification to the
// remote tag's raw spelling. For RefPartialSemver it prints a "Resolved X → Y"
// hint to stdout. Returns "" with no error for non-semver kinds so callers can
// treat the result uniformly: only overwrite their ref when resolved != "".
// displayRef is the user-facing spec used in error messages.
func resolveSemverRef(
	ctx context.Context,
	repoURL, displayRef string,
	refClass source.RefClassification,
	listFn func(ctx context.Context, repoURL string) ([]string, error),
	stdout io.Writer,
) (string, error) {
	switch refClass.Kind {
	case source.RefSemver:
		resolved, err := source.ResolveExactSemver(ctx, repoURL, refClass.Prefix, refClass.Spec, listFn)
		if err != nil {
			return "", fmt.Errorf("resolving version %q: %w", displayRef, err)
		}
		return resolved, nil
	case source.RefPartialSemver:
		resolved, err := source.ResolvePartialSemver(ctx, repoURL, refClass.Prefix, refClass.Spec, listFn)
		if err != nil {
			return "", fmt.Errorf("resolving partial version %q: %w", displayRef, err)
		}
		fmt.Fprintf(stdout, "Resolved %s → %s\n", displayRef, resolved)
		return resolved, nil
	}
	return "", nil
}

// packInstallFromURL implements the URL-based install flow via shallow git clone.
func packInstallFromURL(ctx context.Context, req PackInstallRequest, stdout io.Writer) error {
	refClass := source.ClassifyRef(req.Ref)
	switch refClass.Kind {
	case source.RefCommit:
		// Canonical short-SHA form is lowercase. Git accepts mixed case
		// for checkout, but storing uppercase in the lockfile would
		// disagree with what `git rev-parse` and `git log` print.
		req.Ref = refClass.Spec
	case source.RefLatest:
		// "latest" means unpin — track upstream HEAD.
		req.Ref = ""
	}

	// Resolve URL info: for SSH/git URLs or when SubPath/Ref/partial version
	// is pre-set, skip the HTTP probe (ProbePackURL) and go directly to git
	// operations. Partial semver is treated like an explicit ref for probe
	// purposes — the user declared git intent and the URL is assumed to be
	// a clone-able repo, not a GitHub blob URL that needs normalization.
	var info source.PackURLInfo
	if req.SubPath != "" || req.Ref != "" || refClass.Kind == source.RefPartialSemver || config.IsGitURL(req.URL, "") {
		info = source.PackURLInfo{RepoURL: req.URL, Ref: req.Ref, SubPath: req.SubPath}
	} else {
		var err error
		info, err = source.ProbePackURL(req.URL)
		if err != nil {
			return fmt.Errorf("probing URL: %w", err)
		}

		// Validate pack.json is accessible if we have a direct URL for it.
		if info.PackURL != "" {
			urlOKFn := source.URLOK
			if req.URLOKFn != nil {
				urlOKFn = req.URLOKFn
			}
			ok, err := urlOKFn(ctx, info.PackURL)
			if err != nil {
				return fmt.Errorf("pack.json check failed for %s: %w", info.PackURL, err)
			}
			if !ok {
				return fmt.Errorf("pack.json not found at %s", info.PackURL)
			}
		}
	}
	// Semver resolution: now that info.RepoURL is known, expand exact or
	// partial semver specs to the remote tag's raw spelling. This preserves
	// bare-tag remotes ("1.2.3") and namespaced spellings
	// ("my-pack/v1.2.3") unchanged for checkout.
	listFn := req.ListRemoteTagsFn
	if listFn == nil {
		listFn = source.ListRemoteTags
	}
	// Expand exact or partial semver specs to the remote tag's raw spelling.
	// Partial installs still pin to a single resolved version — they do not
	// create a channel that auto-updates.
	if resolved, err := resolveSemverRef(ctx, info.RepoURL, req.Ref, refClass, listFn, stdout); err != nil {
		return err
	} else if resolved != "" {
		req.Ref = resolved
		info.Ref = resolved
	}

	packsDir := PacksDir(req.ConfigDir)
	if err := os.MkdirAll(packsDir, 0o700); err != nil {
		return fmt.Errorf("creating packs directory: %w", err)
	}

	// Capture pre-install integrity so we can show a diff when replacing.
	var oldIntegrity IntegrityManifest
	if req.Name != "" {
		oldIntegrity, _ = loadIntegrity(filepath.Join(packsDir, req.Name))
	}

	// All remote installs use shallow clone (commit hash + version pinning).
	result, err := packShallowClone(ctx, req, info, packsDir, stdout)
	if err != nil {
		return err
	}

	// Verify pack.json version against the requested tag, if both are present.
	// This educates pack authors when their pack.json version drifts from
	// their git tags. Non-blocking — install proceeds either way.
	warnPackJSONVersionMismatch(stdout, req.Ref, result.manifest.Version)

	now := time.Now()
	if req.NowFn != nil {
		now = req.NowFn()
	}

	// Preview bundled content BEFORE filtering so the user sees what's
	// available. Filtering removes the files and clears manifest fields.
	if req.With == nil {
		packPreviewBundled(result.destDir, result.manifest, stdout)
	}

	// For URL installs, nil With means "decline all bundled categories".
	// Core content (rules, skills, agents, etc.) is always kept; bundled
	// content (profiles, registries, extras) requires explicit --with.
	effectiveWith := req.With
	if effectiveWith == nil {
		effectiveWith = domain.NewBundledSet() // empty set — declines everything
	}

	if err := applyWithFilter(result.destDir, &result.manifest, effectiveWith); err != nil {
		return fmt.Errorf("applying content filter: %w", err)
	}

	name := result.name
	approvedList, declinedList := buildPrefsLists(effectiveWith)
	meta := config.InstalledPackMeta{
		Origin: info.RepoURL, Method: result.method, InstalledAt: now.UTC().Format(time.RFC3339),
		Ref: info.Ref, SubPath: info.SubPath, CommitHash: result.commitHash,
		ContentPaths: req.ContentPaths,
		Approved:     approvedList, Declined: declinedList,
	}
	// Populate drift-detection baseline so doctor broken_refs and
	// sync drift reports work before the first sync runs. The ref is
	// captured so the next sync can report the version transition.
	meta.Resolved = buildResolvedInventory(req.ConfigDir, name, result.destDir, info.Ref, now, stdout)
	if err := packRecordOrigin(req.ConfigDir, name, meta); err != nil {
		fmt.Fprintf(stdout, "Warning: failed to record pack origin: %v\n", err)
	}

	packWarnMCPServers(result.manifest, stdout)

	// Record content integrity and show diff when replacing an existing pack.
	_, changed, _ := saveAndDiffIntegrity(result.destDir, oldIntegrity, stdout)
	if len(oldIntegrity.Files) > 0 && !changed {
		fmt.Fprintf(stdout, "Content unchanged.\n")
	}

	installBundledContent(req.ConfigDir, result.destDir, result.manifest, effectiveWith, stdout)

	if req.Add {
		if err := PackAdd(req.ConfigDir, packProfileName(req.Profile), name, req.Quiet, stdout); err != nil {
			return fmt.Errorf("adding pack to profile: %w", err)
		}
	}

	// Index the pack so search works immediately without requiring sync.
	_ = indexInstalledPack(req.ConfigDir, name, result.destDir)

	return nil
}

// packInstallResult holds the output of a remote install operation.
// The manifest is always unfiltered — callers apply applyWithFilter after
// computing any diffs they need against the full source content.
type packInstallResult struct {
	name       string
	destDir    string
	method     string
	manifest   config.PackManifest
	commitHash string
}

// packShallowClone installs a pack via shallow git clone. The clone is
// transient — content is extracted into a clean standard pack layout and the
// clone is discarded. The installed pack contains only pack content (no .git,
// no non-pack repo files).
func packShallowClone(ctx context.Context, req PackInstallRequest, info source.PackURLInfo, packsDir string, stdout io.Writer) (packInstallResult, error) {
	cloneDir, err := makePackTempDir(req.ConfigDir, "clone-*")
	if err != nil {
		return packInstallResult{}, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(cloneDir)

	cacheRefDir := source.CacheRefDir(req.ConfigDir, info.RepoURL)
	gitFn := req.RunGitFn
	if gitFn == nil {
		gitFn = source.RunGit
	}
	if err := source.EnsureCloneWithRef(ctx, info.RepoURL, cloneDir, info.Ref, cacheRefDir, gitFn); err != nil {
		return packInstallResult{}, fmt.Errorf("cloning %s: %w", info.RepoURL, err)
	}
	// Best-effort: seed the bare-repo cache from the local clone for future
	// --reference reuse. No network call — uses cloneDir as source.
	_ = source.UpdateBareCache(ctx, info.RepoURL, cloneDir, source.GitCacheDir(req.ConfigDir), gitFn)

	commitHash := resolveGitHash(ctx, cloneDir, req.GitHashFn)

	packRoot := cloneDir
	if info.SubPath != "" {
		packRoot = filepath.Join(cloneDir, info.SubPath)
	}

	// Extract content into clean staging directory. Use cloneDir as symlink
	// boundary so subpath packs can resolve symlinks to sibling directories.
	staging, manifest, err := extractPackContent(
		packStagingDir(req.ConfigDir), packRoot, req.ContentPaths, req.Name, cloneDir,
	)
	if err != nil {
		// Provide a helpful hint when subpath packs fail to find pack.json.
		if info.SubPath != "" && errors.Is(err, os.ErrNotExist) {
			if hint := listClonePacks(cloneDir); hint != "" {
				return packInstallResult{}, fmt.Errorf("path %q not found in repository; available packs: %s", info.SubPath, hint)
			}
		}
		return packInstallResult{}, err
	}
	defer os.RemoveAll(staging)

	name, err := resolvePackName(req.Name, manifest.Name)
	if err != nil {
		return packInstallResult{}, err
	}

	destDir := filepath.Join(packsDir, name)
	if err := util.ReplaceDirAtomic(destDir, staging); err != nil {
		return packInstallResult{}, fmt.Errorf("installing pack to %s: %w", destDir, err)
	}

	fmt.Fprintf(stdout, "Cloned: %s -> %s\n", req.URL, destDir)

	return packInstallResult{name: name, destDir: destDir, method: config.MethodClone, manifest: manifest, commitHash: commitHash}, nil
}

// listClonePacks returns a comma-separated list of top-level subdirectories in
// dir that contain a pack.json file. Only uses ReadDir and Stat on the
// already-cloned local temp directory — no symlink following, no recursion.
func listClonePacks(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var packs []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == ".git" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "pack.json")); err == nil {
			packs = append(packs, e.Name())
		}
	}
	return strings.Join(packs, ", ")
}

// isPinned reports whether a lockfile entry represents a pinned install.
// A pin is a ref that resolves to a fixed point — a semver tag (flat or
// namespaced) or a commit hash. Branch names and an empty ref track upstream.
func isPinned(meta config.InstalledPackMeta) bool {
	switch source.ClassifyRef(meta.Ref).Kind {
	case source.RefSemver, source.RefPartialSemver, source.RefCommit:
		return true
	}
	return false
}

// isLikelyTarballURL reports whether url has a shape that git can't clone.
// Used by the legacy http-tarball/archive migration path to short-circuit
// to a helpful error before attempting `git clone <tarball-url>`.
func isLikelyTarballURL(url string) bool {
	s := strings.ToLower(url)
	if strings.HasSuffix(s, ".tar.gz") || strings.HasSuffix(s, ".tgz") ||
		strings.HasSuffix(s, ".tar.bz2") || strings.HasSuffix(s, ".zip") {
		return true
	}
	// GitHub /archive/ paths produce tarball downloads, never git clone.
	return strings.Contains(s, "/archive/")
}

// pinLabel returns the display label for a pinned ref: the semver portion
// (prefix stripped if namespaced), or the short commit hash for raw hash
// pins.
func pinLabel(ref string) string {
	if semver := source.SemverFromRef(ref); semver != "" {
		return semver
	}
	return util.ShortHash(ref)
}

// warnPackJSONVersionMismatch prints a non-blocking warning when a pack's
// manifest version has drifted from its git tags, so authors notice the
// mismatch. No-op for commit hashes, literal refs (branches), "latest",
// or empty refs. Accepts both flat ("v1.2.3") and namespaced
// ("my-pack/v1.2.3") semver refs — extracts the semver portion before
// comparing against the manifest's plain-semver Version field.
func warnPackJSONVersionMismatch(stdout io.Writer, requestedRef, manifestVersion string) {
	requestedSemver := source.SemverFromRef(requestedRef)
	if requestedSemver == "" || manifestVersion == "" {
		return
	}
	expected := source.StripVersionPrefix(requestedSemver)
	actual := source.StripVersionPrefix(manifestVersion)
	if !source.IsSemverTag(actual) || actual == expected {
		return
	}
	fmt.Fprintf(stdout, "Warning: pack.json version (%s) does not match installed tag version (%s)\n", actual, expected)
}

// packProfileName returns the trimmed profile name, defaulting to "default".
func packProfileName(raw string) string {
	if p := strings.TrimSpace(raw); p != "" {
		return p
	}
	return "default"
}

// validatePackName rejects names containing path traversal sequences,
// path separators, or null bytes.
func validatePackName(name string) error {
	if strings.Contains(name, "..") || strings.Contains(name, "/") ||
		strings.Contains(name, "\\") || strings.Contains(name, "\x00") {
		return fmt.Errorf("invalid pack name %q: must not contain path separators or traversal sequences", name)
	}
	return nil
}

// resolvePackName returns reqName if non-empty, else manifestName, else an error.
func resolvePackName(reqName, manifestName string) (string, error) {
	if n := strings.TrimSpace(reqName); n != "" {
		if err := validatePackName(n); err != nil {
			return "", err
		}
		return n, nil
	}
	if manifestName != "" {
		if err := validatePackName(manifestName); err != nil {
			return "", err
		}
		return manifestName, nil
	}
	return "", fmt.Errorf("pack has no name and --name was not provided")
}

// resolveGitHash returns the HEAD commit hash for dir, or "" if unavailable.
// Uses gitHashFn if non-nil, otherwise falls back to source.GitHeadHash.
func resolveGitHash(ctx context.Context, dir string, gitHashFn func(context.Context, string) (string, error)) string {
	fn := gitHashFn
	if fn == nil {
		fn = source.GitHeadHash
	}
	h, err := fn(ctx, dir)
	if err != nil {
		return ""
	}
	return h
}

// inferInstallMethod returns MethodLink or MethodCopy based on the file mode.
func inferInstallMethod(mode os.FileMode) string {
	if mode&os.ModeSymlink != 0 {
		return config.MethodLink
	}
	return config.MethodCopy
}

// packRemoveExisting removes an already-installed pack at destDir, printing
// what it replaces. Used only for the symlink install path where there is no
// staging directory to swap in.
func packRemoveExisting(destDir string, stdout io.Writer) {
	if st, err := os.Lstat(destDir); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(destDir)
			fmt.Fprintf(stdout, "Replacing existing link: %s -> %s\n", destDir, target)
		} else if st.IsDir() {
			fmt.Fprintf(stdout, "Replacing existing copy: %s\n", destDir)
		}
		os.RemoveAll(destDir)
	}
}
