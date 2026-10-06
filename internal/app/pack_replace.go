package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/plugin"
	"github.com/shrug-labs/aipack/internal/util"
)

// A pending replacement is pack-local, not a general transaction mechanism.
// Lockfile persistence selects commit versus rollback after an interruption.
type packReplacement struct {
	Name        string `json:"name"`
	Stage       string `json:"stage"`
	Digest      string `json:"digest"`
	HadPrevious bool   `json:"had_previous"`
}

type packMutationKey struct{}
type recoveredPacksKey struct{}

// PrepareSync holds the mutation lock while the caller loads and resolves its
// profile. RunSync and RunSyncEach reuse this lock for the apply phase.
func PrepareSync(ctx context.Context, configDir string, dryRun bool) (context.Context, func() error, error) {
	ctx, unlock, err := lockPackMutationContext(ctx, configDir, dryRun)
	if err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, recoveredPacksKey{}, false), unlock, nil
}

func lockPackMutation(configDir string, dryRun bool) (func() error, error) {
	_, unlock, err := lockPackMutationContext(context.Background(), configDir, dryRun)
	return unlock, err
}

func lockPackMutationContext(ctx context.Context, configDir string, dryRun bool) (context.Context, func() error, error) {
	if root, _ := ctx.Value(packMutationKey{}).(string); root == canonicalPath(configDir) {
		return ctx, func() error { return nil }, nil
	}
	if dryRun {
		pending, err := packReplacementsPending(configDir)
		if err != nil {
			return ctx, nil, err
		}
		if pending {
			return ctx, nil, fmt.Errorf("pack replacement is pending; run sync without dry-run to recover it")
		}
		if util.PathExists(filepath.Join(nativeOperationDir(configDir), "operation.json")) {
			return ctx, nil, fmt.Errorf("native delivery is pending; run sync without dry-run to recover it")
		}
		return ctx, func() error { return nil }, nil
	}
	ctx, unlock, err := util.LockConfigContext(ctx, configDir)
	if err != nil {
		return ctx, nil, err
	}
	recovered, err := packReplacementsPending(configDir)
	if err != nil {
		return ctx, nil, errors.Join(err, unlock())
	}
	if err := recoverPackReplacements(configDir); err != nil {
		return ctx, nil, errors.Join(err, unlock())
	}
	if err := recoverNativeOperationContext(ctx, configDir); err != nil {
		return ctx, nil, errors.Join(err, unlock())
	}
	ctx = context.WithValue(ctx, packMutationKey{}, canonicalPath(configDir))
	ctx = context.WithValue(ctx, recoveredPacksKey{}, recovered)
	return ctx, unlock, nil
}

func packReplacementsPending(configDir string) (bool, error) {
	entries, err := os.ReadDir(filepath.Dir(packOperationPath(configDir, "")))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return slices.ContainsFunc(entries, func(entry os.DirEntry) bool {
		return !entry.IsDir() && filepath.Ext(entry.Name()) == ".json"
	}), nil
}

func packOperationPath(configDir, name string) string {
	return filepath.Join(configDir, ".tmp", "pack-operations", name+".json")
}
func packBackupPath(configDir, name string) string {
	return filepath.Join(PacksDir(configDir), "."+name+".pending-old")
}

