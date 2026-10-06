package plugin

import (
	"bytes"
	"cmp"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

const Claude = "claude"
const claudeManifest = ".claude-plugin/plugin.json"

// The marketplace name identifies installation; the payload manifest name
// identifies native components. Catalog-only packages use the entry name.
func ClaudeNamespace(selection domain.NativePluginSelection) (string, error) {
	if selection.Package.Harness == domain.HarnessCodex {
		if !domain.ValidNativeName(selection.Package.Name) {
			return "", fmt.Errorf("invalid native Claude component namespace")
		}
		return selection.Package.Name, nil
	}
	if selection.Package.Manifest == "" {
		return selection.Package.Name, nil
	}
	manifest, err := readObject(filepath.Join(selection.Root, "upstream"), claudeManifest)
	if err != nil {
		return "", err
	}
	var name string
	if err := json.Unmarshal(manifest["name"], &name); err != nil || !domain.ValidNativeName(name) {
		return "", fmt.Errorf("invalid native Claude component namespace")
	}
	return name, nil
}

// ReadClaude records the component loading contract of Claude Code 2.1.284.
// Executable definitions and native-only fields remain in the complete payload.
func ReadClaude(root string, spec domain.PluginSource) (config.PackManifest, error) {
	marketplace, entry := spec.Marketplace, spec.Entry
	manifest, err := readObject(root, claudeManifest)
	manifestPath := claudeManifest
	if errors.Is(err, fs.ErrNotExist) {
		manifest, manifestPath = map[string]json.RawMessage{}, ""
	} else if err != nil {
		return config.PackManifest{}, err
	}
	catalog, err := claudeCatalogFields(entry, spec.CatalogCommandOrder)
	if err != nil {
		return config.PackManifest{}, err
	}
	metadata := manifest
	if manifestPath == "" {
		metadata = catalog
	}
	var name, version string
	if err := json.Unmarshal(metadata["name"], &name); err != nil || !domain.ValidNativeName(name) || !domain.ValidNativeName(marketplace) {
		return config.PackManifest{}, fmt.Errorf("invalid native Claude plugin or marketplace identity")
	}
	rawVersion := manifest["version"]
	if len(rawVersion) == 0 {
		rawVersion = catalog["version"]
	}
	if len(rawVersion) > 0 {
		if err := json.Unmarshal(rawVersion, &version); err != nil {
			return config.PackManifest{}, fmt.Errorf("plugin version: %w", err)
		}
	}
	p := &domain.NativePlugin{Format: Claude, Harness: domain.HarnessClaudeCode, Name: name, Marketplace: marketplace,
		Manifest: manifestPath, ConverterVersion: ConverterVersion, MarketplaceEntry: maps.Clone(entry),
		Components: map[domain.PackCategory]map[string][]string{}, HookEvents: map[string]string{}}
	if raw := bytes.TrimSpace(catalog["commands"]); bytes.HasPrefix(raw, []byte("{")) {
		p.CatalogCommandOrder, err = util.JSONPropertyNames(raw)
		if err != nil {
			return config.PackManifest{}, err
		}
	}
	gitMarketplace := config.RegistrySourceUsesGit(config.RegistrySourceEntry{URL: spec.MarketplaceURL, Ref: spec.MarketplaceRef, Path: spec.MarketplacePath})
	p.CopiedSource = gitMarketplace
	npm := spec.NPM != nil
	if source, ok := entry["source"].(map[string]any); ok {
		npm = npm || source["source"] == "npm"
		p.CopiedSource = p.CopiedSource || source["source"] == "url" || source["source"] == "git-subdir" || source["source"] == "github"
	}
	p.CopiedSource = p.CopiedSource || npm
	if spec.MarketplaceURL != "" && !gitMarketplace {
		p.RootDirectoryName = filepath.Base(filepath.Clean(spec.MarketplaceURL))
		if !domain.ValidNativeRootName(p.RootDirectoryName) {
			return config.PackManifest{}, fmt.Errorf("invalid native local marketplace directory name")
		}
	}
	if npm && version == "" {
		p.CacheVersion = "unknown"
	} else if p.CopiedSource && version == "" {
		if _, err := hex.DecodeString(spec.SourceRevision); err != nil || (len(spec.SourceRevision) != 40 && len(spec.SourceRevision) != 64) {
			return config.PackManifest{}, fmt.Errorf("native copied Git version requires the acquired source revision")
		}
		p.CacheVersion = strings.ToLower(spec.SourceRevision[:12])
		if source, ok := entry["source"].(map[string]any); ok && source["source"] == "git-subdir" {
			path, ok := source["path"].(string)
			if !ok || !filepath.IsLocal(path) {
				return config.PackManifest{}, fmt.Errorf("native Git subdirectory version requires a local source path")
			}
			p.CacheVersion += "-" + util.ContentDigest([]byte(filepath.Clean(path)))[:8]
		}
	}
	for _, cat := range []domain.PackCategory{domain.CategorySkills, domain.CategoryAgents, domain.CategoryWorkflows, domain.CategoryHooks, domain.CategoryMCP} {
		p.Components[cat] = map[string][]string{}
	}
	// The host appends catalog component declarations when strict is true.
	// With strict=false, any catalog component declaration conflicts with a manifest.
	for _, key := range []string{"skills", "commands", "agents", "hooks", "outputStyles", "themes"} {
		if _, declared := entry[key]; declared && entry["strict"] == false && manifestPath != "" {
			return config.PackManifest{}, fmt.Errorf("native Claude plugin has conflicting manifest and strict=false catalog components")
		}
	}
	rootName := filepath.Base(root)
	if source, ok := entry["source"].(string); ok {
		rootName = filepath.Base(source)
		if filepath.Clean(source) == "." {
			if !p.CopiedSource && p.RootDirectoryName == "" {
				p.RootDirectoryName = filepath.Base(root)
			}
			rootName = p.RootDirectoryName
		}
	}
	if p.CopiedSource {
		rootName = version
		if rootName == "" {
			rootName = p.CacheVersion
		}
	}
	if !claudeCatalogRootSkills(entry, catalog) {
		if err := discoverSkills(root, "skills", p.Components[domain.CategorySkills]); err != nil {
			return config.PackManifest{}, err
		}
	}
	if _, err := os.Stat(filepath.Join(root, "skills")); errors.Is(err, fs.ErrNotExist) && len(manifest["skills"]) == 0 && len(catalog["skills"]) == 0 {
		_, err := addClaudeSingleSkill(root, ".", rootName, p.Components[domain.CategorySkills])
		if err != nil {
			return config.PackManifest{}, err
		}
	}
	for _, key := range []string{"skills", "commands", "agents"} {
		raw := bytes.TrimSpace(manifest[key])
		if key != "skills" && (len(raw) == 0 || bytes.Equal(raw, []byte("null"))) {
			cat := domain.CategoryWorkflows
			if key == "agents" {
				cat = domain.CategoryAgents
			}
			if err := discoverClaudeFiles(root, key, key, p.Components[cat]); err != nil {
				return config.PackManifest{}, err
			}
		}
		for i, raw := range []json.RawMessage{manifest[key], catalog[key]} {
			if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				continue
			}
			source := manifestPath
			if i == 1 {
				source = ""
			}
			if err := claudeContent(root, key, raw, p.Components, source, rootName); err != nil {
				return config.PackManifest{}, fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	readHooks := func(source string, obj map[string]json.RawMessage, inline bool) error {
		var events map[string]json.RawMessage
		if inline {
			events = obj
		} else {
			if err := json.Unmarshal(obj["hooks"], &events); err != nil || events == nil {
				return fmt.Errorf("%s must wrap its events in hooks", source)
			}
		}
		for event, raw := range events {
			var groups []json.RawMessage
			if err := json.Unmarshal(raw, &groups); err != nil {
				return fmt.Errorf("%s hook event %s: %w", source, event, err)
			}
			if len(groups) == 0 {
				continue
			}
			id := hookID("claude", event)
			if prior := p.HookEvents[id]; prior != "" && prior != event {
				return fmt.Errorf("native hook events normalize to the same selector %q", id)
			}
			p.HookEvents[id] = event
			p.Components[domain.CategoryHooks][id] = append(p.Components[domain.CategoryHooks][id], source)
		}
		return nil
	}
	hookFallback := "hooks/hooks.json"
	if manifestPath == "" && claudeCatalogHasHooks(catalog) {
		hookFallback = ""
	}
	if err := visitClaudeSources(root, manifest, "hooks", hookFallback, manifestPath, readHooks, nil); err != nil {
		return config.PackManifest{}, err
	}
	if raw := bytes.TrimSpace(catalog["hooks"]); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if raw[0] != '{' {
			return config.PackManifest{}, fmt.Errorf("native Claude catalog hooks must be inline objects; paths and arrays are not supported by the host")
		}
		if err := visitClaudeSources(root, catalog, "hooks", "", "", readHooks, nil); err != nil {
			return config.PackManifest{}, err
		}
	}
	mcpFields, mcpSource := manifest, manifestPath
	if manifestPath == "" {
		mcpFields, mcpSource = catalog, ""
	}
	if err := visitClaudeSources(root, mcpFields, "mcpServers", ".mcp.json", mcpSource, func(source string, obj map[string]json.RawMessage, _ bool) error {
		servers, err := mcpServers(obj)
		if err != nil {
			return err
		}
		for id := range servers {
			if id == "" {
				return fmt.Errorf("empty native MCP server ID")
			}
			p.Components[domain.CategoryMCP][id] = []string{source}
		}
		return nil
	}, nil); err != nil {
		return config.PackManifest{}, err
	}
	if path, err := safePath(root, "settings.json"); err != nil {
		return config.PackManifest{}, err
	} else if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
		p.SettingsFiles = []string{"settings.json"}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return config.PackManifest{}, err
	}
	skills := map[string][]string{}
	for id, paths := range p.Components[domain.CategorySkills] {
		id = claudeSkillName(id)
		skills[id] = append(skills[id], paths...)
	}
	p.Components[domain.CategorySkills] = skills
	for cat, entries := range p.Components {
		if cat == domain.CategoryWorkflows {
			// File commands load before payload inline bodies, including files
			// appended by the catalog. Preserve declaration order within each.
			inline := func(path string) int {
				if path == "" || path == claudeManifest {
					return 1
				}
				return 0
			}
			for _, paths := range entries {
				slices.SortStableFunc(paths, func(a, b string) int { return cmp.Compare(inline(a), inline(b)) })
			}
			continue
		}
		for id, paths := range entries {
			slices.Sort(paths)
			entries[id] = slices.Compact(paths)
		}
	}
	return config.PackManifest{SchemaVersion: config.PackSchemaVersion, Name: name, Version: version, Root: ".", NativePlugin: p,
		Skills: slices.Sorted(maps.Keys(p.Components[domain.CategorySkills])), Agents: slices.Sorted(maps.Keys(p.Components[domain.CategoryAgents])),
		Workflows: slices.Sorted(maps.Keys(p.Components[domain.CategoryWorkflows])), Hooks: slices.Sorted(maps.Keys(p.Components[domain.CategoryHooks])),
		MCP: slices.Sorted(maps.Keys(p.Components[domain.CategoryMCP]))}, nil
}

// Native name normalization operates on JavaScript UTF-16 code units.
func claudeSkillName(name string) string {
	var out strings.Builder
	for _, char := range utf16.Encode([]rune(name)) {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			out.WriteByte(byte(char))
		} else {
			out.WriteByte('-')
		}
	}
	return out.String()
}

