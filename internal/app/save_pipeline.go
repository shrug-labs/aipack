package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

// SaveCandidate extends HarnessFile with selection state for the pipeline.
type SaveCandidate struct {
	HarnessFile
	Selected bool
}

// DiscoverSaveRequest holds parameters for file discovery.
type DiscoverSaveRequest struct {
	HarnessID  domain.Harness
	Categories []domain.PackCategory
	Scope      domain.Scope
	ProjectDir string
	Home       string
	ConfigDir  string
}

// SavePipelineRequest holds everything the execute step needs.
type SavePipelineRequest struct {
	Candidates []SaveCandidate
	PackName   string
	ConfigDir  string
	Scope      domain.Scope
	ProjectDir string
	Home       string
	HarnessID  domain.Harness
	CreatePack bool
	Force      bool
	DryRun     bool
}

// SavePipelineResult holds the outcome.
type SavePipelineResult struct {
	SavedFiles     []SavedFile
	Conflicts      []ConflictFile
	SecretFindings []string
	Warnings       []domain.Warning
	PackCreated    bool
}

// DetectHarnessesWithContent returns harnesses that have content on disk
// for the given scope. Checks ManagedRoots for each harness and returns
// only those where at least one managed path exists.
func DetectHarnessesWithContent(scope domain.Scope, projectDir, home string, reg *harness.Registry) []domain.Harness {
	var result []domain.Harness
	for _, h := range reg.All() {
		roots := h.ManagedRoots(scope, projectDir, home)
		for _, r := range roots {
			if _, err := os.Stat(r); err == nil {
				result = append(result, h.ID())
				break
			}
		}
	}
	return result
}

// DiscoverContentVectors runs Capture on one harness and returns which
// PackCategories have at least one file on disk.
func DiscoverContentVectors(harnessID domain.Harness, scope domain.Scope, projectDir, home string, reg *harness.Registry) ([]domain.PackCategory, error) {
	h, err := reg.Lookup(harnessID)
	if err != nil {
		return nil, err
	}
	ctx := harness.CaptureContext{Scope: scope, ProjectDir: projectDir, Home: home}
	res, err := h.Capture(ctx)
	if err != nil {
		return nil, err
	}

	found := map[domain.PackCategory]bool{}
	for _, c := range res.Copies {
		cat := categoryFromDst(c.Dst)
		if cat != "" {
			found[cat] = true
		}
	}
	for _, w := range res.Writes {
		if w.Src != "" {
			if w.IsContent {
				cat := categoryFromDst(w.Dst)
				if cat != "" {
					found[cat] = true
				}
				continue
			}
			found[domain.CategorySettings] = true
		}
	}
	if len(res.MCP) > 0 || len(res.MCPServers) > 0 {
		found[domain.CategoryMCP] = true
	}

	// Return in canonical order.
	var result []domain.PackCategory
	for _, cat := range domain.AllPackCategories() {
		if found[cat] {
			result = append(result, cat)
		}
	}
	if found[domain.CategorySettings] {
		result = append(result, domain.CategorySettings)
	}
	return result, nil
}

// DiscoverSaveFiles runs harness capture + ledger classification for a single
// harness, filtered to the specified categories. Returns candidates with
// default selection: modified + untracked = selected, clean = unselected.
// Works without ledger (everything shows as untracked).
//
// The warnings return collects non-fatal issues (e.g. profile resolution
// failures) that callers should surface to the user.
func DiscoverSaveFiles(req DiscoverSaveRequest, reg *harness.Registry) ([]SaveCandidate, []string, error) {
	var warnings []string

	// Resolve pack roots from active profile for classification.
	res, _, err := ResolveActiveProfile(req.ConfigDir)
	if err != nil {
		// Non-fatal: proceed without pack roots (all files will be untracked).
		warnings = append(warnings, fmt.Sprintf("profile resolution failed (all files will appear untracked): %v", err))
		res = ResolveResult{
			TargetSpec: TargetSpec{
				Scope:      req.Scope,
				ProjectDir: req.ProjectDir,
				Harnesses:  []domain.Harness{req.HarnessID},
				Home:       req.Home,
			},
		}
	}

	// Override scope/home from request.
	ts := res.TargetSpec
	ts.Scope = req.Scope
	ts.ProjectDir = req.ProjectDir
	ts.Home = req.Home
	ts.Harnesses = []domain.Harness{req.HarnessID}

	packRoots := resolvePackRoots(res.Profile)

	inspResult, err := InspectHarness(InspectRequest{
		TargetSpec: ts,
		PackRoots:  packRoots,
	}, reg)
	if err != nil {
		return nil, warnings, err
	}

	wantCat := make(map[domain.PackCategory]bool, len(req.Categories))
	for _, c := range req.Categories {
		wantCat[c] = true
	}

	var candidates []SaveCandidate
	for _, f := range inspResult.Files {
		if !wantCat[f.Category] {
			continue
		}
		selected := f.State == FileModified || f.State == FileUntracked || f.State == FileConflict || f.State == FileSettings
		candidates = append(candidates, SaveCandidate{
			HarnessFile: f,
			Selected:    selected,
		})
	}
	return candidates, warnings, nil
}

