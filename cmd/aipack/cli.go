package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/alecthomas/kong"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/harness"
	ccharness "github.com/shrug-labs/aipack/internal/harness/claudecode"
	clharness "github.com/shrug-labs/aipack/internal/harness/cline"
	cxharness "github.com/shrug-labs/aipack/internal/harness/codex"
	ocharness "github.com/shrug-labs/aipack/internal/harness/opencode"
)

// cliCore contains commands shared by all builds.
type cliCore struct {
	Init     InitCmd     `cmd:"" group:"Setup:" help:"Create default sync-config and profile files"`
	Doctor   DoctorCmd   `cmd:"" group:"Setup:" help:"Run preflight checks on config, packs, and MCP servers"`
	Validate ValidateCmd `cmd:"" group:"Setup:" help:"Validate a pack source tree"`

	Sync   SyncCmd   `cmd:"" group:"Sync/Save:" help:"Apply pack content to harness locations"`
	Render RenderCmd `cmd:"" group:"Sync/Save:" help:"Render pack content to a standalone output directory"`
	Save   SaveCmd   `cmd:"" group:"Sync/Save:" help:"Save harness content back to source packs"`
	Clean  CleanCmd  `cmd:"" group:"Sync/Save:" help:"Remove all managed files from harness locations"`

	Install  InstallCmd  `cmd:"" group:"Pack Management:" help:"Install a pack from a local path, URL, or registry name"`
	Pack     PackCmd     `cmd:"" group:"Pack Management:" help:"Manage installed packs"`
	Registry RegistryCmd `cmd:"" group:"Pack Management:" help:"Browse and search the pack registry"`
	Profile  ProfileCmd  `cmd:"" group:"Profile Management:" help:"Manage sync profiles"`

	Search SearchCmd `cmd:"" group:"Discovery:" help:"Search the pack index by name, tags, role, or kind"`
	Query  QueryCmd  `cmd:"" group:"Discovery:" help:"Run raw SQL against the pack index"`
	Manage ManageCmd `cmd:"" group:"Profile Management:" help:"Interactive TUI for managing profiles and packs"`

	Prompt PromptCmd `cmd:"" group:"Prompts:" help:"Browse and copy prompts from installed packs"`

	Version VersionCmd `cmd:"" group:"Other:" help:"Print the aipack version"`
}

// Globals holds injected IO for testability.
type Globals struct {
	Stdout   io.Writer
	Stderr   io.Writer
	Stdin    io.Reader
	StdinTTY bool
	Registry *harness.Registry
}

// ExitError signals a specific exit code from a Run() method.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit code %d", e.Code) }

// exitPanic is used to catch Kong's internal Exit() calls (e.g. for --help).
type exitPanic struct{ code int }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY bool, extraOpts ...kong.Option) int {
	globals := &Globals{
		Stdout:   stdout,
		Stderr:   stderr,
		Stdin:    stdin,
		StdinTTY: stdinTTY,
		Registry: harness.NewRegistry(
			ccharness.Harness{}, clharness.Harness{}, cxharness.Harness{}, ocharness.Harness{},
		),
	}

	cli := &CLI{}
	opts := []kong.Option{
		kong.Name("aipack"),
		kong.Description("AI agent harness pack manager"),
		kong.Writers(stdout, stderr),
		kong.Exit(func(code int) { panic(exitPanic{code: code}) }),
		kong.Bind(globals),
		kong.UsageOnError(),
	}
	opts = append(opts, extraOpts...)

	var code int
	func() {
		defer func() {
			if r := recover(); r != nil {
				if ep, ok := r.(exitPanic); ok {
					code = ep.code
					return
				}
				panic(r) // re-panic for unexpected panics
			}
		}()

		parser, err := kong.New(cli, opts...)
		if err != nil {
			fmt.Fprintln(stderr, "ERROR:", err)
			code = cmdutil.ExitFail
			return
		}
		ctx, err := parser.Parse(args)
		if err != nil {
			parser.FatalIfErrorf(err)
		}
		err = ctx.Run(globals)
		if err == nil {
			code = cmdutil.ExitOK
			return
		}
		var exitErr ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.Code
			return
		}
		fmt.Fprintln(stderr, "ERROR:", err)
		code = cmdutil.ExitFail
	}()
	return code
}
