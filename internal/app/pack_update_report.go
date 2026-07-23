package app

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

const PackUpdateReportSchemaVersion = 1

// PackUpdateReportStatus is the public semantic status of a dry-run update check.
type PackUpdateReportStatus string

const (
	PackUpdateReportUpdateAvailable PackUpdateReportStatus = "update-available"
	PackUpdateReportUpToDate        PackUpdateReportStatus = "up-to-date"
	PackUpdateReportSkipped         PackUpdateReportStatus = "skipped"
	PackUpdateReportError           PackUpdateReportStatus = "error"
)

// PackUpdateBundledAvailability describes bundled content offered by the
// current candidate. Exact IDs and preference history live in one hierarchy.
type PackUpdateBundledAvailability struct {
	New                []domain.BundledCategory `json:"new"`
	PreviouslyDeclined []domain.BundledCategory `json:"previously_declined"`
	Profiles           []string                 `json:"profiles"`
	Registries         []string                 `json:"registries"`
	Extras             []string                 `json:"extras"`
}

// PackUpdateBundledPreferences describes persisted preference state,
// independent of what the current candidate offers.
type PackUpdateBundledPreferences struct {
	Approved []domain.BundledCategory `json:"approved"`
	Declined []domain.BundledCategory `json:"declined"`
}

type PackUpdateBundledState struct {
	Available   PackUpdateBundledAvailability `json:"available"`
	Preferences PackUpdateBundledPreferences  `json:"preferences"`
}

// PackOriginCoordinates identify an installed or registry-advertised source.
// Origin can be a local path, git URL, or archive URL depending on Method.
type PackOriginCoordinates struct {
	Method       string                         `json:"method"`
	Origin       string                         `json:"origin"`
	Ref          string                         `json:"ref,omitempty"`
	SubPath      string                         `json:"sub_path,omitempty"`
	ContentPaths map[domain.PackCategory]string `json:"content_paths,omitempty"`
}

// PackOriginMigration reports registry drift without adopting it. Installed
// lockfile coordinates remain authoritative until an explicit migration.
type PackOriginMigration struct {
	Installed  PackOriginCoordinates
	Candidate  PackOriginCoordinates
	Provenance config.RegistryEntryProvenance
}

func (m PackOriginMigration) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Installed       PackOriginCoordinates        `json:"installed"`
		Candidate       PackOriginCoordinates        `json:"candidate"`
		RegistrySource  config.RegistrySourceEntry   `json:"registry_source"`
		ShadowedSources []config.RegistrySourceEntry `json:"shadowed_sources"`
	}{
		Installed:       m.Installed,
		Candidate:       m.Candidate,
		RegistrySource:  m.Provenance.Winner,
		ShadowedSources: slices.Clone(m.Provenance.Shadowed),
	})
}

// PackUpdateCheckResult is one semantic update-check result enriched with
// bundled preferences and optional registry-origin drift.
type PackUpdateCheckResult struct {
	PackUpdateResult
	ReportStatus    PackUpdateReportStatus
	Bundled         PackUpdateBundledState
	OriginMigration *PackOriginMigration
}

func (r PackUpdateCheckResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name            string                 `json:"name"`
		Method          string                 `json:"method"`
		Status          PackUpdateReportStatus `json:"status"`
		Message         string                 `json:"message"`
		CommitHash      string                 `json:"commit_hash,omitempty"`
		DryRun          bool                   `json:"dry_run,omitempty"`
		Bundled         PackUpdateBundledState `json:"bundled"`
		OriginMigration *PackOriginMigration   `json:"origin_migration,omitempty"`
	}{
		Name:            r.Name,
		Method:          r.Method,
		Status:          r.ReportStatus,
		Message:         r.Message,
		CommitHash:      r.CommitHash,
		DryRun:          r.DryRun,
		Bundled:         r.Bundled,
		OriginMigration: r.OriginMigration,
	})
}

type PackUpdateReportSummary struct {
	Total           int `json:"total"`
	UpdateAvailable int `json:"update_available"`
	UpToDate        int `json:"up_to_date"`
	Skipped         int `json:"skipped"`
	Error           int `json:"error"`
}

// PackUpdateReport is the versioned stdout document emitted by
// `pack update --dry-run --json`.
type PackUpdateReport struct {
	SchemaVersion int                     `json:"schema_version"`
	CheckedAt     string                  `json:"checked_at"`
	DryRun        bool                    `json:"dry_run"`
	Results       []PackUpdateCheckResult `json:"results"`
	Summary       PackUpdateReportSummary `json:"summary"`
}

// BuildPackUpdateCheckResults enriches results from the same execution snapshot.
// Optional registry provenance failures do not hide valid update outcomes.
func BuildPackUpdateCheckResults(configDir string, execution PackUpdateExecution) []PackUpdateCheckResult {
	names := make([]string, 0, len(execution.Results))
	for _, update := range execution.Results {
		names = append(names, update.Name)
	}
	registryEntries, provenance := config.LoadPackRegistryProvenance(configDir, names)
	results := make([]PackUpdateCheckResult, 0, len(execution.Results))
	for _, update := range execution.Results {
		meta := execution.Installed[update.Name]
		if update.Method == "" {
			update.Method = meta.Method
		}
		results = append(results, PackUpdateCheckResult{
			PackUpdateResult: update,
			ReportStatus:     packUpdateReportStatus(update.Status),
			Bundled:          packUpdateBundledState(meta, update.BundledCandidates),
			OriginMigration:  packOriginMigration(update.Name, meta, registryEntries, provenance),
		})
	}
	slices.SortFunc(results, func(a, b PackUpdateCheckResult) int {
		return strings.Compare(a.Name, b.Name)
	})
	return results
}

