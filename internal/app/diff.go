package app

import "github.com/shrug-labs/aipack/internal/engine"

// ComputeDiff returns a unified diff between two byte slices.
func ComputeDiff(a, b []byte, labelA, labelB string) string {
	return engine.UnifiedDiff(a, b, labelA, labelB)
}
