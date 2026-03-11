package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

// AdoptFileRequest holds parameters for adopting a single untracked harness file
// into an existing pack.
type AdoptFileRequest struct {
	TargetSpec
	ConfigDir   string
	PackName    string
	HarnessPath string // absolute path to the harness file
	Category    string // rules, agents, workflows, skills, mcp
	RelPath     string // relative name within category (e.g. "triage" for rules/triage.md)
}

// ResolvePackRootWithFallback resolves the pack root from a manifest, falling
// back to the pack install directory if the manifest root is empty.
func ResolvePackRootWithFallback(manifestPath string, manifest config.PackManifest, fallback string) string {
	if root := config.ResolvePackRoot(manifestPath, manifest.Root); root != "" {
		return root
	}
	return fallback
}

// CategoryExt returns the file extension for a content category.
func CategoryExt(category string) string {
	if category == "mcp" {
		return ".json"
	}
	return ".md"
}

// AdoptFile copies a single untracked harness file into the named pack's
// directory and updates the ledger so the file becomes tracked.
func AdoptFile(req AdoptFileRequest) error {
	packRoot := filepath.Join(req.ConfigDir, "packs", req.PackName)
	manifestPath := filepath.Join(packRoot, "pack.json")

	manifest, err := config.LoadPackManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("loading pack manifest: %w", err)
	}
	resolvedRoot := ResolvePackRootWithFallback(manifestPath, manifest, packRoot)

	// Build destination path: <pack_root>/<category>/<relpath>.md (or .json for mcp).
	dst := filepath.Join(resolvedRoot, req.Category, req.RelPath+CategoryExt(req.Category))

	// Read source content.
	src := filepath.Clean(req.HarnessPath)
	content, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading harness file: %w", err)
	}

	// Write to pack.
	if err := saveContentToPack(content, dst); err != nil {
		return fmt.Errorf("writing to pack: %w", err)
	}

	// Update pack manifest if the ID is not already listed.
	updated := addToManifest(&manifest, req.Category, req.RelPath)
	if updated {
		if err := config.SavePackManifest(manifestPath, manifest); err != nil {
			return fmt.Errorf("updating pack manifest: %w", err)
		}
	}

	// Update ledger so this file is now tracked.
	ledgerPath := ledgerPathForScope(req.Scope, req.ProjectDir, req.Home, req.Harnesses)
	lg, _, err := engine.LoadLedger(ledgerPath)
	if err != nil {
		return fmt.Errorf("loading ledger: %w", err)
	}
	if lg.Managed == nil {
		lg.Managed = map[string]domain.Entry{}
	}
	lg.Managed[src] = domain.Entry{
		SourcePack: req.PackName,
		Digest:     domain.SingleFileDigest(content),
	}
	if err := engine.SaveLedger(ledgerPath, lg, false); err != nil {
		return fmt.Errorf("saving ledger: %w", err)
	}

	return nil
}

// MoveFileRequest holds parameters for moving a file from one pack to another.
type MoveFileRequest struct {
	AdoptFileRequest
	FromPackName string // source pack to remove from
}

// MoveFile moves a harness file from one pack to another: writes to the
// destination pack, removes from the source pack, and updates the ledger.
func MoveFile(req MoveFileRequest) error {
	// Write to destination pack + update ledger.
	if err := AdoptFile(req.AdoptFileRequest); err != nil {
		return fmt.Errorf("writing to destination pack: %w", err)
	}

	// Remove from source pack.
	srcPackRoot := filepath.Join(req.ConfigDir, "packs", req.FromPackName)
	srcManifestPath := filepath.Join(srcPackRoot, "pack.json")
	srcManifest, err := config.LoadPackManifest(srcManifestPath)
	if err != nil {
		return fmt.Errorf("loading source pack manifest: %w", err)
	}
	srcResolvedRoot := ResolvePackRootWithFallback(srcManifestPath, srcManifest, srcPackRoot)

	srcFile := filepath.Join(srcResolvedRoot, req.Category, req.RelPath+CategoryExt(req.Category))

	// Delete the file from the source pack.
	if err := os.Remove(srcFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing from source pack: %w", err)
	}

	// Update source pack manifest.
	if removeFromManifest(&srcManifest, req.Category, req.RelPath) {
		if err := config.SavePackManifest(srcManifestPath, srcManifest); err != nil {
			return fmt.Errorf("updating source pack manifest: %w", err)
		}
	}

	return nil
}

// manifestListForCategory returns a pointer to the manifest's content list for
// the given category, or nil for "mcp" and unknown categories.
func manifestListForCategory(m *config.PackManifest, category string) *[]string {
	switch category {
	case "rules":
		return &m.Rules
	case "agents":
		return &m.Agents
	case "workflows":
		return &m.Workflows
	case "skills":
		return &m.Skills
	default:
		return nil
	}
}

// addToManifest adds an ID to the appropriate manifest list if not present.
// Returns true if the manifest was modified.
func addToManifest(m *config.PackManifest, category, id string) bool {
	if category == "mcp" {
		if m.MCP.Servers == nil {
			m.MCP.Servers = map[string]config.MCPDefaults{}
		}
		if _, exists := m.MCP.Servers[id]; !exists {
			m.MCP.Servers[id] = config.MCPDefaults{}
			return true
		}
		return false
	}

	list := manifestListForCategory(m, category)
	if list == nil {
		return false
	}
	for _, existing := range *list {
		if strings.EqualFold(existing, id) {
			return false
		}
	}
	*list = append(*list, id)
	return true
}

// removeFromManifest removes an ID from the appropriate manifest list.
// Returns true if the manifest was modified.
func removeFromManifest(m *config.PackManifest, category, id string) bool {
	if category == "mcp" {
		if _, exists := m.MCP.Servers[id]; exists {
			delete(m.MCP.Servers, id)
			return true
		}
		return false
	}

	list := manifestListForCategory(m, category)
	if list == nil {
		return false
	}
	for i, existing := range *list {
		if strings.EqualFold(existing, id) {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return true
		}
	}
	return false
}
