package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// BundledCandidates describes new bundled content categories found during an
// update that were not previously approved by the user. The TUI shows these
// in a checklist dialog; the CLI prints them as a preview.
type BundledCandidates struct {
	Profiles   []string `json:"profiles,omitempty"`
	Registries []string `json:"registries,omitempty"`
	Extras     []string `json:"extras,omitempty"`
}

// Categories returns the list of category names that have bundled content candidates.
func (bc *BundledCandidates) Categories() []domain.BundledCategory {
	if bc == nil {
		return nil
	}
	var cats []domain.BundledCategory
	if len(bc.Profiles) > 0 {
		cats = append(cats, domain.BundledProfiles)
	}
	if len(bc.Registries) > 0 {
		cats = append(cats, domain.BundledRegistries)
	}
	if len(bc.Extras) > 0 {
		cats = append(cats, domain.BundledExtras)
	}
	return cats
}

// Filter returns a copy with only categories not already in approved.
// Safe to call on a nil receiver (returns nil).
func (bc *BundledCandidates) Filter(approved domain.BundledSet) *BundledCandidates {
	if bc == nil {
		return nil
	}
	filtered := &BundledCandidates{}
	if len(bc.Profiles) > 0 && !approved.Has(domain.BundledProfiles) {
		filtered.Profiles = append(filtered.Profiles, bc.Profiles...)
	}
	if len(bc.Registries) > 0 && !approved.Has(domain.BundledRegistries) {
		filtered.Registries = append(filtered.Registries, bc.Registries...)
	}
	if len(bc.Extras) > 0 && !approved.Has(domain.BundledExtras) {
		filtered.Extras = append(filtered.Extras, bc.Extras...)
	}
	if len(filtered.Profiles) == 0 && len(filtered.Registries) == 0 && len(filtered.Extras) == 0 {
		return nil
	}
	return filtered
}

// AnyApprovedBy reports whether the explicit approval set covers any of
// the candidate categories. Safe to call on a nil receiver (returns false).
func (bc *BundledCandidates) AnyApprovedBy(set domain.BundledSet) bool {
	if bc == nil {
		return false
	}
	return (len(bc.Profiles) > 0 && set.Has(domain.BundledProfiles)) ||
		(len(bc.Registries) > 0 && set.Has(domain.BundledRegistries)) ||
		(len(bc.Extras) > 0 && set.Has(domain.BundledExtras))
}

// --- bundled content preferences ---

// metaApprovedSet builds a domain.BundledSet from InstalledPackMeta's Approved/Declined
// fields. Three states: in Approved → approved, in Declined → declined,
// in neither → not_expressed (treated as approved for backward compat).
// When both lists are empty (pre-preference pack), returns domain.BundledAll().
func metaApprovedSet(meta config.InstalledPackMeta) domain.BundledSet {
	if len(meta.Approved) == 0 && len(meta.Declined) == 0 {
		return domain.BundledAll()
	}
	s := make(domain.BundledSet, len(meta.Approved))
	for _, cat := range meta.Approved {
		s[cat] = true
	}
	declined := make(map[domain.BundledCategory]bool, len(meta.Declined))
	for _, cat := range meta.Declined {
		declined[cat] = true
	}
	for _, cat := range domain.AllBundledCategories {
		if !s[cat] && !declined[cat] {
			s[cat] = true // not_expressed → approved
		}
	}
	return s
}

// buildPrefsLists converts a domain.BundledSet into approved/declined string slices
// for storage in InstalledPackMeta. When with is nil, returns nil/nil
// (no preference expressed — backward compat).
func buildPrefsLists(with domain.BundledSet) (approved, declined []domain.BundledCategory) {
	if with == nil {
		return nil, nil
	}
	for _, cat := range domain.AllBundledCategories {
		if with.Has(cat) {
			approved = append(approved, cat)
		} else {
			declined = append(declined, cat)
		}
	}
	return approved, declined
}

