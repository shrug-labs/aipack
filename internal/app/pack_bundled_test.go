package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestMetaApprovedSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		meta     config.InstalledPackMeta
		wantAll  bool                            // true = every category approved
		wantNone bool                            // true = no category approved
		want     map[domain.BundledCategory]bool // per-category checks (nil = skip)
	}{
		{
			name:    "pre-preference (both empty) approves all",
			meta:    config.InstalledPackMeta{},
			wantAll: true,
		},
		{
			name:     "fully opted out",
			meta:     config.InstalledPackMeta{Declined: append([]domain.BundledCategory{}, domain.AllBundledCategories...)},
			wantNone: true,
		},
		{
			name: "mixed: explicit approved + declined + not-expressed",
			meta: config.InstalledPackMeta{
				Approved: []domain.BundledCategory{domain.BundledProfiles},
				Declined: []domain.BundledCategory{domain.BundledExtras},
			},
			want: map[domain.BundledCategory]bool{
				domain.BundledProfiles:   true,  // explicitly approved
				domain.BundledExtras:     false, // explicitly declined
				domain.BundledRegistries: true,  // not_expressed → approved
			},
		},
		{
			name: "not-expressed categories default to approved",
			meta: config.InstalledPackMeta{
				Approved: []domain.BundledCategory{domain.BundledProfiles},
			},
			want: map[domain.BundledCategory]bool{
				domain.BundledProfiles:   true,
				domain.BundledRegistries: true,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := metaApprovedSet(tc.meta)
			if tc.wantAll {
				for _, cat := range domain.AllBundledCategories {
					if !got.Has(cat) {
						t.Errorf("%q should be approved", cat)
					}
				}
			}
			if tc.wantNone {
				for _, cat := range domain.AllBundledCategories {
					if got.Has(cat) {
						t.Errorf("%q should NOT be approved", cat)
					}
				}
			}
			for cat, expected := range tc.want {
				if got.Has(cat) != expected {
					t.Errorf("%s: approved=%v, want %v", cat, got.Has(cat), expected)
				}
			}
		})
	}
}

func TestBuildPrefsLists(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    domain.BundledSet
		wantALen int
		wantDLen int
		wantANil bool
	}{
		{"nil → nil/nil", nil, 0, 0, true},
		{"all → all approved", domain.BundledAll(), len(domain.AllBundledCategories), 0, false},
		{"partial", domain.NewBundledSet(domain.BundledProfiles, domain.BundledExtras), 2, len(domain.AllBundledCategories) - 2, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, d := buildPrefsLists(tc.input)
			if tc.wantANil {
				if a != nil || d != nil {
					t.Fatalf("got %v/%v, want nil/nil", a, d)
				}
				return
			}
			if len(a) != tc.wantALen {
				t.Errorf("approved: got %d, want %d", len(a), tc.wantALen)
			}
			if len(d) != tc.wantDLen {
				t.Errorf("declined: got %d, want %d", len(d), tc.wantDLen)
			}
		})
	}
}

func TestDiffBundledCandidates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		incoming            config.PackManifest
		oldApproved         domain.BundledSet
		wantNil             bool
		wantP, wantR, wantE int // expected counts for profiles, registries, extras
	}{
		{
			name:        "new profiles detected",
			incoming:    config.PackManifest{Profiles: []string{"dev"}},
			oldApproved: domain.NewBundledSet(domain.BundledExtras),
			wantP:       1,
		},
		{
			name:        "already-approved profiles → nil",
			incoming:    config.PackManifest{Profiles: []string{"dev"}},
			oldApproved: domain.NewBundledSet(domain.BundledProfiles),
			wantNil:     true,
		},
		{
			name: "multiple new categories",
			incoming: config.PackManifest{
				Profiles: []string{"full-stack"}, Registries: []string{"team-tools"},
				Extras: []string{"scripts/"}, Rules: []string{"style"}, Skills: []string{"deploy"},
			},
			oldApproved: domain.NewBundledSet(),
			wantP:       1, wantR: 1, wantE: 1,
		},
		{
			name:        "core dirs only → nil",
			incoming:    config.PackManifest{Rules: []string{"style"}, Skills: []string{"deploy"}},
			oldApproved: domain.NewBundledSet(),
			wantNil:     true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := diffBundledCandidates(tc.incoming, tc.oldApproved)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil BundledCandidates")
			}
			if len(got.Profiles) != tc.wantP {
				t.Errorf("profiles: got %d, want %d", len(got.Profiles), tc.wantP)
			}
			if len(got.Registries) != tc.wantR {
				t.Errorf("registries: got %d, want %d", len(got.Registries), tc.wantR)
			}
			if len(got.Extras) != tc.wantE {
				t.Errorf("extras: got %d, want %d", len(got.Extras), tc.wantE)
			}
		})
	}
}

