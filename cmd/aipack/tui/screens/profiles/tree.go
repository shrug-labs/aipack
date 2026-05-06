package profiles

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// treeModel represents an expandable content tree for a profile's pack content.
// It may contain items from multiple packs.
type treeModel struct {
	nodes  []treeNode
	cursor int
	offset int // scroll offset (visible line index)
	packs  []app.ProfilePackInfo
	// mcpCounts carries per-server display counts and the profile-wide total.
	// Populated by ensureTree via computeMCPCounts and applied to each MCP
	// node via applyMCPCounts. Total drives the 128-limit warning on the
	// MCP category header; per-row counts show in the rightmost column.
	mcpCounts profileMCPCounts
}

// applyMCPCounts writes the computed per-server counts into each MCP node's
// mcpToolsEnabled / mcpToolsAvailable fields. Called after buildTreeFromContent
// and after any in-place mutation that changes effective tool counts (picker
// save, bulk action). Nodes outside the MCP category are untouched.
func (t *treeModel) applyMCPCounts() {
	for i := range t.nodes {
		n := &t.nodes[i]
		if n.kind != nodeItem || n.category != domain.CategoryMCP {
			continue
		}
		key := mcpCountsKey{PackIdx: n.packIdx, Server: n.id}
		if c, ok := t.mcpCounts.ByServer[key]; ok {
			n.mcpToolsEnabled = c.Enabled
			n.mcpToolsAvailable = c.Available
		}
	}
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
	formattedSize string              // cached formatted fileSize, empty if not computed

	// MCP tool counts — populated for nodeItem entries in the MCP category.
	// mcpToolsAvailable == -1 means "not an MCP item / not computed". For MCP
	// items, Available is the inventory size and Enabled is the effective
	// count after resolver fallback and disabled_tools subtraction. Shown in
	// the per-row tool count column and summed on the MCP category header.
	mcpToolsEnabled   int
	mcpToolsAvailable int

	// Conflict state — set when multiple packs provide the same (category, id).
	conflict          bool // true if this item conflicts with another pack
	isOverride        bool // true if this item's pack declares an override for it
	isOverridden      bool // true if another pack's override supersedes this item
	competingOverride bool // true if multiple packs declare overrides for this id
}

// ContentRef interface — allows DetectConflicts to work on treeNode slices.
func (n treeNode) ContentCategory() domain.PackCategory { return n.category }
func (n treeNode) ContentID() string                    { return n.id }
func (n treeNode) ContentPackIdx() int                  { return n.packIdx }
func (n treeNode) ContentEnabled() bool                 { return n.enabled }

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
				kind:              nodeItem,
				label:             it.ID,
				enabled:           it.Enabled,
				category:          cat,
				id:                it.ID,
				packIdx:           it.PackIdx,
				parentIdx:         catIdx,
				fileSize:          -1,
				mcpToolsAvailable: -1,
				conflict:          len(it.ConflictPacks) > 0,
				isOverride:        it.IsOverride,
				isOverridden:      it.IsOverridden,
				competingOverride: it.CompetingOverride,
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

// refreshConflicts recalculates conflict/override markers in-place from
// current enabled states and the given pack entries. This avoids a full tree
// rebuild (which re-reads manifests from disk) when toggling items.
func (t *treeModel) refreshConflicts(entries []config.PackEntry) {
	// Build a slice of only item nodes for DetectConflicts.
	var itemIndices []int
	var refs []treeNode
	for i := range t.nodes {
		if t.nodes[i].kind == nodeItem {
			itemIndices = append(itemIndices, i)
			refs = append(refs, t.nodes[i])
		}
	}

	conflicts := app.DetectConflicts(refs, t.packs, entries)

	// Apply computed state in a single pass.
	for ci, ni := range itemIndices {
		c := conflicts[ci]
		n := &t.nodes[ni]
		n.conflict = len(c.ConflictPacks) > 0
		n.isOverride = c.IsOverride
		n.isOverridden = c.IsOverridden
		n.competingOverride = c.CompetingOverride
	}
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
	return t.packs[n.packIdx].ContentPath(n.category, n.id)
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
	for i := range len(t.nodes) {
		if i > t.cursor {
			break
		}
		if t.isVisible(i) {
			if i == t.cursor {
				break
			}
			visIdx++
		}
	}
	t.offset = common.ClampOffset(visIdx, t.offset, visibleH)
}

