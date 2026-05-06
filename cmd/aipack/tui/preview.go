package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	previewoverlay "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/overlays/preview"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/domain"
)

type previewModel = previewoverlay.Model

func newPreviewModel(width, height int) previewModel {
	return previewoverlay.New(width, height, openFileInEditor, openFileWithSystem)
}

// openFileInEditor spawns $EDITOR for the given file path, suspending the TUI.
func openFileInEditor(filePath string) tea.Cmd {
	editor, err := resolveEditorCommand(os.Getenv("EDITOR"), os.Getenv("VISUAL"), runtime.GOOS)
	if err != nil {
		return editorErrorCmd(filePath, err)
	}
	editorPath, err := exec.LookPath(editor.Name)
	if err != nil {
		return editorErrorCmd(filePath, fmt.Errorf("editor command %q from %s is not available: %w; set EDITOR or VISUAL to an installed editor", editor.Raw, editor.Source, err))
	}
	args := append([]string{}, editor.Args...)
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

func resolveEditorCommand(editor, visual, goos string) (cmdutil.EditorCommand, error) {
	return cmdutil.ResolveEditorCommand(editor, visual, goos)
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
	return common.LoadPreview(title, category, packName, filePath)
}

// parseFrontmatter splits YAML frontmatter from markdown body.
// Returns ordered key-value pairs for display.
func parseFrontmatter(content string) ([]fmEntry, string) {
	return common.ParseFrontmatter(content)
}
