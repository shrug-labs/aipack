package app

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"

	"gopkg.in/yaml.v3"
)

// PromptEntry is a prompt found in an installed pack.
type PromptEntry struct {
	Name        string
	Pack        string
	Description string
	Category    string
	Body        []byte
	SourcePath  string
}

// PromptList scans all installed packs and returns all prompts.
func PromptList(configDir string) ([]PromptEntry, error) {
	packsDir := PacksDir(configDir)
	entries, err := os.ReadDir(packsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading packs dir: %w", err)
	}

	var prompts []PromptEntry
	for _, e := range entries {
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		packDir := filepath.Join(packsDir, e.Name())
		manifestPath := filepath.Join(packDir, "pack.json")
		m, err := config.LoadPackManifest(manifestPath)
		if err != nil {
			continue // skip packs with invalid manifests
		}
		packRoot := config.ResolvePackRoot(manifestPath, m.Root)
		for _, id := range m.Prompts {
			pe, err := loadPrompt(id, m.Name, packRoot)
			if err != nil {
				continue // skip individual broken prompts
			}
			prompts = append(prompts, pe)
		}
	}

	sort.Slice(prompts, func(i, j int) bool {
		return prompts[i].Name < prompts[j].Name
	})
	return prompts, nil
}

// PromptShow returns a single prompt by name.
func PromptShow(name string, configDir string) (PromptEntry, error) {
	all, err := PromptList(configDir)
	if err != nil {
		return PromptEntry{}, err
	}
	var matches []PromptEntry
	for _, p := range all {
		if p.Name == name {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return PromptEntry{}, fmt.Errorf("prompt not found: %s", name)
	case 1:
		return matches[0], nil
	default:
		packs := make([]string, len(matches))
		for i, m := range matches {
			packs[i] = m.Pack
		}
		return PromptEntry{}, fmt.Errorf("ambiguous prompt %q: found in packs %v", name, packs)
	}
}

// PromptCopy copies prompt body to clipboard.
func PromptCopy(name string, configDir string) error {
	p, err := PromptShow(name, configDir)
	if err != nil {
		return err
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	default:
		return fmt.Errorf("clipboard not supported on %s", runtime.GOOS)
	}
	cmd.Stdin = bytes.NewReader(p.Body)
	return cmd.Run()
}

func loadPrompt(id, packName, packRoot string) (PromptEntry, error) {
	path := filepath.Join(packRoot, "prompts", id+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return PromptEntry{}, err
	}

	fm, body, err := domain.SplitFrontmatter(raw)
	if err != nil {
		return PromptEntry{}, err
	}

	var meta domain.PromptFrontmatter
	if fm != nil {
		_ = yaml.Unmarshal(fm, &meta)
	}

	return PromptEntry{
		Name:        id,
		Pack:        packName,
		Description: meta.Description,
		Category:    meta.Category,
		Body:        body,
		SourcePath:  path,
	}, nil
}
