package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// FileDiff classifies a single managed file against on-disk state and the ledger.
type FileDiff struct {
	Dst            string          // target file path
	Desired        []byte          // content to write
	Label          string          // human-readable label
	SourcePack     string          // pack provenance
	Kind           domain.DiffKind // classification
	OnDisk         []byte          // nil for create
	Diff           string          // unified diff string (empty for create/identical)
	ManagedOverlay []byte          // managed-only content for ledger (set by MergeMode settings)
	MergeOps       []MergeOp       // merge operations performed (nil for non-merge files)
}

// ClassifyFileKind classifies a file without computing a diff string.
// Use when only the DiffKind is needed (e.g., non-verbose dry-run).
func ClassifyFileKind(dst string, desired []byte, lg domain.Ledger) (domain.DiffKind, error) {
	dst = filepath.Clean(dst)
	onDisk, err := os.ReadFile(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.DiffCreate, nil
		}
		return "", err
	}
	if bytes.Equal(desired, onDisk) {
		return domain.DiffIdentical, nil
	}
	prev := lg.PrevDigest(dst)
	if prev != "" {
		if domain.SingleFileDigest(onDisk) == prev {
			return domain.DiffManaged, nil
		}
	}
	return domain.DiffConflict, nil
}

// ClassifyFile classifies a single file against on-disk state and the ledger.
func ClassifyFile(dst string, desired []byte, label, sourcePack string, lg domain.Ledger) (FileDiff, error) {
	dst = filepath.Clean(dst)

	onDisk, err := os.ReadFile(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return FileDiff{Dst: dst, Desired: desired, Label: label, SourcePack: sourcePack, Kind: domain.DiffCreate}, nil
		}
		return FileDiff{}, err
	}

	return classifyFilePreRead(dst, desired, label, sourcePack, lg, onDisk), nil
}

// classifyFilePreRead classifies a file when the on-disk content is already known.
func classifyFilePreRead(dst string, desired []byte, label, sourcePack string, lg domain.Ledger, onDisk []byte) FileDiff {
	base := FileDiff{Dst: filepath.Clean(dst), Desired: desired, Label: label, SourcePack: sourcePack}

	if bytes.Equal(desired, onDisk) {
		base.Kind = domain.DiffIdentical
		base.OnDisk = onDisk
		return base
	}

	// File differs from desired. Check if it's managed and unmodified since last sync.
	prev := lg.PrevDigest(dst)
	if prev != "" {
		diskDigest := domain.SingleFileDigest(onDisk)
		if diskDigest == prev {
			base.Kind = domain.DiffManaged
			base.OnDisk = onDisk
			base.Diff = UnifiedDiff(onDisk, desired, label+" (current)", label+" (desired)")
			return base
		}
	}

	base.Kind = domain.DiffConflict
	base.OnDisk = onDisk
	base.Diff = UnifiedDiff(onDisk, desired, label+" (current)", label+" (desired)")
	return base
}

// ClassifyCopy walks a source directory and classifies each file against on-disk state.
func ClassifyCopy(src, dst, sourcePack string, lg domain.Ledger) ([]FileDiff, error) {
	var out []FileDiff
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if util.IgnoredName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fd, err := ClassifyFile(target, content, filepath.Join(filepath.Base(dst), rel), sourcePack, lg)
		if err != nil {
			return err
		}
		out = append(out, fd)
		return nil
	})
	return out, err
}

