package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

func RenderClaude(selection domain.NativePluginSelection) ([]File, error) {
	files, _, err := RenderClaudePackage(selection)
	return files, err
}

// RenderClaudePackage filters both package files and the catalog declarations
// that the native host loads. The installed pack's catalog remains unchanged.
func RenderClaudePackage(selection domain.NativePluginSelection) ([]File, map[string]any, error) {
	return renderClaudePackage(selection, nil)
}

func renderClaudePackage(selection domain.NativePluginSelection, ignoreDefaults map[string]bool) ([]File, map[string]any, error) {
	catalog, err := claudeCatalogFields(selection.Package.MarketplaceEntry, selection.Package.CatalogCommandOrder)
	if err != nil {
		return nil, nil, err
	}
	files, err := renderClaude(selection, catalog, ignoreDefaults)
	if err != nil {
		return nil, nil, err
	}
	if selection.Package.CacheVersion != "" {
		if err := setJSON(catalog, "version", selection.Package.CacheVersion); err != nil {
			return nil, nil, err
		}
	}
	body, err := json.Marshal(catalog)
	var entry map[string]any
	if err == nil {
		err = util.UnmarshalJSON(body, &entry)
	}
	if err == nil && len(selection.Package.CatalogCommandOrder) > 0 {
		// Keep the ordered object at the wire boundary; ordinary maps in the
		// persisted descriptor use CatalogCommandOrder to reconstruct it.
		entry["commands"] = catalog["commands"]
	}
	return files, entry, err
}

