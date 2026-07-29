package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	// PrevInventories carries per-pack inventories loaded from the
	// lockfile so the resolver can surface drifted-out refs as
	// BrokenRefs instead of hard errors. Nil preserves the pre-drift
	// behavior (every unknown ref errors).
	PrevInventories map[string]domain.PackInventory
}

// SyncContext holds the resolved profile and targeting information.
type SyncContext struct {
	Profile domain.Profile
	TargetSpec
}

// ResolveProfile resolves a profile config into a fully-typed profile with
// targeting information (scope, harnesses, project dir) from sync-config defaults.
func ResolveProfile(eng *engine.Engine, req ResolveRequest) (SyncContext, []domain.Warning, error) {
	profile, warnings, err := eng.ResolveWithOptions(req.ProfileCfg, req.ProfilePath, req.ConfigDir, config.ResolveOptions{
		CollisionStrategy: req.SyncCfg.Defaults.CollisionStrategy,
		Namespaced:        req.SyncCfg.Defaults.Namespaced,
		PrevInventories:   req.PrevInventories,
	})
	if err != nil {
		return SyncContext{}, warnings, err
	}

	scope := domain.ScopeGlobal
	if req.SyncCfg.Defaults.Scope != "" {
		if s, serr := cmdutil.NormalizeScope(req.SyncCfg.Defaults.Scope); serr == nil {
			scope = s
		}
	}

	hs, err := cmdutil.ResolveHarnesses(req.SyncCfg.Defaults.Harnesses)
	if err != nil {
		return SyncContext{}, warnings, err
	}

	return SyncContext{
		Profile: profile,
		TargetSpec: TargetSpec{
			ConfigDir:  req.ConfigDir,
			Scope:      scope,
			ProjectDir: req.ProjectDir,
			Harnesses:  hs,
			Home:       req.Home,
			Namespaced: req.SyncCfg.Defaults.Namespaced,
		},
	}, warnings, nil
}

// ResolveActiveProfile loads the active profile from sync-config defaults
// and resolves it into a fully-typed profile with targeting information.
// This is the I/O boundary: it resolves ProjectDir and Home from the
// environment so that ResolveProfile (and everything below it) stays I/O-free.
func ResolveActiveProfile(eng *engine.Engine, configDir string) (SyncContext, []domain.Warning, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return SyncContext{}, nil, fmt.Errorf("resolving working directory: %w", err)
	}
	home := config.HomeDir()
	syncCfgPath := config.SyncConfigPath(configDir)
	syncCfg, err := config.LoadSyncConfig(syncCfgPath)
	if err != nil {
		return SyncContext{}, nil, fmt.Errorf("loading sync-config: %w", err)
	}
	profileName := syncCfg.Defaults.Profile
	if profileName == "" {
		profileName = "default"
	}
	profilePath := filepath.Join(configDir, "profiles", profileName+".yaml")
	profileCfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return SyncContext{}, nil, err
	}
	prevInventories, _ := loadLockfileInventories(configDir)
	return ResolveProfile(eng, ResolveRequest{
		ConfigDir:       configDir,
		ProfilePath:     profilePath,
		ProfileCfg:      profileCfg,
		SyncCfg:         syncCfg,
		ProjectDir:      cwd,
		Home:            home,
		PrevInventories: prevInventories,
	})
}

