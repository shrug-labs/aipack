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
	// Archive treats URL as a static zip/tar archive rather than a git repository.
	Archive bool
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

	// Quiet controls the pack's install-time quiet-by-nature state, stored
	// as InstallQuiet on the lockfile. When this pack is added to any
	// profile (via --add, `pack add`, or the TUI), the new profile entry's
	// Quiet flag defaults to the lockfile's InstallQuiet unless explicitly
	// overridden.
	//
	//   nil   — no explicit flag. On first install InstallQuiet=false. On
	//           re-install preserve the existing InstallQuiet.
	//   *true — `-q` was passed. Set InstallQuiet=true; new profile adds
	//           default to quiet.
	//   *false — `--no-quiet` was passed. Set InstallQuiet=false; new
	//           profile adds default to non-quiet.
	//
	// Registry entries that declare `quiet: true` hydrate this as &true
	// when the user didn't pass a flag, so registry-installed packs inherit
	// the intended quiet-by-nature state.
	Quiet *bool

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

	// Events receives live progress events. nil suppresses streaming
	// (CLI passes nil and reads stdout instead).
	Events chan<- PackInstallEvent

	// Test injection points:
	RunGitFn         func(ctx context.Context, args ...string) error
	URLOKFn          func(ctx context.Context, raw string) (bool, error)
	NowFn            func() time.Time
	GitHashFn        func(ctx context.Context, dir string) (string, error)       // nil = source.GitHeadHash
	ListRemoteTagsFn func(ctx context.Context, repoURL string) ([]string, error) // nil = source.ListRemoteTags
}

// PackInstallPhase identifies a live step in a pack install operation.
// Empty phase marks a terminal event, where Err is set on failure or both
// Err and Phase are empty on success (Done == true on the request struct's
// matching event).
type PackInstallPhase string

const (
	PackInstallPhaseStarting   PackInstallPhase = "starting"
	PackInstallPhaseProbing    PackInstallPhase = "probing"
	PackInstallPhaseResolving  PackInstallPhase = "resolving"
	PackInstallPhaseCloning    PackInstallPhase = "cloning"
	PackInstallPhaseExtracting PackInstallPhase = "extracting"
	PackInstallPhaseLinking    PackInstallPhase = "linking"
	PackInstallPhaseCopying    PackInstallPhase = "copying"
	PackInstallPhaseIndexing   PackInstallPhase = "indexing"
)

// PackInstallEvent is emitted for TUI live progress. CLI callers leave
// req.Events nil and read the human output written to stdout instead.
//
// Phase non-empty marks an intermediate state. Phase empty + Err non-nil
// is a terminal failure; Phase empty + Done true is a terminal success.
type PackInstallEvent struct {
	// Pack is a best-effort label (req.Name when set, else input
	// path/URL). Stable for the duration of one install operation.
	Pack  string
	Phase PackInstallPhase
	Err   error
	Done  bool
}

// emitPackInstallEvent pushes ev to events when non-nil. The send is
// blocking; callers must size the channel buffer to absorb every event a
// single install emits (currently up to ~9: Starting, Probing, Resolving,
// Cloning, Extracting, Linking|Copying, Indexing, terminal Err|Done) or
// risk stalling the install goroutine.
func emitPackInstallEvent(events chan<- PackInstallEvent, ev PackInstallEvent) {
	if events == nil {
		return
	}
	events <- ev
}

// installEventLabel picks a stable label for a pack install event. req.Name
// when explicit, else the URL or local path the user supplied. Mirrors
// the labels used in stdout phase lines.
func installEventLabel(req PackInstallRequest) string {
	if req.Name != "" {
		return req.Name
	}
	if req.URL != "" {
		return req.URL
	}
	return req.PackPath
}

// PacksDir returns the canonical pack installation directory.
func PacksDir(configDir string) string {
	return filepath.Join(configDir, "packs")
}

