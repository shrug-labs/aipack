package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// ConfigCmd groups aipack configuration commands.
type ConfigCmd struct {
	Defaults ConfigDefaultsCmd `cmd:"" help:"Manage sync-config defaults"`
	Env      EnvCmd            `cmd:"" help:"Manage the config-dir .env file used by {env:*} refs"`
	Params   ConfigParamsCmd   `cmd:"" help:"Manage profile-scoped params used by {params.*} refs"`
}

func (c *ConfigCmd) Help() string {
	return `Manage aipack configuration, including sync defaults, .env values, and profile params.

Examples:
  aipack config defaults get auto_sync
  aipack config defaults set auto_sync true
  aipack config defaults set namespaced true
  aipack config env list
  aipack config env set API_TOKEN abc123
  aipack config params set workspace team-alpha`
}

// ConfigDefaultsCmd manages sync-config.yaml defaults.
type ConfigDefaultsCmd struct {
	Get ConfigDefaultsGetCmd `cmd:"" help:"Print a sync-config default"`
	Set ConfigDefaultsSetCmd `cmd:"" help:"Set a sync-config default"`
}

func (c *ConfigDefaultsCmd) Help() string {
	return `Manage defaults in sync-config.yaml.

Examples:
  aipack config defaults get auto_sync
  aipack config defaults set auto_sync true
  aipack config defaults set namespaced true`
}

// ConfigDefaultsGetCmd prints scalar defaults from sync-config.yaml.
type ConfigDefaultsGetCmd struct {
	Key string `arg:"" help:"Setting key (profile, harnesses, scope, collision_strategy, auto_sync, namespaced)"`
}

func (c *ConfigDefaultsGetCmd) Help() string {
	return fmt.Sprintf(`Prints a scalar sync-config default.

Supported settings:
  %s
Aliases:
  hyphenated names and defaults.<name> are accepted

Examples:
  aipack config defaults get profile
  aipack config defaults get harnesses
  aipack config defaults get auto_sync
  aipack config defaults get namespaced`, supportedConfigDefaults())
}

func (c *ConfigDefaultsGetCmd) Run(_ context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	key, ok := normalizeConfigDefaultKey(c.Key)
	if !ok {
		fmt.Fprintf(g.Stderr, "unknown config setting %q (supported: %s)\n", c.Key, supportedConfigDefaults())
		return ExitError{Code: cmdutil.ExitUsage}
	}

	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(cfgDir))
	if err != nil {
		return err
	}
	fmt.Fprintln(g.Stdout, configDefaultValue(syncCfg, key))
	return nil
}

// ConfigDefaultsSetCmd updates scalar defaults in sync-config.yaml.
type ConfigDefaultsSetCmd struct {
	Key   string `arg:"" help:"Setting key (profile, harnesses, scope, collision_strategy, auto_sync, namespaced)"`
	Value string `arg:"" help:"Setting value"`
}

func (c *ConfigDefaultsSetCmd) Help() string {
	return fmt.Sprintf(`Sets a scalar sync-config default without opening sync-config.yaml by hand.

Supported settings:
  %s
Aliases:
  hyphenated names and defaults.<name> are accepted

Examples:
  aipack config defaults set profile default
  aipack config defaults set harnesses codex,opencode
  aipack config defaults set scope global
  aipack config defaults set collision_strategy last-wins
  aipack config defaults set auto_sync true
  aipack config defaults set auto-sync false
  aipack config defaults set namespaced true`, supportedConfigDefaults())
}

func (c *ConfigDefaultsSetCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	key, ok := normalizeConfigDefaultKey(c.Key)
	if !ok {
		fmt.Fprintf(g.Stderr, "unknown config setting %q (supported: %s)\n", c.Key, supportedConfigDefaults())
		return ExitError{Code: cmdutil.ExitUsage}
	}

	if key == defaultProfile {
		name, err := setDefaultProfile(cfgDir, c.Value)
		if err != nil {
			return err
		}
		fmt.Fprintf(g.Stdout, "Set %s to %s\n", key, name)
		if !profileHasInstalledPacks(cfgDir, name, g.Stdout, g.Stderr) {
			return nil
		}
		return maybeAutoSyncProfileWithHint(ctx, g, cfgDir, name)
	}

	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(cfgDir))
	if err != nil {
		return err
	}
	value, err := setConfigDefault(&syncCfg, key, c.Value)
	if err != nil {
		fmt.Fprintln(g.Stderr, err)
		return ExitError{Code: cmdutil.ExitUsage}
	}
	if err := config.SaveSyncConfig(config.SyncConfigPath(cfgDir), syncCfg); err != nil {
		return err
	}
	fmt.Fprintf(g.Stdout, "Set %s to %s\n", key, value)
	if key == defaultNamespaced {
		fmt.Fprintln(g.Stdout, "Next: run 'aipack sync --dry-run' to preview rendered name changes, then 'aipack sync' to apply.")
	}
	return nil
}

type configDefaultKey string

const (
	defaultProfile           configDefaultKey = "defaults.profile"
	defaultHarnesses         configDefaultKey = "defaults.harnesses"
	defaultScope             configDefaultKey = "defaults.scope"
	defaultCollisionStrategy configDefaultKey = "defaults.collision_strategy"
	defaultAutoSync          configDefaultKey = "defaults.auto_sync"
	defaultNamespaced        configDefaultKey = "defaults.namespaced"
)

