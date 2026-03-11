package tui

import (
	"fmt"
	"strings"
	"testing"
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
		category: CatRules,
		filePath: "/tmp/pack/rules/empty.md",
	})
	view := p.View()
	if !strings.Contains(view, "(empty)") {
		t.Fatalf("expected (empty) in view, got:\n%s", view)
	}
}

func TestPreviewModel_HelpText(t *testing.T) {
	t.Parallel()
	p := newPreviewModel(80, 40)
	help := p.helpText()
	if !strings.Contains(help, "e:edit") {
		t.Fatalf("expected help to mention e:edit, got %q", help)
	}
	if !strings.Contains(help, "esc:close") {
		t.Fatalf("expected help to mention esc:close, got %q", help)
	}
}
