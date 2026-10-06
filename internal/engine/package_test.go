package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

func TestPackageWriteLifecycle(t *testing.T) {
	for _, name := range []string{"disk", "memory"} {
		t.Run(name, func(t *testing.T) {
			var fileSystem FS = OSFS{}
			if name == "memory" {
				fileSystem = NewMemFS()
			}
			eng := New(fileSystem, &TTYInteractor{})
			root := t.TempDir()
			if name == "disk" {
				t.Cleanup(func() { _ = util.RemoveOwnedTree(root) })
			}
			payload := filepath.Join(root, "imports", "fixture", "payload")
			data := filepath.Join(root, "data", "fixture", "keep")
			if err := fileSystem.MkdirAll(filepath.Dir(data), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := fileSystem.WriteFile(data, []byte("retained"), 0o600); err != nil {
				t.Fatal(err)
			}
			files := []domain.NativePluginFile{
				{Path: "assets", Mode: os.ModeDir | 0o550},
				{Path: "assets/shared.txt", Content: []byte("one"), Mode: 0o440},
				{Path: "empty", Mode: os.ModeDir | 0o750},
				{Path: "run", Content: []byte("#!/bin/sh\n"), Mode: 0o755},
				{Path: "shared", Link: "assets/shared.txt", Mode: os.ModeSymlink | 0o777},
			}
			write := domain.WriteAction{Dst: payload, PackageFiles: files, SourcePack: "fixture"}
			plan := domain.Plan{Writes: []domain.WriteAction{write}, Ledger: filepath.Join(root, "ledger.json")}
			plan.AddDesired(payload)
			apply := func(dry bool) {
				t.Helper()
				warnings, err := eng.ApplyPlan(context.Background(), plan, ApplyRequest{DryRun: dry, Quiet: true}, []string{root})
				if err != nil || len(warnings) != 0 {
					t.Fatalf("apply: %v, %v", warnings, err)
				}
			}
			apply(true)
			if _, err := fileSystem.Stat(payload); !os.IsNotExist(err) {
				t.Fatalf("dry run wrote payload: %v", err)
			}
			apply(false)
			ledger, _, err := eng.LoadLedger(plan.Ledger)
			if err != nil || !ledger.Managed[payload].Package {
				t.Fatalf("missing package ownership: %+v %v", ledger, err)
			}
			assertFiles := func(expected []domain.NativePluginFile) {
				t.Helper()
				got, err := fileSystem.ReadPackage(payload)
				if err != nil || string(domain.PackageManifest(got)) != string(domain.PackageManifest(expected)) {
					t.Fatalf("payload layout changed: %+v %v", got, err)
				}
			}
			assertFiles(files)
			if kind, err := eng.ClassifyWriteKind(write, ledger); err != nil || kind != domain.DiffIdentical {
				t.Fatalf("repeat: %s %v", kind, err)
			}
			apply(false)
			if got, err := fileSystem.ReadFile(filepath.Join(payload, "shared")); err != nil || string(got) != "one" {
				t.Fatalf("runtime symlink: %q %v", got, err)
			}
			if name == "disk" {
				link := filepath.Join(payload, "shared")
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("assets/missing", link); err != nil {
					t.Fatal(err)
				}
				if kind, err := eng.ClassifyWriteKind(write, ledger); err != nil || kind != domain.DiffConflict {
					t.Fatal("broken internal link was treated as a missing package")
				}
				decision, err := eng.shouldDelete(context.Background(), deleteRequest{Path: payload, PrevDigest: ledger.PrevDigest(payload), Package: true})
				if err != nil || decision != DeleteSkippedNonInteractive {
					t.Fatalf("broken internal link allowed deletion: %v %v", decision, err)
				}
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("assets/shared.txt", link); err != nil {
					t.Fatal(err)
				}
				bad := append([]domain.NativePluginFile{}, files...)
				bad[len(bad)-1].Link = "run/missing"
				if err := fileSystem.WritePackage(payload, bad); err == nil {
					t.Fatal("invalid staged package replaced working generation")
				}
				assertFiles(files)
			}
			changed := append([]domain.NativePluginFile{}, files...)
			changed[1].Content = []byte("two")
			changed[3].Mode = 0o700
			write.PackageFiles = changed
			if kind, err := eng.ClassifyWriteKind(write, ledger); err != nil || kind != domain.DiffManaged {
				t.Fatalf("source refresh: %s %v", kind, err)
			}
			plan.Writes[0] = write
			apply(false)
			assertFiles(changed)
			ledger, _, _ = eng.LoadLedger(plan.Ledger)
			// Untracked additions, including Git metadata, protect the whole tree.
			if err := fileSystem.MkdirAll(filepath.Join(payload, ".git"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := fileSystem.WriteFile(filepath.Join(payload, ".git", "HEAD"), []byte("user"), 0o600); err != nil {
				t.Fatal(err)
			}
			if kind, err := eng.ClassifyWriteKind(write, ledger); err != nil || kind != domain.DiffConflict {
				t.Fatalf("local change: %s %v", kind, err)
			}
			plan.Writes, plan.Desired = nil, nil
			warnings, err := eng.ApplyPlan(context.Background(), plan, ApplyRequest{Quiet: true}, []string{root})
			if err != nil || len(warnings) == 0 {
				t.Fatalf("changed package deletion was not refused: %v %v", warnings, err)
			}
			if _, err := fileSystem.Stat(payload); err != nil {
				t.Fatal("changed payload lost", err)
			}
			if err := fileSystem.Remove(filepath.Join(payload, ".git", "HEAD")); err != nil {
				t.Fatal(err)
			}
			if err := fileSystem.Remove(filepath.Join(payload, ".git")); err != nil {
				t.Fatal(err)
			}
			apply(false)
			if _, err := fileSystem.Stat(payload); !os.IsNotExist(err) {
				t.Fatalf("owned stale package remains: %v", err)
			}
			if got, err := fileSystem.ReadFile(data); err != nil || string(got) != "retained" {
				t.Fatalf("runtime data lost: %q %v", got, err)
			}
		})
	}
}

func TestPackageRejectsAmbiguousPathsBeforeWriting(t *testing.T) {
	fs := OSFS{}
	root := filepath.Join(t.TempDir(), "payload")
	for _, files := range [][]domain.NativePluginFile{
		{{Path: "../escape", Mode: 0o600}},
		{{Path: "alias", Mode: os.ModeSymlink, Link: "target"}, {Path: "alias/child", Mode: 0o600}},
		{{Path: "file", Mode: 0o600}, {Path: "file", Mode: 0o755}},
		{{Path: "link", Mode: os.ModeSymlink, Link: "../outside"}},
		{{Path: "dir/../file", Mode: 0o600}},
	} {
		if err := fs.WritePackage(root, files); err == nil {
			t.Fatalf("accepted ambiguous layout: %+v", files)
		}
		if _, err := os.Lstat(root); !os.IsNotExist(err) {
			t.Fatalf("invalid layout wrote root: %v", err)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.ReadPackage(root); err == nil {
		t.Fatal("read followed replaced payload root")
	}
	if err := fs.RemovePackage(root); err == nil {
		t.Fatal("cleanup followed replaced payload root")
	}
	if err := fs.WritePackage(root, []domain.NativePluginFile{}); err == nil {
		t.Fatal("replacement followed payload root symlink")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("external root lost", err)
	}
}
