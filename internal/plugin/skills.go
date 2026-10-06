package plugin

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Codex 0.159.2 uses parsed frontmatter names in both package dialects.
// Invalid skills remain in the payload for native diagnostics, but not selectors.
func discoverCodexSkills(root, rel string, direct bool, out map[string][]string) error {
	ancestors := map[string]bool{}
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		path, err := safePath(root, dir)
		if err != nil {
			return err
		}
		real, err := filepath.EvalSymlinks(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if ancestors[real] {
			return nil
		}
		info, err := os.Stat(real)
		if err != nil || !info.IsDir() {
			return err
		}
		ancestors[real] = true
		defer delete(ancestors, real)
		entries, err := os.ReadDir(real)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			local := filepath.Join(dir, entry.Name())
			path, err := safePath(root, local)
			if err != nil {
				return err
			}
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.IsDir() {
				// Native recursive discovery scans directories through depth six.
				if depth < 6 && (!direct || depth == 0) {
					if err := walk(local, depth+1); err != nil {
						return err
					}
				}
				continue
			}
			if entry.Name() != "SKILL.md" || entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || direct && depth != 1 {
				continue
			}
			real, err := filepath.EvalSymlinks(path)
			if err != nil {
				continue
			}
			body, err := os.ReadFile(real)
			if err != nil {
				continue
			}
			id := codexSkillID(body, filepath.Base(filepath.Dir(real)))
			local = filepath.ToSlash(local)
			if id != "" && !slices.Contains(out[id], local) {
				out[id] = append(out[id], local)
			}
		}
		return nil
	}
	return walk(rel, 0)
}

func codexSkillID(body []byte, fallback string) string {
	if !utf8.Valid(body) {
		return ""
	}
	lines := strings.Split(string(body), "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	var frontmatter string
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			frontmatter = strings.Join(lines[1:i], "\n")
			break
		}
	}
	var fields struct {
		Name        *string   `yaml:"name"`
		Description *string   `yaml:"description"`
		Metadata    yaml.Node `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(frontmatter), &fields); err != nil {
		if yaml.Unmarshal([]byte(repairCodexSkillYAML(frontmatter)), &fields) != nil {
			return ""
		}
	}
	if fields.Metadata.Kind != 0 {
		var metadata struct {
			ShortDescription *string `yaml:"short-description"`
		}
		if fields.Metadata.Kind != yaml.MappingNode || fields.Metadata.Decode(&metadata) != nil {
			return ""
		}
	}
	if fields.Description == nil || len(strings.Fields(*fields.Description)) == 0 {
		return ""
	}
	name := strings.Join(strings.Fields(fallback), " ")
	if fields.Name != nil {
		if value := strings.Join(strings.Fields(*fields.Name), " "); value != "" {
			name = value
		}
	}
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return ""
	}
	return name
}

// Match Codex's recovery of unquoted prose scalars without changing source bytes.
func repairCodexSkillYAML(frontmatter string) string {
	lines := strings.Split(frontmatter, "\n")
	blockIndent := -1
	for i, line := range lines {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if blockIndent >= 0 && (strings.TrimSpace(line) == "" || indent > blockIndent) {
			continue
		}
		blockIndent = -1
		key, value, ok := strings.Cut(line, ":")
		first, _ := utf8.DecodeRuneInString(value)
		if !ok || strings.TrimSpace(key) == "" || value != "" && !unicode.IsSpace(first) {
			continue
		}
		scalar := strings.TrimLeftFunc(value, unicode.IsSpace)
		prefix := value[:len(value)-len(scalar)]
		comment := ""
		for index, char := range scalar {
			previous, _ := utf8.DecodeLastRuneInString(scalar[:index])
			if char == '#' && (index == 0 || unicode.IsSpace(previous)) {
				end := len(strings.TrimRightFunc(scalar[:index], unicode.IsSpace))
				comment, scalar = scalar[end:], scalar[:end]
				break
			}
		}
		scalar = strings.TrimRightFunc(scalar, unicode.IsSpace)
		if scalar == "" || scalar[0] == '\'' || scalar[0] == '"' {
			continue
		}
		if scalar[0] == '|' || scalar[0] == '>' {
			blockIndent = indent
			continue
		}
		prose := false
		for index, char := range scalar {
			if char != ':' {
				continue
			}
			next, _ := utf8.DecodeRuneInString(scalar[index+1:])
			if unicode.IsSpace(next) {
				prose = true
				break
			}
		}
		var parsed any
		if prose || strings.ContainsRune("[{@`", rune(scalar[0])) && yaml.Unmarshal([]byte(scalar), &parsed) != nil {
			lines[i] = key + ":" + prefix + "'" + strings.ReplaceAll(scalar, "'", "''") + "'" + comment
		}
	}
	return strings.Join(lines, "\n")
}
