package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/domain"
)

// previewModel is a full-screen overlay that displays markdown file content
// with parsed frontmatter and a scrollable body.
type previewModel struct {
	title    string
	category domain.PackCategory
	packName string
	filePath string

	frontmatter []fmEntry
	body        string
	errText     string

	viewport viewport.Model
	ready    bool
	width    int
	height   int
}

func newPreviewModel(width, height int) previewModel {
	return previewModel{width: width, height: height}
}

// setContent initialises the viewport with frontmatter + body content.
func (m *previewModel) setContent(msg previewLoadedMsg) {
	m.title = msg.title
	m.category = msg.category
	m.packName = msg.packName
	m.filePath = msg.filePath

	if msg.err != nil {
		m.errText = msg.err.Error()
		return
	}

	m.frontmatter = msg.frontmatter
	m.body = msg.body
	m.renderViewport()
}

// renderViewport rebuilds the viewport content from stored frontmatter + body.
func (m *previewModel) renderViewport() {
	maxW := m.width - 4 // border + padding
	if maxW < 20 {
		maxW = 20
	}

	var sb strings.Builder

	// Header.
	header := m.category.SingularLabel() + "  " + m.title
	if m.packName != "" {
		header += "  " + dimStyle.Render("("+m.packName+")")
	}
	sb.WriteString(previewTitleStyle.Render(header))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render(m.filePath))
	sb.WriteString("\n")
	ruleW := maxW
	if ruleW > 80 {
		ruleW = 80
	}
	sb.WriteString(strings.Repeat("─", ruleW))
	sb.WriteString("\n\n")

	// Frontmatter section.
	if len(m.frontmatter) > 0 {
		for _, e := range m.frontmatter {
			sb.WriteString(previewKeyStyle.Render(e.key + ":"))
			sb.WriteString(" ")
			sb.WriteString(e.value)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
		sb.WriteString(strings.Repeat("─", ruleW))
		sb.WriteString("\n\n")
	}

	// Body.
	body := strings.TrimRight(m.body, "\n")
	if body == "" && len(m.frontmatter) == 0 {
		sb.WriteString(dimStyle.Render("(empty)"))
	} else {
		sb.WriteString(body)
	}

	// Build viewport.
	vpH := m.height - 4 // border top/bottom + footer + help
	if vpH < 5 {
		vpH = 5
	}
	vp := viewport.New(maxW, vpH)
	vp.SetContent(sb.String())
	m.viewport = vp
	m.ready = true
}

func (m previewModel) Update(msg tea.Msg) (previewModel, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "e", "i":
			return m, m.openEditor()
		}
	}

	// Delegate everything else (scroll keys, mouse) to viewport.
	if m.ready {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m previewModel) View() string {
	if m.errText != "" {
		content := fmt.Sprintf("\n  %s\n\n  %s\n",
			previewTitleStyle.Render(m.title),
			errorStyle.Render("Error: "+m.errText))
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

	pct := m.viewport.ScrollPercent()
	scrollInfo := fmt.Sprintf("%3.0f%%", pct*100)
	footer := dimStyle.Render(fmt.Sprintf("─── %s ───", scrollInfo))

	content := m.viewport.View() + "\n" + footer
	return previewBorderStyle.
		Width(m.width - 2).
		Height(m.height - 2).
		Render(content)
}

func (m previewModel) helpText() string {
	return "j/k:scroll  i/e:edit  esc:close"
}

// openEditor spawns $EDITOR via tea.ExecProcess (suspends TUI).
func (m previewModel) openEditor() tea.Cmd {
	return openFileInEditor(m.filePath)
}

// openFileInEditor spawns $EDITOR for the given file path, suspending the TUI.
func openFileInEditor(filePath string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}
	c := exec.Command(editor, filePath)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorFinishedMsg{filePath: filePath, err: err}
	})
}

// loadPreview reads a markdown file asynchronously, parses frontmatter,
// and returns a previewLoadedMsg.
func loadPreview(title string, category domain.PackCategory, packName, filePath string) tea.Cmd {
	return func() tea.Msg {
		const maxSize = 512 * 1024
		f, err := os.Open(filePath)
		if err != nil {
			return previewLoadedMsg{
				title: title, category: category,
				packName: packName, filePath: filePath, err: err,
			}
		}
		defer f.Close()
		buf := make([]byte, maxSize+1)
		n, _ := io.ReadFull(f, buf)
		truncated := n > maxSize
		if truncated {
			n = maxSize
		}
		content := string(buf[:n])

		fm, body := parseFrontmatter(content)
		if truncated {
			body += "\n\n--- (truncated at 512 KB) ---"
		}

		return previewLoadedMsg{
			title: title, category: category,
			packName: packName, filePath: filePath,
			frontmatter: fm, body: body,
		}
	}
}

// parseFrontmatter splits YAML frontmatter from markdown body.
// Returns ordered key-value pairs for display.
func parseFrontmatter(content string) ([]fmEntry, string) {
	fm, body, err := domain.SplitFrontmatter([]byte(content))
	if err != nil || len(fm) == 0 {
		return nil, content
	}

	// Parse YAML preserving key order via yaml.v3 Node API.
	var doc yaml.Node
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return nil, content
	}

	var entries []fmEntry
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		mapping := doc.Content[0]
		if mapping.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(mapping.Content); i += 2 {
				key := mapping.Content[i].Value
				val := formatYAMLValue(mapping.Content[i+1])
				entries = append(entries, fmEntry{key: key, value: val})
			}
		}
	}

	return entries, strings.TrimLeft(string(body), "\n")
}

// formatYAMLValue renders a yaml.Node value as a display string.
func formatYAMLValue(n *yaml.Node) string {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Value
	case yaml.SequenceNode:
		items := make([]string, len(n.Content))
		for i, c := range n.Content {
			items[i] = formatYAMLValue(c)
		}
		return "[" + strings.Join(items, ", ") + "]"
	case yaml.MappingNode:
		pairs := make([]string, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			pairs = append(pairs, n.Content[i].Value+": "+formatYAMLValue(n.Content[i+1]))
		}
		return "{" + strings.Join(pairs, ", ") + "}"
	}
	return fmt.Sprintf("%v", n.Value)
}
