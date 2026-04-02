package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/index"
	"github.com/shrug-labs/aipack/internal/util"

	"gopkg.in/yaml.v3"
)

const embeddedRegistrySourceName = "_embedded"

// ResolveRequest holds the inputs for resolving a profile into a sync context.
type ResolveRequest struct {
	ConfigDir   string
	ProfilePath string
	ProfileCfg  config.ProfileConfig
	SyncCfg     config.SyncConfig
	ProjectDir  string // working directory — caller must provide
	Home        string // $HOME — caller must provide
}

// ResolveResult holds the resolved profile and targeting information.
type ResolveResult struct {
	Profile domain.Profile
	TargetSpec
}

// ResolveProfile resolves a profile config into a fully-typed profile with
// targeting information (scope, harnesses, project dir) from sync-config defaults.
func ResolveProfile(eng *engine.Engine, req ResolveRequest) (ResolveResult, []domain.Warning, error) {
	profile, warnings, err := eng.Resolve(req.ProfileCfg, req.ProfilePath, req.ConfigDir)
	if err != nil {
		return ResolveResult{}, warnings, err
	}

	scope := domain.ScopeProject
	if req.SyncCfg.Defaults.Scope != "" {
		if s, serr := cmdutil.NormalizeScope(req.SyncCfg.Defaults.Scope); serr == nil {
			scope = s
		}
	}

	hs, err := cmdutil.ResolveHarnesses(req.SyncCfg.Defaults.Harnesses)
	if err != nil {
		return ResolveResult{}, warnings, err
	}

	return ResolveResult{
		Profile: profile,
		TargetSpec: TargetSpec{
			Scope:      scope,
			ProjectDir: req.ProjectDir,
			Harnesses:  hs,
			Home:       req.Home,
		},
	}, warnings, nil
}

// ResolveActiveProfile loads the active profile from sync-config defaults
// and resolves it into a fully-typed profile with targeting information.
// This is the I/O boundary: it resolves ProjectDir and Home from the
// environment so that ResolveProfile (and everything below it) stays I/O-free.
func ResolveActiveProfile(eng *engine.Engine, configDir string) (ResolveResult, []domain.Warning, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return ResolveResult{}, nil, fmt.Errorf("resolving working directory: %w", err)
	}
	home := config.HomeDir()
	syncCfgPath := config.SyncConfigPath(configDir)
	syncCfg, err := config.LoadSyncConfig(syncCfgPath)
	if err != nil {
		return ResolveResult{}, nil, fmt.Errorf("loading sync-config: %w", err)
	}
	profileName := syncCfg.Defaults.Profile
	if profileName == "" {
		profileName = "default"
	}
	profilePath := filepath.Join(configDir, "profiles", profileName+".yaml")
	profileCfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return ResolveResult{}, nil, err
	}
	return ResolveProfile(eng, ResolveRequest{
		ConfigDir:   configDir,
		ProfilePath: profilePath,
		ProfileCfg:  profileCfg,
		SyncCfg:     syncCfg,
		ProjectDir:  cwd,
		Home:        home,
	})
}

// resolvePackRoots extracts pack name → root mappings from a resolved profile.
func resolvePackRoots(profile domain.Profile) map[string]string {
	roots := make(map[string]string, len(profile.Packs))
	for _, p := range profile.Packs {
		roots[p.Name] = p.Root
	}
	return roots
}

// TargetSpec holds the common targeting fields shared across request types.
type TargetSpec struct {
	Scope      domain.Scope
	ProjectDir string
	Harnesses  []domain.Harness
	Home       string // $HOME — threaded explicitly for testability
}

// SyncRequest holds the parameters for a sync operation.
type SyncRequest struct {
	TargetSpec
	Force        bool
	SkipSettings bool
	Yes          bool
	DryRun       bool
	Quiet        bool
	Verbose      bool
	NowFn        func() time.Time // test injection; nil = time.Now
}

// SyncResult holds the result of a sync operation.
type SyncResult struct {
	Plan domain.Plan
}

