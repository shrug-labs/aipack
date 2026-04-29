package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	ConfigDir string
	Name      string // empty when All=true
	All       bool
	// Ref is the target ref spec: semver, partial semver, namespaced
	// semver, commit hash, "latest" (unpin), branch name, or "" (carry
	// forward current pin). Any git ref shape is valid — classifier
	// dispatches. Set from either --ref or --version (CLI alias).
	Ref              string
	With             domain.BundledSet                                              // approve these categories for new content; nil = carry forward only
	Quiet            bool                                                           // suppress phase lines; terminal outcome lines still print
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

// PackUpdatePhase identifies a live step in a pack update operation. Empty
// phase marks a terminal event, where Result or Err is populated.
type PackUpdatePhase string

const (
	PackUpdatePhaseStarting       PackUpdatePhase = "starting"
	PackUpdatePhaseCheckingRemote PackUpdatePhase = "checking_remote"
	PackUpdatePhaseCloning        PackUpdatePhase = "cloning"
	PackUpdatePhaseExtracting     PackUpdatePhase = "extracting"
	PackUpdatePhaseMigrating      PackUpdatePhase = "migrating"
)

// PackUpdateEvent is emitted for TUI live progress. CLI callers pass nil and
// read the human output written to stdout instead.
type PackUpdateEvent struct {
	Pack   string
	Phase  PackUpdatePhase
	Result *PackUpdateResult
	Err    error
}

func emitPackUpdateEvent(events chan<- PackUpdateEvent, ev PackUpdateEvent) {
	if events == nil {
		return
	}
	events <- ev
}

func refreshedInstalledPackMeta(
	base config.InstalledPackMeta,
	origin string,
	method string,
	installedAt string,
	ref string,
	subPath string,
	commitHash string,
	contentPaths map[domain.PackCategory]string,
	approved []domain.BundledCategory,
	declined []domain.BundledCategory,
	resolved *domain.PackInventory,
) *config.InstalledPackMeta {
	next := base
	next.Origin = origin
	next.Method = method
	next.InstalledAt = installedAt
	next.Ref = ref
	next.SubPath = subPath
	next.CommitHash = commitHash
	next.ContentPaths = contentPaths
	next.Approved = approved
	next.Declined = declined
	next.Resolved = resolved
	return &next
}

// PackUpdateResult describes the outcome of updating a single pack.
type PackUpdateResult struct {
	Name              string             `json:"name"`
	Method            string             `json:"method"`
	Status            UpdateStatus       `json:"status"`
	Message           string             `json:"message"`
	CommitHash        string             `json:"commit_hash,omitempty"`
	BundledCandidates *BundledCandidates `json:"bundled_candidates,omitempty"` // non-nil when new bundled content is available
}

type packUpdateOutcome struct {
	PackUpdateResult
	manifest      *config.PackManifest
	updatedMeta   *config.InstalledPackMeta
	effective     domain.BundledSet // merged approval set (carried-forward + explicit --with)
	explicitPrefs bool              // true when sync-config had non-empty Approved/Declined
}

