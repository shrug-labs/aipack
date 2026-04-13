package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/source"
	"github.com/shrug-labs/aipack/internal/util"
)

// packUpdateConcurrency bounds parallel packUpdateOne calls in PackUpdate.
// Clone/extract is network + disk heavy; three in flight saturates typical
// broadband while staying well under the connection-limit thresholds that
// cause git hosts to throttle or disconnect a single client.
const packUpdateConcurrency = 3

// PackUpdateRequest holds the inputs for updating pack(s).
type PackUpdateRequest struct {
	ConfigDir        string
	Name             string // empty when All=true
	All              bool
	Version          string                                                         // target version: semver, commit hash, "latest" (unpin), or "" (carry forward)
	With             domain.BundledSet                                              // approve these categories for new content; nil = carry forward only
	RunGitFn         func(ctx context.Context, args ...string) error                // test injection; nil = real git
	NowFn            func() time.Time                                               // test injection; nil = time.Now
	GitHashFn        func(ctx context.Context, dir string) (string, error)          // test injection; nil = source.GitHeadHash
	GitLsRemoteFn    func(ctx context.Context, repoURL, ref string) (string, error) // test injection; nil = source.LsRemoteHead
	ListRemoteTagsFn func(ctx context.Context, repoURL string) ([]string, error)    // test injection; nil = source.ListRemoteTags
}

// UpdateStatus is a typed status value for PackUpdateResult.
type UpdateStatus string

const (
	StatusUpdated  UpdateStatus = "updated"
	StatusUpToDate UpdateStatus = "up-to-date"
	StatusSkipped  UpdateStatus = "skipped"
	StatusError    UpdateStatus = "error"
)

// PackUpdateResult describes the outcome of updating a single pack.
type PackUpdateResult struct {
	Name              string             `json:"name"`
	Method            string             `json:"method"`
	Status            UpdateStatus       `json:"status"`
	Message           string             `json:"message"`
	CommitHash        string             `json:"commit_hash,omitempty"`
	BundledCandidates *BundledCandidates `json:"bundled_candidates,omitempty"` // non-nil when new bundled content is available

	manifest      *config.PackManifest      // loaded manifest, avoids re-reading pack.json
	updatedMeta   *config.InstalledPackMeta // built by packUpdateOne, applied by PackUpdate
	effective     domain.BundledSet         // merged approval set (carried-forward + explicit --with)
	explicitPrefs bool                      // true when sync-config had non-empty Approved/Declined
}

// packUpdateContext holds resolved dependencies for updating packs.
// Built once per PackUpdate call via newPackUpdateContext.
type packUpdateContext struct {
	packsDir         string
	tempDir          string
	configDir        string
	version          string                              // target version from --version flag; empty = carry forward
	with             domain.BundledSet                   // explicit --with from the request; nil = carry forward only
	packs            map[string]config.InstalledPackMeta // from lockfile
	registry         config.Registry                     // loaded once, reused across all packs
	runGitFn         func(ctx context.Context, args ...string) error
	nowFn            func() time.Time
	gitHashFn        func(ctx context.Context, dir string) (string, error)
	gitLsRemoteFn    func(ctx context.Context, repoURL, ref string) (string, error)
	listRemoteTagsFn func(ctx context.Context, repoURL string) ([]string, error)
	stdout           io.Writer
}

// newPackUpdateContext resolves defaults from the request and returns the
// dependency context used by packUpdateOne.
func newPackUpdateContext(req PackUpdateRequest, packs map[string]config.InstalledPackMeta, stdout io.Writer) packUpdateContext {
	runGitFn := req.RunGitFn
	if runGitFn == nil {
		runGitFn = source.RunGit
	}

	nowFn := req.NowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	lsRemoteFn := req.GitLsRemoteFn
	if lsRemoteFn == nil {
		lsRemoteFn = source.LsRemoteHead
	}
	reg, _ := config.LoadMergedRegistry(req.ConfigDir)

	listTagsFn := req.ListRemoteTagsFn
	if listTagsFn == nil {
		listTagsFn = source.ListRemoteTags
	}

	return packUpdateContext{
		packsDir:         PacksDir(req.ConfigDir),
		tempDir:          packStagingDir(req.ConfigDir),
		configDir:        req.ConfigDir,
		version:          req.Version,
		with:             req.With,
		packs:            packs,
		registry:         reg,
		runGitFn:         runGitFn,
		nowFn:            nowFn,
		gitHashFn:        req.GitHashFn,
		gitLsRemoteFn:    lsRemoteFn,
		listRemoteTagsFn: listTagsFn,
		stdout:           stdout,
	}
}

