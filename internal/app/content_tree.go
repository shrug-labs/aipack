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

// ContentPath returns the absolute on-disk path of a content item, honoring
// any organizational subdirectories the manifest's RelPath knows about.
func (p ProfilePackInfo) ContentPath(category domain.PackCategory, id string) string {
	return filepath.Join(p.Root, filepath.FromSlash(p.Manifest.RelPath(category, id)))
}

// ContentSize returns the on-disk size of a content item, or -1 on error.
func (p ProfilePackInfo) ContentSize(category domain.PackCategory, id string) int64 {
	fp := p.ContentPath(category, id)
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

// ContentItem represents a single content item (rule, agent, etc.) with its
// resolved selection state across packs.
type ContentItem struct {
	ID       string
	Category domain.PackCategory
	PackIdx  int // index into the ProfilePackInfo slice
	PackName string
	Enabled  bool

	// Conflict metadata — populated by BuildContentTree when multiple packs
	// provide the same (category, id) pair.
	ConflictPacks     []string // names of other packs that also provide this id
	IsOverride        bool     // this pack declares an override for this id
	IsOverridden      bool     // another pack declares an override that supersedes this item
	CompetingOverride bool     // multiple packs declare overrides for this id
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
		if err := config.DiscoverContent(&manifest, packRoot); err != nil {
			errs = append(errs, fmt.Sprintf("pack %q content discovery: %v", pe.Name, err))
			continue
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
		// Quiet packs flip the default: without an explicit non-empty
		// include list, nothing is selected. Non-quiet packs fall through
		// to ResolveCurrentVector's "nil/empty include = include all" rule.
		// This mirrors the sync-time resolver at
		// internal/config/profile_resolve.go:422 so the tree's [x]/[ ]
		// state matches what sync would actually deliver.
		for _, cat := range domain.SelectableCategories() {
			inventory := p.Manifest.ContentIDs(cat)
			if len(inventory) == 0 {
				continue
			}
			sel := pe.VectorSelectorFor(cat)
			if sel == nil {
				continue
			}
			var selected map[string]bool
			if pe.Quiet && (sel.Include == nil || len(*sel.Include) == 0) {
				selected = map[string]bool{}
			} else {
				selected = config.ToStringSet(config.ResolveCurrentVector(inventory, *sel))
			}
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

		// MCP servers use a different config structure. Quiet packs
		// default to disabled; only an explicit profile entry with
		// enabled != false activates a server. Non-quiet packs inherit
		// the manifest-declared set.
		for _, name := range p.Manifest.ContentIDs(domain.CategoryMCP) {
			enabled := !pe.Quiet
			if mcpCfg, ok := pe.MCP[name]; ok {
				switch {
				case mcpCfg.Enabled == nil:
					// Profile entry present with enabled unset — quiet
					// packs still opt out; non-quiet packs stay enabled.
				case *mcpCfg.Enabled:
					enabled = true
				default:
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

	// Detect conflicts and mark override state.
	conflicts := DetectConflicts(items, packs, entries)
	for i := range items {
		c := conflicts[i]
		items[i].ConflictPacks = c.ConflictPacks
		items[i].IsOverride = c.IsOverride
		items[i].IsOverridden = c.IsOverridden
		items[i].CompetingOverride = c.CompetingOverride
	}

	return ContentTree{Packs: packs, Items: items}
}

// ConflictState holds the conflict/override metadata for a single content item.
type ConflictState struct {
	ConflictPacks     []string // names of other packs that also provide this id
	IsOverride        bool     // this pack declares an override for this id
	IsOverridden      bool     // another pack declares an override that supersedes this item
	CompetingOverride bool     // multiple packs declare overrides for this id
}

// ContentRef provides the fields needed by DetectConflicts for each item.
// Implemented by both ContentItem (BuildContentTree) and treeNode (TUI refresh).
type ContentRef interface {
	ContentCategory() domain.PackCategory
	ContentID() string
	ContentPackIdx() int
	ContentEnabled() bool
}

func (c ContentItem) ContentCategory() domain.PackCategory { return c.Category }
func (c ContentItem) ContentID() string                    { return c.ID }
func (c ContentItem) ContentPackIdx() int                  { return c.PackIdx }
func (c ContentItem) ContentEnabled() bool                 { return c.Enabled }

// DetectConflicts computes conflict and override state for a set of content items.
// Returns one ConflictState per input item (same indices). Only enabled items
// participate in conflict detection.
func DetectConflicts[T ContentRef](items []T, packs []ProfilePackInfo, entries []config.PackEntry) []ConflictState {
	type contentKey struct {
		category domain.PackCategory
		id       string
	}

	// Group enabled items by key.
	byKey := map[contentKey][]int{}
	for i, item := range items {
		if !item.ContentEnabled() {
			continue
		}
		k := contentKey{item.ContentCategory(), item.ContentID()}
		byKey[k] = append(byKey[k], i)
	}

	// Build override owner map from pack entries. When two packs both
	// declare an override for the same key, track the conflict — the last
	// pack in profile order wins but all participants get flagged.
	overrideOwner := map[contentKey]int{}      // key → winning packIdx
	competingKeys := map[contentKey]struct{}{} // keys with multiple override declarations
	for pi, p := range packs {
		pe := entries[p.Index]
		for _, cat := range domain.AllPackCategories() {
			overrides := pe.OverridesForCategory(cat)
			if overrides == nil {
				continue
			}
			for _, id := range *overrides {
				k := contentKey{cat, id}
				if _, exists := overrideOwner[k]; exists {
					competingKeys[k] = struct{}{}
				}
				overrideOwner[k] = pi
			}
		}
	}

	result := make([]ConflictState, len(items))
	for i, item := range items {
		if !item.ContentEnabled() {
			continue
		}
		k := contentKey{item.ContentCategory(), item.ContentID()}
		indices := byKey[k]
		hasConflict := len(indices) >= 2

		if hasConflict {
			var peers []string
			for _, j := range indices {
				if j != i {
					peers = append(peers, packs[items[j].ContentPackIdx()].Name)
				}
			}
			result[i].ConflictPacks = peers
		}

		if owner, ok := overrideOwner[k]; ok {
			if _, competing := competingKeys[k]; competing {
				result[i].CompetingOverride = true
			}
			if item.ContentPackIdx() == owner {
				result[i].IsOverride = hasConflict
			} else if hasConflict {
				result[i].IsOverridden = true
			}
		}
	}
	return result
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

		// Update vector selectors for profile-selectable categories.
		for _, cat := range domain.SelectableCategories() {
			ids := p.Manifest.ContentIDs(cat)
			if len(ids) > 0 {
				*pe.VectorSelectorFor(cat) = config.SelectionsToVector(ids, cats[cat])
			}
		}

		// MCP servers — preserve existing tool allowlists from the profile.
		if len(p.Manifest.MCP) > 0 {
			enabledServers := cats[domain.CategoryMCP]
			existingTools := map[string][]string{}
			existingAlwaysTools := map[string][]string{}
			existingDisabledTools := map[string][]string{}
			for name, cfg := range pe.MCP {
				if cfg.AllowedTools != nil {
					existingTools[name] = cfg.AllowedTools
				}
				if cfg.AlwaysAllowedTools != nil {
					existingAlwaysTools[name] = cfg.AlwaysAllowedTools
				}
				if cfg.DisabledTools != nil {
					existingDisabledTools[name] = cfg.DisabledTools
				}
			}
			pe.MCP = config.MCPToConfig(p.Manifest.MCP, enabledServers, existingTools, existingAlwaysTools)
			for name, disabled := range existingDisabledTools {
				if cfg, ok := pe.MCP[name]; ok {
					cfg.DisabledTools = append([]string{}, disabled...)
					pe.MCP[name] = cfg
				}
			}
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
				sizes[prefix+cat.DirName()+"/"+id] = p.ContentSize(cat, id)
			}
		}
	}
	return sizes
}
