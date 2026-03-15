package engine

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/domain"
)

// MergeAction describes what the three-way merge did to a key.
type MergeAction string

const (
	MergeAdd    MergeAction = "add"
	MergeUpdate MergeAction = "update"
	MergeRemove MergeAction = "remove"
)

// MergeOp records a single merge decision at a specific key path.
type MergeOp struct {
	Key    string      // dot-separated key path (e.g. "mcpServers.atlassian")
	Action MergeAction // what the merge did
}

// mergeSettingsKeys performs a three-way merge of managed content into existing
// settings. prevManaged is the managed overlay from the previous sync (nil on
// first sync). The format (JSON or TOML) is determined by the harness type.
//
// Returns the merged content and a list of merge operations describing what changed.
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
func mergeSettingsKeys(existing, prevManaged, newManaged []byte, harness domain.Harness) ([]byte, []MergeOp, error) {
	switch harness {
	case domain.HarnessOpenCode, domain.HarnessCline, domain.HarnessClaudeCode:
		return threeWayMergeJSON(existing, prevManaged, newManaged)
	case domain.HarnessCodex:
		return threeWayMergeTOML(existing, prevManaged, newManaged)
	default:
		return nil, nil, fmt.Errorf("unsupported harness for merge: %s", harness)
	}
}

func threeWayMergeJSON(onDisk, prevManaged, newManaged []byte) ([]byte, []MergeOp, error) {
	disk, err := parseJSONMap(onDisk)
	if err != nil {
		return nil, nil, fmt.Errorf("parse on-disk JSON: %w", err)
	}
	prev, err := parseJSONMap(prevManaged)
	if err != nil {
		return nil, nil, fmt.Errorf("parse prev-managed JSON: %w", err)
	}
	next, err := parseJSONMap(newManaged)
	if err != nil {
		return nil, nil, fmt.Errorf("parse new-managed JSON: %w", err)
	}

	var ops []MergeOp
	threeWayMergeMap(disk, prev, next, "", &ops)

	out, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return append(out, '\n'), ops, nil
}

func threeWayMergeTOML(onDisk, prevManaged, newManaged []byte) ([]byte, []MergeOp, error) {
	disk, err := parseTOMLMap(onDisk)
	if err != nil {
		return nil, nil, fmt.Errorf("parse on-disk TOML: %w", err)
	}
	prev, err := parseTOMLMap(prevManaged)
	if err != nil {
		return nil, nil, fmt.Errorf("parse prev-managed TOML: %w", err)
	}
	next, err := parseTOMLMap(newManaged)
	if err != nil {
		return nil, nil, fmt.Errorf("parse new-managed TOML: %w", err)
	}

	var ops []MergeOp
	threeWayMergeMap(disk, prev, next, "", &ops)

	out, err := toml.Marshal(disk)
	if err != nil {
		return nil, nil, err
	}
	return out, ops, nil
}

// threeWayMergeMap recursively merges next (new managed) into disk, using prev
// (old managed) to distinguish user-added keys from stale managed keys.
// Merge operations are appended to ops with dot-separated key paths.
func threeWayMergeMap(disk, prev, next map[string]any, prefix string, ops *[]MergeOp) {
	// Keys removed from managed: delete from disk.
	for k := range prev {
		if _, inNext := next[k]; !inNext {
			if _, inDisk := disk[k]; inDisk {
				*ops = append(*ops, MergeOp{Key: prefix + k, Action: MergeRemove})
			}
			delete(disk, k)
		}
	}

	// Keys in new managed: add or update in disk.
	for k, nextVal := range next {
		diskVal, inDisk := disk[k]
		prevVal, _ := prev[k]

		if !inDisk {
			disk[k] = nextVal
			*ops = append(*ops, MergeOp{Key: prefix + k, Action: MergeAdd})
			continue
		}

		// Both disk and next have this key. Try recursive merge for objects.
		if nextMap, ok := nextVal.(map[string]any); ok {
			prevMap, _ := prevVal.(map[string]any)
			if prevMap == nil {
				prevMap = map[string]any{}
			}
			if diskMap, ok := diskVal.(map[string]any); ok {
				threeWayMergeMap(diskMap, prevMap, nextMap, prefix+k+".", ops)
				continue
			}
		}

		// Try three-way merge for arrays.
		if nextArr, ok := nextVal.([]any); ok {
			prevArr, _ := prevVal.([]any)
			if diskArr, ok := diskVal.([]any); ok {
				merged := threeWayMergeArray(diskArr, prevArr, nextArr)
				disk[k] = merged
				if !sameStringSet(prevArr, nextArr) {
					*ops = append(*ops, MergeOp{Key: prefix + k, Action: MergeUpdate})
				}
				continue
			}
		}

		// Scalar or type change: managed value wins unconditionally.
		// This means user edits to managed scalars are overwritten on next sync.
		// This is intentional — scalars don't have set-merge semantics, so the
		// managed value is the only authoritative source. Users who need custom
		// scalar values should add them as new keys (which are preserved).
		disk[k] = nextVal
		if !reflect.DeepEqual(diskVal, nextVal) {
			*ops = append(*ops, MergeOp{Key: prefix + k, Action: MergeUpdate})
		}
	}

	// Keys only on disk (not in prev or next): user-added, preserved automatically.
}

// sameStringSet checks whether two arrays contain the same set of string items.
func sameStringSet(a, b []any) bool {
	sa := toStringSet(a)
	sb := toStringSet(b)
	if len(sa) != len(sb) {
		return false
	}
	for k := range sa {
		if !sb[k] {
			return false
		}
	}
	return true
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
