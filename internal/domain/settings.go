package domain

// ConfigFile is a loaded file from a pack's configs/ directory.
// Used for both base settings templates and plugin configs.
type ConfigFile struct {
	Filename   string // e.g. "opencode.json", "oh-my-opencode.json"
	Content    []byte // raw file bytes (JSON or TOML template)
	SourcePack string // pack that provided this file
}

// SettingsBundle holds loaded harness settings files per harness.
type SettingsBundle map[Harness][]ConfigFile

// PluginsBundle holds loaded harness plugin config files per harness.
type PluginsBundle map[Harness][]ConfigFile

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

// FileBytes returns the content of the named plugin file for a harness, or nil if not found.
func (b PluginsBundle) FileBytes(h Harness, filename string) []byte {
	for _, f := range b[h] {
		if f.Filename == filename {
			return f.Content
		}
	}
	return nil
}
