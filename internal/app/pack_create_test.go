package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPackCreate_ScaffoldsValidPack(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "my-pack")

	if err := PackCreate(PackCreateRequest{Dir: dir, Name: "my-pack"}); err != nil {
		t.Fatalf("PackCreate: %v", err)
	}

	// Verify pack.json is valid JSON.
	b, err := os.ReadFile(filepath.Join(dir, "pack.json"))
	if err != nil {
		t.Fatalf("read pack.json: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatalf("parse pack.json: %v", err)
	}
	if got, want := manifest["name"], "my-pack"; got != want {
		t.Fatalf("name = %v, want %v", got, want)
	}

	// Verify all vector dirs exist.
	for _, sub := range []string{"rules", "agents", "workflows", "skills", "mcp", "configs"} {
		d := filepath.Join(dir, sub)
		st, err := os.Stat(d)
		if err != nil {
			t.Fatalf("missing dir %s: %v", sub, err)
		}
		if !st.IsDir() {
			t.Fatalf("%s is not a directory", sub)
		}
	}
}

func TestPackCreate_DefaultsNameToBasename(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "cool-pack")

	if err := PackCreate(PackCreateRequest{Dir: dir}); err != nil {
		t.Fatalf("PackCreate: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "pack.json"))
	if err != nil {
		t.Fatalf("read pack.json: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatalf("parse pack.json: %v", err)
	}
	if got, want := manifest["name"], "cool-pack"; got != want {
		t.Fatalf("name = %v, want %v", got, want)
	}
}

func TestPackCreate_ErrorOnExistingPackJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := PackCreate(PackCreateRequest{Dir: dir, Name: "test"})
	if err == nil {
		t.Fatal("expected error on existing pack.json")
	}
}

func TestPackCreate_ErrorOnEmptyDir(t *testing.T) {
	t.Parallel()
	err := PackCreate(PackCreateRequest{Dir: ""})
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
}