// resolveInstallQuiet computes the InstallQuiet value to stamp on a pack's
// lockfile meta at install time. Explicit flags win; without a flag a
// re-install preserves the existing value so `pack update` and `pack
// install` without `-q` don't accidentally demote a quiet-installed pack.
func resolveInstallQuiet(configDir, packName string, override *bool) bool {
	if override != nil {
		return *override
	}
	lf, err := config.EnsureLockfileMigrated(configDir)
	if err != nil {
		return false
	}
	if meta, ok := lf.Packs[packName]; ok {
		return meta.InstallQuiet
	}
	return false
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
//
// req.Events, when non-nil, receives PackInstallEvent values for live TUI
// progress. PackInstall always emits a single terminal event (Done on
// success, Err on failure) so callers can use channel close to drive UI
// state transitions.
func PackInstall(ctx context.Context, req PackInstallRequest, stdout io.Writer) error {
	label := installEventLabel(req)
	emitPackInstallEvent(req.Events, PackInstallEvent{Pack: label, Phase: PackInstallPhaseStarting})
	err := packInstallDispatch(ctx, req, stdout)
	if err != nil {
		emitPackInstallEvent(req.Events, PackInstallEvent{Pack: label, Err: err})
	} else {
		emitPackInstallEvent(req.Events, PackInstallEvent{Pack: label, Done: true})
	}
	return err
}

func packInstallDispatch(ctx context.Context, req PackInstallRequest, stdout io.Writer) error {
	if req.ConfigDir == "" {
		return fmt.Errorf("config dir is required")
	}
	if req.Archive && req.URL == "" && req.PackPath != "" {
		req.URL = req.PackPath
		req.PackPath = ""
	}
	if !req.Archive {
		if req.URL != "" && source.IsHTTPArchiveURL(req.URL) {
			req.Archive = true
		} else if req.PackPath != "" && localPathLooksLikeArchiveFile(req.PackPath) {
			req.Archive = true
			req.URL = req.PackPath
			req.PackPath = ""
		}
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
		if req.Archive {
			return packInstallFromArchive(ctx, req, stdout)
		}
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

	if stdout != nil {
		fmt.Fprintf(stdout, "Installing %s (%s)\n", name, packPath)
	}

	packsDir := PacksDir(req.ConfigDir)
	destDir := filepath.Join(packsDir, name)

	if err := os.MkdirAll(packsDir, 0o700); err != nil {
		err = fmt.Errorf("creating packs directory: %w", err)
		if stdout != nil {
			fmt.Fprintf(stdout, "error: %s: %v\n", name, err)
		}
		return err
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
		if stdout != nil {
			fmt.Fprintf(stdout, "Already installed: %s (recording in-place)\n", destDir)
		}
	} else if req.Link {
		method = config.MethodLink
		emitPackInstallEvent(req.Events, PackInstallEvent{Pack: installEventLabel(req), Phase: PackInstallPhaseLinking})
		packRemoveExisting(destDir, stdout)
		if err := createLink(packDir, destDir); err != nil {
			err = fmt.Errorf("creating symlink: %w", err)
			if stdout != nil {
				fmt.Fprintf(stdout, "error: %s: %v\n", name, err)
			}
			return err
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "Linked: %s -> %s\n", destDir, packDir)
		}
	} else {
		method = config.MethodCopy
		emitPackInstallEvent(req.Events, PackInstallEvent{Pack: installEventLabel(req), Phase: PackInstallPhaseCopying})
		// Extract to a temp dir first so repo-relative extras are
		// materialized and rewritten before the atomic install swap.
		staging, extractedManifest, err := extractLocalPackToStaging(req.ConfigDir, packDir)
		if err != nil {
			err = fmt.Errorf("extracting pack: %w", err)
			if stdout != nil {
				fmt.Fprintf(stdout, "error: %s: %v\n", name, err)
			}
			return err
		}
		defer os.RemoveAll(staging)
		if err := applyWithFilter(staging, &extractedManifest, with); err != nil {
			err = fmt.Errorf("applying content filter: %w", err)
			if stdout != nil {
				fmt.Fprintf(stdout, "error: %s: %v\n", name, err)
			}
			return err
		}
		if err := util.ReplaceDirAtomic(destDir, staging); err != nil {
			err = fmt.Errorf("installing pack to %s: %w", destDir, err)
			if stdout != nil {
				fmt.Fprintf(stdout, "error: %s: %v\n", name, err)
			}
			return err
		}
		manifest = extractedManifest
		if stdout != nil {
			fmt.Fprintf(stdout, "Copied: %s -> %s\n", packDir, destDir)
		}
	}

	now := time.Now()
	if req.NowFn != nil {
		now = req.NowFn()
	}

	approvedList, declinedList := buildPrefsLists(with)
	meta := config.InstalledPackMeta{
		Origin: packDir, Method: method, InstalledAt: now.UTC().Format(time.RFC3339),
		Approved: approvedList, Declined: declinedList,
		InstallQuiet: resolveInstallQuiet(req.ConfigDir, name, req.Quiet),
	}
	// Populate drift-detection baseline so doctor broken_refs and
	// sync drift reports work before the first sync runs.
	meta.Resolved = buildResolvedInventory(req.ConfigDir, name, destDir, "", now, stdout)
	if err := packRecordOrigin(req.ConfigDir, name, meta); err != nil {
		if stdout != nil {
			fmt.Fprintf(stdout, "Warning: failed to record pack origin: %v\n", err)
		}
	}

	// Record content integrity hashes for copy installs.
	if method == config.MethodCopy {
		if _, err := saveIntegrity(destDir); err != nil {
			if stdout != nil {
				fmt.Fprintf(stdout, "Warning: failed to record integrity: %v\n", err)
			}
		}
	}

	installBundledContent(req.ConfigDir, destDir, manifest, with, stdout)

	if req.Add {
		if err := PackAdd(req.ConfigDir, packProfileName(req.Profile), name, req.Quiet, stdout); err != nil {
			err = fmt.Errorf("adding pack to profile: %w", err)
			if stdout != nil {
				fmt.Fprintf(stdout, "error: %s: %v\n", name, err)
			}
			return err
		}
	}

	// Index the pack so search works immediately without requiring sync.
	_ = indexInstalledPack(req.ConfigDir, name, destDir)

	return nil
}

// packWarnMCPServers emits a prominent warning when a pack defines MCP servers.
func packWarnMCPServers(manifest config.PackManifest, stdout io.Writer) {
	if len(manifest.MCP) == 0 {
		return
	}
	servers := slices.Clone(manifest.MCP)
	slices.Sort(servers)
	var sb strings.Builder
	sb.WriteString("\nWARNING: This pack defines MCP servers (external tool access):\n")
	for _, s := range servers {
		fmt.Fprintf(&sb, "  %s\n", s)
	}
	sb.WriteString("Review MCP server definitions before running 'aipack sync'.\n\n")
	if stdout != nil {
		fmt.Fprint(stdout, sb.String())
	}
}

// resolveSemverRef expands an exact or partial semver classification to the
// remote tag's raw spelling. For RefPartialSemver it prints a "Resolved X → Y"
// hint. Returns "" with no error for non-semver kinds so
// callers can treat the result uniformly: only overwrite their ref when
// resolved != "". displayRef is the user-facing spec used in error messages.
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
		if stdout != nil {
			fmt.Fprintf(stdout, "Resolved %s → %s\n", displayRef, resolved)
		}
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

	// Pack name is unknown until the manifest is read after clone; req.Name
	// is the user-supplied override (may be empty). Empty Pack renders as
	// "Installing from <url>" — see formatLine.
	if stdout != nil {
		if req.Name == "" {
			fmt.Fprintf(stdout, "Installing from %s\n", req.URL)
		} else {
			fmt.Fprintf(stdout, "Installing %s (%s)\n", req.Name, req.URL)
		}
	}

	// Resolve URL info: for SSH/git URLs or when SubPath/Ref/partial version
	// is pre-set, skip the HTTP probe (ProbePackURL) and go directly to git
	// operations. Partial semver is treated like an explicit ref for probe
	// purposes — the user declared git intent and the URL is assumed to be
	// a clone-able repo, not a GitHub blob URL that needs normalization.
	label := installEventLabel(req)
	var info source.PackURLInfo
	if req.SubPath != "" || req.Ref != "" || refClass.Kind == source.RefPartialSemver || config.IsGitURL(req.URL, "") {
		info = source.PackURLInfo{RepoURL: req.URL, Ref: req.Ref, SubPath: req.SubPath}
	} else {
		emitPackInstallEvent(req.Events, PackInstallEvent{Pack: label, Phase: PackInstallPhaseProbing})
		var err error
		info, err = source.ProbePackURL(req.URL)
		if err != nil {
			err = fmt.Errorf("probing URL: %w", err)
			if stdout != nil {
				if req.Name == "" {
					fmt.Fprintf(stdout, "error: %v\n", err)
				} else {
					fmt.Fprintf(stdout, "error: %s: %v\n", req.Name, err)
				}
			}
			return err
		}

		// Validate pack.json is accessible if we have a direct URL for it.
		if info.PackURL != "" {
			urlOKFn := source.URLOK
			if req.URLOKFn != nil {
				urlOKFn = req.URLOKFn
			}
			ok, err := urlOKFn(ctx, info.PackURL)
			if err != nil {
				err = fmt.Errorf("pack.json check failed for %s: %w", info.PackURL, err)
				if stdout != nil {
					if req.Name == "" {
						fmt.Fprintf(stdout, "error: %v\n", err)
					} else {
						fmt.Fprintf(stdout, "error: %s: %v\n", req.Name, err)
					}
				}
				return err
			}
			if !ok {
				err := fmt.Errorf("pack.json not found at %s", info.PackURL)
				if stdout != nil {
					if req.Name == "" {
						fmt.Fprintf(stdout, "error: %v\n", err)
					} else {
						fmt.Fprintf(stdout, "error: %s: %v\n", req.Name, err)
					}
				}
				return err
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
	if refClass.Kind == source.RefSemver || refClass.Kind == source.RefPartialSemver {
		emitPackInstallEvent(req.Events, PackInstallEvent{Pack: label, Phase: PackInstallPhaseResolving})
	}
	if resolved, err := resolveSemverRef(ctx, info.RepoURL, req.Ref, refClass, listFn, stdout); err != nil {
		if stdout != nil {
			if req.Name == "" {
				fmt.Fprintf(stdout, "error: %v\n", err)
			} else {
				fmt.Fprintf(stdout, "error: %s: %v\n", req.Name, err)
			}
		}
		return err
	} else if resolved != "" {
		req.Ref = resolved
		info.Ref = resolved
	}

	packsDir := PacksDir(req.ConfigDir)
	if err := os.MkdirAll(packsDir, 0o700); err != nil {
		err = fmt.Errorf("creating packs directory: %w", err)
		if stdout != nil {
			if req.Name == "" {
				fmt.Fprintf(stdout, "error: %v\n", err)
			} else {
				fmt.Fprintf(stdout, "error: %s: %v\n", req.Name, err)
			}
		}
		return err
	}

	// All remote installs use shallow clone (commit hash + version pinning).
	result, err := packShallowClone(ctx, req, info, stdout, packCloneOptions{UpdateCache: true})
	if err != nil {
		return err
	}
	defer os.RemoveAll(result.destDir)

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
		err = fmt.Errorf("applying content filter: %w", err)
		if stdout != nil {
			fmt.Fprintf(stdout, "error: %s: %v\n", result.name, err)
		}
		return err
	}

	name := result.name
	destDir := filepath.Join(packsDir, name)
	if err := util.ReplaceDirAtomic(destDir, result.destDir); err != nil {
		err = fmt.Errorf("installing pack to %s: %w", destDir, err)
		if stdout != nil {
			fmt.Fprintf(stdout, "error: %s: %v\n", name, err)
		}
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "Cloned: %s -> %s\n", req.URL, destDir)
	}

	approvedList, declinedList := buildPrefsLists(effectiveWith)
	meta := config.InstalledPackMeta{
		Origin: info.RepoURL, Method: result.method, InstalledAt: now.UTC().Format(time.RFC3339),
		Ref: info.Ref, SubPath: info.SubPath, CommitHash: result.commitHash,
		ContentPaths: req.ContentPaths,
		Approved:     approvedList, Declined: declinedList,
		InstallQuiet: resolveInstallQuiet(req.ConfigDir, name, req.Quiet),
	}
	// Populate drift-detection baseline so doctor broken_refs and
	// sync drift reports work before the first sync runs. The ref is
	// captured so the next sync can report the version transition.
	meta.Resolved = buildResolvedInventory(req.ConfigDir, name, destDir, info.Ref, now, stdout)
	if err := packRecordOrigin(req.ConfigDir, name, meta); err != nil {
		if stdout != nil {
			fmt.Fprintf(stdout, "Warning: failed to record pack origin: %v\n", err)
		}
	}

	packWarnMCPServers(result.manifest, stdout)

	// Record content integrity for tamper detection by `pack update`. The
	// diff itself is suppressed here: install is a fresh-state operation
	// in the user's mental model, and the report leaks bundled-content
	// filter side effects (pack.json field strip/unstrip and bundled file
	// add/remove from `-w` flips) that don't reflect upstream drift.
	// Reserve the integrity diff for `pack update`.
	_, _ = saveIntegrity(destDir)

	installBundledContent(req.ConfigDir, destDir, result.manifest, effectiveWith, stdout)

	if req.Add {
		if err := PackAdd(req.ConfigDir, packProfileName(req.Profile), name, req.Quiet, stdout); err != nil {
			err = fmt.Errorf("adding pack to profile: %w", err)
			if stdout != nil {
				fmt.Fprintf(stdout, "error: %s: %v\n", name, err)
			}
			return err
		}
	}

	// Index the pack so search works immediately without requiring sync.
	_ = indexInstalledPack(req.ConfigDir, name, destDir)

	return nil
}

func packInstallFromArchive(ctx context.Context, req PackInstallRequest, stdout io.Writer) error {
	if req.Ref != "" {
		return fmt.Errorf("--ref/--version is not valid with archive installs")
	}
	if stdout != nil {
		if req.Name == "" {
			fmt.Fprintf(stdout, "Installing archive from %s\n", req.URL)
		} else {
			fmt.Fprintf(stdout, "Installing %s from archive %s\n", req.Name, req.URL)
		}
	}

	packsDir := PacksDir(req.ConfigDir)
	if err := os.MkdirAll(packsDir, 0o700); err != nil {
		err = fmt.Errorf("creating packs directory: %w", err)
		if stdout != nil {
			fmt.Fprintf(stdout, "error: %v\n", err)
		}
		return err
	}

	result, err := packFetchArchive(ctx, req, stdout)
	if err != nil {
		return err
	}
	defer os.RemoveAll(result.destDir)

	now := time.Now()
	if req.NowFn != nil {
		now = req.NowFn()
	}

	if req.With == nil {
		packPreviewBundled(result.destDir, result.manifest, stdout)
	}
	effectiveWith := req.With
	if effectiveWith == nil {
		effectiveWith = domain.NewBundledSet()
	}

	if err := applyWithFilter(result.destDir, &result.manifest, effectiveWith); err != nil {
		err = fmt.Errorf("applying content filter: %w", err)
		if stdout != nil {
			fmt.Fprintf(stdout, "error: %s: %v\n", result.name, err)
		}
		return err
	}

	destDir := filepath.Join(packsDir, result.name)
	if err := util.ReplaceDirAtomic(destDir, result.destDir); err != nil {
		err = fmt.Errorf("installing pack to %s: %w", destDir, err)
		if stdout != nil {
			fmt.Fprintf(stdout, "error: %s: %v\n", result.name, err)
		}
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "Fetched archive: %s -> %s\n", req.URL, destDir)
	}

	origin := archiveOrigin(req.URL)
	approvedList, declinedList := buildPrefsLists(effectiveWith)
	meta := config.InstalledPackMeta{
		Origin: origin, Method: config.MethodArchive, InstalledAt: now.UTC().Format(time.RFC3339),
		SubPath: req.SubPath, ContentPaths: req.ContentPaths,
		Approved: approvedList, Declined: declinedList,
		InstallQuiet: resolveInstallQuiet(req.ConfigDir, result.name, req.Quiet),
	}
	meta.Resolved = buildResolvedInventory(req.ConfigDir, result.name, destDir, "", now, stdout)
	if err := packRecordOrigin(req.ConfigDir, result.name, meta); err != nil {
		if stdout != nil {
			fmt.Fprintf(stdout, "Warning: failed to record pack origin: %v\n", err)
		}
	}

	packWarnMCPServers(result.manifest, stdout)
	_, _ = saveIntegrity(destDir)
	installBundledContent(req.ConfigDir, destDir, result.manifest, effectiveWith, stdout)

	if req.Add {
		if err := PackAdd(req.ConfigDir, packProfileName(req.Profile), result.name, req.Quiet, stdout); err != nil {
			err = fmt.Errorf("adding pack to profile: %w", err)
			if stdout != nil {
				fmt.Fprintf(stdout, "error: %s: %v\n", result.name, err)
			}
			return err
		}
	}

	_ = indexInstalledPack(req.ConfigDir, result.name, destDir)
	return nil
}

