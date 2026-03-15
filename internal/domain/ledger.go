package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"
)

// Entry records metadata about a file managed by a sync operation.
type Entry struct {
	Digest         string  `json:"digest"`
	SyncedAtEpochS int64   `json:"synced_at_epoch_s"`
	MTimeEpochS    float64 `json:"mtime_epoch_s"`
	SourcePack     string  `json:"source_pack,omitempty"`
	ManagedOverlay []byte  `json:"managed_overlay,omitempty"` // managed-only content for three-way merge
}

// Ledger tracks managed files across sync operations.
type Ledger struct {
	Managed   map[string]Entry `json:"managed"`
	UpdatedAt int64            `json:"-"` // epoch seconds from updated_at_epoch_s; 0 if absent
}

// NewLedger creates an empty ledger.
func NewLedger() Ledger {
	return Ledger{Managed: map[string]Entry{}}
}

// PrevDigest returns the previous digest for a managed path, or "" if not found.
func (l Ledger) PrevDigest(dst string) string {
	if e, ok := l.Managed[filepath.Clean(dst)]; ok {
		return e.Digest
	}
	return ""
}

// PrevManagedOverlay returns the managed overlay bytes from the last sync, or nil.
func (l Ledger) PrevManagedOverlay(dst string) []byte {
	if e, ok := l.Managed[filepath.Clean(dst)]; ok {
		return e.ManagedOverlay
	}
	return nil
}

// Record updates the ledger with a new entry, computing the digest from content.
// now is the current wall-clock time (caller supplies time.Now()).
// mtime is best-effort — stat failure results in mtime=0, not an error.
func (l *Ledger) Record(dst string, content []byte, sourcePack string, managedOverlay []byte, now time.Time) {
	if l.Managed == nil {
		l.Managed = map[string]Entry{}
	}
	l.Managed[filepath.Clean(dst)] = Entry{
		Digest:         SingleFileDigest(content),
		SyncedAtEpochS: now.Unix(),
		MTimeEpochS:    bestEffortMtime(dst),
		SourcePack:     sourcePack,
		ManagedOverlay: managedOverlay,
	}
}

// UpdateMetadata updates SourcePack and ManagedOverlay for a file without
// re-hashing. Used for DiffIdentical files where content didn't change but
// provenance may have (e.g., a rule moved between packs).
func (l *Ledger) UpdateMetadata(dst, sourcePack string, managedOverlay []byte, now time.Time) {
	key := filepath.Clean(dst)
	e, ok := l.Managed[key]
	if !ok {
		return
	}
	e.SourcePack = sourcePack
	if managedOverlay != nil {
		e.ManagedOverlay = managedOverlay
	}
	e.SyncedAtEpochS = now.Unix()
	l.Managed[key] = e
}

// Delete removes a ledger entry (used by prune).
func (l *Ledger) Delete(dst string) {
	delete(l.Managed, filepath.Clean(dst))
}

// SingleFileDigest computes the pathDigest-compatible digest for a single file
// from in-memory content. This wraps the raw content hash in the composite
// format used by pathDigest (hash of "." + \0 + file_hash + \n), which is
// how v1 stores digests in the ledger. Using this from in-memory content
// avoids the v1 pattern of re-reading from disk after writing.
func SingleFileDigest(content []byte) string {
	raw := sha256.Sum256(content)
	fileHash := hex.EncodeToString(raw[:])
	h := sha256.New()
	_, _ = h.Write([]byte("."))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(fileHash))
	_, _ = h.Write([]byte("\n"))
	return hex.EncodeToString(h.Sum(nil))
}

// bestEffortMtime returns the file's mtime as fractional epoch seconds,
// or 0.0 if stat fails.
func bestEffortMtime(path string) float64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return float64(st.ModTime().UnixNano()) / 1e9
}
