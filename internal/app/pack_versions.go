package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/source"
)

// PackListVersionsRequest holds the inputs for listing available pack versions.
type PackListVersionsRequest struct {
	ConfigDir string
	Name      string

	// ListRemoteTagsFn is a test injection point. nil = source.ListRemoteTags.
	ListRemoteTagsFn func(ctx context.Context, repoURL string) ([]string, error)
}

// PackVersion describes a single available version with an installed marker.
type PackVersion struct {
	Version   string `json:"version"`
	Installed bool   `json:"installed,omitempty"`
}

// PackListVersionsResult is the output of PackListVersions.
type PackListVersionsResult struct {
	Name             string        `json:"name"`
	Origin           string        `json:"origin"`
	InstalledVersion string        `json:"installed_version,omitempty"` // current pin from lockfile
	Versions         []PackVersion `json:"versions"`
}

// PackListVersions discovers available semver versions for a pack by listing
// remote git tags. It resolves the pack's origin URL from the lockfile (if
// installed) or from the registry (if not installed). Returns the available
// versions sorted descending (newest first), with the currently installed
// version marked when applicable.
func PackListVersions(ctx context.Context, req PackListVersionsRequest) (PackListVersionsResult, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return PackListVersionsResult{}, fmt.Errorf("pack name is required")
	}

	listFn := req.ListRemoteTagsFn
	if listFn == nil {
		listFn = source.ListRemoteTags
	}

	// Resolve origin: lockfile first, then registry.
	var origin, installedVersion string
	lf, err := config.EnsureLockfileMigrated(req.ConfigDir)
	if err != nil {
		return PackListVersionsResult{}, fmt.Errorf("loading lockfile: %w", err)
	}
	if meta, ok := lf.Packs[name]; ok {
		// Only clone installs have a remote git origin we can query for tags.
		// Local/link/copy point at filesystem paths, and ls-remote against a
		// local path either fails or succeeds against an unrelated repo.
		if meta.Method != config.MethodClone {
			return PackListVersionsResult{}, fmt.Errorf(
				"pack %q is installed via %q; version discovery requires a remote clone install",
				name, meta.Method)
		}
		origin = meta.Origin
		if source.IsSemverTag(meta.Ref) {
			installedVersion = source.StripVersionPrefix(meta.Ref)
		} else if source.IsCommitHash(meta.Ref) {
			installedVersion = meta.Ref
		}
	} else {
		entry, err := RegistryLookup(RegistryListRequest{ConfigDir: req.ConfigDir}, name)
		if err != nil {
			return PackListVersionsResult{}, fmt.Errorf("pack %q is not installed and not found in registry: %w", name, err)
		}
		origin = entry.Repo
	}

	if origin == "" {
		return PackListVersionsResult{}, fmt.Errorf("no origin URL recorded for pack %q", name)
	}

	tags, err := listFn(ctx, origin)
	if err != nil {
		return PackListVersionsResult{}, fmt.Errorf("listing remote tags: %w", err)
	}

	semverTags := source.FilterSemverTags(tags)

	versions := make([]PackVersion, 0, len(semverTags))
	installedIsSemver := source.IsSemverTag(installedVersion)
	installedRef := source.NormalizeVersion(installedVersion)
	for _, tag := range semverTags {
		versions = append(versions, PackVersion{
			Version:   tag,
			Installed: installedIsSemver && source.NormalizeVersion(tag) == installedRef,
		})
	}

	return PackListVersionsResult{
		Name:             name,
		Origin:           origin,
		InstalledVersion: installedVersion,
		Versions:         versions,
	}, nil
}
