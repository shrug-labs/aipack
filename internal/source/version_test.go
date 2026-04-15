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
		name   string
		tags   []string
		prefix string
		want   []string
	}{
		// Flat-tag behavior (prefix=""). Matches pre-v0.22 behavior byte-for-byte.
		{"empty", nil, "", nil},
		{"all valid", []string{"v1.0.0", "v2.0.0", "v1.5.0"}, "", []string{"v2.0.0", "v1.5.0", "v1.0.0"}},
		{"mixed valid and invalid", []string{"v1.0.0", "release-1", "v2.0.0", "nope"}, "", []string{"v2.0.0", "v1.0.0"}},
		{"without v prefix", []string{"1.0.0", "2.0.0"}, "", []string{"2.0.0", "1.0.0"}},
		{"preserves raw spelling while sorting semver", []string{"1.2.3", "v2.0.0", "v1.9.0"}, "", []string{"v2.0.0", "v1.9.0", "1.2.3"}},
		{"with prerelease", []string{"v1.0.0", "v1.1.0-beta.1", "v1.1.0"}, "", []string{"v1.1.0", "v1.1.0-beta.1", "v1.0.0"}},
		{"empty prefix rejects namespaced", []string{"v1.0.0", "my-pack/v1.0.0", "my-pack/v2.0.0"}, "", []string{"v1.0.0"}},

		// Namespaced-tag behavior (prefix="my-pack").
		{"prefix filters to namespaced only", []string{"my-pack/v1.0.0", "my-pack/v1.1.0", "other-pack/v2.0.0", "v3.0.0"}, "my-pack", []string{"my-pack/v1.1.0", "my-pack/v1.0.0"}},
		{"prefix no matches", []string{"v1.0.0", "other-pack/v1.0.0"}, "my-pack", nil},
		{"prefix prerelease handling", []string{"my-pack/v1.0.0", "my-pack/v1.1.0-beta.1", "my-pack/v1.1.0"}, "my-pack", []string{"my-pack/v1.1.0", "my-pack/v1.1.0-beta.1", "my-pack/v1.0.0"}},
		{"prefix sort multi-digit minor", []string{"my-pack/v0.9.0", "my-pack/v0.10.0"}, "my-pack", []string{"my-pack/v0.10.0", "my-pack/v0.9.0"}},
		{"prefix preserves bare tail", []string{"my-pack/1.0.0", "my-pack/v2.0.0"}, "my-pack", []string{"my-pack/v2.0.0", "my-pack/1.0.0"}},
		{"deep namespace", []string{"org/my-pack/v1.0.0", "org/my-pack/v2.0.0"}, "org/my-pack", []string{"org/my-pack/v2.0.0", "org/my-pack/v1.0.0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FilterSemverTags(tt.tags, tt.prefix)
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

func TestStripTagPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ref, prefix, want string
	}{
		{"v1.2.3", "", "v1.2.3"},
		{"v1.2.3", "my-pack", "v1.2.3"},
		{"my-pack/v1.2.3", "my-pack", "v1.2.3"},
		{"my-pack/v1.2.3", "other", "my-pack/v1.2.3"},
		{"my-pack/v1.2.3", "", "my-pack/v1.2.3"},
		{"", "my-pack", ""},
		{"", "", ""},
		{"org/my-pack/v1.0.0", "org/my-pack", "v1.0.0"},
	}
	for _, tt := range tests {
		if got := StripTagPrefix(tt.ref, tt.prefix); got != tt.want {
			t.Errorf("StripTagPrefix(%q, %q) = %q, want %q", tt.ref, tt.prefix, got, tt.want)
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

func TestIsSemverRef(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want bool
	}{
		{"v1.2.3", true},
		{"1.2.3", true},
		{"v1.2.3-beta.1", true},
		{"my-pack/v1.2.3", true},
		{"my-pack/1.2.3", true},
		{"org/my-pack/v1.2.3", true},
		{"main", false},
		{"my-pack/main", false},
		{"release-1", false},
		{"", false},
		{"my-pack/", false},
		{"/v1.2.3", false},
	}
	for _, tt := range tests {
		if got := IsSemverRef(tt.in); got != tt.want {
			t.Errorf("IsSemverRef(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestSemverFromRef(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"v1.2.3", "v1.2.3"},
		{"1.2.3", "1.2.3"},
		{"my-pack/v1.2.3", "v1.2.3"},
		{"my-pack/1.2.3", "1.2.3"},
		{"org/my-pack/v1.2.3", "v1.2.3"},
		{"main", ""},
		{"my-pack/main", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := SemverFromRef(tt.in); got != tt.want {
			t.Errorf("SemverFromRef(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTagPrefixFromRef(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"v1.2.3", ""},
		{"1.2.3", ""},
		{"my-pack/v1.2.3", "my-pack"},
		{"my-pack/1.2.3", "my-pack"},
		{"org/my-pack/v1.2.3", "org/my-pack"},
		{"main", ""},
		{"my-pack/main", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := TagPrefixFromRef(tt.in); got != tt.want {
			t.Errorf("TagPrefixFromRef(%q) = %q, want %q", tt.in, got, tt.want)
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

func TestClassifyRef(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want RefClassification
	}{
		// Empty and sentinel.
		{"", RefClassification{Kind: RefEmpty}},
		{"latest", RefClassification{Kind: RefLatest}},
		{"LATEST", RefClassification{Kind: RefLatest}},

		// Flat semver.
		{"1.2.3", RefClassification{Kind: RefSemver, Spec: "v1.2.3"}},
		{"v1.2.3", RefClassification{Kind: RefSemver, Spec: "v1.2.3"}},
		{"v1.0.0-beta.1", RefClassification{Kind: RefSemver, Spec: "v1.0.0-beta.1"}},
		{"v1.2.3+build", RefClassification{Kind: RefSemver, Spec: "v1.2.3+build"}},

		// Flat partial.
		{"v1", RefClassification{Kind: RefPartialSemver, Spec: "v1"}},
		{"1", RefClassification{Kind: RefPartialSemver, Spec: "v1"}},
		{"v1.2", RefClassification{Kind: RefPartialSemver, Spec: "v1.2"}},
		{"1.2", RefClassification{Kind: RefPartialSemver, Spec: "v1.2"}},

		// Namespaced semver.
		{"my-pack/v1.2.3", RefClassification{Kind: RefSemver, Prefix: "my-pack", Spec: "v1.2.3"}},
		{"my-pack/1.2.3", RefClassification{Kind: RefSemver, Prefix: "my-pack", Spec: "v1.2.3"}},
		{"my-pack/v1.0.0-beta", RefClassification{Kind: RefSemver, Prefix: "my-pack", Spec: "v1.0.0-beta"}},

		// Namespaced partial.
		{"my-pack/v1", RefClassification{Kind: RefPartialSemver, Prefix: "my-pack", Spec: "v1"}},
		{"my-pack/v1.2", RefClassification{Kind: RefPartialSemver, Prefix: "my-pack", Spec: "v1.2"}},

		// Deep namespace.
		{"org/my-pack/v1.2.3", RefClassification{Kind: RefSemver, Prefix: "org/my-pack", Spec: "v1.2.3"}},

		// Commit hash.
		{"abc1234", RefClassification{Kind: RefCommit, Spec: "abc1234"}},
		{"aabbccdd1122334455667788", RefClassification{Kind: RefCommit, Spec: "aabbccdd1122334455667788"}},
		{"CAFE123", RefClassification{Kind: RefCommit, Spec: "cafe123"}}, // lowercased
		{"cafe123", RefClassification{Kind: RefCommit, Spec: "cafe123"}},

		// Literal.
		{"main", RefClassification{Kind: RefLiteral, Spec: "main"}},
		{"develop", RefClassification{Kind: RefLiteral, Spec: "develop"}},
		{"release-2026-04-01", RefClassification{Kind: RefLiteral, Spec: "release-2026-04-01"}},
		{"feature/my-branch", RefClassification{Kind: RefLiteral, Spec: "feature/my-branch"}},
		{"my-pack/main", RefClassification{Kind: RefLiteral, Spec: "my-pack/main"}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got := ClassifyRef(tt.in)
			if got != tt.want {
				t.Errorf("ClassifyRef(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
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
	got, err := ResolvePartialSemver(context.Background(), "https://example.com/repo", "", "v1", listFn)
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
		prefix  string
		partial string
		tags    []string
		want    string
		wantErr bool
	}{
		// Flat (prefix="").
		{"major picks latest minor", "", "v1", []string{"v1.0.0", "v1.5.0", "v2.0.0"}, "v1.5.0", false},
		{"major preserves bare tag spelling", "", "v1", []string{"1.0.0", "1.5.0", "v2.0.0"}, "1.5.0", false},
		{"major skips v10 when matching v1", "", "v1", []string{"v1.0.0", "v1.5.0", "v10.0.0"}, "v1.5.0", false},
		{"major-minor picks latest patch", "", "v1.2", []string{"v1.2.0", "v1.2.5", "v1.3.0"}, "v1.2.5", false},
		{"major-minor skips v1.10 when matching v1.2", "", "v1.2", []string{"v1.2.0", "v1.10.0"}, "v1.2.0", false},
		{"unprefixed normalizes", "", "1", []string{"v1.0.0", "v2.0.0"}, "v1.0.0", false},
		{"no stable match errors", "", "v2", []string{"v1.0.0"}, "", true},
		{"all prereleases errors", "", "v1", []string{"v1.0.0-beta.1", "v1.0.0-rc.1"}, "", true},
		{"prefers stable over prerelease", "", "v1", []string{"v1.0.0", "v1.5.0-beta"}, "v1.0.0", false},
		{"exact semver passes through untouched", "", "v1.2.3", []string{"should-not-be-read"}, "v1.2.3", false},
		{"exact unprefixed normalizes", "", "1.2.3", nil, "v1.2.3", false},

		// Namespaced (prefix="my-pack").
		{"namespaced major picks latest", "my-pack", "v1", []string{"my-pack/v1.0.0", "my-pack/v1.5.0", "my-pack/v2.0.0"}, "my-pack/v1.5.0", false},
		{"namespaced isolates from flat", "my-pack", "v1", []string{"v1.0.0", "v1.5.0"}, "", true},
		{"namespaced isolates from other prefix", "my-pack", "v1", []string{"other-pack/v1.0.0"}, "", true},
		{"namespaced prerelease skip", "my-pack", "v1", []string{"my-pack/v1.5.0-beta", "my-pack/v1.4.0"}, "my-pack/v1.4.0", false},
		{"namespaced major-minor skips v1.10", "my-pack", "v1.2", []string{"my-pack/v1.2.0", "my-pack/v1.10.0"}, "my-pack/v1.2.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolvePartialSemver(ctx, "https://example.com/repo", tt.prefix, tt.partial, tagsFn(tt.tags...))
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
		prefix  string
		version string
		tags    []string
		want    string
		wantErr bool
	}{
		// Flat (prefix="").
		{"prefixed input matches bare remote tag", "", "v1.2.3", []string{"1.2.3", "2.0.0"}, "1.2.3", false},
		{"bare input matches prefixed remote tag", "", "1.2.3", []string{"v1.2.3", "v2.0.0"}, "v1.2.3", false},
		{"prefers exact semver match", "", "1.2.3", []string{"v1.2.2", "1.2.3", "v1.2.4"}, "1.2.3", false},
		{"missing tag errors", "", "1.2.3", []string{"v1.2.2", "v1.2.4"}, "", true},

		// Namespaced (prefix="my-pack").
		{"namespaced exact match", "my-pack", "1.0.0", []string{"my-pack/v1.0.0", "my-pack/v2.0.0"}, "my-pack/v1.0.0", false},
		{"namespaced missing tag errors", "my-pack", "9.9.9", []string{"my-pack/v1.0.0"}, "", true},
		{"namespaced isolates from flat", "my-pack", "1.0.0", []string{"v1.0.0"}, "", true},
		{"namespaced isolates from other prefix", "my-pack", "1.0.0", []string{"other-pack/v1.0.0"}, "", true},
		{"namespaced preserves raw spelling", "my-pack", "1.0.0", []string{"my-pack/1.0.0", "my-pack/v2.0.0"}, "my-pack/1.0.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveExactSemver(ctx, "https://example.com/repo", tt.prefix, tt.version, tagsFn(tt.tags...))
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
