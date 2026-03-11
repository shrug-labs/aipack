package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

// FileState classifies a harness file's relationship to its source pack.
type FileState int

const (
	FileClean     FileState = iota // tracked, unchanged
	FileModified                   // tracked, harness content changed since sync
	FileConflict                   // tracked, both harness and pack changed
	FileUntracked                  // not in ledger
	FileSettings                   // settings file with changes
)

// HarnessFile describes a single file in harness locations with its state.
type HarnessFile struct {
	HarnessPath string // absolute path on disk
	RelPath     string // relative path within category (e.g. "triage" for rules/triage.md)
	Category    string // rules, agents, workflows, skills, mcp, settings
	State       FileState
	PackName    string // source pack (empty for untracked)
	PackPath    string // destination path in pack (empty for untracked)
	Size        int64
	Kind        domain.CopyKind // file or dir
}

// InspectRequest holds parameters for inspecting harness file state.
type InspectRequest struct {
	TargetSpec
	PackRoots map[string]string // pack name → resolved root
}

// InspectResult holds the full file inventory from an inspection.
type InspectResult struct {
	Files       []HarnessFile
	LedgerPath  string
	HasLedger   bool
	Warnings    []domain.Warning
	LedgerFiles int // total entries in ledger
}

// InspectHarness captures all harness content and classifies every file
// against the ledger, returning a complete file inventory.
func InspectHarness(req InspectRequest, reg *harness.Registry) (InspectResult, error) {
	home := req.Home
	var result InspectResult

	ledgerPath := ledgerPathForScope(req.Scope, req.ProjectDir, home, req.Harnesses)
	result.LedgerPath = ledgerPath

	lg, _, err := engine.LoadLedger(ledgerPath)
	if err != nil {
		return result, fmt.Errorf("loading ledger: %w", err)
	}
	result.HasLedger = len(lg.Managed) > 0
	result.LedgerFiles = len(lg.Managed)

	ctx := harness.CaptureContext{Scope: req.Scope, ProjectDir: req.ProjectDir, Home: home}

	for _, hid := range req.Harnesses {
		h, err := reg.Lookup(hid)
		if err != nil {
			return result, err
		}
		res, err := h.Capture(ctx)
		if err != nil {
			return result, err
		}
		result.Warnings = append(result.Warnings, res.Warnings...)

		// Content files (rules, agents, workflows, skills).
		for _, c := range res.Copies {
			src := filepath.Clean(c.Src)
			fi := HarnessFile{
				HarnessPath: src,
				RelPath:     filepath.Base(c.Dst),
				Category:    categoryFromDst(c.Dst),
				Kind:        c.Kind,
				Size:        fileOrDirSize(src, c.Kind),
			}

			// For directories (skills), the ledger tracks individual files
			// within the directory, not the directory itself. Classify by
			// checking each child file against the ledger.
			if c.Kind == domain.CopyKindDir {
				state, packName := classifyDirCopy(src, lg, req.PackRoots)
				fi.State = state
				fi.PackName = packName
				if packRoot, ok := req.PackRoots[fi.PackName]; ok {
					fi.PackPath = filepath.Join(packRoot, filepath.FromSlash(c.Dst))
				}
				result.Files = append(result.Files, fi)
				continue
			}

			entry, tracked := lg.Managed[src]
			if !tracked {
				fi.State = FileUntracked
				result.Files = append(result.Files, fi)
				continue
			}

			fi.PackName = inferPackName(entry, req.PackRoots)

			packRoot, ok := req.PackRoots[fi.PackName]
			if ok {
				fi.PackPath = filepath.Join(packRoot, filepath.FromSlash(c.Dst))
			}

			// Check if harness content changed since last sync.
			srcContent, _ := os.ReadFile(src)

			changed, err := contentChangedSinceLedger(srcContent, src, entry.Digest, c.Kind)
			if err != nil {
				return result, err
			}

			if !changed {
				fi.State = FileClean
				result.Files = append(result.Files, fi)
				continue
			}

			// Harness changed — check if pack side also changed (conflict).
			if fi.PackPath != "" {
				if _, statErr := os.Stat(fi.PackPath); statErr == nil {
					packChanged, packErr := fileChangedSinceLedger(fi.PackPath, entry.Digest, c.Kind)
					if packErr != nil {
						return result, packErr
					}
					if packChanged {
						fi.State = FileConflict
						result.Files = append(result.Files, fi)
						continue
					}
				}
			}

			fi.State = FileModified
			result.Files = append(result.Files, fi)
		}

		// Settings files.
		for _, w := range res.Writes {
			if w.Src == "" {
				continue
			}
			src := filepath.Clean(w.Src)
			fi := HarnessFile{
				HarnessPath: src,
				RelPath:     filepath.Base(w.Dst),
				Category:    "settings",
				Kind:        domain.CopyKindFile,
				Size:        fileOrDirSize(src, domain.CopyKindFile),
			}

			entry, tracked := lg.Managed[src]
			if !tracked {
				fi.State = FileUntracked
				result.Files = append(result.Files, fi)
				continue
			}

			fi.PackName = inferPackName(entry, req.PackRoots)

			packRoot, ok := req.PackRoots[fi.PackName]
			if ok {
				fi.PackPath = filepath.Join(packRoot, filepath.FromSlash(w.Dst))
			}

			curDigest := domain.SingleFileDigest(w.Content)
			if curDigest == entry.Digest {
				fi.State = FileClean
			} else {
				fi.State = FileSettings
			}
			result.Files = append(result.Files, fi)
		}
	}

	// Sort: modified/conflict/settings first, then untracked, then clean.
	sort.Slice(result.Files, func(i, j int) bool {
		si, sj := StateSortKey(result.Files[i].State), StateSortKey(result.Files[j].State)
		if si != sj {
			return si < sj
		}
		return result.Files[i].HarnessPath < result.Files[j].HarnessPath
	})

	return result, nil
}

