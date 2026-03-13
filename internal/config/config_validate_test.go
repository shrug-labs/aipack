package config

import "testing"

func TestValidateProfileConfig_OK(t *testing.T) {
	t.Parallel()
	cfg := ProfileConfig{SchemaVersion: ProfileSchemaVersion, Packs: []PackEntry{{Name: "demo"}}}
	errs := ValidateProfileConfig(cfg)
	if len(errs) > 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidateProfileConfig_BadSchemaVersion(t *testing.T) {
	t.Parallel()
	cfg := ProfileConfig{SchemaVersion: 99, Packs: []PackEntry{{Name: "demo"}}}
	errs := ValidateProfileConfig(cfg)
	if len(errs) == 0 {
		t.Fatal("expected error for bad schema_version")
	}
}

func TestValidateProfileConfig_EmptyPackName(t *testing.T) {
	t.Parallel()
	cfg := ProfileConfig{SchemaVersion: ProfileSchemaVersion, Packs: []PackEntry{{Name: ""}}}
	errs := ValidateProfileConfig(cfg)
	if len(errs) == 0 {
		t.Fatal("expected error for empty pack name")
	}
}

func TestValidateProfileConfig_DuplicatePackName(t *testing.T) {
	t.Parallel()
	cfg := ProfileConfig{SchemaVersion: ProfileSchemaVersion, Packs: []PackEntry{{Name: "a"}, {Name: "a"}}}
	errs := ValidateProfileConfig(cfg)
	if len(errs) == 0 {
		t.Fatal("expected error for duplicate pack name")
	}
}
