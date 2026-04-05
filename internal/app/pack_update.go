package app

import (
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

// PackUpdateRequest holds the inputs for updating pack(s).
type PackUpdateRequest struct {
	ConfigDir     string
	Name          string // empty when All=true
	All           bool
	With          domain.BundledSet                                     // approve these categories for new content; nil = carry forward only
	RunGitFn      func(ctx context.Context, args ...string) error       // test injection; nil = real git
	NowFn         func() time.Time                                      // test injection; nil = time.Now
	GitHashFn     func(ctx context.Context, dir string) (string, error) // test injection; nil = source.GitHeadHash
	HTTPTarballFn func(ctx context.Context, tarballURL, destDir, subPath string, opts source.ArchiveOpts) error
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
	packsDir      string
	tempDir       string
	configDir     string
	with          domain.BundledSet // explicit --with from the request; nil = carry forward only
	sc            config.SyncConfig
	registry      config.Registry // loaded once, reused across all packs
	runGitFn      func(ctx context.Context, args ...string) error
	nowFn         func() time.Time
	gitHashFn     func(ctx context.Context, dir string) (string, error)
	httpTarballFn func(ctx context.Context, tarballURL, destDir, subPath string, opts source.ArchiveOpts) error
	stdout        io.Writer
}

// newPackUpdateContext resolves defaults from the request and returns the
// dependency context used by packUpdateOne.
func newPackUpdateContext(req PackUpdateRequest, sc config.SyncConfig, stdout io.Writer) packUpdateContext {
	runGitFn := req.RunGitFn
	if runGitFn == nil {
		runGitFn = source.RunGit
	}

	nowFn := req.NowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	reg, _ := config.LoadMergedRegistry(req.ConfigDir)

	return packUpdateContext{
		packsDir:      PacksDir(req.ConfigDir),
		tempDir:       packStagingDir(req.ConfigDir),
		configDir:     req.ConfigDir,
		with:          req.With,
		sc:            sc,
		registry:      reg,
		runGitFn:      runGitFn,
		nowFn:         nowFn,
		gitHashFn:     req.GitHashFn,
		httpTarballFn: req.HTTPTarballFn,
		stdout:        stdout,
	}
}

// PackUpdate refreshes one or all installed packs.
func PackUpdate(ctx context.Context, req PackUpdateRequest, stdout io.Writer) ([]PackUpdateResult, error) {
	if req.ConfigDir == "" {
		return nil, fmt.Errorf("config dir is required")
	}

	scPath := config.SyncConfigPath(req.ConfigDir)
	sc, _ := config.LoadSyncConfig(scPath)
	uctx := newPackUpdateContext(req, sc, stdout)

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

	var results []PackUpdateResult
	metaChanged := false
	for _, name := range names {
		r := packUpdateOne(ctx, name, uctx)
		if r.updatedMeta != nil {
			if sc.InstalledPacks == nil {
				sc.InstalledPacks = make(map[string]config.InstalledPackMeta)
			}
			sc.InstalledPacks[name] = *r.updatedMeta
			metaChanged = true
		}
		if (r.Status == StatusUpdated || r.Status == StatusUpToDate) && r.manifest != nil {
			packDir := filepath.Join(uctx.packsDir, name)
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
		results = append(results, r)
	}
	if metaChanged {
		if err := config.SaveSyncConfig(scPath, sc); err != nil {
			return results, fmt.Errorf("saving updated pack metadata: %w", err)
		}
	}
	return results, nil
}

func packUpdateOne(ctx context.Context, name string, uctx packUpdateContext) PackUpdateResult {
	packDir := filepath.Join(uctx.packsDir, name)
	meta, hasMeta := uctx.sc.InstalledPacks[name]
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
		oldIntegrity, _ := loadIntegrity(packDir)

		// Unified: re-clone to temp and extract clean pack (handles root,
		// subpath, and content_paths packs identically).
		tmpDir, err := makePackTempDir(uctx.configDir, "clone-*")
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}
		defer os.RemoveAll(tmpDir)
		if err := source.EnsureCloneWith(ctx, meta.Origin, tmpDir, ref, uctx.runGitFn); err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}

		srcRoot := tmpDir
		if meta.SubPath != "" {
			srcRoot = filepath.Join(tmpDir, meta.SubPath)
		}

		newHash := resolveGitHash(ctx, tmpDir, uctx.gitHashFn)
		if newHash != "" && newHash == meta.CommitHash {
			// Content hash is unchanged, but explicit --with may still need to
			// rematerialize previously filtered bundled content.
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
				// Persist expanded preferences when --with adds new approvals.
				if uctx.with != nil {
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

		// Atomic swap: rename existing pack to a backup, move staging in,
		// then remove the backup.  If the rename-in fails we restore the backup
		// so the user never ends up with a missing pack.
		backup := packDir + ".bak"
		if err := os.Rename(packDir, backup); err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}
		if err := os.Rename(staging, packDir); err != nil {
			// Restore the backup so the installed pack is not lost.
			_ = os.Rename(backup, packDir)
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}
		_ = os.RemoveAll(backup)

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
		backup := packDir + ".bak"
		if err := os.Rename(packDir, backup); err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}
		if err := os.Rename(staging, packDir); err != nil {
			_ = os.Rename(backup, packDir)
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}
		_ = os.RemoveAll(backup)

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
		origin := meta.Origin
		if origin == "" {
			return PackUpdateResult{Name: name, Method: method, Status: StatusSkipped, Message: "no origin recorded; cannot re-fetch"}
		}

		// Re-resolve through the registry to pick up ref/origin changes
		// that happened after the pack was initially installed.
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

		oldIntegrity, _ := loadIntegrity(packDir)

		installReq := PackInstallRequest{
			URL:           origin,
			ConfigDir:     uctx.configDir,
			Ref:           ref,
			SubPath:       subPath,
			Name:          name,
			ContentPaths:  meta.ContentPaths,
			RunGitFn:      uctx.runGitFn,
			HTTPTarballFn: uctx.httpTarballFn,
			GitHashFn:     uctx.gitHashFn,
		}
		urlInfo := source.PackURLInfo{RepoURL: origin, Ref: ref, SubPath: subPath}
		var result packInstallResult
		var err error
		if method == config.MethodHTTPTarball {
			result, err = packFetchHTTPTarball(ctx, installReq, urlInfo, uctx.packsDir, io.Discard)
		} else {
			result, err = packShallowClone(ctx, installReq, urlInfo, uctx.packsDir, io.Discard)
		}
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}

		candidates, effective, err := applyPreferenceFilter(result.destDir, &result.manifest, meta, uctx.with)
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: StatusError, Message: err.Error()}
		}

		_, changed, _ := saveAndDiffIntegrity(result.destDir, oldIntegrity, uctx.stdout)
		if !changed && len(oldIntegrity.Files) > 0 {
			aList, dList := buildPrefsLists(effective)
			fmt.Fprintf(uctx.stdout, "Up-to-date (%s): %s\n", method, name)
			return PackUpdateResult{Name: name, Method: method, Status: StatusUpToDate, Message: "content unchanged", BundledCandidates: candidates, manifest: &result.manifest, effective: effective, explicitPrefs: hasExplicitPrefs,
				updatedMeta: &config.InstalledPackMeta{
					Origin: origin, Method: method, InstalledAt: meta.InstalledAt,
					Ref: ref, SubPath: subPath, CommitHash: result.commitHash, ContentPaths: meta.ContentPaths,
					Approved: aList, Declined: dList,
				}}
		}

		aList, dList := buildPrefsLists(effective)
		fmt.Fprintf(uctx.stdout, "Updated (%s): %s from %s\n", method, name, origin)
		return PackUpdateResult{Name: name, Method: method, Status: StatusUpdated, Message: "re-fetched from " + origin, BundledCandidates: candidates, manifest: &result.manifest, effective: effective, explicitPrefs: hasExplicitPrefs,
			updatedMeta: &config.InstalledPackMeta{
				Origin: origin, Method: method, InstalledAt: uctx.nowFn().UTC().Format(time.RFC3339),
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
		return PackUpdateResult{Name: name, Method: method, Status: StatusUpToDate, Message: "symlink target exists"}

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
