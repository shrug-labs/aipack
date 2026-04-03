package engine

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// loadHarnessSettings loads all harness config files from all contributing packs.
// Base settings templates (HarnessSettings) are deep-merged across packs with
// first-pack-wins semantics. Drop-in plugins (HarnessPlugins) are NOT merged —
// same-filename collisions across packs produce an error.
func (e *Engine) loadHarnessSettings(packs []config.ResolvedPack, settingsPacks []string, harnesses []domain.Harness) (domain.SettingsBundle, []domain.Warning, error) {
	return e.loadHarnessFileBundle(packs, settingsPacks, harnesses, func(m config.PackManifest, h string) []string {
		return m.Configs.HarnessSettings[h]
	}, func(m config.PackManifest, h string) []string {
		return m.Configs.HarnessPlugins[h]
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

// settingsFileGroup collects ConfigFile entries for the same filename across packs.
type settingsFileGroup struct {
	configs []domain.ConfigFile
}

func (e *Engine) loadHarnessFileBundle(
	packs []config.ResolvedPack,
	settingsPacks []string,
	harnesses []domain.Harness,
	settingsFor func(config.PackManifest, string) []string,
	pluginsFor func(config.PackManifest, string) []string,
	label string,
) (domain.SettingsBundle, []domain.Warning, error) {
	if len(packs) == 0 {
		return nil, nil, errors.New("no packs resolved")
	}
	if len(settingsPacks) == 0 {
		return nil, nil, nil
	}
	var warnings []domain.Warning
	bundle := domain.SettingsBundle{}
	for _, h := range harnesses {
		contributing := config.HarnessSettingsPacksForHarness(packs, settingsPacks, string(h))
		if len(contributing) == 0 {
			continue
		}

		// --- Base settings templates: deep-merge across packs ---
		byFilename := map[string]*settingsFileGroup{}
		var filenameOrder []string

		for _, p := range contributing {
			for _, f := range settingsFor(p.Manifest, string(h)) {
				b, err := e.FS.ReadFile(filepath.Join(p.Root, "configs", string(h), f))
				if err != nil {
					return nil, warnings, fmt.Errorf("loading harness %s %s/%s: %w", label, h, f, err)
				}
				entry, ok := byFilename[f]
				if !ok {
					entry = &settingsFileGroup{}
					byFilename[f] = entry
					filenameOrder = append(filenameOrder, f)
				}
				entry.configs = append(entry.configs, domain.ConfigFile{
					Filename:   f,
					Content:    b,
					SourcePack: p.Name,
				})
			}
		}

		var configs []domain.ConfigFile
		for _, f := range filenameOrder {
			entry := byFilename[f]
			if len(entry.configs) == 1 {
				configs = append(configs, entry.configs[0])
				continue
			}
			merged, mergeWarnings, err := composeConfigFiles(entry.configs)
			if err != nil {
				return nil, warnings, fmt.Errorf("merging %s settings %s/%s: %w", label, h, f, err)
			}
			warnings = append(warnings, mergeWarnings...)
			configs = append(configs, domain.ConfigFile{
				Filename:   f,
				Content:    merged,
				SourcePack: entry.configs[0].SourcePack,
			})
		}

		// --- Plugins: no merge, collision = error ---
		pluginSeen := map[string]string{} // filename → source pack
		for _, p := range contributing {
			for _, f := range pluginsFor(p.Manifest, string(h)) {
				if prev, ok := pluginSeen[f]; ok {
					return nil, warnings, fmt.Errorf("harness plugin %q for %s provided by both %q and %q — plugins cannot be merged", f, h, prev, p.Name)
				}
				pluginSeen[f] = p.Name
				b, err := e.FS.ReadFile(filepath.Join(p.Root, "configs", string(h), f))
				if err != nil {
					return nil, warnings, fmt.Errorf("loading harness %s plugin %s/%s: %w", label, h, f, err)
				}
				configs = append(configs, domain.ConfigFile{
					Filename:   f,
					Content:    b,
					SourcePack: p.Name,
				})
			}
		}

		bundle[h] = configs
	}
	return bundle, warnings, nil
}
