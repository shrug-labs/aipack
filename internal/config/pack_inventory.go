package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
)

func ResolvePackRoot(manifestPath string, root string) string {
	if root == "" {
		return ""
	}
	if filepath.IsAbs(root) {
		return root
	}
	base := filepath.Dir(manifestPath)
	return filepath.Join(base, root)
}

func validatePackInventory(packName string, packRoot string, manifest PackManifest) error {
	st, err := os.Stat(packRoot)
	if err != nil {
		return fmt.Errorf("pack %q root missing: %w", packName, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("pack %q root is not a directory: %s", packName, packRoot)
	}
	if err := validatePackList(packName, capRules, manifest.Rules); err != nil {
		return err
	}
	if err := validatePackList(packName, capAgents, manifest.Agents); err != nil {
		return err
	}
	if err := validatePackList(packName, capWorkflows, manifest.Workflows); err != nil {
		return err
	}
	if err := validatePackList(packName, capSkills, manifest.Skills); err != nil {
		return err
	}
	if err := validatePackList(packName, capHooks, manifest.Hooks); err != nil {
		return err
	}
	if err := validatePackList(packName, "prompts", manifest.Prompts); err != nil {
		return err
	}
	if manifest.NativePlugin != nil {
		return validateNativePluginInventory(packName, packRoot, manifest)
	}

	for _, id := range manifest.Rules {
		if strings.Contains(id, domain.RuleHarnessSeparator) {
			return fmt.Errorf("pack %q rules id %q must not contain %q (reserved as the harness escape for `/`)",
				packName, id, domain.RuleHarnessSeparator)
		}
	}
	for _, label := range []struct {
		name string
		ids  []string
	}{
		{capAgents, manifest.Agents},
		{capWorkflows, manifest.Workflows},
		{capSkills, manifest.Skills},
		{capHooks, manifest.Hooks},
	} {
		for _, id := range label.ids {
			if strings.ContainsRune(id, '/') {
				return fmt.Errorf("pack %q %s id %q must not contain `/` (only rules support subdirectory authoring)",
					packName, label.name, id)
			}
			switch label.name {
			case capAgents, capWorkflows, capSkills, capHooks:
				if strings.Contains(id, domain.RenderedIdentitySeparator) {
					return fmt.Errorf("pack %q %s id %q must not contain %q (reserved for rendered content identity)",
						packName, label.name, id, domain.RenderedIdentitySeparator)
				}
			}
		}
	}

	if err := validateManifestContent(packName, packRoot, manifest, domain.CategoryRules, manifest.Rules); err != nil {
		return err
	}
	if err := validateManifestContent(packName, packRoot, manifest, domain.CategoryAgents, manifest.Agents); err != nil {
		return err
	}
	if err := validateManifestContent(packName, packRoot, manifest, domain.CategoryWorkflows, manifest.Workflows); err != nil {
		return err
	}
	if err := validateManifestContent(packName, packRoot, manifest, domain.CategorySkills, manifest.Skills); err != nil {
		return err
	}
	if err := validateManifestContent(packName, packRoot, manifest, domain.CategoryHooks, manifest.Hooks); err != nil {
		return err
	}
	for _, id := range manifest.Prompts {
		path := filepath.Join(packRoot, "prompts", filepath.FromSlash(id)+".md")
		if err := requireFile(path); err != nil {
			return fmt.Errorf("pack %q prompts %q missing: %w", packName, id, err)
		}
	}
	for _, name := range manifest.MCP {
		path := filepath.Join(packRoot, "mcp", filepath.FromSlash(name)+".json")
		if err := requireFile(path); err != nil {
			return fmt.Errorf("pack %q mcp server %q missing: %w", packName, name, err)
		}
		if err := validateMCPServerName(packName, name, path); err != nil {
			return err
		}
	}

	if err := validateConfigFileMap(packName, "harness_settings", packRoot, manifest.Configs.HarnessSettings); err != nil {
		return err
	}
	if err := validateConfigFileMap(packName, "harness_plugins", packRoot, manifest.Configs.HarnessPlugins); err != nil {
		return err
	}
	return nil
}

func validateNativePluginInventory(packName, root string, manifest PackManifest) error {
	p := manifest.NativePlugin
	codex := p.Format == "codex-legacy" && p.Harness == domain.HarnessCodex && p.Manifest == ".codex-plugin/plugin.json"
	agent := p.Format == "agent-plugins" && p.Harness == domain.HarnessCodex && p.Manifest == "plugin.json"
	claude := p.Format == "claude" && p.Harness == domain.HarnessClaudeCode && (p.Manifest == ".claude-plugin/plugin.json" || p.Manifest == "")
	if (!codex && !agent && !claude) || p.ConverterVersion < 1 {
		return fmt.Errorf("pack %q has an unsupported native plugin descriptor", packName)
	}
	for _, name := range []string{p.Name, p.Marketplace} {
		if !domain.ValidNativeName(name) {
			return fmt.Errorf("pack %q has an invalid native plugin identity", packName)
		}
	}
	upstream := filepath.Join(root, "upstream")
	if p.Manifest != "" {
		if err := requireFile(filepath.Join(upstream, p.Manifest)); err != nil {
			return err
		}
	} else if p.MarketplaceEntry["name"] != p.Name {
		return fmt.Errorf("pack %q has no catalog manifest identity", packName)
	} else if _, err := os.Lstat(filepath.Join(upstream, ".claude-plugin/plugin.json")); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return err
		}
		return fmt.Errorf("pack %q catalog manifest descriptor conflicts with its payload manifest", packName)
	}
	for _, rel := range p.SettingsFiles {
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("pack %q native settings path escapes payload", packName)
		}
		path := filepath.Join(upstream, rel)
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		realRoot, err := filepath.EvalSymlinks(upstream)
		relReal, relErr := filepath.Rel(realRoot, real)
		if err != nil || relErr != nil || !filepath.IsLocal(relReal) {
			return fmt.Errorf("pack %q native settings path escapes payload", packName)
		}
		if err := requireFile(path); err != nil {
			return err
		}
	}
	for cat, entries := range p.Components {
		switch cat {
		case domain.CategorySkills, domain.CategoryAgents, domain.CategoryWorkflows, domain.CategoryHooks, domain.CategoryMCP:
		default:
			return fmt.Errorf("pack %q has unsupported native category %q", packName, cat)
		}
		if len(entries) != len(manifest.ContentIDs(cat)) {
			return fmt.Errorf("pack %q native %s inventory disagrees with descriptors", packName, cat)
		}
		for _, id := range manifest.ContentIDs(cat) {
			paths := entries[id]
			if len(paths) == 0 {
				return fmt.Errorf("pack %q native %s %q has no source paths", packName, cat, id)
			}
			for _, rel := range paths {
				if rel == "" && claude {
					key, item := "commands", id
					if cat == domain.CategoryHooks {
						key, item = "hooks", p.HookEvents[id]
					} else if cat == domain.CategoryMCP && p.Manifest == "" {
						key = "mcpServers"
					} else if cat != domain.CategoryWorkflows {
						return fmt.Errorf("pack %q has an invalid inline catalog category", packName)
					}
					entries, _ := p.MarketplaceEntry[key].(map[string]any)
					if entries[item] == nil {
						return fmt.Errorf("pack %q native %s %q has no inline catalog declaration", packName, cat, id)
					}
					continue
				}
				if !filepath.IsLocal(rel) {
					return fmt.Errorf("pack %q native path %q escapes payload", packName, rel)
				}
				path := filepath.Join(upstream, filepath.FromSlash(rel))
				if err := requireFile(path); err != nil {
					return err
				}
				real, err := filepath.EvalSymlinks(path)
				if err != nil {
					return err
				}
				realRoot, err := filepath.EvalSymlinks(upstream)
				if err != nil {
					return err
				}
				relReal, err := filepath.Rel(realRoot, real)
				if err != nil || !filepath.IsLocal(relReal) {
					return fmt.Errorf("pack %q native path %q escapes payload", packName, rel)
				}
			}
			if cat == domain.CategoryHooks && p.HookEvents[id] == "" {
				return fmt.Errorf("pack %q native hook %q has no event", packName, id)
			}
		}
	}
	for _, cat := range []domain.PackCategory{domain.CategorySkills, domain.CategoryAgents, domain.CategoryWorkflows, domain.CategoryHooks, domain.CategoryMCP} {
		if len(manifest.ContentIDs(cat)) != len(p.Components[cat]) {
			return fmt.Errorf("pack %q native %s inventory has no descriptors", packName, cat)
		}
	}
	if len(manifest.Rules)+len(manifest.Prompts) > 0 || (!claude && len(manifest.Agents) > 0) || manifest.Configs.HasAnyConfigs() {
		return fmt.Errorf("pack %q mixes unsupported portable content with a native plugin", packName)
	}
	return nil
}

