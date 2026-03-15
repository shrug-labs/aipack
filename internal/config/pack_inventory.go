package config

import (
	"encoding/json"
	"fmt"
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
	if err := validatePackList(packName, "prompts", manifest.Prompts); err != nil {
		return err
	}

	if err := validateManifestContent(packName, packRoot, domain.CategoryRules, manifest.Rules); err != nil {
		return err
	}
	if err := validateManifestContent(packName, packRoot, domain.CategoryAgents, manifest.Agents); err != nil {
		return err
	}
	if err := validateManifestContent(packName, packRoot, domain.CategoryWorkflows, manifest.Workflows); err != nil {
		return err
	}
	if err := validateManifestContent(packName, packRoot, domain.CategorySkills, manifest.Skills); err != nil {
		return err
	}
	for _, id := range manifest.Prompts {
		path := filepath.Join(packRoot, "prompts", id+".md")
		if err := requireFile(path); err != nil {
			return fmt.Errorf("pack %q prompts %q missing: %w", packName, id, err)
		}
	}
	for name := range manifest.MCP.Servers {
		path := filepath.Join(packRoot, "mcp", name+".json")
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

func validateManifestContent(packName string, packRoot string, kind domain.PackCategory, ids []string) error {
	for _, id := range ids {
		path := filepath.Join(packRoot, filepath.FromSlash(kind.PrimaryRelPath(id)))
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