// BuildPackUpdateReport serializes semantic check results into the versioned
// JSON stdout document.
func BuildPackUpdateReport(checkedAt time.Time, results []PackUpdateCheckResult) PackUpdateReport {
	report := PackUpdateReport{
		SchemaVersion: PackUpdateReportSchemaVersion,
		CheckedAt:     checkedAt.UTC().Format(time.RFC3339),
		DryRun:        true,
		Results:       results,
	}
	report.Summary = summarizePackUpdateReport(report.Results)
	return report
}

func packUpdateBundledState(meta config.InstalledPackMeta, candidates *BundledCandidates) PackUpdateBundledState {
	available := PackUpdateBundledAvailability{
		New:                []domain.BundledCategory{},
		PreviouslyDeclined: []domain.BundledCategory{},
		Profiles:           []string{},
		Registries:         []string{},
		Extras:             []string{},
	}
	if candidates != nil {
		available.Profiles = append([]string{}, candidates.Profiles...)
		available.Registries = append([]string{}, candidates.Registries...)
		available.Extras = append([]string{}, candidates.Extras...)
		declined := domain.NewBundledSet(meta.Declined...)
		for _, category := range candidates.Categories() {
			if declined.Has(category) {
				available.PreviouslyDeclined = append(available.PreviouslyDeclined, category)
			} else {
				available.New = append(available.New, category)
			}
		}
	}
	state := PackUpdateBundledState{
		Available: available,
		Preferences: PackUpdateBundledPreferences{
			Approved: slices.Clone(meta.Approved),
			Declined: slices.Clone(meta.Declined),
		},
	}
	if state.Preferences.Approved == nil {
		state.Preferences.Approved = []domain.BundledCategory{}
	}
	if state.Preferences.Declined == nil {
		state.Preferences.Declined = []domain.BundledCategory{}
	}
	slices.Sort(state.Available.New)
	slices.Sort(state.Available.PreviouslyDeclined)
	slices.Sort(state.Available.Profiles)
	slices.Sort(state.Available.Registries)
	slices.Sort(state.Available.Extras)
	slices.Sort(state.Preferences.Approved)
	slices.Sort(state.Preferences.Declined)
	return state
}

func packUpdateReportStatus(status UpdateStatus) PackUpdateReportStatus {
	switch status {
	case StatusUpdated:
		return PackUpdateReportUpdateAvailable
	case StatusUpToDate:
		return PackUpdateReportUpToDate
	case StatusSkipped:
		return PackUpdateReportSkipped
	case StatusError:
		return PackUpdateReportError
	default:
		return PackUpdateReportStatus(status)
	}
}

func packOriginMigration(
	name string,
	installed config.InstalledPackMeta,
	entries map[string]config.RegistryEntry,
	provenance map[string]config.RegistryEntryProvenance,
) *PackOriginMigration {
	entry, ok := entries[name]
	if !ok || installed.Origin == "" {
		return nil
	}
	candidate := registryOriginCoordinates(entry)
	current := PackOriginCoordinates{
		Method:       installed.Method,
		Origin:       installed.Origin,
		Ref:          installed.Ref,
		SubPath:      installed.SubPath,
		ContentPaths: maps.Clone(installed.ContentPaths),
	}
	if samePackOrigin(current, candidate) {
		return nil
	}
	return &PackOriginMigration{
		Installed:  current,
		Candidate:  candidate,
		Provenance: provenance[name],
	}
}

func registryOriginCoordinates(entry config.RegistryEntry) PackOriginCoordinates {
	method := entry.Method
	origin := entry.Repo
	ref := entry.Ref
	if method == "" {
		method = config.MethodClone
	}
	if method == config.MethodArchive {
		origin = entry.URL
		ref = ""
	}
	return PackOriginCoordinates{
		Method:       method,
		Origin:       origin,
		Ref:          ref,
		SubPath:      entry.Path,
		ContentPaths: maps.Clone(entry.ContentPaths),
	}
}

func samePackOrigin(a, b PackOriginCoordinates) bool {
	normalizeMethod := func(method string) string {
		if method == "" {
			return config.MethodClone
		}
		return method
	}
	return normalizeMethod(a.Method) == normalizeMethod(b.Method) &&
		a.Origin == b.Origin &&
		a.SubPath == b.SubPath &&
		maps.Equal(a.ContentPaths, b.ContentPaths)
}

func summarizePackUpdateReport(results []PackUpdateCheckResult) PackUpdateReportSummary {
	summary := PackUpdateReportSummary{Total: len(results)}
	for _, result := range results {
		switch result.ReportStatus {
		case PackUpdateReportUpdateAvailable:
			summary.UpdateAvailable++
		case PackUpdateReportUpToDate:
			summary.UpToDate++
		case PackUpdateReportSkipped:
			summary.Skipped++
		case PackUpdateReportError:
			summary.Error++
		}
	}
	return summary
}
