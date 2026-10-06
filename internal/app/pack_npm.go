package app

import (
	"context"
	"fmt"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/util"
)

func packUpdateNPM(ctx context.Context, name string, meta config.InstalledPackMeta, packDir string, uctx packUpdateContext, explicitPrefs bool) packUpdateOutcome {
	if meta.Plugin == nil || meta.Plugin.NPM == nil {
		return archiveUpdateFailure(name, config.MethodNPM, fmt.Errorf("installed npm package source is missing"), uctx)
	}
	ref := meta.Ref
	if uctx.ref != "" {
		ref = uctx.ref
		if ref == "latest" {
			ref = ""
		}
	}
	meta, err := refreshPluginCatalog(ctx, meta, "", ref, uctx)
	if err != nil {
		return archiveUpdateFailure(name, config.MethodNPM, err, uctx)
	}
	preparation, err := prepareArchiveUpdate(name, meta, packDir, uctx, explicitPrefs)
	if err != nil {
		return archiveUpdateFailure(name, config.MethodNPM, err, uctx)
	}
	fetched, err := packFetchArchive(ctx, PackInstallRequest{ConfigDir: uctx.configDir, URL: meta.Origin, Name: name, Ref: ref, Plugin: meta.Plugin}, uctx.lockedStdout())
	if err != nil {
		return archiveUpdateFailure(name, config.MethodNPM, err, uctx)
	}
	defer util.RemoveOwnedTree(fetched.destDir)
	comparison, err := compareArchiveUpdateCandidate(fetched, meta, preparation, uctx)
	if err != nil {
		return archiveUpdateFailure(name, config.MethodNPM, err, uctx)
	}
	if err := ctx.Err(); err != nil {
		return archiveUpdateFailure(name, config.MethodNPM, err, uctx)
	}
	outcome := applyArchiveUpdate(name, meta, packDir, fetched, preparation, comparison, uctx)
	if !uctx.dryRun && (outcome.Status == StatusUpdated || outcome.Status == StatusUpToDate) {
		if outcome.updatedMeta == nil {
			next := meta
			outcome.updatedMeta = &next
		}
		outcome.updatedMeta.Ref, outcome.updatedMeta.PackageVersion, outcome.updatedMeta.PackageArchiveHash = ref, fetched.packageVersion, fetched.archiveFetch.ByteHash
	}
	return outcome
}
