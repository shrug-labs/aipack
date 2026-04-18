package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// BuildInventories reads each selected pack from disk and produces a
// profile-independent inventory snapshot of its full authored contents.
// The result reflects what the pack ships, not the selector-filtered subset
// that happened to resolve into the active profile.
func BuildInventories(e *Engine, configDir string, profile domain.Profile, now time.Time) (map[string]domain.PackInventory, error) {
	if e == nil {
		e = New(nil, nil)
	}

	out := make(map[string]domain.PackInventory, len(profile.Packs))
	for _, pk := range profile.Packs {
		inv, err := e.buildPackInventory(configDir, pk, now)
		if err != nil {
			return nil, err
		}
		out[pk.Name] = inv
	}
	return out, nil
}

// BuildPackInventory reads a single pack from disk and produces a
// profile-independent inventory snapshot. Use at install/update/rename
// time when a full BuildInventories call (which takes a profile) would
// be overkill — drift detection and doctor's broken_refs check both need
// a baseline inventory in the lockfile before the first sync runs.
func BuildPackInventory(configDir string, pk domain.Pack, now time.Time) (domain.PackInventory, error) {
	return New(nil, nil).buildPackInventory(configDir, pk, now)
}

func (e *Engine) buildPackInventory(configDir string, pk domain.Pack, now time.Time) (domain.PackInventory, error) {
	manifestPath, err := inventoryManifestPath(configDir, pk)
	if err != nil {
		return domain.PackInventory{}, err
	}
	manifest, err := config.LoadPackManifest(manifestPath)
	if err != nil {
		return domain.PackInventory{}, fmt.Errorf("loading manifest for pack %q: %w", pk.Name, err)
	}
	packRoot := config.ResolvePackRoot(manifestPath, manifest.Root)
	if packRoot == "" {
		return domain.PackInventory{}, fmt.Errorf("pack %q root could not be resolved", pk.Name)
	}
	if err := config.DiscoverContent(&manifest, packRoot); err != nil {
		return domain.PackInventory{}, fmt.Errorf("discovering content for pack %q: %w", pk.Name, err)
	}

	inv := domain.PackInventory{
		InventoriedAt: now,
		Rules:         domain.SortedCopy(manifest.Rules),
		Agents:        domain.SortedCopy(manifest.Agents),
		Workflows:     domain.SortedCopy(manifest.Workflows),
	}

	if len(manifest.Skills) > 0 {
		skills, _, err := e.parseSkills(packRoot, manifest.Skills, pk.Name)
		if err != nil {
			return domain.PackInventory{}, fmt.Errorf("parsing skills for pack %q: %w", pk.Name, err)
		}
		inv.Skills = make(map[string]domain.SkillSnapshot, len(skills))
		for _, skill := range skills {
			inv.Skills[skill.Name] = domain.SkillSnapshot{
				Description: skill.Frontmatter.Description,
				Assets:      domain.SortedCopy(skill.Assets),
			}
		}
	}

	if len(manifest.MCP) > 0 {
		rawServers, err := e.loadMCPInventoryDir(filepath.Join(packRoot, "mcp"))
		if err != nil {
			return domain.PackInventory{}, fmt.Errorf("loading mcp inventory for pack %q: %w", pk.Name, err)
		}
		inv.MCPServers = make(map[string]domain.MCPServerSnapshot, len(manifest.MCP))
		for _, name := range manifest.MCP {
			server, ok := rawServers[NormalizeServerName(name)]
			if !ok {
				return domain.PackInventory{}, fmt.Errorf("pack %q mcp server %q missing from inventory", pk.Name, name)
			}
			inv.MCPServers[name] = domain.MCPServerSnapshot{
				AvailableTools: domain.SortedCopy(server.AvailableTools),
				RequiredRefs:   extractRequiredRefs(server),
			}
		}
	}

	return inv, nil
}

func inventoryManifestPath(configDir string, pk domain.Pack) (string, error) {
	candidates := []string{}
	if configDir != "" && pk.Name != "" {
		candidates = append(candidates, filepath.Join(configDir, "packs", pk.Name, "pack.json"))
	}
	if pk.Root != "" {
		candidates = append(candidates, filepath.Join(pk.Root, "pack.json"))
		candidates = append(candidates, filepath.Join(filepath.Dir(pk.Root), "pack.json"))
	}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("pack %q has no manifest path candidates", pk.Name)
	}
	return "", fmt.Errorf("pack %q manifest not found", pk.Name)
}
