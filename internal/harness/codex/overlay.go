package codex

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
	harnesspkg "github.com/shrug-labs/aipack/internal/harness"
)

func (Harness) EmptyManagedOverlay() []byte {
	return nil
}

func (Harness) PruneMCPServersFromManagedOverlay(overlay []byte, serverNames map[string]struct{}) ([]byte, bool, error) {
	if len(serverNames) == 0 || len(strings.TrimSpace(string(overlay))) == 0 {
		return overlay, false, nil
	}
	root := map[string]any{}
	if err := toml.Unmarshal(overlay, &root); err != nil {
		return nil, false, fmt.Errorf("parse managed overlay TOML: %w", err)
	}
	changed := harnesspkg.DeleteMapKeys(root, "mcp_servers", serverNames)
	if !changed {
		return overlay, false, nil
	}
	out, err := toml.Marshal(root)
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}

func (Harness) RetainMCPServersInManagedOverlay(overlay []byte, serverNames map[string]struct{}) ([]byte, error) {
	if len(serverNames) == 0 || len(strings.TrimSpace(string(overlay))) == 0 {
		return nil, nil
	}
	root := map[string]any{}
	if err := toml.Unmarshal(overlay, &root); err != nil {
		return nil, fmt.Errorf("parse managed overlay TOML: %w", err)
	}
	out := map[string]any{}
	if kept, ok := harnesspkg.RetainMapKeys(root["mcp_servers"], serverNames); ok {
		out["mcp_servers"] = kept
	}
	if len(out) == 0 {
		return nil, nil
	}
	b, err := toml.Marshal(out)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
