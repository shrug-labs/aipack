package config

import "testing"

func TestValidateRegistry_OK(t *testing.T) {
	t.Parallel()
	reg := Registry{SchemaVersion: RegistrySchemaVersion, Packs: map[string]RegistryEntry{"demo": {Repo: "https://github.com/org/repo.git"}}}
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
