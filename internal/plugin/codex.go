// Package plugin materializes source-native plugins without rewriting their
// executable definitions or using AIPack's portable content parsers.
package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

const ConverterVersion = 20
const CodexLegacy = "codex-legacy"

// ReadCodex reads the root or legacy manifest contract of Codex CLI 0.159.2. Unknown
// fields remain in the upstream payload; they are not approximated as pack fields.
func ReadCodex(root, marketplace string) (config.PackManifest, error) {
	if _, err := os.Stat(filepath.Join(root, "plugin.json")); err == nil {
		return ReadAgentPlugin(root, marketplace)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return config.PackManifest{}, err
	}
	path := ".codex-plugin/plugin.json"
	manifest, err := readObject(root, path)
	if err != nil {
		return config.PackManifest{}, err
	}
	var name, version string
	if err := json.Unmarshal(manifest["name"], &name); err != nil || !domain.ValidNativeName(name) {
		return config.PackManifest{}, fmt.Errorf("native plugin name must be a valid plugin identifier")
	}
	if !domain.ValidNativeName(marketplace) {
		return config.PackManifest{}, fmt.Errorf("native marketplace name must be a valid marketplace identifier")
	}
	if raw := manifest["version"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &version); err != nil {
			return config.PackManifest{}, fmt.Errorf("plugin version: %w", err)
		}
	}
	p := &domain.NativePlugin{Format: CodexLegacy, Harness: domain.HarnessCodex,
		Name: name, Marketplace: marketplace, Manifest: path, ConverterVersion: ConverterVersion,
		Components: map[domain.PackCategory]map[string][]string{}, HookEvents: map[string]string{}}
	for _, cat := range []domain.PackCategory{domain.CategorySkills, domain.CategoryWorkflows, domain.CategoryHooks, domain.CategoryMCP} {
		p.Components[cat] = map[string][]string{}
	}
	skillRoots, err := pathList(manifest["skills"], []string{"skills"})
	if err != nil {
		return config.PackManifest{}, fmt.Errorf("skills: %w", err)
	}
	for _, skillRoot := range skillRoots {
		if err := discoverCodexSkills(root, skillRoot, false, p.Components[domain.CategorySkills]); err != nil {
			return config.PackManifest{}, err
		}
	}
	// Codex migrates legacy commands to skills when its installer runs. Keep
	// authored command IDs selectable before that migration.
	commandRoots, err := pathList(manifest["commands"], []string{"commands"})
	if err != nil {
		return config.PackManifest{}, fmt.Errorf("commands: %w", err)
	}
	for _, dir := range commandRoots {
		if err := discoverCommands(root, dir, p.Components[domain.CategoryWorkflows]); err != nil {
			return config.PackManifest{}, err
		}
	}
	if err := visitHooks(root, manifest, func(source string, obj map[string]json.RawMessage) error {
		if len(obj["hooks"]) == 0 {
			return nil
		}
		var events map[string]json.RawMessage
		if err := json.Unmarshal(obj["hooks"], &events); err != nil {
			return fmt.Errorf("%s hooks: %w", source, err)
		}
		for event, groups := range events {
			var handlers []json.RawMessage
			if err := json.Unmarshal(groups, &handlers); err != nil {
				return fmt.Errorf("%s event %s: %w", source, event, err)
			}
			if len(handlers) == 0 {
				continue
			}
			id := hookID("codex", event)
			if previous, ok := p.HookEvents[id]; ok && previous != event {
				return fmt.Errorf("native hook events %q and %q normalize to selector %q", previous, event, id)
			}
			p.HookEvents[id] = event
			p.Components[domain.CategoryHooks][id] = append(p.Components[domain.CategoryHooks][id], source)
		}
		return nil
	}); err != nil {
		return config.PackManifest{}, err
	}
	if err := visitMCP(root, manifest, func(source string, obj map[string]json.RawMessage) error {
		servers, err := mcpServers(obj)
		if err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		for id := range servers {
			if id == "" {
				return fmt.Errorf("%s: empty MCP server ID", source)
			}
			p.Components[domain.CategoryMCP][id] = []string{source}
		}
		return nil
	}); err != nil {
		return config.PackManifest{}, err
	}
	appPath := ".app.json"
	if raw := manifest["apps"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &appPath); err != nil {
			return config.PackManifest{}, fmt.Errorf("native apps must reference one configuration file")
		}
	}
	if appPath != "" {
		obj, err := readObject(root, appPath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return config.PackManifest{}, fmt.Errorf("native apps: %w", err)
		}
		if err == nil {
			var apps map[string]json.RawMessage
			if raw := obj["apps"]; len(raw) > 0 {
				if err := json.Unmarshal(raw, &apps); err != nil {
					return config.PackManifest{}, fmt.Errorf("native apps: %w", err)
				}
			}
			p.SettingsFiles = []string{filepath.ToSlash(filepath.Clean(appPath))}
		}
	}
	for _, entries := range p.Components {
		for id, paths := range entries {
			slices.Sort(paths)
			entries[id] = slices.Compact(paths)
		}
	}
	return config.PackManifest{SchemaVersion: config.PackSchemaVersion, Name: name, Version: version, Root: ".",
		NativePlugin: p, Skills: slices.Sorted(maps.Keys(p.Components[domain.CategorySkills])),
		Workflows: slices.Sorted(maps.Keys(p.Components[domain.CategoryWorkflows])),
		Hooks:     slices.Sorted(maps.Keys(p.Components[domain.CategoryHooks])),
		MCP:       slices.Sorted(maps.Keys(p.Components[domain.CategoryMCP]))}, nil
}

