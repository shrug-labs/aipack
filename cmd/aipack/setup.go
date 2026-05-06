package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// SetupCmd turns profile refs into a short fix checklist.
type SetupCmd struct {
	Profile string `arg:"" optional:"" help:"Profile name (default: sync-config defaults.profile, then 'default')" predictor:"profile"`
}

func (c *SetupCmd) Help() string {
	return `Shows the missing params and env vars needed before sync.

Examples:
  aipack setup
  aipack setup production`
}

func (c *SetupCmd) Run(_ context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	result, err := app.ProfileRefs(app.ProfileRefsRequest{ConfigDir: cfgDir, ProfileName: c.Profile})
	if err != nil {
		if errors.Is(err, config.ErrProfileNoPacks) {
			fmt.Fprintln(g.Stdout, "No packs in profile yet. Run `aipack pack install <source> --add` to add one.")
			return nil
		}
		return err
	}
	printSetup(g.Stdout, cfgDir, result)
	return nil
}

func printSetup(w io.Writer, cfgDir string, result app.ProfileRefsResult) {
	fmt.Fprintf(w, "Profile: %s\n", result.Profile)
	missingParams := uniqueMissingRefs(result.Refs, domain.RefKindParam)
	missingEnv := uniqueMissingRefs(result.Refs, domain.RefKindEnv)
	if len(missingParams) == 0 && len(missingEnv) == 0 {
		fmt.Fprintln(w, "All required params and env vars are set.")
		fmt.Fprintln(w, "\nNext")
		fmt.Fprintln(w, "  aipack sync")
		return
	}
	if len(missingParams) > 0 {
		fmt.Fprintln(w, "\nMissing params")
		for _, ref := range missingParams {
			fmt.Fprintf(w, "  %-22s %s\n", ref.Name, setupTarget(ref))
		}
		fmt.Fprintln(w, "\nSet params")
		for _, ref := range missingParams {
			fmt.Fprintf(w, "  aipack profile set-param %s %s <value>\n", result.Profile, ref.Name)
		}
	}
	if len(missingEnv) > 0 {
		fmt.Fprintln(w, "\nMissing env")
		for _, ref := range missingEnv {
			fmt.Fprintf(w, "  %-22s %s\n", ref.Name, setupTarget(ref))
		}
		fmt.Fprintln(w, "\nSet env")
		for _, ref := range missingEnv {
			fmt.Fprintf(w, "  aipack config env set %s <value>\n", ref.Name)
		}
		fmt.Fprintf(w, "  # writes %s\n", config.DotEnvPath(cfgDir))
	}
	fmt.Fprintln(w, "\nNext")
	fmt.Fprintln(w, "  aipack sync")
}

func uniqueMissingRefs(refs []app.ProfileRef, kind string) []app.ProfileRef {
	seen := map[string]struct{}{}
	var out []app.ProfileRef
	for _, ref := range refs {
		if ref.Kind != kind || ref.Status != "missing" {
			continue
		}
		if _, ok := seen[ref.Name]; ok {
			continue
		}
		seen[ref.Name] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func setupTarget(ref app.ProfileRef) string {
	if ref.Target != "" {
		return ref.Target
	}
	if ref.Pack != "" {
		return ref.Pack
	}
	return ref.Location
}

func shellCommand(args ...string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuoteArg(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	safe := true
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("_@%+=:,./-", r):
		default:
			safe = false
		}
		if !safe {
			break
		}
	}
	if safe {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}
