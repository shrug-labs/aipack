package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

type HarnessTarget struct {
	Dir         string
	IsConfigDir bool
}

func targetForHarness(spec TargetSpec, hid domain.Harness) HarnessTarget {
	if spec.Scope == domain.ScopeProject {
		return HarnessTarget{Dir: spec.ProjectDir}
	}
	switch hid {
	case domain.HarnessCodex:
		if dir := envPathFromSpec(spec, "CODEX_HOME"); dir != "" {
			return HarnessTarget{Dir: dir, IsConfigDir: true}
		}
	case domain.HarnessOpenCode:
		if dir := envPathFromSpec(spec, "OPENCODE_CONFIG_DIR"); dir != "" {
			return HarnessTarget{Dir: dir, IsConfigDir: true}
		}
	}
	return HarnessTarget{Dir: spec.Home}
}

func targetDirForHarness(spec TargetSpec, hid domain.Harness) string {
	return targetForHarness(spec, hid).Dir
}

func targetIsConfigDirForHarness(spec TargetSpec, hid domain.Harness) bool {
	return targetForHarness(spec, hid).IsConfigDir
}

// envPathFromSpec resolves an environment variable by name, preferring an
// explicit override in spec.Env over the process environment. A nil or
// partial spec.Env is fine — keys not present in spec.Env fall through to
// os.Getenv, so callers can use spec.Env to override individual variables
// without suppressing runtime lookups for the rest.
func envPathFromSpec(spec TargetSpec, name string) string {
	if v, ok := spec.Env[name]; ok {
		return cleanEnvPath(v)
	}
	return envPath(name)
}

func envPath(name string) string {
	return cleanEnvPath(os.Getenv(name))
}

func cleanEnvPath(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	return filepath.Clean(v)
}

func planRequestForHarness(req SyncRequest, hid domain.Harness) engine.PlanRequest {
	return planRequestForTarget(req.TargetSpec, req.ConfigDir, req.SkipSettings, hid)
}

func planRequestForTarget(spec TargetSpec, configDir string, skipSettings bool, hid domain.Harness) engine.PlanRequest {
	target := targetForHarness(spec, hid)
	return engine.PlanRequest{
		ConfigDir:       configDir,
		Scope:           spec.Scope,
		Harnesses:       []domain.Harness{hid},
		ProjectDir:      spec.ProjectDir,
		Home:            spec.Home,
		TargetDir:       target.Dir,
		TargetConfigDir: target.IsConfigDir,
		SkipSettings:    skipSettings,
		Namespaced:      spec.Namespaced,
	}
}

func captureContextForHarness(spec TargetSpec, hid domain.Harness, knownPacks map[string]struct{}) harness.CaptureContext {
	target := targetForHarness(spec, hid)
	return harness.CaptureContext{
		Scope:           spec.Scope,
		ProjectDir:      spec.ProjectDir,
		Home:            spec.Home,
		TargetDir:       target.Dir,
		TargetConfigDir: target.IsConfigDir,
		KnownPacks:      knownPacks,
	}
}