// diffBundledCandidates compares the incoming manifest's bundled content against
// previously approved categories and returns categories that are new (present
// in source, absent in oldApproved). Returns nil when there are no new
// categories. Accepts a pre-computed domain.BundledSet to avoid redundant
// metaApprovedSet calls when the caller also needs the set.
func diffBundledCandidates(incoming config.PackManifest, oldApproved domain.BundledSet) *BundledCandidates {
	bc := &BundledCandidates{}

	if len(incoming.Profiles) > 0 && !oldApproved.Has(domain.BundledProfiles) {
		bc.Profiles = incoming.Profiles
	}
	if len(incoming.Registries) > 0 && !oldApproved.Has(domain.BundledRegistries) {
		bc.Registries = incoming.Registries
	}
	if len(incoming.Extras) > 0 && !oldApproved.Has(domain.BundledExtras) {
		bc.Extras = incoming.Extras
	}

	if len(bc.Profiles) == 0 && len(bc.Registries) == 0 && len(bc.Extras) == 0 {
		return nil
	}
	return bc
}

// applyPreferenceFilter computes the effective approval set from the old
// installed pack metadata and explicit --with flags, diffs for bundled content
// candidates, then filters the staging directory to match. Returns the
// candidates (nil when nothing new), the merged effective approval set, and
// any error. Callers use the returned set with buildPrefsLists to record
// preferences.
func applyPreferenceFilter(staging string, incoming *config.PackManifest, oldMeta config.InstalledPackMeta, with domain.BundledSet) (*BundledCandidates, domain.BundledSet, error) {
	approved := metaApprovedSet(oldMeta)
	effective := approved.Merge(with)
	candidates := diffBundledCandidates(*incoming, approved).Filter(effective)
	if err := applyWithFilter(staging, incoming, effective); err != nil {
		return nil, nil, err
	}
	return candidates, effective, nil
}

// --- bundled content side-effects ---

// installBundledProfiles copies bundled profile YAML files from the installed pack
// to configDir/profiles/, overwriting any existing file with the same name.
// Bundled profiles are treated as managed content — they stay in sync with the
// upstream pack. Users who want to customize should copy the profile to a new
// name rather than editing the pack-managed original.
func installBundledProfiles(configDir, packDir string, profiles []string, stdout io.Writer) {
	if len(profiles) == 0 {
		return
	}
	profilesDir := filepath.Join(configDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		fmt.Fprintf(stdout, "Warning: failed to create profiles dir: %v\n", err)
		return
	}
	for _, id := range profiles {
		src := filepath.Join(packDir, "profiles", id+".yaml")
		name := id
		dest := filepath.Join(profilesDir, id+".yaml")
		data, err := os.ReadFile(src)
		if err != nil {
			fmt.Fprintf(stdout, "Warning: failed to read bundled profile %s: %v\n", id, err)
			continue
		}
		if existing, err := os.ReadFile(dest); err == nil {
			if !bytes.Equal(existing, data) {
				fmt.Fprintf(stdout, "Overwriting modified profile %q (copy to a new name to preserve customizations)\n", name)
			}
		}
		if err := util.WriteFileAtomicWithPerms(dest, data, 0o700, 0o600); err != nil {
			fmt.Fprintf(stdout, "Warning: failed to write bundled profile %s: %v\n", dest, err)
			continue
		}
		fmt.Fprintf(stdout, "Installed bundled profile %q\n", name)
		fmt.Fprintf(stdout, "  To activate: aipack profile set %s\n", name)
	}
}

// installBundledRegistries merges entries from a pack's bundled registry files into
// the user's embedded registry cache. Follows the same first-seen-wins merge
// pattern as processEmbeddedRegistries (sync.go), but operates on a single
// pack at install/update time.
func installBundledRegistries(configDir, packDir string, registries []string, stdout io.Writer) error {
	if len(registries) == 0 {
		return nil
	}

	// Load existing embedded registry cache to merge into.
	cachePath := config.SourceCachePath(configDir, embeddedRegistrySourceName)
	existing, _ := config.LoadRegistry(cachePath)
	if existing.Packs == nil {
		existing.Packs = make(map[string]config.RegistryEntry)
	}

	var added int
	for _, id := range registries {
		absPath := filepath.Join(packDir, "registries", id+".yaml")
		reg, err := config.LoadRegistry(absPath)
		if err != nil {
			fmt.Fprintf(stdout, "Warning: failed to load registry %s: %v\n", id, err)
			continue
		}
		for name, entry := range reg.Packs {
			if _, exists := existing.Packs[name]; !exists {
				existing.Packs[name] = entry
				added++
			}
		}
	}
	if added == 0 {
		return nil
	}

	existing.SchemaVersion = config.RegistrySchemaVersion
	if err := saveEmbeddedRegistry(configDir, existing); err != nil {
		return fmt.Errorf("saving embedded registry: %w", err)
	}
	if err := indexRegistryEntries(existing, configDir); err != nil {
		return fmt.Errorf("indexing registry entries: %w", err)
	}
	fmt.Fprintf(stdout, "Installed %d bundled registry entry(ies)\n", added)
	return nil
}

