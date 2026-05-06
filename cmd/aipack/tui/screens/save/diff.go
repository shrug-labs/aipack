package save

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
)

type diffViewModel struct {
	title string
	dst   string
	isNew bool

	viewport viewport.Model
	ready    bool
	errText  string
	width    int
	height   int
}

func newDiffViewModel(width, height int, title, dst string) diffViewModel {
	return diffViewModel{
		title:  title,
		dst:    dst,
		width:  width,
		height: height,
	}
}

func (m *diffViewModel) setContent(diffText string, isNew bool, newBody string, errText string) {
	if errText != "" {
		m.errText = errText
		m.ready = true
		return
	}

	m.isNew = isNew

	maxW := max(m.width-4, 20)
	var sb strings.Builder
	common.RenderUnifiedDiff(&sb, m.title, m.dst, diffText, isNew, newBody, maxW, "-")

	vpH := max(m.height-4, 5)
	vp := viewport.New(viewport.WithWidth(maxW), viewport.WithHeight(vpH))
	vp.SetContent(sb.String())
	m.viewport = vp
	m.ready = true
}

func (m diffViewModel) Update(msg tea.Msg) (diffViewModel, tea.Cmd) {
	if m.ready {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m diffViewModel) View() string {
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("125")).
		Padding(0, 1)

	if m.errText != "" {
		content := fmt.Sprintf("\n  %s\n\n  %s\n",
			common.SelectedStyle.Render("Diff: "+m.title),
			common.ErrorStyle.Render("Error: "+m.errText))
		return border.Width(m.width - 2).Height(m.height - 2).Render(content)
	}

	if !m.ready {
		return border.Width(m.width - 2).Height(m.height - 2).Render("\n  Loading...")
	}

	pct := m.viewport.ScrollPercent()
	scrollInfo := fmt.Sprintf("%3.0f%%", pct*100)
	footer := common.DimStyle.Render(fmt.Sprintf("--- %s ---", scrollInfo))

	content := m.viewport.View() + "\n" + footer
	return border.Width(m.width - 2).Height(m.height - 2).Render(content)
}
