package search

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
)

func (m Model) View() *lipgloss.Layer {
	root := lipgloss.NewLayer(m.render())
	root.AddLayers(m.clickLayers()...)
	return root
}

func (m Model) render() string {
	var sb strings.Builder

	cursor := ""
	if m.focus == focusInput {
		cursor = "█"
	}
	inputLabel := common.DimStyle.Render("Search: ")
	if m.focus == focusInput {
		inputLabel = common.SelectedStyle.Render("Search: ")
	}
	sb.WriteString(inputLabel + m.query + cursor + "\n\n")

	kindLabel, catLabel, instLabel := m.filterLabels()
	sb.WriteString(common.DimStyle.Render(fmt.Sprintf("Kind: [%s]  Category: [%s]  Show: [%s]", kindLabel, catLabel, instLabel)) + "\n\n")

	if m.loading {
		sb.WriteString(common.DimStyle.Render("searching..."))
		return common.ContentStyle.Render(sb.String())
	}
	if m.errText != "" {
		sb.WriteString(common.ErrorStyle.Render("Error: " + m.errText))
		return common.ContentStyle.Render(sb.String())
	}
	if !m.searched {
		sb.WriteString(common.DimStyle.Render("type a query and press enter to search"))
		return common.ContentStyle.Render(sb.String())
	}
	if len(m.results) == 0 {
		sb.WriteString(common.DimStyle.Render("no results"))
		return common.ContentStyle.Render(sb.String())
	}

	maxKind, maxPack := m.maxKindWidth, m.maxPackWidth

	end := m.visibleEnd()
	for i := m.offset; i < end; i++ {
		r := m.results[i]
		prefix := "  "
		isSelected := m.focus == focusResults && i == m.cursor

		installedMark := common.DimStyle.Render("✗")
		if r.Installed {
			installedMark = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("✓")
		}

		kind := fmt.Sprintf("%-*s", maxKind, r.Kind)
		pack := fmt.Sprintf("%-*s", maxPack, r.Pack)

		if isSelected {
			prefix = "> "
			line := common.SelectedStyle.Render(prefix+kind+"  "+pack+"  "+r.Name) + "  " + installedMark
			sb.WriteString(line + "\n")
		} else {
			fmt.Fprintf(&sb, "%s%s  %s  %s  %s\n",
				prefix,
				m.searchKindStyle(r.Kind).Render(kind),
				common.DimStyle.Render(pack),
				r.Name,
				installedMark)
		}
		if snippet := singleLineSnippet(r.Snippet); snippet != "" {
			if len(snippet) > m.width-6 && m.width > 10 {
				snippet = snippet[:m.width-9] + "..."
			}
			sb.WriteString("    " + common.DimStyle.Render(snippet) + "\n")
		}
	}

	footer := fmt.Sprintf("%d result(s)", len(m.results))
	sb.WriteString(common.DimStyle.Render("\n" + footer))

	return common.ContentStyle.Render(sb.String())
}

func (m Model) clickLayers() []*lipgloss.Layer {
	padX := common.ContentStyle.GetPaddingLeft()
	padY := common.ContentStyle.GetPaddingTop()
	rowW := max(m.width-padX-common.ContentStyle.GetPaddingRight(), len("Search: "+m.query), 1)
	layers := []*lipgloss.Layer{
		lipgloss.NewLayer(strings.Repeat(" ", rowW)).ID("search:input").X(padX).Y(padY).Z(-2),
	}

	kindLabel, catLabel, instLabel := m.filterLabels()
	kindText := fmt.Sprintf("Kind: [%s]", kindLabel)
	categoryText := fmt.Sprintf("Category: [%s]", catLabel)
	showText := fmt.Sprintf("Show: [%s]", instLabel)
	layers = append(layers,
		lipgloss.NewLayer(kindText).ID("search:filter:kind").X(padX).Y(padY+2).Z(-1),
		lipgloss.NewLayer(categoryText).ID("search:filter:category").X(padX+len(kindText)+2).Y(padY+2).Z(-1),
		lipgloss.NewLayer(showText).ID("search:filter:installed").X(padX+len(kindText)+len(categoryText)+4).Y(padY+2).Z(-1),
	)

	if m.loading || m.errText != "" || !m.searched || len(m.results) == 0 {
		return layers
	}

	y := padY + 4
	rowHit := strings.Repeat(" ", rowW)
	end := m.visibleEnd()
	for i := m.offset; i < end; i++ {
		layers = append(layers,
			lipgloss.NewLayer(rowHit).ID(fmt.Sprintf("search:result:%d", i)).X(padX).Y(y).Z(-2),
		)
		y += resultRowHeight(m.results[i])
	}
	return layers
}

func singleLineSnippet(snippet string) string {
	return strings.Join(strings.Fields(snippet), " ")
}

func (m Model) visibleEnd() int {
	budget := max(m.resultAreaHeight(), 1)
	used := 0
	end := m.offset
	for end < len(m.results) {
		h := resultRowHeight(m.results[end])
		if end > m.offset && used+h > budget {
			break
		}
		used += h
		end++
		if used >= budget {
			break
		}
	}
	return end
}

func (m Model) filterLabels() (string, string, string) {
	kindLabel := "all"
	if m.kindFilter != "" {
		kindLabel = m.kindFilter
	}
	catLabel := "all"
	if m.categoryFilter != "" {
		catLabel = m.categoryFilter
	}
	instLabel := "all"
	if m.installedFilter != "" {
		instLabel = m.installedFilter
	}
	return kindLabel, catLabel, instLabel
}

var kindStyles = map[string]lipgloss.Style{
	"rule":     lipgloss.NewStyle().Foreground(lipgloss.Color("75")),
	"skill":    lipgloss.NewStyle().Foreground(lipgloss.Color("114")),
	"workflow": lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
	"agent":    lipgloss.NewStyle().Foreground(lipgloss.Color("141")),
	"hook":     lipgloss.NewStyle().Foreground(lipgloss.Color("176")),
	"pack":     lipgloss.NewStyle().Foreground(lipgloss.Color("204")),
}

func (m Model) searchKindStyle(kind string) lipgloss.Style {
	if s, ok := kindStyles[kind]; ok {
		return s
	}
	return common.DimStyle
}
