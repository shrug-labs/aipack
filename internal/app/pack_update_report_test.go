package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestBuildPackUpdateReport_BundledStateOriginMigrationAndSummary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installed := map[string]config.InstalledPackMeta{
		"demo": {
			Method:   config.MethodClone,
			Origin:   "https://example.com/installed.git",
			Approved: []domain.BundledCategory{domain.BundledExtras},
			Declined: []domain.BundledCategory{domain.BundledRegistries},
		},
	}
	sc := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	sc.RegistrySources = []config.RegistrySourceEntry{
		{Name: "team", URL: "https://example.com/team.yaml"},
		{Name: "public", URL: "https://example.com/public.yaml"},
	}
	if err := config.SaveSyncConfig(config.SyncConfigPath(dir), sc); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.RegistriesCacheDir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, repo := range map[string]string{
		"team":   "https://example.com/candidate.git",
		"public": "https://example.com/shadowed.git",
	} {
		body := "schema_version: 1\npacks:\n  demo:\n    repo: " + repo + "\n"
		if err := os.WriteFile(config.SourceCachePath(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	checkedAt := time.Date(2026, 7, 23, 16, 0, 0, 0, time.UTC)
	report := buildUpdateReportForTest(dir, checkedAt, installed, []PackUpdateResult{{
		Name:   "demo",
		Method: config.MethodClone,
		Status: StatusUpdated,
		DryRun: true,
		BundledCandidates: &BundledCandidates{
			Profiles:   []string{"team"},
			Registries: []string{"catalog"},
		},
	}})
	if report.SchemaVersion != 1 || !report.DryRun || report.CheckedAt != "2026-07-23T16:00:00Z" {
		t.Fatalf("report metadata = %+v", report)
	}
	if report.Summary.Total != 1 || report.Summary.UpdateAvailable != 1 {
		t.Fatalf("summary = %+v", report.Summary)
	}
	result := report.Results[0]
	if result.ReportStatus != PackUpdateReportUpdateAvailable {
		t.Fatalf("status = %q", result.ReportStatus)
	}
	if len(result.Bundled.Available.New) != 1 || result.Bundled.Available.New[0] != domain.BundledProfiles {
		t.Fatalf("available.new = %v", result.Bundled.Available.New)
	}
	if len(result.Bundled.Available.PreviouslyDeclined) != 1 ||
		result.Bundled.Available.PreviouslyDeclined[0] != domain.BundledRegistries {
		t.Fatalf("available.previously_declined = %v", result.Bundled.Available.PreviouslyDeclined)
	}
	if len(result.Bundled.Preferences.Approved) != 1 ||
		result.Bundled.Preferences.Approved[0] != domain.BundledExtras {
		t.Fatalf("preferences.approved = %v", result.Bundled.Preferences.Approved)
	}
	if len(result.Bundled.Preferences.Declined) != 1 ||
		result.Bundled.Preferences.Declined[0] != domain.BundledRegistries {
		t.Fatalf("preferences.declined = %v", result.Bundled.Preferences.Declined)
	}
	if result.OriginMigration == nil {
		t.Fatal("expected origin migration candidate")
	}
	if result.OriginMigration.Provenance.Winner.Name != "team" {
		t.Fatalf("registry source = %+v", result.OriginMigration.Provenance.Winner)
	}
	if len(result.OriginMigration.Provenance.Shadowed) != 1 ||
		result.OriginMigration.Provenance.Shadowed[0].Name != "public" {
		t.Fatalf("shadowed = %+v", result.OriginMigration.Provenance.Shadowed)
	}
	if len(result.Bundled.Available.Profiles) != 1 || result.Bundled.Available.Profiles[0] != "team" {
		t.Fatalf("available.profiles = %v", result.Bundled.Available.Profiles)
	}
	if result.Bundled.Available.Extras == nil {
		t.Fatal("available.extras must serialize as an empty array, not null")
	}
}

func TestBuildPackUpdateReport_DoesNotReportMatchingOrigin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	origin := "https://example.com/demo.git"
	installed := map[string]config.InstalledPackMeta{
		"demo": {Method: config.MethodClone, Origin: origin, Ref: "v1.2.3"},
	}
	sc := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	sc.RegistrySources = []config.RegistrySourceEntry{{Name: "team", URL: "https://example.com/team.yaml"}}
	if err := config.SaveSyncConfig(config.SyncConfigPath(dir), sc); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.RegistriesCacheDir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "schema_version: 1\npacks:\n  demo:\n    repo: " + origin + "\n    ref: main\n"
	if err := os.WriteFile(filepath.Join(config.RegistriesCacheDir(dir), "team.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	report := buildUpdateReportForTest(dir, time.Now(), installed, []PackUpdateResult{{
		Name: "demo", Method: config.MethodClone, Status: StatusUpToDate,
	}})
	if report.Results[0].OriginMigration != nil {
		t.Fatalf("unexpected migration = %+v", report.Results[0].OriginMigration)
	}
}

func TestBuildPackUpdateReport_UsesExecutionStateWithoutReadingConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installed := map[string]config.InstalledPackMeta{
		"legacy": {
			Method:   config.MethodArchive,
			Origin:   "https://example.com/legacy.zip",
			Declined: []domain.BundledCategory{domain.BundledProfiles},
		},
	}

	report := buildUpdateReportForTest(dir, time.Now(), installed, []PackUpdateResult{{
		Name:   "legacy",
		Status: StatusUpdated,
		BundledCandidates: &BundledCandidates{
			Profiles: []string{"team"},
		},
	}})
	result := report.Results[0]
	if result.Method != config.MethodArchive {
		t.Fatalf("method = %q", result.Method)
	}
	if len(result.Bundled.Available.PreviouslyDeclined) != 1 ||
		result.Bundled.Available.PreviouslyDeclined[0] != domain.BundledProfiles {
		t.Fatalf("available.previously_declined = %v", result.Bundled.Available.PreviouslyDeclined)
	}
	if len(result.Bundled.Available.New) != 0 {
		t.Fatalf("available.new = %v", result.Bundled.Available.New)
	}
	if len(result.Bundled.Preferences.Declined) != 1 ||
		result.Bundled.Preferences.Declined[0] != domain.BundledProfiles {
		t.Fatalf("preferences.declined = %v", result.Bundled.Preferences.Declined)
	}
	if _, err := os.Stat(config.LockfilePath(dir)); !os.IsNotExist(err) {
		t.Fatalf("report migrated lockfile, stat err=%v", err)
	}
}

func buildUpdateReportForTest(
	configDir string,
	checkedAt time.Time,
	installed map[string]config.InstalledPackMeta,
	results []PackUpdateResult,
) PackUpdateReport {
	execution := PackUpdateExecution{Results: results, Installed: installed}
	checkResults := BuildPackUpdateCheckResults(configDir, execution)
	return BuildPackUpdateReport(checkedAt, checkResults)
}
