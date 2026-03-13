package main

import (
	"encoding/json"
	"fmt"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

type ValidateCmd struct {
	PackRoot string `arg:"" help:"Pack root directory to validate" type:"path"`
	JSON     bool   `help:"Emit machine-readable JSON report instead of human-readable text" name:"json"`
}

func (c *ValidateCmd) Help() string {
	return `Read-only validation command for a single pack source tree. Checks pack
structure and content policy without installing or syncing anything.

Exit code 0 if validation passes, 1 if findings are reported.

Examples:
  # Validate a local pack source tree
  aipack validate ./my-pack

  # Emit machine-readable JSON output
  aipack validate ./my-pack --json

See also: doctor, pack install`
}

func (c *ValidateCmd) Run(g *Globals) error {
	rep := app.RunPackValidate(app.PackValidateRequest{PackRoot: c.PackRoot})
	if c.JSON {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return err
		}
		_, _ = g.Stdout.Write(append(b, '\n'))
	} else {
		if rep.OK && len(rep.Findings) == 0 {
			fmt.Fprintln(g.Stdout, "validate OK")
		} else if rep.OK {
			fmt.Fprintln(g.Stdout, "validate OK (with warnings)")
			printFindings(g, rep.Findings)
		} else {
			fmt.Fprintln(g.Stderr, "validate FAILED")
			printFindings(g, rep.Findings)
		}
	}
	if rep.OK {
		return nil
	}
	return ExitError{Code: cmdutil.ExitFail}
}

func printFindings(g *Globals, findings []config.Finding) {
	for _, f := range findings {
		if f.Severity == config.FindingSeverityWarning {
			fmt.Fprintf(g.Stderr, "- [warning] %s\n", f)
		} else {
			fmt.Fprintf(g.Stderr, "- %s\n", f)
		}
		if f.Remediation != "" {
			fmt.Fprintf(g.Stderr, "  remediation: %s\n", f.Remediation)
		}
	}
}
