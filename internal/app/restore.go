package app

import (
	"fmt"
	"io"

	"github.com/shrug-labs/aipack/internal/engine"
)

// RestoreRequest holds parameters for a restore operation.
type RestoreRequest struct {
	TargetSpec
	FilterHarness string // when non-empty, restore only files for this harness
	DryRun        bool
	Stderr        io.Writer
}

// RestoreResult holds the output of a restore operation.
type RestoreResult struct {
	RestoredFiles []engine.RestoredFile
}

// RunRestore restores settings files from the presync cache.
func RunRestore(req RestoreRequest) (RestoreResult, error) {
	stderr := req.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	var result RestoreResult
	for _, harnessID := range req.Harnesses {
		ledgerPath := ledgerPathForScope(req.Scope, req.ProjectDir, req.Home, harnessID)
		restored, err := engine.RestoreFromCache(ledgerPath, req.FilterHarness, req.DryRun)
		if err != nil {
			return RestoreResult{}, err
		}
		result.RestoredFiles = append(result.RestoredFiles, restored...)
	}

	if len(result.RestoredFiles) == 0 {
		fmt.Fprintln(stderr, "no cached settings files found")
		return RestoreResult{}, nil
	}

	for _, r := range result.RestoredFiles {
		if req.DryRun {
			fmt.Fprintf(stderr, "  would restore: %s\n", r.OriginalPath)
		} else {
			fmt.Fprintf(stderr, "  restored: %s\n", r.OriginalPath)
		}
	}

	return result, nil
}
