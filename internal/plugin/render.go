package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// RenderCodex returns a selected native view. Complete stored source remains
// untouched. Filtering removes activation entrypoints, never shared assets.
// Unchanged JSON retains its exact original bytes (including unknown fields).
func RenderCodex(selection domain.NativePluginSelection) ([]File, error) {
	if (selection.Package.Format != CodexLegacy && selection.Package.Format != AgentPlugins) || selection.Package.ConverterVersion != ConverterVersion {
		return nil, fmt.Errorf("unsupported native plugin format/converter: %s/%d", selection.Package.Format, selection.Package.ConverterVersion)
	}
	root := filepath.Join(selection.Root, "upstream")
	manifest, err := readObject(root, selection.Package.Manifest)
	if err != nil {
		return nil, err
	}
	files, err := ReadFiles(root)
	if err != nil {
		return nil, err
	}
	changed := map[string][]byte{}
	if err := filterCodexOnboarding(selection, manifest); err != nil {
		return nil, err
	}
	if selection.Package.Format == AgentPlugins {
		return renderAgentPlugin(selection, root, files, manifest, changed)
	}
	if !selection.SettingsEnabled {
		for _, path := range selection.Package.SettingsFiles {
			obj, err := readObject(root, path)
			if err != nil {
				return nil, err
			}
			if err := setJSON(obj, "apps", map[string]any{}); err != nil {
				return nil, err
			}
			if err := putJSON(changed, path, obj); err != nil {
				return nil, err
			}
		}
	}
	return renderCodexLegacy(selection, root, files, manifest, changed)
}

func filterCodexOnboarding(selection domain.NativePluginSelection, manifest map[string]json.RawMessage) error {
	var extensions map[string]json.RawMessage
	var openai map[string]json.RawMessage
	if json.Unmarshal(manifest["extensions"], &extensions) != nil || json.Unmarshal(extensions["com.openai"], &openai) != nil {
		return nil
	}
	var onboarding string
	_ = json.Unmarshal(openai["onboardingSkill"], &onboarding)
	if onboarding == "" {
		return nil
	}
	remove := !selection.SettingsEnabled
	for id, paths := range selection.Package.Components[domain.CategorySkills] {
		for _, path := range paths {
			if filepath.Clean(onboarding) == filepath.Clean(path) || filepath.Clean(onboarding) == filepath.Dir(path) {
				remove = remove || !selected(selection, domain.CategorySkills, id)
			}
		}
	}
	if !remove {
		return nil
	}
	delete(openai, "onboardingSkill")
	if err := setJSON(extensions, "com.openai", openai); err != nil {
		return err
	}
	return setJSON(manifest, "extensions", extensions)
}

func renderCodexLegacy(selection domain.NativePluginSelection, root string, files []File, manifest map[string]json.RawMessage, changed map[string][]byte) ([]File, error) {
	filterHooks := func(source string, obj map[string]json.RawMessage) error {
		if len(obj["hooks"]) == 0 {
			return nil
		}
		var events map[string]json.RawMessage
		if err := json.Unmarshal(obj["hooks"], &events); err != nil {
			return err
		}
		for event := range events {
			if !selected(selection, domain.CategoryHooks, hookID("codex", event)) {
				delete(events, event)
			}
		}
		return setJSON(obj, "hooks", events)
	}
	// Inline hooks must be edited inside the manifest, not written over it.
	if raw := bytes.TrimSpace(manifest["hooks"]); len(raw) > 0 && (raw[0] == '{' || raw[0] == '[') {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) == nil && obj != nil {
			if err := filterHooks(selection.Package.Manifest, obj); err != nil {
				return nil, err
			}
			if err := setJSON(manifest, "hooks", obj); err != nil {
				return nil, err
			}
		} else {
			var objects []map[string]json.RawMessage
			if json.Unmarshal(raw, &objects) == nil {
				for _, obj := range objects {
					if err := filterHooks(selection.Package.Manifest, obj); err != nil {
						return nil, err
					}
				}
				if err := setJSON(manifest, "hooks", objects); err != nil {
					return nil, err
				}
			} else if err := filterHookFiles(root, manifest, filterHooks, changed); err != nil {
				return nil, err
			}
		}
	} else if err := filterHookFiles(root, manifest, filterHooks, changed); err != nil {
		return nil, err
	}
	filterMCP := func(source string, obj map[string]json.RawMessage) error {
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
	}
	if raw := bytes.TrimSpace(manifest["mcpServers"]); len(raw) > 0 && raw[0] == '{' {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, err
		}
		if err := filterMCP(selection.Package.Manifest, obj); err != nil {
			return nil, err
		}
		if err := setJSON(manifest, "mcpServers", obj); err != nil {
			return nil, err
		}
	} else if err := visitMCP(root, manifest, func(source string, obj map[string]json.RawMessage) error {
		if err := filterMCP(source, obj); err != nil {
			return err
		}
		return putJSON(changed, source, obj)
	}); err != nil {
		return nil, err
	}
	if err := putJSON(changed, selection.Package.Manifest, manifest); err != nil {
		return nil, err
	}
	drop, needed := map[string]bool{}, map[string]bool{}
	for _, cat := range []domain.PackCategory{domain.CategorySkills, domain.CategoryWorkflows} {
		for id, paths := range selection.Package.Components[cat] {
			for _, path := range paths {
				if selected(selection, cat, id) {
					needed[path] = true
				} else {
					drop[path] = true
				}
			}
		}
	}
	return renderedFiles(files, changed, drop, needed)
}

