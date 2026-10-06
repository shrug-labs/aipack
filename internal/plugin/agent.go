package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

const AgentPlugins = "agent-plugins"
const agentPluginSchema = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
const agentMCPSchema = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"

var agentPluginName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)

// ReadAgentPlugin follows Codex 0.159.2's root-manifest branch. Skills and MCP
// use fixed locations; legacy commands, hooks, and apps remain inactive assets.
func ReadAgentPlugin(root, marketplace string) (config.PackManifest, error) {
	manifest, err := readObject(root, "plugin.json")
	if err != nil {
		return config.PackManifest{}, err
	}
	var schema, name, version string
	if json.Unmarshal(manifest["$schema"], &schema) != nil || schema != agentPluginSchema {
		return config.PackManifest{}, fmt.Errorf("root plugin.json requires supported Agent Plugins schema %q", agentPluginSchema)
	}
	if json.Unmarshal(manifest["name"], &name) != nil || len(name) > 64 || !agentPluginName.MatchString(name) || strings.Contains(name, "--") || strings.Contains(name, "..") || !domain.ValidNativeName(marketplace) {
		return config.PackManifest{}, fmt.Errorf("invalid Agent Plugins or marketplace name")
	}
	for _, field := range []string{"version", "description", "homepage", "repository", "license"} {
		if raw, exists := manifest[field]; exists {
			var value string
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
				return config.PackManifest{}, fmt.Errorf("agent plugin %s must be a string", field)
			}
		}
	}
	if raw, exists := manifest["keywords"]; exists {
		var keywords []*string
		if json.Unmarshal(raw, &keywords) != nil || keywords == nil || slices.Contains(keywords, nil) {
			return config.PackManifest{}, fmt.Errorf("agent plugin keywords must be a string array")
		}
	}
	if raw, exists := manifest["author"]; exists {
		author, err := codexStructFields(raw, []string{"name", "email", "url"})
		if err != nil {
			return config.PackManifest{}, fmt.Errorf("agent plugin author: %w", err)
		}
		for field, fieldRaw := range author {
			if bytes.TrimSpace(raw)[0] == '[' && bytes.Equal(bytes.TrimSpace(fieldRaw), []byte("null")) {
				continue
			}
			var value string
			if !slices.Contains([]string{"name", "email", "url"}, field) || bytes.Equal(bytes.TrimSpace(fieldRaw), []byte("null")) || json.Unmarshal(fieldRaw, &value) != nil {
				return config.PackManifest{}, fmt.Errorf("invalid Agent Plugins author.%s", field)
			}
		}
	}
	_ = json.Unmarshal(manifest["version"], &version)
	p := &domain.NativePlugin{Format: AgentPlugins, Harness: domain.HarnessCodex, Name: name, Marketplace: marketplace, Manifest: "plugin.json", ConverterVersion: ConverterVersion,
		Components: map[domain.PackCategory]map[string][]string{domain.CategorySkills: {}, domain.CategoryMCP: {}}}
	if err := discoverCodexSkills(root, "skills", true, p.Components[domain.CategorySkills]); err != nil {
		return config.PackManifest{}, err
	}
	if info, err := os.Lstat(filepath.Join(root, "mcp.json")); err == nil && info.Mode().IsRegular() {
		obj, err := readObject(root, "mcp.json")
		var schema string
		if err == nil && json.Unmarshal(obj["$schema"], &schema) == nil && schema == agentMCPSchema && len(obj) == 2 && obj["mcpServers"] != nil {
			servers, err := mcpServers(obj)
			if err == nil {
				for id := range servers {
					p.Components[domain.CategoryMCP][id] = []string{"mcp.json"}
				}
			}
		}
		// The native loader disables an invalid MCP component, preserving the
		// plugin's other capabilities and the original file for diagnostics.
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return config.PackManifest{}, err
	}
	// A valid com.openai object takes precedence over the legacy overlay.
	var extensions map[string]json.RawMessage
	var openai map[string]json.RawMessage
	_ = json.Unmarshal(manifest["extensions"], &extensions)
	if json.Unmarshal(extensions["com.openai"], &openai) != nil || openai == nil {
		if overlay, err := readObject(root, ".codex-plugin/plugin.json"); err == nil {
			if err := validateCodexExtension(overlay); err != nil {
				return config.PackManifest{}, err
			}
			p.SettingsFiles = []string{".codex-plugin/plugin.json"}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return config.PackManifest{}, err
		}
	} else if err := validateCodexExtension(openai); err != nil {
		return config.PackManifest{}, err
	}
	return config.PackManifest{SchemaVersion: config.PackSchemaVersion, Name: name, Version: strings.TrimSpace(version), Root: ".", NativePlugin: p,
		Skills: slices.Sorted(maps.Keys(p.Components[domain.CategorySkills])), MCP: slices.Sorted(maps.Keys(p.Components[domain.CategoryMCP]))}, nil
}

