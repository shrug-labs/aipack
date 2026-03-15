package app

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// ProfileCreateRequest holds the inputs for creating a profile.
type ProfileCreateRequest struct {
	ConfigDir string
	Name      string
}

// ProfileCreate creates a new empty profile YAML file.
func ProfileCreate(req ProfileCreateRequest) error {
	name, err := config.NormalizeProfileName(req.Name)
	if err != nil {
		return err
	}

	profilesDir := filepath.Join(req.ConfigDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		return fmt.Errorf("creating profiles directory: %w", err)
	}

	dest := filepath.Join(profilesDir, name+".yaml")
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("profile %q already exists at %s", name, dest)
	}

	cfg := config.ProfileConfig{
		SchemaVersion: config.ProfileSchemaVersion,
		Packs:         []config.PackEntry{},
	}
	out, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshalling profile: %w", err)
	}

	if err := util.WriteFileAtomicWithPerms(dest, out, 0o700, 0o600); err != nil {
		return fmt.Errorf("writing profile: %w", err)
	}
	return nil
}

// ProfileDeleteRequest holds the inputs for deleting a profile.
type ProfileDeleteRequest struct {
	ConfigDir string
	Name      string
}

// ProfileDelete deletes a profile YAML file and clears the active profile
// setting if the deleted profile was active.
func ProfileDelete(req ProfileDeleteRequest) error {
	name, err := config.NormalizeProfileName(req.Name)
	if err != nil {
		return err
	}

	profilePath := filepath.Join(req.ConfigDir, "profiles", name+".yaml")
	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		return fmt.Errorf("profile %q does not exist", name)
	}

	if err := os.Remove(profilePath); err != nil {
		return fmt.Errorf("deleting profile: %w", err)
	}

	// If this was the active profile, clear the setting.
	scPath := config.SyncConfigPath(req.ConfigDir)
	sc, loadErr := config.LoadSyncConfig(scPath)
	if loadErr == nil && sc.Defaults.Profile == name {
		sc.Defaults.Profile = ""
		_ = config.SaveSyncConfig(scPath, sc)
	}

	return nil
}

// ProfileSaveRequest holds the inputs for saving a profile.
type ProfileSaveRequest struct {
	ConfigDir string
	Name      string
	Config    config.ProfileConfig
}

// ProfileSave writes a profile config to disk with proper permissions.
func ProfileSave(req ProfileSaveRequest) error {
	name, err := config.NormalizeProfileName(req.Name)
	if err != nil {
		return err
	}

	dest := filepath.Join(req.ConfigDir, "profiles", name+".yaml")
	out, err := yaml.Marshal(&req.Config)
	if err != nil {
		return fmt.Errorf("marshalling profile: %w", err)
	}

	if err := util.WriteFileAtomicWithPerms(dest, out, 0o700, 0o600); err != nil {
		return fmt.Errorf("writing profile: %w", err)
	}
	return nil
}

// ProfileDuplicateRequest holds the inputs for duplicating a profile.
type ProfileDuplicateRequest struct {
	ConfigDir string
	SrcName   string
	DstName   string
}

// ProfileDuplicate copies a profile to a new name with validation and proper permissions.
func ProfileDuplicate(req ProfileDuplicateRequest) error {
	dstName, err := config.NormalizeProfileName(req.DstName)
	if err != nil {
		return err
	}

	srcName, err := config.NormalizeProfileName(req.SrcName)
	if err != nil {
		return err
	}
	srcPath := filepath.Join(req.ConfigDir, "profiles", srcName+".yaml")
	dstPath := filepath.Join(req.ConfigDir, "profiles", dstName+".yaml")

	if _, err := os.Stat(dstPath); err == nil {
		return fmt.Errorf("profile %q already exists", dstName)
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("reading source profile: %w", err)
	}

	if err := util.WriteFileAtomicWithPerms(dstPath, data, 0o700, 0o600); err != nil {
		return fmt.Errorf("writing duplicate profile: %w", err)
	}
	return nil
}

// ProfileSetRequest holds the inputs for setting the active profile.
type ProfileSetRequest struct {
	ConfigDir string
	Name      string
}

// ProfileSet sets the active profile in sync-config.
func ProfileSet(req ProfileSetRequest) error {
	name, err := config.NormalizeProfileName(req.Name)
	if err != nil {
		return err
	}
	profilePath := filepath.Join(req.ConfigDir, "profiles", name+".yaml")
	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		return fmt.Errorf("profile %q does not exist at %s", name, profilePath)
	}
	scPath := config.SyncConfigPath(req.ConfigDir)
	sc, err := config.LoadSyncConfig(scPath)
	if err != nil {
		return fmt.Errorf("loading sync-config: %w", err)
	}
	sc.Defaults.Profile = name
	if err := config.SaveSyncConfig(scPath, sc); err != nil {
		return fmt.Errorf("saving sync-config: %w", err)
	}
	return nil
}

// ProfileListItem holds display-ready profile information.
type ProfileListItem struct {
	Name     string
	Path     string
	IsActive bool
	Config   config.ProfileConfig
	LoadErr  error
}

// ProfileListItems lists all profiles with their configs, marking which is active.
func ProfileListItems(configDir string, syncCfg config.SyncConfig) ([]ProfileListItem, error) {
	profilesDir := filepath.Join(configDir, "profiles")
	names, err := config.ListProfileNames(profilesDir)
	if err != nil {
		return nil, err
	}
	defaultProfile := syncCfg.Defaults.Profile
	if defaultProfile == "" {
		defaultProfile = "default"
	}
	items := make([]ProfileListItem, 0, len(names))
	for _, name := range names {
		path := filepath.Join(profilesDir, name+".yaml")
		item := ProfileListItem{
			Name:     name,
			Path:     path,
			IsActive: name == defaultProfile,
		}
		cfg, loadErr := config.LoadProfile(path)
		item.Config = cfg
		item.LoadErr = loadErr
		items = append(items, item)
	}
	return items, nil
}

// SaveSyncConfig writes sync-config to disk.
func SaveSyncConfig(configDir string, cfg config.SyncConfig) error {
	return config.SaveSyncConfig(config.SyncConfigPath(configDir), cfg)
}

// ReloadSyncConfig loads sync-config from disk.
func ReloadSyncConfig(configDir string) (config.SyncConfig, error) {
	return config.LoadSyncConfig(config.SyncConfigPath(configDir))
}

// ToggleSyncHarness returns a new SyncConfig with the named harness toggled on or off.
func ToggleSyncHarness(cfg config.SyncConfig, name string) config.SyncConfig {
	for i, h := range cfg.Defaults.Harnesses {
		if h == name {
			out := make([]string, 0, len(cfg.Defaults.Harnesses)-1)
			out = append(out, cfg.Defaults.Harnesses[:i]...)
			out = append(out, cfg.Defaults.Harnesses[i+1:]...)
			cfg.Defaults.Harnesses = out
			return cfg
		}
	}
	cfg.Defaults.Harnesses = append(cfg.Defaults.Harnesses, name)
	return cfg
}

// CycleSyncScope returns a new SyncConfig with the scope toggled between project and global.
func CycleSyncScope(cfg config.SyncConfig) config.SyncConfig {
	if cfg.Defaults.Scope == string(domain.ScopeGlobal) {
		cfg.Defaults.Scope = string(domain.ScopeProject)
	} else {
		cfg.Defaults.Scope = string(domain.ScopeGlobal)
	}
	return cfg
}
