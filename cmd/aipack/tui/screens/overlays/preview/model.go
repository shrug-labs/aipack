package preview

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/domain"
)

type OpenFunc func(string) tea.Cmd

var (
	previewBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("205")).
				Padding(0, 1)
	previewTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	previewKeyStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
)

// Model is a full-screen overlay that displays markdown file content with
// parsed frontmatter and a scrollable body.
type Model struct {
	title    string
	category domain.PackCategory
	packName string
	filePath string

	frontmatter []common.FrontmatterEntry
	body        string
	errText     string

	viewport viewport.Model
	ready    bool
	width    int
	height   int

	openEditor OpenFunc
	openSystem OpenFunc
}

func New(width, height int, openEditor, openSystem OpenFunc) Model {
	return Model{
		width:      width,
		height:     height,
		openEditor: openEditor,
		openSystem: openSystem,
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) SetSize(width, height int) common.Screen {
	return m.SetSizeModel(width, height)
}

func (m Model) SetSizeModel(width, height int) Model {
	m.width = width
	m.height = height
	if m.ready {
		m.viewport.SetWidth(width - 4)
		m.viewport.SetHeight(height - 4)
	}
	return m
}

func (m Model) SetContent(msg common.PreviewLoadedMsg) Model {
	m.title = msg.Title
	m.category = msg.Category
	m.packName = msg.PackName
	m.filePath = msg.FilePath

	if msg.Err != nil {
		m.errText = msg.Err.Error()
		return m
	}

	m.frontmatter = msg.Frontmatter
	m.body = msg.Body
	m.renderViewport()
	return m
}

func (m *Model) renderViewport() {
	maxW := max(m.width-4, 20)

	var sb strings.Builder
	header := m.title
	if label := m.category.SingularLabel(); label != "" {
		header = label + "  " + header
	}
	if m.packName != "" {
		header += "  " + common.DimStyle.Render("("+m.packName+")")
	}
	sb.WriteString(previewTitleStyle.Render(header))
	sb.WriteString("\n")
	if m.filePath != "" {
		sb.WriteString(common.DimStyle.Render(m.filePath))
		sb.WriteString("\n")
	}
	ruleW := min(maxW, 80)
	sb.WriteString(strings.Repeat("-", ruleW))
	sb.WriteString("\n\n")

	if len(m.frontmatter) > 0 {
		for _, e := range m.frontmatter {
			sb.WriteString(previewKeyStyle.Render(e.Key + ":"))
			sb.WriteString(" ")
			sb.WriteString(e.Value)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
		sb.WriteString(strings.Repeat("-", ruleW))
		sb.WriteString("\n\n")
	}

	body := strings.TrimRight(m.body, "\n")
	if body == "" && len(m.frontmatter) == 0 {
		sb.WriteString(common.DimStyle.Render("(empty)"))
	} else {
		sb.WriteString(body)
	}

	vpH := max(m.height-4, 5)
	vp := viewport.New(viewport.WithWidth(maxW), viewport.WithHeight(vpH))
	vp.SetContent(sb.String())
	m.viewport = vp
	m.ready = true
}

func (m Model) Update(msg tea.Msg) (common.Screen, tea.Cmd) {
	return m.UpdateModel(msg)
}

func (m Model) UpdateModel(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.SetSizeModel(msg.Width, msg.Height), nil
	case common.LayerHitMsg:
		if !common.IsLeftClick(msg.Mouse) {
			return m, nil
		}
		switch msg.ID {
		case "preview:edit":
			if m.filePath != "" && m.openEditor != nil {
				return m, m.openEditor(m.filePath)
			}
		case "preview:open":
			if m.filePath != "" && m.openSystem != nil {
				return m, m.openSystem(m.filePath)
			}
		}
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "e", "i":
			if m.filePath != "" && m.openEditor != nil {
				return m, m.openEditor(m.filePath)
			}
		case "o":
			if m.filePath != "" && m.openSystem != nil {
				return m, m.openSystem(m.filePath)
			}
		}
	}

	if m.ready {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) View() *lipgloss.Layer {
	layer := lipgloss.NewLayer(m.Render()).
		AddLayers(lipgloss.NewLayer("[close]").ID("preview:close").X(max(2, m.width-10)).Y(1).Z(-1))
	if m.filePath != "" {
		layer = layer.AddLayers(
			lipgloss.NewLayer("[edit]").ID("preview:edit").X(max(2, m.width-27)).Y(1).Z(-1),
			lipgloss.NewLayer("[open]").ID("preview:open").X(max(2, m.width-19)).Y(1).Z(-1),
		)
	}
	return layer
}

func (m Model) Render() string {
	if m.errText != "" {
		content := fmt.Sprintf("\n  %s\n\n  %s\n",
			previewTitleStyle.Render(m.title),
			common.ErrorStyle.Render("Error: "+m.errText))
		return previewBorderStyle.
			Width(m.width - 2).
			Height(m.height - 2).
			Render(content)
	}

	if !m.ready {
		return previewBorderStyle.
			Width(m.width - 2).
			Height(m.height - 2).
			Render("\n  Loading...")
	}

	scrollInfo := fmt.Sprintf("%3.0f%%", m.viewport.ScrollPercent()*100)
	footer := common.DimStyle.Render(fmt.Sprintf("--- %s ---", scrollInfo))
	content := m.viewport.View() + "\n" + footer
	return previewBorderStyle.
		Width(m.width - 2).
		Height(m.height - 2).
		Render(content)
}

func (m Model) HelpText() string {
	if m.filePath == "" {
		return "j/k:scroll  esc:close"
	}
	return "j/k:scroll  i/e:edit  o:open  esc:close"
}

func (m Model) Ready() bool {
	return m.ready
}

func (m Model) Request() (title string, category domain.PackCategory, packName, filePath string) {
	return m.title, m.category, m.packName, m.filePath
}
