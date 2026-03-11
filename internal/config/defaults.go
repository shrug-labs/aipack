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
	"  harnesses: [cline]\n" +
	"  scope: project\n")

// InitProfileBytes is the content written into profiles/default.yaml by init.
var InitProfileBytes = []byte("schema_version: 1\n" +
	"packs: []\n")

// InitRegistryBytes is the content written into registry.yaml by init.
var InitRegistryBytes = []byte("schema_version: 1\n" +
	"packs: {}\n" +
	"# Example:\n" +
	"#   my-pack:\n" +
	"#     repo: https://github.com/org/repo.git\n" +
	"#     path: packs/my-pack       # subdirectory within repo (omit for root)\n" +
	"#     description: My team's AI agent pack\n" +
	"#     owner: Team Name\n" +
	"#     contact: \"#slack-channel\"\n")

// EnsureInit creates the config directory and writes default config files if
// the directory does not already exist. Returns true if first-time creation
// occurred, false if the directory was already present.
func EnsureInit(configDir string) (bool, error) {
	if _, err := os.Stat(configDir); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return false, fmt.Errorf("creating config dir: %w", err)
	}
	files := []struct {
		path    string
		content []byte
	}{
		{SyncConfigPath(configDir), InitSyncConfigBytes},
		{filepath.Join(configDir, "profiles", "default.yaml"), InitProfileBytes},
		{filepath.Join(configDir, "registry.yaml"), InitRegistryBytes},
	}
	for _, f := range files {
		if err := writeIfNotExists(f.path, f.content); err != nil {
			return false, err
		}
	}
	return true, nil
}

func writeIfNotExists(path string, content []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o600)
}
