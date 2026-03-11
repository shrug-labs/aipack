package engine

import (
	"encoding/json"
	"fmt"

	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/domain"
)

// MergeSettingsKeys performs a three-way merge of managed content into existing
// settings. prevManaged is the managed overlay from the previous sync (nil on
// first sync). The format (JSON or TOML) is determined by the harness type.
//
// Three-way merge semantics:
//   - Objects: recurse by key. Keys removed from managed are deleted. Keys
//     added to managed are inserted. Keys only on disk are preserved (user-added).
//   - Arrays of strings: items removed from the managed set are deleted. Items
//     added are appended. Items only on disk are preserved. Duplicates are removed.
//   - Scalars / other values: managed value wins.
//   - First sync (prevManaged is nil): all on-disk items are treated as user-added
//     and preserved. All managed items are added. Equivalent to additive deep merge.
//
// Format dispatch is intentionally a switch on harness type rather than a
// SettingsFormat() interface method — acceptable complexity for 4 harnesses.
func mergeSettingsKeys(existing, prevManaged, newManaged []byte, harness domain.Harness) ([]byte, error) {
	switch harness {
	case domain.HarnessOpenCode, domain.HarnessCline, domain.HarnessClaudeCode:
		return threeWayMergeJSON(existing, prevManaged, newManaged)
	case domain.HarnessCodex:
		return threeWayMergeTOML(existing, prevManaged, newManaged)
	default:
		return nil, fmt.Errorf("unsupported harness for merge: %s", harness)
	}
}

func threeWayMergeJSON(onDisk, prevManaged, newManaged []byte) ([]byte, error) {
	disk, err := parseJSONMap(onDisk)
	if err != nil {
		return nil, fmt.Errorf("parse on-disk JSON: %w", err)
	}
	prev, err := parseJSONMap(prevManaged)
	if err != nil {
		return nil, fmt.Errorf("parse prev-managed JSON: %w", err)
	}
	next, err := parseJSONMap(newManaged)
	if err != nil {
		return nil, fmt.Errorf("parse new-managed JSON: %w", err)
	}

	threeWayMergeMap(disk, prev, next)

	out, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func threeWayMergeTOML(onDisk, prevManaged, newManaged []byte) ([]byte, error) {
	disk, err := parseTOMLMap(onDisk)
	if err != nil {
		return nil, fmt.Errorf("parse on-disk TOML: %w", err)
	}
	prev, err := parseTOMLMap(prevManaged)
	if err != nil {
		return nil, fmt.Errorf("parse prev-managed TOML: %w", err)
	}
	next, err := parseTOMLMap(newManaged)
	if err != nil {
		return nil, fmt.Errorf("parse new-managed TOML: %w", err)
	}

	threeWayMergeMap(disk, prev, next)

	out, err := toml.Marshal(disk)
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// threeWayMergeMap recursively merges next (new managed) into disk, using prev
// (old managed) to distinguish user-added keys from stale managed keys.
func threeWayMergeMap(disk, prev, next map[string]any) {
	// Keys removed from managed: delete from disk.
	for k := range prev {
		if _, inNext := next[k]; !inNext {
			delete(disk, k)
		}
	}

	// Keys in new managed: add or update in disk.
	for k, nextVal := range next {
		diskVal, inDisk := disk[k]
		prevVal, _ := prev[k]

		if !inDisk {
			disk[k] = nextVal
			continue
		}

		// Both disk and next have this key. Try recursive merge for objects.
		if nextMap, ok := nextVal.(map[string]any); ok {
			prevMap, _ := prevVal.(map[string]any)
			if prevMap == nil {
				prevMap = map[string]any{}
			}
			if diskMap, ok := diskVal.(map[string]any); ok {
				threeWayMergeMap(diskMap, prevMap, nextMap)
				continue
			}
		}

		// Try three-way merge for arrays.
		if nextArr, ok := nextVal.([]any); ok {
			prevArr, _ := prevVal.([]any)
			if diskArr, ok := diskVal.([]any); ok {
				disk[k] = threeWayMergeArray(diskArr, prevArr, nextArr)
				continue
			}
		}

		// Scalar or type change: managed value wins.
		disk[k] = nextVal
	}

	// Keys only on disk (not in prev or next): user-added, preserved automatically.
}

// threeWayMergeArray merges managed string arrays using set semantics.
func threeWayMergeArray(disk, prev, next []any) []any {
	prevSet := toStringSet(prev)
	nextSet := toStringSet(next)

	seen := map[string]bool{}
	var result []any

	for _, v := range disk {
		s, ok := v.(string)
		if !ok {
			result = append(result, v)
			continue
		}
		if prevSet[s] && !nextSet[s] {
			continue // removed from managed set
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		result = append(result, v)
	}

	for _, v := range next {
		s, ok := v.(string)
		if !ok {
			result = append(result, v)
			continue
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		result = append(result, v)
	}

	return result
}

func toStringSet(arr []any) map[string]bool {
	m := map[string]bool{}
	for _, v := range arr {
		if s, ok := v.(string); ok {
			m[s] = true
		}
	}
	return m
}

func parseJSONMap(b []byte) (map[string]any, error) {
	if len(b) == 0 {
		return map[string]any{}, nil
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func parseTOMLMap(b []byte) (map[string]any, error) {
	if len(b) == 0 {
		return map[string]any{}, nil
	}
	m := map[string]any{}
	if err := toml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}
