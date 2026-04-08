package app

import (
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestCycleCollisionStrategy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input config.CollisionStrategy
		want  config.CollisionStrategy
	}{
		{"", config.CollisionFirstWins},                       // empty (default) → first-wins
		{config.CollisionLastWins, config.CollisionFirstWins}, // last-wins → first-wins
		{config.CollisionFirstWins, config.CollisionError},    // first-wins → error
		{config.CollisionError, config.CollisionLastWins},     // error → last-wins
	}
	for _, tt := range tests {
		var cfg config.SyncConfig
		cfg.Defaults.CollisionStrategy = tt.input
		got := CycleCollisionStrategy(cfg)
		if got.Defaults.CollisionStrategy != tt.want {
			t.Errorf("CycleCollisionStrategy(%q) = %q, want %q", tt.input, got.Defaults.CollisionStrategy, tt.want)
		}
	}
}

func TestCycleCollisionStrategy_FullLoop(t *testing.T) {
	t.Parallel()

	var cfg config.SyncConfig // starts empty (treated as last-wins)
	cfg = CycleCollisionStrategy(cfg)
	if cfg.Defaults.CollisionStrategy != config.CollisionFirstWins {
		t.Fatalf("step 1: got %q, want first-wins", cfg.Defaults.CollisionStrategy)
	}
	cfg = CycleCollisionStrategy(cfg)
	if cfg.Defaults.CollisionStrategy != config.CollisionError {
		t.Fatalf("step 2: got %q, want error", cfg.Defaults.CollisionStrategy)
	}
	cfg = CycleCollisionStrategy(cfg)
	if cfg.Defaults.CollisionStrategy != config.CollisionLastWins {
		t.Fatalf("step 3: got %q, want last-wins", cfg.Defaults.CollisionStrategy)
	}
	cfg = CycleCollisionStrategy(cfg)
	if cfg.Defaults.CollisionStrategy != config.CollisionFirstWins {
		t.Fatalf("step 4: got %q, want first-wins (loop back)", cfg.Defaults.CollisionStrategy)
	}
}

func TestCycleSyncScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"", string(domain.ScopeProject)},                         // empty (default=global) → project
		{string(domain.ScopeGlobal), string(domain.ScopeProject)}, // global → project
		{string(domain.ScopeProject), string(domain.ScopeGlobal)}, // project → global
	}
	for _, tt := range tests {
		var cfg config.SyncConfig
		cfg.Defaults.Scope = tt.input
		got := CycleSyncScope(cfg)
		if got.Defaults.Scope != tt.want {
			t.Errorf("CycleSyncScope(%q) = %q, want %q", tt.input, got.Defaults.Scope, tt.want)
		}
	}
}