// PackUpdate refreshes one or all installed packs.
func PackUpdate(ctx context.Context, req PackUpdateRequest, stdout io.Writer) ([]PackUpdateResult, error) {
	if req.ConfigDir == "" {
		return nil, fmt.Errorf("config dir is required")
	}
	if err := source.ValidateVersionSpecifier(req.Version); err != nil {
		return nil, err
	}

	lfPath := config.LockfilePath(req.ConfigDir)
	lf, err := config.EnsureLockfileMigrated(req.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("loading lockfile: %w", err)
	}
	uctx := newPackUpdateContext(req, lf.Packs, stdout)

	var names []string
	if req.All {
		entries, err := os.ReadDir(uctx.packsDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
				names = append(names, e.Name())
			}
		}
	} else {
		names = []string{req.Name}
	}

	// uctx.packs is a read-only map after construction; uctx is copied per
	// goroutine to override stdout. The only shared filesystem contention
	// point is the bare-clone cache, which source.UpdateBareCache serializes
	// per cache key. Lockfile mutation and bundled installs run sequentially
	// in the post-phase below to preserve deterministic order.
	buffers := make([]*bytes.Buffer, len(names))
	results := make([]PackUpdateResult, len(names))
	for i := range buffers {
		buffers[i] = &bytes.Buffer{}
	}

	dispatched := parallelBounded(ctx, len(names), packUpdateConcurrency, func(i int) {
		local := uctx
		local.stdout = buffers[i]
		results[i] = packUpdateOne(ctx, names[i], local)
	})
	for j := dispatched; j < len(names); j++ {
		results[j] = PackUpdateResult{
			Name:    names[j],
			Status:  StatusError,
			Message: "cancelled",
		}
	}

	metaChanged := false
	for i, r := range results {
		if buffers[i].Len() > 0 {
			_, _ = stdout.Write(buffers[i].Bytes())
		}
		if r.updatedMeta != nil {
			lf.Packs[r.Name] = *r.updatedMeta
			metaChanged = true
		}
		if (r.Status == StatusUpdated || r.Status == StatusUpToDate) && r.manifest != nil {
			packDir := filepath.Join(uctx.packsDir, r.Name)
			// Auto-install bundled profiles/registries from the carried-forward
			// approval set when the user has expressed preferences (used --with
			// at least once). Pre-preference installs only apply on explicit
			// --with to avoid surprising users who never opted in.
			bundledWith := req.With
			if r.explicitPrefs {
				bundledWith = r.effective
			}
			installBundledContent(uctx.configDir, packDir, *r.manifest, bundledWith, stdout)
		}
	}
	if metaChanged {
		if err := config.SaveLockfile(lfPath, lf); err != nil {
			return results, fmt.Errorf("saving updated pack metadata: %w", err)
		}
	}
	return results, nil
}

