package app

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/util"
)

// Portable activation shares the existing operation journal and ledger commit
// marker. Data directories are deliberately absent from recovery ownership.
type portableOperation struct {
	Payloads map[string]portablePayloadSnapshot
	Settings []portableSettingsSnapshot
}

type portablePayloadSnapshot struct {
	Delivery      domain.PackageDelivery
	Before        nativeViewSnapshot
	DesiredDigest string
}

type portableSettingsSnapshot struct {
	Path          string
	Before, After []byte
	Original      []byte // formatting only; restore values through owned overlays
	Existed       bool
	Mode          os.FileMode
}

func validatePortableDelivery(path string, d domain.PackageDelivery) error {
	parts := strings.Split(d.Binding, "@")
	_, err := hex.DecodeString(d.Generation)
	if len(parts) != 2 || !domain.ValidNativeName(parts[0]) || !domain.ValidNativeName(parts[1]) || len(d.Generation) != 64 || err != nil || !filepath.IsAbs(d.SettingsPath) || filepath.Base(d.SettingsPath) != "opencode.json" {
		return fmt.Errorf("invalid portable delivery identity")
	}
	base := filepath.Dir(d.SettingsPath)
	if filepath.Clean(path) != filepath.Join(base, "aipack-imports", d.Binding, d.Generation, "payload") || filepath.Clean(d.DataDir) != filepath.Join(base, "aipack-data", d.Binding) {
		return fmt.Errorf("portable recovery paths escape their package")
	}
	if (d.Home != "" && (!filepath.IsAbs(d.Home) || !filepath.IsAbs(d.ConfigHome))) || (d.ConfigHome != "" && !filepath.IsAbs(d.ConfigHome)) || (d.ProjectDir != "" && (!filepath.IsAbs(d.Home) || !filepath.IsAbs(d.ProjectDir) || base != filepath.Join(d.ProjectDir, ".opencode"))) {
		return fmt.Errorf("invalid portable scope ownership")
	}
	return nil
}

func planPortableOperation(plan domain.Plan, prior domain.Ledger) (*portableOperation, error) {
	p := &portableOperation{Payloads: map[string]portablePayloadSnapshot{}}
	paths := map[string]bool{}
	for path, entry := range prior.Managed {
		if entry.Package && entry.Delivery != nil {
			p.Payloads[path] = portablePayloadSnapshot{Delivery: *entry.Delivery}
			paths[entry.Delivery.SettingsPath] = true
		}
	}
	for _, action := range plan.Writes {
		if action.Delivery != nil {
			p.Payloads[action.Dst] = portablePayloadSnapshot{Delivery: *action.Delivery, DesiredDigest: action.EffectiveDigest()}
			paths[action.Delivery.SettingsPath] = true
		}
	}
	if len(p.Payloads) == 0 {
		return nil, nil
	}
	for path, snapshot := range p.Payloads {
		if err := validatePortableDelivery(path, snapshot.Delivery); err != nil {
			return nil, err
		}
		files, err := (engine.OSFS{}).ReadPackage(path)
		if err != nil {
			if _, rootErr := os.Lstat(path); os.IsNotExist(rootErr) {
				continue
			}
			return nil, err
		}
		digest := domain.SingleFileDigest(domain.PackageManifest(files))
		if entry, owned := prior.Managed[path]; !owned || entry.Digest != digest {
			return nil, fmt.Errorf("portable payload %s has local changes; preserve or revert them before syncing", path)
		}
		snapshot.Before = nativeViewSnapshot{Exists: true, Digest: digest}
		p.Payloads[path] = snapshot
	}
	after := map[string][]byte{}
	for _, action := range append(append([]domain.SettingsAction{}, plan.Settings...), plan.MCP...) {
		if paths[action.Dst] {
			after[action.Dst] = action.Desired
		}
	}
	for path := range paths {
		disk, old, next := map[string]any{}, map[string]any{}, map[string]any{}
		body, err := os.ReadFile(path)
		existed, mode := err == nil, os.FileMode(0o600)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if existed {
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				return nil, fmt.Errorf("portable settings must be a regular file: %s", path)
			}
			mode = info.Mode().Perm()
			if err := util.UnmarshalJSON(body, &disk); err != nil {
				return nil, err
			}
		}
		for _, item := range []struct {
			raw []byte
			dst *map[string]any
		}{{prior.PrevManagedOverlay(path), &old}, {after[path], &next}} {
			if len(item.raw) > 0 {
				if err := util.UnmarshalJSON(item.raw, item.dst); err != nil {
					return nil, err
				}
			}
		}
		before, err := util.MarshalPrettyJSON(portableOwnedBefore(disk, old, next))
		if err != nil {
			return nil, err
		}
		newOverlay, err := util.MarshalPrettyJSON(next)
		if err != nil {
			return nil, err
		}
		p.Settings = append(p.Settings, portableSettingsSnapshot{Path: path, Before: before, After: newOverlay, Original: body, Existed: existed, Mode: mode})
	}
	return p, nil
}