// DiscoverSaveFilesAllScopes runs discovery for both project and global scopes,
// merging and deduplicating results. Each candidate carries its source Scope.
func DiscoverSaveFilesAllScopes(req DiscoverSaveRequest, reg *harness.Registry) ([]SaveCandidate, []string, error) {
	var allCandidates []SaveCandidate
	var allWarnings []string

	for _, scope := range []domain.Scope{domain.ScopeProject, domain.ScopeGlobal} {
		if scope == domain.ScopeProject && req.ProjectDir == "" {
			continue
		}
		scopeReq := req
		scopeReq.Scope = scope
		candidates, warnings, err := DiscoverSaveFiles(scopeReq, reg)
		if err != nil {
			allWarnings = append(allWarnings, fmt.Sprintf("%s scope: %v", scope, err))
			continue
		}
		allWarnings = append(allWarnings, warnings...)
		allCandidates = append(allCandidates, candidates...)
	}

	// Deduplicate by HarnessPath (absolute paths are unique across scopes).
	seen := map[string]bool{}
	var deduped []SaveCandidate
	for _, c := range allCandidates {
		if seen[c.HarnessPath] {
			continue
		}
		seen[c.HarnessPath] = true
		deduped = append(deduped, c)
	}

	return deduped, allWarnings, nil
}

// DiscoverContentVectorsAllScopes merges content vectors from both scopes.
func DiscoverContentVectorsAllScopes(harnessID domain.Harness, projectDir, home string, reg *harness.Registry) ([]domain.PackCategory, error) {
	found := map[domain.PackCategory]bool{}
	for _, scope := range []domain.Scope{domain.ScopeProject, domain.ScopeGlobal} {
		if scope == domain.ScopeProject && projectDir == "" {
			continue
		}
		vectors, err := DiscoverContentVectors(harnessID, scope, projectDir, home, reg)
		if err != nil {
			continue
		}
		for _, v := range vectors {
			found[v] = true
		}
	}
	var result []domain.PackCategory
	for _, cat := range domain.AllPackCategories() {
		if found[cat] {
			result = append(result, cat)
		}
	}
	if found[domain.CategorySettings] {
		result = append(result, domain.CategorySettings)
	}
	return result, nil
}

