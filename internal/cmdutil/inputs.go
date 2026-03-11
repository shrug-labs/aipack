package cmdutil

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

const DefaultHarnessEnv = "AIPACK_DEFAULT_HARNESS"

// ResolveHarnesses resolves a list of harness name strings to typed Harness values.
// If the input list is empty, falls back to the AIPACK_DEFAULT_HARNESS env var.
func ResolveHarnesses(harnesses []string) ([]domain.Harness, error) {
	hsRaw := harnesses
	if len(hsRaw) == 0 {
		envVal := os.Getenv(DefaultHarnessEnv)
		envDefault := ParseHarnessEnv(envVal)
		if len(envDefault) == 0 {
			return nil, fmt.Errorf("no harness configured (set --harness, defaults.harnesses in sync-config, or %s)", DefaultHarnessEnv)
		}
		hsRaw = envDefault
	}

	var hs []domain.Harness
	for _, h := range hsRaw {
		hv, err := NormalizeHarness(h)
		if err != nil {
			return nil, err
		}
		hs = append(hs, hv)
	}
	return hs, nil
}

func NormalizeHarness(raw string) (domain.Harness, error) {
	if h, ok := domain.ParseHarness(raw); ok {
		return h, nil
	}
	return "", fmt.Errorf("unknown harness: %s", raw)
}

func HarnessChoices() string {
	return domain.HarnessNamesJoined("|")
}

func ParseHarnessEnv(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	return out
}

func NormalizeScope(scope string) (domain.Scope, error) {
	s := strings.ToLower(strings.TrimSpace(scope))
	switch s {
	case string(domain.ScopeProject):
		return domain.ScopeProject, nil
	case string(domain.ScopeGlobal):
		return domain.ScopeGlobal, nil
	default:
		return "", fmt.Errorf("unknown scope %q (expected %q or %q)", scope, domain.ScopeProject, domain.ScopeGlobal)
	}
}

// ResolveConfigDir returns the config dir, using the default if empty.
func ResolveConfigDir(configDir string, home string) (string, error) {
	if configDir != "" {
		return configDir, nil
	}
	d, err := config.DefaultConfigDir(home)
	if err != nil {
		return "", fmt.Errorf("HOME is not set; pass --config-dir")
	}
	return d, nil
}

// EnsureConfigDir resolves the config dir and auto-initializes it if it does
// not yet exist. Prints a notice to stderr on first-time creation.
func EnsureConfigDir(configDir, home string, stderr io.Writer) (string, error) {
	dir, err := ResolveConfigDir(configDir, home)
	if err != nil {
		return "", err
	}
	created, err := config.EnsureInit(dir)
	if err != nil {
		return "", err
	}
	if created {
		fmt.Fprintf(stderr, "Initialized config: %s\n", dir)
	}
	return dir, nil
}

// PrintWarnings writes domain.Warning entries to w in a consistent format.
func PrintWarnings(w io.Writer, warnings []domain.Warning) {
	for _, warn := range warnings {
		fmt.Fprintf(w, "warning: %s\n", warn.String())
	}
}

// WriteJSON marshals v as indented JSON and writes it to w.
// Intended for CLI --json output paths.
func WriteJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, _ = w.Write(append(b, '\n'))
	return nil
}

// IsRegistryName returns true if arg looks like a registry pack name rather
// than a local file path (no path separators and not an existing path on disk).
func IsRegistryName(arg string) bool {
	if strings.ContainsAny(arg, "/\\") {
		return false
	}
	return !util.PathExists(arg)
}

func ResolveProjectDir(repoRoot string, projectDir string) (string, error) {
	if filepath.IsAbs(projectDir) {
		return filepath.Abs(projectDir)
	}
	// Interpret relative project dirs as relative to repo root, not the current CWD.
	return filepath.Abs(filepath.Join(repoRoot, projectDir))
}
