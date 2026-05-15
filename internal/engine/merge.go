package engine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// MergeAction describes what the three-way merge did to a key.
type MergeAction string

const (
	MergeAdd    MergeAction = "add"
	MergeUpdate MergeAction = "update"
	MergeRemove MergeAction = "remove"
	MergeReset  MergeAction = "reset" // on-disk file was corrupted; replaced with managed state
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
//   - Arrays of strings: managed items appear first in managed order. Items
//     removed from the managed set are deleted. User-only items from disk are
//     appended. Duplicates are removed.
//   - Scalars / other values: managed value wins.
//   - First sync (prevManaged is nil): all on-disk items are treated as user-added
//     and preserved. All managed items are added. Equivalent to additive deep merge.
//
// Format dispatch is intentionally a switch on harness type rather than a
// SettingsFormat() interface method — acceptable complexity for 4 harnesses.
func mergeSettingsKeys(existing, prevManaged, newManaged []byte, harness domain.Harness, additiveOnly bool) ([]byte, []MergeOp, error) {
	switch harness {
	case domain.HarnessOpenCode, domain.HarnessCline, domain.HarnessClaudeCode:
		return threeWayMergeJSON(existing, prevManaged, newManaged, additiveOnly)
	case domain.HarnessCodex:
		return threeWayMergeTOML(existing, prevManaged, newManaged, additiveOnly)
	default:
		return nil, nil, fmt.Errorf("unsupported harness for merge: %s", harness)
	}
}

func threeWayMergeJSON(onDisk, prevManaged, newManaged []byte, additiveOnly bool) ([]byte, []MergeOp, error) {
	var ops []MergeOp
	disk, err := parseJSONMap(onDisk)
	if err != nil {
		// Corrupted on-disk file — treat as empty so managed state wins.
		disk = map[string]any{}
		ops = append(ops, MergeOp{Key: "(root)", Action: MergeReset})
	}
	prev, err := parseJSONMap(prevManaged)
	if err != nil {
		return nil, nil, fmt.Errorf("parse prev-managed JSON: %w", err)
	}
	next, err := parseJSONMap(newManaged)
	if err != nil {
		return nil, nil, fmt.Errorf("parse new-managed JSON: %w", err)
	}

	threeWayMergeMap(disk, prev, next, "", additiveOnly, &ops)

	out, err := marshalJSON(disk)
	if err != nil {
		return nil, nil, err
	}
	return out, ops, nil
}

func threeWayMergeTOML(onDisk, prevManaged, newManaged []byte, additiveOnly bool) ([]byte, []MergeOp, error) {
	var ops []MergeOp
	disk, err := parseTOMLMap(onDisk)
	if err != nil {
		// Corrupted on-disk file — treat as empty so managed state wins.
		disk = map[string]any{}
		ops = append(ops, MergeOp{Key: "(root)", Action: MergeReset})
	}
	prev, err := parseTOMLMap(prevManaged)
	if err != nil {
		return nil, nil, fmt.Errorf("parse prev-managed TOML: %w", err)
	}
	next, err := parseTOMLMap(newManaged)
	if err != nil {
		return nil, nil, fmt.Errorf("parse new-managed TOML: %w", err)
	}

	pruneRemovedCodexMCPServerTables(disk, prev, next, &ops)
	threeWayMergeMap(disk, prev, next, "", additiveOnly, &ops)

	out, err := marshalTOML(disk)
	if err != nil {
		return nil, nil, err
	}
	return out, ops, nil
}

func pruneRemovedCodexMCPServerTables(disk, prev, next map[string]any, ops *[]MergeOp) {
	diskServers, ok := disk["mcp_servers"].(map[string]any)
	if !ok {
		return
	}
	prevServers, ok := prev["mcp_servers"].(map[string]any)
	if !ok {
		return
	}
	nextServers, _ := next["mcp_servers"].(map[string]any)
	changed := false
	for name := range prevServers {
		if _, stillManaged := nextServers[name]; stillManaged {
			continue
		}
		if _, onDisk := diskServers[name]; !onDisk {
			continue
		}
		delete(diskServers, name)
		changed = true
		*ops = append(*ops, MergeOp{Key: "mcp_servers." + name, Action: MergeRemove})
	}
	if !changed {
		return
	}
	if len(diskServers) == 0 {
		delete(disk, "mcp_servers")
		return
	}
	disk["mcp_servers"] = diskServers
}

// threeWayMergeMap recursively merges next (new managed) into disk, using prev
// (old managed) to distinguish user-added keys from stale managed keys.
// Merge operations are appended to ops with dot-separated key paths.
func threeWayMergeMap(disk, prev, next map[string]any, prefix string, additiveOnly bool, ops *[]MergeOp) {
	// Keys removed from managed: delete from disk.
	for k := range prev {
		if _, inNext := next[k]; !inNext {
			removeManagedValue(disk, k, prev[k], prefix+k, additiveOnly, ops)
		}
	}

	// Keys in new managed: add or update in disk.
	for k, nextVal := range next {
		diskVal, inDisk := disk[k]
		prevVal := prev[k]

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
				threeWayMergeMap(diskMap, prevMap, nextMap, prefix+k+".", additiveOnly, ops)
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

func removeManagedValue(disk map[string]any, key string, prevVal any, path string, additiveOnly bool, ops *[]MergeOp) {
	if additiveOnly || isPluginAdditivePath(path) {
		return
	}
	diskVal, inDisk := disk[key]
	if !inDisk {
		return
	}

	if prevMap, ok := prevVal.(map[string]any); ok {
		if diskMap, ok := diskVal.(map[string]any); ok {
			for child, childPrev := range prevMap {
				removeManagedValue(diskMap, child, childPrev, path+"."+child, additiveOnly, ops)
			}
			if len(diskMap) == 0 {
				delete(disk, key)
				*ops = append(*ops, MergeOp{Key: path, Action: MergeRemove})
			}
			return
		}
	}

	if prevArr, ok := prevVal.([]any); ok {
		if diskArr, ok := diskVal.([]any); ok {
			filtered, changed := removeManagedArrayValues(diskArr, prevArr)
			if !changed {
				if reflect.DeepEqual(diskVal, prevVal) {
					delete(disk, key)
					*ops = append(*ops, MergeOp{Key: path, Action: MergeRemove})
				}
				return
			}
			if len(filtered) == 0 {
				delete(disk, key)
				*ops = append(*ops, MergeOp{Key: path, Action: MergeRemove})
			} else {
				disk[key] = filtered
				*ops = append(*ops, MergeOp{Key: path, Action: MergeUpdate})
			}
			return
		}
	}

	if reflect.DeepEqual(diskVal, prevVal) {
		delete(disk, key)
		*ops = append(*ops, MergeOp{Key: path, Action: MergeRemove})
	}
}

func removeManagedArrayValues(disk, prev []any) ([]any, bool) {
	prevSet := toStringSet(prev)
	if len(prevSet) == 0 {
		return disk, false
	}
	out := make([]any, 0, len(disk))
	changed := false
	for _, v := range disk {
		s, ok := v.(string)
		if ok && prevSet[s] {
			changed = true
			continue
		}
		out = append(out, v)
	}
	return out, changed
}

func isPluginAdditivePath(path string) bool {
	path = strings.TrimSuffix(path, ".")
	return path == "plugins" || strings.HasPrefix(path, "plugins.") ||
		path == "enabledPlugins" || strings.HasPrefix(path, "enabledPlugins.") ||
		path == "extraKnownMarketplaces" || strings.HasPrefix(path, "extraKnownMarketplaces.")
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

// threeWayMergeArray merges managed string arrays with managed-order-first
// semantics. Managed items appear first in the managed (next) order; user-only
// items from disk are appended afterward. This ensures positional arrays like
// command args always reflect the managed order, while still preserving
// user-added items. For set-like arrays (e.g. enabled_tools) the reorder is
// harmless because item order has no semantic meaning.
func threeWayMergeArray(disk, prev, next []any) []any {
	prevSet := toStringSet(prev)
	nextSet := toStringSet(next)

	seen := map[string]bool{}
	var result []any

	// Pass 1: managed items in managed order.
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

	// Pass 2: user-only items from disk (not in prev or next), preserving
	// their relative disk order.
	for _, v := range disk {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if seen[s] {
			continue
		}
		if prevSet[s] && !nextSet[s] {
			continue // removed from managed set
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

func marshalJSON(m map[string]any) ([]byte, error) {
	return util.MarshalPrettyJSON(m)
}

func marshalTOML(m map[string]any) ([]byte, error) {
	return toml.Marshal(m)
}