func packTreeDigest(root string) (string, error) {
	files, err := plugin.ReadPayloadFiles(root)
	if err != nil {
		return "", err
	}
	files = slices.DeleteFunc(files, func(file plugin.File) bool { return file.Path == integrityFileName })
	if files == nil {
		return util.ContentDigest([]byte("null")), nil
	}
	hash := sha256.New()
	hash.Write([]byte("["))
	for i, file := range files {
		if i > 0 {
			hash.Write([]byte(","))
		}
		body, err := json.Marshal(file)
		if err != nil {
			return "", err
		}
		hash.Write(body)
	}
	hash.Write([]byte("]"))
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyImportedPackUnmodified(packDir string, meta config.InstalledPackMeta) error {
	if meta.Plugin == nil && meta.ConverterVersion == 0 {
		return nil
	}
	if meta.MaterializedDigest == "" {
		return fmt.Errorf("imported plugin has no recorded content baseline; preserve its files and profile selections, then delete the pack before reinstalling")
	}
	digest, err := packTreeDigest(packDir)
	if err != nil {
		return fmt.Errorf("cannot verify imported plugin content: %w", err)
	}
	if digest != meta.MaterializedDigest {
		return fmt.Errorf("imported plugin contains local changes; preserve them and restore the installed source before updating or reinstalling")
	}
	return nil
}

func beginPackReplacement(configDir, name, stage string) (*packReplacement, error) {
	if _, err := resolvePackName(name, ""); err != nil {
		return nil, err
	}
	if canonicalPath(filepath.Dir(stage)) != canonicalPath(packStagingDir(configDir)) {
		return nil, fmt.Errorf("replacement stage escapes config staging directory")
	}
	path := packOperationPath(configDir, name)
	if util.PathExists(path) || util.PathExists(packBackupPath(configDir, name)) {
		return nil, fmt.Errorf("pack %q has a pending replacement; reconcile it before another mutation", name)
	}
	digest, err := packTreeDigest(stage)
	if err != nil {
		return nil, err
	}
	dest := filepath.Join(PacksDir(configDir), name)
	_, err = os.Lstat(dest)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	op := &packReplacement{Name: name, Stage: stage, Digest: digest, HadPrevious: err == nil}
	if op.HadPrevious {
		lf, err := config.LoadLockfile(config.LockfilePath(configDir))
		if err != nil {
			return nil, err
		}
		if err := verifyImportedPackUnmodified(dest, lf.Packs[name]); err != nil {
			return nil, fmt.Errorf("pack %q: %w", name, err)
		}
	}
	body, err := json.Marshal(op)
	if err != nil {
		return nil, err
	}
	if err := util.WriteFileAtomicWithPerms(path, body, 0o700, 0o600); err != nil {
		return nil, err
	}
	backup := ""
	if op.HadPrevious {
		backup = packBackupPath(configDir, name)
	}
	if err := util.ReplaceDirWithBackup(dest, stage, backup); err != nil {
		return nil, errors.Join(err, recoverPackReplacement(configDir, *op))
	}
	return op, nil
}

func recoverPackReplacement(configDir string, op packReplacement) error {
	if _, err := resolvePackName(op.Name, ""); err != nil {
		return err
	}
	if canonicalPath(filepath.Dir(op.Stage)) != canonicalPath(packStagingDir(configDir)) {
		return fmt.Errorf("pending replacement stage escapes config directory")
	}
	if raw, err := hex.DecodeString(op.Digest); err != nil || len(raw) != sha256.Size {
		return fmt.Errorf("invalid pending replacement digest")
	}
	lf, err := config.LoadLockfile(config.LockfilePath(configDir))
	if err != nil {
		return err
	}
	dest, backup := filepath.Join(PacksDir(configDir), op.Name), packBackupPath(configDir, op.Name)
	digest, digestErr := packTreeDigest(dest)
	if digestErr != nil && !errors.Is(digestErr, fs.ErrNotExist) {
		return fmt.Errorf("cannot verify pack %q during recovery; installed and backup trees retained: %w", op.Name, digestErr)
	}
	if lf.Packs[op.Name].MaterializedDigest == op.Digest && digestErr == nil && digest == op.Digest {
		if err := util.RemoveOwnedTree(backup); err != nil {
			return err
		}
	} else if util.PathExists(backup) {
		if digestErr == nil && digest != op.Digest {
			return fmt.Errorf("pack %q changed during interrupted replacement; prior tree retained at %s", op.Name, backup)
		}
		if err := util.RemoveOwnedTree(dest); err != nil {
			return err
		}
		if err := os.Rename(backup, dest); err != nil {
			return err
		}
	} else if !op.HadPrevious && digestErr == nil && digest == op.Digest {
		if err := util.RemoveOwnedTree(dest); err != nil {
			return err
		}
	} else if op.HadPrevious && errors.Is(digestErr, fs.ErrNotExist) {
		return fmt.Errorf("pack %q is missing both installed and backup trees", op.Name)
	}
	if err := util.RemoveOwnedTree(op.Stage); err != nil {
		return err
	}
	return os.Remove(packOperationPath(configDir, op.Name))
}

func recoverPackReplacements(configDir string) error {
	entries, err := os.ReadDir(filepath.Dir(packOperationPath(configDir, "")))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(filepath.Dir(packOperationPath(configDir, "")), entry.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var op packReplacement
		if err := json.Unmarshal(body, &op); err != nil {
			return fmt.Errorf("reading pending replacement %s: %w", path, err)
		}
		if entry.Name() != op.Name+".json" {
			return fmt.Errorf("invalid pending replacement identity")
		}
		if err := recoverPackReplacement(configDir, op); err != nil {
			return err
		}
	}
	return nil
}
