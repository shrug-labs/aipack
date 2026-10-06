package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/plugin"
	"github.com/shrug-labs/aipack/internal/util"
)

// Portable imports reserve their native MCP namespaces without taking over
// existing user installations or accepting a partially merged launch command.
func preflightPortablePlugins(eng *engine.Engine, plan domain.Plan, ledger domain.Ledger, configDir string) error {
	if err := validateNativeLedgerHarness(plan.Ledger, ledger); err != nil {
		return err
	}
	deliveries := map[string]*domain.PackageDelivery{}
	for path, entry := range ledger.Managed {
		if entry.Delivery != nil {
			if !entry.Package {
				return fmt.Errorf("portable ownership requires a package tree: %s", path)
			}
			if err := validatePortableDelivery(path, *entry.Delivery); err != nil {
				return err
			}
			files, err := eng.FS.ReadPackage(path)
			if err != nil {
				if _, rootErr := eng.FS.Stat(path); !os.IsNotExist(err) || !os.IsNotExist(rootErr) {
					return fmt.Errorf("cannot validate portable payload %s: %w", path, err)
				}
			} else if domain.SingleFileDigest(domain.PackageManifest(files)) != entry.Digest {
				return fmt.Errorf("portable payload %s has local changes; preserve or revert them before syncing", path)
			}
			d := entry.Delivery
			deliveries[d.SettingsPath+"\x00"+d.Binding] = &domain.PackageDelivery{Binding: d.Binding, SettingsPath: d.SettingsPath, ManagedOverlay: []byte(`{"mcp":{}}`)}
		}
	}
	for _, action := range plan.Writes {
		if action.Delivery == nil {
			continue
		}
		if err := validatePortableDelivery(action.Dst, *action.Delivery); err != nil {
			return err
		}
		// A binding's config and data are shared across its generations. A
		// second scope must not change or clean another scope's delivery.
		if err := preflightPortableBinding(eng, action.Delivery.Binding, action.Delivery.SettingsPath, ledger); err != nil {
			return err
		}
		if entry, tracked := ledger.Managed[action.Dst]; !tracked || !entry.Package || entry.Delivery == nil {
			if _, err := eng.FS.Stat(action.Dst); err == nil {
				return fmt.Errorf("portable payload %s already exists without AIPack ownership", action.Dst)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		delivery := action.Delivery
		deliveries[delivery.SettingsPath+"\x00"+delivery.Binding] = delivery
	}
	for _, delivery := range deliveries {
		body, err := eng.FS.ReadFile(delivery.SettingsPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		disk, desired, owned := map[string]any{}, map[string]any{}, map[string]any{}
		if err := util.UnmarshalJSON(body, &disk); err != nil {
			return fmt.Errorf("cannot establish portable plugin ownership in %s: %w", delivery.SettingsPath, err)
		}
		if err := util.UnmarshalJSON(delivery.ManagedOverlay, &desired); err != nil {
			return err
		}
		for _, raw := range ledger.PackageOverlays(delivery.SettingsPath) {
			previous := map[string]any{}
			if err := util.UnmarshalJSON(raw, &previous); err != nil {
				return fmt.Errorf("invalid portable plugin ownership: %w", err)
			}
			if servers, ok := previous["mcp"].(map[string]any); ok {
				for name, entry := range servers {
					owned[name] = entry
				}
			}
		}
		servers, _ := disk["mcp"].(map[string]any)
		previous := map[string]any{}
		if len(owned) > 0 {
			if err := util.UnmarshalJSON(ledger.PrevManagedOverlay(delivery.SettingsPath), &previous); err != nil {
				return fmt.Errorf("invalid portable activation baseline: %w", err)
			}
		}
		previousServers, _ := previous["mcp"].(map[string]any)
		for name := range owned {
			if entry, present := servers[name]; present && !reflect.DeepEqual(entry, previousServers[name]) {
				return fmt.Errorf("OpenCode MCP server %s has local changes; preserve or revert them before syncing", name)
			}
		}
		for name := range desired["mcp"].(map[string]any) {
			for existing := range servers {
				if plugin.OpenCodeToolName(existing) != plugin.OpenCodeToolName(name) {
					continue
				}
				_, tracked := owned[existing]
				if existing != name || !tracked {
					return fmt.Errorf("OpenCode MCP namespace %s collides with unowned native server %s; resolve ownership before syncing", name, existing)
				}
			}
		}
	}
	return preflightPortableScopes(eng, plan, configDir)
}

func preflightPortableBinding(eng *engine.Engine, binding, settingsPath string, ledger domain.Ledger) error {
	bindingRoot := filepath.Join(filepath.Dir(settingsPath), "aipack-imports", binding)
	generations, err := eng.FS.ReadDir(bindingRoot)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, generation := range generations {
		path := filepath.Join(bindingRoot, generation.Name(), "payload")
		if _, err := eng.FS.Stat(path); os.IsNotExist(err) {
			continue // Empty generation parents remain after package removal.
		} else if err != nil {
			return err
		}
		if entry, owned := ledger.Managed[path]; !owned || !entry.Package || entry.Delivery == nil {
			return fmt.Errorf("portable binding %s is already delivered outside this scope; use separate native config roots or remove the other delivery first", binding)
		}
	}
	return nil
}

func preflightPortableScopes(eng *engine.Engine, plan domain.Plan, configDir string) error {
	selected := map[string]*domain.PackageDelivery{}
	for _, action := range plan.Writes {
		if action.Delivery != nil {
			selected[action.Delivery.Binding] = action.Delivery
		}
	}
	if len(selected) == 0 {
		return nil
	}
	return filepath.WalkDir(filepath.Join(configDir, "ledger"), func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Base(path) != "opencode.json" || filepath.Clean(path) == filepath.Clean(plan.Ledger) {
			return nil
		}
		other, warnings, err := eng.LoadLedger(path)
		if err != nil {
			return err
		}
		if len(warnings) != 0 {
			return fmt.Errorf("cannot determine portable scope ownership: %s", warnings[0])
		}
		for payload, owned := range other.Managed {
			d := owned.Delivery
			if d == nil || selected[d.Binding] == nil {
				continue
			}
			if !owned.Package {
				return fmt.Errorf("portable scope ownership requires a package tree: %s", payload)
			}
			if err := validatePortableDelivery(payload, *d); err != nil {
				return err
			}
			current := selected[d.Binding]
			if current.Home == "" || d.Home == "" || current.ConfigHome == "" || d.ConfigHome == "" {
				return fmt.Errorf("portable binding %s lacks scope ownership; clean the other scope before syncing", d.Binding)
			}
			scope := domain.ScopeGlobal
			if d.ProjectDir != "" {
				scope = domain.ScopeProject
			}
			if filepath.Clean(path) != engine.LedgerPath(configDir, scope, d.ProjectDir, domain.HarnessOpenCode) {
				return fmt.Errorf("portable scope ownership does not match its ledger: %s", path)
			}
			if !portableScopesOverlap(*current, *d) {
				continue
			}
			if current.Generation != d.Generation {
				return fmt.Errorf("portable binding %s has conflicting selections in overlapping scopes; clean or disable the other scope before syncing", d.Binding)
			}
		}
		return nil
	})
}

func portableScopesOverlap(a, b domain.PackageDelivery) bool {
	if a.ProjectDir != "" && b.ProjectDir != "" {
		return domain.IsUnderAny(canonicalPath(a.ProjectDir), []string{canonicalPath(b.ProjectDir)}) || domain.IsUnderAny(canonicalPath(b.ProjectDir), []string{canonicalPath(a.ProjectDir)})
	}
	for _, pair := range [][2]domain.PackageDelivery{{a, b}, {b, a}} {
		global, host := pair[0], pair[1]
		if global.ProjectDir != "" {
			continue
		}
		for _, root := range []string{host.ConfigHome, filepath.Join(host.Home, ".config/opencode"), filepath.Join(host.Home, ".opencode")} {
			if canonicalPath(filepath.Dir(global.SettingsPath)) == canonicalPath(root) {
				return true
			}
		}
	}
	return false
}