func TestBundledCandidates_Categories(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		bc   *BundledCandidates
		want int
	}{
		{"nil receiver", nil, 0},
		{"empty", &BundledCandidates{}, 0},
		{"profiles only", &BundledCandidates{Profiles: []string{"dev"}}, 1},
		{"all three", &BundledCandidates{Profiles: []string{"a"}, Registries: []string{"b"}, Extras: []string{"c"}}, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := len(tc.bc.Categories()); got != tc.want {
				t.Errorf("Categories() returned %d, want %d", got, tc.want)
			}
		})
	}
}

func TestBundledCandidates_Filter(t *testing.T) {
	t.Parallel()
	bc := &BundledCandidates{
		Profiles:   []string{"dev"},
		Registries: []string{"team"},
		Extras:     []string{"scripts"},
	}

	// Filter with profiles approved → profiles removed from candidates.
	got := bc.Filter(domain.NewBundledSet(domain.BundledProfiles))
	if got == nil {
		t.Fatal("expected non-nil after partial filter")
	}
	if len(got.Profiles) != 0 {
		t.Errorf("profiles should be filtered out, got %v", got.Profiles)
	}
	if len(got.Registries) != 1 || len(got.Extras) != 1 {
		t.Errorf("registries/extras should remain: r=%v e=%v", got.Registries, got.Extras)
	}

	// Filter with all approved → nil.
	if got := bc.Filter(domain.BundledAll()); got != nil {
		t.Errorf("expected nil when all approved, got %+v", got)
	}

	// Nil receiver → nil.
	var nilBC *BundledCandidates
	if got := nilBC.Filter(domain.BundledAll()); got != nil {
		t.Errorf("nil receiver should return nil, got %+v", got)
	}
}

func TestBundledCandidates_AnyApprovedBy(t *testing.T) {
	t.Parallel()
	bc := &BundledCandidates{Profiles: []string{"dev"}, Extras: []string{"scripts"}}

	if !bc.AnyApprovedBy(domain.NewBundledSet(domain.BundledProfiles)) {
		t.Error("should match when profiles approved and candidates have profiles")
	}
	if bc.AnyApprovedBy(domain.NewBundledSet(domain.BundledRegistries)) {
		t.Error("should not match when only registries approved but candidates have none")
	}

	var nilBC *BundledCandidates
	if nilBC.AnyApprovedBy(domain.BundledAll()) {
		t.Error("nil receiver should return false")
	}
}

func TestInstallBundledProfiles(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T, staleContent string) (string, string) {
		t.Helper()
		packDir := t.TempDir()
		configDir := t.TempDir()
		m := map[string]any{
			"schema_version": 1, "name": "test-pack", "version": "2.0.0",
			"root": ".", "profiles": []string{"dev"},
		}
		b, _ := json.Marshal(m)
		os.WriteFile(filepath.Join(packDir, "pack.json"), b, 0o600)
		os.MkdirAll(filepath.Join(packDir, "profiles"), 0o700)
		os.WriteFile(filepath.Join(packDir, "profiles", "dev.yaml"),
			[]byte("schema_version: 1\npacks:\n  - updated-pack\n"), 0o600)
		writeEmptyProfile(t, configDir, "default")
		writeTestSyncConfig(t, configDir)
		os.WriteFile(filepath.Join(configDir, "profiles", "dev.yaml"),
			[]byte(staleContent), 0o600)
		return packDir, configDir
	}

	tests := []struct {
		name         string
		stale        string
		with         domain.BundledSet
		wantContains string // expected in resulting dev.yaml
		wantAbsent   string // should NOT be in dev.yaml
	}{
		{
			name:         "overwrites with -w all",
			stale:        "schema_version: 1\npacks:\n  - stale-pack\n",
			with:         domain.BundledAll(),
			wantContains: "updated-pack",
			wantAbsent:   "stale-pack",
		},
		{
			name:         "default local install overwrites",
			stale:        "schema_version: 1\npacks:\n  - stale-pack\n",
			with:         nil, // local installs default to BundledAll
			wantContains: "updated-pack",
		},
		{
			name:         "without profiles skips",
			stale:        "schema_version: 1\npacks:\n  - existing-pack\n",
			with:         domain.NewBundledSet(domain.BundledExtras),
			wantContains: "existing-pack",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			packDir, configDir := setup(t, tc.stale)
			var out bytes.Buffer
			PackInstall(context.Background(), PackInstallRequest{
				PackPath: packDir, ConfigDir: configDir, Link: true,
				Register: true, Profile: "default", With: tc.with,
				NowFn: func() time.Time { return fixedNow },
			}, &out)

			got, _ := os.ReadFile(filepath.Join(configDir, "profiles", "dev.yaml"))
			if tc.wantContains != "" && !strings.Contains(string(got), tc.wantContains) {
				t.Fatalf("dev.yaml should contain %q:\n%s", tc.wantContains, got)
			}
			if tc.wantAbsent != "" && strings.Contains(string(got), tc.wantAbsent) {
				t.Fatalf("dev.yaml should not contain %q:\n%s", tc.wantAbsent, got)
			}
		})
	}
}