func packUpdateOne(ctx context.Context, name string, uctx packUpdateContext) PackUpdateResult {
	packDir := filepath.Join(uctx.packsDir, name)
	meta, hasMeta := uctx.packs[name]
	hasExplicitPrefs := len(meta.Approved) > 0 || len(meta.Declined) > 0

	info, err := os.Lstat(packDir)
	if err != nil {
		return PackUpdateResult{Name: name, Status: StatusError, Message: fmt.Sprintf("not installed: %v", err)}
	}

	method := meta.Method
	if method == "" {
		method = inferInstallMethod(info.Mode())
	}

	switch method {
	case config.MethodClone:
		ref := meta.Ref

		switch source.ClassifyVersion(uctx.version) {
		case source.VersionSemver:
			// Move pin: resolve to the remote tag's raw spelling so bare-tag
			// remotes ("1.2.3") remain checkoutable.
			resolved, err := source.ResolveExactSemver(ctx, meta.Origin, uctx.version, uctx.listRemoteTagsFn)
			if err != nil {
				return PackUpdateResult{Name: name, Method: method, Status: StatusError,
					Message: fmt.Sprintf("resolving version %q: %v", uctx.version, err)}
			}
			ref = resolved
			uctx.version = source.StripVersionPrefix(resolved)
		case source.VersionPartialSemver:
			// Partial semver ("v1" or "v1.2"): resolve to the highest
			// matching stable tag and pin to that exact version.
			resolved, err := source.ResolvePartialSemver(ctx, meta.Origin, uctx.version, uctx.listRemoteTagsFn)
			if err != nil {
				return PackUpdateResult{Name: name, Method: method, Status: StatusError,
					Message: fmt.Sprintf("resolving partial version %q: %v", uctx.version, err)}
			}
			fmt.Fprintf(uctx.stdout, "Resolved %s → %s\n", source.NormalizeVersion(uctx.version), resolved)
			ref = resolved
			// uctx is passed by value to packUpdateOne, so this assignment
			// is goroutine-local — required because the down-stream pinMoved
			// check and metadata recording need the resolved version. If
			// packUpdateContext ever becomes a pointer receiver, this
			// becomes a data race across the parallel update workers.
			uctx.version = source.StripVersionPrefix(resolved)
		case source.VersionCommit:
			// Move pin: clone default branch, then checkout the commit.
			// Canonical short-SHA form is lowercase (see pack.go); apply
			// here too in case --version was passed mixed-case to update.
			uctx.version = strings.ToLower(uctx.version)
			ref = uctx.version
		case source.VersionLatest:
			// Unpin: clear ref so the re-clone tracks default branch HEAD.
			ref = ""
		case source.VersionNone:
			// No --version flag. If the pack is pinned, skip the update
			// and report the pin status.
			if isPinned(meta) {
				return packReportPinned(ctx, name, method, meta, uctx)
			}
		}

		// A pin move must force a re-clone + metadata rewrite even when the
		// new tag happens to resolve to the same commit as the current pin.
		// Without this, the fast paths below would print "up-to-date" and
		// silently drop the new ref/version.
		pinMoved := uctx.version != "" && ref != meta.Ref

		// Fast path: use ls-remote to check if the remote hash changed before
		// doing the expensive clone. Skip when --with adds new bundled
		// categories (need the clone to extract previously-filtered content),
		// and skip when the user is explicitly moving the pin.
		if meta.CommitHash != "" && !pinMoved {
			needsReExtract := false
			if uctx.with != nil {
				approved := metaApprovedSet(meta)
				for cat := range uctx.with {
					if !approved.Has(cat) {
						needsReExtract = true
						break
					}
				}
			}
			if !needsReExtract {
				remoteHash, lsErr := uctx.gitLsRemoteFn(ctx, meta.Origin, ref)
				if lsErr == nil && remoteHash != "" && remoteHash == meta.CommitHash {
					fmt.Fprintf(uctx.stdout, "Up-to-date (clone): %s @ %s\n", name, util.ShortHash(meta.CommitHash))
					return buildUpToDateResult(name, method,
						"already at "+util.ShortHash(meta.CommitHash),
						filepath.Join(packDir, "pack.json"), meta, uctx.with, hasExplicitPrefs)
				}
				// ls-remote failed or returned different hash — fall through to clone.
			}
		}

		oldIntegrity, _ := loadIntegrity(packDir)

		// Unified: re-clone to temp and extract clean pack (handles root,
		// subpath, and content_paths packs identically).
		tmpDir, err := makePackTempDir(uctx.configDir, "clone-*")
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}
		defer os.RemoveAll(tmpDir)
		cacheRefDir := source.CacheRefDir(uctx.configDir, meta.Origin)
		if err := source.EnsureCloneWithRef(ctx, meta.Origin, tmpDir, ref, cacheRefDir, uctx.runGitFn); err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}
		_ = source.UpdateBareCache(ctx, meta.Origin, tmpDir, source.GitCacheDir(uctx.configDir), uctx.runGitFn)

		srcRoot := tmpDir
		if meta.SubPath != "" {
			srcRoot = filepath.Join(tmpDir, meta.SubPath)
		}

		newHash := resolveGitHash(ctx, tmpDir, uctx.gitHashFn)
		if newHash != "" && newHash == meta.CommitHash {
			// Same hash, but --with may approve previously filtered content.
			var rawCandidates *BundledCandidates
			var srcManifest config.PackManifest
			approved := metaApprovedSet(meta)
			if m, err := config.LoadPackManifest(filepath.Join(srcRoot, "pack.json")); err == nil {
				srcManifest = m
				rawCandidates = diffBundledCandidates(srcManifest, approved)
			}
			effective := approved.Merge(uctx.with)
			if !rawCandidates.AnyApprovedBy(effective) {
				candidates := rawCandidates.Filter(effective)
				fmt.Fprintf(uctx.stdout, "Up-to-date (clone): %s @ %s\n", name, util.ShortHash(newHash))
				r := PackUpdateResult{Name: name, Method: method, Status: StatusUpToDate, Message: "already at " + util.ShortHash(newHash), CommitHash: newHash, BundledCandidates: candidates, manifest: &srcManifest, effective: effective, explicitPrefs: hasExplicitPrefs}
				// Persist metadata when --with adds new approvals or when an
				// explicit pin move needs to record the new ref/version.
				if uctx.with != nil || pinMoved {
					aList, dList := buildPrefsLists(effective)
					r.updatedMeta = &config.InstalledPackMeta{
						Origin: meta.Origin, Method: method, InstalledAt: meta.InstalledAt,
						Ref: ref, SubPath: meta.SubPath, CommitHash: newHash, ContentPaths: meta.ContentPaths,
						Approved: aList, Declined: dList,
					}
				}
				return r
			}
		}

		staging, newManifest, err := extractPackContent(uctx.tempDir, srcRoot, meta.ContentPaths, name, tmpDir)
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}
		defer os.RemoveAll(staging)

		candidates, effective, err := applyPreferenceFilter(staging, &newManifest, meta, uctx.with)
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}

		if err := util.ReplaceDirAtomic(packDir, staging); err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}

		aList, dList := buildPrefsLists(effective)
		_, _, _ = saveAndDiffIntegrity(packDir, oldIntegrity, uctx.stdout)
		msg := "updated"
		if newHash != "" {
			msg = util.ShortHash(newHash)
			if meta.CommitHash != "" && meta.CommitHash != newHash {
				msg = util.ShortHash(meta.CommitHash) + " -> " + util.ShortHash(newHash)
			}
		}
		fmt.Fprintf(uctx.stdout, "Updated (clone): %s %s\n", name, msg)
		return PackUpdateResult{Name: name, Method: method, Status: StatusUpdated, Message: msg, CommitHash: newHash, BundledCandidates: candidates, manifest: &newManifest, effective: effective, explicitPrefs: hasExplicitPrefs,
			updatedMeta: &config.InstalledPackMeta{
				Origin: meta.Origin, Method: method, InstalledAt: uctx.nowFn().UTC().Format(time.RFC3339),
				Ref: ref, SubPath: meta.SubPath, CommitHash: newHash, ContentPaths: meta.ContentPaths,
				Approved: aList, Declined: dList,
			}}

	case config.MethodCopy:
		origin := meta.Origin
		if origin == "" {
			return PackUpdateResult{Name: name, Method: method, Status: StatusSkipped, Message: "no origin recorded; cannot re-copy"}
		}
		if _, err := os.Stat(origin); err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: fmt.Sprintf("origin not found: %s", origin)}
		}
		oldIntegrity, _ := loadIntegrity(packDir)
		staging, copyManifest, err := extractLocalPackToStaging(uctx.configDir, origin)
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}
		defer os.RemoveAll(staging)
		candidates, effective, err := applyPreferenceFilter(staging, &copyManifest, meta, uctx.with)
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}
		if err := util.ReplaceDirAtomic(packDir, staging); err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}

		aList, dList := buildPrefsLists(effective)
		_, _, _ = saveAndDiffIntegrity(packDir, oldIntegrity, uctx.stdout)
		fmt.Fprintf(uctx.stdout, "Updated (copy): %s from %s\n", name, origin)
		return PackUpdateResult{Name: name, Method: method, Status: StatusUpdated, Message: "re-copied from " + origin, BundledCandidates: candidates, manifest: &copyManifest, effective: effective, explicitPrefs: hasExplicitPrefs,
			updatedMeta: &config.InstalledPackMeta{
				Origin: origin, Method: method, InstalledAt: uctx.nowFn().UTC().Format(time.RFC3339),
				ContentPaths: meta.ContentPaths,
				Approved:     aList, Declined: dList,
			}}

	case config.MethodArchive, config.MethodHTTPTarball:
		// Legacy tarball/archive packs are transparently migrated to clone on
		// next update. Future updates use the fast clone path with commit hash.
		origin := meta.Origin
		if origin == "" {
			return PackUpdateResult{Name: name, Method: method, Status: StatusSkipped, Message: "no origin recorded; cannot re-fetch"}
		}

		// Re-resolve through the registry to pick up ref/origin changes
		// that happened after the pack was initially installed.
		originalOrigin := origin
		ref := meta.Ref
		subPath := meta.SubPath
		if entry, ok := uctx.registry.Packs[name]; ok {
			if entry.Repo != "" {
				origin = entry.Repo
			}
			if entry.Ref != "" {
				ref = entry.Ref
			}
			if entry.Path != "" {
				subPath = entry.Path
			}
		}

		// Legacy installs may have stored either a clone-able repo URL or
		// an actual tarball URL in meta.Origin. The new code path only
		// clones, so a tarball-shaped origin without a registry remap
		// would fall through to `git clone <tarball-url>` and fail with
		// an opaque "not a git repository" error. Detect the shape and
		// return a targeted remediation message instead.
		if isLikelyTarballURL(origin) {
			return PackUpdateResult{
				Name: name, Method: method, Status: StatusError,
				Message: fmt.Sprintf("legacy %s install cannot auto-migrate to clone: origin %q is a tarball URL and the registry has no git remap. Reinstall manually with: aipack pack delete %s && aipack pack install --url <git-url>", method, originalOrigin, name),
			}
		}

		// Surface the URL transition so users can spot a registry repoint
		// (intentional or otherwise) before the new content lands.
		if origin != originalOrigin {
			fmt.Fprintf(uctx.stdout, "Migrating %s pack %s: %s -> %s\n", method, name, originalOrigin, origin)
		}

		oldIntegrity, _ := loadIntegrity(packDir)

		stageDir, err := makePackTempDir(uctx.configDir, "update-stage-*")
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}
		defer os.RemoveAll(stageDir)

		installReq := PackInstallRequest{
			URL:          origin,
			ConfigDir:    uctx.configDir,
			Ref:          ref,
			SubPath:      subPath,
			Name:         name,
			ContentPaths: meta.ContentPaths,
			RunGitFn:     uctx.runGitFn,
			GitHashFn:    uctx.gitHashFn,
		}
		urlInfo := source.PackURLInfo{RepoURL: origin, Ref: ref, SubPath: subPath}
		result, err := packShallowClone(ctx, installReq, urlInfo, stageDir, io.Discard)
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}

		// Filter staged content before installing to the final location.
		candidates, effective, err := applyPreferenceFilter(result.destDir, &result.manifest, meta, uctx.with)
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}

		if err := util.ReplaceDirAtomic(packDir, result.destDir); err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}

		_, _, _ = saveAndDiffIntegrity(packDir, oldIntegrity, uctx.stdout)
		aList, dList := buildPrefsLists(effective)
		fmt.Fprintf(uctx.stdout, "Updated (migrated %s -> clone): %s from %s\n", method, name, origin)
		return PackUpdateResult{Name: name, Method: config.MethodClone, Status: StatusUpdated, Message: "migrated from " + method + " to clone", CommitHash: result.commitHash, BundledCandidates: candidates, manifest: &result.manifest, effective: effective, explicitPrefs: hasExplicitPrefs,
			updatedMeta: &config.InstalledPackMeta{
				Origin: origin, Method: config.MethodClone, InstalledAt: uctx.nowFn().UTC().Format(time.RFC3339),
				Ref: ref, SubPath: subPath, CommitHash: result.commitHash, ContentPaths: meta.ContentPaths,
				Approved: aList, Declined: dList,
			}}

	case config.MethodLink:
		target, err := os.Readlink(packDir)
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: fmt.Sprintf("readlink: %v", err)}
		}
		if _, err := os.Stat(target); err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: fmt.Sprintf("symlink target missing: %s", target)}
		}

		fmt.Fprintf(uctx.stdout, "OK (link): %s -> %s\n", name, target)
		return buildUpToDateResult(name, method, "symlink target exists",
			filepath.Join(target, "pack.json"), meta, uctx.with, hasExplicitPrefs)

	case config.MethodLocal:
		fmt.Fprintf(uctx.stdout, "OK (local): %s\n", name)
		return PackUpdateResult{Name: name, Method: method, Status: StatusUpToDate, Message: "installed in-place"}

	default:
		if !hasMeta {
			return PackUpdateResult{Name: name, Method: method, Status: StatusSkipped, Message: "no install metadata; cannot determine update method"}
		}
		return PackUpdateResult{Name: name, Method: method, Status: StatusSkipped, Message: fmt.Sprintf("unknown method %q", method)}
	}
}

