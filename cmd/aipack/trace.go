package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

type TraceCmd struct {
	Type        string  `arg:"" help:"Resource type or exact resource name" predictor:"resource"`
	Name        string  `arg:"" optional:"" help:"Resource name when type is provided" predictor:"resource"`
	Profile     string  `help:"Profile name (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
	ProfilePath string  `help:"Direct path to a profile YAML file" name:"profile-path" type:"path"`
	Scope       string  `help:"Scope: project|global (default: sync-config defaults.scope, then 'global')" default:"default" enum:"project,global,default"`
	ProjectDir  *string `help:"Project directory for scope=project" name:"project-dir" type:"path"`
	Harness     string  `help:"Filter to specific harness" name:"harness" predictor:"harness"`
	JSON        bool    `help:"Machine-readable JSON output" name:"json"`
}

func (c *TraceCmd) Help() string {
	return `Traces a pack resource from source through the sync pipeline to its
harness destination(s). Shows the pack source path, planned destination
per harness, and on-disk state (create, identical, managed, conflict,
untracked, error). If the resource is installed but inactive, reports the
profile blocker and next commands instead of destinations.

Useful for debugging content routing issues — "why didn't my rule appear?"
or "which pack is this agent coming from?"

Examples:
  # Trace a resource by exact name
  aipack trace anti-slop

  # Trace a rule
  aipack trace rule anti-slop

  # Trace a skill with JSON output
  aipack trace skill deep-research --json

  # Trace an MCP server
  aipack trace mcp atlassian

  # Trace within a specific harness
  aipack trace rule user-baseline --harness claudecode

See also: sync --dry-run --verbose, status`
}

func (c *TraceCmd) Validate() error {
	if c.Scope == string(domain.ScopeGlobal) && c.ProjectDir != nil {
		return fmt.Errorf("--project-dir is not valid for --scope global")
	}
	if c.Name != "" {
		if _, err := normalizeTraceType(c.Type); err != nil {
			return err
		}
	}
	return nil
}

func (c *TraceCmd) Run(ctx context.Context, g *Globals) error {
	loaded, exitCode := loadProfileAllowNoEnabledPacks(c.Profile, c.ProfilePath, g.ConfigDir, g.Stderr)
	if exitCode >= 0 {
		return ExitError{Code: exitCode}
	}

	resType, resName, diagnostic, ok, err := resolveTraceArgs(loaded, c.Type, c.Name, g.Stderr)
	if err != nil {
		return err
	}
	if !ok {
		return ExitError{Code: cmdutil.ExitFail}
	}

	result, err := c.runResolved(ctx, g, loaded, resType, resName, diagnostic)
	if err != nil {
		return err
	}

	if c.JSON {
		if err := cmdutil.WriteJSON(g.Stdout, result); err != nil {
			return err
		}
		if !result.Found {
			return ExitError{Code: cmdutil.ExitFail}
		}
		return nil
	}

	printTraceHuman(result, g)
	if !result.Found {
		return ExitError{Code: cmdutil.ExitFail}
	}
	return nil
}

func (c *TraceCmd) runResolved(ctx context.Context, g *Globals, loaded loadedProfile, resType, resName string, diagnostic *app.TraceDiagnostic) (app.TraceResult, error) {
	scope, err := cmdutil.ResolveScopeDefault(c.Scope, loaded.syncCfg.Defaults.Scope)
	if err != nil {
		return app.TraceResult{}, err
	}
	if err := validateProjectDirForScope(scope, c.ProjectDir); err != nil {
		fmt.Fprintln(g.Stderr, "ERROR:", err)
		return app.TraceResult{}, ExitError{Code: cmdutil.ExitUsage}
	}

	projectDir, err := os.Getwd()
	if err != nil {
		return app.TraceResult{}, err
	}
	if scope == domain.ScopeProject && c.ProjectDir != nil {
		projectDir, err = filepath.Abs(*c.ProjectDir)
		if err != nil {
			return app.TraceResult{}, err
		}
	}

	hs, err := cmdutil.ResolveHarnessesOptional(c.Harness, loaded.syncCfg.Defaults.Harnesses)
	if err != nil {
		return app.TraceResult{}, err
	}

	eng := engine.New(nil, nil)
	return app.RunTrace(ctx, eng, loaded.profile, app.TraceRequest{
		TargetSpec: app.TargetSpec{
			ConfigDir:  loaded.configDir,
			Scope:      scope,
			ProjectDir: projectDir,
			Harnesses:  hs,
			Home:       config.HomeDir(),
			Namespaced: loaded.syncCfg.Defaults.Namespaced,
		},
		ProfileName:   loaded.profileName,
		ProfileConfig: loaded.profileCfg,
		ResourceType:  resType,
		ResourceName:  resName,
		Diagnostic:    diagnostic,
	}, g.Registry)
}

func printTraceHuman(result app.TraceResult, g *Globals) {
	if !result.Found {
		fmt.Fprintf(g.Stderr, "%s %q not found in active profile\n", result.ResourceType, result.ResourceName)
		printTraceBlockersAndRemediation(result, g.Stderr)
		return
	}

	fmt.Fprintf(g.Stdout, "%s: %s\n", result.ResourceType, result.ResourceName)
	if result.ProfileState != "" && result.ProfileState != app.TraceProfileStateActive {
		fmt.Fprintf(g.Stdout, "  profile state: %s\n", result.ProfileState)
	}
	if result.Source != nil {
		fmt.Fprintf(g.Stdout, "  pack: %s\n", result.Source.Pack)
		if result.Source.SourcePath != "" {
			fmt.Fprintf(g.Stdout, "  source: %s\n", result.Source.SourcePath)
		}
	}

	if len(result.Destinations) == 0 {
		fmt.Fprintln(g.Stdout, "  destinations: (none planned)")
		printTraceBlockersAndRemediation(result, g.Stdout)
		return
	}

	fmt.Fprintln(g.Stdout, "  destinations:")
	for _, d := range result.Destinations {
		harness := d.Harness
		if harness == "" {
			harness = "?"
		}
		fmt.Fprintf(g.Stdout, "    %s: %s [%s]\n", harness, d.Path, d.State)
	}
}

func printTraceBlockersAndRemediation(result app.TraceResult, w io.Writer) {
	if len(result.Blockers) > 0 {
		fmt.Fprintln(w, "  blockers:")
		for _, blocker := range result.Blockers {
			fmt.Fprintf(w, "    - %s\n", blocker)
		}
	}
	if len(result.Remediation) > 0 {
		fmt.Fprintln(w, "  remediation:")
		for _, cmd := range result.Remediation {
			fmt.Fprintf(w, "    %s\n", cmd)
		}
	}
}

func resolveTraceArgs(loaded loadedProfile, first, second string, stderr io.Writer) (string, string, *app.TraceDiagnostic, bool, error) {
	if second != "" {
		resType, err := normalizeTraceType(first)
		return resType, second, nil, err == nil, err
	}
	name := strings.TrimSpace(first)
	candidates := app.FindTraceCandidates(loaded.profile, name)
	switch len(candidates) {
	case 0:
		inactive := app.FindTraceDiagnosticCandidates(loaded.profileCfg, loaded.configDir, name, loaded.profileName)
		switch len(inactive) {
		case 0:
			fmt.Fprintf(stderr, "resource %q not found in active profile\n", name)
			fmt.Fprintf(stderr, "Try: %s\n", traceSearchShellCommand(name))
			return "", "", nil, false, nil
		case 1:
			diagnostic := inactive[0]
			return diagnostic.Candidate.ResourceType, diagnostic.Candidate.ResourceName, &diagnostic, true, nil
		default:
			printTraceDiagnostics(stderr, name, inactive)
			return "", "", nil, false, nil
		}
	case 1:
		return candidates[0].ResourceType, candidates[0].ResourceName, nil, true, nil
	default:
		printTraceCandidates(stderr, name, candidates)
		return "", "", nil, false, nil
	}
}

func normalizeTraceType(raw string) (string, error) {
	cat, ok := domain.ParseSingularLabel(strings.ToLower(strings.TrimSpace(raw)))
	if !ok || !app.IsTraceableCategory(cat) {
		return "", fmt.Errorf("invalid resource type %q (valid: rule, agent, workflow, skill, hook, plugin, mcp)", raw)
	}
	if cat == domain.CategoryMCP {
		return "mcp", nil
	}
	return strings.ToLower(cat.SingularLabel()), nil
}

func printTraceCandidates(w io.Writer, name string, candidates []app.TraceCandidate) {
	fmt.Fprintf(w, "Multiple resources named %q:\n", name)
	for _, candidate := range candidates {
		state := ""
		if candidate.ProfileState != "" {
			state = " state=" + string(candidate.ProfileState)
		}
		fmt.Fprintf(w, "  %-8s pack=%s%s\n", candidate.ResourceType, candidate.Pack, state)
	}
	fmt.Fprintln(w, "\nRun one explicit command:")
	for _, candidate := range candidates {
		fmt.Fprintf(w, "  %s\n", traceExplicitShellCommand(candidate.ResourceType, candidate.ResourceName))
	}
}

func printTraceDiagnostics(w io.Writer, name string, diagnostics []app.TraceDiagnostic) {
	candidates := make([]app.TraceCandidate, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		candidates = append(candidates, diagnostic.Candidate)
	}
	printTraceCandidates(w, name, candidates)
}

func traceSearchShellCommand(name string) string {
	return cmdutil.ShellCommandWithOperand([]string{"aipack", "search"}, name)
}

func traceExplicitShellCommand(resType, name string) string {
	return cmdutil.ShellCommandWithOperand([]string{"aipack", "trace", resType}, name)
}
