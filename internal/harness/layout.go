package harness

import (
	"encoding/json"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/util"
)

// StripManaged finds the OwnedFile matching path and strips managed keys
// from content. Returns content unchanged if no OwnedFile matches.
func (l Layout) StripManaged(content []byte, path string, ctx EditContext) ([]byte, error) {
	path = filepath.Clean(path)
	for _, of := range l.OwnedFiles {
		if filepath.Clean(of.Path) == path {
			return ApplyEdit(content, of.Format, ctx, of.Strip)
		}
	}
	return content, nil
}

// ApplyEdit parses content according to format, applies edit, and serializes.
// Empty input is treated as an empty document for both formats.
func ApplyEdit(content []byte, format FileFormat, ctx EditContext, edit func(root map[string]any, ctx EditContext)) ([]byte, error) {
	root := map[string]any{}
	switch format {
	case FormatJSON:
		if len(content) == 0 {
			content = []byte("{}")
		}
		if err := json.Unmarshal(content, &root); err != nil {
			return nil, err
		}
		edit(root, ctx)
		out, err := util.MarshalPrettyJSON(root)
		if err != nil {
			return nil, err
		}
		return out, nil
	case FormatTOML:
		if err := toml.Unmarshal(content, &root); err != nil {
			return nil, err
		}
		edit(root, ctx)
		out, err := toml.Marshal(root)
		if err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		return out, nil
	}
	return content, nil
}
