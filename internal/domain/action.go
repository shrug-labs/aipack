package domain

// WriteAction represents a file to be written with in-memory content.
type WriteAction struct {
	Dst        string // target file path
	Content    []byte // full content to write
	SourcePack string // pack that produced this write
	Src        string // set by Capture (abs path read from); empty for Plan writes

	// IsContent marks this write as pack content (agents, workflows) rather
	// than a settings file. Content writes are saved directly without
	// StripManagedSettings processing.
	IsContent bool

	// SourceDigest is the digest of the on-disk source file before any format
	// transformation (e.g., promoted SKILL.md → re-rendered agent.md). When
	// set, round-trip change detection uses this instead of hashing Content,
	// keeping the ledger consistent with what sync records.
	SourceDigest string
}

// EffectiveDigest returns the digest to use for ledger tracking. For content
// writes with a SourceDigest, it returns SourceDigest (the on-disk promoted
// file hash). Otherwise it hashes Content directly.
func (w WriteAction) EffectiveDigest() string {
	if w.IsContent && w.SourceDigest != "" {
		return w.SourceDigest
	}
	return SingleFileDigest(w.Content)
}

// CopyAction represents a file or directory to be copied from source to destination.
type CopyAction struct {
	Src        string   // source path (pack file or directory)
	Dst        string   // destination path
	Kind       CopyKind // file or dir
	SourcePack string   // pack provenance
}

// SettingsAction represents a declarative settings file sync with optional merge mode.
// When MergeMode is true, Desired contains only managed keys and the apply layer
// merges these into the existing on-disk file rather than replacing it entirely.
type SettingsAction struct {
	Dst            string  // target file path
	Desired        []byte  // fully rendered content (or managed-keys-only when MergeMode)
	ManagedOverlay []byte  // managed-only subset for ledger (pre-computed at plan time)
	Harness        Harness // which harness this belongs to
	Label          string  // human label, e.g. "opencode.json"
	SourcePack     string  // harness settings source pack name
	MergeMode      bool    // when true, merge managed keys into existing file
}

// MCPAction represents a single MCP server as a first-class sync unit.
// ConfigPath points to the harness config file that embeds or stores it.
type MCPAction struct {
	Name         string
	ConfigPath   string
	Content      []byte
	SourcePack   string
	Harness      Harness
	Embedded     bool
	AllowedTools []string
}

// LedgerKey returns the synthetic ledger key for this MCP server.
func (m MCPAction) LedgerKey() string {
	return MCPLedgerKey(m.ConfigPath, m.Name)
}
