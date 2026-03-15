package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
)

// treeModel represents an expandable content tree for a profile's pack content.
// It may contain items from multiple packs.
type treeModel struct {
	nodes  []treeNode
	cursor int
	offset int // scroll offset (visible line index)
	packs  []app.ProfilePackInfo
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
	category      domain.PackCategory // one of domain.Category* constants
	id            string              // item id within category (only for nodeItem)
	packIdx       int                 // index into treeModel.packs (only for nodeItem)
	parentIdx     int                 // index of parent category node (-1 for categories)
	fileSize      int64               // bytes, -1 = not computed
	formattedSize string              // cached formatSize(fileSize), empty if not computed
}

// buildTreeFromContent converts an app.ContentTree into a treeModel for rendering.
func buildTreeFromContent(ct app.ContentTree) treeModel {
	categoryOrder := domain.AllPackCategories()

	// Group items by category.
	categoryItems := map[domain.PackCategory][]app.ContentItem{}
	for _, item := range ct.Items {
		categoryItems[item.Category] = append(categoryItems[item.Category], item)
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
			label:     cat.Label(),
			expanded:  true,
			category:  cat,
			parentIdx: -1,
		})
		for _, it := range items {
			nodes = append(nodes, treeNode{
				kind:      nodeItem,
				label:     it.ID,
				enabled:   it.Enabled,
				category:  cat,
				id:        it.ID,
				packIdx:   it.PackIdx,
				parentIdx: catIdx,
				fileSize:  -1,
			})
		}
	}

	return treeModel{
		nodes: nodes,
		packs: ct.Packs,
	}
}

// toContentTree reconstructs an app.ContentTree from the current node enabled states.
func (t *treeModel) toContentTree() app.ContentTree {
	var items []app.ContentItem
	for _, n := range t.nodes {
		if n.kind != nodeItem {
			continue
		}
		items = append(items, app.ContentItem{
			ID:       n.id,
			Category: n.category,
			PackIdx:  n.packIdx,
			PackName: t.packName(n.packIdx),
			Enabled:  n.enabled,
		})
	}
	return app.ContentTree{Packs: t.packs, Items: items}
}

func (t *treeModel) packName(idx int) string {
	if idx >= 0 && idx < len(t.packs) {
		return t.packs[idx].Name
	}
	return ""
}

// cursorNode returns the treeNode at the current cursor, or nil.
func (t *treeModel) cursorNode() *treeNode {
	if len(t.nodes) == 0 || t.cursor < 0 || t.cursor >= len(t.nodes) {
		return nil
	}
	return &t.nodes[t.cursor]
}

// contentPath resolves the absolute file path for a content item given its
// pack root, category, and id.
func contentPath(root string, category domain.PackCategory, id string) string {
	return filepath.Join(root, filepath.FromSlash(category.PrimaryRelPath(id)))
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
	return contentPath(t.packs[n.packIdx].Root, n.category, n.id)
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
		key := fmt.Sprintf("%d:%s/%s", n.packIdx, n.category.DirName(), n.id)
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
	stats := map[domain.PackCategory]*catStats{}
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
			if w := len(t.packs[n.packIdx].Name); w > maxPackW {
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
				name := t.packs[n.packIdx].Name
				pad := maxPackW - len(name)
				if pad > 0 {
					line.WriteString(strings.Repeat(" ", pad))
				}
				line.WriteString(packColorMuted(t.packs[n.packIdx].Index).Render(name))
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
	writeScrollWindow(&sb, lines, t.offset, max(height-1, 1))

	return sb.String()
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