// StateSortKey maps FileState to display/sort priority (lower = higher priority).
func StateSortKey(s FileState) int {
	switch s {
	case FileConflict:
		return 0
	case FileModified:
		return 1
	case FileSettings:
		return 2
	case FileUntracked:
		return 3
	case FileClean:
		return 4
	default:
		return 5
	}
}

// inferPackName fills in the pack name from the single-pack shortcut when
// the ledger entry has no SourcePack recorded.
func inferPackName(entry domain.Entry, packRoots map[string]string) string {
	if entry.SourcePack != "" {
		return entry.SourcePack
	}
	if len(packRoots) == 1 {
		for k := range packRoots {
			return k
		}
	}
	return ""
}

// findChildEntry searches the ledger for any entry whose path is under dir.
// Used for CopyKindDir items (skills) where the ledger tracks individual files.
func findChildEntry(dir string, lg domain.Ledger) (domain.Entry, bool) {
	prefix := dir + string(filepath.Separator)
	for k, e := range lg.Managed {
		if strings.HasPrefix(k, prefix) {
			return e, true
		}
	}
	return domain.Entry{}, false
}

// dirChildrenClean checks whether all files in dir match their individual
// ledger entries. Returns true when every on-disk file is tracked and its
// digest matches, false otherwise.
func dirChildrenClean(dir string, lg domain.Ledger) bool {
	clean := true
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		entry, ok := lg.Managed[filepath.Clean(p)]
		if !ok {
			clean = false
			return nil
		}
		content, rerr := os.ReadFile(p)
		if rerr != nil {
			clean = false
			return nil
		}
		if domain.SingleFileDigest(content) != entry.Digest {
			clean = false
		}
		return nil
	})
	return clean
}

// classifyDirCopy determines the state of a directory copy (skill) by checking
// each child file against the ledger. Returns the aggregate state and pack name.
func classifyDirCopy(dir string, lg domain.Ledger, packRoots map[string]string) (FileState, string) {
	entry, tracked := findChildEntry(dir, lg)
	if !tracked {
		return FileUntracked, ""
	}

	packName := inferPackName(entry, packRoots)

	if dirChildrenClean(dir, lg) {
		return FileClean, packName
	}
	return FileModified, packName
}

func categoryFromDst(dst string) string {
	parts := strings.SplitN(filepath.ToSlash(dst), "/", 2)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func fileOrDirSize(path string, kind domain.CopyKind) int64 {
	if kind == domain.CopyKindDir {
		var total int64
		_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			info, ierr := d.Info()
			if ierr == nil {
				total += info.Size()
			}
			return nil
		})
		return total
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
