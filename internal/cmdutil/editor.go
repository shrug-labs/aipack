package cmdutil

import (
	"fmt"
	"strings"
	"unicode"
)

// EditorCommand is an editor executable plus its configured arguments.
type EditorCommand struct {
	Source string
	Raw    string
	Name   string
	Args   []string
}

// ResolveEditorCommand resolves EDITOR/VISUAL-style values to a command.
func ResolveEditorCommand(editor, visual, goos string) (EditorCommand, error) {
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
	parts, err := SplitEditorCommand(raw)
	if err != nil {
		return EditorCommand{}, fmt.Errorf("parse editor command %q from %s: %w", raw, source, err)
	}
	return EditorCommand{
		Source: source,
		Raw:    raw,
		Name:   parts[0],
		Args:   parts[1:],
	}, nil
}

// SplitEditorCommand splits shell-like editor commands with simple quote support.
func SplitEditorCommand(s string) ([]string, error) {
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
