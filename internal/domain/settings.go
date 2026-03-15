package domain

// ConfigFile is a loaded file from a pack's configs/ directory.
type ConfigFile struct {
	Filename   string // e.g. "opencode.json"
	Content    []byte // raw file bytes (JSON or TOML template)
	SourcePack string // pack that provided this file
}

// SettingsBundle holds loaded harness config files per harness.
// Includes both base settings templates (merged via RenderBytes) and
// drop-in config files (copied as-is).
type SettingsBundle map[Harness][]ConfigFile

// FileBytes returns the content of the named file for a harness, or nil if not found.
func (b SettingsBundle) FileBytes(h Harness, filename string) []byte {
	for _, f := range b[h] {
		if f.Filename == filename {
			return f.Content
		}
	}
	return nil
}

// SourcePack returns the source pack of the named file for a harness, or "" if not found.
func (b SettingsBundle) SourcePack(h Harness, filename string) string {
	for _, f := range b[h] {
		if f.Filename == filename {
			return f.SourcePack
		}
	}
	return ""
}

// DropInFiles returns all config files for a harness except the named base file.
// These are drop-in configs that get deployed as-is without transformation.
func (b SettingsBundle) DropInFiles(h Harness, baseFile string) []ConfigFile {
	var result []ConfigFile
	for _, f := range b[h] {
		if f.Filename != baseFile {
			result = append(result, f)
		}
	}
	return result
}
