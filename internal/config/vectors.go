package config

import (
	"os"
	"sort"
	"strings"
)

// SelectionsToVector converts a set of selected items from an inventory into
// the most compact VectorSelector representation.
func SelectionsToVector(inventory, selected []string) VectorSelector {
	if len(inventory) == 0 {
		return VectorSelector{}
	}
	selectedSet := ToStringSet(selected)
	allSelected := true
	noneSelected := len(selected) == 0
	for _, id := range inventory {
		if !selectedSet[id] {
			allSelected = false
		}
	}
	if allSelected {
		return VectorSelector{} // nil,nil = all
	}
	if noneSelected {
		empty := []string{}
		return VectorSelector{Include: &empty}
	}
	// Partial: use whichever list is shorter.
	sorted := append([]string{}, selected...)
	sort.Strings(sorted)
	excluded := make([]string, 0)
	for _, id := range inventory {
		if !selectedSet[id] {
			excluded = append(excluded, id)
		}
	}
	sort.Strings(excluded)
	if len(sorted) <= len(excluded) {
		return VectorSelector{Include: &sorted}
	}
	return VectorSelector{Exclude: &excluded}
}

// ResolveCurrentVector returns the currently selected items for a vector,
// resolving the VectorSelector against the manifest inventory.
func ResolveCurrentVector(inventory []string, sel VectorSelector) []string {
	if sel.Include != nil {
		return append([]string{}, *sel.Include...)
	}
	if sel.Exclude != nil {
		excludeSet := ToStringSet(*sel.Exclude)
		out := make([]string, 0)
		for _, id := range inventory {
			if !excludeSet[id] {
				out = append(out, id)
			}
		}
		return out
	}
	return append([]string{}, inventory...)
}

// MCPToConfig converts selected MCP servers and tool allowlists back to
// profile config format.
func MCPToConfig(manifest PackManifest, enabledServers []string, allowedTools map[string][]string) map[string]MCPServerConfig {
	out := map[string]MCPServerConfig{}
	enabledSet := ToStringSet(enabledServers)

	for name, defaults := range manifest.MCP.Servers {
		if !enabledSet[name] {
			out[name] = MCPServerConfig{Enabled: BoolPtr(false)}
			continue
		}
		tools := allowedTools[name]
		if StringSlicesEqual(tools, defaults.DefaultAllowedTools) {
			out[name] = MCPServerConfig{Enabled: BoolPtr(true)}
		} else {
			sorted := append([]string{}, tools...)
			sort.Strings(sorted)
			out[name] = MCPServerConfig{Enabled: BoolPtr(true), AllowedTools: sorted}
		}
	}
	return out
}

// PackEnabled returns whether a pack is enabled (nil defaults to true).
func PackEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

// SettingsEnabled returns whether settings sync is enabled (nil defaults to false).
func SettingsEnabled(v *bool) bool {
	return v != nil && *v
}

// BoolPtr returns a pointer to a bool value.
func BoolPtr(v bool) *bool {
	return &v
}

// VectorEqual returns true if two VectorSelectors are equivalent.
func VectorEqual(a, b VectorSelector) bool {
	return ptrSliceEqual(a.Include, b.Include) && ptrSliceEqual(a.Exclude, b.Exclude)
}

// MCPConfigEqual returns true if two MCP config maps are equivalent.
func MCPConfigEqual(a, b map[string]MCPServerConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if !ptrBoolEqual(av.Enabled, bv.Enabled) {
			return false
		}
		if !StringSlicesEqual(av.AllowedTools, bv.AllowedTools) {
			return false
		}
		if !StringSlicesEqual(av.DisabledTools, bv.DisabledTools) {
			return false
		}
	}
	return true
}

// ToStringSet converts a string slice to a set (map[string]bool).
func ToStringSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// StringSlicesEqual returns true if two string slices contain the same
// elements (order-insensitive).
func StringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aSorted := append([]string{}, a...)
	bSorted := append([]string{}, b...)
	sort.Strings(aSorted)
	sort.Strings(bSorted)
	for i := range aSorted {
		if aSorted[i] != bSorted[i] {
			return false
		}
	}
	return true
}

func ptrSliceEqual(a, b *[]string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return StringSlicesEqual(*a, *b)
}

func ptrBoolEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// ListProfileNames returns the sorted list of profile names (without .yaml extension)
// found in the given directory.
func ListProfileNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	sort.Strings(names)
	return names, nil
}
