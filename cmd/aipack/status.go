package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

type StatusCmd struct {
	Profile     string `help:"Profile name (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
	ProfilePath string `help:"Direct path to a profile YAML file (overrides --profile)" name:"profile-path" type:"path"`
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

func (c *StatusCmd) Run(ctx context.Context, g *Globals) error {
	loaded, code := loadProfile(c.Profile, c.ProfilePath, g.ConfigDir, g.Stderr)
	if code >= 0 {
		return ExitError{Code: code}
	}
	cmdutil.PrintWarnings(g.Stderr, loaded.warnings)

	resolvedPacks, settingsPacks, err := config.ResolveProfile(loaded.profileCfg, loaded.profilePath, loaded.configDir)
	if err != nil {
		return err
	}

	es := app.BuildEcosystemStatus(resolvedPacks, settingsPacks, loaded.profileName, loaded.profilePath, loaded.configDir)

	if c.JSON {
		return cmdutil.WriteJSON(g.Stdout, es)
	}

	printEcosystemStatus(es, g.Stdout)
	return nil
}

func printEcosystemStatus(es *app.EcosystemStatus, w io.Writer) {
	fmt.Fprintf(w, "profile: %s (%s)\n", es.Profile, es.ProfilePath)
	if len(es.SettingsPacks) > 0 {
		fmt.Fprintf(w, "settings: %s\n", strings.Join(es.SettingsPacks, ", "))
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
		if summary := statusContentSummary(p); summary != "" {
			fmt.Fprintf(w, "     %s\n", summary)
		}
	}
	fmt.Fprintf(w, "\ntotals: %s\n", statusTotals(es))

	if len(config.HomeDir()) > 0 {
		if es.ConfigDir != "" {
			fmt.Fprintf(w, "config: %s\n", es.ConfigDir)
		}
	}
}

func statusContentSummary(p app.PackStatus) string {
	c := app.ContentCounts{
		Rules: p.Rules, Skills: p.Skills,
		Workflows: p.Workflows, Agents: p.Agents,
		MCP: p.MCPServers,
	}
	if c.IsZero() {
		return ""
	}
	return c.String()
}

func statusTotals(es *app.EcosystemStatus) string {
	c := app.ContentCounts{
		Rules: es.TotalRules, Skills: es.TotalSkills,
		Workflows: es.TotalWorkflows, Agents: es.TotalAgents,
		MCP: es.TotalMCP,
	}
	if c.IsZero() {
		return "0 resources"
	}
	return c.String()
}
