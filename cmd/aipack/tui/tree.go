package tui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/shrug-labs/aipack/internal/config"
)

// Category constants for content tree nodes.
const (
	CatRules     = "rules"
	CatAgents    = "agents"
	CatWorkflows = "workflows"
	CatSkills    = "skills"
	CatMCP       = "mcp"
)

// packInfo holds resolved metadata for a single pack within a profile.
type packInfo struct {
	idx      int // index into profile's packs slice
	name     string
	root     string
	manifest config.PackManifest
}

// treeModel represents an expandable content tree for a profile's pack content.
// It may contain items from multiple packs.
type treeModel struct {
	nodes  []treeNode
	cursor int
	offset int // scroll offset (visible line index)
	packs  []packInfo
}

type treeNodeKind int

const (
	nodeCategory treeNodeKind = iota
	nodeItem
)

type treeNode struct {
	kind          treeNodeKind
	label         string
	enabled       bool
	expanded      bool
	category      string // one of Cat* constants
	id            string // item id within category (only for nodeItem)
	packIdx       int    // index into treeModel.packs (only for nodeItem)
	parentIdx     int    // index of parent category node (-1 for categories)
	fileSize      int64  // bytes, -1 = not computed
	formattedSize string // cached formatSize(fileSize), empty if not computed
}

// buildMultiPackTree constructs a tree from all resolved packs in a profile.
func buildMultiPackTree(packs []packInfo, entries []config.PackEntry) treeModel {
	type item struct {
		id       string
		enabled  bool
		packIdx  int
		packName string
	}

	// Collect items per category across all packs.
	categoryItems := map[string][]item{}
	categoryOrder := []string{CatRules, CatAgents, CatWorkflows, CatSkills, CatMCP}

	for pi, p := range packs {
		pe := entries[p.idx]

		addItems := func(category string, inventory []string, sel config.VectorSelector) {
			if len(inventory) == 0 {
				return
			}
			selected := config.ToStringSet(config.ResolveCurrentVector(inventory, sel))
			for _, id := range inventory {
				categoryItems[category] = append(categoryItems[category], item{
					id:       id,
					enabled:  selected[id],
					packIdx:  pi,
					packName: p.name,
				})
			}
		}

		addItems(CatRules, p.manifest.Rules, pe.Rules)
		addItems(CatAgents, p.manifest.Agents, pe.Agents)
		addItems(CatWorkflows, p.manifest.Workflows, pe.Workflows)
		addItems(CatSkills, p.manifest.Skills, pe.Skills)

		// MCP servers.
		if len(p.manifest.MCP.Servers) > 0 {
			serverNames := make([]string, 0, len(p.manifest.MCP.Servers))
			for name := range p.manifest.MCP.Servers {
				serverNames = append(serverNames, name)
			}
			sort.Strings(serverNames)

			for _, name := range serverNames {
				enabled := true
				if mcpCfg, ok := pe.MCP[name]; ok {
					if mcpCfg.Enabled != nil && !*mcpCfg.Enabled {
						enabled = false
					}
				}
				categoryItems[CatMCP] = append(categoryItems[CatMCP], item{
					id:       name,
					enabled:  enabled,
					packIdx:  pi,
					packName: p.name,
				})
			}
		}
	}

	// Build flat node list.
	var nodes []treeNode
	for _, cat := range categoryOrder {
		items := categoryItems[cat]
		if len(items) == 0 {
			continue
		}
		catIdx := len(nodes)
		nodes = append(nodes, treeNode{
			kind:      nodeCategory,
			label:     categoryLabel(cat),
			expanded:  true,
			category:  cat,
			parentIdx: -1,
		})
		for _, it := range items {
			nodes = append(nodes, treeNode{
				kind:      nodeItem,
				label:     it.id,
				enabled:   it.enabled,
				category:  cat,
				id:        it.id,
				packIdx:   it.packIdx,
				parentIdx: catIdx,
				fileSize:  -1,
			})
		}
	}

	return treeModel{
		nodes: nodes,
		packs: packs,
	}
}