// RunSync plans and applies a sync. It is the primary v2 entry point for the sync command.
//
// Error policy: loading/reading failures that degrade gracefully are returned
// as warnings. Writing failures that leave inconsistent state are fatal errors.
func RunSync(ctx context.Context, eng *engine.Engine, profile domain.Profile, req SyncRequest, reg *harness.Registry, stdout, stderr io.Writer) (SyncResult, []domain.Warning, error) {
	var warnings []domain.Warning
	baseDir := req.ProjectDir
	if req.Scope == domain.ScopeGlobal {
		baseDir = req.Home
	}

	if !req.DryRun {
		// Migrate legacy ledgers (combined-harness or project-local) on first run.
		managedRootsMap := map[domain.Harness][]string{}
		for _, hid := range req.Harnesses {
			if h, lerr := reg.Lookup(hid); lerr == nil {
				managedRootsMap[hid] = h.Layout(req.Scope, baseDir, req.Home).ValidationRoots
			}
		}
		if n, merr := eng.MigrateOldLedgers(req.Scope, req.ProjectDir, req.Home, req.Harnesses, managedRootsMap); merr != nil {
			warnings = append(warnings, warningf("ledger-migration", "%v", merr))
		} else if n > 0 {
			fmt.Fprintf(stderr, "migrated %d ledger entries to per-harness format\n", n)
		}
	}

	// Plan and apply per harness — each gets its own ledger.
	aggregatePlan := domain.Plan{Desired: map[string]struct{}{}}
	for _, hid := range req.Harnesses {
		planners, err := reg.AsPlanners([]domain.Harness{hid})
		if err != nil {
			return SyncResult{}, warnings, wrapFatalSync("lookup harness planners", err)
		}

		planReq := engine.PlanRequest{
			Scope:        req.Scope,
			Harnesses:    []domain.Harness{hid},
			ProjectDir:   req.ProjectDir,
			Home:         req.Home,
			SkipSettings: req.SkipSettings,
		}

		plan, err := engine.PlanSync(ctx, profile, planReq, planners)
		if err != nil {
			return SyncResult{}, warnings, wrapFatalSync("plan sync", err)
		}

		if req.DryRun {
			mergePlans(&aggregatePlan, plan)
			continue
		}

		h, _ := reg.Lookup(hid)
		managedRoots := h.Layout(req.Scope, baseDir, req.Home).ValidationRoots

		applyReq := engine.ApplyRequest{
			Force:  req.Force,
			Yes:    req.Yes,
			DryRun: req.DryRun,
			Quiet:  req.Quiet,
			Stderr: stderr,
			Req:    planReq,
		}

		applyWarnings, err := eng.ApplyPlan(ctx, plan, applyReq, managedRoots)
		warnings = append(warnings, applyWarnings...)
		if err != nil {
			return SyncResult{}, warnings, wrapFatalSync("apply plan", err)
		}

		// Reconcile per-server MCP ledger entries with actual on-disk state.
		// ApplyPlan records per-server entries using planned content, but
		// three-way merge may write different content (preserving user
		// additions). Re-capture from the now-updated file and record the
		// actual digests so that save/inspect classify correctly.
		if len(plan.MCPServers) > 0 {
			reconWarnings := reconcileMCPLedger(ctx, eng, plan, h, req, req.NowFn)
			warnings = append(warnings, reconWarnings...)
		}

		mergePlans(&aggregatePlan, plan)
	}

	if req.DryRun {
		if req.Verbose {
			summary, err := PlanWithDiffs(ctx, eng, profile, req, reg)
			if err != nil {
				return SyncResult{}, warnings, wrapFatalSync("compute verbose dry-run diffs", err)
			}
			printDryRunVerbose(summary, stdout)
		} else {
			printDryRun(eng, aggregatePlan, req, reg, CountProfileContent(profile), stdout)
		}
		return SyncResult{Plan: aggregatePlan}, warnings, nil
	}

	// Post-sync tasks — run once.
	if idxErr := updateIndex(profile, req.Home); idxErr != nil {
		warnings = append(warnings, warningf("index", "index update failed: %v", idxErr))
	}
	regWarnings := processEmbeddedRegistries(profile, req.Home, stderr)
	warnings = append(warnings, regWarnings...)

	return SyncResult{Plan: aggregatePlan}, warnings, nil
}

