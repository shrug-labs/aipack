package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

const defaultWriteMode os.FileMode = 0o644

// FileDiff classifies a single managed file against on-disk state and the ledger.
type FileDiff struct {
	Dst            string          // target file path
	Desired        []byte          // content to write
	DesiredMode    os.FileMode     // permission bits to use when writing
	Label          string          // human-readable label
	SourcePack     string          // pack provenance
	Kind           domain.DiffKind // classification
	OnDisk         []byte          // nil for create
	Diff           string          // unified diff string (empty for create/identical)
	ManagedOverlay []byte          // managed-only content for ledger (set by MergeMode settings)
	MergeOps       []MergeOp       // merge operations performed (nil for non-merge files)
}

func LabelSettingsActions(actions []domain.SettingsAction, labelFor func(string) string) []domain.SettingsAction {
	if len(actions) == 0 {
		return nil
	}
	out := slices.Clone(actions)
	for i := range out {
		if labelFor != nil {
			out[i].Label = labelFor(out[i].Dst)
		}
	}
	return out
}

type ClassifyCopyOptions struct {
	LabelForPath func(string) string
}

// classifyFileKind classifies a file without computing a diff string.
// Use when only the DiffKind is needed (e.g., non-verbose dry-run).
func (e *Engine) classifyFileKind(dst string, desired []byte, lg domain.Ledger) (domain.DiffKind, error) {
	dst = filepath.Clean(dst)
	onDisk, err := e.FS.ReadFile(dst)
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

// ClassifyWriteKind classifies a generated write without computing a diff.
func (e *Engine) ClassifyWriteKind(w domain.WriteAction, lg domain.Ledger) (domain.DiffKind, error) {
	fd, err := e.classifyWrite(w, filepath.Base(w.Dst), lg, false)
	if err != nil {
		return "", err
	}
	return fd.Kind, nil
}

// ClassifyWrite classifies a generated write, including optional desired mode.
func (e *Engine) ClassifyWrite(w domain.WriteAction, label string, lg domain.Ledger) (FileDiff, error) {
	return e.classifyWrite(w, label, lg, true)
}

func (e *Engine) classifyWrite(w domain.WriteAction, label string, lg domain.Ledger, withDiff bool) (FileDiff, error) {
	dst := filepath.Clean(w.Dst)
	desiredMode := w.EffectiveMode(defaultWriteMode)
	onDisk, err := e.FS.ReadFile(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return FileDiff{
				Dst: dst, Desired: w.Content, DesiredMode: desiredMode, Label: label,
				SourcePack: w.SourcePack, Kind: domain.DiffCreate,
			}, nil
		}
		return FileDiff{}, err
	}
	modeOK, onDiskMode, err := e.writeModeMatches(dst, w)
	if err != nil {
		return FileDiff{}, err
	}
	contentMatches := bytes.Equal(onDisk, w.Content)
	if !contentMatches {
		contentMatches = domain.SingleFileDigest(onDisk) == w.EffectiveDigest()
	}
	if contentMatches {
		fd := FileDiff{
			Dst: dst, Desired: w.Content, DesiredMode: desiredMode, Label: label,
			SourcePack: w.SourcePack, OnDisk: onDisk,
		}
		if modeOK {
			fd.Kind = domain.DiffIdentical
		} else {
			fd.Kind = domain.DiffManaged
			// Content matches; only file mode drifted. Populate a Diff line
			// so apply prints something more informative than a bare "update:".
			fd.Diff = fmt.Sprintf("mode: %#o → %#o (content unchanged)\n", onDiskMode.Perm(), desiredMode.Perm())
		}
		return fd, nil
	}
	if !withDiff {
		return FileDiff{
			Dst: dst, Desired: w.Content, DesiredMode: desiredMode, Label: label,
			SourcePack: w.SourcePack, OnDisk: onDisk,
			Kind: classifyFileKindPreRead(dst, w.Content, lg, onDisk),
		}, nil
	}
	fd := classifyFilePreRead(dst, w.Content, label, w.SourcePack, lg, onDisk)
	fd.DesiredMode = desiredMode
	return fd, nil
}

