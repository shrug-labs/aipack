package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/engine"
)

type DoctorCmd struct {
	ProfilePath string `help:"Direct path to a profile YAML file" name:"profile-path" type:"path"`
	Profile     string `help:"Profile name (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
	JSON        bool   `help:"Emit machine-readable JSON report instead of human-readable text" name:"json"`
	Fix         bool   `help:"Repair safe configuration and managed-file issues" name:"fix"`
}

func (c *DoctorCmd) Help() string {
	return `Checks configuration, profiles, pack manifests, required environment values,
MCP executable paths and managed-file records.

Without --fix, the command is read-only. With --fix, it auto-repairs safe
issues: removes orphaned ledger entries and fills missing source-pack metadata.
Interrupted pack changes are recovered before repairs. If another operation is
running, wait for it to finish and retry.

Exit code 0 if no critical check fails, 1 on a critical failure.

Examples:
  # Run default checks
  aipack doctor

  # Auto-fix safe issues
  aipack doctor --fix

  # Check a specific profile
  aipack doctor --profile prod

  # Machine-readable JSON output
  aipack doctor --profile default --json

See also: init, sync, status`
}

func (c *DoctorCmd) Run(ctx context.Context, g *Globals) error {
	eng := engine.New(nil, nil)
	rep := app.RunDoctor(ctx, eng, app.DoctorRequest{
		ConfigDir:   g.ConfigDir,
		ProfilePath: c.ProfilePath,
		ProfileName: c.Profile,
		Home:        config.HomeDir(),
		Fix:         c.Fix,
		Version:     version,
	})

	if c.JSON {
		if err := cmdutil.WriteJSON(g.Stdout, rep); err != nil {
			return err
		}
		if rep.OK {
			return nil
		}
		return ExitError{Code: cmdutil.ExitFail}
	}
	printDoctorHuman(rep, g.Stdout, g.Stderr)
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
		case "mcp_refs_present":
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