func packFetchArchive(ctx context.Context, req PackInstallRequest, stdout io.Writer) (packInstallResult, error) {
	archiveDir, err := makePackTempDir(req.ConfigDir, "archive-*")
	if err != nil {
		err = fmt.Errorf("creating temp dir: %w", err)
		if stdout != nil {
			fmt.Fprintf(stdout, "error: %v\n", err)
		}
		return packInstallResult{}, err
	}
	defer os.RemoveAll(archiveDir)

	if stdout != nil {
		if req.Name == "" {
			fmt.Fprintf(stdout, "Fetching archive %s\n", req.URL)
		} else {
			fmt.Fprintf(stdout, "Fetching archive %s from %s\n", req.Name, req.URL)
		}
	}
	emitPackInstallEvent(req.Events, PackInstallEvent{Pack: installEventLabel(req), Phase: PackInstallPhaseExtracting})
	if err := source.FetchArchive(ctx, req.URL, archiveDir, source.HTTPArchiveOptions{}); err != nil {
		err = fmt.Errorf("fetching archive %s: %w", req.URL, err)
		if stdout != nil {
			if req.Name == "" {
				fmt.Fprintf(stdout, "error: %v\n", err)
			} else {
				fmt.Fprintf(stdout, "error: %s: %v\n", req.Name, err)
			}
		}
		return packInstallResult{}, err
	}

	var packRoot string
	if req.ContentPaths != nil {
		packRoot, err = resolveArchiveContentRoot(archiveDir, req.SubPath)
	} else {
		packRoot, err = resolveArchivePackRoot(archiveDir, req.SubPath)
	}
	if err != nil {
		if stdout != nil {
			if req.Name == "" {
				fmt.Fprintf(stdout, "error: %v\n", err)
			} else {
				fmt.Fprintf(stdout, "error: %s: %v\n", req.Name, err)
			}
		}
		return packInstallResult{}, err
	}

	staging, manifest, err := extractPackContent(
		packStagingDir(req.ConfigDir), packRoot, req.ContentPaths, req.Name, archiveDir,
	)
	if err != nil {
		if stdout != nil {
			if req.Name == "" {
				fmt.Fprintf(stdout, "error: %v\n", err)
			} else {
				fmt.Fprintf(stdout, "error: %s: %v\n", req.Name, err)
			}
		}
		return packInstallResult{}, err
	}

	name, err := resolvePackName(req.Name, manifest.Name)
	if err != nil {
		os.RemoveAll(staging)
		if stdout != nil {
			if req.Name == "" {
				fmt.Fprintf(stdout, "error: %v\n", err)
			} else {
				fmt.Fprintf(stdout, "error: %s: %v\n", req.Name, err)
			}
		}
		return packInstallResult{}, err
	}
	return packInstallResult{name: name, destDir: staging, method: config.MethodArchive, manifest: manifest}, nil
}