// Portable extensions and their legacy overlays use the native legacy parser
// even for fields whose capabilities the portable runtime leaves inactive.
func validateCodexExtension(obj map[string]json.RawMessage) error {
	for _, field := range []string{"name", "version", "description", "apps"} {
		if raw, exists := obj[field]; exists {
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && field != "name" {
				continue
			}
			var value string
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
				return fmt.Errorf("codex extension %s must be a string", field)
			}
		}
	}
	if raw, exists := obj["keywords"]; exists {
		var values []*string
		if json.Unmarshal(raw, &values) != nil || values == nil || slices.Contains(values, nil) {
			return fmt.Errorf("codex extension keywords must be a string array")
		}
	}
	raw, exists := obj["interface"]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	fields := []string{"displayName", "shortDescription", "longDescription", "developerName", "category", "capabilities", "websiteUrl", "privacyPolicyUrl", "termsOfServiceUrl", "defaultPrompt", "brandColor", "composerIcon", "logo", "logoDark", "screenshots"}
	iface, err := codexStructFields(raw, fields)
	if err != nil {
		return fmt.Errorf("codex extension interface: %w", err)
	}
	for _, field := range fields {
		value, exists := iface[field]
		if alias := strings.TrimSuffix(field, "Url") + "URL"; strings.HasSuffix(field, "Url") {
			if other, present := iface[alias]; present {
				if exists {
					return fmt.Errorf("duplicate Codex extension interface field %s", field)
				}
				value, exists = other, true
			}
		}
		if !exists || field == "defaultPrompt" {
			continue
		}
		if field == "capabilities" || field == "screenshots" {
			var values []*string
			if json.Unmarshal(value, &values) != nil || values == nil || slices.Contains(values, nil) {
				return fmt.Errorf("codex extension interface %s must be a string array", field)
			}
		} else if !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			var text string
			if json.Unmarshal(value, &text) != nil {
				return fmt.Errorf("codex extension interface %s must be a string", field)
			}
		}
	}
	return nil
}

func codexStructFields(raw json.RawMessage, fields []string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil && obj != nil {
		return obj, nil
	}
	// Serde accepts struct fields in sequence order, including [].
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil || len(values) > len(fields) {
		return nil, fmt.Errorf("expected an object or field sequence")
	}
	obj = map[string]json.RawMessage{}
	for i, value := range values {
		obj[fields[i]] = value
	}
	return obj, nil
}

func renderAgentPlugin(selection domain.NativePluginSelection, root string, files []File, manifest map[string]json.RawMessage, changed map[string][]byte) ([]File, error) {
	for _, path := range selection.Package.SettingsFiles {
		obj, err := readObject(root, path)
		if err != nil {
			return nil, err
		}
		if err := filterCodexOnboarding(selection, obj); err != nil {
			return nil, err
		}
		if err := putJSON(changed, path, obj); err != nil {
			return nil, err
		}
	}
	if len(selection.Package.Components[domain.CategoryMCP]) > 0 {
		obj, err := readObject(root, "mcp.json")
		if err != nil {
			return nil, err
		}
		servers, err := mcpServers(obj)
		if err != nil {
			return nil, err
		}
		for id := range servers {
			if !selected(selection, domain.CategoryMCP, id) {
				delete(servers, id)
			}
		}
		if err := setJSON(obj, "mcpServers", servers); err != nil {
			return nil, err
		}
		if err := putJSON(changed, "mcp.json", obj); err != nil {
			return nil, err
		}
	}
	if err := putJSON(changed, "plugin.json", manifest); err != nil {
		return nil, err
	}
	drop := map[string]bool{}
	for id, paths := range selection.Package.Components[domain.CategorySkills] {
		if !selected(selection, domain.CategorySkills, id) {
			for _, path := range paths {
				drop[path] = true
			}
		}
	}
	return renderedFiles(files, changed, drop, nil)
}
