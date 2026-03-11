package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/util"
)

// PackCreateRequest describes a pack scaffolding request.
type PackCreateRequest struct {
	Dir  string // directory to create
	Name string // pack name (defaults to dir basename)
}

// PackCreate scaffolds a new pack directory with all capability vector subdirs.
func PackCreate(req PackCreateRequest) error {
	dir := req.Dir
	if dir == "" {
		return fmt.Errorf("pack directory is required")
	}

	name := req.Name
	if name == "" {
		name = filepath.Base(dir)
	}

	// Error if pack.json already exists.
	manifestPath := filepath.Join(dir, "pack.json")
	if _, err := os.Stat(manifestPath); err == nil {
		return fmt.Errorf("pack.json already exists: %s", manifestPath)
	}

	// Create all capability vector dirs.
	dirs := []string{
		filepath.Join(dir, "rules"),
		filepath.Join(dir, "agents"),
		filepath.Join(dir, "workflows"),
		filepath.Join(dir, "skills"),
		filepath.Join(dir, "prompts"),
		filepath.Join(dir, "mcp"),
		filepath.Join(dir, "configs"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", d, err)
		}
		// Create .gitkeep so empty dirs are tracked.
		gitkeep := filepath.Join(d, ".gitkeep")
		if _, err := os.Stat(gitkeep); os.IsNotExist(err) {
			if err := os.WriteFile(gitkeep, nil, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", gitkeep, err)
			}
		}
	}

	manifest := map[string]any{
		"schema_version": 1,
		"name":           name,
		"version":        "0.1.0",
		"root":           ".",
		"rules":          []any{},
		"agents":         []any{},
		"workflows":      []any{},
		"skills":         []any{},
		"prompts":        []any{},
		"mcp":            map[string]any{"servers": map[string]any{}},
		"configs":        map[string]any{"harness_settings": map[string]any{}},
	}

	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pack manifest: %w", err)
	}
	b = append(b, '\n')
	if err := util.WriteFileAtomicWithPerms(manifestPath, b, 0o755, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", manifestPath, err)
	}
	return nil
}