// cursorNode returns the treeNode at the current cursor, or nil.
func (t *treeModel) cursorNode() *treeNode {
	if len(t.nodes) == 0 || t.cursor < 0 || t.cursor >= len(t.nodes) {
		return nil
	}
	return &t.nodes[t.cursor]
}

// contentPath resolves the absolute file path for a content item given its
// pack root, category, and id. Returns "" for unknown categories (e.g. MCP).
func contentPath(root, category, id string) string {
	switch category {
	case CatRules:
		return filepath.Join(root, "rules", id+".md")
	case CatAgents:
		return filepath.Join(root, "agents", id+".md")
	case CatWorkflows:
		return filepath.Join(root, "workflows", id+".md")
	case CatSkills:
		return filepath.Join(root, "skills", id, "SKILL.md")
	case CatMCP:
		return filepath.Join(root, "mcp", id+".json")
	}
	return ""
}

// filePath returns the absolute file path for the item at the cursor.
// Returns "" for category nodes.
func (t *treeModel) filePath() string {
	n := t.cursorNode()
	if n == nil || n.kind != nodeItem {
		return ""
	}
	if n.packIdx < 0 || n.packIdx >= len(t.packs) {
		return ""
	}
	return contentPath(t.packs[n.packIdx].root, n.category, n.id)
}

// toggle flips the enabled state of the item at the cursor.
// Returns true if state changed.
func (t *treeModel) toggle() bool {
	if len(t.nodes) == 0 || t.cursor < 0 || t.cursor >= len(t.nodes) {
		return false
	}
	n := &t.nodes[t.cursor]
	if n.kind == nodeCategory {
		n.expanded = !n.expanded
		return false // not a content change
	}
	n.enabled = !n.enabled
	return true
}

// moveUp moves the cursor up, skipping hidden nodes.
func (t *treeModel) moveUp() {
	for t.cursor > 0 {
		t.cursor--
		if t.isVisible(t.cursor) {
			return
		}
	}
}

// moveDown moves the cursor down, skipping hidden nodes.
func (t *treeModel) moveDown() {
	for t.cursor < len(t.nodes)-1 {
		t.cursor++
		if t.isVisible(t.cursor) {
			return
		}
	}
}

// isVisible returns true if the node at idx should be shown.
func (t *treeModel) isVisible(idx int) bool {
	n := t.nodes[idx]
	if n.kind == nodeCategory {
		return true
	}
	if n.parentIdx >= 0 && n.parentIdx < len(t.nodes) {
		return t.nodes[n.parentIdx].expanded
	}
	return true
}

// applyToProfile writes the current tree state back to all affected pack entries.
func (t *treeModel) applyToProfile(packs []config.PackEntry) {
	for pi, p := range t.packs {
		pe := &packs[p.idx]

		// Collect enabled items for this pack.
		cats := map[string][]string{}
		for _, n := range t.nodes {
			if n.kind == nodeItem && n.packIdx == pi && n.enabled {
				cats[n.category] = append(cats[n.category], n.id)
			}
		}

		// Update vector selectors.
		if len(p.manifest.Rules) > 0 {
			pe.Rules = config.SelectionsToVector(p.manifest.Rules, cats[CatRules])
		}
		if len(p.manifest.Agents) > 0 {
			pe.Agents = config.SelectionsToVector(p.manifest.Agents, cats[CatAgents])
		}
		if len(p.manifest.Workflows) > 0 {
			pe.Workflows = config.SelectionsToVector(p.manifest.Workflows, cats[CatWorkflows])
		}
		if len(p.manifest.Skills) > 0 {
			pe.Skills = config.SelectionsToVector(p.manifest.Skills, cats[CatSkills])
		}

		// MCP servers.
		if len(p.manifest.MCP.Servers) > 0 {
			enabledServers := cats[CatMCP]
			pe.MCP = config.MCPToConfig(p.manifest, enabledServers, map[string][]string{})
			if pe.MCP == nil {
				pe.MCP = map[string]config.MCPServerConfig{}
			}
		}
	}
}