func archiveOrigin(archiveSource string) string {
	if source.IsHTTPURL(archiveSource) {
		return archiveSource
	}
	abs, err := filepath.Abs(archiveSource)
	if err != nil {
		return archiveSource
	}
	return abs
}

func localPathLooksLikeArchiveFile(path string) bool {
	if !source.LooksLikeArchive(path) {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func resolveArchivePackRoot(root, subPath string) (string, error) {
	subPath = strings.Trim(strings.TrimSpace(subPath), "/")
	if subPath != "" {
		if packRoot := archivePackRootCandidate(root, subPath); packRoot != "" {
			return packRoot, nil
		}
		if top, ok := singleTopLevelDir(root); ok {
			if packRoot := archivePackRootCandidate(top, subPath); packRoot != "" {
				return packRoot, nil
			}
		}
		if hint := listArchivePacks(root); hint != "" {
			return "", fmt.Errorf("path %q not found in archive; available packs: %s", subPath, hint)
		}
		return "", fmt.Errorf("path %q not found in archive", subPath)
	}

	if packRoot := archivePackRootCandidate(root, ""); packRoot != "" {
		return packRoot, nil
	}
	if top, ok := singleTopLevelDir(root); ok {
		if packRoot := archivePackRootCandidate(top, ""); packRoot != "" {
			return packRoot, nil
		}
	}
	if hint := listArchivePacks(root); hint != "" {
		return "", fmt.Errorf("pack.json not found at archive root; available packs: %s", hint)
	}
	return "", fmt.Errorf("pack.json not found in archive")
}

func resolveArchiveContentRoot(root, subPath string) (string, error) {
	subPath = strings.Trim(strings.TrimSpace(subPath), "/")
	if subPath != "" {
		if packRoot := archiveContentRootCandidate(root, subPath); packRoot != "" {
			return packRoot, nil
		}
		if top, ok := singleTopLevelDir(root); ok {
			if packRoot := archiveContentRootCandidate(top, subPath); packRoot != "" {
				return packRoot, nil
			}
		}
		return "", fmt.Errorf("path %q not found in archive", subPath)
	}
	if top, ok := singleTopLevelDir(root); ok {
		return top, nil
	}
	return root, nil
}

func archivePackRootCandidate(root, subPath string) string {
	candidate := root
	if subPath != "" {
		candidate = filepath.Join(root, filepath.FromSlash(subPath))
	}
	if _, err := os.Stat(filepath.Join(candidate, "pack.json")); err == nil {
		return candidate
	}
	return ""
}

func archiveContentRootCandidate(root, subPath string) string {
	candidate := root
	if subPath != "" {
		candidate = filepath.Join(root, filepath.FromSlash(subPath))
	}
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return ""
}

func singleTopLevelDir(root string) (string, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", false
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(root, entry.Name()))
		} else if entry.Name() == "pack.json" {
			return "", false
		}
	}
	if len(dirs) != 1 {
		return "", false
	}
	return dirs[0], true
}