// Only keys touched by either managed overlay participate in rollback. Set
// arrays retain owned members; positional MCP arrays retain their exact bytes.
func portableOwnedBefore(disk, old, next map[string]any) map[string]any {
	out := map[string]any{}
	for _, managed := range []map[string]any{old, next} {
		for key := range managed {
			if _, processed := out[key]; processed {
				continue
			}
			value, exists := disk[key]
			if !exists {
				continue
			}
			if object, ok := value.(map[string]any); ok {
				before, _ := old[key].(map[string]any)
				after, _ := next[key].(map[string]any)
				out[key] = portableOwnedBefore(object, before, after)
			} else if items, ok := value.([]any); ok && key != "command" && key != "args" {
				before, _ := old[key].([]any)
				after, _ := next[key].([]any)
				ownedItems := append(append([]any{}, before...), after...)
				kept := []any{}
				for _, item := range items {
					for _, owned := range ownedItems {
						if reflect.DeepEqual(item, owned) {
							kept = append(kept, item)
							break
						}
					}
				}
				out[key] = kept
			} else {
				out[key] = value
			}
		}
	}
	return out
}

func portableBackupPath(configDir, path string) string {
	return filepath.Join(nativeOperationDir(configDir), "portable", util.ContentDigest([]byte(path)))
}

func snapshotPortableOperation(configDir string, p *portableOperation) error {
	if p == nil {
		return nil
	}
	fs := engine.OSFS{}
	for path, snapshot := range p.Payloads {
		if !snapshot.Before.Exists {
			continue
		}
		files, err := fs.ReadPackage(path)
		if err != nil {
			return err
		}
		if domain.SingleFileDigest(domain.PackageManifest(files)) != snapshot.Before.Digest {
			return fmt.Errorf("portable payload changed while preparing recovery: %s", path)
		}
		if err := fs.WritePackage(portableBackupPath(configDir, path), files); err != nil {
			return err
		}
	}
	return nil
}

