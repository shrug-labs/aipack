package source

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseLsRemoteTags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		out  string
		want []string
	}{
		{"empty", "", nil},
		{"single lightweight", "abc123\trefs/tags/v1.0.0\n", []string{"v1.0.0"}},
		{"annotated with deref", "abc123\trefs/tags/v1.0.0\ndef456\trefs/tags/v1.0.0^{}\n", []string{"v1.0.0"}},
		{"multiple tags", "a\trefs/tags/v1.0.0\nb\trefs/tags/v2.0.0\nc\trefs/tags/v1.5.0\n", []string{"v1.0.0", "v2.0.0", "v1.5.0"}},
		{"mixed with non-semver", "a\trefs/tags/v1.0.0\nb\trefs/tags/release-20230101\nc\trefs/tags/v2.0.0\n", []string{"v1.0.0", "release-20230101", "v2.0.0"}},
		{"no tab separator", "malformed line\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseLsRemoteTags([]byte(tt.out))
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("tag[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFilterSemverTags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tags []string
		want []string
	}{
		{"empty", nil, nil},
		{"all valid", []string{"v1.0.0", "v2.0.0", "v1.5.0"}, []string{"v2.0.0", "v1.5.0", "v1.0.0"}},
		{"mixed valid and invalid", []string{"v1.0.0", "release-1", "v2.0.0", "nope"}, []string{"v2.0.0", "v1.0.0"}},
		{"without v prefix", []string{"1.0.0", "2.0.0"}, []string{"2.0.0", "1.0.0"}},
		{"preserves raw spelling while sorting semver", []string{"1.2.3", "v2.0.0", "v1.9.0"}, []string{"v2.0.0", "v1.9.0", "1.2.3"}},
		{"with prerelease", []string{"v1.0.0", "v1.1.0-beta.1", "v1.1.0"}, []string{"v1.1.0", "v1.1.0-beta.1", "v1.0.0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FilterSemverTags(tt.tags)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestLatestSemverTag(t *testing.T) {
	t.Parallel()
	if got := LatestSemverTag(nil); got != "" {
		t.Errorf("LatestSemverTag(nil) = %q, want empty", got)
	}
	if got := LatestSemverTag([]string{"v2.0.0", "v1.5.0", "v1.0.0"}); got != "v2.0.0" {
		t.Errorf("LatestSemverTag = %q, want v2.0.0", got)
	}
}

func TestNormalizeVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"1.2.3", "v1.2.3"},
		{"v1.2.3", "v1.2.3"},
		{"v0.1.0-beta", "v0.1.0-beta"},
		{"0.1.0-beta", "v0.1.0-beta"},
	}
	for _, tt := range tests {
		if got := NormalizeVersion(tt.in); got != tt.want {
			t.Errorf("NormalizeVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestStripVersionPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"v1.2.3", "1.2.3"},
		{"1.2.3", "1.2.3"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := StripVersionPrefix(tt.in); got != tt.want {
			t.Errorf("StripVersionPrefix(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsSemverTag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want bool
	}{
		{"v1.0.0", true},
		{"1.0.0", true},
		{"v1.0.0-beta.1", true},
		{"release-1", false},
		{"latest", false},
		{"main", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsSemverTag(tt.in); got != tt.want {
			t.Errorf("IsSemverTag(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestIsCommitHash(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want bool
	}{
		{"abc1234", true},
		{"aabbccdd11223344556677889900aabbccddeeff", true}, // 40 chars
		{"abc123", false},   // too short (6)
		{"abc1234x", false}, // non-hex char
		{"ABC1234", true},   // uppercase hex — accepted (real-world copy/paste from URLs)
		{"DeAdBeEf", true},  // mixed case hex
		{"", false},
		{"1.2.3", false},   // dots are not hex
		{"develop", false}, // 'v' is not hex
		{"deadbeef", true}, // 8 hex chars
	}
	for _, tt := range tests {
		if got := IsCommitHash(tt.in); got != tt.want {
			t.Errorf("IsCommitHash(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestClassifyVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want VersionKind
	}{
		{"", VersionNone},
		{"1.2.3", VersionSemver},
		{"v1.2.3", VersionSemver},
		{"v1.0.0-beta.1", VersionSemver},
		{"v1.2.3+build", VersionSemver},
		{"v1", VersionPartialSemver},
		{"1", VersionPartialSemver},
		{"v1.2", VersionPartialSemver},
		{"1.2", VersionPartialSemver},
		{"latest", VersionLatest},
		{"LATEST", VersionLatest},
		{"abc1234", VersionCommit},
		{"aabbccdd1122334455667788", VersionCommit},
		// All-hex 7+ char strings classify as commit hashes even if they
		// happen to be valid branch names — commit wins over branch on
		// ambiguity. Users who want to update to such a branch must use
		// --ref, not --version.
		{"cafe123", VersionCommit},
		{"CAFE123", VersionCommit}, // uppercase paste from a URL still classifies as commit
		{"develop", VersionNone},   // branch name, not a version
		{"main", VersionNone},
		{"release-1", VersionNone},
	}
	for _, tt := range tests {
		if got := ClassifyVersion(tt.in); got != tt.want {
			t.Errorf("ClassifyVersion(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestValidateVersionSpecifier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		wantErr bool
	}{
		{"", false},
		{"1.2.3", false},
		{"v1", false},
		{"abc1234", false},
		{"latest", false},
		{"main", true},
		{"release-1", true},
	}
	for _, tt := range tests {
		err := ValidateVersionSpecifier(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateVersionSpecifier(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
		}
	}
}

func TestIsPartialSemver(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want bool
	}{
		{"v1", true},
		{"1", true},
		{"v1.2", true},
		{"1.2", true},
		{"v1.2.3", false},
		{"v1.2.3-beta.1", false},
		{"v1.2.3+build", false},
		{"main", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsPartialSemver(tt.in); got != tt.want {
			t.Errorf("IsPartialSemver(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestResolvePartialSemver_ListFnError verifies that errors from the tag-list
// function propagate up wrapped, so callers can distinguish "remote unreachable"
// from "no matching tag" without parsing strings.
func TestResolvePartialSemver_ListFnError(t *testing.T) {
	t.Parallel()
	listFn := func(context.Context, string) ([]string, error) {
		return nil, errors.New("simulated network failure")
	}
	got, err := ResolvePartialSemver(context.Background(), "https://example.com/repo", "v1", listFn)
	if err == nil {
		t.Fatalf("expected error, got %q", got)
	}
	if !strings.Contains(err.Error(), "simulated network failure") {
		t.Errorf("error should wrap original cause, got: %v", err)
	}
}

func TestResolvePartialSemver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tagsFn := func(ts ...string) func(context.Context, string) ([]string, error) {
		return func(context.Context, string) ([]string, error) { return ts, nil }
	}

	tests := []struct {
		name    string
		partial string
		tags    []string
		want    string
		wantErr bool
	}{
		{"major picks latest minor", "v1", []string{"v1.0.0", "v1.5.0", "v2.0.0"}, "v1.5.0", false},
		{"major preserves bare tag spelling", "v1", []string{"1.0.0", "1.5.0", "v2.0.0"}, "1.5.0", false},
		{"major skips v10 when matching v1", "v1", []string{"v1.0.0", "v1.5.0", "v10.0.0"}, "v1.5.0", false},
		{"major-minor picks latest patch", "v1.2", []string{"v1.2.0", "v1.2.5", "v1.3.0"}, "v1.2.5", false},
		{"major-minor skips v1.10 when matching v1.2", "v1.2", []string{"v1.2.0", "v1.10.0"}, "v1.2.0", false},
		{"unprefixed normalizes", "1", []string{"v1.0.0", "v2.0.0"}, "v1.0.0", false},
		{"no stable match errors", "v2", []string{"v1.0.0"}, "", true},
		{"all prereleases errors", "v1", []string{"v1.0.0-beta.1", "v1.0.0-rc.1"}, "", true},
		{"prefers stable over prerelease", "v1", []string{"v1.0.0", "v1.5.0-beta"}, "v1.0.0", false},
		{"exact semver passes through untouched", "v1.2.3", []string{"should-not-be-read"}, "v1.2.3", false},
		{"exact unprefixed normalizes", "1.2.3", nil, "v1.2.3", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolvePartialSemver(ctx, "https://example.com/repo", tt.partial, tagsFn(tt.tags...))
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveExactSemver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tagsFn := func(ts ...string) func(context.Context, string) ([]string, error) {
		return func(context.Context, string) ([]string, error) { return ts, nil }
	}

	tests := []struct {
		name    string
		version string
		tags    []string
		want    string
		wantErr bool
	}{
		{"prefixed input matches bare remote tag", "v1.2.3", []string{"1.2.3", "2.0.0"}, "1.2.3", false},
		{"bare input matches prefixed remote tag", "1.2.3", []string{"v1.2.3", "v2.0.0"}, "v1.2.3", false},
		{"prefers exact semver match", "1.2.3", []string{"v1.2.2", "1.2.3", "v1.2.4"}, "1.2.3", false},
		{"missing tag errors", "1.2.3", []string{"v1.2.2", "v1.2.4"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveExactSemver(ctx, "https://example.com/repo", tt.version, tagsFn(tt.tags...))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
