package opencode

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/plugin"
	"github.com/shrug-labs/aipack/internal/util"
)

func planImportedPlugins(f *domain.Fragment, ctx engine.SyncContext, configBase, configPath string) ([]map[string]any, []domain.TraceRef, error) {
	var overlays []map[string]any
	var refs []domain.TraceRef
	bindings := map[string]string{}
	for _, pack := range ctx.Profile.Packs {
		s := pack.NativePlugin
		if s == nil {
			continue
		}
		binding := s.Package.Binding()
		if previous, exists := bindings[binding]; exists {
			return nil, nil, fmt.Errorf("plugin binding %s is selected by both %s and %s", binding, previous, pack.Name)
		}
		bindings[binding] = pack.Name
		dataRoot := filepath.Join(configBase, "aipack-data", binding)
		files, overlay, err := plugin.RenderCodexForOpenCode(*s, "/aipack-payload", "/aipack-data")
		if err != nil {
			return nil, nil, fmt.Errorf("plugin pack %s: %w", pack.Name, err)
		}
		body, err := json.Marshal(struct {
			Package domain.NativePlugin
			Payload []byte
			Overlay map[string]any
		}{s.Package, domain.PackageManifest(files), overlay})
		if err != nil {
			return nil, nil, err
		}
		generation := util.ContentDigest(body)
		configHome := ctx.NativeConfigDir
		if configHome == "" {
			configHome = filepath.Join(ctx.Home, ".config/opencode")
		}
		projectDir := ""
		if ctx.Scope == domain.ScopeProject {
			projectDir = ctx.TargetDir
		}
		payloadRoot := filepath.Join(configBase, "aipack-imports", binding, generation, "payload")
		files, overlay, err = plugin.RenderCodexForOpenCode(*s, payloadRoot, dataRoot)
		if err != nil {
			return nil, nil, err
		}
		managed, err := json.Marshal(overlay)
		if err != nil {
			return nil, nil, err
		}
		packRefs := []domain.TraceRef{{Category: domain.CategoryPlugins, Name: s.Package.Name, SourcePack: pack.Name}}
		for category, ids := range s.Selected {
			for _, id := range ids {
				packRefs = append(packRefs, domain.TraceRef{Category: category, Name: id, SourcePack: pack.Name})
			}
		}
		f.Writes = append(f.Writes, domain.WriteAction{
			Dst: payloadRoot, Src: filepath.Join(s.Root, "upstream"), PackageFiles: files, SourcePack: pack.Name,
			Category: domain.CategoryPlugins, TraceRefs: packRefs,
			Delivery: &domain.PackageDelivery{Binding: binding, Generation: generation, Home: ctx.Home, ConfigHome: configHome, ProjectDir: projectDir, DataDir: dataRoot, SettingsPath: configPath, ManagedOverlay: managed},
		})
		f.Desired = append(f.Desired, payloadRoot)
		for name, entry := range overlay["mcp"].(map[string]any) {
			content, err := json.Marshal(entry)
			if err != nil {
				return nil, nil, err
			}
			f.MCPServers = append(f.MCPServers, domain.MCPAction{Name: name, ConfigPath: configPath, Content: content, SourcePack: pack.Name, Harness: domain.HarnessOpenCode, Embedded: true})
		}
		overlays = append(overlays, overlay)
		refs = append(refs, packRefs...)
	}
	return overlays, refs, nil
}

