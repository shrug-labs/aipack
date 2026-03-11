package config

import (
	"path/filepath"
	"testing"
)

func TestNormalizeProfileName_Valid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "default empty", in: "", want: "default"},
		{name: "default whitespace", in: "   ", want: "default"},
		{name: "simple", in: "ocm", want: "ocm"},
		{name: "with separators allowed chars", in: "ocm-prod_1.2", want: "ocm-prod_1.2"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeProfileName(tc.in)
			if err != nil {
				t.Fatalf("NormalizeProfileName(%q) returned error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeProfileName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeProfileName_Invalid(t *testing.T) {
	t.Parallel()

	tests := []string{
		"../evil",
		"..",
		"a/../b",
		"nested/profile",
		`nested\profile`,
		"bad name",
		"bad*name",
	}

	for _, in := range tests {
		in := in
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			if _, err := NormalizeProfileName(in); err == nil {
				t.Fatalf("NormalizeProfileName(%q) expected error, got nil", in)
			}
		})
	}
}

func TestResolveProfilePath_UsesConfigDir(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	got, err := ResolveProfilePath("", configDir, "myprofile", "")
	if err != nil {
		t.Fatalf("ResolveProfilePath returned error: %v", err)
	}
	want := filepath.Join(configDir, "profiles", "myprofile.yaml")
	if got != want {
		t.Fatalf("ResolveProfilePath = %q, want %q", got, want)
	}
}
