package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/willabides/kongplete"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
	ccharness "github.com/shrug-labs/aipack/internal/harness/claudecode"
	clharness "github.com/shrug-labs/aipack/internal/harness/cline"
	cxharness "github.com/shrug-labs/aipack/internal/harness/codex"
	ocharness "github.com/shrug-labs/aipack/internal/harness/opencode"
)

// cliCore contains commands shared by all builds.
type cliCore struct {
	ConfigDir string `help:"Config directory (default: %APPDATA%\\aipack on Windows; ~/.config/aipack elsewhere)" name:"config-dir" type:"path"`

	Init   InitCmd   `cmd:"" group:"Setup:" help:"Create default sync-config and profile files"`
	Doctor DoctorCmd `cmd:"" group:"Setup:" help:"Run preflight checks on config, packs, and MCP servers"`

	Sync    SyncCmd    `cmd:"" group:"Sync/Save:" help:"Apply pack content to harness locations"`
	Render  RenderCmd  `cmd:"" group:"Sync/Save:" help:"Render pack content to a standalone output directory"`
	Save    SaveCmd    `cmd:"" group:"Sync/Save:" help:"Save harness content back to source packs"`
	Clean   CleanCmd   `cmd:"" group:"Sync/Save:" help:"Remove all managed files from harness locations"`
	Restore RestoreCmd `cmd:"" group:"Sync/Save:" help:"Restore settings files from pre-sync or base cache"`

	Install  InstallCmd  `cmd:"" hidden:"" help:"Alias for 'pack install'"`
	Pack     PackCmd     `cmd:"" group:"Pack Management:" help:"Manage installed packs"`
	Registry RegistryCmd `cmd:"" group:"Pack Management:" help:"Browse and search the pack registry"`
	Profile  ProfileCmd  `cmd:"" group:"Profile Management:" help:"Manage sync profiles"`

	MCP    MCPCmd    `cmd:"" group:"Discovery:" help:"MCP server operations (inspect tools, discover inventory)"`
	Search SearchCmd `cmd:"" group:"Discovery:" help:"Search the pack index by name, tags, role, or kind"`
	Query  QueryCmd  `cmd:"" group:"Discovery:" help:"Run raw SQL against the pack index"`
	Status StatusCmd `cmd:"" group:"Discovery:" help:"Show ecosystem status: profile, packs, and content vectors"`
	Trace  TraceCmd  `cmd:"" group:"Discovery:" help:"Trace a resource from pack source to harness destination"`
	Manage ManageCmd `cmd:"" group:"Profile Management:" help:"Interactive TUI for managing profiles and packs"`

	Prompt PromptCmd `cmd:"" group:"Prompts:" help:"Browse and copy prompts from installed packs"`

	Update  UpdateCmd  `cmd:"" group:"Other:" help:"Update aipack to the latest version"`
	Version VersionCmd `cmd:"" group:"Other:" help:"Print the aipack version"`
}

// Globals holds injected IO for testability.
type Globals struct {
	ConfigDir string
	Stdout    io.Writer
	Stderr    io.Writer
	Stdin     io.Reader
	StdinTTY  bool
	Registry  *harness.Registry
}

// ExitError signals a specific exit code from a Run() method.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit code %d", e.Code) }

// exitPanic is used to catch Kong's internal Exit() calls (e.g. for --help).
type exitPanic struct{ code int }

func validateProjectDirForScope(scope domain.Scope, projectDir *string) error {
	if scope == domain.ScopeGlobal && projectDir != nil {
		return fmt.Errorf("--project-dir is not valid for effective scope global")
	}
	return nil
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY bool, extraOpts ...kong.Option) int {
	goCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Once the first SIGINT/SIGTERM has cancelled the context, restore
	// the default signal handler so a second ctrl-C forces termination.
	// Without this, signal.NotifyContext keeps diverting subsequent
	// signals to a no-op (the context is already cancelled), leaving the
	// user no way to abort a stuck operation that's not respecting ctx
	// (e.g. an MCP probe whose subprocess won't die promptly).
	go func() {
		<-goCtx.Done()
		stop()
	}()

	globals := &Globals{
		Stdout:   stdout,
		Stderr:   stderr,
		Stdin:    stdin,
		StdinTTY: stdinTTY,
		Registry: harness.NewRegistry(
			ccharness.Harness{}, clharness.Harness{}, cxharness.Harness{}, ocharness.Harness{},
		),
	}

	// Route bare `--version` / `-V` to the version subcommand. Can't register
	// them as Kong flags because `pack install` / `pack update` already have
	// their own --version and Kong propagates root flags into subcommands.
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-V") {
		args = []string{"version"}
	}

	cli := &CLI{}
	opts := []kong.Option{
		kong.Name("aipack"),
		kong.Description("AI agent harness pack manager"),
		kong.Writers(stdout, stderr),
		kong.Exit(func(code int) { panic(exitPanic{code: code}) }),
		kong.Bind(globals),
		kong.BindTo(goCtx, (*context.Context)(nil)),
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
		kongplete.Complete(parser,
			kongplete.WithPredictors(completionPredictors()),
		)
		kctx, err := parser.Parse(args)
		if err != nil {
			parser.FatalIfErrorf(err)
		}
		globals.ConfigDir = cli.ConfigDir
		err = kctx.Run(globals)
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
