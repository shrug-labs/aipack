package harness

// DeleteMapKeys deletes named keys from root[key].
func DeleteMapKeys(root map[string]any, key string, names map[string]struct{}) bool {
	child, ok := root[key].(map[string]any)
	if !ok {
		return false
	}
	changed := false
	for name := range names {
		if _, ok := child[name]; ok {
			delete(child, name)
			changed = true
		}
	}
	return changed
}

// RetainMapKeys returns a copy of value containing only named keys.
func RetainMapKeys(value any, names map[string]struct{}) (map[string]any, bool) {
	child, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	out := map[string]any{}
	for name := range names {
		if val, ok := child[name]; ok {
			out[name] = val
		}
	}
	return out, len(out) > 0
}

// PruneMapKeys removes the named entries from root[key], preserving any other
// (user-added) entries. If pruning empties the child map, the key itself is
// removed so no empty managed container is left behind. It only touches the
// file when it actually removes a managed entry, so an empty name set — or a
// child holding only user entries — leaves the document unchanged. Returns
// true if anything changed.
func PruneMapKeys(root map[string]any, key string, names map[string]struct{}) bool {
	if !DeleteMapKeys(root, key, names) {
		return false
	}
	if child, ok := root[key].(map[string]any); ok && len(child) == 0 {
		delete(root, key)
	}
	return true
}
