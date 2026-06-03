package app

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/source"
	"github.com/shrug-labs/aipack/internal/util"
)

// CleanRequest describes a clean run.
type CleanRequest struct {
	TargetSpec
	WipeLedger bool
	WipeCache  bool
	Yes        bool
	DryRun     bool

	Stdin           io.Reader
	Stderr          io.Writer
	StdinIsTerminal func() bool
}

// RunClean resets harness capability vectors without bricking the harness.
func RunClean(ctx context.Context, eng *engine.Engine, req CleanRequest, reg *harness.Registry) error {
	home := req.Home
	if req.Scope == domain.ScopeGlobal && strings.TrimSpace(home) == "" {
		return fmt.Errorf("HOME is not set (required for global scope)")
	}

	stdin := req.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stderr := req.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	isTerminal := req.StdinIsTerminal
	if isTerminal == nil {
		isTerminal = func() bool {
			f, ok := stdin.(*os.File)
			if !ok {
				return false
			}
			st, err := f.Stat()
			if err != nil {
				return false
			}
			return (st.Mode() & os.ModeCharDevice) != 0
		}
	}

	hs := req.Harnesses
	if len(hs) == 0 {
		hs = domain.AllHarnesses()
	}
	for _, h := range hs {
		if _, ok := domain.ParseHarness(string(h)); !ok {
			return fmt.Errorf("unknown harness: %s", h)
		}
	}

	ct := cleanTarget{
		eng: eng, configDir: req.ConfigDir, scope: req.Scope,
		home: home, projectDir: req.ProjectDir, harnesses: hs,
		wipeLedger: req.WipeLedger, wipeCache: req.WipeCache, reg: reg,
		env: req.TargetSpec.Env,
	}

	if req.DryRun {
		ops := buildCleanOps(ct)
		for _, op := range ops {
			fmt.Fprintf(stderr, "  would remove: %s\n", op.path())
		}
		return nil
	}

	if !req.Yes && !isTerminal() {
		return fmt.Errorf("refusing to clean without --yes (non-interactive)")
	}

	ops := buildCleanOps(ct)

	rctx := cleanRunContext{Yes: req.Yes, Stdin: stdin, Stderr: stderr}
	for _, op := range ops {
		if err := op.run(ctx, rctx); err != nil {
			return err
		}
	}
	return nil
}

type cleanRunContext struct {
	Yes    bool
	Stdin  io.Reader
	Stderr io.Writer
}

type cleanOp interface {
	run(ctx context.Context, rctx cleanRunContext) error
	path() string
}

type removePathOp struct {
	Path string
}

func (o removePathOp) path() string { return o.Path }