func (e *Engine) writeModeMatches(dst string, w domain.WriteAction) (bool, os.FileMode, error) {
	if w.DesiredMode == 0 {
		return true, 0, nil
	}
	info, err := e.FS.Stat(dst)
	if err != nil {
		return false, 0, err
	}
	mode := info.Mode().Perm()
	return mode == w.DesiredMode.Perm(), mode, nil
}

// ClassifyFile classifies a single file against on-disk state and the ledger.
func (e *Engine) ClassifyFile(dst string, desired []byte, label, sourcePack string, lg domain.Ledger) (FileDiff, error) {
	return e.classifyFileWithMode(dst, desired, defaultWriteMode, false, label, sourcePack, lg)
}

func (e *Engine) ClassifyFileWithMode(dst string, desired []byte, desiredMode os.FileMode, label, sourcePack string, lg domain.Ledger) (FileDiff, error) {
	if desiredMode == 0 {
		return e.ClassifyFile(dst, desired, label, sourcePack, lg)
	}
	return e.classifyFileWithMode(dst, desired, desiredMode.Perm(), true, label, sourcePack, lg)
}

func (e *Engine) classifyFileWithMode(dst string, desired []byte, desiredMode os.FileMode, checkMode bool, label, sourcePack string, lg domain.Ledger) (FileDiff, error) {
	dst = filepath.Clean(dst)

	onDisk, err := e.FS.ReadFile(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return FileDiff{Dst: dst, Desired: desired, DesiredMode: desiredMode, Label: label, SourcePack: sourcePack, Kind: domain.DiffCreate}, nil
		}
		return FileDiff{}, err
	}

	if checkMode && bytes.Equal(desired, onDisk) {
		info, err := e.FS.Stat(dst)
		if err != nil {
			return FileDiff{}, err
		}
		onDiskMode := info.Mode().Perm()
		if onDiskMode != desiredMode.Perm() {
			return FileDiff{
				Dst: dst, Desired: desired, DesiredMode: desiredMode, Label: label,
				SourcePack: sourcePack, Kind: domain.DiffManaged, OnDisk: onDisk,
				Diff: fmt.Sprintf("mode: %#o → %#o (content unchanged)\n", onDiskMode, desiredMode.Perm()),
			}, nil
		}
	}

	fd := classifyFilePreRead(dst, desired, label, sourcePack, lg, onDisk)
	fd.DesiredMode = desiredMode
	return fd, nil
}

