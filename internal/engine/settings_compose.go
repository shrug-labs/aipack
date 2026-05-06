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

	parse, marshal, err := codecFor(files[0].Filename)
	if err != nil {
		return nil, nil, err
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

// expandConfigFileRefs resolves {env:*}, {params.*}, and {pack:root} refs in
// a settings template. Files without refs short-circuit to the original bytes
// (no parse, no re-format); files with refs are parsed, walked, and
// re-marshalled, which loses comments and key ordering.
func expandConfigFileRefs(params map[string]string, packRoot string, file domain.ConfigFile) ([]byte, error) {
	return expandConfigFileRefsWithEnv(params, nil, packRoot, file)
}

func expandConfigFileRefsWithEnv(params map[string]string, env map[string]string, packRoot string, file domain.ConfigFile) ([]byte, error) {
	if !hasTemplateRefs(string(file.Content)) {
		return file.Content, nil
	}

	parse, marshal, err := codecFor(file.Filename)
	if err != nil {
		return nil, err
	}

	root, err := parse(file.Content)
	if err != nil {
		return nil, err
	}
	_, changed, err := expandConfigValueRefsWithEnv(params, env, packRoot, root, "")
	if err != nil {
		return nil, err
	}
	if !changed {
		return file.Content, nil
	}
	return marshal(root)
}

func expandConfigValueRefsWithEnv(params map[string]string, env map[string]string, packRoot string, value any, path string) (any, bool, error) {
	switch v := value.(type) {
	case string:
		if !hasTemplateRefs(v) {
			return v, false, nil
		}
		out, err := expandTemplateRefsWithEnv(params, env, packRoot, v)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", path, err)
		}
		return out, out != v, nil
	case map[string]any:
		changed := false
		for k, child := range v {
			next, childChanged, err := expandConfigValueRefsWithEnv(params, env, packRoot, child, joinConfigPath(path, k))
			if err != nil {
				return nil, false, err
			}
			v[k] = next
			changed = changed || childChanged
		}
		return v, changed, nil
	case []any:
		changed := false
		for i, child := range v {
			next, childChanged, err := expandConfigValueRefsWithEnv(params, env, packRoot, child, joinConfigIndex(path, i))
			if err != nil {
				return nil, false, err
			}
			v[i] = next
			changed = changed || childChanged
		}
		return v, changed, nil
	default:
		return value, false, nil
	}
}

func joinConfigPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func joinConfigIndex(prefix string, i int) string {
	return fmt.Sprintf("%s[%d]", prefix, i)
}

// deepMergeFirstWins merges overlay into base. base values win at leaf conflicts.
// Warnings are emitted for each conflict identifying the packs and key path.
func deepMergeFirstWins(base, overlay map[string]any, overlayPack, basePack, filename, path string) []domain.Warning {
	var warnings []domain.Warning
	for k, overlayVal := range overlay {
		keyPath := joinConfigPath(path, k)
		baseVal, exists := base[k]
		if !exists {
			base[k] = overlayVal
			continue
		}

		if baseMap, ok := baseVal.(map[string]any); ok {
			if overlayMap, ok := overlayVal.(map[string]any); ok {
				w := deepMergeFirstWins(baseMap, overlayMap, overlayPack, basePack, filename, keyPath)
				warnings = append(warnings, w...)
				continue
			}
		}

		if !reflect.DeepEqual(baseVal, overlayVal) {
			warnings = append(warnings, domain.Warning{
				Message: fmt.Sprintf("settings key %q in %s: pack %q value kept, pack %q value skipped (reorder packs in profile to change priority)",
					keyPath, filename, basePack, overlayPack),
			})
		}
	}
	return warnings
}

// codecFor returns the parse and marshal helpers for the format implied by
// the filename's extension. Unsupported extensions are an explicit error so
// a future YAML/etc. settings file doesn't get silently parsed as JSON.
func codecFor(filename string) (parse func([]byte) (map[string]any, error), marshal func(map[string]any) ([]byte, error), err error) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".json":
		return parseJSONMap, marshalJSON, nil
	case ".toml":
		return parseTOMLMap, marshalTOML, nil
	default:
		return nil, nil, fmt.Errorf("unsupported settings file extension for %q (expected .json or .toml)", filename)
	}
}
