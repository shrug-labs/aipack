package app

import (
	"fmt"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// ProfilePackInfo holds resolved pack metadata for display and tree building.
type ProfilePackInfo struct {
	Index    int // position in profile's pack list
	Name     string
	Root     string
	Manifest config.PackManifest
}

// HasConfigs reports whether this pack provides any harness config files.
func (p ProfilePackInfo) HasConfigs() bool {
	return p.Manifest.Configs.HasAnyConfigs()
}

// ContentItem represents a single content item (rule, agent, etc.) with its
// resolved selection state across packs.
type ContentItem struct {
	ID       string
	Category domain.PackCategory
	PackIdx  int // index into the ProfilePackInfo slice
	PackName string
	Enabled  bool
}

// ContentTree holds the resolved content inventory for a profile.
// The TUI uses this as a pure data source for rendering.
type ContentTree struct {
	Packs []ProfilePackInfo
	Items []ContentItem // flat list, grouped by category
}

// ResolveProfilePacks loads and resolves manifests for each pack entry.
// Returns resolved pack info and any errors encountered.
func ResolveProfilePacks(configDir string, packs []config.PackEntry) ([]ProfilePackInfo, []string) {
	var resolved []ProfilePackInfo
	var errs []string
	for i, pe := range packs {
		manifestPath := filepath.Join(configDir, "packs", pe.Name, "pack.json")
		manifest, err := config.LoadPackManifest(manifestPath)
		if err != nil {
			errs = append(errs, fmt.Sprintf("pack %q: %v", pe.Name, err))
			continue
		}
		packRoot := config.ResolvePackRoot(manifestPath, manifest.Root)
		if packRoot == "" {
			packRoot = filepath.Join(configDir, "packs", pe.Name)
		}
		resolved = append(resolved, ProfilePackInfo{
			Index:    i,
			Name:     pe.Name,
			Root:     packRoot,
			Manifest: manifest,
		})
	}
	return resolved, errs
}

// BuildContentTree resolves pack manifests against profile entries to produce
// a flat list of content items with their enabled/disabled state.
// entries must be the original unfiltered PackEntry slice passed to
// ResolveProfilePacks — each ProfilePackInfo.Index indexes into it.
func BuildContentTree(packs []ProfilePackInfo, entries []config.PackEntry) ContentTree {
	categoryItems := map[domain.PackCategory][]ContentItem{}
	categoryOrder := domain.AllPackCategories()

	for pi, p := range packs {
		pe := entries[p.Index]

		// Authored categories use VectorSelector for selection state.
		for _, cat := range domain.AuthoredCategories() {
			inventory := p.Manifest.ContentIDs(cat)
			if len(inventory) == 0 {
				continue
			}
			sel := pe.VectorSelectorFor(cat)
			if sel == nil {
				continue
			}
			selected := config.ToStringSet(config.ResolveCurrentVector(inventory, *sel))
			for _, id := range inventory {
				categoryItems[cat] = append(categoryItems[cat], ContentItem{
					ID:       id,
					Category: cat,
					PackIdx:  pi,
					PackName: p.Name,
					Enabled:  selected[id],
				})
			}
		}

		// MCP servers use a different config structure.
		for _, name := range p.Manifest.ContentIDs(domain.CategoryMCP) {
			enabled := true
			if mcpCfg, ok := pe.MCP[name]; ok {
				if mcpCfg.Enabled != nil && !*mcpCfg.Enabled {
					enabled = false
				}
			}
			categoryItems[domain.CategoryMCP] = append(categoryItems[domain.CategoryMCP], ContentItem{
				ID:       name,
				Category: domain.CategoryMCP,
				PackIdx:  pi,
				PackName: p.Name,
				Enabled:  enabled,
			})
		}
	}

	// Flatten in category order.
	var items []ContentItem
	for _, cat := range categoryOrder {
		items = append(items, categoryItems[cat]...)
	}

	return ContentTree{Packs: packs, Items: items}
}

// ApplyContentTree writes toggled content selections back to profile entries.
func ApplyContentTree(tree ContentTree, entries []config.PackEntry) {
	for pi, p := range tree.Packs {
		pe := &entries[p.Index]

		// Collect enabled items for this pack.
		cats := map[domain.PackCategory][]string{}
		for _, item := range tree.Items {
			if item.PackIdx == pi && item.Enabled {
				cats[item.Category] = append(cats[item.Category], item.ID)
			}
		}

		// Update vector selectors for authored categories.
		for _, cat := range domain.AuthoredCategories() {
			ids := p.Manifest.ContentIDs(cat)
			if len(ids) > 0 {
				*pe.VectorSelectorFor(cat) = config.SelectionsToVector(ids, cats[cat])
			}
		}

		// MCP servers — preserve existing tool allowlists from the profile.
		if len(p.Manifest.MCP.Servers) > 0 {
			enabledServers := cats[domain.CategoryMCP]
			existingTools := map[string][]string{}
			for name, cfg := range pe.MCP {
				if len(cfg.AllowedTools) > 0 {
					existingTools[name] = cfg.AllowedTools
				}
			}
			pe.MCP = config.MCPToConfig(p.Manifest, enabledServers, existingTools)
			if pe.MCP == nil {
				pe.MCP = map[string]config.MCPServerConfig{}
			}
		}
	}
}

// PackContentSizes computes file sizes for all content items in the given packs.
// Returns a map of "packIdx:category/id" → bytes.
func PackContentSizes(packs []ProfilePackInfo) map[string]int64 {
	sizes := map[string]int64{}
	for pi, p := range packs {
		prefix := fmt.Sprintf("%d:", pi)
		for _, cat := range domain.AllPackCategories() {
			for _, id := range p.Manifest.ContentIDs(cat) {
				sizes[prefix+cat.DirName()+"/"+id] = PackContentSize(p.Root, cat, id)
			}
		}
	}
	return sizes
}

// PackContentSize returns the on-disk size of a content item.
func PackContentSize(root string, category domain.PackCategory, id string) int64 {
	fp := filepath.Join(root, filepath.FromSlash(category.PrimaryRelPath(id)))
	kind := domain.CopyKindFile
	if category == domain.CategorySkills {
		fp = filepath.Dir(fp)
		kind = domain.CopyKindDir
	}
	size, err := fileOrDirSize(fp, kind)
	if err != nil {
		return -1
	}
	return size
}