// RunSavePipeline copies selected candidates to the destination pack,
// updates the ledger, and scans for secrets. With DryRun, reports what
// would change without writing anything.
func RunSavePipeline(req SavePipelineRequest, reg *harness.Registry) (SavePipelineResult, error) {
	var result SavePipelineResult
	packRoot := filepath.Join(req.ConfigDir, "packs", req.PackName)

	// Create pack scaffold if requested.
	if req.CreatePack && !req.DryRun {
		if err := PackCreate(PackCreateRequest{Dir: packRoot, Name: req.PackName}); err != nil {
			return result, fmt.Errorf("creating pack: %w", err)
		}
		// Register in sync-config.
		if err := registerNewPack(req.ConfigDir, req.PackName, packRoot); err != nil {
			return result, err
		}
		result.PackCreated = true
	}

	// For dry-run on new packs, we can still report what would be saved
	// but skip manifest/ledger loading since the pack doesn't exist yet.
	if req.DryRun && req.CreatePack {
		for _, c := range req.Candidates {
			if !c.Selected {
				continue
			}
			if c.PackName != "" && c.PackName != req.PackName {
				continue
			}
			src := filepath.Clean(c.HarnessPath)
			dst := buildPackDst(packRoot, req.HarnessID, c.Category, c.RelPath, c.Kind)
			result.SavedFiles = append(result.SavedFiles, SavedFile{
				HarnessPath: src,
				PackName:    req.PackName,
				PackPath:    dst,
			})
		}
		result.PackCreated = true // would be created
		return result, nil
	}

	// Load manifest.
	manifestPath := filepath.Join(packRoot, "pack.json")
	manifest, err := config.LoadPackManifest(manifestPath)
	if err != nil {
		return result, fmt.Errorf("loading pack manifest: %w", err)
	}
	resolvedRoot := ResolvePackRootWithFallback(manifestPath, manifest, packRoot)

	// Load ledgers. When candidates span both scopes we need one ledger per scope.
	type scopeLedger struct {
		path   string
		ledger domain.Ledger
	}
	ledgers := map[domain.Scope]*scopeLedger{}
	loadLedger := func(scope domain.Scope) *scopeLedger {
		if sl, ok := ledgers[scope]; ok {
			return sl
		}
		lp := ledgerPathForScope(scope, req.ProjectDir, req.Home, req.HarnessID)
		lg, _, lerr := engine.LoadLedger(lp)
		if lerr != nil {
			lg = domain.NewLedger()
		}
		if lg.Managed == nil {
			lg.Managed = map[string]domain.Entry{}
		}
		sl := &scopeLedger{path: lp, ledger: lg}
		ledgers[scope] = sl
		return sl
	}
	// candidateScope returns the scope for a candidate, falling back to req.Scope.
	candidateScope := func(c SaveCandidate) domain.Scope {
		if c.Scope != "" {
			return c.Scope
		}
		return req.Scope
	}
	// Pre-load the request's default scope so it's always available.
	loadLedger(req.Scope)

	manifestChanged := false

	for _, c := range req.Candidates {
		if !c.Selected {
			continue
		}
		if c.PackName != "" && c.PackName != req.PackName {
			continue
		}

		src := filepath.Clean(c.HarnessPath)
		dst := buildPackDst(resolvedRoot, req.HarnessID, c.Category, c.RelPath, c.Kind)

		if c.Category == domain.CategoryMCP {
			content := c.Content
			if len(content) == 0 {
				return result, fmt.Errorf("missing MCP content for %s", c.RelPath)
			}

			conflict, err := checkMCPConflict(content, dst)
			if err != nil {
				return result, err
			}
			if conflict && !req.Force {
				result.Conflicts = append(result.Conflicts, ConflictFile{
					HarnessPath: src, PackPath: dst, PackName: req.PackName,
				})
				continue
			}

			if !req.DryRun {
				if err := saveContentToPack(content, dst); err != nil {
					return result, fmt.Errorf("saving MCP server %s: %w", c.RelPath, err)
				}
				loadLedger(candidateScope(c)).ledger.Managed[domain.MCPLedgerKey(src, c.RelPath)] = domain.Entry{
					SourcePack: req.PackName,
					Digest:     domain.SingleFileDigest(content),
				}
				if manifest.MCP.Servers == nil {
					manifest.MCP.Servers = map[string]config.MCPDefaults{}
				}
				manifest.MCP.Servers[c.RelPath] = config.MCPDefaults{
					DefaultAllowedTools: append([]string{}, c.AllowedTools...),
				}
				manifestChanged = true
			}
			for _, m := range scanBytesForSecrets(content) {
				result.SecretFindings = append(result.SecretFindings, c.RelPath+": "+m)
			}

			result.SavedFiles = append(result.SavedFiles, SavedFile{
				HarnessPath: src,
				PackName:    req.PackName,
				PackPath:    dst,
			})
			continue
		}

		switch c.Kind {
		default:
			return result, fmt.Errorf("unsupported copy kind %q for %s", c.Kind, src)
		case domain.CopyKindDir:
			// Check conflict.
			conflict, err := checkDirConflict(src, dst)
			if err != nil {
				return result, err
			}
			if conflict && !req.Force {
				result.Conflicts = append(result.Conflicts, ConflictFile{
					HarnessPath: src, PackPath: dst, PackName: req.PackName,
				})
				continue
			}
			if !req.DryRun {
				if err := saveDirToPack(src, dst); err != nil {
					return result, fmt.Errorf("saving dir %s: %w", src, err)
				}
			}
			// Secret scan directory files.
			if walkErr := filepath.WalkDir(src, func(p string, d os.DirEntry, werr error) error {
				if werr != nil || d.IsDir() {
					return werr
				}
				b, rerr := os.ReadFile(p)
				if rerr != nil {
					return nil
				}
				for _, m := range scanBytesForSecrets(b) {
					rel, _ := filepath.Rel(src, p)
					if rel == "" {
						rel = filepath.Base(p)
					}
					result.SecretFindings = append(result.SecretFindings, filepath.ToSlash(rel)+": "+m)
				}
				return nil
			}); walkErr != nil {
				result.Warnings = append(result.Warnings, domain.Warning{
					Path:    src,
					Message: fmt.Sprintf("secret scan walk: %v", walkErr),
				})
			}
			// Update ledger for each child file.
			if !req.DryRun {
				if walkErr := filepath.WalkDir(src, func(p string, d os.DirEntry, werr error) error {
					if werr != nil || d.IsDir() {
						return werr
					}
					content, rerr := os.ReadFile(p)
					if rerr != nil {
						return nil
					}
					loadLedger(candidateScope(c)).ledger.Managed[filepath.Clean(p)] = domain.Entry{
						SourcePack: req.PackName,
						Digest:     domain.SingleFileDigest(content),
					}
					return nil
				}); walkErr != nil {
					result.Warnings = append(result.Warnings, domain.Warning{
						Path:    src,
						Message: fmt.Sprintf("ledger update walk: %v", walkErr),
					})
				}
			}

		case domain.CopyKindFile:
			// Content may be normalized (e.g., agent frontmatter transform).
			// Read raw harness bytes separately for the ledger digest so that
			// change detection (which compares raw bytes) stays consistent.
			content := c.Content
			rawBytes, rerr := os.ReadFile(src)
			if len(content) == 0 {
				if rerr != nil {
					return result, fmt.Errorf("reading %s: %w", src, rerr)
				}
				content = rawBytes
			}

			// Check conflict.
			conflict, err := checkFileConflict(content, dst)
			if err != nil {
				return result, err
			}
			if conflict && !req.Force {
				result.Conflicts = append(result.Conflicts, ConflictFile{
					HarnessPath: src, PackPath: dst, PackName: req.PackName,
				})
				continue
			}

			if !req.DryRun {
				if err := saveContentToPack(content, dst); err != nil {
					return result, fmt.Errorf("saving %s: %w", src, err)
				}
			}
			for _, m := range scanBytesForSecrets(content) {
				result.SecretFindings = append(result.SecretFindings, c.RelPath+": "+m)
			}

			// Update ledger with raw harness bytes digest. Change detection
			// in contentChangedSinceLedger always reads raw bytes from disk,
			// so the ledger must record the same to avoid phantom drift.
			if !req.DryRun {
				digest := domain.SingleFileDigest(content)
				if rerr == nil {
					digest = domain.SingleFileDigest(rawBytes)
				}
				loadLedger(candidateScope(c)).ledger.Managed[src] = domain.Entry{
					SourcePack: req.PackName,
					Digest:     digest,
				}
			}
		}

		// Update manifest.
		if !req.DryRun {
			if addSaveCandidateToManifest(&manifest, req.HarnessID, c.Category, c.RelPath) {
				manifestChanged = true
			}
		}

		result.SavedFiles = append(result.SavedFiles, SavedFile{
			HarnessPath: src,
			PackName:    req.PackName,
			PackPath:    dst,
		})
	}

	if req.DryRun {
		return result, nil
	}

	// Save manifest if changed.
	if manifestChanged {
		if err := config.SavePackManifest(manifestPath, manifest); err != nil {
			return result, fmt.Errorf("saving pack manifest: %w", err)
		}
	}

	// Save ledgers for all touched scopes.
	for scope, sl := range ledgers {
		if err := engine.SaveLedger(sl.path, sl.ledger, false); err != nil {
			return result, fmt.Errorf("saving ledger for scope %s: %w", scope, err)
		}
	}

	return result, nil
}