// loadLockfileInventories returns per-pack inventories from the lockfile so
// profile resolution can distinguish drifted-out refs from typos. Returns nil
// on any error to preserve pre-drift behavior.
func loadLockfileInventories(configDir string) (map[string]domain.PackInventory, error) {
	if configDir == "" {
		return nil, nil
	}
	lf, err := config.EnsureLockfileMigrated(configDir)
	if err != nil {
		return nil, err
	}
	if len(lf.Packs) == 0 {
		return nil, nil
	}
	out := make(map[string]domain.PackInventory, len(lf.Packs))
	for name, meta := range lf.Packs {
		if meta.Resolved != nil {
			out[name] = *meta.Resolved
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ResolveTargetSpec builds a TargetSpec from sync-config defaults without
// requiring a resolved profile. Use this when you need targeting information
// (scope, project dir, harnesses) but not pack resolution.
func ResolveTargetSpec(syncCfg config.SyncConfig, configDir, projectDir, home string) (TargetSpec, error) {
	scope := domain.ScopeGlobal
	if syncCfg.Defaults.Scope != "" {
		if s, serr := cmdutil.NormalizeScope(syncCfg.Defaults.Scope); serr == nil {
			scope = s
		}
	}
	hs, err := cmdutil.ResolveHarnesses(syncCfg.Defaults.Harnesses)
	if err != nil {
		return TargetSpec{}, err
	}

	return TargetSpec{
		ConfigDir:  configDir,
		Scope:      scope,
		ProjectDir: projectDir,
		Harnesses:  hs,
		Home:       home,
		Namespaced: syncCfg.Defaults.Namespaced,
	}, nil
}

// resolvePackRoots extracts pack name → root mappings from a resolved profile.
func resolvePackRoots(profile domain.Profile) map[string]string {
	roots := make(map[string]string, len(profile.Packs))
	for _, p := range profile.Packs {
		roots[p.Name] = p.Root
	}
	return roots
}

func knownPacksFromRoots(packRoots map[string]string) map[string]struct{} {
	if len(packRoots) == 0 {
		return nil
	}
	known := make(map[string]struct{}, len(packRoots))
	for name := range packRoots {
		known[name] = struct{}{}
	}
	return known
}

// TargetSpec holds the common targeting fields shared across request types.
type TargetSpec struct {
	ConfigDir       string
	Scope           domain.Scope
	ProjectDir      string
	Harnesses       []domain.Harness
	ActiveHarnesses []domain.Harness // full resolved sync target set; defaults to Harnesses
	Home            string           // $HOME — threaded explicitly for testability
	Namespaced      bool             // render pack-authored content with pack provenance suffixes
	Env             map[string]string
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

// SyncResult holds the result of a sync operation against one harness.
type SyncResult struct {
	Harness  domain.Harness
	Plan     domain.Plan
	Warnings []domain.Warning
}

// RunSyncEach syncs each resolved harness independently, in profile order. Each
// harness gets its own plan, apply, ledger, and self-contained output block —
// there is no cross-harness aggregation. It stops at the first harness that
// fails and returns the results gathered so far along with the error.
func RunSyncEach(ctx context.Context, eng *engine.Engine, profile domain.Profile, req SyncRequest, reg *harness.Registry, stdout, stderr io.Writer) ([]SyncResult, []domain.Warning, error) {
	if err := ValidateSyncHarnesses(req.Harnesses); err != nil {
		return nil, nil, wrapFatalSync("validate sync targets", err)
	}
	activeHarnesses := append([]domain.Harness{}, req.Harnesses...)
	req.ActiveHarnesses = activeHarnesses
	var warnings []domain.Warning
	req, newInventories, prepWarnings := prepareSyncProfile(eng, profile, req, reg, stdout, stderr)
	warnings = append(warnings, prepWarnings...)

	results := make([]SyncResult, 0, len(req.Harnesses))
	for _, hid := range req.Harnesses {
		single := req
		single.Harnesses = []domain.Harness{hid}
		res, w, err := runSyncHarness(ctx, eng, profile, single, hid, reg, stdout, stderr)
		warnings = append(warnings, w...)
		res.Harness = hid
		res.Warnings = w
		results = append(results, res)
		if err != nil {
			return results, warnings, err
		}
	}
	warnings = append(warnings, finishSyncProfile(profile, req, newInventories, stderr)...)
	return results, warnings, nil
}

// ValidateSyncHarnesses checks that a sync run has at least one target harness.
func ValidateSyncHarnesses(hs []domain.Harness) error {
	if len(hs) == 0 {
		return fmt.Errorf("sync requires at least one harness")
	}
	return nil
}

// RunSync plans and applies a sync. It is the primary v2 entry point for the sync command.
//
// Error policy: loading/reading failures that degrade gracefully are returned
// as warnings. Writing failures that leave inconsistent state are fatal errors.
func RunSync(ctx context.Context, eng *engine.Engine, profile domain.Profile, req SyncRequest, reg *harness.Registry, stdout, stderr io.Writer) (SyncResult, []domain.Warning, error) {
	var warnings []domain.Warning
	hid, err := SingleSyncHarness(req.Harnesses)
	if err != nil {
		return SyncResult{}, warnings, wrapFatalSync("validate sync target", err)
	}
	req, newInventories, prepWarnings := prepareSyncProfile(eng, profile, req, reg, stdout, stderr)
	warnings = append(warnings, prepWarnings...)

	res, runWarnings, err := runSyncHarness(ctx, eng, profile, req, hid, reg, stdout, stderr)
	warnings = append(warnings, runWarnings...)
	if err != nil {
		return res, warnings, err
	}
	warnings = append(warnings, finishSyncProfile(profile, req, newInventories, stderr)...)
	return res, warnings, nil
}

func prepareSyncProfile(eng *engine.Engine, profile domain.Profile, req SyncRequest, reg *harness.Registry, stdout, stderr io.Writer) (SyncRequest, map[string]domain.PackInventory, []domain.Warning) {
	var warnings []domain.Warning
	req.ConfigDir = config.FallbackConfigDir(req.ConfigDir, req.Home)

	// Build new pack inventories and diff against the previous lockfile
	// state. Printed before the harness loop so the user sees drift in
	// both dry-run and real-sync modes. The writeback happens after a
	// successful apply further down (dry-run does not write).
	now := time.Now
	if req.NowFn != nil {
		now = req.NowFn
	}
	newInventories, invErr := engine.BuildInventories(eng, req.ConfigDir, profile, now())
	if invErr != nil {
		warnings = append(warnings, warningf("inventory", "building pack inventories failed: %v", invErr))
		newInventories = nil
	}
	// Stamp each new inventory with the ref currently recorded in the
	// lockfile so the next sync can report the version transition.
	if lf, lfErr := config.LoadLockfile(config.LockfilePath(req.ConfigDir)); lfErr == nil {
		for name, inv := range newInventories {
			if meta, ok := lf.Packs[name]; ok {
				inv.CapturedAtRef = meta.Ref
				newInventories[name] = inv
			}
		}
	}
	printDrift(stdout, req.ConfigDir, profile, newInventories)

	if !req.DryRun {
		warnings = append(warnings, migrateLegacyLedgersForSync(eng, reg, req, stderr)...)
	}
	return req, newInventories, warnings
}

func migrateLegacyLedgersForSync(eng *engine.Engine, reg *harness.Registry, req SyncRequest, stderr io.Writer) []domain.Warning {
	// Route every legacy entry to the harness that currently writes to its
	// location so a single-harness sync does not claim another harness's files.
	migrationHarnesses := make([]domain.Harness, 0, len(reg.All()))
	managedRootsMap := map[domain.Harness][]string{}
	for _, h := range reg.All() {
		id := h.ID()
		migrationHarnesses = append(migrationHarnesses, id)
		managedRootsMap[id] = h.Layout(req.Scope, targetDirForHarness(req.TargetSpec, id), req.Home).ValidationRoots
	}
	n, err := eng.MigrateOldLedgers(req.ConfigDir, req.Scope, req.ProjectDir, migrationHarnesses, managedRootsMap)
	if err != nil {
		return []domain.Warning{warningf("ledger-migration", "%v", err)}
	}
	if n > 0 && stderr != nil {
		fmt.Fprintf(stderr, "migrated %d ledger entries to per-harness format\n", n)
	}
	return nil
}

func finishSyncProfile(profile domain.Profile, req SyncRequest, newInventories map[string]domain.PackInventory, stderr io.Writer) []domain.Warning {
	if req.DryRun {
		return nil
	}
	var warnings []domain.Warning
	if idxErr := updateIndex(profile, req.ConfigDir); idxErr != nil {
		warnings = append(warnings, warningf("index", "index update failed: %v", idxErr))
	}
	warnings = append(warnings, processEmbeddedRegistries(profile, req.ConfigDir, stderr)...)

	if invErr := writebackInventories(req.ConfigDir, newInventories); invErr != nil {
		warnings = append(warnings, warningf("inventory", "lockfile inventory writeback failed: %v", invErr))
	}
	return warnings
}

func runSyncHarness(ctx context.Context, eng *engine.Engine, profile domain.Profile, req SyncRequest, hid domain.Harness, reg *harness.Registry, stdout, stderr io.Writer) (SyncResult, []domain.Warning, error) {
	var warnings []domain.Warning

	planners, err := reg.AsPlanners([]domain.Harness{hid})
	if err != nil {
		return SyncResult{}, warnings, wrapFatalSync("lookup harness planners", err)
	}
	planReq := planRequestForHarness(req, hid)
	plan, err := engine.PlanSync(ctx, profile, planReq, planners)
	if err != nil {
		return SyncResult{}, warnings, wrapFatalSync("plan sync", err)
	}
	warnings = append(warnings, plan.Warnings...)

	if req.DryRun {
		h, _ := reg.Lookup(hid)
		layout := h.Layout(req.Scope, targetDirForHarness(req.TargetSpec, hid), req.Home)
		validationWarnings, validationErr := eng.ApplyPlan(ctx, plan, engine.ApplyRequest{
			Force:  req.Force,
			Yes:    req.Yes,
			DryRun: true,
			Quiet:  true,
			Req:    planReq,
			LabelForPath: func(path string) string {
				return syncDisplayPath(hid, path)
			},
		}, layout.ValidationRoots)
		warnings = append(warnings, validationWarnings...)
		if validationErr != nil {
			if stderr != nil {
				fmt.Fprintf(stderr, "error: sync: %v\n", validationErr)
			}
			return SyncResult{}, warnings, wrapFatalSync("validate dry-run plan", validationErr)
		}
		if req.Verbose {
			summary, err := PlanWithDiffs(ctx, eng, profile, req, reg)
			if err != nil {
				if stderr != nil {
					fmt.Fprintf(stderr, "error: sync: %v\n", err)
				}
				return SyncResult{}, warnings, wrapFatalSync("compute verbose dry-run diffs", err)
			}
			printDryRunVerbose(summary, stdout)
		} else {
			printDryRun(eng, plan, req, CountProfileContent(profile), stdout)
		}
		return SyncResult{Harness: hid, Plan: plan}, warnings, nil
	}

	h, _ := reg.Lookup(hid)
	layout := h.Layout(req.Scope, targetDirForHarness(req.TargetSpec, hid), req.Home)
	managedRoots := layout.ValidationRoots
	cleanup := staleCleanupContextForHarness(eng, reg, req.TargetSpec, hid, layout, nil)
	if lg, _, lerr := eng.LoadLedger(plan.Ledger); lerr == nil {
		if candidates, cerr := eng.StaleCandidatesWithLedger(plan, cleanup.Roots, lg); cerr == nil {
			cleanup = staleCleanupContextForHarness(eng, reg, req.TargetSpec, hid, layout, candidates)
		}
	}

	stripFuncs := make(map[string]func([]byte, domain.Ledger) ([]byte, error), len(layout.OwnedFiles))
	for _, of := range layout.OwnedFiles {
		stripFuncs[filepath.Clean(of.Path)] = stripFuncForOwnedFile(of)
	}

	applyReq := engine.ApplyRequest{
		Force:      req.Force,
		Yes:        req.Yes,
		DryRun:     req.DryRun,
		Quiet:      req.Quiet,
		Stdout:     stderr,
		Req:        planReq,
		StaleRoots: cleanup.StaleRoots,
		LabelForPath: func(path string) string {
			return syncDisplayPath(hid, path)
		},
		PruneOnlyStalePaths: cleanup.Protected,
		StripFuncs:          stripFuncs,
	}

	applyWarnings, err := eng.ApplyPlan(ctx, plan, applyReq, managedRoots)
	warnings = append(warnings, applyWarnings...)
	if err != nil {
		if stderr != nil {
			fmt.Fprintf(stderr, "error: sync: %v\n", err)
		}
		return SyncResult{}, warnings, wrapFatalSync("apply plan", err)
	}
	warnings = append(warnings, newInactiveStaleContext(eng, reg, req.TargetSpec, hid, cleanup.StaleRoots).prune(ctx, engine.PruneLedgerManagedRequest{
		Yes:    req.Yes,
		DryRun: req.DryRun,
	})...)

	// Reconcile per-server MCP ledger entries with actual on-disk state.
	// ApplyPlan records per-server entries using planned content, but
	// three-way merge may write different content (preserving user
	// additions). Re-capture from the now-updated file and record the
	// actual digests so that save/inspect classify correctly.
	if len(plan.MCPServers) > 0 {
		reconWarnings := reconcileMCPLedger(ctx, eng, plan, h, req, req.NowFn)
		warnings = append(warnings, reconWarnings...)
	}

	return SyncResult{Harness: hid, Plan: plan}, warnings, nil
}

func ValidateSingleSyncHarness(hs []domain.Harness) error {
	if len(hs) == 1 {
		return nil
	}
	if len(hs) == 0 {
		return fmt.Errorf("sync requires exactly one harness")
	}
	names := make([]string, len(hs))
	for i, h := range hs {
		names[i] = string(h)
	}
	return fmt.Errorf("sync targets one harness per run; resolved %s", strings.Join(names, ","))
}

func SingleSyncHarness(hs []domain.Harness) (domain.Harness, error) {
	if err := ValidateSingleSyncHarness(hs); err != nil {
		return "", err
	}
	return hs[0], nil
}

func FirstSyncHarness(hs []domain.Harness) (domain.Harness, bool) {
	if len(hs) == 0 {
		return "", false
	}
	return hs[0], true
}

func ResolveSingleSyncHarness(raw []string) (domain.Harness, error) {
	hs, err := cmdutil.ResolveHarnesses(raw)
	if err != nil {
		return "", err
	}
	return SingleSyncHarness(hs)
}

// printDrift compares the new per-pack inventories against the previous
// state in the lockfile and emits typed drift events for each pack that
// changed or has broken profile references. Non-fatal on any error.
//
// Events follow the sync.drift.* taxonomy: a per-pack `report_starting`
// event, optional `section` events for each populated subsection, then
// per-item `removed_referenced` / `removed` / `added` / `changed` events
// with sub-detail events for skill and MCP context.
func printDrift(stdout io.Writer, configDir string, profile domain.Profile, newInventories map[string]domain.PackInventory) {
	if stdout == nil {
		return
	}
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		return
	}
	brokenByPack := make(map[string][]domain.BrokenRef, len(profile.BrokenRefs))
	for _, br := range profile.BrokenRefs {
		brokenByPack[br.PackName] = append(brokenByPack[br.PackName], br)
	}
	packOrder := make([]string, 0, len(newInventories))
	for _, pk := range profile.Packs {
		packOrder = append(packOrder, pk.Name)
	}
	for _, name := range packOrder {
		newInv, ok := newInventories[name]
		if !ok {
			continue
		}
		meta, hasMeta := lf.Packs[name]
		if !hasMeta || meta.Resolved == nil {
			continue
		}
		diff := engine.DiffInventory(name, *meta.Resolved, newInv)
		oldRef := meta.Resolved.CapturedAtRef
		engine.EmitDriftReport(stdout, diff, brokenByPack[name], oldRef, meta.Ref)
	}
}

// writebackInventories records new per-pack inventories into the lockfile.
// Runs through mutateLockfile so the save is atomic and race-free with
// other lockfile writes in the same process.
func writebackInventories(configDir string, newInventories map[string]domain.PackInventory) error {
	if len(newInventories) == 0 {
		return nil
	}
	return mutateLockfile(configDir, func(lf *config.Lockfile) error {
		for name, inv := range newInventories {
			meta, ok := lf.Packs[name]
			if !ok {
				// Pack not tracked in lockfile yet (local or not-yet-installed);
				// skip rather than creating an orphan entry.
				continue
			}
			invCopy := inv
			meta.Resolved = &invCopy
			lf.Packs[name] = meta
		}
		return nil
	})
}

// reconcileMCPLedger re-captures MCP servers from the now-updated harness
// config and overwrites the per-server ledger entries with actual (post-merge)
// digests. This corrects the planned-vs-merged divergence that occurs when
// three-way merge preserves user additions in the config file.
func reconcileMCPLedger(ctx context.Context, eng *engine.Engine, plan domain.Plan, h harness.Harness, req SyncRequest, nowFn func() time.Time) []domain.Warning {
	if plan.Ledger == "" {
		return nil
	}

	res, err := h.Capture(ctx, captureContextForHarness(req.TargetSpec, h.ID(), nil))
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

func printDryRun(eng *engine.Engine, plan domain.Plan, req SyncRequest, counts ContentCounts, stdout io.Writer) {
	cfgDir := config.FallbackConfigDir(req.ConfigDir, req.Home)

	hid, ok := FirstSyncHarness(req.Harnesses)
	if !ok {
		hid = ""
	}
	ledgerPath := plan.Ledger
	if ledgerPath == "" && hid != "" {
		ledgerPath = engine.LedgerPath(cfgDir, req.Scope, req.ProjectDir, hid)
	}
	lg := domain.NewLedger()
	if ledgerPath != "" {
		if loaded, _, err := eng.LoadLedger(ledgerPath); err == nil {
			lg = loaded
		}
	}

	emitOp := func(verb, path string) {
		if stdout == nil {
			return
		}
		label := syncDisplayPath(hid, path)
		if verb == "skip-conflict" {
			fmt.Fprintf(stdout, "skip(conflict): %s\n", label)
			return
		}
		fmt.Fprintf(stdout, "%s: %s\n", verb, label)
	}

	var changes, skips int
	for _, wr := range plan.Writes {
		kind, err := classifyWriteKind(eng, wr, lg)
		if err != nil {
			emitOp("create", wr.Dst)
			changes++
			continue
		}
		switch kind {
		case domain.DiffIdentical:
			skips++
		case domain.DiffCreate:
			emitOp("create", wr.Dst)
			changes++
		case domain.DiffManaged:
			emitOp("update", wr.Dst)
			changes++
		case domain.DiffConflict:
			if req.Force {
				emitOp("overwrite", wr.Dst)
				changes++
			} else {
				emitOp("skip-conflict", wr.Dst)
				skips++
			}
		}
	}
	for _, cp := range plan.Copies {
		dirKind := classifyCopyKind(eng, cp, lg)
		switch dirKind {
		case domain.DiffIdentical:
			skips++
		case domain.DiffCreate:
			emitOp("create", cp.Dst)
			changes++
		case domain.DiffManaged:
			emitOp("update", cp.Dst)
			changes++
		case domain.DiffConflict:
			if req.Force {
				emitOp("overwrite", cp.Dst)
				changes++
			} else {
				emitOp("skip-conflict", cp.Dst)
				skips++
			}
		}
	}
	// Merge actions count only when aipack-managed keys would change.
	for _, group := range [][]domain.SettingsAction{plan.Settings, plan.MCP} {
		for _, sa := range group {
			kind, derr := eng.ClassifySettingsKind(sa, lg)
			if derr != nil {
				emitOp("merge", sa.Dst)
				changes++
				continue
			}
			if kind == domain.DiffIdentical {
				skips++
				continue
			}
			emitOp("merge", sa.Dst)
			changes++
		}
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "plan: %d file ops from %s, %d identical\n", changes, counts.String(), skips)
	}
}

func stripFuncForOwnedFile(of harness.OwnedFile) func([]byte, domain.Ledger) ([]byte, error) {
	return func(content []byte, lg domain.Ledger) ([]byte, error) {
		mcpIndex := domain.MCPServerNamesByPath(lg.Managed)
		ctx := harness.EditContext{
			ManagedMCPServers:      mcpIndex.ForPath(of.Path),
			PreviousManagedOverlay: lg.PrevManagedOverlay(of.Path),
		}
		return harness.ApplyEdit(content, of.Format, ctx, of.Strip)
	}
}

// classifyCopyKind aggregates per-file classifications for a CopyAction into a
// single DiffKind. For directory copies it walks the source and classifies each
// file; for file copies it classifies the single file. The worst per-file kind
// wins: any conflict makes the whole copy a conflict; any non-identical file
// without conflict makes it managed; all identical means identical.
func classifyCopyKind(eng *engine.Engine, cp domain.CopyAction, lg domain.Ledger) domain.DiffKind {
	switch cp.Kind {
	case domain.CopyKindDir:
		fds, err := eng.ClassifyCopyWithOptions(cp.Src, cp.Dst, cp.SourcePack, lg, engine.ClassifyCopyOptions{
			SourceBoundary: cp.SourceBoundary,
		})
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
		fd, err := eng.ClassifyCopyFileWithOptions(cp.Src, cp.Dst, cp.SourcePack, lg, engine.ClassifyCopyOptions{
			LabelForPath:   func(string) string { return filepath.Base(cp.Dst) },
			SourceBoundary: cp.SourceBoundary,
		})
		if err != nil {
			return domain.DiffCreate
		}
		return fd.Kind
	default:
		return domain.DiffCreate
	}
}

func updateIndex(profile domain.Profile, configDir string) error {
	db, err := openIndexDB(configDir, "")
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
			_ = config.DiscoverContent(&m, packRoot)

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
			resources = append(resources, structuredResourcesFromManifestRoot(
				packRoot,
				m,
				domain.CategoryMCP,
			)...)
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
	_ = config.DiscoverContent(&m, root)

	info := index.PackInfo{
		Name:      packName,
		Version:   m.Version,
		Installed: true,
		Source:    "install",
	}

	resources := resourcesFromManifestRoot(packName, root, m)
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
	resources = append(resources, indexHooksFromManifest(diffIDs(m.Hooks, skip), func(id string) (string, string) {
		p := filepath.Join(packRoot, filepath.FromSlash(m.RelPath(domain.CategoryHooks, id)))
		return p, filepath.Dir(p)
	})...)
	resources = append(resources, indexContentFromManifest("agent", diffIDs(m.Agents, skip), flatPath(filepath.Join(packRoot, "agents"), ".md"))...)
	resources = append(resources, indexContentFromManifest("workflow", diffIDs(m.Workflows, skip), flatPath(filepath.Join(packRoot, "workflows"), ".md"))...)

	for _, pe := range PromptListForPack(packName, m, packRoot) {
		resources = append(resources, index.PromptResource(
			pe.Name, pe.Description, pe.SourcePath, pe.Category, string(pe.Body),
		))
	}
	return resources
}

func indexHooksFromManifest(ids []string, pathFn func(id string) (filePath, resourcePath string)) []index.Resource {
	var resources []index.Resource
	for _, id := range ids {
		fp, rp := pathFn(id)
		raw, err := os.ReadFile(fp)
		if err != nil {
			continue
		}
		var meta struct {
			Description string `yaml:"description"`
		}
		_ = yaml.Unmarshal(raw, &meta)
		resources = append(resources, index.ResourceFromMetadata("hook", id, meta.Description, rp, nil, string(raw)))
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
func processEmbeddedRegistries(profile domain.Profile, cfgDir string, stderr io.Writer) []domain.Warning {
	var warnings []domain.Warning
	var allEntries []config.Registry
	for _, pack := range profile.Packs {
		for _, regID := range pack.Registries {
			absPath := filepath.Join(pack.Root, "registries", regID+".yaml")
			reg, err := config.LoadRegistry(absPath)
			if err != nil {
				warnings = append(warnings, warningPathf(absPath, "registry", "loading embedded registry %s from pack %s: %v", regID, pack.Name, err))
				continue
			}
			allEntries = append(allEntries, reg)
		}
	}
	if len(allEntries) == 0 {
		return warnings
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

	if stderr != nil {
		fmt.Fprintf(stderr, "Merged and indexed %d pack(s) from embedded registries\n", len(merged.Packs))
	}
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

func printDryRunVerbose(summary PlanSummary, stdout io.Writer) {
	total := summary.TotalChanges()
	if total == 0 {
		if stdout != nil {
			fmt.Fprintln(stdout, "plan: no changes")
		}
		return
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "plan: %d changes (%d rules, %d workflows, %d agents, %d skills, %d hooks, %d settings, %d mcp, %d stale)\n",
			total, summary.NumRules, summary.NumWorkflows, summary.NumAgents, summary.NumSkills,
			summary.NumHooks, summary.NumSettings, summary.NumMCP, summary.NumStale)
	}

	for _, op := range summary.Ops {
		label := string(op.Kind)
		if op.DiffKind != "" {
			label = string(op.DiffKind)
		}
		dst := op.DisplayDst
		if dst == "" {
			dst = op.Dst
		}
		if op.SourcePack != "" {
			if stdout != nil {
				fmt.Fprintf(stdout, "\n%s: %s [%s]\n", label, dst, op.SourcePack)
			}
		} else if stdout != nil {
			fmt.Fprintf(stdout, "\n%s: %s\n", label, dst)
		}
		if len(op.MergeOps) > 0 {
			printMergeOps(op.MergeOps, stdout)
		}
		if op.Diff != "" && stdout != nil {
			fmt.Fprintln(stdout, op.Diff)
		}
	}
}

func printMergeOps(ops []engine.MergeOp, stdout io.Writer) {
	for _, m := range ops {
		if stdout != nil {
			fmt.Fprintf(stdout, "  merge %s: %s\n", m.Action, m.Key)
		}
	}
}