// hasSizes returns true if file sizes have been computed for this tree.
func (t *treeModel) hasSizes() bool {
	for _, n := range t.nodes {
		if n.kind == nodeItem && n.fileSize >= 0 {
			return true
		}
	}
	return false
}

// updateFileSizes applies a sizes map (packIdx:category/id -> bytes) to tree nodes.
func (t *treeModel) updateFileSizes(sizes map[string]int64) {
	for i := range t.nodes {
		n := &t.nodes[i]
		if n.kind != nodeItem {
			continue
		}
		key := fmt.Sprintf("%d:%s/%s", n.packIdx, n.category, n.id)
		if sz, ok := sizes[key]; ok {
			n.fileSize = sz
		}
	}
}

// clampOffset ensures the cursor's visible line is within the scroll window.
func (t *treeModel) clampOffset(visibleH int) {
	// Map cursor to its visible-line index.
	visIdx := 0
	for i := 0; i < len(t.nodes) && i <= t.cursor; i++ {
		if t.isVisible(i) {
			if i == t.cursor {
				break
			}
			visIdx++
		}
	}
	t.offset = clampOffset(visIdx, t.offset, visibleH)
}

// view renders the tree, windowed to height visible lines.
func (t *treeModel) view(focused bool, height int) string {
	if len(t.nodes) == 0 {
		return dimStyle.Render("  (no content)")
	}

	multiPack := len(t.packs) > 1

	// First pass: find longest label, pack name, and size for column alignment.
	// Also pre-compute category stats and formatted sizes.
	const prefixW = 8 // "XX  [x] " = 8 printable chars
	maxLabelW := 0
	maxPackW := 0
	maxSizeW := 0
	type catStats struct {
		count, enabled int
		sizeTotal      int64
		hasSize        bool
	}
	stats := map[string]*catStats{}
	for i := range t.nodes {
		n := &t.nodes[i]
		if n.kind != nodeItem {
			continue
		}
		cs := stats[n.category]
		if cs == nil {
			cs = &catStats{}
			stats[n.category] = cs
		}
		cs.count++
		if n.enabled {
			cs.enabled++
		}
		if n.enabled && n.fileSize >= 0 {
			cs.sizeTotal += n.fileSize
			cs.hasSize = true
		}
		if n.fileSize >= 0 {
			n.formattedSize = formatSize(n.fileSize)
		}
		if !t.isVisible(i) {
			continue
		}
		if len(n.label) > maxLabelW {
			maxLabelW = len(n.label)
		}
		if multiPack && n.packIdx >= 0 && n.packIdx < len(t.packs) {
			if w := len(t.packs[n.packIdx].name); w > maxPackW {
				maxPackW = w
			}
		}
		if n.formattedSize != "" {
			if w := len(n.formattedSize); w > maxSizeW {
				maxSizeW = w
			}
		}
	}

	// Build all visible lines, then slice to scroll window.
	var lines []string

	for i, n := range t.nodes {
		if !t.isVisible(i) {
			continue
		}

		cursor := "  "
		if focused && i == t.cursor {
			cursor = "> "
		}

		switch n.kind {
		case nodeCategory:
			arrow := treeCollapsed
			if n.expanded {
				arrow = treeExpanded
			}
			cs := stats[n.category]
			total, enabled := 0, 0
			if cs != nil {
				total, enabled = cs.count, cs.enabled
			}
			catLeft := fmt.Sprintf("%s%s %s (%d/%d)", cursor, arrow, n.label, enabled, total)

			catSuffix := ""
			if cs != nil && cs.hasSize {
				catSuffix = fileSizeStyle.Render(formatSize(cs.sizeTotal))
			}

			if catSuffix != "" {
				alignCol := prefixW + maxLabelW + 2
				rightEdge := alignCol + maxPackW + 2 + maxSizeW
				catLeftW := lipgloss.Width(catLeft)
				catSuffixW := lipgloss.Width(catSuffix)
				gap := max(rightEdge-catLeftW-catSuffixW, 2)
				catLine := catLeft + strings.Repeat(" ", gap) + catSuffix
				if focused && i == t.cursor {
					catLine = selectedStyle.Render(catLeft) + strings.Repeat(" ", gap) + catSuffix
				}
				lines = append(lines, catLine)
			} else {
				if focused && i == t.cursor {
					catLeft = selectedStyle.Render(catLeft)
				}
				lines = append(lines, catLeft)
			}

		case nodeItem:
			check := treeCheckOff
			if n.enabled {
				check = treeCheckOn
			}
			label := n.label
			if focused && i == t.cursor {
				label = selectedStyle.Render(label)
			}

			left := fmt.Sprintf("%s  %s %s", cursor, check, label)

			hasPack := multiPack && n.packIdx >= 0 && n.packIdx < len(t.packs)
			hasSize := n.fileSize >= 0

			if !hasPack && !hasSize {
				lines = append(lines, left)
				continue
			}

			var line strings.Builder
			leftW := lipgloss.Width(left)
			alignCol := prefixW + maxLabelW + 2
			gap := max(alignCol-leftW, 2)
			line.WriteString(left + strings.Repeat(" ", gap))

			if hasPack {
				name := t.packs[n.packIdx].name
				pad := maxPackW - len(name)
				if pad > 0 {
					line.WriteString(strings.Repeat(" ", pad))
				}
				line.WriteString(packColorMuted(t.packs[n.packIdx].idx).Render(name))
			} else if maxPackW > 0 {
				line.WriteString(strings.Repeat(" ", maxPackW))
			}

			if hasSize {
				sizeStr := n.formattedSize
				pad := maxSizeW - len(sizeStr)
				line.WriteString("  ")
				if pad > 0 {
					line.WriteString(strings.Repeat(" ", pad))
				}
				line.WriteString(fileSizeStyle.Render(sizeStr))
			}

			lines = append(lines, line.String())
		}
	}

	var sb strings.Builder
	sb.WriteString("\n")
	writeScrollWindow(&sb, lines, t.offset, max(height-2, 1))

	return sb.String()
}