// installBundledContent installs bundled profiles and registries for approved categories.
func installBundledContent(configDir, packDir string, manifest config.PackManifest, with domain.BundledSet, stdout io.Writer) {
	if with.Has(domain.BundledProfiles) && len(manifest.Profiles) > 0 {
		installBundledProfiles(configDir, packDir, manifest.Profiles, stdout)
	}
	if with.Has(domain.BundledRegistries) && len(manifest.Registries) > 0 {
		if err := installBundledRegistries(configDir, packDir, manifest.Registries, stdout); err != nil {
			fmt.Fprintf(stdout, "Warning: bundled registry install failed: %v\n", err)
		}
	}
}

// packPreviewBundled prints available bundled content that was not applied.
// Used for remote installs when --with is not specified.
func packPreviewBundled(packDir string, manifest config.PackManifest, stdout io.Writer) {
	var lines []string
	for _, id := range manifest.Profiles {
		if util.PathExists(filepath.Join(packDir, "profiles", id+".yaml")) {
			lines = append(lines, fmt.Sprintf("  profile:    %s", id))
		}
	}
	for _, id := range manifest.Registries {
		if util.PathExists(filepath.Join(packDir, "registries", id+".yaml")) {
			lines = append(lines, fmt.Sprintf("  registry:   %s", id))
		}
	}
	for _, relPath := range manifest.Extras {
		if util.PathExists(filepath.Join(packDir, relPath)) {
			lines = append(lines, fmt.Sprintf("  extra:      %s", relPath))
		}
	}
	// Summarize content directories (always installed, shown for awareness).
	var contentDirs []string
	if len(manifest.Rules) > 0 {
		contentDirs = append(contentDirs, fmt.Sprintf("%d rules", len(manifest.Rules)))
	}
	if len(manifest.Skills) > 0 {
		contentDirs = append(contentDirs, fmt.Sprintf("%d skills", len(manifest.Skills)))
	}
	if len(manifest.Agents) > 0 {
		contentDirs = append(contentDirs, fmt.Sprintf("%d agents", len(manifest.Agents)))
	}
	if len(manifest.Workflows) > 0 {
		contentDirs = append(contentDirs, fmt.Sprintf("%d workflows", len(manifest.Workflows)))
	}
	if len(manifest.Prompts) > 0 {
		contentDirs = append(contentDirs, fmt.Sprintf("%d prompts", len(manifest.Prompts)))
	}
	if len(manifest.MCP.Servers) > 0 {
		contentDirs = append(contentDirs, fmt.Sprintf("%d MCP servers", len(manifest.MCP.Servers)))
	}
	if manifest.Configs.HasAnyConfigs() {
		contentDirs = append(contentDirs, "harness configs")
	}
	if len(contentDirs) > 0 {
		lines = append(lines, fmt.Sprintf("  content:    %s", strings.Join(contentDirs, ", ")))
	}

	if len(lines) > 0 {
		fmt.Fprintln(stdout, "This pack bundles additional content (use -w to accept, e.g. -w all):")
		for _, l := range lines {
			fmt.Fprintln(stdout, l)
		}
	}
}

// PackApproveBundled reapplies pack updates with newly approved bundled content
// categories so filtered files are materialized on disk and side effects
// (profiles, registries) run through the normal update path.
func PackApproveBundled(configDir string, packNames []string, approved domain.BundledSet, stdout io.Writer) error {
	if len(packNames) == 0 || len(approved) == 0 {
		return nil
	}
	for _, name := range packNames {
		results, err := PackUpdate(context.Background(), PackUpdateRequest{
			ConfigDir: configDir,
			Name:      name,
			With:      approved,
		}, stdout)
		if err != nil {
			return fmt.Errorf("reapplying bundled content for %s: %w", name, err)
		}
		if len(results) != 1 {
			return fmt.Errorf("reapplying bundled content for %s: expected 1 result, got %d", name, len(results))
		}
		switch results[0].Status {
		case StatusUpdated, StatusUpToDate:
			continue
		default:
			return fmt.Errorf("reapplying bundled content for %s: %s", name, results[0].Message)
		}
	}
	return nil
}
