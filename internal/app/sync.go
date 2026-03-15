package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
}

// ResolveResult holds the resolved profile and targeting information.
type ResolveResult struct {
	Profile domain.Profile
	TargetSpec
}

// ResolveProfile resolves a profile config into a fully-typed profile with
// targeting information (scope, harnesses, project dir) from sync-config defaults.
func ResolveProfile(req ResolveRequest) (ResolveResult, []domain.Warning, error) {
	profile, warnings, err := engine.Resolve(req.ProfileCfg, req.ProfilePath, req.ConfigDir)
	if err != nil {
		return ResolveResult{}, warnings, err
	}

	scope := domain.ScopeProject
	if req.SyncCfg.Defaults.Scope != "" {
		if s, serr := cmdutil.NormalizeScope(req.SyncCfg.Defaults.Scope); serr == nil {
			scope = s
		}
	}

	cwd, _ := os.Getwd()

	hs, err := cmdutil.ResolveHarnesses(req.SyncCfg.Defaults.Harnesses)
	if err != nil {
		return ResolveResult{}, warnings, err
	}

	return ResolveResult{
		Profile: profile,
		TargetSpec: TargetSpec{
			Scope:      scope,
			ProjectDir: cwd,
			Harnesses:  hs,
			Home:       os.Getenv("HOME"),
		},
	}, warnings, nil
}

