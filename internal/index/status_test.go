package index_test

import (
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/index"
)

func TestSearchStatusFilters(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	if err := db.Update(index.PackInfo{Name: "installed-pack", Version: "1.0.0"}, []index.Resource{
		{Kind: "skill", Name: "runbook", Description: "Installed skill"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "registered-pack", Description: "Registered pack"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateInspectedIndex(index.PackInfo{Name: "inspected-pack", Description: "Inspected pack"}, []index.Resource{
		{Kind: "rule", Name: "preview", Description: "Inspected rule"},
	}); err != nil {
		t.Fatal(err)
	}

	installed, err := db.Search("", index.SearchFilters{Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].Status != "installed" || installed[0].Pack != "installed-pack" {
		t.Fatalf("installed results = %+v", installed)
	}

	registered, err := db.Search("", index.SearchFilters{Status: "registered"})
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 || registered[0].Status != "registered" || registered[0].Pack != "registered-pack" {
		t.Fatalf("registered results = %+v", registered)
	}

	inspected, err := db.Search("", index.SearchFilters{Status: "inspected", Kind: "rule"})
	if err != nil {
		t.Fatal(err)
	}
	if len(inspected) != 1 || inspected[0].Status != "inspected" || inspected[0].Pack != "inspected-pack" {
		t.Fatalf("inspected results = %+v", inspected)
	}
}

func TestDropStaleInspected_RemovesOldInspectedOnly(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	// Seed an installed pack — must survive the drop.
	if err := db.Update(index.PackInfo{Name: "installed-pack"}, []index.Resource{
		{Kind: "skill", Name: "runbook"},
	}); err != nil {
		t.Fatal(err)
	}
	// Seed a registered pack — must survive the drop.
	if err := db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "registered-pack", Description: "Registered pack"},
	}); err != nil {
		t.Fatal(err)
	}
	// Seed an inspected pack now (last_indexed_at = now).
	if err := db.UpdateInspectedIndex(index.PackInfo{Name: "fresh-inspected"}, []index.Resource{
		{Kind: "rule", Name: "fresh"},
	}); err != nil {
		t.Fatal(err)
	}
	// Backdate a separate inspected pack so the TTL drop targets it.
	if err := db.UpdateInspectedIndex(index.PackInfo{Name: "stale-inspected"}, []index.Resource{
		{Kind: "rule", Name: "stale"},
	}); err != nil {
		t.Fatal(err)
	}
	staleAt := time.Now().Add(-90 * 24 * time.Hour).Unix()
	if _, err := db.Exec(`UPDATE packs SET last_indexed_at = ? WHERE name = 'stale-inspected'`, staleAt); err != nil {
		t.Fatal(err)
	}

	removed, err := db.DropStaleInspected(time.Now(), 30*24*time.Hour)
	if err != nil {
		t.Fatalf("DropStaleInspected: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 stale row removed, got %d", removed)
	}

	all, err := db.Search("", index.SearchFilters{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, r := range all {
		names[r.Pack] = true
	}
	if !names["installed-pack"] || !names["registered-pack"] || !names["fresh-inspected"] {
		t.Fatalf("expected installed/registered/fresh to survive, got %v", names)
	}
	if names["stale-inspected"] {
		t.Fatalf("stale inspected row should have been dropped, got %v", names)
	}
}

func TestClearInspected_RemovesInspectedOnly(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.Update(index.PackInfo{Name: "installed-pack"}, []index.Resource{
		{Kind: "skill", Name: "runbook"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "registered-pack", Description: "Registered pack"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateInspectedIndex(index.PackInfo{Name: "inspected-a"}, []index.Resource{
		{Kind: "rule", Name: "preview-a"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateInspectedIndex(index.PackInfo{Name: "inspected-b"}, []index.Resource{
		{Kind: "rule", Name: "preview-b"},
	}); err != nil {
		t.Fatal(err)
	}

	removed, err := db.ClearInspected()
	if err != nil {
		t.Fatalf("ClearInspected: %v", err)
	}
	if removed != 2 {
		t.Fatalf("expected 2 inspected rows removed, got %d", removed)
	}

	inspected, err := db.Search("", index.SearchFilters{Status: "inspected"})
	if err != nil {
		t.Fatal(err)
	}
	if len(inspected) != 0 {
		t.Fatalf("expected no inspected rows, got %+v", inspected)
	}

	installed, err := db.Search("", index.SearchFilters{Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].Pack != "installed-pack" {
		t.Fatalf("installed rows disturbed: %+v", installed)
	}

	registered, err := db.Search("", index.SearchFilters{Status: "registered"})
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 || registered[0].Pack != "registered-pack" {
		t.Fatalf("registered rows disturbed: %+v", registered)
	}
}

func TestClearInspected_RestoresPreviouslyRegisteredPack(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "registered-pack", Description: "Registered pack"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateInspectedIndex(index.PackInfo{Name: "registered-pack", Description: "Inspected pack"}, []index.Resource{
		{Kind: "rule", Name: "preview"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClearInspected(); err != nil {
		t.Fatalf("ClearInspected: %v", err)
	}

	registered, err := db.Search("", index.SearchFilters{Status: "registered"})
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 || registered[0].Pack != "registered-pack" || registered[0].Status != "registered" {
		t.Fatalf("registered pack should survive inspected clear, got %+v", registered)
	}
}

func TestClearInspected_RestoresDeepIndexedResources(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.UpdateDeepIndex(index.PackInfo{Name: "deep-pack", Description: "Deep indexed pack"}, []index.Resource{
		{Kind: "skill", Name: "runbook", Description: "Deep indexed runbook"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateInspectedIndex(index.PackInfo{Name: "deep-pack", Description: "Inspected pack"}, []index.Resource{
		{Kind: "rule", Name: "preview", Description: "Inspected preview"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClearInspected(); err != nil {
		t.Fatalf("ClearInspected: %v", err)
	}

	registered, err := db.Search("Deep", index.SearchFilters{Status: "registered", Kind: "skill"})
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 || registered[0].Pack != "deep-pack" || registered[0].Name != "runbook" {
		t.Fatalf("deep-indexed resource should survive inspected clear, got %+v", registered)
	}

	inspected, err := db.Search("", index.SearchFilters{Status: "inspected"})
	if err != nil {
		t.Fatal(err)
	}
	if len(inspected) != 0 {
		t.Fatalf("inspected resources should be cleared, got %+v", inspected)
	}
}

func TestDropStaleInspected_RestoresDeepIndexedResources(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.UpdateDeepIndex(index.PackInfo{Name: "deep-pack", Description: "Deep indexed pack"}, []index.Resource{
		{Kind: "skill", Name: "runbook", Description: "Deep indexed runbook"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateInspectedIndex(index.PackInfo{Name: "deep-pack", Description: "Inspected pack"}, []index.Resource{
		{Kind: "rule", Name: "preview", Description: "Inspected preview"},
	}); err != nil {
		t.Fatal(err)
	}
	staleAt := time.Now().Add(-90 * 24 * time.Hour).Unix()
	if _, err := db.Exec(`UPDATE packs SET last_indexed_at = ? WHERE name = 'deep-pack'`, staleAt); err != nil {
		t.Fatal(err)
	}

	if _, err := db.DropStaleInspected(time.Now(), 30*24*time.Hour); err != nil {
		t.Fatalf("DropStaleInspected: %v", err)
	}

	registered, err := db.Search("Deep", index.SearchFilters{Status: "registered", Kind: "skill"})
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 || registered[0].Pack != "deep-pack" || registered[0].Name != "runbook" {
		t.Fatalf("deep-indexed resource should survive stale inspected drop, got %+v", registered)
	}
}

func TestUpdateRegistryPacks_RestoresInspectedPackToRegistered(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "registered-pack", Description: "Registered pack"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateInspectedIndex(index.PackInfo{Name: "registered-pack", Description: "Inspected pack"}, []index.Resource{
		{Kind: "rule", Name: "preview"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "registered-pack", Description: "Registered pack refreshed"},
	}); err != nil {
		t.Fatal(err)
	}

	registered, err := db.Search("", index.SearchFilters{Status: "registered"})
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 || registered[0].Pack != "registered-pack" || registered[0].Status != "registered" {
		t.Fatalf("registry refresh should restore registered status, got %+v", registered)
	}
}

func TestUpdateRegistryPacks_PreservesDeepIndexedResources(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "registered-pack", Description: "Registered pack"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateDeepIndex(index.PackInfo{Name: "registered-pack", Description: "Deep indexed pack"}, []index.Resource{
		{Kind: "skill", Name: "runbook", Description: "Indexed runbook"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRegistryPacks([]index.PackInfo{
		{Name: "registered-pack", Description: "Registered pack refreshed"},
	}); err != nil {
		t.Fatal(err)
	}

	results, err := db.Search("Indexed", index.SearchFilters{Status: "registered", Kind: "skill"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Pack != "registered-pack" || results[0].Name != "runbook" {
		t.Fatalf("registry refresh should preserve deep-indexed resources, got %+v", results)
	}
}

func TestUpdateInspectedIndex_DropsStaleOpportunistically(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	// Seed a stale inspected pack.
	if err := db.UpdateInspectedIndex(index.PackInfo{Name: "stale-inspected"}, []index.Resource{
		{Kind: "rule", Name: "stale"},
	}); err != nil {
		t.Fatal(err)
	}
	staleAt := time.Now().Add(-90 * 24 * time.Hour).Unix()
	if _, err := db.Exec(`UPDATE packs SET last_indexed_at = ? WHERE name = 'stale-inspected'`, staleAt); err != nil {
		t.Fatal(err)
	}

	// New inspect must drop the stale row as a side effect.
	if err := db.UpdateInspectedIndex(index.PackInfo{Name: "fresh-inspected"}, []index.Resource{
		{Kind: "rule", Name: "fresh"},
	}); err != nil {
		t.Fatal(err)
	}

	results, err := db.Search("", index.SearchFilters{Status: "inspected"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Pack == "stale-inspected" {
			t.Fatalf("stale inspected row should have been opportunistically dropped, got %+v", results)
		}
	}
}