type configDefaultSpec struct {
	key  configDefaultKey
	name string
	get  func(config.SyncConfig) string
	set  func(*config.SyncConfig, string) error
}

var configDefaultSpecs = []configDefaultSpec{
	{
		key:  defaultProfile,
		name: "profile",
		get: func(syncCfg config.SyncConfig) string {
			if syncCfg.Defaults.Profile == "" {
				return "default"
			}
			return syncCfg.Defaults.Profile
		},
	},
	{
		key:  defaultHarnesses,
		name: "harnesses",
		get: func(syncCfg config.SyncConfig) string {
			return strings.Join(syncCfg.Defaults.Harnesses, ",")
		},
		set: func(syncCfg *config.SyncConfig, raw string) error {
			harnesses, err := parseDefaultHarnesses(raw)
			if err != nil {
				return err
			}
			syncCfg.Defaults.Harnesses = harnesses
			return nil
		},
	},
	{
		key:  defaultScope,
		name: "scope",
		get: func(syncCfg config.SyncConfig) string {
			if syncCfg.Defaults.Scope == "" {
				return string(domain.ScopeGlobal)
			}
			return syncCfg.Defaults.Scope
		},
		set: func(syncCfg *config.SyncConfig, raw string) error {
			scope, err := cmdutil.NormalizeScope(raw)
			if err != nil {
				return err
			}
			syncCfg.Defaults.Scope = string(scope)
			return nil
		},
	},
	{
		key:  defaultCollisionStrategy,
		name: "collision_strategy",
		get: func(syncCfg config.SyncConfig) string {
			if syncCfg.Defaults.CollisionStrategy == "" {
				return string(config.CollisionLastWins)
			}
			return string(syncCfg.Defaults.CollisionStrategy)
		},
		set: func(syncCfg *config.SyncConfig, raw string) error {
			strategy, err := config.NormalizeCollisionStrategy(raw)
			if err != nil {
				return err
			}
			syncCfg.Defaults.CollisionStrategy = strategy
			return nil
		},
	},
	{
		key:  defaultAutoSync,
		name: "auto_sync",
		get: func(syncCfg config.SyncConfig) string {
			return strconv.FormatBool(syncCfg.Defaults.AutoSync)
		},
		set: func(syncCfg *config.SyncConfig, raw string) error {
			value, err := parseConfigBool(raw)
			if err != nil {
				return err
			}
			syncCfg.Defaults.AutoSync = value
			return nil
		},
	},
	{
		key:  defaultNamespaced,
		name: "namespaced",
		get: func(syncCfg config.SyncConfig) string {
			return strconv.FormatBool(syncCfg.Defaults.Namespaced)
		},
		set: func(syncCfg *config.SyncConfig, raw string) error {
			value, err := parseConfigBool(raw)
			if err != nil {
				return err
			}
			syncCfg.Defaults.Namespaced = value
			return nil
		},
	},
}

func supportedConfigDefaults() string {
	names := make([]string, 0, len(configDefaultSpecs))
	for _, spec := range configDefaultSpecs {
		names = append(names, spec.name)
	}
	return strings.Join(names, ", ")
}

func normalizeConfigDefaultKey(key string) (configDefaultKey, bool) {
	normalized := normalizeConfigDefaultName(key)
	for _, spec := range configDefaultSpecs {
		if spec.name == normalized {
			return spec.key, true
		}
	}
	return "", false
}

func normalizeConfigDefaultName(key string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(strings.ToLower(key)), "-", "_")
	return strings.TrimPrefix(normalized, "defaults.")
}

func configDefaultSpecForKey(key configDefaultKey) (configDefaultSpec, bool) {
	for _, spec := range configDefaultSpecs {
		if spec.key == key {
			return spec, true
		}
	}
	return configDefaultSpec{}, false
}

func configDefaultValue(syncCfg config.SyncConfig, key configDefaultKey) string {
	spec, ok := configDefaultSpecForKey(key)
	if !ok {
		return ""
	}
	return spec.get(syncCfg)
}

func setDefaultProfile(configDir, value string) (string, error) {
	if err := app.ProfileSet(app.ProfileSetRequest{
		ConfigDir: configDir,
		Name:      value,
	}); err != nil {
		return "", err
	}
	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		return "", err
	}
	return configDefaultValue(syncCfg, defaultProfile), nil
}

func setConfigDefault(syncCfg *config.SyncConfig, key configDefaultKey, raw string) (string, error) {
	spec, ok := configDefaultSpecForKey(key)
	if !ok || spec.set == nil {
		return "", fmt.Errorf("unknown config setting %q", key)
	}
	if err := spec.set(syncCfg, raw); err != nil {
		return "", err
	}
	return configDefaultValue(*syncCfg, key), nil
}

func parseDefaultHarnesses(raw string) ([]string, error) {
	rawHarnesses := cmdutil.ParseHarnessEnv(raw)
	if len(rawHarnesses) == 0 {
		return nil, fmt.Errorf("harnesses must include at least one harness")
	}
	harnesses, err := cmdutil.ResolveHarnesses(rawHarnesses)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(harnesses))
	for _, harness := range harnesses {
		name := string(harness)
		if seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	return result, nil
}

func parseConfigBool(value string) (bool, error) {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("invalid boolean value %q (expected true or false)", value)
	}
	return parsed, nil
}
