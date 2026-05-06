package claudecode

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shrug-labs/aipack/internal/engine"
	harnesspkg "github.com/shrug-labs/aipack/internal/harness"
)

func (Harness) EmptyManagedOverlay() []byte {
	return []byte("{}\n")
}

func (Harness) PruneMCPServersFromManagedOverlay(overlay []byte, serverNames map[string]struct{}) ([]byte, bool, error) {
	if len(serverNames) == 0 || len(strings.TrimSpace(string(overlay))) == 0 {
		return overlay, false, nil
	}
	root := map[string]any{}
	if err := json.Unmarshal(overlay, &root); err != nil {
		return nil, false, fmt.Errorf("parse managed overlay JSON: %w", err)
	}
	changed := harnesspkg.DeleteMapKeys(root, "mcpServers", serverNames)
	changed = removeMCPPermissions(root, serverNames) || changed
	if !changed {
		return overlay, false, nil
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}

func (Harness) RetainMCPServersInManagedOverlay(overlay []byte, serverNames map[string]struct{}) ([]byte, error) {
	if len(serverNames) == 0 || len(strings.TrimSpace(string(overlay))) == 0 {
		return []byte("{}\n"), nil
	}
	root := map[string]any{}
	if err := json.Unmarshal(overlay, &root); err != nil {
		return nil, fmt.Errorf("parse managed overlay JSON: %w", err)
	}
	out := map[string]any{}
	if kept, ok := harnesspkg.RetainMapKeys(root["mcpServers"], serverNames); ok {
		out["mcpServers"] = kept
	}
	if perms, ok := retainMCPPermissions(root["permissions"], serverNames); ok {
		out["permissions"] = perms
	}
	if len(out) == 0 {
		return []byte("{}\n"), nil
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func removeMCPPermissions(root map[string]any, serverNames map[string]struct{}) bool {
	perms, ok := root["permissions"].(map[string]any)
	if !ok {
		return false
	}
	changed := false
	for _, key := range []string{"allow", "deny"} {
		filtered, didChange := filterMCPPermissions(perms[key], serverNames)
		if !didChange {
			continue
		}
		changed = true
		if len(filtered) == 0 && key == "deny" {
			delete(perms, key)
			continue
		}
		perms[key] = filtered
	}
	return changed
}

func retainMCPPermissions(value any, serverNames map[string]struct{}) (map[string]any, bool) {
	perms, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	out := map[string]any{}
	for _, key := range []string{"allow", "deny"} {
		filtered := retainMCPPermissionItems(perms[key], serverNames)
		if len(filtered) > 0 {
			out[key] = filtered
		}
	}
	return out, len(out) > 0
}

func filterMCPPermissions(value any, serverNames map[string]struct{}) ([]any, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]any, 0, len(items))
	changed := false
	for _, item := range items {
		s, ok := item.(string)
		if ok && isMCPPermissionForServer(s, serverNames) {
			changed = true
			continue
		}
		out = append(out, item)
	}
	return out, changed
}

func retainMCPPermissionItems(value any, serverNames map[string]struct{}) []any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if ok && isMCPPermissionForServer(s, serverNames) {
			out = append(out, item)
		}
	}
	return out
}

func isMCPPermissionForServer(permission string, serverNames map[string]struct{}) bool {
	for name := range serverNames {
		prefix := "mcp__" + engine.NormalizeServerName(name) + "__"
		if strings.HasPrefix(permission, prefix) {
			return true
		}
	}
	return false
}
