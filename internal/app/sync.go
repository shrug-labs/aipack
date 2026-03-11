package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/index"
	"github.com/shrug-labs/aipack/internal/util"

	"gopkg.in/yaml.v3"
)

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

// TargetSpec holds the common targeting fields shared across request types.
type TargetSpec struct {
	Scope      domain.Scope
	ProjectDir string
	Harnesses  []domain.Harness
	Home       string // $HOME — threaded explicitly for testability
}

func (t TargetSpec) toPlanRequest() engine.PlanRequest {
	return engine.PlanRequest{
		Scope:      t.Scope,
		Harnesses:  t.Harnesses,
		ProjectDir: t.ProjectDir,
		Home:       t.Home,
	}
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
}

func (r SyncRequest) toPlanRequest() engine.PlanRequest {
	pr := r.TargetSpec.toPlanRequest()
	pr.SkipSettings = r.SkipSettings
	return pr
}

// SyncResult holds the result of a sync operation.
type SyncResult struct {
	Plan domain.Plan
}

// RunSync plans and applies a sync. It is the primary v2 entry point for the sync command.
func RunSync(profile domain.Profile, req SyncRequest, reg *harness.Registry, stdout, stderr io.Writer) (SyncResult, error) {
	planners, err := reg.AsPlanners(req.Harnesses)
	if err != nil {
		return SyncResult{}, err
	}

	planReq := req.toPlanRequest()

	if req.DryRun && req.Verbose {
		summary, err := PlanWithDiffs(profile, req, reg)
		if err != nil {
			return SyncResult{}, err
		}
		printDryRunVerbose(summary, stdout)
		return SyncResult{}, nil
	}

	plan, err := engine.PlanSync(profile, planReq, planners)
	if err != nil {
		return SyncResult{}, err
	}

	if req.DryRun {
		printDryRun(plan, req.Force, stdout)
		return SyncResult{Plan: plan}, nil
	}

	// Compute managed roots for prune.
	managedRoots := computeManagedRoots(reg, req)

	applyReq := engine.ApplyRequest{
		Force:        req.Force,
		Prune:        req.Force,
		Yes:          req.Yes,
		DryRun:       req.DryRun,
		SkipSettings: req.SkipSettings,
		Quiet:        req.Quiet,
		Req:          planReq,
	}

	if err := engine.ApplyPlan(plan, applyReq, managedRoots); err != nil {
		return SyncResult{}, err
	}

	// Update the search index (best-effort — don't fail sync on index errors).
	if idxErr := updateIndex(profile, req.Home); idxErr != nil {
		fmt.Fprintf(stderr, "warning: index update failed: %v\n", idxErr)
	}

	// Process embedded registries from packs (best-effort).
	if regErr := processEmbeddedRegistries(profile, req.Home, stderr); regErr != nil {
		fmt.Fprintf(stderr, "warning: embedded registry processing failed: %v\n", regErr)
	}

	return SyncResult{Plan: plan}, nil
}

func computeManagedRoots(reg *harness.Registry, req SyncRequest) []string {
	baseDir := req.ProjectDir
	if req.Scope == domain.ScopeGlobal {
		baseDir = req.Home
	}
	return harness.ManagedRoots(reg, req.Scope, baseDir, req.Home, req.Harnesses)
}

func printDryRun(plan domain.Plan, force bool, w io.Writer) {
	// Load ledger for accurate classification (identical vs managed vs conflict).
	var lg domain.Ledger
	if plan.Ledger != "" {
		if l, _, err := engine.LoadLedger(plan.Ledger); err == nil {
			lg = l
		}
	}

	var changes, skips int
	for _, wr := range plan.Writes {
		kind, err := engine.ClassifyFileKind(wr.Dst, wr.Content, wr.SourcePack, lg)
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
			if force {
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
			if force {
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
// manifests and merges their entries into the local registry + search index.
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

	// Load or create local registry and merge.
	cfgDir, err := config.DefaultConfigDir(home)
	if err != nil {
		return fmt.Errorf("resolving config dir: %w", err)
	}
	localPath := config.ResolveRegistryPath("", "", cfgDir)
	local, err := config.LoadRegistry(localPath)
	if err != nil {
		local = config.Registry{
			SchemaVersion: config.RegistrySchemaVersion,
			Packs:         make(map[string]config.RegistryEntry),
		}
	}

	added := 0
	newEntries := config.Registry{Packs: make(map[string]config.RegistryEntry)}
	for _, reg := range allEntries {
		for name, entry := range reg.Packs {
			if _, exists := local.Packs[name]; !exists {
				local.Packs[name] = entry
				newEntries.Packs[name] = entry
				added++
			}
		}
	}
	if added == 0 {
		return nil
	}

	out, err := yaml.Marshal(&local)
	if err != nil {
		return fmt.Errorf("marshalling registry: %w", err)
	}
	if err := util.WriteFileAtomicWithPerms(localPath, out, 0o700, 0o600); err != nil {
		return fmt.Errorf("writing registry: %w", err)
	}

	// Index only the newly-added entries into the search index.
	if err := indexRegistryEntries(newEntries, cfgDir); err != nil {
		return fmt.Errorf("indexing embedded registry entries: %w", err)
	}

	fmt.Fprintf(stderr, "Merged %d new pack(s) from embedded registries\n", added)
	return nil
}

func printDryRunVerbose(summary PlanSummary, w io.Writer) {
	total := summary.TotalChanges()
	if total == 0 {
		fmt.Fprintln(w, "plan: no changes")
		return
	}
	fmt.Fprintf(w, "plan: %d changes (%d writes, %d copies, %d settings, %d plugins, %d prunes)\n",
		total, summary.NumWrites, summary.NumCopies, summary.NumSettings, summary.NumPlugins, summary.NumPrunes)

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
		if op.Diff != "" {
			fmt.Fprintln(w, op.Diff)
		}
	}
}