// CatalogEntries retains command-object order when an existing catalog
// is rewritten for another entry. Other fields keep their ordinary JSON shape.
func CatalogEntries(body []byte) ([]map[string]any, error) {
	var catalog map[string]json.RawMessage
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, err
	}
	var rawEntries []map[string]json.RawMessage
	if err := json.Unmarshal(catalog["plugins"], &rawEntries); err != nil {
		return nil, err
	}
	if rawEntries == nil {
		return nil, nil
	}
	entries := make([]map[string]any, 0, len(rawEntries))
	for _, fields := range rawEntries {
		raw, err := json.Marshal(fields)
		var entry map[string]any
		if err == nil {
			err = util.UnmarshalJSON(raw, &entry)
		}
		if err != nil {
			return nil, err
		}
		if commands := bytes.TrimSpace(fields["commands"]); bytes.HasPrefix(commands, []byte("{")) {
			order, err := util.JSONPropertyNames(commands)
			if err != nil {
				return nil, err
			}
			entry["commands"], err = util.OrderJSONObject(commands, order)
			if err != nil {
				return nil, err
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Catalog-backed root entries share source files in the native loader. Keep
// their union in one tree and select content through each catalog declaration.
func RenderClaudeSharedRoots(actions []domain.NativePluginAction) error {
	groups := map[string][]int{}
	for i, action := range actions {
		source, _ := action.Package.MarketplaceEntry["source"].(string)
		if !action.Package.CopiedSource && action.Package.Manifest == "" && source != "" && filepath.Clean(source) == "." {
			groups[action.MarketplaceDir] = append(groups[action.MarketplaceDir], i)
		}
	}
	for _, indices := range groups {
		if len(indices) < 2 {
			continue
		}
		ignoreDefaults := map[string]bool{}
		for _, i := range indices {
			selection := actions[i].Selection
			if selection == nil {
				continue
			}
			for key, cat := range map[string]domain.PackCategory{"commands": domain.CategoryWorkflows, "agents": domain.CategoryAgents} {
				defaults := map[string][]string{}
				if err := discoverClaudeFiles(filepath.Join(selection.Root, "upstream"), key, key, defaults); err != nil {
					return err
				}
				for id, paths := range defaults {
					if !selected(*selection, cat, id) {
						for _, path := range paths {
							ignoreDefaults[path] = true
						}
					}
				}
			}
		}
		if len(ignoreDefaults) > 0 {
			for _, i := range indices {
				action := &actions[i]
				if action.Selection == nil {
					return fmt.Errorf("shared Claude default selection requires its original source and profile choices")
				}
				selection := *action.Selection
				selection.Package.Components = maps.Clone(selection.Package.Components)
				selection.Package.Components[domain.CategoryAgents] = maps.Clone(selection.Package.Components[domain.CategoryAgents])
				files, entry, err := renderClaudePackage(selection, ignoreDefaults)
				if err != nil {
					return err
				}
				action.Files, action.Package.Components, action.Package.MarketplaceEntry = files, selection.Package.Components, entry
			}
		}
		const catalogPath = ".claude-plugin/marketplace.json"
		files := map[string]File{}
		var metadata map[string]any
		owned := map[string]bool{}
		for _, i := range indices {
			owned[actions[i].Package.Name] = true
		}
		catalogEntries := map[string]map[string]any{}
		var catalogOrder []string
		sameOrder, orderKnown := true, false
		baseBinding := ""
		for _, i := range indices {
			action := &actions[i]
			for _, file := range action.Files {
				if file.Path == ".aipack-marketplace" || strings.HasPrefix(file.Path, ".aipack-marketplace/") {
					return fmt.Errorf("shared Claude root conflicts with generated marketplace metadata")
				}
				if file.Path == catalogPath {
					var header map[string]any
					if err := util.UnmarshalJSON(file.Content, &header); err != nil {
						return err
					}
					entries, err := CatalogEntries(file.Content)
					if err != nil || entries == nil {
						return fmt.Errorf("shared Claude root requires a catalog plugins array")
					}
					var names []string
					seen := map[string]bool{}
					for _, entry := range entries {
						name, _ := entry["name"].(string)
						if !domain.ValidNativeName(name) || seen[name] {
							return fmt.Errorf("shared Claude root has an invalid or duplicate catalog entry %q", name)
						}
						if prior, exists := catalogEntries[name]; exists && !owned[name] && !reflect.DeepEqual(prior, entry) {
							return fmt.Errorf("shared Claude root has conflicting inactive catalog entry %q; update its packs to one catalog revision", name)
						}
						seen[name] = true
						names = append(names, name)
						catalogEntries[name] = entry
					}
					if !orderKnown {
						catalogOrder, orderKnown = names, true
					} else if !slices.Equal(catalogOrder, names) {
						sameOrder = false
					}
					delete(header, "plugins")
					if metadata != nil && !reflect.DeepEqual(metadata, header) {
						return fmt.Errorf("shared Claude root has conflicting marketplace metadata; update its packs to one catalog revision")
					}
					metadata = header
				}
				if prior, exists := files[file.Path]; exists && (prior.Mode != file.Mode || prior.Link != file.Link || (file.Path != catalogPath && !bytes.Equal(prior.Content, file.Content))) {
					return fmt.Errorf("shared Claude root has conflicting selected payload %s", file.Path)
				}
				if file.Path == catalogPath {
					if baseBinding != "" && action.Package.Binding() > baseBinding {
						continue
					}
					baseBinding = action.Package.Binding()
				}
				files[file.Path] = file
			}
		}
		for _, i := range indices {
			action := &actions[i]
			present := map[string]bool{}
			for _, file := range action.Files {
				present[file.Path] = true
			}
			fields, err := claudeCatalogFields(action.Package.MarketplaceEntry, action.Package.CatalogCommandOrder)
			if err != nil {
				return err
			}
			paths, err := pathList(fields["skills"], nil)
			if err != nil {
				return err
			}
			var kept []string
			for _, path := range paths {
				prefix := filepath.ToSlash(filepath.Clean(path))
				wanted := false
				for _, sources := range action.Package.Components[domain.CategorySkills] {
					for _, source := range sources {
						if (prefix == "." || strings.HasPrefix(source, prefix+"/")) && present[source] {
							wanted = true
							break
						}
					}
				}
				if wanted {
					kept = append(kept, path)
				}
			}
			if len(paths) > 0 && len(kept) != len(paths) {
				if len(kept) == 0 {
					kept = []string{"./.aipack-marketplace/empty-skills"}
					files[".aipack-marketplace"] = File{Path: ".aipack-marketplace", Mode: fs.ModeDir | 0o700}
					files[".aipack-marketplace/empty-skills"] = File{Path: ".aipack-marketplace/empty-skills", Mode: fs.ModeDir | 0o700}
				}
				action.Package.MarketplaceEntry = maps.Clone(action.Package.MarketplaceEntry)
				action.Package.MarketplaceEntry["skills"] = kept
			}
			for _, sources := range action.Package.Components[domain.CategorySkills] {
				for _, source := range sources {
					_, retained := files[source]
					if present[source] || !retained {
						continue
					}
					if len(paths) == 0 {
						return fmt.Errorf("shared Claude default skill %s needs independent activation support", source)
					}
					for _, path := range kept {
						prefix := filepath.ToSlash(filepath.Clean(path))
						if prefix == "." || strings.HasPrefix(source, prefix+"/") {
							return fmt.Errorf("shared Claude skill scan %s includes excluded source %s", path, source)
						}
					}
				}
			}
			for key, cat := range map[string]domain.PackCategory{"commands": domain.CategoryWorkflows, "agents": domain.CategoryAgents} {
				for _, sources := range action.Package.Components[cat] {
					for _, source := range sources {
						if _, retained := files[source]; !present[source] && retained && strings.HasPrefix(source, key+"/") {
							return fmt.Errorf("shared Claude default %s source %s needs independent activation support", key, source)
						}
					}
				}
			}
			action.SharedRoot = true
			action.Package.MarketplaceMetadata = maps.Clone(metadata)
		}
		original, exists := files[catalogPath]
		if !exists {
			return fmt.Errorf("shared Claude root is missing its canonical marketplace catalog")
		}
		catalog := map[string]json.RawMessage{}
		if err := json.Unmarshal(original.Content, &catalog); err != nil {
			return err
		}
		for _, i := range indices {
			name := actions[i].Package.Name
			catalogEntries[name] = actions[i].Package.MarketplaceEntry
			if !slices.Contains(catalogOrder, name) {
				sameOrder = false
			}
		}
		if !sameOrder {
			catalogOrder = slices.Sorted(maps.Keys(catalogEntries))
		}
		var entries []map[string]any
		for _, name := range catalogOrder {
			entries = append(entries, catalogEntries[name])
		}
		if err := setJSON(catalog, "plugins", entries); err != nil {
			return err
		}
		changed := map[string][]byte{}
		if err := putJSON(changed, catalogPath, catalog); err != nil {
			return err
		}
		merged, err := renderedFiles(slices.SortedFunc(maps.Values(files), func(a, b File) int { return strings.Compare(a.Path, b.Path) }), changed, nil, nil)
		if err != nil {
			return err
		}
		for _, i := range indices {
			actions[i].Files = merged
		}
	}
	return nil
}

func renderClaude(selection domain.NativePluginSelection, catalog map[string]json.RawMessage, ignoreDefaults map[string]bool) ([]File, error) {
	if selection.Package.Format != Claude || selection.Package.ConverterVersion != ConverterVersion {
		return nil, fmt.Errorf("unsupported native plugin format/converter: %s/%d", selection.Package.Format, selection.Package.ConverterVersion)
	}
	root := filepath.Join(selection.Root, "upstream")
	manifest := map[string]json.RawMessage{}
	if selection.Package.Manifest != "" {
		var err error
		manifest, err = readObject(root, claudeManifest)
		if err != nil {
			return nil, err
		}
	}
	files, err := ReadFiles(root)
	if err != nil {
		return nil, err
	}
	changed, drop := map[string][]byte{}, map[string]bool{}
	hidden := map[string]File{}
	excluded := map[domain.PackCategory]map[string]bool{}
	needed := map[string]bool{}
	for _, cat := range []domain.PackCategory{domain.CategorySkills, domain.CategoryWorkflows, domain.CategoryAgents} {
		excluded[cat] = map[string]bool{}
		for id, paths := range selection.Package.Components[cat] {
			if selected(selection, cat, id) {
				for _, path := range paths {
					needed[path] = true
				}
				continue
			}
			for _, path := range paths {
				if path != "" && path != claudeManifest {
					drop[path] = true
					excluded[cat][path] = true
				}
			}
		}
	}
	// Declarations can share source assets across categories. Keep exclusions
	// per category so disabling a command does not disable an agent using it.
	if err := filterClaudeContent(selection, manifest, drop, excluded, false); err != nil {
		return nil, err
	}
	if err := filterClaudeContent(selection, catalog, drop, excluded, true); err != nil {
		return nil, err
	}
	// Default scans still load files whose catalog declarations were removed.
	for key, cat := range map[string]domain.PackCategory{"commands": domain.CategoryWorkflows, "agents": domain.CategoryAgents} {
		raw := bytes.TrimSpace(manifest[key])
		if len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
			continue
		}
		retained := false
		for path := range excluded[cat] {
			retained = retained || (needed[path] && strings.HasPrefix(path, key+"/"))
		}
		if retained {
			if selection.Package.Manifest == "" {
				sources := map[string]File{}
				for _, file := range files {
					sources[file.Path] = file
				}
				for path := range excluded[cat] {
					if !needed[path] || !strings.HasPrefix(path, key+"/") {
						continue
					}
					file, err := ResolvePayloadFile(sources, path)
					if err != nil {
						return nil, err
					}
					if !file.Mode.IsRegular() {
						return nil, fmt.Errorf("shared Claude default source %s must resolve to a file", path)
					}
					hidden[path], drop[path] = file, true
				}
				continue
			}
			commands, agents := map[string]any{}, []string{}
			for _, id := range slices.Sorted(maps.Keys(selection.Package.Components[cat])) {
				if !selected(selection, cat, id) {
					continue
				}
				for _, path := range selection.Package.Components[cat][id] {
					if !strings.HasPrefix(path, key+"/") {
						continue
					}
					if key == "commands" {
						commands[id] = map[string]string{"source": "./" + path}
						break
					}
					agents = append(agents, "./"+path)
					if err := nameClaudeAgent(root, path, id, changed); err != nil {
						return nil, err
					}
				}
			}
			if key == "commands" {
				err = setJSON(manifest, key, commands)
			} else {
				err = setJSON(manifest, key, agents)
			}
			if err != nil {
				return nil, err
			}
			continue
		}
		for id, paths := range selection.Package.Components[cat] {
			if selected(selection, cat, id) {
				continue
			}
			for _, path := range paths {
				if strings.HasPrefix(path, key+"/") {
					drop[path] = true
				}
			}
		}
	}
	for path := range needed {
		if _, hide := hidden[path]; !hide {
			delete(drop, path)
		}
	}
	if len(ignoreDefaults) > 0 {
		sources := map[string]File{}
		occupied := map[string]bool{}
		for _, file := range files {
			sources[file.Path], occupied[file.Path] = file, true
		}
		for path := range ignoreDefaults {
			file, err := ResolvePayloadFile(sources, path)
			if err != nil {
				return nil, fmt.Errorf("shared Claude default source %s: %w", path, err)
			}
			if !file.Mode.IsRegular() {
				return nil, fmt.Errorf("shared Claude default source %s must resolve to a file", path)
			}
			hidden[path], drop[path] = file, true
			// The hidden source is restored as a link below; references to it
			// must keep following that path rather than copying its old body.
			for kept := range needed {
				target, err := ResolvePayloadFile(sources, kept)
				if err == nil && target.Path == file.Path {
					delete(needed, kept)
				}
			}
		}
		commands := map[string]json.RawMessage{}
		rewriteCommands := false
		if raw := bytes.TrimSpace(catalog["commands"]); len(raw) > 0 && raw[0] == '{' {
			if err := json.Unmarshal(raw, &commands); err != nil {
				return nil, err
			}
		}
		for id, paths := range selection.Package.Components[domain.CategoryWorkflows] {
			if !selected(selection, domain.CategoryWorkflows, id) {
				delete(commands, id)
				continue
			}
			if len(paths) == 0 || paths[0] == "" {
				continue
			}
			for _, path := range paths {
				rewriteCommands = rewriteCommands || ignoreDefaults[path]
			}
			var definition map[string]json.RawMessage
			_ = json.Unmarshal(commands[id], &definition)
			var original string
			_ = json.Unmarshal(definition["source"], &original)
			if filepath.ToSlash(filepath.Clean(original)) != paths[0] {
				commands[id], err = json.Marshal(map[string]string{"source": "./" + paths[0]})
				if err != nil {
					return nil, err
				}
			}
		}
		if rewriteCommands {
			for id, raw := range commands {
				var definition map[string]json.RawMessage
				_ = json.Unmarshal(raw, &definition)
				if len(definition["source"]) > 0 && !selected(selection, domain.CategoryWorkflows, id) {
					delete(commands, id)
				}
			}
			if err := setJSON(catalog, "commands", commands); err != nil {
				return nil, err
			}
			if catalog["commands"], err = util.OrderJSONObject(catalog["commands"], selection.Package.CatalogCommandOrder); err != nil {
				return nil, err
			}
		}
		agents, err := pathList(catalog["agents"], nil)
		if err != nil {
			return nil, err
		}
		rewriteAgents := false
		agents = slices.DeleteFunc(agents, func(path string) bool { return ignoreDefaults[filepath.ToSlash(filepath.Clean(path))] })
		for _, id := range slices.Sorted(maps.Keys(selection.Package.Components[domain.CategoryAgents])) {
			if !selected(selection, domain.CategoryAgents, id) {
				continue
			}
			var delivered []string
			for _, path := range selection.Package.Components[domain.CategoryAgents][id] {
				if !ignoreDefaults[path] {
					delivered = append(delivered, path)
					continue
				}
				rewriteAgents = true
				base := path + ".aipack-agent-" + selection.Package.Name
				for n := 1; occupied[base+".md"] || occupied[base+".source"]; n++ {
					base = fmt.Sprintf("%s.aipack-agent-%s.%d", path, selection.Package.Name, n)
				}
				body := hidden[path]
				renamed := map[string][]byte{}
				if err := nameClaudeAgent(root, path, id, renamed); err != nil {
					return nil, err
				}
				if content, exists := renamed[path]; exists {
					body.Content = content
				}
				body.Path = base + ".source"
				alias := base + ".md"
				files = append(files, body, File{Path: alias, Link: filepath.Base(body.Path), Mode: fs.ModeSymlink | 0o777})
				occupied[body.Path], occupied[alias] = true, true
				agents = append(agents, "./"+alias)
				delivered = append(delivered, alias, body.Path)
			}
			selection.Package.Components[domain.CategoryAgents][id] = delivered
		}
		if rewriteAgents {
			if err := setJSON(catalog, "agents", agents); err != nil {
				return nil, err
			}
		}
	}
	filterHooks := func(_ string, obj map[string]json.RawMessage, inline bool) error {
		events := obj
		if !inline {
			events = nil
			if err := json.Unmarshal(obj["hooks"], &events); err != nil {
				return err
			}
		}
		for event := range events {
			if !selected(selection, domain.CategoryHooks, hookID("claude", event)) {
				delete(events, event)
			}
		}
		if !inline {
			return setJSON(obj, "hooks", events)
		}
		return nil
	}
	if err := visitClaudeSources(root, catalog, "hooks", "", "", filterHooks, changed); err != nil {
		return nil, err
	}
	hookFallback := "hooks/hooks.json"
	if selection.Package.Manifest == "" && claudeCatalogHasHooks(catalog) {
		hookFallback = ""
	}
	if err := visitClaudeSources(root, manifest, "hooks", hookFallback, selection.Package.Manifest, filterHooks, changed); err != nil {
		return nil, err
	}
	mcpFields, mcpSource := manifest, selection.Package.Manifest
	if mcpSource == "" {
		mcpFields = catalog
	}
	if err := visitClaudeSources(root, mcpFields, "mcpServers", ".mcp.json", mcpSource, func(_ string, obj map[string]json.RawMessage, _ bool) error {
		servers, err := mcpServers(obj)
		if err != nil {
			return err
		}
		for id := range servers {
			if !selected(selection, domain.CategoryMCP, id) {
				delete(servers, id)
			}
		}
		if _, wrapped := obj["mcpServers"]; wrapped {
			return setJSON(obj, "mcpServers", servers)
		}
		return nil
	}, changed); err != nil {
		return nil, err
	}
	if !selection.SettingsEnabled {
		delete(manifest, "settings")
		delete(catalog, "settings")
		for _, path := range selection.Package.SettingsFiles {
			obj, err := readObject(root, path)
			if err != nil {
				return nil, err
			}
			delete(obj, "agent")
			delete(obj, "subagentStatusLine")
			if err := putJSON(changed, path, obj); err != nil {
				return nil, err
			}
		}
	}
	if selection.Package.Manifest != "" {
		if err := putJSON(changed, claudeManifest, manifest); err != nil {
			return nil, err
		}
	}
	// The native local-source loader rereads the canonical root catalog even
	// when registration points at a separate selected catalog file.
	const rootCatalogPath = ".claude-plugin/marketplace.json"
	if source, ok := selection.Package.MarketplaceEntry["source"].(string); ok && filepath.Clean(source) == "." && slices.ContainsFunc(files, func(file File) bool { return file.Path == rootCatalogPath }) {
		rootCatalog, err := readObject(root, rootCatalogPath)
		if err != nil {
			return nil, err
		}
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(rootCatalog["plugins"], &entries); err != nil {
			return nil, err
		}
		var name string
		if err := json.Unmarshal(catalog["name"], &name); err != nil {
			return nil, err
		}
		for i, entry := range entries {
			var originalName string
			if json.Unmarshal(entry["name"], &originalName) == nil && originalName == name {
				entries[i] = catalog
			}
		}
		if err := setJSON(rootCatalog, "plugins", entries); err != nil {
			return nil, err
		}
		if err := putJSON(changed, rootCatalogPath, rootCatalog); err != nil {
			return nil, err
		}
	}
	files, err = renderedFiles(files, changed, drop, needed)
	if err != nil {
		return nil, err
	}
	occupied := map[string]bool{}
	for _, file := range files {
		occupied[file.Path] = true
	}
	for _, path := range slices.Sorted(maps.Keys(hidden)) {
		// Native default scans skip file links, while explicit declarations
		// follow them. Keep the backing body in the same directory so its
		// relative references retain their source location.
		target := path + ".aipack-source"
		for n := 1; occupied[target]; n++ {
			target = fmt.Sprintf("%s.aipack-source.%d", path, n)
		}
		body := hidden[path]
		body.Path = target
		files = append(files, body, File{Path: path, Link: filepath.Base(target), Mode: fs.ModeSymlink | 0o777})
		occupied[target], occupied[path] = true, true
	}
	return files, nil
}

func filterClaudeContent(selection domain.NativePluginSelection, manifest map[string]json.RawMessage, drop map[string]bool, excluded map[domain.PackCategory]map[string]bool, catalog bool) error {
	for key, cat := range map[string]domain.PackCategory{"skills": domain.CategorySkills, "commands": domain.CategoryWorkflows, "agents": domain.CategoryAgents} {
		raw := bytes.TrimSpace(manifest[key])
		if len(raw) == 0 {
			continue
		}
		if key == "commands" && raw[0] == '{' {
			var commands map[string]json.RawMessage
			if err := json.Unmarshal(raw, &commands); err != nil {
				return err
			}
			ids, err := claudeCommandIDs(raw)
			if err != nil {
				return err
			}
			var kept bytes.Buffer
			kept.WriteByte('{')
			for _, id := range ids {
				command := commands[id]
				var definition map[string]json.RawMessage
				if err := json.Unmarshal(command, &definition); err != nil {
					return err
				}
				var source string
				_ = json.Unmarshal(definition["source"], &source)
				clean := filepath.ToSlash(filepath.Clean(source))
				if source != "" {
					// Removing the declaration disables this command while
					// retaining a possibly shared source asset.
					delete(drop, clean)
				}
				// A later alias of a selected file stays inert while the first
				// alias remains. Remove all aliases when that file is excluded.
				active := selected(selection, cat, id) || (source != "" && claudeCommandSourceSeen(selection.Package.Components[cat], clean))
				if !(catalog && source == "") && (!active || excluded[cat][clean]) {
					continue
				}
				if kept.Len() > 1 {
					kept.WriteByte(',')
				}
				name, _ := json.Marshal(id)
				kept.Write(name)
				kept.WriteByte(':')
				kept.Write(command)
			}
			kept.WriteByte('}')
			manifest[key] = kept.Bytes()
			continue
		}
		paths, err := pathList(raw, nil)
		if err != nil {
			return err
		}
		var kept []string
		for _, path := range paths {
			clean := filepath.ToSlash(filepath.Clean(path))
			if key == "skills" {
				known, active, retained := false, false, false
				for sourceCat, components := range selection.Package.Components {
					for id, sources := range components {
						for _, source := range sources {
							if clean != "." && !strings.HasPrefix(source, clean+"/") {
								continue
							}
							if sourceCat == cat {
								known = true
								active = active || selected(selection, cat, id)
							} else {
								retained = retained || excluded[cat][source]
							}
						}
					}
				}
				if known && !active && retained {
					continue
				}
			}
			if excluded[cat][clean] && strings.HasSuffix(clean, ".md") {
				delete(drop, clean)
				continue
			}
			kept = append(kept, path)
		}
		if len(kept) != len(paths) {
			if kept == nil {
				kept = []string{}
			}
			if err := setJSON(manifest, key, kept); err != nil {
				return err
			}
		}
	}
	return nil
}

// Explicit agent file declarations omit default directory prefixes. Preserve
// the native name when a selected default scan becomes an explicit list.
func nameClaudeAgent(root, path, name string, changed map[string][]byte) error {
	body, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return err
	}
	fm, body, err := domain.SplitFrontmatter(body)
	if err != nil {
		return err
	}
	var node yaml.Node
	if len(fm) > 0 {
		if err := yaml.Unmarshal(fm, &node); err != nil {
			return err
		}
	} else {
		node = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("native agent %s frontmatter must be a mapping", path)
	}
	fields := node.Content[0]
	value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}
	replaced := false
	for i := 0; i < len(fields.Content); i += 2 {
		if fields.Content[i].Value == "name" {
			if fields.Content[i+1].Value == name {
				return nil
			}
			fields.Content[i+1], replaced = value, true
		}
	}
	if !replaced {
		fields.Content = append(fields.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"}, value)
	}
	fm, err = yaml.Marshal(&node)
	if err != nil {
		return err
	}
	changed[path] = append(append([]byte("---\n"), fm...), append([]byte("---\n"), body...)...)
	return nil
}
