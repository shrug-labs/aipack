package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

type ProfileCmd struct {
	Create     ProfileCreateCmd     `cmd:"" help:"Create a new empty profile"`
	Delete     ProfileDeleteCmd     `cmd:"" help:"Delete a profile from the config directory"`
	List       ProfileListCmd       `cmd:"" help:"List available profiles in the config directory"`
	Set        ProfileSetCmd        `cmd:"" help:"Set the active profile (defaults.profile in sync-config)"`
	Show       ProfileShowCmd       `cmd:"" help:"Show resolved configuration for a profile"`
	Refs       ProfileRefsCmd       `cmd:"" help:"Show params and env refs required by a profile"`
	SetParam   ProfileSetParamCmd   `cmd:"" help:"Set a profile parameter"`
	UnsetParam ProfileUnsetParamCmd `cmd:"" help:"Unset a profile parameter"`
}

func (c *ProfileCmd) Help() string {
	return fmt.Sprintf(`Manage sync profiles. Profiles define which packs, content, and settings to
sync to which harnesses.

Profiles are stored as YAML files under %s.`,
		configPathDisplay("profiles"),
	)
}

// --- profile refs ---

type ProfileRefsCmd struct {
	Name string `arg:"" optional:"" help:"Profile name (default: sync-config defaults.profile, then 'default')" predictor:"profile"`
	JSON bool   `help:"Emit machine-readable JSON" name:"json"`
}

func (c *ProfileRefsCmd) Help() string {
	return `Shows {params.*} and {env:*} references used by enabled MCP servers and
contributing harness settings for a profile.

Examples:
  aipack profile refs
  aipack profile refs dev --json

See also: profile set-param, doctor`
}

func (c *ProfileRefsCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	result, err := app.ProfileRefs(app.ProfileRefsRequest{ConfigDir: cfgDir, ProfileName: c.Name})
	if err != nil {
		if errors.Is(err, config.ErrProfileNoPacks) {
			if c.JSON {
				return cmdutil.WriteJSON(g.Stdout, app.ProfileRefsResult{Profile: c.Name, Refs: []app.ProfileRef{}})
			}
			fmt.Fprintln(g.Stdout, "No packs in profile yet. Run `aipack pack install <source> --add` to add one.")
			return nil
		}
		return err
	}
	if c.JSON {
		if result.Refs == nil {
			result.Refs = []app.ProfileRef{}
		}
		return cmdutil.WriteJSON(g.Stdout, result)
	}
	fmt.Fprintf(g.Stdout, "Profile: %s\n", result.Profile)
	if len(result.Refs) == 0 {
		fmt.Fprintln(g.Stdout, "No params or env refs found.")
		return nil
	}
	for _, ref := range result.Refs {
		target := ref.Target
		if target == "" {
			target = ref.Pack
		}
		if target != "" {
			fmt.Fprintf(g.Stdout, "  %-22s %-10s %s\n", ref.Display, ref.Status, target)
		} else {
			fmt.Fprintf(g.Stdout, "  %-22s %s\n", ref.Display, ref.Status)
		}
	}
	return nil
}

// --- profile set-param ---

type ProfileSetParamCmd struct {
	Profile string `arg:"" help:"Profile name" predictor:"profile"`
	Key     string `arg:"" help:"Parameter key"`
	Value   string `arg:"" help:"Parameter value"`
}

func (c *ProfileSetParamCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	if err := app.ProfileSetParam(app.ProfileParamRequest{
		ConfigDir: cfgDir, ProfileName: c.Profile, Key: c.Key, Value: c.Value, Stdout: g.Stdout,
	}); err != nil {
		return err
	}
	fmt.Fprintf(g.Stdout, "Set %s in profile %s\n", c.Key, c.Profile)
	return nil
}

// --- profile unset-param ---

type ProfileUnsetParamCmd struct {
	Profile string `arg:"" help:"Profile name" predictor:"profile"`
	Key     string `arg:"" help:"Parameter key"`
}

