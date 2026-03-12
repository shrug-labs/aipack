package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
)

type DoctorCmd struct {
	ConfigDir   string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
	ProfilePath string `help:"Direct path to a profile YAML file" name:"profile-path" type:"path"`
	Profile     string `help:"Profile name (default: sync-config defaults.profile, then 'default')" name:"profile"`
	JSON        bool   `help:"Emit machine-readable JSON report instead of human-readable text" name:"json"`
	Status      bool   `help:"Show ecosystem status: profile, packs, content vectors, and totals" name:"status"`
	Fix         bool   `help:"Auto-fix safe issues (prune orphaned ledger entries, fill missing SourcePack)" name:"fix"`
}

func (c *DoctorCmd) Help() string {
	return `Diagnostic command. Runs preflight checks: sync-config exists and parses,
profile loads, pack manifests are valid, MCP server binary paths exist,
required environment variables are set, and ledger health.

Without --fix, the command is read-only. With --fix, it auto-repairs safe
issues: prunes orphaned ledger entries, fills missing SourcePack fields.

Exit code 0 if all checks pass, 1 if any check fails.

Examples:
  # Run default checks
  aipack doctor

  # Auto-fix safe issues
  aipack doctor --fix

  # Check a specific profile
  aipack doctor --profile prod

  # Machine-readable JSON output
  aipack doctor --profile default --json

  # Show ecosystem status (profile, packs, content vectors)
  aipack doctor --status

See also: init, sync`
}

func (c *DoctorCmd) Run(g *Globals) error {
	rep := app.RunDoctor(app.DoctorRequest{
		ConfigDir:   c.ConfigDir,
		ProfilePath: c.ProfilePath,
		ProfileName: c.Profile,
		Home:        os.Getenv("HOME"),
		Status:      c.Status,
		Fix:         c.Fix,
	})

	if c.JSON {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return err
		}
		_, _ = g.Stdout.Write(append(b, '\n'))
		if rep.OK {
			return nil
		}
		return ExitError{Code: cmdutil.ExitFail}
	}
	printDoctorHuman(rep, g.Stdout, g.Stderr)
	if rep.Ecosystem != nil {
		printEcosystemStatus(rep.Ecosystem, g.Stdout)
	}
	if rep.OK {
		return nil
	}
	return ExitError{Code: cmdutil.ExitFail}
}

func printDoctorHuman(rep app.DoctorReport, stdout io.Writer, stderr io.Writer) {
	// Collect warnings separately — they don't affect overall OK status.
	var warnings []app.CheckResult
	for _, c := range rep.Checks {
		if c.Status == "warn" {
			warnings = append(warnings, c)
		}
	}

	if rep.OK {
		if len(warnings) == 0 {
			fmt.Fprintln(stdout, "doctor OK")
			return
		}
		fmt.Fprintln(stdout, "doctor OK (with warnings)")
	} else {
		fmt.Fprintln(stderr, "doctor FAILED")
	}
	for _, c := range rep.Checks {
		if c.Status == "pass" {
			continue
		}
		if c.Fixed {
			fmt.Fprintf(stdout, "- %s: %s [FIXED: %s]\n", c.Name, c.Message, c.FixAction)
			continue
		}
		fmt.Fprintf(stderr, "- %s: %s\n", c.Name, c.Message)
		switch c.Name {
		case "mcp_env_vars_present":
			if c.Details != nil {
				switch missing := c.Details["missing"].(type) {
				case []string:
					if len(missing) > 0 {
						fmt.Fprintf(stderr, "  missing env: %v\n", missing)
					}
				case []any:
					if len(missing) > 0 {
						fmt.Fprintf(stderr, "  missing env: %v\n", missing)
					}
				}
			}
		case "mcp_server_paths_exist":
			if c.Details != nil {
				switch failures := c.Details["failures"].(type) {
				case []map[string]any:
					if len(failures) > 0 {
						fmt.Fprintf(stderr, "  missing paths: %d\n", len(failures))
					}
				case []any:
					if len(failures) > 0 {
						fmt.Fprintf(stderr, "  missing paths: %d\n", len(failures))
					}
				}
			}
		case "packs_registered":
			if c.Details != nil {
				if unreg, ok := c.Details["unregistered"]; ok {
					fmt.Fprintf(stderr, "  unregistered: %v\n", unreg)
				}
			}
		case "pack_version_drift":
			if c.Details != nil {
				if drifted, ok := c.Details["drifted"]; ok {
					switch items := drifted.(type) {
					case []app.PackDrift:
						for _, d := range items {
							if d.OriginVersion != "" {
								fmt.Fprintf(stderr, "  %s (%s): %s -> %s\n", d.Name, d.Method, d.InstalledVersion, d.OriginVersion)
							} else if d.CurrentHash != "" {
								fmt.Fprintf(stderr, "  %s (%s): %s -> %s\n", d.Name, d.Method, d.InstalledHash, d.CurrentHash)
							}
						}
					case []any:
						fmt.Fprintf(stderr, "  drifted packs: %d\n", len(items))
					}
				}
			}
		}
		if strings.TrimSpace(c.Remediation) != "" {
			fmt.Fprintf(stderr, "  remediation: %s\n", c.Remediation)
		}
	}
}

func printEcosystemStatus(es *app.EcosystemStatus, w io.Writer) {
	fmt.Fprintf(w, "\nprofile: %s (%s)\n", es.Profile, es.ProfilePath)
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
}