func hookID(prefix, event string) string {
	var out strings.Builder
	out.WriteString(prefix + "-")
	for i, r := range event {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				out.WriteByte('-')
			}
			out.WriteRune(r + ('a' - 'A'))
		} else if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			out.WriteRune(r)
		} else {
			out.WriteByte('-')
		}
	}
	return strings.TrimRight(out.String(), "-")
}

func safePath(root, rel string) (string, error) {
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("plugin path %q must stay within the package", rel)
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	real, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	r, err := filepath.Rel(realRoot, real)
	if err != nil || !filepath.IsLocal(r) {
		return "", fmt.Errorf("plugin path %q escapes the package", rel)
	}
	return path, nil
}

func readObject(root, rel string) (map[string]json.RawMessage, error) {
	path, err := safePath(root, rel)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	if obj == nil {
		return nil, fmt.Errorf("%s must contain a JSON object", rel)
	}
	return obj, nil
}

func pathList(raw json.RawMessage, defaults []string) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return defaults, nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, fmt.Errorf("expected a path or path array")
	}
	if len(many) == 0 {
		return defaults, nil
	}
	return many, nil
}

func discoverSkills(root, rel string, out map[string][]string) error {
	path, err := safePath(root, rel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	ids, paths, err := config.DiscoverEntryDirs(path, rel, domain.SkillEntryFile, "native skill")
	if err != nil {
		return err
	}
	for _, id := range ids {
		if previous, ok := out[id]; ok {
			if len(previous) == 1 && filepath.Clean(previous[0]) == filepath.Clean(paths[id]) {
				continue
			}
			return fmt.Errorf("duplicate native skill ID %q", id)
		}
		if _, err := safePath(root, paths[id]); err != nil {
			return err
		}
		out[id] = []string{paths[id]}
	}
	return nil
}

func discoverCommands(root, rel string, out map[string][]string) error {
	path, err := safePath(root, rel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	ids, paths, err := config.DiscoverIDsByLeaf(path, rel, ".md")
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, ok := out[id]; ok {
			return fmt.Errorf("duplicate native command ID %q", id)
		}
		out[id] = []string{paths[id]}
	}
	return nil
}

// visitHooks follows Codex's explicit-declaration precedence, including inline
// objects and lists. The callback sees whole event groups, never flattened handlers.
func visitHooks(root string, manifest map[string]json.RawMessage, visit func(string, map[string]json.RawMessage) error) error {
	raw := manifest["hooks"]
	if len(raw) == 0 || string(raw) == "null" {
		obj, err := readObject(root, "hooks/hooks.json")
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return visit("hooks/hooks.json", obj)
	}
	var one map[string]json.RawMessage
	if json.Unmarshal(raw, &one) == nil && one != nil {
		return visit(".codex-plugin/plugin.json", one)
	}
	var inline []map[string]json.RawMessage
	if json.Unmarshal(raw, &inline) == nil {
		for _, obj := range inline {
			if err := visit(".codex-plugin/plugin.json", obj); err != nil {
				return err
			}
		}
		return nil
	}
	paths, err := pathList(raw, nil)
	if err != nil {
		return fmt.Errorf("hooks: %w", err)
	}
	for _, path := range paths {
		obj, err := readObject(root, path)
		if err != nil {
			return err
		}
		if err := visit(path, obj); err != nil {
			return err
		}
	}
	return nil
}

func visitMCP(root string, manifest map[string]json.RawMessage, visit func(string, map[string]json.RawMessage) error) error {
	raw := manifest["mcpServers"]
	if len(raw) > 0 && string(raw) != "null" {
		var inline map[string]json.RawMessage
		if json.Unmarshal(raw, &inline) == nil && inline != nil {
			return visit(".codex-plugin/plugin.json", inline)
		}
		var path string
		if err := json.Unmarshal(raw, &path); err != nil {
			return fmt.Errorf("mcpServers must be a path or object")
		}
		obj, err := readObject(root, path)
		if err != nil {
			return err
		}
		return visit(path, obj)
	}
	obj, err := readObject(root, ".mcp.json")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return visit(".mcp.json", obj)
}

func mcpServers(obj map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if raw, wrapped := obj["mcpServers"]; wrapped {
		var servers map[string]json.RawMessage
		if err := json.Unmarshal(raw, &servers); err != nil || servers == nil {
			return nil, fmt.Errorf("mcpServers must be an object")
		}
		return servers, nil
	}
	return obj, nil
}
