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
	MergeAdd      MergeAction = "add"
	MergeUpdate   MergeAction = "update"
	MergeRemove   MergeAction = "remove"
	MergeReset    MergeAction = "reset"    // on-disk file was corrupted; replaced with managed state
	MergeConflict MergeAction = "conflict" // managed scalar dropped — user value preserved
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
//   - Scalars / other values: managed value updates only when disk still
//     matches the previous managed value. User-owned/user-edited scalar
//     values are preserved.
//   - First sync (prevManaged is nil): all on-disk items are treated as user-added
//     and preserved. All managed items are added. Equivalent to additive deep merge.
//
// Format dispatch is intentionally a switch on harness type rather than a
// SettingsFormat() interface method — acceptable complexity for 4 harnesses.
func mergeSettingsKeys(existing, prevManaged, newManaged []byte, harness domain.Harness, additiveOnly bool) ([]byte, []MergeOp, error) {
	switch harness {
	case domain.HarnessClaudeCode:
		return threeWayMergeClaudeJSON(existing, prevManaged, newManaged, additiveOnly)
	case domain.HarnessOpenCode, domain.HarnessCline:
		return threeWayMergeJSON(existing, prevManaged, newManaged, additiveOnly)
	case domain.HarnessCodex:
		return threeWayMergeTOML(existing, prevManaged, newManaged, additiveOnly)
	default:
		return nil, nil, fmt.Errorf("unsupported harness for merge: %s", harness)
	}
}

func threeWayMergeJSON(onDisk, prevManaged, newManaged []byte, additiveOnly bool) ([]byte, []MergeOp, error) {
	return threeWayMergeJSONWithOptions(onDisk, prevManaged, newManaged, additiveOnly, mergeOptions{})
}

func threeWayMergeClaudeJSON(onDisk, prevManaged, newManaged []byte, additiveOnly bool) ([]byte, []MergeOp, error) {
	return threeWayMergeJSONWithOptions(onDisk, prevManaged, newManaged, additiveOnly, mergeOptions{
		ObjectArrayPath: isClaudeHookEventArrayPath,
	})
}

type mergeOptions struct {
	ObjectArrayPath func(path string) bool
}

func (o mergeOptions) isObjectArrayPath(path string) bool {
	return o.ObjectArrayPath != nil && o.ObjectArrayPath(path)
}

type mergeContext struct {
	additiveOnly bool
	options      mergeOptions
	ops          *[]MergeOp
}