func claudeCatalogFields(entry map[string]any, commandOrder []string) (map[string]json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if entry == nil {
		return fields, nil
	}
	body, err := json.Marshal(entry)
	if err == nil {
		err = json.Unmarshal(body, &fields)
	}
	if err == nil && len(commandOrder) > 0 && bytes.HasPrefix(bytes.TrimSpace(fields["commands"]), []byte("{")) {
		fields["commands"], err = util.OrderJSONObject(fields["commands"], commandOrder)
	}
	return fields, err
}

// An empty catalog hooks object lets the host load its default hooks file.
func claudeCatalogHasHooks(fields map[string]json.RawMessage) bool {
	var hooks map[string]json.RawMessage
	_ = json.Unmarshal(fields["hooks"], &hooks)
	return len(hooks) > 0
}

func claudeCatalogRootSkills(entry map[string]any, fields map[string]json.RawMessage) bool {
	source, ok := entry["source"].(string)
	paths, err := pathList(fields["skills"], nil)
	return ok && filepath.Clean(source) == "." && err == nil && len(paths) > 0
}

// A directly addressed skill uses frontmatter identity; directory scans use
// child directory names. Root fallback identity follows the upstream source.
func addClaudeSingleSkill(root, rel, fallback string, out map[string][]string) (bool, error) {
	path, err := safePath(root, filepath.Join(rel, "SKILL.md"))
	if err != nil {
		return false, err
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	fm, _, err := domain.SplitFrontmatter(body)
	if err != nil {
		return false, err
	}
	var metadata map[string]any
	_ = yaml.Unmarshal(fm, &metadata)
	if name, ok := metadata["name"].(string); ok && name != "" {
		fallback = name
	}
	if fallback == "" {
		return false, fmt.Errorf("unnamed root skills require stable native source-directory identity, which is not available")
	}
	source := filepath.ToSlash(filepath.Join(rel, "SKILL.md"))
	if previous, exists := out[fallback]; exists && (len(previous) != 1 || previous[0] != source) {
		return false, fmt.Errorf("duplicate native skill ID %q", fallback)
	}
	out[fallback] = []string{source}
	return true, nil
}

func claudeContent(root, key string, raw json.RawMessage, components map[domain.PackCategory]map[string][]string, source, rootName string) error {
	cat := domain.CategoryWorkflows
	if key == "skills" {
		cat = domain.CategorySkills
	} else if key == "agents" {
		cat = domain.CategoryAgents
	}
	if key == "commands" && bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		var commands map[string]map[string]json.RawMessage
		if err := json.Unmarshal(raw, &commands); err != nil {
			return err
		}
		ids, err := claudeCommandIDs(raw)
		if err != nil {
			return err
		}
		for _, id := range ids {
			command := commands[id]
			// Catalog command objects load file sources, but ignore inline bodies.
			if source == "" && len(command["source"]) == 0 {
				continue
			}
			var path string
			if (len(command["source"]) > 0) == (len(command["content"]) > 0) {
				return fmt.Errorf("command %q requires exactly one of source or content", id)
			}
			if raw := command["source"]; len(raw) > 0 {
				if err := json.Unmarshal(raw, &path); err != nil {
					return err
				}
				if _, err := claudePath(root, path); err != nil {
					return err
				}
			} else {
				path = source
			}
			if path != "" {
				path = filepath.ToSlash(filepath.Clean(path))
			}
			if len(command["source"]) > 0 && claudeCommandSourceSeen(components[cat], path) {
				continue
			}
			components[cat][id] = append(components[cat][id], path)
		}
		return nil
	}
	paths, err := pathList(raw, nil)
	if err != nil {
		return err
	}
	for _, rel := range paths {
		var path string
		if key == "skills" && (rel == "." || rel == "./") {
			path = root
		} else if path, err = claudePath(root, rel); err != nil {
			return err
		}
		if key == "skills" {
			fallback := filepath.Base(path)
			if filepath.Clean(rel) == "." {
				fallback = rootName
			}
			single, err := addClaudeSingleSkill(root, rel, fallback, components[cat])
			if err != nil {
				return err
			}
			if !single {
				if err := discoverSkills(root, rel, components[cat]); err != nil {
					return err
				}
			}
			continue
		}
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		if st.IsDir() {
			if key == "agents" {
				return fmt.Errorf("agents must name .md files, not directories")
			}
			if err := discoverClaudeFiles(root, rel, key, components[cat]); err != nil {
				return err
			}
			continue
		}
		if filepath.Ext(path) != ".md" {
			return fmt.Errorf("%s path must name a .md file", key)
		}
		id := strings.TrimSuffix(filepath.Base(path), ".md")
		if err := addClaudeFile(root, rel, key, id, components[cat]); err != nil {
			return err
		}
	}
	return nil
}

