package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/plugin"
	"github.com/shrug-labs/aipack/internal/util"
)

func TestPackTreeDigestPreservesEncoding(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"empty", "integrity-only", "payload"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if kind != "empty" {
				writeFile(t, filepath.Join(root, integrityFileName), "ignored")
			}
			if kind == "payload" {
				writeFile(t, filepath.Join(root, "assets", "binary"), "\x00\xff\n<>&\u2028")
				writeFile(t, filepath.Join(root, "entry.md"), "Unicode: 雪")
				if err := os.Chmod(filepath.Join(root, "entry.md"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			files, err := plugin.ReadFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			files = slices.DeleteFunc(files, func(file plugin.File) bool { return file.Path == integrityFileName })
			body, err := json.Marshal(files)
			if err != nil {
				t.Fatal(err)
			}
			got, err := packTreeDigest(root)
			if err != nil || got != util.ContentDigest(body) {
				t.Fatalf("persisted digest changed: got %s want %s error=%v", got, util.ContentDigest(body), err)
			}
		})
	}
}

func TestSyncRejectsProfileResolvedBeforePackRecovery(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	root := filepath.Join(PacksDir(dir), "probe")
	manifest := `{"schema_version":2,"name":"probe","root":".","rules":["rule"]}`
	writeFile(t, filepath.Join(root, "pack.json"), manifest)
	writeFile(t, filepath.Join(root, "rules/rule.md"), "committed body")
	stage, err := makePackTempDir(dir, "extract-*")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(stage, "pack.json"), manifest)
	writeFile(t, filepath.Join(stage, "rules/rule.md"), "uncommitted body")
	if _, err := beginPackReplacement(dir, "probe", stage); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(nil, nil)
	cfg := config.ProfileConfig{Packs: []config.PackEntry{{Name: "probe"}}}
	profile, _, err := eng.Resolve(cfg, "", dir, config.CollisionError, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := SyncRequest{TargetSpec: TargetSpec{ConfigDir: dir, Home: home, Scope: domain.ScopeGlobal, Harnesses: []domain.Harness{domain.HarnessClaudeCode}, Env: map[string]string{"CLAUDE_CONFIG_DIR": ""}}, Yes: true}
	if _, _, err := RunSync(context.Background(), eng, profile, req, testRegistry(), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "resolve the profile again") {
		t.Fatalf("accepted stale resolved profile: %v", err)
	}
	if string(mustRead(t, filepath.Join(root, "rules/rule.md"))) != "committed body" {
		t.Fatal("source recovery failed")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude/rules/rule.md")); !os.IsNotExist(err) {
		t.Fatalf("stale content was delivered: %v", err)
	}
	ctx, unlock, err := PrepareSync(context.Background(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	profile, _, err = eng.Resolve(cfg, "", dir, config.CollisionError, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunSyncEach(ctx, eng, profile, req, testRegistry(), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, filepath.Join(home, ".claude/rules/rule.md"))) != "committed body" {
		t.Fatal("fresh resolution did not deliver the recovered source")
	}
}

func TestMaterializedImportReplacementPreservesLocalChanges(t *testing.T) {
	t.Parallel()
	configDir, source := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(source, "pack.json"), `{"schema_version":2,"name":"probe","root":".","native_plugin":{"format":"codex-legacy","harness":"codex","name":"probe","marketplace":"market","manifest":".codex-plugin/plugin.json","converter_version":20,"components":{}}}`)
	writeFile(t, filepath.Join(source, "upstream/.codex-plugin/plugin.json"), `{"name":"probe"}`)
	writeFile(t, filepath.Join(source, "upstream/script.py"), "original")
	req := PackInstallRequest{ConfigDir: configDir, PackPath: source}
	if err := PackInstall(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(PacksDir(configDir), "probe/upstream/script.py")
	writeFile(t, installed, "local edit")
	for _, link := range []bool{false, true} {
		req.Link = link
		if err := PackInstall(context.Background(), req, nil); err == nil || !strings.Contains(err.Error(), "local changes") {
			t.Fatalf("replacement accepted local changes (link=%t): %v", link, err)
		}
		if string(mustRead(t, installed)) != "local edit" {
			t.Fatal("replacement lost local changes")
		}
	}
}

func TestImportedPackLinkReplacementPreservesLocalChanges(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"modified", "git", "missing-baseline", "unchanged"} {
		t.Run(change, func(t *testing.T) {
			configDir, replacement := t.TempDir(), t.TempDir()
			dest := filepath.Join(PacksDir(configDir), "probe")
			writeFile(t, filepath.Join(dest, "pack.json"), `{"schema_version":2,"name":"probe","root":"."}`)
			file := filepath.Join(dest, "upstream", "script.py")
			writeFile(t, file, "original")
			digest, err := packTreeDigest(dest)
			if err != nil {
				t.Fatal(err)
			}
			meta := config.InstalledPackMeta{Plugin: &domain.PluginSource{Name: "probe", Marketplace: "market"}, MaterializedDigest: digest}
			if change == "modified" {
				writeFile(t, file, "local edit")
			} else if change == "git" {
				writeFile(t, filepath.Join(dest, ".git", "HEAD"), "local history")
			} else if change == "missing-baseline" {
				meta.MaterializedDigest = ""
			}
			lockPath := config.LockfilePath(configDir)
			if err := config.SaveLockfile(lockPath, config.Lockfile{Packs: map[string]config.InstalledPackMeta{"probe": meta}}); err != nil {
				t.Fatal(err)
			}
			beforeFile, beforeLock := mustRead(t, file), mustRead(t, lockPath)
			writeFile(t, filepath.Join(replacement, "pack.json"), `{"schema_version":2,"name":"probe","root":"."}`)
			err = PackInstall(context.Background(), PackInstallRequest{ConfigDir: configDir, PackPath: replacement, Link: true}, nil)
			if change == "unchanged" {
				if err != nil {
					t.Fatal(err)
				}
				if target, err := os.Readlink(dest); err != nil || target != replacement {
					t.Fatalf("unchanged import was not replaced by the requested link: %q %v", target, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "imported plugin") {
				t.Fatalf("link replacement accepted unverifiable imported content: %v", err)
			}
			if !bytes.Equal(beforeFile, mustRead(t, file)) || !bytes.Equal(beforeLock, mustRead(t, lockPath)) {
				t.Fatal("refused link replacement changed imported content or metadata")
			}
		})
	}
}

func TestImportedPackReplacementPreservesLocalChanges(t *testing.T) {
	for _, change := range []string{"modified", "added", "git", "nested-git", "git-file", "removed", "mode", "manifest", "missing-baseline", "ordinary-pack", "unchanged"} {
		t.Run(change, func(t *testing.T) {
			configDir := t.TempDir()
			dest := filepath.Join(PacksDir(configDir), "probe")
			writeFile(t, filepath.Join(dest, "pack.json"), `{"schema_version":1,"name":"probe","root":"."}`)
			file := filepath.Join(dest, "upstream", "script.py")
			writeFile(t, file, "original")
			digest, err := packTreeDigest(dest)
			if err != nil {
				t.Fatal(err)
			}
			meta := config.InstalledPackMeta{Plugin: &domain.PluginSource{Name: "probe", Marketplace: "market"}, MaterializedDigest: digest}
			switch change {
			case "modified", "ordinary-pack":
				writeFile(t, file, "local edit")
			case "added":
				writeFile(t, filepath.Join(dest, "upstream", "local.txt"), "local addition")
			case "git":
				writeFile(t, filepath.Join(dest, ".git", "HEAD"), "local history")
			case "nested-git":
				writeFile(t, filepath.Join(dest, "upstream", ".git", "HEAD"), "local history")
			case "git-file":
				writeFile(t, filepath.Join(dest, ".git"), "gitdir: ../local-history")
			case "removed":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(file, 0o755); err != nil {
					t.Fatal(err)
				}
			case "manifest":
				writeFile(t, filepath.Join(dest, "pack.json"), `{"schema_version":1,"name":"probe","root":".","description":"edited"}`)
			case "missing-baseline":
				meta.MaterializedDigest = ""
			}
			if change == "ordinary-pack" {
				meta.Plugin = nil
			}
			if change != "ordinary-pack" && change != "unchanged" {
				for _, dryRun := range []bool{true, false} {
					result := packUpdateOne(context.Background(), "probe", packUpdateContext{packsDir: PacksDir(configDir), packs: map[string]config.InstalledPackMeta{"probe": meta}, dryRun: dryRun})
					if result.Status != StatusError || !strings.Contains(result.Message, "imported plugin") || result.DryRun != dryRun {
						t.Fatalf("update failed to refuse local changes (dry-run=%t): %+v", dryRun, result)
					}
				}
			}
			if err := config.SaveLockfile(config.LockfilePath(configDir), config.Lockfile{Packs: map[string]config.InstalledPackMeta{"probe": meta}}); err != nil {
				t.Fatal(err)
			}
			stage, err := makePackTempDir(configDir, "extract-*")
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(stage, "next.txt"), "replacement")
			before, err := packTreeDigest(dest)
			if err != nil {
				t.Fatal(err)
			}
			_, err = beginPackReplacement(configDir, "probe", stage)
			if change == "ordinary-pack" || change == "unchanged" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "imported plugin") {
				t.Fatalf("replacement error: %v", err)
			}
			after, err := packTreeDigest(dest)
			if err != nil || before != after {
				t.Fatalf("local content changed: %v", err)
			}
			for _, path := range []string{packOperationPath(configDir, "probe"), packBackupPath(configDir, "probe")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("refusal left transaction state: %s %v", path, err)
				}
			}
			if string(mustRead(t, filepath.Join(stage, "next.txt"))) != "replacement" {
				t.Fatal("refusal changed staging")
			}
		})
	}
}