// InstalledPackNames returns the names of all installed packs.
func InstalledPackNames(configDir string) ([]string, error) {
	packsDir := filepath.Join(configDir, "packs")
	entries, err := os.ReadDir(packsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Only include directories that have a pack.json.
		if _, err := os.Stat(filepath.Join(packsDir, e.Name(), "pack.json")); err == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// buildPackDst constructs the destination path within a pack for a content item.
func buildPackDst(packRoot string, harnessID domain.Harness, category domain.PackCategory, relPath string, kind domain.CopyKind) string {
	if kind == domain.CopyKindDir {
		return filepath.Join(packRoot, category.DirName(), relPath)
	}
	if category == domain.CategorySettings {
		return filepath.Join(packRoot, "configs", string(harnessID), relPath)
	}
	return filepath.Join(packRoot, category.DirName(), relPath+category.Ext())
}

func addSaveCandidateToManifest(m *config.PackManifest, harnessID domain.Harness, category domain.PackCategory, relPath string) bool {
	if category != domain.CategorySettings {
		return addToManifest(m, category, relPath)
	}
	if m.Configs.HarnessSettings == nil {
		m.Configs.HarnessSettings = map[string][]string{}
	}
	key := string(harnessID)
	for _, existing := range m.Configs.HarnessSettings[key] {
		if strings.EqualFold(existing, relPath) {
			return false
		}
	}
	m.Configs.HarnessSettings[key] = append(m.Configs.HarnessSettings[key], relPath)
	return true
}

// registerNewPack records a newly created pack in sync-config.
func registerNewPack(configDir, packName, packRoot string) error {
	syncCfgPath := config.SyncConfigPath(configDir)
	syncCfg, err := config.LoadSyncConfig(syncCfgPath)
	if err != nil {
		return fmt.Errorf("loading sync-config: %w", err)
	}
	if syncCfg.InstalledPacks == nil {
		syncCfg.InstalledPacks = map[string]config.InstalledPackMeta{}
	}
	syncCfg.InstalledPacks[packName] = config.InstalledPackMeta{
		Origin: packRoot,
		Method: config.MethodLocal,
	}
	if err := config.SaveSyncConfig(syncCfgPath, syncCfg); err != nil {
		return fmt.Errorf("saving sync-config: %w", err)
	}
	return nil
}