// classifyFilePreRead classifies a file when the on-disk content is already known.
func classifyFilePreRead(dst string, desired []byte, label, sourcePack string, lg domain.Ledger, onDisk []byte) FileDiff {
	base := FileDiff{Dst: filepath.Clean(dst), Desired: desired, DesiredMode: defaultWriteMode, Label: label, SourcePack: sourcePack}

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
func (e *Engine) ClassifyCopy(src, dst, sourcePack string, lg domain.Ledger) ([]FileDiff, error) {
	return e.ClassifyCopyWithOptions(src, dst, sourcePack, lg, ClassifyCopyOptions{})
}

func (e *Engine) ClassifyCopyWithOptions(src, dst, sourcePack string, lg domain.Ledger, opts ClassifyCopyOptions) ([]FileDiff, error) {
	var out []FileDiff
	src = filepath.Clean(src)
	if resolver, ok := e.FS.(symlinkEvaluator); ok {
		resolved, err := resolver.EvalSymlinks(src)
		if err == nil && resolved != src {
			if info, statErr := e.FS.Stat(resolved); statErr == nil && info.IsDir() {
				src = resolved
			}
		}
	}
	err := e.FS.WalkDir(src, func(p string, d os.DirEntry, werr error) error {
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
		content, err := e.FS.ReadFile(p)
		if err != nil {
			return err
		}
		desiredMode, err := e.copyDesiredMode(p)
		if err != nil {
			return err
		}
		label := filepath.Join(filepath.Base(dst), rel)
		if opts.LabelForPath != nil {
			label = opts.LabelForPath(target)
		}
		fd, err := e.ClassifyFileWithMode(target, content, desiredMode, label, sourcePack, lg)
		if err != nil {
			return err
		}
		out = append(out, fd)
		return nil
	})
	return out, err
}

func (e *Engine) copyDesiredMode(src string) (os.FileMode, error) {
	info, err := e.FS.Stat(src)
	if err != nil {
		return 0, err
	}
	mode := info.Mode().Perm()
	if mode&0o111 == 0 {
		return defaultWriteMode, nil
	}
	return mode, nil
}

// ComputeSettingsDiffs classifies each settings action against on-disk state and the ledger.
// When MergeMode is set, performs three-way merge using the previous managed overlay.
func (e *Engine) ComputeSettingsDiffs(settings []domain.SettingsAction, lg domain.Ledger) ([]FileDiff, error) {
	var out []FileDiff
	for _, s := range settings {
		d, err := e.computeSettingsDiff(s, lg, true)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// ClassifySettingsKind classifies a settings action without constructing a
// unified diff. Merge-mode actions still perform the merge needed to determine
// whether aipack-managed keys would change.
func (e *Engine) ClassifySettingsKind(setting domain.SettingsAction, lg domain.Ledger) (domain.DiffKind, error) {
	fd, err := e.computeSettingsDiff(setting, lg, false)
	if err != nil {
		return "", err
	}
	return fd.Kind, nil
}

func (e *Engine) computeSettingsDiff(s domain.SettingsAction, lg domain.Ledger, withDiff bool) (FileDiff, error) {
	if !s.MergeMode {
		if withDiff {
			return e.ClassifyFile(s.Dst, s.Desired, s.Label, s.SourcePack, lg)
		}
		kind, err := e.classifyFileKind(s.Dst, s.Desired, lg)
		if err != nil {
			return FileDiff{}, err
		}
		return FileDiff{
			Dst: filepath.Clean(s.Dst), Desired: s.Desired, Label: s.Label,
			SourcePack: s.SourcePack, Kind: kind,
		}, nil
	}

	existing, err := e.FS.ReadFile(s.Dst)
	fileExists := true
	if err != nil {
		if !os.IsNotExist(err) {
			return FileDiff{}, err
		}
		fileExists = false
	}
	desired := s.Desired
	var mergeOps []MergeOp
	if fileExists && len(existing) > 0 {
		prevManaged := lg.PrevManagedOverlay(s.Dst)
		merged, mops, merr := mergeSettingsKeys(existing, prevManaged, s.Desired, s.Harness, s.AdditiveOnly)
		if merr != nil {
			return FileDiff{}, fmt.Errorf("merge %s: %w", s.Label, merr)
		}
		desired = merged
		mergeOps = mops
	}

	var fd FileDiff
	if !fileExists {
		fd = FileDiff{Dst: filepath.Clean(s.Dst), Desired: desired, Label: s.Label, SourcePack: s.SourcePack, Kind: domain.DiffCreate}
	} else if len(mergeOps) == 0 && settingsBytesEqual(existing, desired, s.Harness) {
		// No managed keys changed. Keep the on-disk bytes as desired so the
		// ledger digest follows harness formatting.
		fd = FileDiff{
			Dst: filepath.Clean(s.Dst), Desired: existing, Label: s.Label,
			SourcePack: s.SourcePack, Kind: domain.DiffIdentical, OnDisk: existing,
		}
	} else if withDiff {
		fd = classifyFilePreRead(s.Dst, desired, s.Label, s.SourcePack, lg, existing)
		if fd.Diff != "" {
			diffExisting, diffDesired := settingsDiffInputs(existing, desired, s.Harness)
			fd.Diff = UnifiedDiff(diffExisting, diffDesired, s.Label+" (current)", s.Label+" (after merge)")
		}
	} else {
		fd = FileDiff{
			Dst: filepath.Clean(s.Dst), Desired: desired, Label: s.Label,
			SourcePack: s.SourcePack, Kind: classifyFileKindPreRead(s.Dst, desired, lg, existing),
		}
	}

	if fd.Kind == domain.DiffConflict {
		fd.Kind = domain.DiffManaged
	}
	fd.ManagedOverlay = s.Desired
	if withDiff {
		fd.MergeOps = mergeOps
	}
	return fd, nil
}

func settingsBytesEqual(a, b []byte, harness domain.Harness) bool {
	if bytes.Equal(a, b) {
		return true
	}
	normalizedA, errA := normalizeSettingsBytes(a, harness)
	normalizedB, errB := normalizeSettingsBytes(b, harness)
	if errA != nil || errB != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(normalizedA, normalizedB)
}

func classifyFileKindPreRead(dst string, desired []byte, lg domain.Ledger, onDisk []byte) domain.DiffKind {
	if bytes.Equal(desired, onDisk) {
		return domain.DiffIdentical
	}
	if prev := lg.PrevDigest(dst); prev != "" && domain.SingleFileDigest(onDisk) == prev {
		return domain.DiffManaged
	}
	return domain.DiffConflict
}

func settingsDiffInputs(existing, desired []byte, harness domain.Harness) ([]byte, []byte) {
	normalizedExisting, errExisting := normalizeSettingsBytes(existing, harness)
	normalizedDesired, errDesired := normalizeSettingsBytes(desired, harness)
	if errExisting != nil || errDesired != nil {
		return existing, desired
	}
	return normalizedExisting, normalizedDesired
}

func normalizeSettingsBytes(in []byte, harness domain.Harness) ([]byte, error) {
	switch harness {
	case domain.HarnessOpenCode, domain.HarnessCline, domain.HarnessClaudeCode:
		m, err := parseJSONMap(in)
		if err != nil {
			return nil, err
		}
		return marshalJSON(m)
	case domain.HarnessCodex:
		m, err := parseTOMLMap(in)
		if err != nil {
			return nil, err
		}
		return marshalTOML(m)
	default:
		return nil, fmt.Errorf("unsupported harness for settings diff: %s", harness)
	}
}

// pathDigest computes a composite digest for a path (file or directory).
// Format: sorted "key\0sha256\n" entries hashed with streaming SHA-256.
// This is NOT interchangeable with app/save.go's dirDigest, which uses a
// different format ("rel:sha256\n" + ContentDigest). The two never cross-compare:
// pathDigest is used for ledger entries during sync, while dirDigest is used
// only for save's source-change detection.
func (e *Engine) pathDigest(path string) (string, error) {
	digest, _, err := e.pathDigestStatus(path)
	return digest, err
}

func (e *Engine) pathDigestStatus(path string) (string, bool, error) {
	m, missing, err := e.collectFilesStatus(path)
	if err != nil {
		return "", missing, err
	}
	keys := slices.Sorted(maps.Keys(m))
	h := sha256.New()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(m[k]))
		_, _ = h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil)), missing, nil
}

// PathDigest computes the ledger-compatible digest for a file or directory.
func (e *Engine) PathDigest(path string) (string, error) {
	return e.pathDigest(path)
}

func (e *Engine) collectFiles(root string) (map[string]string, error) {
	out, _, err := e.collectFilesStatus(root)
	return out, err
}

func (e *Engine) collectFilesStatus(root string) (map[string]string, bool, error) {
	out := map[string]string{}
	st, err := e.FS.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return out, true, nil
		}
		return nil, false, err
	}
	if !st.IsDir() {
		b, err := e.FS.ReadFile(root)
		if err != nil {
			if os.IsNotExist(err) {
				return out, true, nil
			}
			return nil, false, err
		}
		out["."] = util.ContentDigest(b)
		return out, false, nil
	}
	err = e.FS.WalkDir(root, func(p string, d os.DirEntry, werr error) error {
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
		b, err := e.FS.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = util.ContentDigest(b)
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return out, true, nil
		}
		return nil, false, err
	}
	return out, false, nil
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
	hunks := groupHunks(edits, contextLines)
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

func groupHunks(edits []edit, ctx int) []hunk {
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
		s := max(sp.start-ctx, 0)
		e := min(sp.end+ctx, len(edits))
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
			for k := range es.start {
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
