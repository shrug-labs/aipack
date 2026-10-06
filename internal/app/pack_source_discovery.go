package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/plugin"
)

var pluginCatalogPaths = []string{".agents/plugins/marketplace.json", ".agents/plugins/api_marketplace.json", ".claude-plugin/marketplace.json"}

// Local checkouts and unpacked marketplaces retain their catalog identity.
func discoverLocalPackPlugin(payload string) (*domain.PluginSource, error) {
	if _, err := os.Stat(filepath.Join(payload, "pack.json")); err == nil {
		return nil, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for root := payload; ; root = filepath.Dir(root) {
		for _, path := range pluginCatalogPaths {
			if _, err := os.Stat(filepath.Join(root, path)); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return nil, err
			}
			rel, err := filepath.Rel(root, payload)
			if err != nil {
				return nil, err
			}
			spec, err := discoverPackPlugin(root, rel, config.RegistrySourceEntry{URL: root})
			if err != nil || spec != nil && spec.MarketplacePath != "" {
				return spec, err
			}
			break
		}
		if filepath.Dir(root) == root {
			break
		}
	}
	return discoverPackPlugin(payload, "", config.RegistrySourceEntry{URL: payload})
}

// Direct imports prefer the colocated catalog's identity and policy. A
// standalone plugin uses its authored name as its generated marketplace name.
func discoverPackPlugin(root, subpath string, source config.RegistrySourceEntry) (*domain.PluginSource, error) {
	payload := filepath.Join(root, subpath)
	if _, err := os.Stat(filepath.Join(payload, "pack.json")); err == nil {
		return nil, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, path := range pluginCatalogPaths {
		data, err := config.ReadRepositoryFile(root, path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		coordinates := source
		coordinates.Path = path
		reg, err := config.ParseRegistrySource(data, coordinates)
		if err != nil {
			return nil, err
		}
		var matches []string
		for name, entry := range reg.Packs {
			if entry.Plugin != nil && entry.Repo == source.URL && filepath.Clean(entry.Path) == filepath.Clean(subpath) {
				matches = append(matches, name)
			}
		}
		if len(matches) > 1 {
			slices.Sort(matches)
			return nil, fmt.Errorf("plugin path %q has multiple catalog entries: %v; install by catalog name", subpath, matches)
		}
		if len(matches) == 1 {
			entry := reg.Packs[matches[0]]
			if entry.Unsupported != "" {
				return nil, fmt.Errorf("plugin %q: %s", matches[0], entry.Unsupported)
			}
			return entry.Plugin, nil
		}
	}
	for _, manifest := range []struct{ path, format string }{
		{"plugin.json", plugin.AgentPlugins},
		{".codex-plugin/plugin.json", plugin.CodexLegacy},
		{".claude-plugin/plugin.json", plugin.Claude},
	} {
		data, err := config.ReadRepositoryFile(root, filepath.Join(subpath, manifest.path))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var header struct{ Name string }
		if err := json.Unmarshal(data, &header); err != nil || !domain.ValidNativeName(header.Name) {
			return nil, fmt.Errorf("invalid plugin name in %s", manifest.path)
		}
		if manifest.format != plugin.Claude {
			if _, err := os.Stat(filepath.Join(payload, ".claude-plugin/plugin.json")); err == nil {
				return nil, fmt.Errorf("plugin has both Claude and Codex manifests; install through its catalog to choose a format")
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
		return &domain.PluginSource{Format: manifest.format, Name: header.Name, Marketplace: header.Name,
			MarketplaceURL: source.URL, MarketplaceRef: source.Ref,
			Entry: map[string]any{"name": header.Name, "source": "./" + filepath.ToSlash(filepath.Clean(subpath))}}, nil
	}
	return nil, nil
}