type packUpdateContext struct {
	packsDir         string
	tempDir          string
	configDir        string
	ref              string                              // target ref spec from --ref/--version; empty = carry forward current pin
	with             domain.BundledSet                   // explicit --with from the request; nil = carry forward only
	packs            map[string]config.InstalledPackMeta // from lockfile
	runGitFn         func(ctx context.Context, args ...string) error
	nowFn            func() time.Time
	gitHashFn        func(ctx context.Context, dir string) (string, error)
	gitLsRemoteFn    func(ctx context.Context, repoURL, ref string) (string, error)
	listRemoteTagsFn func(ctx context.Context, repoURL string) ([]string, error)
	quiet            bool
	stdout           io.Writer
	stdoutMu         *sync.Mutex
	events           chan<- PackUpdateEvent
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	if w.w == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func (uctx packUpdateContext) lockedStdout() io.Writer {
	if uctx.stdout == nil {
		return nil
	}
	return lockedWriter{mu: uctx.stdoutMu, w: uctx.stdout}
}

// newPackUpdateContext resolves defaults from the request and returns the
// dependency context used by packUpdateOne.
func newPackUpdateContext(req PackUpdateRequest, packs map[string]config.InstalledPackMeta, stdout io.Writer, events chan<- PackUpdateEvent) packUpdateContext {
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

	listTagsFn := req.ListRemoteTagsFn
	if listTagsFn == nil {
		listTagsFn = source.ListRemoteTags
	}

	return packUpdateContext{
		packsDir:         PacksDir(req.ConfigDir),
		tempDir:          packStagingDir(req.ConfigDir),
		configDir:        req.ConfigDir,
		ref:              req.Ref,
		with:             req.With,
		packs:            packs,
		runGitFn:         runGitFn,
		nowFn:            nowFn,
		gitHashFn:        req.GitHashFn,
		gitLsRemoteFn:    lsRemoteFn,
		listRemoteTagsFn: listTagsFn,
		quiet:            req.Quiet,
		stdout:           stdout,
		stdoutMu:         &sync.Mutex{},
		events:           events,
	}
}

// PackUpdate refreshes one or all installed packs.
func PackUpdate(ctx context.Context, req PackUpdateRequest, stdout io.Writer, events chan<- PackUpdateEvent) ([]PackUpdateResult, error) {
	if req.ConfigDir == "" {
		return nil, fmt.Errorf("config dir is required")
	}

	lfPath := config.LockfilePath(req.ConfigDir)
	lf, err := config.EnsureLockfileMigrated(req.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("loading lockfile: %w", err)
	}
	uctx := newPackUpdateContext(req, lf.Packs, stdout, events)

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

	// uctx.packs is a read-only map after construction. Workers share one
	// stdout mutex so terminal outcome lines stay line-atomic under --all.
	// The post-phase below stays on the main goroutine: the lockfile is a
	// single shared file (one SaveLockfile call), and bundled installs
	// write into shared configDir/{profiles,registries} where
	// first-writer-wins on filename collisions must be stable.
	outcomes := make([]packUpdateOutcome, len(names))

	dispatched := parallelBounded(ctx, len(names), packUpdateConcurrency, func(i int) {
		outcomes[i] = packUpdateOne(ctx, names[i], uctx)
		// Workers hit by Esc-cancellation mid-clone return StatusError with
		// the underlying ctx-canceled string ("context canceled", git wraps).
		// Relabel them as "cancelled" so isCancelledResult / anyCancelled in
		// the TUI render layer treat them as cancellations, not failures.
		if errors.Is(ctx.Err(), context.Canceled) && outcomes[i].Status == StatusError {
			outcomes[i].Message = "cancelled"
		}
	})
	for j := dispatched; j < len(names); j++ {
		outcomes[j] = packUpdateOutcome{
			PackUpdateResult: PackUpdateResult{
				Name:    names[j],
				Status:  StatusError,
				Message: "cancelled",
			},
		}
	}

	results := make([]PackUpdateResult, len(outcomes))
	metaChanged := false
	checkedAt := uctx.nowFn().UTC().Format(time.RFC3339)
	for i, o := range outcomes {
		results[i] = o.PackUpdateResult
		if o.updatedMeta != nil {
			lf.Packs[o.Name] = *o.updatedMeta
			metaChanged = true
		}
		// Stamp LastCheckedAt for every dispatched probe — success,
		// up-to-date, OR error. Material evidence to the user that they
		// just probed this pack, even when content was already current.
		// Skip never-dispatched cancellations (Status=="cancelled" with
		// empty Method): we never actually probed those.
		if o.Status != "" && o.Message != "cancelled" {
			if meta, ok := lf.Packs[o.Name]; ok {
				meta.LastCheckedAt = checkedAt
				lf.Packs[o.Name] = meta
				metaChanged = true
			}
		}
		if (o.Status == StatusUpdated || o.Status == StatusUpToDate) && o.manifest != nil {
			packDir := filepath.Join(uctx.packsDir, o.Name)
			// Auto-install bundled profiles/registries from the carried-forward
			// approval set when the user has expressed preferences (used --with
			// at least once). Pre-preference installs only apply on explicit
			// --with to avoid surprising users who never opted in.
			bundledWith := req.With
			if o.explicitPrefs {
				bundledWith = o.effective
			}
			installBundledContent(uctx.configDir, packDir, *o.manifest, bundledWith, stdout)
		}
	}
	if metaChanged {
		if err := config.SaveLockfile(lfPath, lf); err != nil {
			return results, fmt.Errorf("saving updated pack metadata: %w", err)
		}
	}
	return results, nil
}

func packUpdateOne(ctx context.Context, name string, uctx packUpdateContext) packUpdateOutcome {
	packDir := filepath.Join(uctx.packsDir, name)
	meta, hasMeta := uctx.packs[name]
	hasExplicitPrefs := len(meta.Approved) > 0 || len(meta.Declined) > 0

	info, err := os.Lstat(packDir)
	if err != nil {
		updateErr := fmt.Errorf("not installed: %w", err)
		result := PackUpdateResult{Name: name, Status: StatusError, Message: fmt.Sprintf("not installed: %v", err)}
		emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: updateErr})
		return packUpdateOutcome{PackUpdateResult: result}
	}

	method := meta.Method
	if method == "" {
		method = inferInstallMethod(info.Mode())
	}

	emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Phase: PackUpdatePhaseStarting})

	switch method {
	case config.MethodClone:
		ref := meta.Ref

		// Auto-prepend the installed prefix when the user passes a bare
		// semver spec to a pack that was installed under a namespaced tag
		// convention. Lets users type `--version 1.2.3` on namespaced
		// packs without knowing (or retyping) the prefix.
		refClass := source.ClassifyRef(uctx.ref)
		if installedPrefix := source.TagPrefixFromRef(meta.Ref); installedPrefix != "" && refClass.Prefix == "" {
			switch refClass.Kind {
			case source.RefSemver, source.RefPartialSemver:
				refClass.Prefix = installedPrefix
			}
		}

		switch refClass.Kind {
		case source.RefSemver, source.RefPartialSemver:
			resolved, err := resolveSemverRef(ctx, meta.Origin, uctx.ref, refClass, uctx.listRemoteTagsFn, uctx.lockedStdout())
			if err != nil {
				result := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
				emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: err})
				return packUpdateOutcome{PackUpdateResult: result}
			}
			ref = resolved
			uctx.ref = source.StripVersionPrefix(source.SemverFromRef(resolved))
		case source.RefCommit:
			// Canonical short-SHA form is lowercase (see pack.go).
			uctx.ref = refClass.Spec
			ref = refClass.Spec
		case source.RefLatest:
			// Unpin: clear ref so the re-clone tracks default branch HEAD.
			ref = ""
		case source.RefLiteral:
			ref = refClass.Spec
		case source.RefEmpty:
			if isPinned(meta) {
				return packReportPinned(ctx, name, method, meta, uctx)
			}
		}

		// A pin move must force a re-clone + metadata rewrite even when the
		// new tag happens to resolve to the same commit as the current pin.
		// Without this, the fast paths below would print "up-to-date" and
		// silently drop the new ref/version.
		pinMoved := uctx.ref != "" && ref != meta.Ref

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
				if !uctx.quiet && uctx.stdout != nil {
					uctx.stdoutMu.Lock()
					fmt.Fprintf(uctx.stdout, "Checking remote: %s %s\n", name, meta.Origin)
					uctx.stdoutMu.Unlock()
				}
				emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Phase: PackUpdatePhaseCheckingRemote})
				remoteHash, lsErr := uctx.gitLsRemoteFn(ctx, meta.Origin, ref)
				if lsErr == nil && remoteHash != "" && remoteHash == meta.CommitHash {
					if uctx.stdout != nil {
						uctx.stdoutMu.Lock()
						fmt.Fprintf(uctx.stdout, "Up-to-date (clone): %s @ %s\n", name, util.ShortHash(meta.CommitHash))
						uctx.stdoutMu.Unlock()
					}
					outcome := buildUpToDateResult(name, method,
						"already at "+util.ShortHash(meta.CommitHash),
						filepath.Join(packDir, "pack.json"), packDir, meta.Ref, meta.CommitHash, meta, uctx, hasExplicitPrefs, false)
					emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &outcome.PackUpdateResult})
					return outcome
				}
				// ls-remote failed or returned different hash — fall through to clone.
			}
		}

		oldIntegrity, _ := loadIntegrity(packDir)

		// Unified: re-clone to temp and extract clean pack (handles root,
		// subpath, and content_paths packs identically).
		tmpDir, err := makePackTempDir(uctx.configDir, "clone-*")
		if err != nil {
			result := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: err})
			return packUpdateOutcome{PackUpdateResult: result}
		}
		defer os.RemoveAll(tmpDir)
		cacheRefDir := source.CacheRefDir(uctx.configDir, meta.Origin)
		if !uctx.quiet && uctx.stdout != nil {
			uctx.stdoutMu.Lock()
			fmt.Fprintf(uctx.stdout, "Cloning %s from %s\n", name, meta.Origin)
			uctx.stdoutMu.Unlock()
		}
		emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Phase: PackUpdatePhaseCloning})
		if err := source.EnsureCloneWithRef(ctx, meta.Origin, tmpDir, ref, cacheRefDir, uctx.runGitFn); err != nil {
			result := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: err})
			return packUpdateOutcome{PackUpdateResult: result}
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
				if uctx.stdout != nil {
					uctx.stdoutMu.Lock()
					fmt.Fprintf(uctx.stdout, "Up-to-date (clone): %s @ %s\n", name, util.ShortHash(newHash))
					uctx.stdoutMu.Unlock()
				}
				outcome := buildUpToDateResult(name, method,
					"already at "+util.ShortHash(newHash),
					filepath.Join(srcRoot, "pack.json"), packDir, ref, newHash, meta, uctx, hasExplicitPrefs, pinMoved)
				emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &outcome.PackUpdateResult})
				return outcome
			}
		}

		if !uctx.quiet && uctx.stdout != nil {
			uctx.stdoutMu.Lock()
			fmt.Fprintf(uctx.stdout, "Extracting %s\n", name)
			uctx.stdoutMu.Unlock()
		}
		emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Phase: PackUpdatePhaseExtracting})
		staging, newManifest, err := extractPackContent(uctx.tempDir, srcRoot, meta.ContentPaths, name, tmpDir)
		if err != nil {
			result := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: err})
			return packUpdateOutcome{PackUpdateResult: result}
		}
		defer os.RemoveAll(staging)

		candidates, effective, err := applyPreferenceFilter(staging, &newManifest, meta, uctx.with)
		if err != nil {
			result := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: err})
			return packUpdateOutcome{PackUpdateResult: result}
		}

		if err := util.ReplaceDirAtomic(packDir, staging); err != nil {
			result := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: err})
			return packUpdateOutcome{PackUpdateResult: result}
		}

		aList, dList := buildPrefsLists(effective)
		_, _, _ = saveAndDiffIntegrity(packDir, oldIntegrity, uctx.lockedStdout())
		msg := "updated"
		if newHash != "" {
			msg = util.ShortHash(newHash)
			if meta.CommitHash != "" && meta.CommitHash != newHash {
				msg = util.ShortHash(meta.CommitHash) + " -> " + util.ShortHash(newHash)
			}
		}
		if uctx.stdout != nil {
			uctx.stdoutMu.Lock()
			fmt.Fprintf(uctx.stdout, "Updated (clone): %s %s\n", name, msg)
			uctx.stdoutMu.Unlock()
		}
		now := uctx.nowFn()
		result := PackUpdateResult{Name: name, Method: method, Status: StatusUpdated, Message: msg, CommitHash: newHash, BundledCandidates: candidates}
		// Rebuild inventory from the just-installed packDir so drift detection
		// has an up-to-date baseline reflecting the new commit's content.
		resolved := buildResolvedInventory(uctx.configDir, name, packDir, ref, now, uctx.lockedStdout())
		outcome := packUpdateOutcome{
			PackUpdateResult: result,
			manifest:         &newManifest,
			effective:        effective,
			explicitPrefs:    hasExplicitPrefs,
			updatedMeta: refreshedInstalledPackMeta(meta, meta.Origin, method, now.UTC().Format(time.RFC3339),
				ref, meta.SubPath, newHash, meta.ContentPaths, aList, dList, resolved),
		}
		emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &outcome.PackUpdateResult})
		return outcome

	case config.MethodCopy:
		origin := meta.Origin
		if origin == "" {
			return packUpdateOutcome{PackUpdateResult: PackUpdateResult{Name: name, Method: method, Status: StatusSkipped, Message: "no origin recorded; cannot re-copy"}}
		}
		if _, err := os.Stat(origin); err != nil {
			updateErr := fmt.Errorf("origin not found: %s", origin)
			result := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: fmt.Sprintf("origin not found: %s", origin)}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: updateErr})
			return packUpdateOutcome{PackUpdateResult: result}
		}
		oldIntegrity, _ := loadIntegrity(packDir)
		staging, copyManifest, err := extractLocalPackToStaging(uctx.configDir, origin)
		if err != nil {
			result := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: err})
			return packUpdateOutcome{PackUpdateResult: result}
		}
		defer os.RemoveAll(staging)
		candidates, effective, err := applyPreferenceFilter(staging, &copyManifest, meta, uctx.with)
		if err != nil {
			result := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: err})
			return packUpdateOutcome{PackUpdateResult: result}
		}
		if err := util.ReplaceDirAtomic(packDir, staging); err != nil {
			result := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: err})
			return packUpdateOutcome{PackUpdateResult: result}
		}

		aList, dList := buildPrefsLists(effective)
		_, _, _ = saveAndDiffIntegrity(packDir, oldIntegrity, uctx.lockedStdout())
		if uctx.stdout != nil {
			uctx.stdoutMu.Lock()
			fmt.Fprintf(uctx.stdout, "Updated (copy): %s from %s\n", name, origin)
			uctx.stdoutMu.Unlock()
		}
		now := uctx.nowFn()
		result := PackUpdateResult{Name: name, Method: method, Status: StatusUpdated, Message: "re-copied from " + origin, BundledCandidates: candidates}
		outcome := packUpdateOutcome{
			PackUpdateResult: result,
			manifest:         &copyManifest,
			effective:        effective,
			explicitPrefs:    hasExplicitPrefs,
			updatedMeta: refreshedInstalledPackMeta(meta, origin, method, now.UTC().Format(time.RFC3339),
				"", "", "", meta.ContentPaths, aList, dList, buildResolvedInventory(uctx.configDir, name, packDir, "", now, uctx.lockedStdout())),
		}
		emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &outcome.PackUpdateResult})
		return outcome

	case config.MethodArchive, config.MethodHTTPTarball:
		// Legacy tarball/archive packs are transparently migrated to clone on
		// next update. Future updates use the fast clone path with commit hash.
		origin := meta.Origin
		if origin == "" {
			return packUpdateOutcome{PackUpdateResult: PackUpdateResult{Name: name, Method: method, Status: StatusSkipped, Message: "no origin recorded; cannot re-fetch"}}
		}

		// Re-resolve through the registry to pick up ref/origin changes
		// that happened after the pack was initially installed. Loaded
		// lazily here since the common MethodClone path never needs it.
		registry, _ := config.LoadMergedRegistry(uctx.configDir)
		originalOrigin := origin
		ref := meta.Ref
		subPath := meta.SubPath
		if entry, ok := registry.Packs[name]; ok {
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
			msg := fmt.Sprintf("legacy %s install cannot auto-migrate to clone: origin %q is a tarball URL and the registry has no git remap. Reinstall manually with: aipack pack delete %s && aipack pack install --url <git-url>", method, originalOrigin, name)
			updateErr := fmt.Errorf("%s", msg)
			result := PackUpdateResult{
				Name: name, Method: method, Status: StatusError,
				Message: msg,
			}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: updateErr})
			return packUpdateOutcome{PackUpdateResult: result}
		}

		// Surface the URL transition so users can spot a registry repoint
		// (intentional or otherwise) before the new content lands.
		if origin != originalOrigin {
			if !uctx.quiet && uctx.stdout != nil {
				uctx.stdoutMu.Lock()
				fmt.Fprintf(uctx.stdout, "Migrating %s pack %s: %s -> %s\n", method, name, originalOrigin, origin)
				uctx.stdoutMu.Unlock()
			}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Phase: PackUpdatePhaseMigrating})
		}

		oldIntegrity, _ := loadIntegrity(packDir)

		stageDir, err := makePackTempDir(uctx.configDir, "update-stage-*")
		if err != nil {
			result := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: err})
			return packUpdateOutcome{PackUpdateResult: result}
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
		// Suppress packShallowClone's "Cloned: ..." line here because the
		// migration emits its own terminal update line below.
		result, err := packShallowClone(ctx, installReq, urlInfo, stageDir, nil)
		if err != nil {
			updateResult := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &updateResult, Err: err})
			return packUpdateOutcome{PackUpdateResult: updateResult}
		}

		// Filter staged content before installing to the final location.
		candidates, effective, err := applyPreferenceFilter(result.destDir, &result.manifest, meta, uctx.with)
		if err != nil {
			updateResult := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &updateResult, Err: err})
			return packUpdateOutcome{PackUpdateResult: updateResult}
		}

		if err := util.ReplaceDirAtomic(packDir, result.destDir); err != nil {
			updateResult := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &updateResult, Err: err})
			return packUpdateOutcome{PackUpdateResult: updateResult}
		}

		_, _, _ = saveAndDiffIntegrity(packDir, oldIntegrity, uctx.lockedStdout())
		aList, dList := buildPrefsLists(effective)
		if uctx.stdout != nil {
			uctx.stdoutMu.Lock()
			fmt.Fprintf(uctx.stdout, "Updated (migrated %s -> clone): %s from %s\n", method, name, origin)
			uctx.stdoutMu.Unlock()
		}
		now := uctx.nowFn()
		updateResult := PackUpdateResult{Name: name, Method: config.MethodClone, Status: StatusUpdated, Message: "migrated from " + method + " to clone", CommitHash: result.commitHash, BundledCandidates: candidates}
		outcome := packUpdateOutcome{
			PackUpdateResult: updateResult,
			manifest:         &result.manifest,
			effective:        effective,
			explicitPrefs:    hasExplicitPrefs,
			updatedMeta: refreshedInstalledPackMeta(meta, origin, config.MethodClone, now.UTC().Format(time.RFC3339),
				ref, subPath, result.commitHash, meta.ContentPaths, aList, dList,
				buildResolvedInventory(uctx.configDir, name, packDir, ref, now, uctx.lockedStdout())),
		}
		emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &outcome.PackUpdateResult})
		return outcome

	case config.MethodLink:
		target, err := os.Readlink(packDir)
		if err != nil {
			updateErr := fmt.Errorf("readlink: %w", err)
			result := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: fmt.Sprintf("readlink: %v", err)}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: updateErr})
			return packUpdateOutcome{PackUpdateResult: result}
		}
		if _, err := os.Stat(target); err != nil {
			updateErr := fmt.Errorf("symlink target missing: %s", target)
			result := PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: fmt.Sprintf("symlink target missing: %s", target)}
			emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result, Err: updateErr})
			return packUpdateOutcome{PackUpdateResult: result}
		}

		if uctx.stdout != nil {
			uctx.stdoutMu.Lock()
			fmt.Fprintf(uctx.stdout, "OK (link): %s -> %s\n", name, target)
			uctx.stdoutMu.Unlock()
		}
		outcome := buildUpToDateResult(name, method, "symlink target exists",
			filepath.Join(target, "pack.json"), target, meta.Ref, meta.CommitHash, meta, uctx, hasExplicitPrefs, false)
		emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &outcome.PackUpdateResult})
		return outcome

	case config.MethodLocal:
		if uctx.stdout != nil {
			uctx.stdoutMu.Lock()
			fmt.Fprintf(uctx.stdout, "OK (local): %s\n", name)
			uctx.stdoutMu.Unlock()
		}
		outcome := packUpdateOutcome{PackUpdateResult: PackUpdateResult{Name: name, Method: method, Status: StatusUpToDate, Message: "installed in-place"}}
		// Backfill Resolved for pre-v0.22 local installs that never had
		// an inventory baseline. The pack is already on disk; no network
		// or re-extraction needed.
		if meta.Resolved == nil {
			if inv := buildResolvedInventory(uctx.configDir, name, packDir, meta.Ref, uctx.nowFn(), uctx.lockedStdout()); inv != nil {
				outcome.updatedMeta = refreshedInstalledPackMeta(meta, meta.Origin, method, meta.InstalledAt,
					meta.Ref, meta.SubPath, meta.CommitHash, meta.ContentPaths, meta.Approved, meta.Declined, inv)
			}
		}
		emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &outcome.PackUpdateResult})
		return outcome

	default:
		if !hasMeta {
			return packUpdateOutcome{PackUpdateResult: PackUpdateResult{Name: name, Method: method, Status: StatusSkipped, Message: "no install metadata; cannot determine update method"}}
		}
		return packUpdateOutcome{PackUpdateResult: PackUpdateResult{Name: name, Method: method, Status: StatusSkipped, Message: fmt.Sprintf("unknown method %q", method)}}
	}
}

