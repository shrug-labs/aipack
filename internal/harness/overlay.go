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