func listArchivePacks(root string) string {
	var packs []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, entry.Name(), "pack.json")); err == nil {
			packs = append(packs, entry.Name())
			continue
		}
		children, err := os.ReadDir(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		for _, child := range children {
			if !child.IsDir() {
				continue
			}
			rel := filepath.Join(entry.Name(), child.Name())
			if _, err := os.Stat(filepath.Join(root, rel, "pack.json")); err == nil {
				packs = append(packs, filepath.ToSlash(rel))
			}
		}
	}
	slices.Sort(packs)
	return strings.Join(packs, ", ")
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

type packCloneOptions struct {
	UpdateCache bool
}

// packShallowClone installs a pack via shallow git clone. The clone is
// transient — content is extracted into a clean standard pack layout and the
// clone is discarded. The returned destDir is a staging directory; callers
// apply filtering and atomically move it to the final install location.
func packShallowClone(ctx context.Context, req PackInstallRequest, info source.PackURLInfo, stdout io.Writer, opts packCloneOptions) (packInstallResult, error) {
	cloneDir, err := makePackTempDir(req.ConfigDir, "clone-*")
	if err != nil {
		err = fmt.Errorf("creating temp dir: %w", err)
		if stdout != nil {
			if req.Name == "" {
				fmt.Fprintf(stdout, "error: %v\n", err)
			} else {
				fmt.Fprintf(stdout, "error: %s: %v\n", req.Name, err)
			}
		}
		return packInstallResult{}, err
	}
	defer os.RemoveAll(cloneDir)

	cacheRefDir := source.CacheRefDir(req.ConfigDir, info.RepoURL)
	gitFn := req.RunGitFn
	if gitFn == nil {
		gitFn = source.RunGit
	}
	if stdout != nil {
		if req.Name == "" {
			fmt.Fprintf(stdout, "Cloning from %s\n", info.RepoURL)
		} else {
			fmt.Fprintf(stdout, "Cloning %s from %s\n", req.Name, info.RepoURL)
		}
	}
	emitPackInstallEvent(req.Events, PackInstallEvent{Pack: installEventLabel(req), Phase: PackInstallPhaseCloning})
	if err := source.EnsureCloneWithRef(ctx, info.RepoURL, cloneDir, info.Ref, cacheRefDir, gitFn); err != nil {
		err = fmt.Errorf("cloning %s: %w", info.RepoURL, err)
		if stdout != nil {
			if req.Name == "" {
				fmt.Fprintf(stdout, "error: %v\n", err)
			} else {
				fmt.Fprintf(stdout, "error: %s: %v\n", req.Name, err)
			}
		}
		return packInstallResult{}, err
	}
	if opts.UpdateCache {
		// Best-effort: seed the bare-repo cache from the local clone for future
		// --reference reuse. No network call — uses cloneDir as source.
		_ = source.UpdateBareCache(ctx, info.RepoURL, cloneDir, source.GitCacheDir(req.ConfigDir), gitFn)
	}

	commitHash := resolveGitHash(ctx, cloneDir, req.GitHashFn)

	packRoot := cloneDir
	if info.SubPath != "" {
		packRoot = filepath.Join(cloneDir, info.SubPath)
	}

	// Extract content into clean staging directory. Use cloneDir as symlink
	// boundary so subpath packs can resolve symlinks to sibling directories.
	emitPackInstallEvent(req.Events, PackInstallEvent{Pack: installEventLabel(req), Phase: PackInstallPhaseExtracting})
	staging, manifest, err := extractPackContent(
		packStagingDir(req.ConfigDir), packRoot, req.ContentPaths, req.Name, cloneDir,
	)
	if err != nil {
		// Provide a helpful hint when subpath packs fail to find pack.json.
		if info.SubPath != "" && errors.Is(err, os.ErrNotExist) {
			if hint := listClonePacks(cloneDir); hint != "" {
				err = fmt.Errorf("path %q not found in repository; available packs: %s", info.SubPath, hint)
			}
		}
		if stdout != nil {
			if req.Name == "" {
				fmt.Fprintf(stdout, "error: %v\n", err)
			} else {
				fmt.Fprintf(stdout, "error: %s: %v\n", req.Name, err)
			}
		}
		return packInstallResult{}, err
	}

	name, err := resolvePackName(req.Name, manifest.Name)
	if err != nil {
		os.RemoveAll(staging)
		if stdout != nil {
			if req.Name == "" {
				fmt.Fprintf(stdout, "error: %v\n", err)
			} else {
				fmt.Fprintf(stdout, "error: %s: %v\n", req.Name, err)
			}
		}
		return packInstallResult{}, err
	}
	return packInstallResult{name: name, destDir: staging, method: config.MethodClone, manifest: manifest, commitHash: commitHash}, nil
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
	if source.LooksLikeArchive(s) || strings.HasSuffix(s, ".tar.bz2") {
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

// warnPackJSONVersionMismatch emits a non-blocking warning when a pack's
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
	if stdout != nil {
		fmt.Fprintf(stdout, "Warning: pack.json version (%s) does not match installed tag version (%s)\n", actual, expected)
	}
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

// packRemoveExisting removes an already-installed pack at destDir, emitting
// a notice for what it replaces. Used only for the symlink install path
// where there is no staging directory to swap in.
func packRemoveExisting(destDir string, stdout io.Writer) {
	if st, err := os.Lstat(destDir); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(destDir)
			if stdout != nil {
				fmt.Fprintf(stdout, "Replacing existing link: %s -> %s\n", destDir, target)
			}
		} else if st.IsDir() {
			if stdout != nil {
				fmt.Fprintf(stdout, "Replacing existing copy: %s\n", destDir)
			}
		}
		os.RemoveAll(destDir)
	}
}