// packReportPinned reports the status of a pinned pack without updating it.
func packReportPinned(ctx context.Context, name, method string, meta config.InstalledPackMeta, uctx packUpdateContext) PackUpdateResult {
	msg := "pinned at " + pinLabel(meta.Ref)
	hint := pinHint(ctx, meta, source.IsSemverTag(meta.Ref), uctx)
	fmt.Fprintf(uctx.stdout, "Pinned (clone): %s %s%s\n", name, msg, hint)
	return PackUpdateResult{Name: name, Method: method, Status: StatusUpToDate, Message: msg + hint}
}

// pinHint returns a human-readable status hint for a pinned pack via one
// network call. On failure it returns " (drift check unavailable)" so users
// can distinguish "up-to-date" from "we don't know" — collapsing both to ""
// would mislead offline runs.
func pinHint(ctx context.Context, meta config.InstalledPackMeta, semverPin bool, uctx packUpdateContext) string {
	if meta.Origin == "" {
		return ""
	}
	const unavailable = " (drift check unavailable)"
	if semverPin {
		tags, err := uctx.listRemoteTagsFn(ctx, meta.Origin)
		if err != nil {
			return unavailable
		}
		latest := source.LatestSemverTag(source.FilterSemverTags(tags))
		if latest == "" {
			// Not a network failure — remote genuinely has no semver tags.
			// No useful drift signal; omit the hint.
			return ""
		}
		if source.NormalizeVersion(latest) == source.NormalizeVersion(meta.Ref) {
			return " (up-to-date)"
		}
		return fmt.Sprintf(" (latest: %s)", latest)
	}
	// Commit-hash pin: HEAD ls-remote tells us if the upstream branch moved.
	if meta.CommitHash == "" {
		return ""
	}
	ref := meta.Ref
	if source.IsCommitHash(ref) {
		ref = ""
	}
	remoteHash, err := uctx.gitLsRemoteFn(ctx, meta.Origin, ref)
	if err != nil {
		return unavailable
	}
	if remoteHash == "" {
		// Remote responded but returned no ref — degenerate case, omit.
		return ""
	}
	if remoteHash == meta.CommitHash {
		return " (up-to-date)"
	}
	return " (remote has moved)"
}

