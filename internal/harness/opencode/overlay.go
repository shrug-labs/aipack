package opencode

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
	changed := harnesspkg.DeleteMapKeys(root, "mcp", serverNames)
	changed = removeMCPTools(root, serverNames) || changed
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
	if kept, ok := harnesspkg.RetainMapKeys(root["mcp"], serverNames); ok {
		out["mcp"] = kept
	}
	if tools, ok := retainMCPTools(root["tools"], serverNames); ok {
		out["tools"] = tools
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

func removeMCPTools(root map[string]any, serverNames map[string]struct{}) bool {
	tools, ok := root["tools"].(map[string]any)
	if !ok {
		return false
	}
	changed := false
	for key := range tools {
		for name := range serverNames {
			prefix := engine.NormalizeServerName(name) + "_"
			if strings.HasPrefix(key, prefix) {
				delete(tools, key)
				changed = true
				break
			}
		}
	}
	return changed
}

func retainMCPTools(value any, serverNames map[string]struct{}) (map[string]any, bool) {
	tools, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	out := map[string]any{}
	for key, val := range tools {
		for name := range serverNames {
			prefix := engine.NormalizeServerName(name) + "_"
			if strings.HasPrefix(key, prefix) {
				out[key] = val
				break
			}
		}
	}
	return out, len(out) > 0
}