// packReportPinned reports the status of a pinned pack without updating it.
func packReportPinned(ctx context.Context, name, method string, meta config.InstalledPackMeta, uctx packUpdateContext) packUpdateOutcome {
	msg := "pinned at " + pinLabel(meta.Ref)
	hint := pinHint(ctx, meta, source.IsSemverRef(meta.Ref), uctx)
	if uctx.stdout != nil {
		uctx.stdoutMu.Lock()
		fmt.Fprintf(uctx.stdout, "Pinned (clone): %s %s\n", name, msg+hint)
		uctx.stdoutMu.Unlock()
	}
	result := PackUpdateResult{Name: name, Method: method, Status: StatusUpToDate, Message: msg + hint}
	emitPackUpdateEvent(uctx.events, PackUpdateEvent{Pack: name, Result: &result})
	return packUpdateOutcome{PackUpdateResult: result}
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
		prefix := source.TagPrefixFromRef(meta.Ref)
		latest := source.LatestSemverTag(source.FilterSemverTags(tags, prefix))
		if latest == "" {
			// Not a network failure — remote genuinely has no semver tags.
			// No useful drift signal; omit the hint.
			return ""
		}
		if latest == meta.Ref {
			return " (up-to-date)"
		}
		// Display the semver portion in the drift hint — namespaced pins
		// strip the prefix so users see "v0.3.1" instead of
		// "my-pack/v0.3.1", matching the v-prefixed form used elsewhere
		// (pinLabel, PackShowEntry.PinLabel).
		return fmt.Sprintf(" (latest: %s)", source.SemverFromRef(latest))
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
//
// When meta.Resolved is nil (e.g. a pre-v0.22 install that never had a
// Resolved block), opportunistically rebuilds the inventory from disk so
// drift detection and doctor broken_refs have a baseline going forward.
// The backfill runs on the fast path (no new clone), against the already-
// installed pack content — no network, no extraction.
func buildUpToDateResult(
	name string,
	method string,
	message string,
	manifestPath string,
	inventoryRoot string,
	ref string,
	commitHash string,
	meta config.InstalledPackMeta,
	uctx packUpdateContext,
	hasExplicitPrefs bool,
	forceMeta bool,
) packUpdateOutcome {
	var manifest *config.PackManifest
	var candidates *BundledCandidates
	approved := metaApprovedSet(meta)
	if m, err := config.LoadPackManifest(manifestPath); err == nil {
		manifest = &m
		candidates = diffBundledCandidates(m, approved)
	}
	effective := approved.Merge(uctx.with)

	result := PackUpdateResult{
		Name: name, Method: method, Status: StatusUpToDate,
		Message: message, CommitHash: commitHash,
		BundledCandidates: candidates.Filter(effective),
	}
	outcome := packUpdateOutcome{
		PackUpdateResult: result,
		manifest:         manifest,
		effective:        effective,
		explicitPrefs:    hasExplicitPrefs,
	}

	var backfilled *domain.PackInventory
	if meta.Resolved == nil {
		if inventoryRoot == "" {
			inventoryRoot = filepath.Dir(manifestPath)
		}
		backfilled = buildResolvedInventory(uctx.configDir, name, inventoryRoot, ref, uctx.nowFn(), uctx.lockedStdout())
	}

	// Record metadata when either the user passed --with (effective may
	// differ from stored approvals) or we just backfilled a missing
	// inventory baseline — both require a lockfile write.
	if uctx.with != nil || forceMeta || backfilled != nil {
		aList, dList := buildPrefsLists(effective)
		resolved := meta.Resolved
		if backfilled != nil {
			resolved = backfilled
		}
		outcome.updatedMeta = refreshedInstalledPackMeta(meta, meta.Origin, method, meta.InstalledAt,
			ref, meta.SubPath, commitHash, meta.ContentPaths, aList, dList, resolved)
	}
	return outcome
}
