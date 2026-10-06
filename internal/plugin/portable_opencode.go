package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
	"gopkg.in/yaml.v3"
)

func codexOpenCodeSkillIssue(s domain.NativePluginSelection, id string) string {
	paths := s.Package.Components[domain.CategorySkills][id]
	if len(paths) != 1 || !domain.ValidNativeName(id) || filepath.Base(paths[0]) != domain.SkillEntryFile {
		return "requires one local skill entrypoint and a preserved identity"
	}
	path, err := safePath(filepath.Join(s.Root, "upstream"), paths[0])
	if err != nil {
		return err.Error()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	fm, _, err := domain.SplitFrontmatter(raw)
	var meta map[string]any
	if err != nil || yaml.Unmarshal(fm, &meta) != nil || meta["name"] != id {
		return "OpenCode skill identity differs or its entrypoint cannot be read"
	}
	if description, exists := meta["description"]; exists {
		if _, ok := description.(string); !ok {
			return "OpenCode requires a string skill description"
		}
	}
	return ""
}

func CodexOpenCodeIssues(s domain.NativePluginSelection) []string {
	return Compatibility(s, domain.HarnessOpenCode).Unsupported
}

// RenderCodexForOpenCode preserves the package outside automatic native
// activation directories. Explicit paths and native permissions select skills;
// names include the original binding so different plugins can share leaf IDs.
// The returned settings are a managed overlay, never a source manifest.
func RenderCodexForOpenCode(s domain.NativePluginSelection, payloadRoot, dataRoot string) ([]File, map[string]any, error) {
	pkg := s.Package
	if pkg.Harness != domain.HarnessCodex || (pkg.Format != CodexLegacy && pkg.Format != AgentPlugins) || pkg.ConverterVersion != ConverterVersion {
		return nil, nil, fmt.Errorf("unsupported Codex source format/converter: %s/%d", pkg.Format, pkg.ConverterVersion)
	}
	if !domain.ValidNativeName(pkg.Name) || !domain.ValidNativeName(pkg.Marketplace) {
		return nil, nil, fmt.Errorf("invalid native plugin binding")
	}
	for _, path := range []string{payloadRoot, dataRoot} {
		if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || strings.Contains(path, "{env:") || strings.Contains(path, "{file:") {
			return nil, nil, fmt.Errorf("portable payload/data paths must be absolute without native config substitutions")
		}
	}
	for _, pair := range [][2]string{{payloadRoot, dataRoot}, {dataRoot, payloadRoot}} {
		if rel, err := filepath.Rel(pair[0], pair[1]); err != nil || filepath.IsLocal(rel) {
			return nil, nil, fmt.Errorf("portable payload and runtime data must have separate roots")
		}
	}
	if issues := CodexOpenCodeIssues(s); len(issues) > 0 {
		return nil, nil, fmt.Errorf("unsupported selected components: %s", strings.Join(issues, ", "))
	}
	root := filepath.Join(s.Root, "upstream")
	files, err := ReadFiles(root)
	if err != nil {
		return nil, nil, err
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, nil, err
	}
	indices := map[string]int{}
	for i, file := range files {
		indices[file.Path] = i
	}
	paths := []string{}
	permission := map[string]any{}
	skills := map[string]string{pkg.Binding() + ":*": "deny"}
	seen, names := map[string]bool{}, map[string]string{}
	for _, id := range s.Selected[domain.CategorySkills] {
		entry := pkg.Components[domain.CategorySkills][id][0]
		dir := filepath.Dir(entry)
		paths = append(paths, filepath.Join(payloadRoot, dir))
		skills[pkg.Binding()+":"+id] = "ask"
		err := util.WalkFilesResolvingSymlinks(filepath.Join(root, dir), root, func(logical, resolved string, info os.FileInfo) error {
			if filepath.Base(logical) != domain.SkillEntryFile || seen[logical] {
				return nil
			}
			seen[logical] = true
			raw, err := os.ReadFile(resolved)
			if err != nil {
				return err
			}
			fm, body, err := domain.SplitFrontmatter(raw)
			var meta map[string]any
			if err != nil || yaml.Unmarshal(fm, &meta) != nil {
				return fmt.Errorf("recursive OpenCode skill %s has invalid frontmatter", logical)
			}
			name, valid := meta["name"].(string)
			if !valid {
				return nil // The native loader ignores a skill without a string name.
			}
			if previous, exists := names[name]; exists && previous != logical {
				return fmt.Errorf("recursive OpenCode skill identity %q collides at %s and %s", name, previous, logical)
			}
			names[name] = logical
			rel, err := filepath.Rel(canonicalRoot, resolved)
			if err != nil {
				return err
			}
			index, exists := indices[filepath.ToSlash(rel)]
			if !exists {
				return fmt.Errorf("recursive OpenCode skill %s has no retained payload file", logical)
			}
			meta["name"] = pkg.Binding() + ":" + name
			fm, err = yaml.Marshal(meta)
			if err != nil {
				return err
			}
			if slices.Contains(s.Selected[domain.CategorySkills], name) {
				body = []byte(codexClaudeVariables.ReplaceAllStringFunc(string(body), func(variable string) string {
					if strings.Contains(variable, "DATA") {
						return dataRoot
					}
					return payloadRoot
				}))
			}
			files[index].Content = []byte("---\n" + string(fm) + "---\n" + string(body))
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	slices.Sort(paths)
	permission["skill"] = skills
	mcp := map[string]any{}
	serverNames := map[string]string{}
	for _, id := range s.Selected[domain.CategoryMCP] {
		entry, err := codexStdioMCP(s, id, domain.HarnessOpenCode, payloadRoot, dataRoot)
		if err != nil {
			return nil, nil, err
		}
		name := pkg.Binding() + ":" + id
		mcp[name] = entry
		prefix := OpenCodeToolName(name) + "_"
		if previous, exists := serverNames[prefix]; exists {
			return nil, nil, fmt.Errorf("OpenCode MCP namespace collides between %s and %s", previous, id)
		}
		serverNames[prefix] = id
		policy := s.MCPPolicy[id]
		if len(policy.AllowedTools)+len(policy.AlwaysAllowedTools) > 0 {
			permission[prefix+"*"] = "deny"
		}
		for _, tool := range policy.AllowedTools {
			permission[prefix+OpenCodeToolPattern(tool)] = "ask"
		}
		for _, tool := range policy.AlwaysAllowedTools {
			permission[prefix+OpenCodeToolPattern(tool)] = "allow"
		}
		for _, tool := range policy.DisabledTools {
			permission[prefix+OpenCodeToolPattern(tool)] = "deny"
		}
	}
	settings := map[string]any{"skills": map[string]any{"paths": paths}, "permission": permission, "mcp": mcp}
	return files, settings, nil
}

// OpenCodeToolName matches the native MCP identifier sanitizer.
func OpenCodeToolName(value string) string {
	return openCodeToolIdentifier(value, false)
}

// OpenCodeToolPattern preserves profile wildcards in tool permission patterns.
func OpenCodeToolPattern(value string) string {
	return openCodeToolIdentifier(value, true)
}

func openCodeToolIdentifier(value string, pattern bool) string {
	var out strings.Builder
	for _, c := range utf16.Encode([]rune(value)) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || pattern && (c == '*' || c == '?') {
			out.WriteByte(byte(c))
		} else {
			out.WriteByte('_')
		}
	}
	return out.String()
}