// buildUpToDateResult constructs a StatusUpToDate result with bundled-content
// metadata loaded from manifestPath. CommitHash is read from meta — empty for
// link packs, which don't track commits.
func buildUpToDateResult(name, method, message, manifestPath string, meta config.InstalledPackMeta, with domain.BundledSet, hasExplicitPrefs bool) PackUpdateResult {
	var manifest *config.PackManifest
	var candidates *BundledCandidates
	approved := metaApprovedSet(meta)
	if m, err := config.LoadPackManifest(manifestPath); err == nil {
		manifest = &m
		candidates = diffBundledCandidates(m, approved)
	}
	effective := approved.Merge(with)

	r := PackUpdateResult{
		Name: name, Method: method, Status: StatusUpToDate,
		Message: message, CommitHash: meta.CommitHash,
		BundledCandidates: candidates.Filter(effective),
		manifest:          manifest, effective: effective, explicitPrefs: hasExplicitPrefs,
	}
	if with != nil {
		aList, dList := buildPrefsLists(effective)
		r.updatedMeta = &config.InstalledPackMeta{
			Origin: meta.Origin, Method: method, InstalledAt: meta.InstalledAt,
			Ref: meta.Ref, SubPath: meta.SubPath, CommitHash: meta.CommitHash,
			ContentPaths: meta.ContentPaths,
			Approved:     aList, Declined: dList,
		}
	}
	return r
}
