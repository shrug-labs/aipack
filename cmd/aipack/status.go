package main

import (
	"fmt"
	"io"
	"os"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

type StatusCmd struct {
	Profile     string `help:"Profile name (default: sync-config defaults.profile, then 'default')" name:"profile"`
	ProfilePath string `help:"Direct path to a profile YAML file (overrides --profile)" name:"profile-path" type:"path"`
	ConfigDir   string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	JSON        bool   `help:"Emit machine-readable JSON" name:"json"`
}

func (c *StatusCmd) Help() string {
	return `Shows ecosystem status: active profile, installed packs with content
inventories (rules, agents, workflows, skills, MCP servers), and totals.

Examples:
  # Show status for the default profile
  aipack status

  # Show status for a specific profile
  aipack status --profile production

  # Machine-readable JSON output
  aipack status --json

See also: doctor, profile show`
}

func (c *StatusCmd) Run(g *Globals) error {
	loaded, code := loadProfile(c.Profile, c.ProfilePath, c.ConfigDir, g.Stderr)
	if code >= 0 {
		return ExitError{Code: code}
	}
	cmdutil.PrintWarnings(g.Stderr, loaded.warnings)

	resolvedPacks, settingsPack, err := config.ResolveProfile(loaded.profileCfg, loaded.profilePath, loaded.configDir)
	if err != nil {
		return err
	}

	es := app.BuildEcosystemStatus(resolvedPacks, settingsPack, loaded.profileName, loaded.profilePath, loaded.configDir)

	if c.JSON {
		return cmdutil.WriteJSON(g.Stdout, es)
	}

	printEcosystemStatus(es, g.Stdout)
	return nil
}

func printEcosystemStatus(es *app.EcosystemStatus, w io.Writer) {
	fmt.Fprintf(w, "profile: %s (%s)\n", es.Profile, es.ProfilePath)
	if es.SettingsPack != "" {
		fmt.Fprintf(w, "settings: %s\n", es.SettingsPack)
	}
	fmt.Fprintf(w, "\npacks (%d):\n", len(es.Packs))
	for i, p := range es.Packs {
		settings := ""
		if p.Settings {
			settings = " (settings)"
		}
		ver := ""
		if p.Version != "" {
			ver = " v" + p.Version
		}
		fmt.Fprintf(w, "  %d. %s%s%s\n", i+1, p.Name, ver, settings)
		fmt.Fprintf(w, "     rules: %d  agents: %d  workflows: %d  skills: %d  mcp: %d\n",
			p.Rules, p.Agents, p.Workflows, p.Skills, p.MCPServers)
	}
	fmt.Fprintf(w, "\ntotals: %d rules, %d agents, %d workflows, %d skills, %d mcp servers\n",
		es.TotalRules, es.TotalAgents, es.TotalWorkflows, es.TotalSkills, es.TotalMCP)

	if len(os.Getenv("HOME")) > 0 {
		// Check for config dir in the status output.
		if es.ConfigDir != "" {
			fmt.Fprintf(w, "config: %s\n", es.ConfigDir)
		}
	}
}
