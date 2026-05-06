package common

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/domain"
)

// PreviewLoadedMsg carries the result of an async file read for preview.
type PreviewLoadedMsg struct {
	Title       string
	Category    domain.PackCategory
	PackName    string
	FilePath    string
	Frontmatter []FrontmatterEntry
	Body        string
	Err         error
}

// FrontmatterEntry is a single frontmatter key-value pair, preserving YAML order.
type FrontmatterEntry struct {
	Key   string
	Value string
}

// LoadPreview reads a markdown file asynchronously, parses frontmatter,
// and returns a PreviewLoadedMsg.
func LoadPreview(title string, category domain.PackCategory, packName, filePath string) tea.Cmd {
	return func() tea.Msg {
		const maxSize = 512 * 1024

		target := filePath
		if info, err := os.Stat(filePath); err == nil && info.IsDir() {
			target = filepath.Join(filePath, domain.SkillEntryFile)
		}

		f, err := os.Open(target)
		if err != nil {
			return PreviewLoadedMsg{
				Title: title, Category: category,
				PackName: packName, FilePath: filePath, Err: err,
			}
		}
		defer f.Close()

		data, _ := io.ReadAll(io.LimitReader(f, maxSize+1))
		truncated := len(data) > maxSize
		if truncated {
			data = data[:maxSize]
		}
		content := string(data)

		fm, body := ParseFrontmatter(content)
		if truncated {
			body += "\n\n--- (truncated at 512 KB) ---"
		}

		return PreviewLoadedMsg{
			Title: title, Category: category,
			PackName: packName, FilePath: filePath,
			Frontmatter: fm, Body: body,
		}
	}
}

// ParseFrontmatter splits YAML frontmatter from markdown body.
func ParseFrontmatter(content string) ([]FrontmatterEntry, string) {
	fm, body, err := domain.SplitFrontmatter([]byte(content))
	if err != nil || len(fm) == 0 {
		return nil, content
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return nil, content
	}

	var entries []FrontmatterEntry
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		mapping := doc.Content[0]
		if mapping.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(mapping.Content); i += 2 {
				key := mapping.Content[i].Value
				val := FormatYAMLValue(mapping.Content[i+1])
				entries = append(entries, FrontmatterEntry{Key: key, Value: val})
			}
		}
	}

	return entries, strings.TrimLeft(string(body), "\n")
}

// FormatYAMLValue renders a yaml.Node value as a display string.
func FormatYAMLValue(n *yaml.Node) string {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Value
	case yaml.SequenceNode:
		items := make([]string, len(n.Content))
		for i, c := range n.Content {
			items[i] = FormatYAMLValue(c)
		}
		return "[" + strings.Join(items, ", ") + "]"
	case yaml.MappingNode:
		pairs := make([]string, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			pairs = append(pairs, n.Content[i].Value+": "+FormatYAMLValue(n.Content[i+1]))
		}
		return "{" + strings.Join(pairs, ", ") + "}"
	}
	return fmt.Sprintf("%v", n.Value)
}