func recoverPortableOperation(configDir string, op nativeOperation, ledger domain.Ledger) error {
	p, fs := op.Portable, engine.OSFS{}
	paths := map[string]bool{}
	for path, snapshot := range p.Payloads {
		if err := validatePortableDelivery(path, snapshot.Delivery); err != nil {
			return err
		}
		paths[snapshot.Delivery.SettingsPath] = true
		if snapshot.Before.Exists {
			files, err := fs.ReadPackage(portableBackupPath(configDir, path))
			if err != nil || domain.SingleFileDigest(domain.PackageManifest(files)) != snapshot.Before.Digest {
				return fmt.Errorf("portable recovery snapshot changed; payloads retained: %s", path)
			}
		}
	}
	if len(p.Settings) != len(paths) || len(paths) == 0 {
		return fmt.Errorf("portable recovery has incomplete activation ownership")
	}
	for _, snapshot := range p.Settings {
		if !paths[snapshot.Path] {
			return fmt.Errorf("portable recovery contains an unowned settings path")
		}
		delete(paths, snapshot.Path)
	}
	if err := validateOperationSkills(configDir, op); err != nil {
		return err
	}
	if ledger.NativeOperation == op.ID {
		return util.RemoveOwnedTree(nativeOperationDir(configDir))
	}
	// Validate all current trees before restoring any activation. An unsafe
	// replacement must not leave a partially rolled-back configuration.
	for path := range p.Payloads {
		if _, err := fs.ReadPackage(path); err != nil {
			if _, rootErr := os.Lstat(path); !os.IsNotExist(rootErr) {
				return fmt.Errorf("cannot recover portable payload %s: %w", path, err)
			}
		}
	}
	eng := engine.New(nil, nil)
	var diffs []engine.FileDiff
	for _, snapshot := range p.Settings {
		current := map[string]any{}
		if body, err := os.ReadFile(snapshot.Path); err == nil {
			if err := util.UnmarshalJSON(body, &current); err != nil {
				return fmt.Errorf("portable settings changed during recovery; preserve or restore %s: %w", snapshot.Path, err)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		baseline := domain.NewLedger()
		baseline.Managed[snapshot.Path] = domain.Entry{ManagedOverlay: snapshot.After}
		fd, err := eng.ComputeSettingsDiffs([]domain.SettingsAction{{Dst: snapshot.Path, Desired: snapshot.Before, Harness: domain.HarnessOpenCode, MergeMode: true}}, baseline)
		if err != nil {
			return err
		}
		for _, decision := range fd[0].MergeOps {
			if decision.Action == engine.MergeConflict {
				return fmt.Errorf("portable settings changed during recovery at %s; preserve or restore %s", decision.Key, snapshot.Path)
			}
		}
		before, desired := map[string]any{}, map[string]any{}
		if err := util.UnmarshalJSON(fd[0].Desired, &desired); err != nil {
			return err
		}
		if len(snapshot.Original) > 0 {
			if err := util.UnmarshalJSON(snapshot.Original, &before); err != nil {
				return fmt.Errorf("invalid portable settings snapshot: %w", err)
			}
		}
		if reflect.DeepEqual(current, desired) {
			fd[0].Kind = domain.DiffIdentical
		} else if reflect.DeepEqual(before, desired) && len(snapshot.Original) > 0 {
			fd[0].Desired = snapshot.Original
		}
		diffs = append(diffs, fd[0])
	}
	if err := restoreOperationSkills(configDir, op); err != nil {
		return err
	}
	// Restore activation first; no settings failure may leave a live reference
	// to a new payload that recovery has already removed.
	for i, fd := range diffs {
		if fd.Kind == domain.DiffIdentical {
			continue
		}
		root := map[string]any{}
		if err := util.UnmarshalJSON(fd.Desired, &root); err != nil {
			return err
		}
		snapshot := p.Settings[i]
		if !snapshot.Existed && len(root) == 0 {
			if err := os.Remove(fd.Dst); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else if err := fs.WriteFile(fd.Dst, fd.Desired, snapshot.Mode); err != nil {
			return err
		}
	}
	retained := false
	for path, snapshot := range p.Payloads {
		files, err := fs.ReadPackage(path)
		missing := false
		if err != nil {
			_, rootErr := os.Lstat(path)
			missing = os.IsNotExist(rootErr)
			if !missing {
				return err
			}
		}
		current := domain.SingleFileDigest(domain.PackageManifest(files))
		if !missing && snapshot.Before.Exists && current == snapshot.Before.Digest {
			continue
		}
		if !missing && current != snapshot.DesiredDigest {
			dst := filepath.Join(nativeOperationDir(configDir), "interrupted-portable", util.ContentDigest([]byte(path)))
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return err
			}
			if _, err := os.Lstat(dst); !os.IsNotExist(err) {
				return fmt.Errorf("portable payload changed again during recovery; both copies retained")
			}
			if err := os.Rename(path, dst); err != nil {
				return err
			}
			retained = true
		} else if !missing {
			if err := fs.RemovePackage(path); err != nil {
				return err
			}
		}
		if snapshot.Before.Exists {
			files, err := fs.ReadPackage(portableBackupPath(configDir, path))
			if err != nil {
				return err
			}
			if err := fs.WritePackage(path, files); err != nil {
				return err
			}
		}
	}
	if retained || util.PathExists(filepath.Join(nativeOperationDir(configDir), "interrupted-portable")) || util.PathExists(filepath.Join(nativeOperationDir(configDir), "interrupted-skills")) {
		dst := filepath.Join(configDir, ".tmp/native-recoveries", op.ID)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		return os.Rename(nativeOperationDir(configDir), dst)
	}
	return util.RemoveOwnedTree(nativeOperationDir(configDir))
}
