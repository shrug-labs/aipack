package app

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
)

// EnvEntry is one key/value pair from the config-dir .env file.
type EnvEntry struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Length int    `json:"length"`
}

// EnvListResult holds the contents of the config-dir .env file.
type EnvListResult struct {
	Path    string     `json:"path"`
	Entries []EnvEntry `json:"entries"`
}

// EnvList loads the config-dir .env file and returns its keys plus value
// lengths. Values are populated only when includeValues is true; the masked
// form is the default so screen-shared output doesn't leak secrets.
func EnvList(configDir string, includeValues bool) (EnvListResult, error) {
	if configDir == "" {
		return EnvListResult{}, fmt.Errorf("config dir is required")
	}
	path := config.DotEnvPath(configDir)
	values, err := config.LoadDotEnv(path)
	if err != nil {
		return EnvListResult{}, err
	}
	keys := slices.Sorted(maps.Keys(values))
	entries := make([]EnvEntry, 0, len(keys))
	for _, k := range keys {
		entry := EnvEntry{Key: k, Length: len(values[k])}
		if includeValues {
			entry.Value = values[k]
		}
		entries = append(entries, entry)
	}
	return EnvListResult{Path: path, Entries: entries}, nil
}

// EnvGet returns the value for a single key, or "", false if absent.
func EnvGet(configDir, key string) (string, bool, error) {
	if configDir == "" {
		return "", false, fmt.Errorf("config dir is required")
	}
	if !config.IsValidDotEnvKey(key) {
		return "", false, fmt.Errorf("invalid env key %q", key)
	}
	values, err := config.LoadDotEnv(config.DotEnvPath(configDir))
	if err != nil {
		return "", false, err
	}
	v, ok := values[key]
	return v, ok, nil
}

// EnvSet writes a KEY=value entry to the config-dir .env file. Existing
// entries are replaced in place; new entries append at end-of-file. Comments,
// blank lines, and unrelated entries are preserved. Refuses keys that don't
// match the .env identifier rules and values containing newlines.
func EnvSet(configDir, key, value string) error {
	if configDir == "" {
		return fmt.Errorf("config dir is required")
	}
	if !config.IsValidDotEnvKey(strings.TrimSpace(key)) {
		return fmt.Errorf("invalid env key %q", key)
	}
	return config.SetDotEnv(config.DotEnvPath(configDir), strings.TrimSpace(key), value)
}

// EnvUnset removes a KEY entry from the config-dir .env file. Missing entries
// are not an error.
func EnvUnset(configDir, key string) error {
	if configDir == "" {
		return fmt.Errorf("config dir is required")
	}
	if !config.IsValidDotEnvKey(strings.TrimSpace(key)) {
		return fmt.Errorf("invalid env key %q", key)
	}
	return config.UnsetDotEnv(config.DotEnvPath(configDir), strings.TrimSpace(key))
}

// EnvPath returns the absolute path to the config-dir .env file. Useful for
// `aipack config env path` and for printing in setup checklists.
func EnvPath(configDir string) string {
	if configDir == "" {
		return ""
	}
	return config.DotEnvPath(configDir)
}