func (c *ProfileUnsetParamCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	if err := app.ProfileUnsetParam(app.ProfileParamRequest{
		ConfigDir: cfgDir, ProfileName: c.Profile, Key: c.Key, Stdout: g.Stdout,
	}); err != nil {
		return err
	}
	fmt.Fprintf(g.Stdout, "Unset %s in profile %s\n", c.Key, c.Profile)
	return nil
}

// --- profile list ---

type ProfileListCmd struct{}

func (c *ProfileListCmd) Help() string {
	return fmt.Sprintf(`Lists all .yaml files in %s. The default profile (from
sync-config defaults.profile, or "default") is marked with (active).

Examples:
  # List profiles
  aipack profile list

  # Use a custom config directory
  aipack profile list --config-dir /path/to/config

See also: profile show, init`,
		configPathDisplay("profiles"),
	)
}

func (c *ProfileListCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}

	profilesDir := filepath.Join(cfgDir, "profiles")
	names, err := config.ListProfileNames(profilesDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(g.Stdout, "No profiles found.")
			return nil
		}
		return err
	}

	defaultProfile := ""
	sc, err := config.LoadSyncConfig(config.SyncConfigPath(cfgDir))
	if err == nil && sc.Defaults.Profile != "" {
		defaultProfile = sc.Defaults.Profile
	}
	if defaultProfile == "" {
		defaultProfile = "default"
	}

	if len(names) == 0 {
		fmt.Fprintln(g.Stdout, "No profiles found.")
		return nil
	}
	for _, name := range names {
		if name == defaultProfile {
			fmt.Fprintf(g.Stdout, "  %s (active)\n", name)
		} else {
			fmt.Fprintf(g.Stdout, "  %s\n", name)
		}
	}
	return nil
}

// --- profile set ---

type ProfileSetCmd struct {
	Name    string `arg:"" help:"Profile name to activate" predictor:"profile"`
	Install bool   `help:"Install missing packs from registry after setting profile" name:"install"`
}

func (c *ProfileSetCmd) Help() string {
	return fmt.Sprintf(`Sets the active profile by updating defaults.profile in sync-config.yaml.
The named profile must already exist under %s.

After setting the profile, reports any packs declared in the profile that are
not installed. Use --install to automatically install them from the registry.

Examples:
  # Activate a profile
  aipack profile set my-team

  # Activate a profile and install missing packs
  aipack profile set my-team --install

See also: profile list, profile create, pack install`,
		configPathDisplay("profiles"),
	)
}

func (c *ProfileSetCmd) Run(ctx context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	if err := app.ProfileSet(app.ProfileSetRequest{
		ConfigDir: cfgDir,
		Name:      c.Name,
	}); err != nil {
		return err
	}
	fmt.Fprintf(g.Stdout, "Active profile: %s\n", c.Name)

	if c.Install {
		results, err := app.PackInstallMissing(ctx, app.PackInstallMissingRequest{
			ConfigDir:   cfgDir,
			ProfileName: c.Name,
		}, g.Stdout)
		if err != nil {
			return err
		}
		printInstallMissingResults(results, c.Name, g.Stdout)
		if err := installMissingExitCode(results); err != nil {
			return err
		}
		return maybeAutoSyncProfile(ctx, g, cfgDir, c.Name)
	} else {
		if !profileHasInstalledPacks(cfgDir, c.Name, g.Stdout, g.Stderr) {
			return nil
		}
	}
	return maybeAutoSyncProfile(ctx, g, cfgDir, c.Name)
}

func profileHasInstalledPacks(cfgDir, profileName string, stdout, stderr io.Writer) bool {
	missing, err := app.ProfileMissingPacks(cfgDir, profileName)
	if err != nil {
		// Non-fatal — profile is already set.
		fmt.Fprintf(stderr, "Warning: could not check missing packs: %v\n", err)
		return false
	}
	if len(missing) > 0 {
		fmt.Fprintf(stdout, "%d pack(s) not installed: %s\n", len(missing), strings.Join(missing, ", "))
		fmt.Fprintln(stdout, "Run 'aipack pack install -m' to install them.")
		return false
	}
	return true
}

// --- profile show ---

