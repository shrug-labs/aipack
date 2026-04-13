package source

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
)

// ListRemoteTags returns all tag names from a remote git repo.
func ListRemoteTags(ctx context.Context, repoURL string) ([]string, error) {
	out, err := gitLsRemoteRaw(ctx, repoURL, "--tags")
	if err != nil {
		return nil, err
	}
	return parseLsRemoteTags(out), nil
}

// parseLsRemoteTags extracts tag names from ls-remote --tags output.
// Skips dereferenced (^{}) entries since we only need the tag names.
func parseLsRemoteTags(out []byte) []string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil
	}
	var tags []string
	for line := range strings.SplitSeq(s, "\n") {
		// Skip dereferenced annotated tag entries.
		if strings.Contains(line, "^{}") {
			continue
		}
		_, ref, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		tag := strings.TrimPrefix(ref, "refs/tags/")
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// FilterSemverTags returns only valid semver tags, normalized to v-prefix
// form for comparison while preserving the remote's raw tag spelling in the
// returned slice. Results are sorted descending (newest first).
func FilterSemverTags(tags []string) []string {
	type semverTag struct {
		raw  string
		norm string
	}
	var valid []semverTag
	seen := map[string]struct{}{}
	for _, tag := range tags {
		norm := NormalizeVersion(tag)
		if !semver.IsValid(norm) {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		valid = append(valid, semverTag{raw: tag, norm: norm})
	}
	slices.SortFunc(valid, func(a, b semverTag) int {
		return semver.Compare(b.norm, a.norm) // descending
	})
	out := make([]string, len(valid))
	for i, tag := range valid {
		out[i] = tag.raw
	}
	return out
}

// LatestSemverTag returns the first (highest) tag from a pre-sorted list,
// or "" if empty.
func LatestSemverTag(sorted []string) string {
	if len(sorted) == 0 {
		return ""
	}
	return sorted[0]
}

// NormalizeVersion ensures a version string has the "v" prefix required by
// golang.org/x/mod/semver.
func NormalizeVersion(v string) string {
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

// StripVersionPrefix removes the "v" prefix from a version string for display.
func StripVersionPrefix(v string) string {
	return strings.TrimPrefix(v, "v")
}

// IsSemverTag returns true if the string is a valid semver version
// (with or without the v-prefix).
func IsSemverTag(s string) bool {
	return semver.IsValid(NormalizeVersion(s))
}

// IsPartialSemver returns true if s is a valid semver reference that is
// missing .MINOR or .PATCH — e.g. "v1" or "v1.2". Full versions like
// "v1.2.3", "v1.2.3-beta.1", and "v1.2.3+build" return false.
func IsPartialSemver(s string) bool {
	norm := NormalizeVersion(s)
	if !semver.IsValid(norm) {
		return false
	}
	// Strip prerelease (-) and build metadata (+) to isolate the base.
	base, _, _ := strings.Cut(norm, "-")
	base, _, _ = strings.Cut(base, "+")
	return strings.Count(base, ".") < 2
}

// ResolvePartialSemver expands a partial semver reference (like "v1" or
// "v1.2") to the highest matching stable tag from the remote. Returns the
// remote tag's raw spelling. Errors when no stable tag matches.
//
// "Stable" means prereleases are skipped — a user who wants a prerelease
// must pass an exact tag. Build metadata is tolerated but ignored for
// matching.
//
// If s is not a partial semver, it is returned unchanged (as a normalized
// semver string) without a network call, so callers can pass any
// version through this function safely.
func ResolvePartialSemver(ctx context.Context, repoURL, s string, listFn func(ctx context.Context, url string) ([]string, error)) (string, error) {
	norm := NormalizeVersion(s)
	if !IsPartialSemver(norm) {
		return norm, nil
	}
	if listFn == nil {
		listFn = ListRemoteTags
	}
	tags, err := listFn(ctx, repoURL)
	if err != nil {
		return "", fmt.Errorf("listing remote tags: %w", err)
	}
	sorted := FilterSemverTags(tags) // descending
	prefix := norm + "."
	for _, tag := range sorted {
		tagNorm := NormalizeVersion(tag)
		if semver.Prerelease(tagNorm) != "" {
			continue
		}
		if strings.HasPrefix(tagNorm, prefix) {
			return tag, nil
		}
	}
	return "", fmt.Errorf("no stable semver tag matching %q in remote", s)
}

// ResolveExactSemver resolves an exact semver request to the remote's raw tag
// spelling. The caller passes a full semver version, with or without a
// v-prefix.
func ResolveExactSemver(ctx context.Context, repoURL, s string, listFn func(ctx context.Context, url string) ([]string, error)) (string, error) {
	norm := NormalizeVersion(s)
	if !IsSemverTag(norm) || IsPartialSemver(norm) {
		return norm, nil
	}
	if listFn == nil {
		listFn = ListRemoteTags
	}
	tags, err := listFn(ctx, repoURL)
	if err != nil {
		return "", fmt.Errorf("listing remote tags: %w", err)
	}
	for _, tag := range FilterSemverTags(tags) {
		if NormalizeVersion(tag) == norm {
			return tag, nil
		}
	}
	return "", fmt.Errorf("no semver tag matching %q in remote", s)
}

// IsCommitHash returns true if s looks like a git commit hash (7-40 hex
// characters, case-insensitive). Distinguishes "abc1234" (commit) from
// "1.2.3" (semver) from "develop" (branch name with non-hex characters).
// On ambiguity — a 7+ char all-hex string that could also be a branch
// name like "cafe123" — commit wins; users wanting to pin to such a
// branch must rename it. Callers that store the result should lowercase
// it first to match git's canonical short-SHA form.
func IsCommitHash(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// VersionKind classifies a version specifier.
type VersionKind int

const (
	VersionNone          VersionKind = iota // empty — tracking HEAD
	VersionSemver                           // exact semver pin ("1.2.3")
	VersionPartialSemver                    // partial semver ("v1", "1.2") — resolves to latest matching stable tag
	VersionCommit                           // commit hash pin ("abc1234")
	VersionLatest                           // "latest" sentinel — clear pin
)

// ClassifyVersion determines what kind of version specifier v is.
func ClassifyVersion(v string) VersionKind {
	if v == "" {
		return VersionNone
	}
	if strings.EqualFold(v, "latest") {
		return VersionLatest
	}
	if IsSemverTag(v) {
		if IsPartialSemver(v) {
			return VersionPartialSemver
		}
		return VersionSemver
	}
	if IsCommitHash(v) {
		return VersionCommit
	}
	return VersionNone
}

// ValidateVersionSpecifier checks whether v is an accepted --version value.
// Accepted values: exact semver, partial semver, commit hash, "latest", or
// empty string. Returns an error for unsupported strings (for example, branch
// names like "main").
func ValidateVersionSpecifier(v string) error {
	if v == "" {
		return nil
	}
	if ClassifyVersion(v) != VersionNone {
		return nil
	}
	return fmt.Errorf("invalid version %q: expected semver (1.2.3), partial semver (v1 or v1.2), commit hash, or latest", v)
}
