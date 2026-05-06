package cline

import (
	"encoding/json"
	"fmt"
	"strings"

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
	if len(out) == 0 {
		return []byte("{}\n"), nil
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
