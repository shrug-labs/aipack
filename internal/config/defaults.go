package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// InitSyncConfigBytes is the content written into sync-config.yaml by init.
var InitSyncConfigBytes = []byte("schema_version: 1\n" +
	"defaults:\n" +
	"  profile: default\n" +
	"  harnesses: [codex]\n" +
	"  scope: global\n" +
	"  collision_strategy: last-wins\n" +
	"  auto_sync: false\n")

// InitProfileBytes is the content written into profiles/default.yaml by init.
var InitProfileBytes = []byte("schema_version: 2\n" +
	"packs: []\n")

// InitEnvBytes is the empty placeholder written into .env by init.
var InitEnvBytes = []byte("")

// DotEnvPath returns the config-local env file path.
func DotEnvPath(configDir string) string {
	return filepath.Join(configDir, ".env")
}

// EnsureInit ensures the config directory and default files exist. Missing
// files are created without overwriting existing ones. Returns true if any
// file was written, false when everything was already in place.
func EnsureInit(configDir string) (bool, error) {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return false, fmt.Errorf("creating config dir: %w", err)
	}
	files := []struct {
		path    string
		content []byte
	}{
		{SyncConfigPath(configDir), InitSyncConfigBytes},
		{filepath.Join(configDir, "profiles", "default.yaml"), InitProfileBytes},
		{DotEnvPath(configDir), InitEnvBytes},
	}
	wrote := false
	for _, f := range files {
		created, err := writeIfNotExists(f.path, f.content)
		if err != nil {
			return false, err
		}
		if created {
			wrote = true
		}
	}
	return wrote, nil
}

func writeIfNotExists(path string, content []byte) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, content, 0o600)
}