// ComputeSettingsDiffs classifies each settings action against on-disk state and the ledger.
// When MergeMode is set, performs three-way merge using the previous managed overlay.
func ComputeSettingsDiffs(settings []domain.SettingsAction, lg domain.Ledger) ([]FileDiff, error) {
	var out []FileDiff
	for _, s := range settings {
		if s.MergeMode {
			existing, err := os.ReadFile(s.Dst)
			fileExists := true
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
				fileExists = false
			}
			desired := s.Desired
			var mergeOps []MergeOp
			if fileExists && len(existing) > 0 {
				prevManaged := lg.PrevManagedOverlay(s.Dst)
				merged, mops, merr := mergeSettingsKeys(existing, prevManaged, s.Desired, s.Harness)
				if merr != nil {
					return nil, fmt.Errorf("merge %s: %w", s.Label, merr)
				}
				desired = merged
				mergeOps = mops
			}
			var fd FileDiff
			if !fileExists {
				fd = FileDiff{Dst: filepath.Clean(s.Dst), Desired: desired, Label: s.Label, SourcePack: s.SourcePack, Kind: domain.DiffCreate}
			} else if len(mergeOps) == 0 {
				// No managed keys changed — file is identical from our
				// perspective. Use on-disk content as desired so the
				// ledger digest matches reality (avoids false dirty
				// detection when the harness reformats the file).
				fd = FileDiff{
					Dst: filepath.Clean(s.Dst), Desired: existing, Label: s.Label,
					SourcePack: s.SourcePack, Kind: domain.DiffIdentical, OnDisk: existing,
				}
			} else {
				fd = classifyFilePreRead(s.Dst, desired, s.Label, s.SourcePack, lg, existing)
				// MergeMode: the three-way merge already resolved conflicts
				// by preserving user content. Reclassify as managed (safe to
				// update) so apply doesn't require --force.
				if fd.Kind == domain.DiffConflict {
					fd.Kind = domain.DiffManaged
				}
			}
			fd.ManagedOverlay = s.Desired
			fd.MergeOps = mergeOps
			out = append(out, fd)
			continue
		}
		d, err := ClassifyFile(s.Dst, s.Desired, s.Label, s.SourcePack, lg)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// pathDigest computes a composite digest for a path (file or directory).
// Format: sorted "key\0sha256\n" entries hashed with streaming SHA-256.
// This is NOT interchangeable with app/save.go's dirDigest, which uses a
// different format ("rel:sha256\n" + ContentDigest). The two never cross-compare:
// pathDigest is used for ledger entries during sync, while dirDigest is used
// only for save's source-change detection.
func pathDigest(path string) (string, error) {
	m, err := collectFiles(path)
	if err != nil {
		return "", err
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(m[k]))
		_, _ = h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func collectFiles(root string) (map[string]string, error) {
	out := map[string]string{}
	st, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	if !st.IsDir() {
		d, err := util.FileDigest(root)
		if err != nil {
			return nil, err
		}
		out["."] = d
		return out, nil
	}
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if util.IgnoredName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		dig, err := util.FileDigest(p)
		if err != nil {
			return err
		}
		out[rel] = dig
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UnifiedDiff produces a simple unified-style diff between two byte slices.
func UnifiedDiff(a, b []byte, labelA, labelB string) string {
	linesA := splitLines(a)
	linesB := splitLines(b)

	edits := diffLines(linesA, linesB)
	if len(edits) == 0 {
		return ""
	}

	const contextLines = 3
	hunks := groupHunks(edits, len(linesA), len(linesB), contextLines)
	if len(hunks) == 0 {
		return ""
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "--- %s\n", labelA)
	fmt.Fprintf(&buf, "+++ %s\n", labelB)
	for _, h := range hunks {
		fmt.Fprintf(&buf, "@@ -%d,%d +%d,%d @@\n", h.startA+1, h.countA, h.startB+1, h.countB)
		for _, l := range h.lines {
			buf.WriteString(l)
			buf.WriteByte('\n')
		}
	}
	return buf.String()
}

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type editKind int

const (
	editEqual  editKind = iota
	editDelete          // line from A only
	editInsert          // line from B only
)

type edit struct {
	kind editKind
	line string
	idxA int // index in A (-1 for insert)
	idxB int // index in B (-1 for delete)
}

func diffLines(a, b []string) []edit {
	n := len(a)
	m := len(b)
	if n == 0 && m == 0 {
		return nil
	}

	lcs := computeLCS(a, b)

	var edits []edit
	ia, ib := 0, 0
	li := 0
	for ia < n || ib < m {
		if li < len(lcs) && ia == lcs[li][0] && ib == lcs[li][1] {
			edits = append(edits, edit{kind: editEqual, line: a[ia], idxA: ia, idxB: ib})
			ia++
			ib++
			li++
		} else if ia < n && (li >= len(lcs) || ia < lcs[li][0]) {
			edits = append(edits, edit{kind: editDelete, line: a[ia], idxA: ia, idxB: -1})
			ia++
		} else if ib < m && (li >= len(lcs) || ib < lcs[li][1]) {
			edits = append(edits, edit{kind: editInsert, line: b[ib], idxA: -1, idxB: ib})
			ib++
		}
	}
	return edits
}

func computeLCS(a, b []string) [][2]int {
	n := len(a)
	m := len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var result [][2]int
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			result = append(result, [2]int{i, j})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return result
}

type hunk struct {
	startA int
	countA int
	startB int
	countB int
	lines  []string
}

func groupHunks(edits []edit, lenA, lenB, ctx int) []hunk {
	type span struct{ start, end int }
	var spans []span
	i := 0
	for i < len(edits) {
		if edits[i].kind == editEqual {
			i++
			continue
		}
		s := i
		for i < len(edits) && edits[i].kind != editEqual {
			i++
		}
		spans = append(spans, span{s, i})
	}
	if len(spans) == 0 {
		return nil
	}

	type expandedSpan struct{ start, end int }
	var expanded []expandedSpan
	for _, sp := range spans {
		s := sp.start - ctx
		if s < 0 {
			s = 0
		}
		e := sp.end + ctx
		if e > len(edits) {
			e = len(edits)
		}
		if len(expanded) > 0 && s <= expanded[len(expanded)-1].end {
			expanded[len(expanded)-1].end = e
		} else {
			expanded = append(expanded, expandedSpan{s, e})
		}
	}

	var hunks []hunk
	for _, es := range expanded {
		h := hunk{}
		startA := 0
		startB := 0
		if es.start > 0 {
			for k := 0; k < es.start; k++ {
				switch edits[k].kind {
				case editEqual:
					startA++
					startB++
				case editDelete:
					startA++
				case editInsert:
					startB++
				}
			}
		}
		h.startA = startA
		h.startB = startB

		ca, cb := 0, 0
		for k := es.start; k < es.end; k++ {
			e := edits[k]
			switch e.kind {
			case editEqual:
				h.lines = append(h.lines, " "+e.line)
				ca++
				cb++
			case editDelete:
				h.lines = append(h.lines, "-"+e.line)
				ca++
			case editInsert:
				h.lines = append(h.lines, "+"+e.line)
				cb++
			}
		}
		h.countA = ca
		h.countB = cb
		hunks = append(hunks, h)
	}
	return hunks
}