// view renders the tree, windowed to height visible lines.
// Lines wider than width are truncated to prevent wrapping.
func (t *treeModel) view(focused bool, width, height int) string {
	if len(t.nodes) == 0 {
		return common.DimStyle.Render("  (no content)")
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
			n.formattedSize = common.FormatSize(n.fileSize)
		}
		if n.category == domain.CategoryMCP && n.mcpToolsAvailable >= 0 {
			// For MCP items, replace the meaningless JSON-file byte size
			// with the tool count in the right-aligned column. Set
			// fileSize = 0 so the "hasSize" renderer picks up formattedSize;
			// DON'T add to sizeTotal — the category header uses the tool
			// count label instead.
			n.fileSize = 0
			n.formattedSize = fmt.Sprintf("%d/%d", n.mcpToolsEnabled, n.mcpToolsAvailable)
		}
		if !t.isVisible(i) {
			continue
		}
		if len(n.label) > maxLabelW {
			maxLabelW = len(n.label)
		}
		if n.packIdx >= 0 && n.packIdx < len(t.packs) {
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

	// When the full layout overflows the view, drop size first, then shorten
	// pack attribution before dropping it entirely. This preserves provenance
	// at normal terminal widths without letting every row wrap or trail off.
	const minPackAttributionW = 6
	alignCol := prefixW + maxLabelW + 2
	showPack := multiPack && maxPackW > 0
	packColW := maxPackW
	showSize := maxSizeW > 0

	widthFor := func(packW int, size bool) int {
		w := prefixW + maxLabelW
		if packW > 0 {
			w += 2 + packW
		}
		if size {
			w += 2 + maxSizeW
		}
		return w
	}
	if width > 0 {
		if showSize && widthFor(packColW, true) > width {
			showSize = false
		}
		if showPack {
			availablePackW := width - alignCol
			if showSize {
				availablePackW -= 2 + maxSizeW
			}
			packColW = min(packColW, availablePackW)
			if packColW < minPackAttributionW {
				showPack = false
				packColW = 0
			}
		}
	}
	rightEdge := alignCol
	if showPack {
		rightEdge += packColW
	}
	if showSize {
		rightEdge += 2 + maxSizeW
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
			if n.category == domain.CategoryMCP {
				catSuffix = common.DimStyle.Render(fmt.Sprintf("%d/%d tools", t.mcpCounts.TotalEnabled, t.mcpCounts.TotalAvailable))
			} else if showSize && cs != nil && cs.hasSize {
				catSuffix = common.DimStyle.Render(common.FormatSize(cs.sizeTotal))
			}

			if catSuffix != "" {
				catLeftW := lipgloss.Width(catLeft)
				catSuffixW := lipgloss.Width(catSuffix)
				gap := max(rightEdge-catLeftW-catSuffixW, 2)
				catLine := catLeft + strings.Repeat(" ", gap) + catSuffix
				if focused && i == t.cursor {
					catLine = common.SelectedStyle.Render(catLeft) + strings.Repeat(" ", gap) + catSuffix
				}
				lines = append(lines, catLine)
			} else {
				if focused && i == t.cursor {
					catLeft = common.SelectedStyle.Render(catLeft)
				}
				lines = append(lines, catLeft)
			}

		case nodeItem:
			check := treeCheckOff
			if n.enabled {
				check = treeCheckOn
			}
			overridden := n.isOverridden

			label := n.label
			if focused && i == t.cursor {
				if overridden {
					label = common.SelectedStyle.Strikethrough(true).Render(label)
				} else {
					label = common.SelectedStyle.Render(label)
				}
			} else if overridden {
				label = common.DimStyle.Strikethrough(true).Render(label)
			}

			// Conflict/override marker — shown inline after the label.
			marker := ""
			if n.conflict {
				if n.isOverride {
					if n.competingOverride {
						marker = " " + common.WarningStyle.Render("⬡") // wins but another pack also claims override
					} else {
						marker = " " + treeOverrideWinner
					}
				} else if overridden {
					marker = " " + common.DimStyle.Render("⬡")
				} else {
					marker = " " + common.WarningStyle.Render("⚠")
				}
			}

			left := fmt.Sprintf("%s  %s %s", cursor, check, label) + marker

			hasPack := showPack && n.packIdx >= 0 && n.packIdx < len(t.packs)
			hasSize := showSize && n.fileSize >= 0

			if !hasPack && !hasSize {
				lines = append(lines, left)
				continue
			}

			var line strings.Builder
			leftW := lipgloss.Width(left)
			gap := max(alignCol-leftW, 2)
			line.WriteString(left + strings.Repeat(" ", gap))

			if hasPack {
				packName := common.TruncateText(t.packs[n.packIdx].Name, packColW)
				line.WriteString(packColorBright(t.packs[n.packIdx].Index).Render(packName))
				if pad := packColW - lipgloss.Width(packName); pad > 0 {
					line.WriteString(strings.Repeat(" ", pad))
				}
			} else if showPack {
				line.WriteString(strings.Repeat(" ", packColW))
			}

			if hasSize {
				sizeStr := n.formattedSize
				pad := maxSizeW - len(sizeStr)
				line.WriteString("  ")
				if pad > 0 {
					line.WriteString(strings.Repeat(" ", pad))
				}
				line.WriteString(common.DimStyle.Render(sizeStr))
			}

			lines = append(lines, line.String())
		}
	}

	if width > 0 {
		lines = common.TruncateLines(lines, width)
	}

	var sb strings.Builder
	common.WriteScrollWindow(&sb, lines, t.offset, max(height-1, 1), func(int) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("  ...")
	})

	return sb.String()
}
