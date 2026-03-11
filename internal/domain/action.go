package domain

// WriteAction represents a file to be written with in-memory content.
type WriteAction struct {
	Dst        string // target file path
	Content    []byte // full content to write
	SourcePack string // pack that produced this write
	Src        string // set by Capture (abs path read from); empty for Plan writes
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