func mergePlans(dst *domain.Plan, src domain.Plan) {
	dst.Writes = append(dst.Writes, src.Writes...)
	dst.Copies = append(dst.Copies, src.Copies...)
	dst.Settings = append(dst.Settings, src.Settings...)
	dst.MCP = append(dst.MCP, src.MCP...)
	dst.MCPServers = append(dst.MCPServers, src.MCPServers...)
	if dst.Desired == nil {
		dst.Desired = map[string]struct{}{}
	}
	for path := range src.Desired {
		dst.Desired[path] = struct{}{}
	}
	if dst.Ledger == "" {
		dst.Ledger = src.Ledger
	}
}

// reconcileMCPLedger re-captures MCP servers from the now-updated harness
// config and overwrites the per-server ledger entries with actual (post-merge)
// digests. This corrects the planned-vs-merged divergence that occurs when
// three-way merge preserves user additions in the config file.
func reconcileMCPLedger(ctx context.Context, eng *engine.Engine, plan domain.Plan, h harness.Harness, req SyncRequest, nowFn func() time.Time) []domain.Warning {
	if plan.Ledger == "" {
		return nil
	}

	res, err := h.Capture(ctx, harness.CaptureContext{
		Scope:      req.Scope,
		ProjectDir: req.ProjectDir,
		Home:       req.Home,
	})
	if err != nil {
		return []domain.Warning{warningf("mcp-reconcile", "re-capture failed: %v", err)}
	}

	if len(res.MCP) == 0 && len(res.MCPServers) > 0 {
		res.MaterializeCapturedMCP(sourcePathForMCP(res))
	}

	lg, _, err := eng.LoadLedger(plan.Ledger)
	if err != nil {
		return []domain.Warning{warningf("mcp-reconcile", "load ledger: %v", err)}
	}

	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	updated := false
	for _, captured := range res.MCP {
		key := domain.MCPLedgerKey(captured.HarnessPath, captured.Server.Name)
		entry, ok := lg.Managed[key]
		if !ok {
			continue // only reconcile entries that were recorded during apply
		}
		trackedContent, terr := domain.MCPTrackedBytes(captured.Server)
		if terr != nil {
			continue
		}
		actualDigest := domain.SingleFileDigest(trackedContent)
		if actualDigest != entry.Digest {
			lg.Record(key, trackedContent, entry.SourcePack, nil, now)
			updated = true
		}
	}

	if updated {
		if serr := eng.SaveLedger(plan.Ledger, lg, false); serr != nil {
			return []domain.Warning{warningf("mcp-reconcile", "save ledger: %v", serr)}
		}
	}
	return nil
}

func printDryRun(eng *engine.Engine, plan domain.Plan, req SyncRequest, reg *harness.Registry, counts ContentCounts, w io.Writer) {
	baseDir := req.ProjectDir
	if req.Scope == domain.ScopeGlobal {
		baseDir = req.Home
	}

	ledgers := map[domain.Harness]domain.Ledger{}
	for _, hid := range req.Harnesses {
		path := engine.LedgerPathForScope(req.Scope, req.ProjectDir, req.Home, hid)
		if lg, _, err := eng.LoadLedger(path); err == nil {
			ledgers[hid] = lg
		}
	}

	rootsIdx := harness.BuildRootsIndex(reg, req.Scope, baseDir, req.Home)
	ledgerForPath := func(path string) domain.Ledger {
		hid := rootsIdx.Identify(path)
		if lg, ok := ledgers[hid]; ok {
			return lg
		}
		if plan.Ledger != "" {
			if lg, _, err := eng.LoadLedger(plan.Ledger); err == nil {
				return lg
			}
		}
		return domain.NewLedger()
	}

	var changes, skips int
	for _, wr := range plan.Writes {
		kind, err := classifyWriteKind(eng, wr, ledgerForPath(wr.Dst))
		if err != nil {
			fmt.Fprintf(w, "create: %s\n", wr.Dst)
			changes++
			continue
		}
		switch kind {
		case domain.DiffIdentical:
			skips++
		case domain.DiffCreate:
			fmt.Fprintf(w, "create: %s\n", wr.Dst)
			changes++
		case domain.DiffManaged:
			fmt.Fprintf(w, "update: %s\n", wr.Dst)
			changes++
		case domain.DiffConflict:
			if req.Force {
				fmt.Fprintf(w, "overwrite: %s\n", wr.Dst)
				changes++
			} else {
				fmt.Fprintf(w, "skip(conflict): %s\n", wr.Dst)
				skips++
			}
		}
	}
	for _, cp := range plan.Copies {
		lg := ledgerForPath(cp.Dst)
		dirKind := classifyCopyKind(eng, cp, lg)
		switch dirKind {
		case domain.DiffIdentical:
			skips++
		case domain.DiffCreate:
			fmt.Fprintf(w, "create: %s\n", cp.Dst)
			changes++
		case domain.DiffManaged:
			fmt.Fprintf(w, "update: %s\n", cp.Dst)
			changes++
		case domain.DiffConflict:
			if req.Force {
				fmt.Fprintf(w, "overwrite: %s\n", cp.Dst)
				changes++
			} else {
				fmt.Fprintf(w, "skip(conflict): %s\n", cp.Dst)
				skips++
			}
		}
	}
	source := counts.String()
	if n := len(plan.Settings); n > 0 {
		source += fmt.Sprintf(", %d settings", n)
	}
	fmt.Fprintf(w, "plan: %d file ops from %s, %d identical\n", changes, source, skips)
}