func (o removePathOp) run(ctx context.Context, rctx cleanRunContext) error {
	if o.Path == "" || filepath.Clean(o.Path) == "." {
		return fmt.Errorf("invalid clean path: %q", o.Path)
	}
	if _, err := os.Stat(o.Path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !rctx.Yes {
		ok, err := cleanPromptYesNo(ctx, rctx.Stdin, rctx.Stderr, fmt.Sprintf("Delete path? %s [y/N]: ", o.Path))
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return os.RemoveAll(o.Path)
}

type editFileOp struct {
	FilePath string
	Format   harness.FileFormat
	Context  harness.EditContext
	Edit     func(root map[string]any, ctx harness.EditContext)
}

func (o editFileOp) path() string { return o.FilePath }

func (o editFileOp) run(ctx context.Context, rctx cleanRunContext) error {
	if o.FilePath == "" || filepath.Clean(o.FilePath) == "." {
		return fmt.Errorf("invalid config path: %q", o.FilePath)
	}
	b, err := os.ReadFile(o.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	out, err := harness.ApplyEdit(b, o.Format, o.Context, o.Edit)
	if err != nil {
		return err
	}
	if !rctx.Yes {
		ok, err := cleanPromptYesNo(ctx, rctx.Stdin, rctx.Stderr, fmt.Sprintf("Update config (surgical reset)? %s [y/N]: ", o.FilePath))
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return util.WriteFileAtomic(o.FilePath, out)
}

// cleanTarget bundles the resolved inputs for buildCleanOps.
type cleanTarget struct {
	eng        *engine.Engine
	configDir  string
	scope      domain.Scope
	home       string
	projectDir string
	harnesses  []domain.Harness
	wipeLedger bool
	wipeCache  bool
	reg        *harness.Registry
	env        map[string]string // optional caller-provided env overrides
}

func buildCleanOps(t cleanTarget) []cleanOp {
	configDir := config.FallbackConfigDir(t.configDir, t.home)
	home := t.home
	scope := t.scope
	projectDir := t.projectDir
	hs := t.harnesses
	var ops []cleanOp
	seenRemovePaths := map[string]struct{}{}

	for _, hid := range hs {
		h, err := t.reg.Lookup(hid)
		if err != nil {
			continue
		}
		spec := TargetSpec{Scope: scope, ProjectDir: projectDir, Home: home, Env: t.env}
		layout := h.Layout(scope, targetDirForHarness(spec, hid), home)

		ledgerPath := engine.LedgerPath(configDir, scope, projectDir, hid)
		lg, _, lgErr := t.eng.LoadLedger(ledgerPath)

		// OwnedFiles are partially owned — reset managed keys, not delete.
		// Bind the file's managed MCP server names so reset prunes only
		// aipack's servers and preserves any the user added by hand.
		ownedPaths := map[string]struct{}{}
		var mcpIndex domain.MCPServerNameIndex
		if lgErr == nil {
			mcpIndex = domain.MCPServerNamesByPath(lg.Managed)
		}
		for _, of := range layout.OwnedFiles {
			ownedPaths[filepath.Clean(of.Path)] = struct{}{}
			ops = append(ops, editFileOp{
				FilePath: of.Path,
				Format:   of.Format,
				Context: harness.EditContext{
					ManagedMCPServers:      mcpIndex.ForPath(of.Path),
					PreviousManagedOverlay: lg.PrevManagedOverlay(of.Path),
				},
				Edit: of.Reset,
			})
		}

		// Explicit fully-owned paths are safe to remove wholesale.
		for _, path := range layout.RemovePaths {
			cleanPath := filepath.Clean(path)
			if _, seen := seenRemovePaths[cleanPath]; seen {
				continue
			}
			seenRemovePaths[cleanPath] = struct{}{}
			ops = append(ops, removePathOp{Path: cleanPath})
		}

		// Mixed containers may also hold fully-owned leaf files (for example,
		// plugin/drop-in config files). Remove any ledger-tracked paths inside
		// validation roots that are not partially-owned files and not already
		// covered by an explicit RemovePath.
		if lgErr != nil {
			continue
		}
		for trackedPath := range lg.Managed {
			cleanPath := filepath.Clean(trackedPath)
			if !domain.IsUnderAny(cleanPath, layout.ValidationRoots) {
				continue
			}
			if _, owned := ownedPaths[cleanPath]; owned {
				continue
			}
			if _, seen := seenRemovePaths[cleanPath]; seen {
				continue
			}
			seenRemovePaths[cleanPath] = struct{}{}
			ops = append(ops, removePathOp{Path: cleanPath})
		}
	}

	if t.wipeLedger && home != "" {
		ledgerDir := filepath.Join(configDir, "ledger")
		if scope == domain.ScopeProject {
			ops = append(ops, removePathOp{Path: filepath.Join(ledgerDir, engine.EncodeProjectPath(projectDir))})
			ops = append(ops, removePathOp{Path: filepath.Join(projectDir, ".aipack", "ledger.json")})
		} else {
			ops = append(ops, removePathOp{Path: ledgerDir})
		}
	}

	if t.wipeCache {
		ops = append(ops, removePathOp{Path: source.GitCacheDir(configDir)})
	}

	slices.SortStableFunc(ops, func(a, b cleanOp) int {
		return cmp.Compare(a.path(), b.path())
	})

	return ops
}

func cleanPromptYesNo(ctx context.Context, r io.Reader, w io.Writer, msg string) (bool, error) {
	if _, err := fmt.Fprint(w, msg); err != nil {
		return false, err
	}
	// Read in a goroutine so context cancellation (e.g. Ctrl-C) is respected.
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		br := bufio.NewReader(r)
		line, err := br.ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case res := <-ch:
		if res.err != nil && !errors.Is(res.err, io.EOF) {
			return false, res.err
		}
		ans := strings.ToLower(strings.TrimSpace(res.line))
		return ans == "y" || ans == "yes", nil
	}
}
