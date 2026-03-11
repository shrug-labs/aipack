package domain

import (
	"strings"
)

// Harness identifies a supported AI harness.
type Harness string

const (
	HarnessClaudeCode Harness = "claudecode"
	HarnessOpenCode   Harness = "opencode"
	HarnessCodex      Harness = "codex"
	HarnessCline      Harness = "cline"
)

var allHarnesses = []Harness{HarnessCline, HarnessClaudeCode, HarnessCodex, HarnessOpenCode}

// AllHarnesses returns a copy of the known harness list.
func AllHarnesses() []Harness {
	out := make([]Harness, len(allHarnesses))
	copy(out, allHarnesses)
	return out
}

// HarnessNames returns the string names of all harnesses.
func HarnessNames() []string {
	out := make([]string, len(allHarnesses))
	for i, h := range allHarnesses {
		out[i] = string(h)
	}
	return out
}

// HarnessNamesJoined returns harness names joined by sep.
func HarnessNamesJoined(sep string) string {
	return strings.Join(HarnessNames(), sep)
}

// ParseHarness parses a raw string into a Harness, returning false if unknown.
func ParseHarness(raw string) (Harness, bool) {
	want := strings.ToLower(strings.TrimSpace(raw))
	if want == "" {
		return "", false
	}
	for _, h := range allHarnesses {
		if want == string(h) {
			return h, true
		}
	}
	return "", false
}

// Scope identifies project vs global sync scope.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeGlobal  Scope = "global"
)

// CopyKind distinguishes file from directory copy actions.
type CopyKind string

const (
	CopyKindFile CopyKind = "file"
	CopyKindDir  CopyKind = "dir"
)

// DiffKind classifies a file against on-disk state.
type DiffKind string

const (
	DiffCreate    DiffKind = "create"    // file doesn't exist on disk
	DiffIdentical DiffKind = "identical" // desired == on-disk
	DiffManaged   DiffKind = "managed"   // on-disk matches ledger digest (safe to update)
	DiffConflict  DiffKind = "conflict"  // on-disk modified by user since last sync
)
