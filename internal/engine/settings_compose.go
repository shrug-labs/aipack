package engine

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
)

// composeConfigFiles deep-merges base template files from multiple packs
// for the same harness + filename. First pack in the slice wins at leaf
// value conflicts. A warning is emitted for each conflict.
//
// Returns the merged content and any conflict warnings.
func composeConfigFiles(files []domain.ConfigFile) ([]byte, []domain.Warning, error) {
	if len(files) == 0 {
		return nil, nil, nil
	}
	if len(files) == 1 {
		return files[0].Content, nil, nil
	}

	format := detectFormat(files[0].Filename)
	parse := parseJSONMap
	marshal := marshalJSON
	if format == "toml" {
		parse = parseTOMLMap
		marshal = marshalTOML
	}

	base, err := parse(files[0].Content)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s from pack %q: %w", files[0].Filename, files[0].SourcePack, err)
	}

	var warnings []domain.Warning
	for _, f := range files[1:] {
		overlay, err := parse(f.Content)
		if err != nil {
			return nil, warnings, fmt.Errorf("parse %s from pack %q: %w", f.Filename, f.SourcePack, err)
		}
		w := deepMergeFirstWins(base, overlay, f.SourcePack, files[0].SourcePack, files[0].Filename, "")
		warnings = append(warnings, w...)
	}

	out, err := marshal(base)
	if err != nil {
		return nil, warnings, err
	}
	return out, warnings, nil
}

// deepMergeFirstWins merges overlay into base. base values win at leaf conflicts.
// Warnings are emitted for each conflict identifying the packs and key path.
func deepMergeFirstWins(base, overlay map[string]any, overlayPack, basePack, filename, prefix string) []domain.Warning {
	var warnings []domain.Warning
	for k, overlayVal := range overlay {
		baseVal, exists := base[k]
		if !exists {
			base[k] = overlayVal
			continue
		}

		if baseMap, ok := baseVal.(map[string]any); ok {
			if overlayMap, ok := overlayVal.(map[string]any); ok {
				w := deepMergeFirstWins(baseMap, overlayMap, overlayPack, basePack, filename, prefix+k+".")
				warnings = append(warnings, w...)
				continue
			}
		}

		if !reflect.DeepEqual(baseVal, overlayVal) {
			warnings = append(warnings, domain.Warning{
				Message: fmt.Sprintf("settings key %q in %s: pack %q value kept, pack %q value skipped (reorder packs in profile to change priority)",
					prefix+k, filename, basePack, overlayPack),
			})
		}
	}
	return warnings
}

func detectFormat(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == ".toml" {
		return "toml"
	}
	return "json"
}
