package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
)

// CodexClaudeIssues describes selected capabilities without silently dropping
// native behavior. Unsupported fields remain explicit.
func CodexClaudeIssues(selection domain.NativePluginSelection) []string {
	return Compatibility(selection, domain.HarnessClaudeCode).Unsupported
}

func codexClaudeSkillIssue(selection domain.NativePluginSelection, id string) string {
	paths := selection.Package.Components[domain.CategorySkills][id]
	if len(paths) != 1 || !filepath.IsLocal(paths[0]) || filepath.Base(paths[0]) != domain.SkillEntryFile || filepath.Base(filepath.Dir(paths[0])) != id || !domain.ValidNativeName(id) || claudeSkillName(id) != id {
		return "requires one matching skill directory"
	}
	names := map[string][]string{}
	loaded, err := addClaudeSingleSkill(filepath.Join(selection.Root, "upstream"), filepath.Dir(paths[0]), id, names)
	if err != nil || !loaded || names[id] == nil {
		return "Claude skill identity differs or its entrypoint cannot be read"
	}
	return ""
}

var codexClaudeVariables = regexp.MustCompile(`\$(?:\{(?:CODEX_)?PLUGIN_(?:ROOT|DATA)\}|(?:CODEX_)?PLUGIN_(?:ROOT|DATA)\b)|<plugin-root>`)

func translateCodexForClaude(value string) string {
	return codexClaudeVariables.ReplaceAllStringFunc(value, func(variable string) string {
		if strings.Contains(variable, "DATA") {
			return "${CLAUDE_PLUGIN_DATA}"
		}
		return "${CLAUDE_PLUGIN_ROOT}/payload"
	})
}

// RenderCodexForClaude retains the entire source tree beneath payload/. Claude
// loads only explicit skill directories; source default hooks, agents, commands
// and excluded skills remain assets, never automatic activation entrypoints.
func RenderCodexForClaude(selection domain.NativePluginSelection) ([]File, domain.NativePlugin, error) {
	pkg := selection.Package
	if pkg.Harness != domain.HarnessCodex || (pkg.Format != CodexLegacy && pkg.Format != AgentPlugins) || pkg.ConverterVersion != ConverterVersion {
		return nil, pkg, fmt.Errorf("unsupported Codex source format/converter: %s/%d", pkg.Format, pkg.ConverterVersion)
	}
	if issues := CodexClaudeIssues(selection); len(issues) > 0 {
		return nil, pkg, fmt.Errorf("unsupported selected components: %s", strings.Join(issues, ", "))
	}
	root := filepath.Join(selection.Root, "upstream")
	manifest, err := readObject(root, pkg.Manifest)
	if err != nil {
		return nil, pkg, err
	}
	files, err := ReadFiles(root)
	if err != nil {
		return nil, pkg, err
	}
	skillDirs := []string{}
	components := map[string][]string{}
	for _, id := range selection.Selected[domain.CategorySkills] {
		path := selection.Package.Components[domain.CategorySkills][id][0]
		skillDirs = append(skillDirs, "./payload/"+filepath.ToSlash(filepath.Dir(path)))
		components[id] = []string{"payload/" + path}
	}
	slices.Sort(skillDirs)
	byPath := map[string]File{}
	indices := map[string]int{}
	for i, file := range files {
		byPath[file.Path] = file
		indices[file.Path] = i
	}
	for _, paths := range components {
		path := strings.TrimPrefix(paths[0], "payload/")
		if _, exists := byPath[path]; !exists {
			return nil, pkg, fmt.Errorf("portable skill %s beneath a directory alias is not supported", path)
		}
		file, err := ResolvePayloadFile(byPath, path)
		if err != nil || !file.Mode.IsRegular() {
			return nil, pkg, fmt.Errorf("portable skill %s must resolve to a file: %v", path, err)
		}
		// Materialize only the selected entrypoint; its relative asset layout stays intact.
		file.Path, file.Link = path, ""
		_, body, err := domain.SplitFrontmatter(file.Content)
		if err != nil {
			return nil, pkg, fmt.Errorf("portable skill %s: %w", path, err)
		}
		// Translate instructions, never frontmatter identity or trigger metadata.
		translated := translateCodexForClaude(string(body))
		file.Content = []byte(string(file.Content[:len(file.Content)-len(body)]) + translated)
		files[indices[path]] = file
	}
	for i := range files {
		files[i].Path = "payload/" + files[i].Path
	}
	// A new root prevents native default discovery from activating source assets.
	files = append([]File{{Path: "payload", Mode: fs.ModeDir | 0o755}, {Path: ".claude-plugin", Mode: fs.ModeDir | 0o755}}, files...)
	nativeManifest := map[string]any{"name": pkg.Name, "skills": skillDirs, "commands": map[string]any{}, "agents": []string{}, "hooks": map[string]any{}, "mcpServers": map[string]any{}}
	mcp := map[string]any{}
	mcpComponents := map[string][]string{}
	for _, id := range selection.Selected[domain.CategoryMCP] {
		entry, err := codexClaudeMCP(selection, id)
		if err != nil {
			return nil, pkg, fmt.Errorf("portable MCP %s: %w", id, err)
		}
		mcp[id] = entry
		mcpComponents[id] = []string{claudeManifest}
	}
	nativeManifest["mcpServers"] = mcp
	hookGroups := map[string][]map[string]any{}
	hookComponents := map[string][]string{}
	for _, id := range selection.Selected[domain.CategoryHooks] {
		groups, err := codexClaudeHookGroups(selection, id)
		if errors.Is(err, errHookUnavailable) {
			continue
		}
		if err != nil {
			return nil, pkg, fmt.Errorf("portable hook %s: %w", id, err)
		}
		hookGroups[pkg.HookEvents[id]] = append(hookGroups[pkg.HookEvents[id]], groups...)
		hookComponents[id] = []string{claudeManifest}
	}
	// Inline Claude declarations are event maps, without a second hooks envelope.
	nativeManifest["hooks"] = hookGroups
	for _, field := range []string{"version", "description", "author", "homepage", "repository", "license", "keywords"} {
		if raw, exists := manifest[field]; exists {
			nativeManifest[field] = raw
		}
	}
	body, err := json.MarshalIndent(nativeManifest, "", "  ")
	if err != nil {
		return nil, pkg, err
	}
	files = append(files, File{Path: claudeManifest, Content: append(body, '\n'), Mode: 0o644})
	pkg.Harness, pkg.Format, pkg.Manifest = domain.HarnessClaudeCode, Claude, claudeManifest
	pkg.Components = map[domain.PackCategory]map[string][]string{domain.CategorySkills: components}
	if len(mcpComponents) > 0 {
		pkg.Components[domain.CategoryMCP] = mcpComponents
	}
	if len(hookComponents) > 0 {
		pkg.Components[domain.CategoryHooks] = hookComponents
	}
	if len(hookComponents) == 0 {
		pkg.HookEvents = nil
	}
	pkg.SettingsFiles, pkg.CatalogCommandOrder = nil, nil
	pkg.CacheVersion, pkg.RootDirectoryName, pkg.CopiedSource = "", "", false
	pkg.MarketplaceEntry = map[string]any{"name": pkg.Name, "source": "./plugins/" + pkg.Name}
	// This catalog is AIPack's generated delivery view, not the upstream catalog.
	pkg.MarketplaceMetadata = map[string]any{"owner": map[string]any{"name": "AIPack"}}
	return files, pkg, nil
}