func categoryLabel(cat string) string {
	switch cat {
	case CatRules:
		return "Rules"
	case CatAgents:
		return "Agents"
	case CatWorkflows:
		return "Workflows"
	case CatSkills:
		return "Skills"
	case CatMCP:
		return "MCP Servers"
	}
	return cat
}

// formatSize formats bytes as a human-readable string.
func formatSize(b int64) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/1024/1024)
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// computeFileSizesForProfile computes file sizes for all packs in a tree.
func computeFileSizesForProfile(packs []packInfo) map[string]int64 {
	sizes := map[string]int64{}
	for pi, p := range packs {
		prefix := fmt.Sprintf("%d:", pi)

		sizeForContent := func(category string, ids []string) {
			for _, id := range ids {
				fp := contentPath(p.root, category, id)
				if fp == "" {
					continue
				}
				// Skills are directories; everything else is a single file.
				if category == CatSkills {
					sizes[prefix+category+"/"+id] = dirSize(fp[:len(fp)-len("/SKILL.md")])
				} else {
					sizes[prefix+category+"/"+id] = fileSize(fp)
				}
			}
		}

		sizeForContent(CatRules, p.manifest.Rules)
		sizeForContent(CatAgents, p.manifest.Agents)
		sizeForContent(CatWorkflows, p.manifest.Workflows)
		sizeForContent(CatSkills, p.manifest.Skills)

		for name := range p.manifest.MCP.Servers {
			sizes[prefix+CatMCP+"/"+name] = fileSize(filepath.Join(p.root, "mcp", name+".json"))
		}
	}
	return sizes
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return info.Size()
}

func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