func validateManifestContent(packName string, packRoot string, manifest PackManifest, kind domain.PackCategory, ids []string) error {
	for _, id := range ids {
		path := filepath.Join(packRoot, filepath.FromSlash(manifest.RelPath(kind, id)))
		if err := requireFile(path); err != nil {
			return fmt.Errorf("pack %q %s %q missing: %w", packName, kind.DirName(), id, err)
		}
	}
	return nil
}

func validatePackList(packName string, label string, items []string) error {
	seen := map[string]struct{}{}
	for _, raw := range items {
		v := strings.TrimSpace(raw)
		if v == "" {
			return fmt.Errorf("pack %q %s contains empty id", packName, label)
		}
		if _, ok := seen[v]; ok {
			return fmt.Errorf("pack %q %s contains duplicate id %q", packName, label, v)
		}
		seen[v] = struct{}{}
	}
	return nil
}

func validateConfigFileMap(packName, label, packRoot string, harnessMap map[string][]string) error {
	for harness, files := range harnessMap {
		h := strings.ToLower(strings.TrimSpace(harness))
		if h == "" {
			return fmt.Errorf("pack %q configs.%s contains empty harness key", packName, label)
		}
		for _, f := range files {
			name := strings.TrimSpace(f)
			if name == "" {
				return fmt.Errorf("pack %q configs.%s[%s] contains empty filename", packName, label, h)
			}
			path := filepath.Join(packRoot, "configs", h, filepath.FromSlash(name))
			if err := requireFile(path); err != nil {
				return fmt.Errorf("pack %q configs.%s[%s] missing %q: %w", packName, label, h, name, err)
			}
		}
	}
	return nil
}

// validateMCPServerName reads an MCP server JSON file and verifies that the
// "name" field inside it matches the manifest key. A mismatch causes the server
// to silently vanish from inventory during sync.
func validateMCPServerName(packName, manifestKey, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("pack %q mcp server %q: %w", packName, manifestKey, err)
	}
	var server domain.MCPServer
	if err := json.Unmarshal(b, &server); err != nil {
		return fmt.Errorf("pack %q mcp server %q: invalid JSON: %w", packName, manifestKey, err)
	}
	normalizedName := strings.ToLower(strings.TrimSpace(server.Name))
	normalizedKey := strings.ToLower(strings.TrimSpace(manifestKey))
	if normalizedName == "" {
		return fmt.Errorf("pack %q mcp server %q: missing \"name\" field in %s", packName, manifestKey, filepath.Base(path))
	}
	if normalizedName != normalizedKey {
		return fmt.Errorf("pack %q mcp server %q: name field is %q in %s (must match manifest key)", packName, manifestKey, server.Name, filepath.Base(path))
	}
	return nil
}

func requireFile(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("not a file: %s", path)
	}
	return nil
}
