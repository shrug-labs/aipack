package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/source"
)

// PackShowEntry describes detailed information about an installed pack.
type PackShowEntry struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`       // pack.json version (informational)
	Pin         string   `json:"pin,omitempty"` // lockfile version pin: "1.2.3", commit hash, or "" for HEAD
	Path        string   `json:"path"`
	Method      string   `json:"method"`
	Origin      string   `json:"origin"`
	Ref         string   `json:"ref,omitempty"`
	CommitHash  string   `json:"commit_hash,omitempty"`
	InstalledAt string   `json:"installed_at,omitempty"`
	Rules       []string `json:"rules"`
	Agents      []string `json:"agents"`
	Workflows   []string `json:"workflows"`
	Skills      []string `json:"skills"`
	Prompts     []string `json:"prompts"`
	MCPServers  []string `json:"mcp_servers"`
	Extras      []string `json:"extras,omitempty"`
}

// PinLabel returns a human-readable label for the lockfile pin:
// "v1.2.3 (pinned)" for semver pins, "@abc1234 (pinned)" for commit pins,
// or "" when the pack is not pinned. Single source of truth shared by the
// CLI's pack list/show output and the TUI's details panel.
func (e PackShowEntry) PinLabel() string {
	if e.Pin == "" {
		return ""
	}
	switch source.ClassifyVersion(e.Pin) {
	case source.VersionSemver:
		return "v" + source.StripVersionPrefix(e.Pin) + " (pinned)"
	case source.VersionCommit:
		return "@" + e.Pin + " (pinned)"
	default:
		return e.Pin + " (pinned)"
	}
}

// ContentIDs returns the resource IDs for the given category.
func (e PackShowEntry) ContentIDs(cat domain.PackCategory) []string {
	switch cat {
	case domain.CategoryRules:
		return e.Rules
	case domain.CategoryAgents:
		return e.Agents
	case domain.CategoryWorkflows:
		return e.Workflows
	case domain.CategorySkills:
		return e.Skills
	case domain.CategoryPrompts:
		return e.Prompts
	case domain.CategoryMCP:
		return e.MCPServers
	}
	return nil
}

// Counts returns content counts for display formatting.
func (e PackShowEntry) Counts() ContentCounts {
	return ContentCounts{
		Rules:     len(e.Rules),
		Skills:    len(e.Skills),
		Workflows: len(e.Workflows),
		Agents:    len(e.Agents),
		Prompts:   len(e.Prompts),
		MCP:       len(e.MCPServers),
	}
}

// PackList returns all installed packs. It delegates to PackListDetailed,
// which populates content inventory fields (Rules, Agents, etc.) as well.
func PackList(configDir string) ([]PackShowEntry, error) {
	return PackListDetailed(configDir)
}

// PackShow returns detailed information about an installed pack.
func PackShow(configDir string, name string) (PackShowEntry, error) {
	if strings.TrimSpace(name) == "" {
		return PackShowEntry{}, fmt.Errorf("pack name is required")
	}
	packsDir := PacksDir(configDir)

	lf, err := config.EnsureLockfileMigrated(configDir)
	if err != nil {
		return PackShowEntry{}, fmt.Errorf("loading lockfile: %w", err)
	}

	return packShowCore(packsDir, name, lf.Packs)
}

// PackListDetailed returns detailed information for all installed packs,
// loading the lockfile once instead of per-pack.
func PackListDetailed(configDir string) ([]PackShowEntry, error) {
	// Run migration first — this must happen whether or not packs/ exists,
	// so that legacy installed_packs from sync-config are preserved after an
	// upgrade even before any packs have been installed on the new layout.
	lf, err := config.EnsureLockfileMigrated(configDir)
	if err != nil {
		return nil, fmt.Errorf("loading lockfile: %w", err)
	}

	packsDir := PacksDir(configDir)
	entries, err := os.ReadDir(packsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []PackShowEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		entry, err := packShowCore(packsDir, name, lf.Packs)
		if err != nil {
			continue
		}
		result = append(result, entry)
	}
	return result, nil
}

// packShowCore builds a PackShowEntry for a single pack using pre-loaded metadata.
func packShowCore(packsDir, name string, meta map[string]config.InstalledPackMeta) (PackShowEntry, error) {
	packDir := filepath.Join(packsDir, name)

	info, err := os.Lstat(packDir)
	if err != nil {
		if os.IsNotExist(err) {
			return PackShowEntry{}, fmt.Errorf("pack %q is not installed", name)
		}
		return PackShowEntry{}, fmt.Errorf("stat pack %q: %w", name, err)
	}

	entry := PackShowEntry{Name: name}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(packDir)
		if err == nil {
			entry.Path = target
		} else {
			entry.Path = packDir
		}
	} else {
		entry.Path = packDir
	}

	manifestPath := filepath.Join(packDir, "pack.json")
	if m, err := config.LoadPackManifest(manifestPath); err == nil {
		packRoot := config.ResolvePackRoot(manifestPath, m.Root)
		if packRoot != "" {
			_ = config.DiscoverContent(&m, packRoot)
		}
		entry.Version = m.Version
		entry.Rules = m.Rules
		entry.Agents = m.Agents
		entry.Workflows = m.Workflows
		entry.Skills = m.Skills
		entry.Prompts = m.Prompts
		servers := make([]string, 0, len(m.MCP.Servers))
		for k := range m.MCP.Servers {
			servers = append(servers, k)
		}
		entry.MCPServers = servers
		entry.Extras = m.Extras
	}

	if m, ok := meta[name]; ok {
		entry.Origin = m.Origin
		entry.Method = m.Method
		entry.Ref = m.Ref
		entry.CommitHash = m.CommitHash
		entry.InstalledAt = m.InstalledAt
		if isPinned(m) {
			entry.Pin = m.Ref
		}
	}

	if entry.Method == "" {
		entry.Method = inferInstallMethod(info.Mode())
	}

	if entry.Rules == nil {
		entry.Rules = []string{}
	}
	if entry.Agents == nil {
		entry.Agents = []string{}
	}
	if entry.Workflows == nil {
		entry.Workflows = []string{}
	}
	if entry.Skills == nil {
		entry.Skills = []string{}
	}
	if entry.Prompts == nil {
		entry.Prompts = []string{}
	}
	if entry.MCPServers == nil {
		entry.MCPServers = []string{}
	}

	return entry, nil
}