type ProfileShowCmd struct {
	Name        string `arg:"" optional:"" help:"Profile name (default: sync-config defaults.profile, then 'default')" predictor:"profile"`
	ProfilePath string `help:"Direct path to a profile YAML file (overrides name)" name:"profile-path" type:"path"`
	JSON        bool   `help:"Emit machine-readable JSON" name:"json"`
}

func (c *ProfileShowCmd) Help() string {
	return `Loads and fully resolves a profile, displaying its sources, packs (with content
lists for rules, agents, workflows, skills, plugins, MCP servers), and harness
settings preferences.

Profile name resolution: positional argument > sync-config defaults.profile > "default"

Examples:
  # Show the default profile
  aipack profile show

  # Show a named profile
  aipack profile show production

  # Machine-readable JSON output
  aipack profile show --json

  # Show from a direct profile file
  aipack profile show --profile-path /path/to/profile.yaml

See also: profile list, sync, doctor`
}

func (c *ProfileShowCmd) Run(ctx context.Context, g *Globals) error {
	loaded, code := loadProfile(c.Name, c.ProfilePath, g.ConfigDir, g.Stderr)
	if code >= 0 {
		return ExitError{Code: code}
	}

	cmdutil.PrintWarnings(g.Stderr, loaded.warnings)

	if c.JSON {
		return cmdutil.WriteJSON(g.Stdout, loaded.profile)
	}

	fmt.Fprintf(g.Stdout, "Profile: %s (from %s)\n", loaded.profileName, loaded.profilePath)

	if len(loaded.profileCfg.Packs) > 0 {
		// Index MCP servers by source pack for display.
		mcpByPack := map[string][]string{}
		for _, s := range loaded.profile.MCPServers {
			mcpByPack[s.SourcePack] = append(mcpByPack[s.SourcePack], s.Name)
		}
		for k := range mcpByPack {
			slices.Sort(mcpByPack[k])
		}

		// Build set of enabled pack names from the resolved profile.
		enabledPacks := map[string]bool{}
		for _, p := range loaded.profile.Packs {
			enabledPacks[p.Name] = true
		}

		fmt.Fprintln(g.Stdout, "\nPacks:")
		for _, p := range loaded.profile.Packs {
			fmt.Fprintf(g.Stdout, "  %s\n", p.Name)
			if len(p.Rules) > 0 {
				names := make([]string, len(p.Rules))
				for i, r := range p.Rules {
					names[i] = r.Name
				}
				fmt.Fprintf(g.Stdout, "    Rules:       %s\n", strings.Join(names, ", "))
			}
			if len(p.Agents) > 0 {
				names := make([]string, len(p.Agents))
				for i, a := range p.Agents {
					names[i] = a.Name
				}
				fmt.Fprintf(g.Stdout, "    Agents:      %s\n", strings.Join(names, ", "))
			}
			if len(p.Workflows) > 0 {
				names := make([]string, len(p.Workflows))
				for i, w := range p.Workflows {
					names[i] = w.Name
				}
				fmt.Fprintf(g.Stdout, "    Workflows:   %s\n", strings.Join(names, ", "))
			}
			if len(p.Skills) > 0 {
				names := make([]string, len(p.Skills))
				for i, s := range p.Skills {
					names[i] = s.Name
				}
				fmt.Fprintf(g.Stdout, "    Skills:      %s\n", strings.Join(names, ", "))
			}
			if len(p.Plugins) > 0 {
				names := make([]string, len(p.Plugins))
				for i, plugin := range p.Plugins {
					names[i] = plugin.Name
				}
				fmt.Fprintf(g.Stdout, "    Plugins:     %s\n", strings.Join(names, ", "))
			}
			if mcpNames, ok := mcpByPack[p.Name]; ok && len(mcpNames) > 0 {
				fmt.Fprintf(g.Stdout, "    MCP Servers: %s\n", strings.Join(mcpNames, ", "))
			}
		}

		// Show disabled packs after enabled ones.
		for _, pe := range loaded.profileCfg.Packs {
			if !enabledPacks[pe.Name] {
				fmt.Fprintf(g.Stdout, "  %s (disabled)\n", pe.Name)
			}
		}
	}

	return nil
}
