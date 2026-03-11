package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// loadHarnessSettings loads harness settings files from packs based on the
// resolved configuration. Returns typed ConfigFile structs with SourcePack
// set at load time (no retroactive attribution).
func loadHarnessSettings(packs []config.ResolvedPack, settingsPack string, harnesses []domain.Harness) (domain.SettingsBundle, []domain.Warning, error) {
	return loadHarnessFileBundle(packs, settingsPack, harnesses, func(m config.PackManifest, h string) []string {
		return m.Configs.HarnessSettings[h]
	}, "settings")
}

// loadHarnessPlugins loads harness plugin config files from packs.
func loadHarnessPlugins(packs []config.ResolvedPack, settingsPack string, harnesses []domain.Harness) (domain.PluginsBundle, []domain.Warning, error) {
	b, warnings, err := loadHarnessFileBundle(packs, settingsPack, harnesses, func(m config.PackManifest, h string) []string {
		return m.Configs.HarnessPlugins[h]
	}, "plugins")
	return domain.PluginsBundle(b), warnings, err
}

// SettingsDecision describes how a harness should emit settings/plugin actions.
type SettingsDecision struct {
	EmitSettings  bool // true if full settings merge should be emitted
	EmitMCPPlugin bool // true if MCP-only plugin action should be emitted
	MergeMode     bool // true if managed-keys-only merge (skipSettings mode)
}

// ClassifySettings determines how settings should be emitted for a harness.
//
// Decision tree:
//
//	!skipSettings && hasManagedContent → EmitSettings (full merge)
//	skipSettings && hasManagedContent → EmitMCPPlugin with MergeMode (managed keys only)
//	hasMCP (regardless of skipSettings) → EmitMCPPlugin (MCP is never gated by skipSettings)
//	neither → nothing
func ClassifySettings(hasMCP, hasManagedContent, skipSettings bool) SettingsDecision {
	if !skipSettings && (hasMCP || hasManagedContent) {
		return SettingsDecision{EmitSettings: true}
	}
	if skipSettings && (hasMCP || hasManagedContent) {
		return SettingsDecision{EmitMCPPlugin: true, MergeMode: true}
	}
	return SettingsDecision{}
}

func loadHarnessFileBundle(
	packs []config.ResolvedPack,
	settingsPack string,
	harnesses []domain.Harness,
	filesFor func(config.PackManifest, string) []string,
	label string,
) (domain.SettingsBundle, []domain.Warning, error) {
	if len(packs) == 0 {
		return nil, nil, errors.New("no packs resolved")
	}
	if settingsPack == "" {
		return nil, nil, nil
	}
	var warnings []domain.Warning
	bundle := domain.SettingsBundle{}
	for _, h := range harnesses {
		p, err := config.HarnessSettingsPackForHarness(packs, settingsPack, string(h))
		if err != nil {
			// Not every harness has settings files — skip silently.
			continue
		}
		files := filesFor(p.Manifest, string(h))
		if len(files) == 0 {
			continue
		}
		configs := make([]domain.ConfigFile, 0, len(files))
		for _, f := range files {
			b, err := os.ReadFile(filepath.Join(p.Root, "configs", string(h), f))
			if err != nil {
				return nil, warnings, fmt.Errorf("loading harness %s %s/%s: %w", label, h, f, err)
			}
			configs = append(configs, domain.ConfigFile{
				Filename:   f,
				Content:    b,
				SourcePack: p.Name,
			})
		}
		bundle[h] = configs
	}
	return bundle, warnings, nil
}