func threeWayMergeJSONWithOptions(onDisk, prevManaged, newManaged []byte, additiveOnly bool, opts mergeOptions) ([]byte, []MergeOp, error) {
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

	threeWayMergeMap(disk, prev, next, "", mergeContext{additiveOnly: additiveOnly, options: opts, ops: &ops})

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
	threeWayMergeMap(disk, prev, next, "", mergeContext{additiveOnly: additiveOnly, ops: &ops})

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
func threeWayMergeMap(disk, prev, next map[string]any, prefix string, ctx mergeContext) {
	// Keys removed from managed: delete from disk.
	for k := range prev {
		if _, inNext := next[k]; !inNext {
			removeManagedValue(disk, k, prev[k], prefix+k, ctx)
		}
	}

	// Keys in new managed: add or update in disk.
	for k, nextVal := range next {
		diskVal, inDisk := disk[k]
		prevVal, inPrev := prev[k]

		if !inDisk {
			disk[k] = nextVal
			*ctx.ops = append(*ctx.ops, MergeOp{Key: prefix + k, Action: MergeAdd})
			continue
		}

		// Both disk and next have this key. Try recursive merge for objects.
		if nextMap, ok := nextVal.(map[string]any); ok {
			prevMap, _ := prevVal.(map[string]any)
			if prevMap == nil {
				prevMap = map[string]any{}
			}
			if diskMap, ok := diskVal.(map[string]any); ok {
				threeWayMergeMap(diskMap, prevMap, nextMap, prefix+k+".", ctx)
				continue
			}
		}

		// Try three-way merge for arrays.
		if nextArr, ok := nextVal.([]any); ok {
			prevArr, _ := prevVal.([]any)
			if diskArr, ok := diskVal.([]any); ok {
				path := prefix + k
				keyer := arrayKeyerForPath(ctx.options, path)
				merged := threeWayMergeArrayByKey(diskArr, prevArr, nextArr, keyer)
				disk[k] = merged
				// Suppress the MergeUpdate signal when the managed set is
				// unchanged and the only difference between the on-disk array
				// and the merged result is ordering — without this guard every
				// sync emits a phantom "update" each time the user reorders a
				// set-like array (e.g. permissions.allow). The rewrite itself
				// still happens (positional arrays like command args need
				// managed-order-first), but the noise on the log is gone.
				if !sameElementSetByKey(diskArr, merged, keyer) {
					*ctx.ops = append(*ctx.ops, MergeOp{Key: path, Action: MergeUpdate})
				}
				continue
			}
		}

		// Scalar or type change: apply the managed value only when disk still
		// has the previous managed value. If there was no previous managed value,
		// the on-disk scalar is user-owned and must be preserved. If the user has
		// edited the managed scalar since the last sync, preserve that edit too.
		if !inPrev {
			// First-time collision: a managed value is being introduced for a
			// key the user already has. Preserve the user's value but emit a
			// MergeConflict op so the surrounding UI surfaces the rejection
			// rather than silently dropping the pack's intent.
			if !reflect.DeepEqual(diskVal, nextVal) {
				*ctx.ops = append(*ctx.ops, MergeOp{Key: prefix + k, Action: MergeConflict})
			}
			continue
		}
		if !reflect.DeepEqual(diskVal, prevVal) {
			// User edited a previously-managed scalar; preserve their edit but
			// surface that the pack's new value did not land.
			if !reflect.DeepEqual(diskVal, nextVal) {
				*ctx.ops = append(*ctx.ops, MergeOp{Key: prefix + k, Action: MergeConflict})
			}
			continue
		}
		if reflect.DeepEqual(diskVal, nextVal) {
			continue
		}
		disk[k] = nextVal
		*ctx.ops = append(*ctx.ops, MergeOp{Key: prefix + k, Action: MergeUpdate})
	}

	// Keys only on disk (not in prev or next): user-added, preserved automatically.
}

func removeManagedValue(disk map[string]any, key string, prevVal any, path string, ctx mergeContext) {
	if ctx.additiveOnly || isPluginAdditivePath(path) {
		return
	}
	diskVal, inDisk := disk[key]
	if !inDisk {
		return
	}

	if prevMap, ok := prevVal.(map[string]any); ok {
		if diskMap, ok := diskVal.(map[string]any); ok {
			for child, childPrev := range prevMap {
				removeManagedValue(diskMap, child, childPrev, path+"."+child, ctx)
			}
			if len(diskMap) == 0 {
				delete(disk, key)
				*ctx.ops = append(*ctx.ops, MergeOp{Key: path, Action: MergeRemove})
			}
			return
		}
	}

	if prevArr, ok := prevVal.([]any); ok {
		if diskArr, ok := diskVal.([]any); ok {
			filtered, changed := removeManagedArrayValuesByKey(diskArr, prevArr, arrayKeyerForPath(ctx.options, path))
			if !changed {
				if reflect.DeepEqual(diskVal, prevVal) {
					delete(disk, key)
					*ctx.ops = append(*ctx.ops, MergeOp{Key: path, Action: MergeRemove})
				}
				return
			}
			if len(filtered) == 0 {
				delete(disk, key)
				*ctx.ops = append(*ctx.ops, MergeOp{Key: path, Action: MergeRemove})
			} else {
				disk[key] = filtered
				*ctx.ops = append(*ctx.ops, MergeOp{Key: path, Action: MergeUpdate})
			}
			return
		}
	}

	if reflect.DeepEqual(diskVal, prevVal) {
		delete(disk, key)
		*ctx.ops = append(*ctx.ops, MergeOp{Key: path, Action: MergeRemove})
	}
}

type arrayElementKeyer func(any) (string, bool)

func arrayKeyerForPath(opts mergeOptions, path string) arrayElementKeyer {
	if opts.isObjectArrayPath(path) {
		return canonicalArrayElementKey
	}
	return stringArrayElementKey
}

func stringArrayElementKey(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func canonicalArrayElementKey(v any) (string, bool) {
	return canonicalJSONKey(v)
}

func removeManagedArrayValuesByKey(disk, prev []any, keyer arrayElementKeyer) ([]any, bool) {
	prevSet := arrayElementSet(prev, keyer)
	if len(prevSet) == 0 {
		return disk, false
	}
	out := make([]any, 0, len(disk))
	changed := false
	for _, v := range disk {
		key, ok := keyer(v)
		if ok && prevSet[key] {
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

// threeWayMergeArrayByKey merges managed arrays with managed-order-first
// semantics. Managed items appear first in the managed (next) order; user-only
// items from disk are appended afterward. Elements that the keyer cannot
// identify are treated as user-owned values and preserved.
func threeWayMergeArrayByKey(disk, prev, next []any, keyer arrayElementKeyer) []any {
	prevSet := arrayElementSet(prev, keyer)
	nextSet := arrayElementSet(next, keyer)
	seen := map[string]bool{}
	// Non-nil so an empty merge (both arrays empty, or all items cancel out)
	// marshals to JSON `[]`, not `null` — e.g. `permissions.allow: null` breaks
	// Claude Code startup.
	result := []any{}

	// Pass 1: managed items in managed order.
	for _, v := range next {
		key, ok := keyer(v)
		if !ok {
			result = append(result, v)
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, v)
	}

	// Pass 2: user-only items from disk (not in prev or next), preserving
	// their relative disk order.
	for _, v := range disk {
		key, ok := keyer(v)
		if !ok {
			result = append(result, v)
			continue
		}
		if seen[key] {
			continue
		}
		if prevSet[key] && !nextSet[key] {
			continue // removed from managed set
		}
		seen[key] = true
		result = append(result, v)
	}

	return result
}

// sameElementSet reports whether two arrays contain the same multiset of
// elements regardless of order.
func sameElementSetByKey(a, b []any, keyer arrayElementKeyer) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[string]int{}
	for _, v := range a {
		k, ok := keyer(v)
		if !ok {
			return false
		}
		counts[k]++
	}
	for _, v := range b {
		k, ok := keyer(v)
		if !ok {
			return false
		}
		counts[k]--
		if counts[k] < 0 {
			return false
		}
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}

func arrayElementSet(arr []any, keyer arrayElementKeyer) map[string]bool {
	m := map[string]bool{}
	for _, v := range arr {
		key, ok := keyer(v)
		if ok {
			m[key] = true
		}
	}
	return m
}

func canonicalJSONKey(v any) (string, bool) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func isClaudeHookEventArrayPath(path string) bool {
	path = strings.TrimSuffix(path, ".")
	if !strings.HasPrefix(path, "hooks.") {
		return false
	}
	return !strings.Contains(strings.TrimPrefix(path, "hooks."), ".")
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