// classifyCopyKind aggregates per-file classifications for a CopyAction into a
// single DiffKind. For directory copies it walks the source and classifies each
// file; for file copies it classifies the single file. The worst per-file kind
// wins: any conflict makes the whole copy a conflict; any non-identical file
// without conflict makes it managed; all identical means identical.
func classifyCopyKind(eng *engine.Engine, cp domain.CopyAction, lg domain.Ledger) domain.DiffKind {
	switch cp.Kind {
	case domain.CopyKindDir:
		fds, err := eng.ClassifyCopy(cp.Src, cp.Dst, cp.SourcePack, lg)
		if err != nil {
			// Can't classify — treat as new copy so it shows up.
			return domain.DiffCreate
		}
		worst := domain.DiffIdentical
		for _, fd := range fds {
			switch fd.Kind {
			case domain.DiffConflict:
				return domain.DiffConflict
			case domain.DiffManaged:
				worst = domain.DiffManaged
			case domain.DiffCreate:
				if worst == domain.DiffIdentical {
					worst = domain.DiffCreate
				}
			}
		}
		return worst
	case domain.CopyKindFile:
		content, err := eng.FS.ReadFile(cp.Src)
		if err != nil {
			return domain.DiffCreate
		}
		fd, err := eng.ClassifyFile(cp.Dst, content, filepath.Base(cp.Dst), cp.SourcePack, lg)
		if err != nil {
			return domain.DiffCreate
		}
		return fd.Kind
	default:
		return domain.DiffCreate
	}
}

func updateIndex(profile domain.Profile, home string) error {
	db, err := openIndexDB("", home)
	if err != nil {
		return err
	}
	defer db.Close()

	for _, pack := range profile.Packs {
		info, resources := index.ExtractFromPack(pack)

		// Load the manifest for content that bypasses or isn't included
		// in the resolved pack (prompts always, other types when quiet).
		manifestPath := filepath.Join(pack.Root, "pack.json")
		if m, merr := config.LoadPackManifest(manifestPath); merr == nil {
			packRoot := config.ResolvePackRoot(manifestPath, m.Root)
			if packRoot == "" {
				packRoot = pack.Root
			}

			// Index manifest content not already provided by the resolved
			// pack. Resolved resources have richer parsed data; manifest
			// items fill gaps for discovery (quiet packs, partial includes).
			skip := map[string]bool{}
			for _, r := range pack.Rules {
				skip[r.Name] = true
			}
			for _, s := range pack.Skills {
				skip[s.Name] = true
			}
			for _, a := range pack.Agents {
				skip[a.Name] = true
			}
			for _, w := range pack.Workflows {
				skip[w.Name] = true
			}
			resources = append(resources, indexManifestContent(pack.Name, m, packRoot, skip)...)
		}

		if err := db.Update(info, resources); err != nil {
			return fmt.Errorf("updating index for pack %s: %w", pack.Name, err)
		}
	}
	return nil
}

