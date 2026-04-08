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
	// Register adds a source + pack entry to the active profile.
	Register bool
	// Profile is the profile to register in (defaults to sync-config's defaults.profile).
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
	// Ref is a git ref (branch/tag) to checkout after cloning.
	// When set alongside URL, bypasses ProbePackURL and clones directly.
	Ref string

	// Test injection points:
	RunGitFn      func(ctx context.Context, args ...string) error
	HTTPTarballFn func(ctx context.Context, tarballURL, destDir, subPath string, opts source.ArchiveOpts) error
	URLOKFn       func(ctx context.Context, raw string) (bool, error)
	NowFn         func() time.Time
	GitHashFn     func(ctx context.Context, dir string) (string, error) // nil = source.GitHeadHash
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

// PackInstall installs a pack to the canonical location and optionally registers it in a profile.
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
	if req.Register {
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
		fmt.Fprintf(stdout, "Already installed: %s (registering in-place)\n", destDir)
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
	if err := packRecordOrigin(req.ConfigDir, name, config.InstalledPackMeta{
		Origin: packDir, Method: method, InstalledAt: now.UTC().Format(time.RFC3339),
		Approved: approvedList, Declined: declinedList,
	}); err != nil {
		fmt.Fprintf(stdout, "Warning: failed to record pack origin: %v\n", err)
	}

	// Record content integrity hashes for copy installs.
	if method == config.MethodCopy {
		if _, err := saveIntegrity(destDir); err != nil {
			fmt.Fprintf(stdout, "Warning: failed to record integrity: %v\n", err)
		}
	}

	installBundledContent(req.ConfigDir, destDir, manifest, with, stdout)

	if req.Register {
		if err := PackRegister(req.ConfigDir, packProfileName(req.Profile), name, req.Quiet, stdout); err != nil {
			return fmt.Errorf("registering pack in profile: %w", err)
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

// packInstallFromURL implements the URL-based install flow.
// Tries git archive (selective fetch) first; falls back to git clone if the
// remote does not support git archive --remote.
func packInstallFromURL(ctx context.Context, req PackInstallRequest, stdout io.Writer) error {
	// Resolve URL info: for SSH/git URLs or when SubPath/Ref is pre-set,
	// skip the HTTP probe (ProbePackURL) and go directly to git operations.
	var info source.PackURLInfo
	if req.SubPath != "" || req.Ref != "" || config.IsGitURL(req.URL, "") {
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
	packsDir := PacksDir(req.ConfigDir)
	if err := os.MkdirAll(packsDir, 0o700); err != nil {
		return fmt.Errorf("creating packs directory: %w", err)
	}

	// Capture pre-install integrity so we can show a diff when replacing.
	var oldIntegrity IntegrityManifest
	if req.Name != "" {
		oldIntegrity, _ = loadIntegrity(filepath.Join(packsDir, req.Name))
	}

	// Select fetch strategy based on the repository URL.
	strategy := source.SelectFetchStrategy(info.RepoURL)
	// Existing tests inject RunGitFn to control clone behavior. Without this
	// guard they'd be routed through HTTP tarball for GitHub URLs and call the
	// real network. RunGitFn without HTTPTarballFn means "test wants git path".
	if strategy == source.StrategyHTTPTarball && req.RunGitFn != nil && req.HTTPTarballFn == nil {
		strategy = source.StrategyShallowClone
	}
	var result packInstallResult
	var err error
	switch strategy {
	case source.StrategyHTTPTarball:
		result, err = packFetchHTTPTarball(ctx, req, info, packsDir, stdout)
	default:
		result, err = packShallowClone(ctx, req, info, packsDir, stdout)
	}
	if err != nil {
		return err
	}

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
	if err := packRecordOrigin(req.ConfigDir, name, config.InstalledPackMeta{
		Origin: req.URL, Method: result.method, InstalledAt: now.UTC().Format(time.RFC3339),
		Ref: info.Ref, SubPath: info.SubPath, CommitHash: result.commitHash,
		ContentPaths: req.ContentPaths,
		Approved:     approvedList, Declined: declinedList,
	}); err != nil {
		fmt.Fprintf(stdout, "Warning: failed to record pack origin: %v\n", err)
	}

	packWarnMCPServers(result.manifest, stdout)

	// Record content integrity and show diff when replacing an existing pack.
	_, changed, _ := saveAndDiffIntegrity(result.destDir, oldIntegrity, stdout)
	if len(oldIntegrity.Files) > 0 && !changed {
		fmt.Fprintf(stdout, "Content unchanged.\n")
	}

	installBundledContent(req.ConfigDir, result.destDir, result.manifest, effectiveWith, stdout)

	if req.Register {
		if err := PackRegister(req.ConfigDir, packProfileName(req.Profile), name, req.Quiet, stdout); err != nil {
			return fmt.Errorf("registering pack in profile: %w", err)
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

// packFetchHTTPTarball installs a pack by downloading a GitHub HTTP tarball.
func packFetchHTTPTarball(ctx context.Context, req PackInstallRequest, info source.PackURLInfo, packsDir string, stdout io.Writer) (packInstallResult, error) {
	tarballURL, err := source.GitHubTarballURL(info.RepoURL, info.Ref)
	if err != nil {
		return packInstallResult{}, fmt.Errorf("building tarball URL: %w", err)
	}

	tmpDir, err := makePackTempDir(req.ConfigDir, "http-tarball-*")
	if err != nil {
		return packInstallResult{}, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fmt.Fprintf(stdout, "Downloading %s...\n", tarballURL)

	fetchFn := req.HTTPTarballFn
	if fetchFn == nil {
		fetchFn = source.FetchHTTPTarball
	}
	if err := fetchFn(ctx, tarballURL, tmpDir, info.SubPath, source.ArchiveOpts{}); err != nil {
		return packInstallResult{}, fmt.Errorf("fetching tarball from %s: %w", tarballURL, err)
	}

	// Extract content into clean pack layout.
	staging, manifest, err := extractPackContent(
		packStagingDir(req.ConfigDir), tmpDir, req.ContentPaths, req.Name, "",
	)
	if err != nil {
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
	fmt.Fprintf(stdout, "Installed: %s -> %s\n", req.URL, destDir)

	// commitHash is not available from HTTP tarballs (no .git dir). Change
	// detection for this method uses the integrity manifest instead.
	return packInstallResult{name: name, destDir: destDir, method: config.MethodHTTPTarball, manifest: manifest}, nil
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