func renderedFiles(files []File, changed map[string][]byte, drop, needed map[string]bool) ([]File, error) {
	byPath := map[string]File{}
	for _, file := range files {
		byPath[file.Path] = file
	}
	replacements := map[string][]byte{}
	for path, replacement := range changed {
		original, err := ResolvePayloadFile(byPath, path)
		if err != nil {
			return nil, err
		}
		if !original.Mode.IsRegular() {
			return nil, fmt.Errorf("native replacement %s must resolve to a file", path)
		}
		same := bytes.Equal(original.Content, replacement)
		if filepath.Ext(path) != ".md" {
			var before, next any
			if util.UnmarshalJSON(original.Content, &before) != nil || util.UnmarshalJSON(replacement, &next) != nil {
				return nil, fmt.Errorf("invalid native JSON %s", path)
			}
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(next)
			same = bytes.Equal(a, b)
		}
		if !same {
			replacements[path] = replacement
		}
	}
	files = slices.Clone(files)
	var output []File
	for i := 0; i < len(files); i++ {
		file := files[i]
		if drop[file.Path] {
			continue
		}
		if file.Link != "" {
			prefix := file.Path + "/"
			materialize := false
			for path := range replacements {
				materialize = materialize || strings.HasPrefix(path, prefix)
			}
			for path, excluded := range drop {
				materialize = materialize || (excluded && strings.HasPrefix(path, prefix))
			}
			required := needed[file.Path]
			for path, kept := range needed {
				required = required || (kept && strings.HasPrefix(path, prefix))
			}
			original, err := ResolvePayloadFile(byPath, file.Path)
			if err == nil {
				if original.Mode.IsRegular() && required && drop[original.Path] {
					file.Mode, file.Link, file.Content = original.Mode, "", original.Content
					byPath[file.Path] = file
				} else if original.Mode.IsDir() && required {
					targetPrefix := original.Path + "/"
					for path, excluded := range drop {
						materialize = materialize || (excluded && (path == original.Path || strings.HasPrefix(path, targetPrefix)))
					}
				}
			}
			if materialize {
				if err != nil {
					return nil, err
				}
				if !original.Mode.IsDir() {
					return nil, fmt.Errorf("native selection parent %s must resolve to a directory", file.Path)
				}
				targetPrefix := original.Path + "/"
				// ponytail: scan the payload per changed directory alias; index subtrees if selection becomes slow.
				for _, child := range files {
					if !strings.HasPrefix(child.Path, targetPrefix) {
						continue
					}
					path := prefix + strings.TrimPrefix(child.Path, targetPrefix)
					if child.Link != "" {
						target := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(child.Path), child.Link)))
						if target != original.Path && !strings.HasPrefix(target, targetPrefix) {
							child.Link, err = filepath.Rel(filepath.Dir(path), target)
							if err != nil {
								return nil, err
							}
						}
					}
					child.Path = path
					files = append(files, child)
					byPath[path] = child
				}
				file.Mode, file.Link = original.Mode, ""
				byPath[file.Path] = file
			}
		}
		if replacement, ok := replacements[file.Path]; ok {
			original, err := ResolvePayloadFile(byPath, file.Path)
			if err != nil {
				return nil, err
			}
			file.Mode, file.Link, file.Content = original.Mode, "", replacement
		}
		output = append(output, file)
	}
	return output, nil
}

// ResolvePayloadFile follows bounded file and directory links within a complete payload.
func ResolvePayloadFile(files map[string]File, path string) (File, error) {
	path = filepath.ToSlash(filepath.Clean(path))
	for depth := 0; depth <= len(files); depth++ {
		parts, redirected := strings.Split(path, "/"), false
		for i := range parts {
			prefix := strings.Join(parts[:i+1], "/")
			file, exists := files[prefix]
			if !exists || file.Link == "" {
				continue
			}
			target := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(prefix), file.Link)))
			if filepath.IsAbs(file.Link) || !filepath.IsLocal(target) {
				return File{}, fmt.Errorf("native symlink %s escapes package", prefix)
			}
			path = filepath.ToSlash(filepath.Clean(filepath.Join(target, strings.Join(parts[i+1:], "/"))))
			redirected = true
			break
		}
		if !redirected {
			file, exists := files[path]
			if !exists {
				return File{}, fmt.Errorf("missing native payload target %s", path)
			}
			return file, nil
		}
	}
	return File{}, fmt.Errorf("cyclic native symlink %s", path)
}

func filterHookFiles(root string, manifest map[string]json.RawMessage, filter func(string, map[string]json.RawMessage) error, changed map[string][]byte) error {
	return visitHooks(root, manifest, func(source string, obj map[string]json.RawMessage) error {
		if err := filter(source, obj); err != nil {
			return err
		}
		return putJSON(changed, source, obj)
	})
}

func setJSON(obj map[string]json.RawMessage, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	obj[key] = raw
	return nil
}

func putJSON(changed map[string][]byte, path string, obj map[string]json.RawMessage) error {
	raw, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	changed[filepath.ToSlash(filepath.Clean(path))] = append(raw, '\n')
	return nil
}