// indexInstalledPack indexes a single installed pack into the search index
// by reading its manifest and content files directly. Called at install time
// so search works without requiring a full sync first.
func indexInstalledPack(configDir, packName, packRoot string) error {
	db, err := openIndexDB(configDir, "")
	if err != nil {
		return err
	}
	defer db.Close()

	manifestPath := filepath.Join(packRoot, "pack.json")
	m, err := config.LoadPackManifest(manifestPath)
	if err != nil {
		return nil // no manifest = nothing to index, not an error
	}

	root := config.ResolvePackRoot(manifestPath, m.Root)
	if root == "" {
		root = packRoot
	}

	info := index.PackInfo{
		Name:      packName,
		Version:   m.Version,
		Installed: true,
		Source:    "install",
	}

	resources := indexManifestContent(packName, m, root, nil)
	return db.Update(info, resources)
}

// indexManifestContent reads content from a manifest's declared files and
// returns index resources. The skip set excludes IDs already provided by
// the resolved pack (which has richer parsed data). Pass nil to index everything.
func indexManifestContent(packName string, m config.PackManifest, packRoot string, skip map[string]bool) []index.Resource {
	flatPath := func(dir, ext string) func(string) (string, string) {
		return func(id string) (string, string) {
			p := filepath.Join(dir, id+ext)
			return p, p
		}
	}
	skillPath := func(id string) (string, string) {
		dir := filepath.Join(packRoot, "skills", id)
		return filepath.Join(dir, "SKILL.md"), dir
	}

	var resources []index.Resource
	resources = append(resources, indexContentFromManifest("rule", diffIDs(m.Rules, skip), flatPath(filepath.Join(packRoot, "rules"), ".md"))...)
	resources = append(resources, indexContentFromManifest("skill", diffIDs(m.Skills, skip), skillPath)...)
	resources = append(resources, indexContentFromManifest("agent", diffIDs(m.Agents, skip), flatPath(filepath.Join(packRoot, "agents"), ".md"))...)
	resources = append(resources, indexContentFromManifest("workflow", diffIDs(m.Workflows, skip), flatPath(filepath.Join(packRoot, "workflows"), ".md"))...)

	for _, pe := range PromptListForPack(packName, m, packRoot) {
		resources = append(resources, index.PromptResource(
			pe.Name, pe.Description, pe.SourcePath, pe.Category, string(pe.Body),
		))
	}
	return resources
}

// indexContentFromManifest reads content files and returns lightweight index
// resources. Each ID is resolved to a file path via pathFn(id), which returns
// (filePath, resourcePath) — the file to read and the path stored in the index.
// Files that can't be read or parsed are silently skipped.
func indexContentFromManifest(kind string, ids []string, pathFn func(id string) (filePath, resourcePath string)) []index.Resource {
	var resources []index.Resource
	for _, id := range ids {
		fp, rp := pathFn(id)
		raw, err := os.ReadFile(fp)
		if err != nil {
			continue
		}
		fm, body, err := domain.SplitFrontmatter(raw)
		if err != nil {
			continue
		}
		var meta map[string]any
		if fm != nil {
			_ = yaml.Unmarshal(fm, &meta)
		}
		resources = append(resources, index.ResourceFromMetadata(
			kind, id, index.MetaString(meta, "description"), rp, meta, string(body),
		))
	}
	return resources
}

// diffIDs returns manifest IDs not present in the skip set.
func diffIDs(manifestIDs []string, resolved map[string]bool) []string {
	if len(resolved) == 0 {
		return manifestIDs
	}
	var out []string
	for _, id := range manifestIDs {
		if !resolved[id] {
			out = append(out, id)
		}
	}
	return out
}

