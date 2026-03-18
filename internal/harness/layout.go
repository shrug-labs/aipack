package harness

import (
	"encoding/json"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// StripManaged finds the OwnedFile matching path and strips managed keys
// from content. Returns content unchanged if no OwnedFile matches.
func (l Layout) StripManaged(content []byte, path string) ([]byte, error) {
	path = filepath.Clean(path)
	for _, of := range l.OwnedFiles {
		if filepath.Clean(of.Path) == path {
			return ApplyEdit(content, of.Format, of.Strip)
		}
	}
	return content, nil
}

// ApplyEdit parses content according to format, applies edit, and serializes.
// Empty input is treated as an empty document for both formats.
func ApplyEdit(content []byte, format FileFormat, edit func(map[string]any)) ([]byte, error) {
	root := map[string]any{}
	switch format {
	case FormatJSON:
		if len(content) == 0 {
			content = []byte("{}")
		}
		if err := json.Unmarshal(content, &root); err != nil {
			return nil, err
		}
		edit(root)
		out, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(out, '\n'), nil
	case FormatTOML:
		if err := toml.Unmarshal(content, &root); err != nil {
			return nil, err
		}
		edit(root)
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