func discoverClaudeFiles(root, rel, key string, out map[string][]string) error {
	path, err := safePath(root, rel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	return filepath.WalkDir(path, func(file string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 || strings.ToLower(filepath.Ext(file)) != ".md" {
			return err
		}
		local, err := filepath.Rel(path, file)
		if err != nil {
			return err
		}
		id := strings.TrimSuffix(filepath.ToSlash(local), filepath.Ext(local))
		return addClaudeFile(root, filepath.Join(rel, local), key, id, out)
	})
}

func addClaudeFile(root, rel, key, id string, out map[string][]string) error {
	path, err := safePath(root, rel)
	if err != nil {
		return err
	}
	base := filepath.Base(id)
	prefix := strings.ReplaceAll(strings.TrimSuffix(id, base), "/", ":")
	id = prefix + base
	if key == "agents" {
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fm, _, _ := domain.SplitFrontmatter(body)
		var metadata map[string]any
		if yaml.Unmarshal(fm, &metadata) == nil {
			if name, ok := metadata["name"].(string); ok && name != "" {
				id = prefix + name
			}
		}
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if !slices.Contains(out[id], rel) {
		out[id] = append(out[id], rel)
	}
	return nil
}

func claudeCommandSourceSeen(commands map[string][]string, path string) bool {
	for _, paths := range commands {
		if slices.Contains(paths, path) {
			return true
		}
	}
	return false
}

// Object.entries visits array-index keys first, then other keys in their
// original insertion order. Duplicate JSON keys retain their first position.
func claudeCommandIDs(raw json.RawMessage) ([]string, error) {
	ids, err := util.JSONPropertyNames(raw)
	if err != nil {
		return nil, err
	}
	index := func(id string) uint64 {
		n, err := strconv.ParseUint(id, 10, 32)
		if err != nil || strconv.FormatUint(n, 10) != id || n == 1<<32-1 {
			return 1 << 32
		}
		return n
	}
	slices.SortStableFunc(ids, func(a, b string) int {
		return cmp.Compare(index(a), index(b))
	})
	return ids, nil
}

func claudePath(root, rel string) (string, error) {
	if !strings.HasPrefix(rel, "./") || strings.Contains(rel, "\\") || slices.Contains(strings.Split(rel, "/"), "..") {
		return "", fmt.Errorf("native Claude component path %q must start with ./ and stay within the plugin", rel)
	}
	path, err := safePath(root, rel)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}

// Claude loads default configuration first, then mixed paths/inline objects.
func visitClaudeSources(root string, manifest map[string]json.RawMessage, key, fallback, source string, visit func(string, map[string]json.RawMessage, bool) error, changed map[string][]byte) error {
	if fallback != "" {
		if obj, err := readObject(root, fallback); err == nil {
			if err := visit(fallback, obj, false); err != nil {
				return err
			}
			if changed != nil {
				if err := putJSON(changed, fallback, obj); err != nil {
					return err
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	raw := bytes.TrimSpace(manifest[key])
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	values := []json.RawMessage{raw}
	array := raw[0] == '['
	if array {
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
	}
	for i, value := range values {
		var rel string
		if json.Unmarshal(value, &rel) == nil {
			if strings.HasSuffix(rel, ".mcpb") || strings.HasSuffix(rel, ".dxt") {
				return fmt.Errorf("native Claude MCP bundle inventory is not implemented yet")
			}
			if _, err := claudePath(root, rel); err != nil {
				return err
			}
			obj, err := readObject(root, rel)
			if err != nil {
				return err
			}
			if err := visit(filepath.ToSlash(filepath.Clean(rel)), obj, false); err != nil {
				return err
			}
			if changed != nil {
				if err := putJSON(changed, rel, obj); err != nil {
					return err
				}
			}
		} else {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(value, &obj); err != nil || obj == nil {
				return fmt.Errorf("native configuration must be a path or object")
			}
			if err := visit(source, obj, true); err != nil {
				return err
			}
			if changed != nil {
				var err error
				values[i], err = json.Marshal(obj)
				if err != nil {
					return err
				}
			}
		}
	}
	if changed != nil {
		if array {
			return setJSON(manifest, key, values)
		}
		manifest[key] = values[0]
	}
	return nil
}
