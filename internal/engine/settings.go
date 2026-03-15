package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// loadHarnessSettings loads all harness config files (both settings templates
// and drop-in configs) from packs. Files from harness_settings and
// harness_plugins are merged into a single bundle.
func loadHarnessSettings(packs []config.ResolvedPack, settingsPack string, harnesses []domain.Harness) (domain.SettingsBundle, []domain.Warning, error) {
	return loadHarnessFileBundle(packs, settingsPack, harnesses, func(m config.PackManifest, h string) []string {
		files := append([]string{}, m.Configs.HarnessSettings[h]...)
		files = append(files, m.Configs.HarnessPlugins[h]...)
		return files
	}, "settings")
}

// SettingsDecision describes how a harness should emit settings/MCP actions.
type SettingsDecision struct {
	EmitSettings bool // true if full settings merge should be emitted
	EmitMCP      bool // true if MCP-only action should be emitted
	MergeMode    bool // true if managed-keys-only merge (skipSettings mode)
}

// ClassifySettings determines how settings should be emitted for a harness.
//
// Decision tree:
//
//	!skipSettings && (hasMCP || hasManagedContent) → EmitSettings (full merge)
//	skipSettings && (hasMCP || hasManagedContent) → EmitMCP with MergeMode (managed keys only)
//	neither → nothing
func ClassifySettings(hasMCP, hasManagedContent, skipSettings bool) SettingsDecision {
	if !skipSettings && (hasMCP || hasManagedContent) {
		return SettingsDecision{EmitSettings: true}
	}
	if skipSettings && (hasMCP || hasManagedContent) {
		return SettingsDecision{EmitMCP: true, MergeMode: true}
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
