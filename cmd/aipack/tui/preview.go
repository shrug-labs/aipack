package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

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
	maxW := max(m.width-4, 20) // border + padding

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
	ruleW := min(maxW, 80)
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
	vpH := max(m.height-4, 5) // border top/bottom + footer + help
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
		case "o":
			return m, openFileWithSystem(m.filePath)
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
	return "j/k:scroll  i/e:edit  o:open  esc:close"
}

// openEditor spawns $EDITOR via tea.ExecProcess (suspends TUI).
func (m previewModel) openEditor() tea.Cmd {
	return openFileInEditor(m.filePath)
}

type editorCommand struct {
	source string
	raw    string
	name   string
	args   []string
}

// openFileInEditor spawns $EDITOR for the given file path, suspending the TUI.
func openFileInEditor(filePath string) tea.Cmd {
	editor, err := resolveEditorCommand(os.Getenv("EDITOR"), os.Getenv("VISUAL"), runtime.GOOS)
	if err != nil {
		return editorErrorCmd(filePath, err)
	}
	editorPath, err := exec.LookPath(editor.name)
	if err != nil {
		return editorErrorCmd(filePath, fmt.Errorf("editor command %q from %s is not available: %w; set EDITOR or VISUAL to an installed editor", editor.raw, editor.source, err))
	}
	args := append([]string{}, editor.args...)
	args = append(args, filePath)
	c := exec.Command(editorPath, args...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorFinishedMsg{filePath: filePath, err: err}
	})
}

func editorErrorCmd(filePath string, err error) tea.Cmd {
	return func() tea.Msg {
		return editorFinishedMsg{filePath: filePath, err: err}
	}
}

func resolveEditorCommand(editor, visual, goos string) (editorCommand, error) {
	raw := strings.TrimSpace(editor)
	source := "EDITOR"
	if raw == "" {
		raw = strings.TrimSpace(visual)
		source = "VISUAL"
	}
	if raw == "" {
		source = "default"
		if goos == "windows" {
			raw = "notepad.exe"
		} else {
			raw = "vi"
		}
	}
	parts, err := splitEditorCommand(raw)
	if err != nil {
		return editorCommand{}, fmt.Errorf("parse editor command %q from %s: %w", raw, source, err)
	}
	return editorCommand{
		source: source,
		raw:    raw,
		name:   parts[0],
		args:   parts[1:],
	}, nil
}

func splitEditorCommand(s string) ([]string, error) {
	var parts []string
	var b strings.Builder
	var quote rune
	for _, r := range s {
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			if b.Len() > 0 {
				parts = append(parts, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote %q", string(quote))
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return parts, nil
}

// openFileWithSystem hands the file to the OS default opener (`open` on
// macOS, `xdg-open` on Linux, `cmd /c start` on Windows). The launched
// app runs detached — the TUI does not suspend and there is no completion
// event. Only Start() failures surface, via systemOpenErrorMsg.
func openFileWithSystem(filePath string) tea.Cmd {
	name, args := systemOpenCommand(runtime.GOOS, filePath)
	return func() tea.Msg {
		c := exec.Command(name, args...)
		if err := c.Start(); err != nil {
			return systemOpenErrorMsg{err: fmt.Errorf("launch %s: %w", name, err)}
		}
		// Reap the launcher (open/xdg-open/cmd) so it doesn't sit as a
		// zombie until the TUI exits. The launcher itself returns in ms
		// after spawning the real GUI app.
		go func() { _ = c.Wait() }()
		return nil
	}
}

// launchFile dispatches to openFileInEditor or openFileWithSystem based
// on which action the caller is handling. Lets each Edit/Open case in
// the action handlers collapse to one branch.
func launchFile(action, filePath string) tea.Cmd {
	if action == actOpenFile {
		return openFileWithSystem(filePath)
	}
	return openFileInEditor(filePath)
}

// systemOpenCommand returns the executable + args for opening a file
// with the OS default handler on the given platform.
func systemOpenCommand(goos, filePath string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{filePath}
	case "windows":
		// `start` is a cmd.exe builtin; the empty "" is the window-title arg.
		return "cmd", []string{"/c", "start", "", filePath}
	default:
		return "xdg-open", []string{filePath}
	}
}

// loadPreview reads a markdown file asynchronously, parses frontmatter,
// and returns a previewLoadedMsg.
func loadPreview(title string, category domain.PackCategory, packName, filePath string) tea.Cmd {
	return func() tea.Msg {
		const maxSize = 512 * 1024

		// Skills are directories — read the entry file inside.
		target := filePath
		if info, err := os.Stat(filePath); err == nil && info.IsDir() {
			target = filepath.Join(filePath, domain.SkillEntryFile)
		}

		f, err := os.Open(target)
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
