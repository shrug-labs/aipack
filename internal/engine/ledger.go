package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
)

// LoadLedger reads a ledger from disk; returns an empty ledger if the file does not exist.
// Returns a warning if the file exists but contains invalid JSON.
func (e *Engine) LoadLedger(path string) (domain.Ledger, []domain.Warning, error) {
	b, err := e.FS.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.NewLedger(), nil, nil
		}
		return domain.Ledger{}, nil, err
	}
	var raw struct {
		Managed         map[string]domain.Entry `json:"managed"`
		UpdatedAtEpochS int64                   `json:"updated_at_epoch_s"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return domain.NewLedger(), []domain.Warning{{
			Field:   "ledger",
			Message: fmt.Sprintf("corrupt ledger %s (resetting): %v", path, err),
		}}, nil
	}
	if raw.Managed == nil {
		raw.Managed = map[string]domain.Entry{}
	}
	return domain.Ledger{Managed: raw.Managed, UpdatedAt: raw.UpdatedAtEpochS}, nil, nil
}

// SaveLedger persists a ledger to disk.
// When dryRun is true the file is not actually written.
func (e *Engine) SaveLedger(path string, l domain.Ledger, dryRun bool) error {
	payload := map[string]any{
		"schema_version":     1,
		"updated_at_epoch_s": time.Now().Unix(),
		"tool":               "aipack",
		"managed":            l.Managed,
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if dryRun {
		return nil
	}
	if err := e.FS.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return e.FS.WriteFile(path, b, 0o600)
}

// EncodeProjectPath encodes an absolute project directory as a directory name
// using the Claude Code convention: replace path separators with hyphens.
// /Users/foo/bar → -Users-foo-bar
// On Windows, colons (from drive letters like C:) are also stripped and
// both forward and back slashes are replaced.
func EncodeProjectPath(projectDir string) string {
	s := strings.ReplaceAll(projectDir, "\\", "-")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, ":", "")
	return s
}

// MigrateOldLedgers checks for legacy ledger formats and splits entries into
// per-harness ledgers. Returns the number of entries migrated.
//
// Legacy formats detected:
//   - Global combined: ~/.config/aipack/ledger/claudecode+opencode.json
//   - Project local:   <projectDir>/.aipack/ledger.json
//
// Entries are routed by path prefix matching against harness managed roots.
func (e *Engine) MigrateOldLedgers(configDir string, scope domain.Scope, projectDir string, harnesses []domain.Harness, managedRoots map[domain.Harness][]string) (int, error) {
	migrated := 0

	// Check for old project-local ledger.
	if scope == domain.ScopeProject {
		oldPath := filepath.Join(projectDir, ".aipack", "ledger.json")
		n, err := e.migrateOneLedger(oldPath, configDir, scope, projectDir, harnesses, managedRoots)
		if err != nil {
			return 0, err
		}
		migrated += n
	}

	// Check for old combined-harness ledgers in global ledger dir.
	ledgerDir := filepath.Join(configDir, "ledger")
	entries, err := e.FS.ReadDir(ledgerDir)
	if err != nil {
		if os.IsNotExist(err) {
			return migrated, nil
		}
		return 0, err
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		// Combined ledgers contain "+" in the filename.
		if !strings.Contains(ent.Name(), "+") {
			continue
		}
		oldPath := filepath.Join(ledgerDir, ent.Name())
		n, err := e.migrateOneLedger(oldPath, configDir, scope, projectDir, harnesses, managedRoots)
		if err != nil {
			return migrated, err
		}
		migrated += n
	}

	return migrated, nil
}

func (e *Engine) migrateOneLedger(oldPath, configDir string, scope domain.Scope, projectDir string, harnesses []domain.Harness, managedRoots map[domain.Harness][]string) (int, error) {
	old, _, err := e.LoadLedger(oldPath)
	if err != nil || len(old.Managed) == 0 {
		return 0, err
	}

	// Load or create per-harness ledgers and distribute entries.
	perHarness := map[domain.Harness]*domain.Ledger{}
	for _, h := range harnesses {
		lp := LedgerPath(configDir, scope, projectDir, h)
		lg, _, lerr := e.LoadLedger(lp)
		if lerr != nil {
			return 0, lerr
		}
		perHarness[h] = &lg
	}

	migrated := 0
	for path, entry := range old.Managed {
		for h, roots := range managedRoots {
			if domain.IsUnderAny(path, roots) {
				if lg, ok := perHarness[h]; ok {
					if _, exists := lg.Managed[path]; !exists {
						lg.Managed[path] = entry
						migrated++
					}
				}
				break
			}
		}
	}

	if migrated == 0 {
		return 0, nil
	}

	// Save per-harness ledgers.
	for h, lg := range perHarness {
		lp := LedgerPath(configDir, scope, projectDir, h)
		if err := e.SaveLedger(lp, *lg, false); err != nil {
			return migrated, err
		}
	}

	return migrated, nil
}

// LedgerPath returns the ledger file path for a single harness.
// All ledgers live under <configDir>/ledger/. Project-scoped ledgers use a
// path-encoded subdirectory.
func LedgerPath(configDir string, scope domain.Scope, projectDir string, harness domain.Harness) string {
	base := filepath.Join(configDir, "ledger")
	if scope == domain.ScopeProject {
		base = filepath.Join(base, EncodeProjectPath(projectDir))
	}
	return filepath.Join(base, string(harness)+".json")
}
