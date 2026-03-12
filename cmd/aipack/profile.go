package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

type ProfileCmd struct {
	Create ProfileCreateCmd `cmd:"" help:"Create a new empty profile"`
	Delete ProfileDeleteCmd `cmd:"" help:"Delete a profile from the config directory"`
	List   ProfileListCmd   `cmd:"" help:"List available profiles in the config directory"`
	Set    ProfileSetCmd    `cmd:"" help:"Set the active profile (defaults.profile in sync-config)"`
	Show   ProfileShowCmd   `cmd:"" help:"Show resolved configuration for a profile"`
}

func (c *ProfileCmd) Help() string {
	return `Manage sync profiles. Profiles define which packs, content, and settings to
sync to which harnesses.

Profiles are stored as YAML files under ~/.config/aipack/profiles/.`
}

// --- profile list ---

type ProfileListCmd struct {
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
}

func (c *ProfileListCmd) Help() string {
	return `Lists all .yaml files in ~/.config/aipack/profiles/. The default profile (from
sync-config defaults.profile, or "default") is marked with *.

Examples:
  # List profiles
  aipack profile list

  # Use a custom config directory
  aipack profile list --config-dir /path/to/config

See also: profile show, init`
}

func (c *ProfileListCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
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
		marker := " "
		if name == defaultProfile {
			marker = "*"
		}
		fmt.Fprintf(g.Stdout, "  %s %s\n", name, marker)
	}
	return nil
}

// --- profile set ---

type ProfileSetCmd struct {
	Name      string `arg:"" help:"Profile name to activate"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
}

func (c *ProfileSetCmd) Help() string {
	return `Sets the active profile by updating defaults.profile in sync-config.yaml.
The named profile must already exist under ~/.config/aipack/profiles/.

Examples:
  # Activate a profile
  aipack profile set my-team

See also: profile list, profile create`
}

func (c *ProfileSetCmd) Run(g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(c.ConfigDir, os.Getenv("HOME"), g.Stderr)
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
	return nil
}

// --- profile show ---

type ProfileShowCmd struct {
	Name        string `arg:"" optional:"" help:"Profile name (default: sync-config defaults.profile, then 'default')"`
	ConfigDir   string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	ProfilePath string `help:"Direct path to a profile YAML file (overrides name)" name:"profile-path" type:"path"`
	JSON        bool   `help:"Emit machine-readable JSON" name:"json"`
}

func (c *ProfileShowCmd) Help() string {
	return `Loads and fully resolves a profile, displaying its sources, packs (with content
lists for rules, agents, workflows, skills, MCP servers), and harness
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

func (c *ProfileShowCmd) Run(g *Globals) error {
	loaded, code := loadProfile(c.Name, c.ProfilePath, c.ConfigDir, g.Stderr)
	if code >= 0 {
		return ExitError{Code: code}
	}

	cmdutil.PrintWarnings(g.Stderr, loaded.warnings)

	if c.JSON {
		b, err := json.MarshalIndent(loaded.profile, "", "  ")
		if err != nil {
			return err
		}
		_, _ = g.Stdout.Write(append(b, '\n'))
		return nil
	}

	fmt.Fprintf(g.Stdout, "Profile: %s (from %s)\n", loaded.profileName, loaded.profilePath)

	if len(loaded.profileCfg.Packs) > 0 {
		// Index MCP servers by source pack for display.
		mcpByPack := map[string][]string{}
		for _, s := range loaded.profile.MCPServers {
			mcpByPack[s.SourcePack] = append(mcpByPack[s.SourcePack], s.Name)
		}
		for k := range mcpByPack {
			sort.Strings(mcpByPack[k])
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
