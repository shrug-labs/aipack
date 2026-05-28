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
//
// prefix scopes filtering to a git tag namespace. When prefix is empty, only
// flat semver tags (v1.2.3, 1.2.3) match — tags of the form "<anything>/..."
// are rejected by semver validation, so namespaced tags in a multi-pack repo
// are correctly skipped. When prefix is non-empty, only tags of the form
// "<prefix>/<semver>" match; the returned slice preserves the raw remote
// spelling including the prefix, so callers can git-checkout the tags
// unchanged.
func FilterSemverTags(tags []string, prefix string) []string {
	type semverTag struct {
		raw  string
		norm string // normalized semver form, prefix stripped
	}
	var valid []semverTag
	seen := map[string]struct{}{}
	for _, tag := range tags {
		stripped := tag
		if prefix != "" {
			rest, ok := strings.CutPrefix(tag, prefix+"/")
			if !ok {
				continue
			}
			stripped = rest
		}
		norm := NormalizeVersion(stripped)
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

// StripTagPrefix removes a namespace prefix ("<prefix>/") from a tag ref,
// returning the portion after the slash. Returns ref unchanged when prefix
// is empty, ref is empty, or ref does not start with "<prefix>/". The single
// entry point for prefix stripping across the codebase.
func StripTagPrefix(ref, prefix string) string {
	if prefix == "" || ref == "" {
		return ref
	}
	if rest, ok := strings.CutPrefix(ref, prefix+"/"); ok {
		return rest
	}
	return ref
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

// splitSemverRef decomposes a ref into (prefix, semver tail). Accepts flat
// ("v1.2.3") and namespaced ("my-pack/v1.2.3", "org/my-pack/v1.2.3") forms.
// Returns ok=false when ref is empty or has no semver-shaped tail.
func splitSemverRef(ref string) (prefix, tail string, ok bool) {
	if IsSemverTag(ref) {
		return "", ref, true
	}
	if idx := strings.LastIndex(ref, "/"); idx > 0 && idx < len(ref)-1 {
		t := ref[idx+1:]
		if IsSemverTag(t) {
			return ref[:idx], t, true
		}
	}
	return "", "", false
}

// IsSemverRef returns true if ref is a semver-shaped git tag, optionally
// namespaced by a pack prefix ("<prefix>/<semver>").
func IsSemverRef(ref string) bool {
	_, _, ok := splitSemverRef(ref)
	return ok
}

// SemverFromRef returns the semver portion of a ref, stripped of any
// namespace prefix, or "" when ref is not a semver-shaped ref.
//
//	SemverFromRef("v1.2.3")            => "v1.2.3"
//	SemverFromRef("1.2.3")             => "1.2.3"
//	SemverFromRef("my-pack/v1.2.3")    => "v1.2.3"
//	SemverFromRef("org/my-pack/1.2.3") => "1.2.3"
//	SemverFromRef("main")              => ""
func SemverFromRef(ref string) string {
	_, tail, _ := splitSemverRef(ref)
	return tail
}

// TagPrefixFromRef returns the namespace prefix of a semver-shaped ref, or
// "" when ref is flat, empty, or not a semver at all.
//
//	TagPrefixFromRef("v1.2.3")             => ""
//	TagPrefixFromRef("my-pack/v1.2.3")     => "my-pack"
//	TagPrefixFromRef("org/my-pack/v1.2.3") => "org/my-pack"
//	TagPrefixFromRef("main")               => ""
func TagPrefixFromRef(ref string) string {
	prefix, _, _ := splitSemverRef(ref)
	return prefix
}

// ResolvePartialSemver expands a partial semver reference (like "v1" or
// "v1.2") to the highest matching stable tag from the remote. Returns the
// remote tag's raw spelling. Errors when no stable tag matches.
//
// "Stable" means prereleases are skipped — a user who wants a prerelease
// must pass an exact tag. Build metadata is tolerated but ignored for
// matching.
//
// prefix scopes resolution to "<prefix>/<semver>" tags. Empty prefix matches
// flat tags only; non-empty prefix matches only namespaced tags for that
// prefix. The two modes are mutually exclusive (no fallback).
//
// If s is not a partial semver, it is returned unchanged (as a normalized
// semver string) without a network call, so callers can pass any version
// through this function safely.
func ResolvePartialSemver(ctx context.Context, repoURL, prefix, s string, listFn func(ctx context.Context, url string) ([]string, error)) (string, error) {
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
	sorted := FilterSemverTags(tags, prefix) // descending
	wantPrefix := norm + "."
	for _, tag := range sorted {
		tagNorm := NormalizeVersion(StripTagPrefix(tag, prefix))
		if semver.Prerelease(tagNorm) != "" {
			continue
		}
		if strings.HasPrefix(tagNorm, wantPrefix) {
			return tag, nil
		}
	}
	return "", fmt.Errorf("no stable semver tag matching %q in remote", s)
}

// ResolveExactSemver resolves an exact semver request to the remote's raw
// tag spelling. The caller passes a full semver version, with or without a
// v-prefix.
//
// prefix scopes resolution to "<prefix>/<semver>" tags. Empty prefix matches
// flat tags only; non-empty prefix matches only namespaced tags for that
// prefix.
func ResolveExactSemver(ctx context.Context, repoURL, prefix, s string, listFn func(ctx context.Context, url string) ([]string, error)) (string, error) {
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
	for _, tag := range FilterSemverTags(tags, prefix) {
		if NormalizeVersion(StripTagPrefix(tag, prefix)) == norm {
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

// RefKind classifies a git ref specifier — the string a user passes via
// --ref (or the deprecated --version, or the @<spec> install shorthand).
// aipack does not distinguish "version" from "ref" at the semantic layer;
// both map to this classification.
type RefKind int

const (
	RefEmpty         RefKind = iota // empty string — track upstream HEAD
	RefSemver                       // exact semver ("1.2.3"), optionally prefixed ("my-pack/v1.2.3")
	RefPartialSemver                // partial semver ("v1", "1.2"), optionally prefixed ("my-pack/v1")
	RefCommit                       // commit hash ("abc1234")
	RefLatest                       // "latest" sentinel — unpin, track HEAD
	RefLiteral                      // any other string (branch, non-semver tag, anything git can checkout)
)

// RefClassification is the structured result of ClassifyRef.
//
// Prefix is non-empty only for namespaced semver forms (e.g. Kind=RefSemver,
// Prefix="my-pack", Spec="v1.2.3" for input "my-pack/v1.2.3").
//
// Spec is the normalized form for semver kinds (v-prefixed), the lowercased
// hash for commits, the raw input for literals, and empty for Empty/Latest
// kinds.
type RefClassification struct {
	Kind   RefKind
	Prefix string
	Spec   string
}

// ClassifyRef determines what kind of git ref s represents. It never errors —
// every string is a legitimate ref attempt, interpretation is best-effort.
//
// Dispatch order:
//
//  1. Empty string → RefEmpty
//  2. "latest" (case-insensitive) → RefLatest
//  3. Commit hash (7–40 hex chars) → RefCommit
//  4. Flat semver / partial semver → RefSemver / RefPartialSemver
//  5. Namespaced semver / partial (split at last "/") → RefSemver / RefPartialSemver with Prefix set
//  6. Anything else → RefLiteral (branch, non-semver tag, unrecognized string)
//
// Ordering matters: commit-hash detection precedes semver so numeric-looking
// short hashes aren't misread as partial versions. Short numeric partial
// versions like "1" and "1.2" remain semver because they do not satisfy the
// 7-character minimum hash length.
func ClassifyRef(s string) RefClassification {
	if s == "" {
		return RefClassification{Kind: RefEmpty}
	}
	if strings.EqualFold(s, "latest") {
		return RefClassification{Kind: RefLatest}
	}
	if IsCommitHash(s) {
		return RefClassification{Kind: RefCommit, Spec: strings.ToLower(s)}
	}
	if prefix, tail, ok := splitSemverRef(s); ok {
		norm := NormalizeVersion(tail)
		kind := RefSemver
		if IsPartialSemver(norm) {
			kind = RefPartialSemver
		}
		return RefClassification{Kind: kind, Prefix: prefix, Spec: norm}
	}
	return RefClassification{Kind: RefLiteral, Spec: s}
}
