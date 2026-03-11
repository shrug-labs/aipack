package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ResolveProfilePath returns the absolute path to a profile YAML file.
// If profilePath is provided, it is used directly (absolutized if relative).
// Otherwise, the profile name is normalized and resolved under configDir.
func ResolveProfilePath(profilePath string, configDir string, profile string, home string) (string, error) {
	if profilePath != "" {
		if !filepath.IsAbs(profilePath) {
			abs, err := filepath.Abs(profilePath)
			if err != nil {
				return "", err
			}
			profilePath = abs
		}
		return profilePath, nil
	}

	profileName, err := NormalizeProfileName(profile)
	if err != nil {
		return "", err
	}

	if configDir == "" {
		d, err := DefaultConfigDir(home)
		if err != nil {
			return "", fmt.Errorf("HOME is not set; pass --profile-path or --config-dir")
		}
		configDir = d
	}
	return filepath.Join(configDir, "profiles", profileName+".yaml"), nil
}

// NormalizeProfileName validates and normalizes a profile name.
// An empty name defaults to "default". Only [A-Za-z0-9._-] are allowed.
func NormalizeProfileName(profile string) (string, error) {
	name := strings.TrimSpace(profile)
	if name == "" {
		return "default", nil
	}
	if strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid profile %q: traversal is not allowed", profile)
	}
	if strings.ContainsAny(name, `/\\`) {
		return "", fmt.Errorf("invalid profile %q: path separators are not allowed", profile)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return "", fmt.Errorf("invalid profile %q: only [A-Za-z0-9._-] are allowed", profile)
	}
	return name, nil
}
