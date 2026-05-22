package config

import (
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestValidateRegistry_OK(t *testing.T) {
	t.Parallel()
	reg := Registry{
		SchemaVersion: RegistrySchemaVersion,
		Packs:         map[string]RegistryEntry{"demo": {Repo: "https://github.com/org/repo.git"}},
		Collections: map[string]RegistryCollection{
			"starter": {Packs: []RegistryCollectionPack{{Name: "demo", With: []string{"profiles"}}}},
		},
	}
	errs := ValidateRegistry(reg)
	if len(errs) > 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidateRegistry_ArchiveEntry(t *testing.T) {
	t.Parallel()
	reg := Registry{SchemaVersion: RegistrySchemaVersion, Packs: map[string]RegistryEntry{
		"demo": {Method: MethodArchive, URL: "https://example.com/demo.zip"},
	}}
	errs := ValidateRegistry(reg)
	if len(errs) > 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidateRegistry_MissingRepo(t *testing.T) {
	t.Parallel()
	reg := Registry{SchemaVersion: RegistrySchemaVersion, Packs: map[string]RegistryEntry{"demo": {}}}
	errs := ValidateRegistry(reg)
	if len(errs) == 0 {
		t.Fatal("expected error for missing repo")
	}
}

func TestValidateRegistry_ArchiveMissingURL(t *testing.T) {
	t.Parallel()
	reg := Registry{SchemaVersion: RegistrySchemaVersion, Packs: map[string]RegistryEntry{
		"demo": {Method: MethodArchive},
	}}
	errs := ValidateRegistry(reg)
	if len(errs) == 0 {
		t.Fatal("expected error for missing archive url")
	}
}

func TestValidateRegistry_UnknownMethod(t *testing.T) {
	t.Parallel()
	reg := Registry{SchemaVersion: RegistrySchemaVersion, Packs: map[string]RegistryEntry{
		"demo": {Method: "mystery", Repo: "https://github.com/org/repo.git"},
	}}
	errs := ValidateRegistry(reg)
	if len(errs) == 0 {
		t.Fatal("expected error for unknown method")
	}
}

func TestValidateRegistry_ContentPathsRejectsInvalidCategory(t *testing.T) {
	t.Parallel()
	reg := Registry{SchemaVersion: RegistrySchemaVersion, Packs: map[string]RegistryEntry{
		"demo": {
			Repo: "https://github.com/org/repo.git",
			ContentPaths: map[domain.PackCategory]string{
				domain.CategoryMCP: "servers",
			},
		},
	}}
	errs := ValidateRegistry(reg)
	if len(errs) == 0 {
		t.Fatal("expected error for invalid content_paths category")
	}
}

func TestValidateRegistry_ContentPathsRejectsEscapes(t *testing.T) {
	t.Parallel()
	reg := Registry{SchemaVersion: RegistrySchemaVersion, Packs: map[string]RegistryEntry{
		"demo": {
			Repo: "https://github.com/org/repo.git",
			ContentPaths: map[domain.PackCategory]string{
				domain.CategoryRules: "../outside",
			},
		},
	}}
	errs := ValidateRegistry(reg)
	if len(errs) == 0 {
		t.Fatal("expected error for escaping content_paths path")
	}
}

func TestValidateRegistry_CollectionRequiresPacks(t *testing.T) {
	t.Parallel()
	reg := Registry{
		SchemaVersion: RegistrySchemaVersion,
		Packs:         map[string]RegistryEntry{"demo": {Repo: "https://github.com/org/repo.git"}},
		Collections:   map[string]RegistryCollection{"starter": {}},
	}
	errs := ValidateRegistry(reg)
	if len(errs) == 0 {
		t.Fatal("expected error for empty collection")
	}
}

func TestValidateRegistry_CollectionRejectsBadPack(t *testing.T) {
	t.Parallel()
	reg := Registry{
		SchemaVersion: RegistrySchemaVersion,
		Packs:         map[string]RegistryEntry{"demo": {Repo: "https://github.com/org/repo.git"}},
		Collections: map[string]RegistryCollection{
			"starter": {Packs: []RegistryCollectionPack{{}, {Name: "demo"}, {Name: "demo"}}},
		},
	}
	errs := ValidateRegistry(reg)
	if len(errs) < 2 {
		t.Fatalf("expected errors for empty and duplicate pack refs, got %v", errs)
	}
}

func TestValidateRegistry_CollectionRejectsBadWith(t *testing.T) {
	t.Parallel()
	reg := Registry{
		SchemaVersion: RegistrySchemaVersion,
		Packs:         map[string]RegistryEntry{"demo": {Repo: "https://github.com/org/repo.git"}},
		Collections: map[string]RegistryCollection{
			"starter": {Packs: []RegistryCollectionPack{{Name: "demo", With: []string{"plugins"}}}},
		},
	}
	errs := ValidateRegistry(reg)
	if len(errs) == 0 {
		t.Fatal("expected error for invalid collection with value")
	}
}

func TestValidateRegistry_CollectionRejectsArchiveRef(t *testing.T) {
	t.Parallel()
	reg := Registry{
		SchemaVersion: RegistrySchemaVersion,
		Packs: map[string]RegistryEntry{
			"archive": {Method: MethodArchive, URL: "https://example.com/archive.zip"},
		},
		Collections: map[string]RegistryCollection{
			"starter": {Packs: []RegistryCollectionPack{{Name: "archive", Ref: "v1.0.0"}}},
		},
	}
	errs := ValidateRegistry(reg)
	if len(errs) == 0 {
		t.Fatal("expected error for archive pack ref")
	}
}
