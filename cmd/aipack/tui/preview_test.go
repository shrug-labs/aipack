package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestParseFrontmatter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		wantKeys []string
		wantBody string
	}{
		{
			name:     "standard frontmatter",
			input:    "---\ntitle: hello\ndescription: world\n---\n\n# Body",
			wantKeys: []string{"title", "description"},
			wantBody: "# Body",
		},
		{
			name:     "no frontmatter",
			input:    "# Just a heading\n\nSome text.",
			wantKeys: nil,
			wantBody: "# Just a heading\n\nSome text.",
		},
		{
			name:     "empty body",
			input:    "---\ntitle: test\n---\n",
			wantKeys: []string{"title"},
			wantBody: "",
		},
		{
			name:     "list in frontmatter",
			input:    "---\naudiece:\n  - dev\n  - ops\n---\n\nContent.",
			wantKeys: []string{"audiece"},
			wantBody: "Content.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fm, body := parseFrontmatter(tt.input)
			if len(fm) != len(tt.wantKeys) {
				t.Fatalf("expected %d frontmatter entries, got %d", len(tt.wantKeys), len(fm))
			}
			for i, key := range tt.wantKeys {
				if fm[i].key != key {
					t.Errorf("entry %d: expected key %q, got %q", i, key, fm[i].key)
				}
			}
			if body != tt.wantBody {
				t.Errorf("expected body %q, got %q", tt.wantBody, body)
			}
		})
	}
}

func TestPreviewModel_ErrorRendering(t *testing.T) {
	t.Parallel()
	p := newPreviewModel(80, 40)
	p.setContent(previewLoadedMsg{
		title: "missing-rule",
		err:   fmt.Errorf("open: no such file"),
	})
	view := p.View()
	if !strings.Contains(view, "no such file") {
		t.Fatalf("expected error in view, got:\n%s", view)
	}
}

func TestPreviewModel_EmptyContent(t *testing.T) {
	t.Parallel()
	p := newPreviewModel(80, 40)
	p.setContent(previewLoadedMsg{
		title:    "empty-rule",
		category: domain.CategoryRules,
		filePath: "/tmp/pack/rules/empty.md",
	})
	view := p.View()
	if !strings.Contains(view, "(empty)") {
		t.Fatalf("expected (empty) in view, got:\n%s", view)
	}
}

func TestLoadPreview_SkillDirectory(t *testing.T) {
	t.Parallel()

	// Create a temp skill directory with a SKILL.md entry file.
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "agent-configuration")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ntitle: Agent Configuration\n---\n\nSkill body content here."
	if err := os.WriteFile(filepath.Join(skillDir, domain.SkillEntryFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// loadPreview returns a tea.Cmd; execute it to get the message.
	cmd := loadPreview("agent-configuration", domain.CategorySkills, "aipack-core", skillDir)
	msg := cmd().(previewLoadedMsg)

	if msg.err != nil {
		t.Fatalf("unexpected error: %v", msg.err)
	}
	if len(msg.frontmatter) == 0 {
		t.Fatal("expected frontmatter entries, got none")
	}
	if !strings.Contains(msg.body, "Skill body content here") {
		t.Fatalf("expected skill body in preview, got: %q", msg.body)
	}
}

func TestPreviewModel_HelpText(t *testing.T) {
	t.Parallel()
	p := newPreviewModel(80, 40)
	help := p.helpText()
	if !strings.Contains(help, "e:edit") {
		t.Fatalf("expected help to mention e:edit, got %q", help)
	}
	if !strings.Contains(help, "o:open") {
		t.Fatalf("expected help to mention o:open, got %q", help)
	}
	if !strings.Contains(help, "esc:close") {
		t.Fatalf("expected help to mention esc:close, got %q", help)
	}
}

func TestResolveEditorCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		editor     string
		visual     string
		goos       string
		wantSource string
		wantName   string
		wantArgs   []string
	}{
		{
			name:       "editor with args",
			editor:     "code --wait",
			visual:     "vim",
			goos:       "linux",
			wantSource: "EDITOR",
			wantName:   "code",
			wantArgs:   []string{"--wait"},
		},
		{
			name:       "visual fallback",
			visual:     "nvim -O",
			goos:       "darwin",
			wantSource: "VISUAL",
			wantName:   "nvim",
			wantArgs:   []string{"-O"},
		},
		{
			name:       "windows default",
			goos:       "windows",
			wantSource: "default",
			wantName:   "notepad.exe",
		},
		{
			name:       "unix default",
			goos:       "linux",
			wantSource: "default",
			wantName:   "vi",
		},
		{
			name:       "quoted executable path",
			editor:     `"C:\Program Files\Notepad++\notepad++.exe" -multiInst`,
			goos:       "windows",
			wantSource: "EDITOR",
			wantName:   `C:\Program Files\Notepad++\notepad++.exe`,
			wantArgs:   []string{"-multiInst"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveEditorCommand(tt.editor, tt.visual, tt.goos)
			if err != nil {
				t.Fatalf("resolveEditorCommand: %v", err)
			}
			if got.source != tt.wantSource {
				t.Fatalf("source = %q, want %q", got.source, tt.wantSource)
			}
			if got.name != tt.wantName {
				t.Fatalf("name = %q, want %q", got.name, tt.wantName)
			}
			if strings.Join(got.args, "\x00") != strings.Join(tt.wantArgs, "\x00") {
				t.Fatalf("args = %#v, want %#v", got.args, tt.wantArgs)
			}
		})
	}
}

func TestResolveEditorCommand_UnterminatedQuote(t *testing.T) {
	t.Parallel()
	_, err := resolveEditorCommand(`"C:\Program Files\Vim\vim.exe`, "", "windows")
	if err == nil {
		t.Fatal("expected unterminated quote error")
	}
	if !strings.Contains(err.Error(), "unterminated quote") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSystemOpenCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{"darwin", "open", []string{"file.md"}},
		{"linux", "xdg-open", []string{"file.md"}},
		{"freebsd", "xdg-open", []string{"file.md"}},
		{"windows", "cmd", []string{"/c", "start", "", "file.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			t.Parallel()
			name, args := systemOpenCommand(tt.goos, "file.md")
			if name != tt.wantName {
				t.Fatalf("name = %q, want %q", name, tt.wantName)
			}
			if strings.Join(args, "\x00") != strings.Join(tt.wantArgs, "\x00") {
				t.Fatalf("args = %#v, want %#v", args, tt.wantArgs)
			}
		})
	}
}
