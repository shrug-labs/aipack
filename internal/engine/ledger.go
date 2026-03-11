package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
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
		Managed map[string]domain.Entry `json:"managed"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return domain.NewLedger(), fmt.Sprintf("corrupt ledger %s (resetting): %v", path, err), nil
	}
	if raw.Managed == nil {
		raw.Managed = map[string]domain.Entry{}
	}
	return domain.Ledger{Managed: raw.Managed}, "", nil
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

// LedgerPathForScope returns the ledger file path for the given scope and harness set.
func LedgerPathForScope(scope domain.Scope, projectDir, home string, harnesses []string) string {
	if scope == domain.ScopeProject {
		return fmt.Sprintf("%s/.aipack/ledger.json", projectDir)
	}
	if home == "" {
		return fmt.Sprintf("%s/.aipack/ledger.json", projectDir)
	}
	keys := make([]string, len(harnesses))
	copy(keys, harnesses)
	sort.Strings(keys)
	name := ""
	for i, k := range keys {
		if i > 0 {
			name += "+"
		}
		name += k
	}
	if name == "" {
		name = "multi"
	}
	return fmt.Sprintf("%s/.config/aipack/ledger/%s.json", home, name)
}