// ResolveActiveProfile loads the active profile from sync-config defaults
// and resolves it into a fully-typed profile with targeting information.
// This is the primary entry point for callers that don't need custom
// profile resolution (e.g., in-memory edits).
func ResolveActiveProfile(configDir string) (ResolveResult, []domain.Warning, error) {
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
	return ResolveProfile(ResolveRequest{
		ConfigDir:   configDir,
		ProfilePath: profilePath,
		ProfileCfg:  profileCfg,
		SyncCfg:     syncCfg,
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
	Prune        bool
	SkipSettings bool
	Yes          bool
	DryRun       bool
	Quiet        bool
	Verbose      bool
}

// SyncResult holds the result of a sync operation.
type SyncResult struct {
	Plan domain.Plan
}

// RunSync plans and applies a sync. It is the primary v2 entry point for the sync command.
func RunSync(profile domain.Profile, req SyncRequest, reg *harness.Registry, stdout, stderr io.Writer) (SyncResult, error) {
	baseDir := req.ProjectDir
	if req.Scope == domain.ScopeGlobal {
		baseDir = req.Home
	}

	if !req.DryRun {
		// Migrate legacy ledgers (combined-harness or project-local) on first run.
		managedRootsMap := map[string][]string{}
		harnessNames := make([]string, len(req.Harnesses))
		for i, hid := range req.Harnesses {
			harnessNames[i] = strings.ToLower(string(hid))
			if h, lerr := reg.Lookup(hid); lerr == nil {
				managedRootsMap[harnessNames[i]] = h.ManagedRoots(req.Scope, baseDir, req.Home)
			}
		}
		if n, merr := engine.MigrateOldLedgers(req.Scope, req.ProjectDir, req.Home, harnessNames, managedRootsMap); merr != nil {
			fmt.Fprintf(stderr, "warning: ledger migration: %v\n", merr)
		} else if n > 0 {
			fmt.Fprintf(stderr, "migrated %d ledger entries to per-harness format\n", n)
		}
	}

	// Plan and apply per harness — each gets its own ledger.
	aggregatePlan := domain.Plan{Desired: map[string]struct{}{}}
	for _, hid := range req.Harnesses {
		planners, err := reg.AsPlanners([]domain.Harness{hid})
		if err != nil {
			return SyncResult{}, err
		}

		planReq := engine.PlanRequest{
			Scope:        req.Scope,
			Harnesses:    []domain.Harness{hid},
			ProjectDir:   req.ProjectDir,
			Home:         req.Home,
			SkipSettings: req.SkipSettings,
		}

		plan, err := engine.PlanSync(profile, planReq, planners)
		if err != nil {
			return SyncResult{}, err
		}

		if req.DryRun {
			mergePlans(&aggregatePlan, plan)
			continue
		}

		h, _ := reg.Lookup(hid)
		managedRoots := h.ManagedRoots(req.Scope, baseDir, req.Home)

		applyReq := engine.ApplyRequest{
			Force:  req.Force,
			Prune:  req.Prune,
			Yes:    req.Yes,
			DryRun: req.DryRun,
			Quiet:  req.Quiet,
			Req:    planReq,
		}

		if err := engine.ApplyPlan(plan, applyReq, managedRoots); err != nil {
			return SyncResult{}, err
		}
		mergePlans(&aggregatePlan, plan)
	}

	if req.DryRun {
		if req.Verbose {
			summary, err := PlanWithDiffs(profile, req, reg)
			if err != nil {
				return SyncResult{}, err
			}
			printDryRunVerbose(summary, stdout)
		} else {
			printDryRun(aggregatePlan, req, reg, stdout)
		}
		return SyncResult{Plan: aggregatePlan}, nil
	}

	// Post-sync tasks — run once.
	if idxErr := updateIndex(profile, req.Home); idxErr != nil {
		fmt.Fprintf(stderr, "warning: index update failed: %v\n", idxErr)
	}
	if regErr := processEmbeddedRegistries(profile, req.Home, stderr); regErr != nil {
		fmt.Fprintf(stderr, "warning: embedded registry processing failed: %v\n", regErr)
	}

	return SyncResult{Plan: aggregatePlan}, nil
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

func printDryRun(plan domain.Plan, req SyncRequest, reg *harness.Registry, w io.Writer) {
	baseDir := req.ProjectDir
	if req.Scope == domain.ScopeGlobal {
		baseDir = req.Home
	}

	ledgers := map[domain.Harness]domain.Ledger{}
	for _, hid := range req.Harnesses {
		path := engine.LedgerPathForScope(req.Scope, req.ProjectDir, req.Home, strings.ToLower(string(hid)))
		if lg, _, err := engine.LoadLedger(path); err == nil {
			ledgers[hid] = lg
		}
	}

	ledgerForPath := func(path string) domain.Ledger {
		hid := harness.IdentifyHarness(reg, req.Scope, baseDir, req.Home, path)
		if lg, ok := ledgers[hid]; ok {
			return lg
		}
		if plan.Ledger != "" {
			if lg, _, err := engine.LoadLedger(plan.Ledger); err == nil {
				return lg
			}
		}
		return domain.NewLedger()
	}

	var changes, skips int
	for _, wr := range plan.Writes {
		kind, err := classifyWriteKind(wr, ledgerForPath(wr.Dst))
		if err != nil {
			fmt.Fprintf(w, "write: %s\n", wr.Dst)
			changes++
			continue
		}
		switch kind {
		case domain.DiffIdentical:
			skips++
		case domain.DiffCreate:
			fmt.Fprintf(w, "write: %s\n", wr.Dst)
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
		if _, err := os.Stat(cp.Dst); err == nil {
			if req.Force {
				fmt.Fprintf(w, "overwrite(copy): %s\n", cp.Dst)
				changes++
			} else {
				fmt.Fprintf(w, "skip(existing copy): %s\n", cp.Dst)
				skips++
			}
			continue
		}
		fmt.Fprintf(w, "copy: %s\n", cp.Dst)
		changes++
	}
	fmt.Fprintf(w, "plan: %d changes, %d identical\n", changes, skips)
}

func updateIndex(profile domain.Profile, home string) error {
	db, err := openIndexDB("", home)
	if err != nil {
		return err
	}
	defer db.Close()

	for _, pack := range profile.Packs {
		info, resources := index.ExtractFromPack(pack)
		if err := db.Update(info, resources); err != nil {
			return fmt.Errorf("updating index for pack %s: %w", pack.Name, err)
		}
	}
	return nil
}

// processEmbeddedRegistries loads registry YAML files declared in pack
// manifests and indexes their entries into the search index.
func processEmbeddedRegistries(profile domain.Profile, home string, stderr io.Writer) error {
	var allEntries []config.Registry
	for _, pack := range profile.Packs {
		for _, regPath := range pack.Registries {
			absPath := filepath.Join(pack.Root, regPath)
			reg, err := config.LoadRegistry(absPath)
			if err != nil {
				fmt.Fprintf(stderr, "warning: loading embedded registry %s from pack %s: %v\n", regPath, pack.Name, err)
				continue
			}
			allEntries = append(allEntries, reg)
		}
	}
	if len(allEntries) == 0 {
		return nil
	}

	cfgDir, err := config.DefaultConfigDir(home)
	if err != nil {
		return fmt.Errorf("resolving config dir: %w", err)
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
		return nil
	}

	if err := saveEmbeddedRegistry(cfgDir, merged); err != nil {
		return fmt.Errorf("saving embedded registry cache: %w", err)
	}
	if err := indexRegistryEntries(merged, cfgDir); err != nil {
		return fmt.Errorf("indexing embedded registry entries: %w", err)
	}

	fmt.Fprintf(stderr, "Merged and indexed %d pack(s) from embedded registries\n", len(merged.Packs))
	return nil
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
	fmt.Fprintf(w, "plan: %d changes (%d rules, %d workflows, %d agents, %d skills, %d settings, %d mcp, %d prunes)\n",
		total, summary.NumRules, summary.NumWorkflows, summary.NumAgents, summary.NumSkills,
		summary.NumSettings, summary.NumMCP, summary.NumPrunes)

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
