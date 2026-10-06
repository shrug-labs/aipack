package app

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/plugin"
	"github.com/shrug-labs/aipack/internal/util"
)

func TestPortableProcessLoss(t *testing.T) {
	for _, operation := range []string{"sync", "clean", "delete"} {
		for _, boundary := range []string{"before", "after"} {
			t.Run(operation+"/"+boundary, func(t *testing.T) {
				root, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = util.RemoveOwnedTree(root) })
				source := filepath.Join(root, "source")
				writeFile(t, filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"probe","version":"1.0.0"}`)
				// Mixed-package delivery owns the journal exercised by this check.
				writeFile(t, filepath.Join(source, ".mcp.json"), `{"mcpServers":{"probe":{"command":"false","cwd":"."}}}`)
				skill := filepath.Join(source, "skills/first/SKILL.md")
				writeFile(t, skill, "---\nname: first\ndescription: Owned fixture\n---\nBEFORE\n")
				cfg := filepath.Join(root, "config")
				install := PackInstallRequest{PackPath: source, ConfigDir: cfg, Name: "probe", Plugin: &domain.PluginSource{Format: plugin.CodexLegacy, Name: "probe", Marketplace: "market"}}
				if err := PackInstall(context.Background(), install, nil); err != nil {
					t.Fatal(err)
				}
				eng, profile, spec := portableProcessFixture(t, root)
				result, _, err := RunSync(context.Background(), eng, profile, SyncRequest{TargetSpec: spec, Yes: true, Quiet: true}, testRegistry(), nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				settings := filepath.Join(root, "native/opencode.json")
				beforeSettings, beforeLedger := mustRead(t, settings), mustRead(t, result.Plan.Ledger)
				data := filepath.Join(root, "native/aipack-data/probe@market/retained.txt")
				writeFile(t, data, "retained data")
				if operation == "sync" {
					writeFile(t, skill, "---\nname: first\ndescription: Owned fixture\n---\nAFTER\n")
					if err := PackInstall(context.Background(), install, nil); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPortableProcessLossHelper$", "-test.timeout=30s")
				cmd.Env = append(os.Environ(), "AIPACK_TEST_PORTABLE_PROCESS_ROOT="+root, "AIPACK_TEST_PORTABLE_PROCESS_OPERATION="+operation, "AIPACK_TEST_PORTABLE_PROCESS_BOUNDARY="+boundary)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				stdout, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				scanner := bufio.NewScanner(stdout)
				ready := false
				for scanner.Scan() {
					if scanner.Text() == "portable ledger boundary" {
						ready = true
						break
					}
				}
				if !ready {
					_ = cmd.Wait()
					t.Fatalf("child did not reach commit boundary: %s", stderr.String())
				}
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				if err := cmd.Wait(); err == nil {
					t.Fatal("child completed instead of being killed")
				}
				if _, err := os.Stat(filepath.Join(nativeOperationDir(cfg), "operation.json")); err != nil {
					t.Fatal("process loss left no recovery journal", err)
				}
				if _, _, err := RunSync(context.Background(), eng, profile, SyncRequest{TargetSpec: spec, DryRun: true}, testRegistry(), nil, nil); err == nil || !strings.Contains(err.Error(), "pending") {
					t.Fatalf("dry-run bypassed interrupted child: %v", err)
				}
				unlock, err := lockPackMutation(cfg, false)
				if err != nil {
					t.Fatal("next mutation did not recover process loss", err)
				}
				if err := unlock(); err != nil {
					t.Fatal(err)
				}
				if boundary == "before" && (!bytes.Equal(beforeSettings, mustRead(t, settings)) || !bytes.Equal(beforeLedger, mustRead(t, result.Plan.Ledger))) {
					t.Fatal("killed pre-commit child did not restore prior activation and receipt")
				}
				if boundary == "before" {
					for _, action := range result.Plan.Writes {
						if action.Delivery == nil {
							continue
						}
						files, err := eng.FS.ReadPackage(action.Dst)
						if err != nil || domain.SingleFileDigest(domain.PackageManifest(files)) != action.EffectiveDigest() {
							t.Fatal("killed child did not restore the complete previous payload", err)
						}
					}
				} else {
					committed, _, _ := eng.LoadLedger(result.Plan.Ledger)
					if operation == "sync" {
						found := false
						for path, entry := range committed.Managed {
							if entry.Delivery != nil {
								found = bytes.Contains(mustRead(t, filepath.Join(path, "skills/first/SKILL.md")), []byte("AFTER"))
							}
						}
						if !found {
							t.Fatal("committed sync was rolled back after process loss")
						}
					}
					for _, action := range result.Plan.Writes {
						if action.Delivery == nil || operation == "sync" {
							continue
						}
						if _, tracked := committed.Managed[action.Dst]; tracked {
							t.Fatal("committed removal was rolled back after process loss")
						}
					}
				}
				eng, profile, spec = portableProcessFixture(t, root)
				if err := runPortableProcessOperation(eng, profile, spec, operation); err != nil {
					t.Fatal("retry after real process loss failed", err)
				}
				if string(mustRead(t, data)) != "retained data" {
					t.Fatal("real process loss discarded data")
				}
				ledger, _, err := eng.LoadLedger(result.Plan.Ledger)
				if err != nil {
					t.Fatal(err)
				}
				packages := 0
				for path, entry := range ledger.Managed {
					if entry.Delivery != nil {
						packages++
						if operation != "sync" || !bytes.Contains(mustRead(t, filepath.Join(path, "skills/first/SKILL.md")), []byte("AFTER")) {
							t.Fatal("recovered payload does not match the committed operation")
						}
					}
				}
				wantPackages := 0
				if operation == "sync" {
					wantPackages = 1
				}
				if packages != wantPackages {
					t.Fatal("real process recovery left incorrect package ownership")
				}
			})
		}
	}
}

func portableProcessFixture(t *testing.T, root string) (*engine.Engine, domain.Profile, TargetSpec) {
	t.Helper()
	eng := engine.New(nil, nil)
	spec := TargetSpec{ConfigDir: filepath.Join(root, "config"), Home: filepath.Join(root, "home"), ProjectDir: root, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessOpenCode}, Env: map[string]string{"OPENCODE_CONFIG_DIR": filepath.Join(root, "native")}}
	profile, _, err := eng.Resolve(config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe", MCP: map[string]config.MCPServerConfig{"probe": {DisabledTools: []string{"hidden"}}}}}}, "", spec.ConfigDir, config.CollisionError, nil)
	if err != nil {
		t.Fatal(err)
	}
	return eng, profile, spec
}

func runPortableProcessOperation(eng *engine.Engine, profile domain.Profile, spec TargetSpec, operation string) error {
	switch operation {
	case "sync":
		_, _, err := RunSync(context.Background(), eng, profile, SyncRequest{TargetSpec: spec, Yes: true, Quiet: true}, testRegistry(), nil, nil)
		return err
	case "clean":
		return RunClean(context.Background(), eng, CleanRequest{TargetSpec: spec, Yes: true, Stderr: io.Discard}, testRegistry())
	case "delete":
		_, err := PackDeleteWithOptions(eng, PackDeleteRequest{ConfigDir: spec.ConfigDir, Name: "probe", Registry: testRegistry()}, nil)
		return err
	}
	return fmt.Errorf("unknown owned process operation %q", operation)
}

func TestPortableProcessLossHelper(t *testing.T) {
	root := os.Getenv("AIPACK_TEST_PORTABLE_PROCESS_ROOT")
	if root == "" {
		t.Skip("child-only portable process-loss fixture")
	}
	eng, profile, spec := portableProcessFixture(t, root)
	eng.FS = portableProcessBoundary{Path: engine.LedgerPath(spec.ConfigDir, spec.Scope, root, domain.HarnessOpenCode), After: os.Getenv("AIPACK_TEST_PORTABLE_PROCESS_BOUNDARY") == "after"}
	if err := runPortableProcessOperation(eng, profile, spec, os.Getenv("AIPACK_TEST_PORTABLE_PROCESS_OPERATION")); err != nil {
		t.Fatal(err)
	}
	t.Fatal("child missed its ledger boundary")
}

type portableProcessBoundary struct {
	engine.OSFS
	Path  string
	After bool
}

func (f portableProcessBoundary) WriteFile(path string, data []byte, mode os.FileMode) error {
	if filepath.Clean(path) != filepath.Clean(f.Path) {
		return f.OSFS.WriteFile(path, data, mode)
	}
	if f.After {
		if err := f.OSFS.WriteFile(path, data, mode); err != nil {
			return err
		}
	}
	fmt.Fprintln(os.Stdout, "portable ledger boundary")
	select {} // Parent kills this process; no defers or in-memory recovery run.
}