// processEmbeddedRegistries loads registry YAML files declared in pack
// manifests and indexes their entries into the search index.
//
// Error policy: registry load failures are warnings (degraded but functional).
// Registry save/index failures are also warnings — a failed cache shouldn't
// abort a successful sync.
func processEmbeddedRegistries(profile domain.Profile, home string, stderr io.Writer) []domain.Warning {
	var warnings []domain.Warning
	var allEntries []config.Registry
	for _, pack := range profile.Packs {
		for _, regPath := range pack.Registries {
			absPath := filepath.Join(pack.Root, regPath)
			reg, err := config.LoadRegistry(absPath)
			if err != nil {
				warnings = append(warnings, warningPathf(absPath, "registry", "loading embedded registry %s from pack %s: %v", regPath, pack.Name, err))
				continue
			}
			allEntries = append(allEntries, reg)
		}
	}
	if len(allEntries) == 0 {
		return warnings
	}

	cfgDir, err := config.DefaultConfigDir(home)
	if err != nil {
		return append(warnings, warningf("registry", "resolving config dir: %v", err))
	}

	merged := config.Registry{
		SchemaVersion: config.RegistrySchemaVersion,
		Packs:         make(map[string]config.RegistryEntry),
	}
	for _, reg := range allEntries {
		for name, entry := range reg.Packs {
			if _, exists := merged.Packs[name]; !exists {
				merged.Packs[name] = entry
			}
		}
	}
	if len(merged.Packs) == 0 {
		return warnings
	}

	if err := saveEmbeddedRegistry(cfgDir, merged); err != nil {
		return append(warnings, warningf("registry", "saving embedded registry cache: %v", err))
	}
	if err := indexRegistryEntries(merged, cfgDir); err != nil {
		return append(warnings, warningf("registry", "indexing embedded registry entries: %v", err))
	}

	fmt.Fprintf(stderr, "Merged and indexed %d pack(s) from embedded registries\n", len(merged.Packs))
	return warnings
}

func saveEmbeddedRegistry(configDir string, reg config.Registry) error {
	scPath := config.SyncConfigPath(configDir)
	sc, err := config.LoadSyncConfig(scPath)
	if err != nil {
		return fmt.Errorf("loading sync-config: %w", err)
	}
	upsertRegistrySource(&sc, config.RegistrySourceEntry{
		Name: embeddedRegistrySourceName,
		URL:  "embedded://sync",
	})
	if err := config.SaveSyncConfig(scPath, sc); err != nil {
		return fmt.Errorf("saving sync-config: %w", err)
	}

	cacheDir := config.RegistriesCacheDir(configDir)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return fmt.Errorf("creating registries cache dir: %w", err)
	}
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return fmt.Errorf("marshalling registry: %w", err)
	}
	cachePath := config.SourceCachePath(configDir, embeddedRegistrySourceName)
	if err := util.WriteFileAtomicWithPerms(cachePath, out, 0o700, 0o600); err != nil {
		return fmt.Errorf("writing cached registry: %w", err)
	}
	return nil
}

func printDryRunVerbose(summary PlanSummary, w io.Writer) {
	total := summary.TotalChanges()
	if total == 0 {
		fmt.Fprintln(w, "plan: no changes")
		return
	}
	fmt.Fprintf(w, "plan: %d changes (%d rules, %d workflows, %d agents, %d skills, %d settings, %d mcp, %d stale)\n",
		total, summary.NumRules, summary.NumWorkflows, summary.NumAgents, summary.NumSkills,
		summary.NumSettings, summary.NumMCP, summary.NumStale)

	for _, op := range summary.Ops {
		label := string(op.Kind)
		if op.DiffKind != "" {
			label = string(op.DiffKind)
		}
		if op.SourcePack != "" {
			fmt.Fprintf(w, "\n%s: %s [%s]\n", label, op.Dst, op.SourcePack)
		} else {
			fmt.Fprintf(w, "\n%s: %s\n", label, op.Dst)
		}
		if len(op.MergeOps) > 0 {
			printMergeOps(op.MergeOps, w)
		}
		if op.Diff != "" {
			fmt.Fprintln(w, op.Diff)
		}
	}
}

// printMergeOps formats merge operations for verbose dry-run output.
func printMergeOps(ops []engine.MergeOp, w io.Writer) {
	for _, m := range ops {
		fmt.Fprintf(w, "  merge %s: %s\n", m.Action, m.Key)
	}
}
