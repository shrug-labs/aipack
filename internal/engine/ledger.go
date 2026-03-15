package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// LoadLedger reads a ledger from disk; returns an empty ledger if the file does not exist.
// Returns a warning string (non-empty) if the file exists but contains invalid JSON.
func LoadLedger(path string) (domain.Ledger, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.NewLedger(), "", nil
		}
		return domain.Ledger{}, "", err
	}
	var raw struct {
		Managed         map[string]domain.Entry `json:"managed"`
		UpdatedAtEpochS int64                   `json:"updated_at_epoch_s"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return domain.NewLedger(), fmt.Sprintf("corrupt ledger %s (resetting): %v", path, err), nil
	}
	if raw.Managed == nil {
		raw.Managed = map[string]domain.Entry{}
	}
	return domain.Ledger{Managed: raw.Managed, UpdatedAt: raw.UpdatedAtEpochS}, "", nil
}

// SaveLedger persists a ledger to disk.
// When dryRun is true the file is not actually written.
func SaveLedger(path string, l domain.Ledger, dryRun bool) error {
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
	return util.WriteFileAtomicWithPerms(path, b, 0o700, 0o600)
}

// EncodeProjectPath encodes an absolute project directory as a directory name
// using the Claude Code convention: replace path separators with hyphens.
// /Users/foo/bar → -Users-foo-bar
func EncodeProjectPath(projectDir string) string {
	return strings.ReplaceAll(projectDir, string(filepath.Separator), "-")
}

// MigrateOldLedgers checks for legacy ledger formats and splits entries into
// per-harness ledgers. Returns the number of entries migrated.
//
// Legacy formats detected:
//   - Global combined: ~/.config/aipack/ledger/claudecode+opencode.json
//   - Project local:   <projectDir>/.aipack/ledger.json
//
// Entries are routed by path prefix matching against harness managed roots.
func MigrateOldLedgers(scope domain.Scope, projectDir, home string, harnesses []string, managedRoots map[string][]string) (int, error) {
	migrated := 0

	// Check for old project-local ledger.
	if scope == domain.ScopeProject {
		oldPath := filepath.Join(projectDir, ".aipack", "ledger.json")
		n, err := migrateOneLedger(oldPath, scope, projectDir, home, harnesses, managedRoots)
		if err != nil {
			return 0, err
		}
		migrated += n
	}

	// Check for old combined-harness ledgers in global ledger dir.
	ledgerDir := filepath.Join(home, ".config", "aipack", "ledger")
	entries, err := os.ReadDir(ledgerDir)
	if err != nil {
		if os.IsNotExist(err) {
			return migrated, nil
		}
		return 0, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// Combined ledgers contain "+" in the filename.
		if !strings.Contains(e.Name(), "+") {
			continue
		}
		oldPath := filepath.Join(ledgerDir, e.Name())
		n, err := migrateOneLedger(oldPath, scope, projectDir, home, harnesses, managedRoots)
		if err != nil {
			return migrated, err
		}
		migrated += n
	}

	return migrated, nil
}

func migrateOneLedger(oldPath string, scope domain.Scope, projectDir, home string, harnesses []string, managedRoots map[string][]string) (int, error) {
	old, _, err := LoadLedger(oldPath)
	if err != nil || len(old.Managed) == 0 {
		return 0, err
	}

	// Load or create per-harness ledgers and distribute entries.
	perHarness := map[string]*domain.Ledger{}
	for _, h := range harnesses {
		lp := LedgerPathForScope(scope, projectDir, home, h)
		lg, _, lerr := LoadLedger(lp)
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
		lp := LedgerPathForScope(scope, projectDir, home, h)
		if err := SaveLedger(lp, *lg, false); err != nil {
			return migrated, err
		}
	}

	return migrated, nil
}

// LedgerPathForScope returns the ledger file path for a single harness.
// All ledgers live under ~/.config/aipack/ledger/. Project-scoped ledgers
// use a path-encoded subdirectory.
func LedgerPathForScope(scope domain.Scope, projectDir, home, harness string) string {
	if home == "" {
		home = projectDir // fallback
	}
	base := filepath.Join(home, ".config", "aipack", "ledger")
	if scope == domain.ScopeProject {
		base = filepath.Join(base, EncodeProjectPath(projectDir))
	}
	return filepath.Join(base, harness+".json")
}