func mergeImportedSettings(content []byte, overlays []map[string]any) ([]byte, error) {
	if len(overlays) == 0 {
		return content, nil
	}
	root := map[string]any{}
	if err := util.UnmarshalJSON(content, &root); err != nil {
		return nil, err
	}
	servers, _ := root["mcp"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		root["mcp"] = servers
	}
	names := map[string]string{}
	for name := range servers {
		names[plugin.OpenCodeToolName(name)] = name
	}
	permissions, _ := root["permission"].(map[string]any)
	if root["permission"] != nil && permissions == nil {
		return nil, fmt.Errorf("portable plugins require an object permission configuration")
	}
	if permissions == nil {
		permissions = map[string]any{}
		root["permission"] = permissions
	}
	skills, _ := root["skills"].(map[string]any)
	if skills == nil {
		skills = map[string]any{}
		root["skills"] = skills
	}
	paths, _ := skills["paths"].([]any)
	if paths == nil {
		paths = []any{}
	}
	for _, overlay := range overlays {
		for name, entry := range overlay["mcp"].(map[string]any) {
			key := plugin.OpenCodeToolName(name)
			if previous, exists := names[key]; exists {
				return nil, fmt.Errorf("OpenCode MCP namespace collides between %s and %s", previous, name)
			}
			names[key], servers[name] = name, entry
		}
		for _, path := range overlay["skills"].(map[string]any)["paths"].([]string) {
			paths = append(paths, path)
		}
		for key, value := range overlay["permission"].(map[string]any) {
			if key == "skill" {
				current, _ := permissions[key].(map[string]any)
				if current == nil {
					current = map[string]any{}
					if previous, exists := permissions[key]; exists {
						current["*"] = previous
					}
				}
				for name, permission := range value.(map[string]string) {
					if old, exists := current[name]; exists && !reflect.DeepEqual(old, permission) {
						return nil, fmt.Errorf("OpenCode skill permission collision: %s", name)
					}
					current[name] = permission
				}
				permissions[key] = current
			} else {
				if old, exists := permissions[key]; exists && !reflect.DeepEqual(old, value) {
					return nil, fmt.Errorf("OpenCode tool permission collision: %s", key)
				}
				permissions[key] = value
			}
		}
	}
	skills["paths"] = paths
	return util.MarshalPrettyJSON(root)
}

// PruneImportedOverlay removes this package's activation keys while preserving
// unrelated skills, servers and user permissions. Payload files are separate.
func PruneImportedOverlay(root map[string]any, owned map[string]any) {
	if mcp, ok := owned["mcp"].(map[string]any); ok {
		for name := range mcp {
			if current, ok := root["mcp"].(map[string]any); ok {
				delete(current, name)
			}
		}
	}
	permissions, hasPermissions := owned["permission"].(map[string]any)
	currentPermissions, ok := root["permission"].(map[string]any)
	if hasPermissions && ok {
		for name, value := range permissions {
			if name != "skill" {
				delete(currentPermissions, name)
				continue
			}
			skills, ok := value.(map[string]any)
			if !ok {
				continue
			}
			active, ok := currentPermissions[name].(map[string]any)
			if !ok {
				continue
			}
			for skill := range skills {
				delete(active, skill)
			}
			if len(active) == 0 {
				delete(currentPermissions, name)
			}
		}
		if len(currentPermissions) == 0 {
			delete(root, "permission")
		}
	}
	if ownedSkills, ok := owned["skills"].(map[string]any); ok {
		if current, ok := root["skills"].(map[string]any); ok {
			ownedPaths, _ := ownedSkills["paths"].([]any)
			paths, _ := current["paths"].([]any)
			kept := []any{}
			for _, path := range paths {
				found := false
				for _, managed := range ownedPaths {
					if reflect.DeepEqual(path, managed) {
						found = true
						break
					}
				}
				if !found {
					kept = append(kept, path)
				}
			}
			if len(kept) == 0 {
				delete(current, "paths")
			} else {
				current["paths"] = kept
			}
			if len(current) == 0 {
				delete(root, "skills")
			}
		}
	}
}

func stripImportedOverlays(root map[string]any, overlays [][]byte) {
	for _, raw := range overlays {
		owned := map[string]any{}
		if util.UnmarshalJSON(raw, &owned) == nil {
			PruneImportedOverlay(root, owned)
		}
	}
}

func stripManagedReferences(root map[string]any, ctx harness.EditContext, reset bool) {
	previous := map[string]any{}
	if len(ctx.PreviousManagedOverlay) == 0 || util.UnmarshalJSON(ctx.PreviousManagedOverlay, &previous) != nil {
		delete(root, "tools")
		delete(root, "instructions")
		delete(root, "skills")
	} else {
		if tools, ok := previous["tools"].(map[string]any); ok {
			names := map[string]struct{}{}
			for name := range tools {
				names[name] = struct{}{}
			}
			harness.PruneMapKeys(root, "tools", names)
		}
		PruneImportedOverlay(root, map[string]any{"skills": previous["skills"]})
		if owned, ok := previous["instructions"].([]any); ok {
			if current, ok := root["instructions"].([]any); ok {
				kept := []any{}
				for _, value := range current {
					found := false
					for _, managed := range owned {
						if reflect.DeepEqual(value, managed) {
							found = true
							break
						}
					}
					if !found {
						kept = append(kept, value)
					}
				}
				if len(kept) == 0 {
					delete(root, "instructions")
				} else {
					root["instructions"] = kept
				}
			}
		}
	}
	if reset && root["tools"] == nil {
		root["tools"] = map[string]any{}
	}
}