func TestPackReplacementRecovery(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-metadata", true: "after-metadata"}[committed], func(t *testing.T) {
			configDir := t.TempDir()
			dest := filepath.Join(PacksDir(configDir), "probe")
			writeFile(t, filepath.Join(dest, "pack.json"), `{"schema_version":1,"name":"probe","version":"1.0.0","root":"."}`)
			writeFile(t, filepath.Join(dest, "old.txt"), "prior generation")
			stage, err := makePackTempDir(configDir, "extract-*")
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(stage, "pack.json"), `{"schema_version":1,"name":"probe","version":"2.0.0","root":"."}`)
			writeFile(t, filepath.Join(stage, "new.txt"), "next generation")
			op, err := beginPackReplacement(configDir, "probe", stage)
			if err != nil {
				t.Fatal(err)
			}
			if committed {
				lf := config.Lockfile{Packs: map[string]config.InstalledPackMeta{"probe": {MaterializedDigest: op.Digest}}}
				if err := config.SaveLockfile(config.LockfilePath(configDir), lf); err != nil {
					t.Fatal(err)
				}
			}
			if err := recoverPackReplacements(configDir); err != nil {
				t.Fatal(err)
			}
			want, gone := "old.txt", "new.txt"
			if committed {
				want, gone = gone, want
			}
			if _, err := os.Stat(filepath.Join(dest, want)); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dest, gone)); !os.IsNotExist(err) {
				t.Fatal("wrong recovery generation")
			}
			if _, err := os.Stat(packBackupPath(configDir, "probe")); !os.IsNotExist(err) {
				t.Fatal("backup was not reconciled")
			}
			if _, err := os.Stat(packOperationPath(configDir, "probe")); !os.IsNotExist(err) {
				t.Fatal("pending operation remains")
			}
		})
	}
	// Concurrent user changes must be retained with the prior backup available.
	configDir := t.TempDir()
	dest := filepath.Join(PacksDir(configDir), "probe")
	writeFile(t, filepath.Join(dest, "old.txt"), "prior")
	stage, err := makePackTempDir(configDir, "extract-*")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(stage, "new.txt"), "next")
	if _, err := beginPackReplacement(configDir, "probe", stage); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dest, ".git", "HEAD"), "local history")
	if err := recoverPackReplacements(configDir); err == nil {
		t.Fatal("recovery discarded concurrent Git metadata")
	}
	if string(mustRead(t, filepath.Join(dest, ".git", "HEAD"))) != "local history" || string(mustRead(t, filepath.Join(packBackupPath(configDir, "probe"), "old.txt"))) != "prior" {
		t.Fatal("recovery lost Git metadata or the prior generation")
	}
	writeFile(t, filepath.Join(dest, "user.txt"), "user change")
	if err := recoverPackReplacements(configDir); err == nil {
		t.Fatal("recovery discarded concurrent user data")
	}
	if string(mustRead(t, filepath.Join(dest, "user.txt"))) != "user change" || string(mustRead(t, filepath.Join(packBackupPath(configDir, "probe"), "old.txt"))) != "prior" {
		t.Fatal("recovery lost data")
	}
	// An invalid source tree cannot be treated as an unchanged generation.
	if err := os.Symlink("../outside", filepath.Join(dest, "invalid-link")); err != nil {
		t.Fatal(err)
	}
	if err := recoverPackReplacements(configDir); err == nil {
		t.Fatal("recovery accepted an unverifiable tree")
	}
	if string(mustRead(t, filepath.Join(dest, "user.txt"))) != "user change" || string(mustRead(t, filepath.Join(packBackupPath(configDir, "probe"), "old.txt"))) != "prior" {
		t.Fatal("unverifiable tree recovery lost data")
	}
}
