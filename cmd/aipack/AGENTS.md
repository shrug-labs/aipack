# CLI Layer

Kong-based CLI adapters. Each command is a struct with a `Run(g *Globals) error` method. Business logic lives in `internal/app/` — CLI adapters parse flags and format output, nothing more.

## Globals

Single struct injected at startup via `kong.Bind()`:

```go
type Globals struct {
    Stdout   io.Writer
    Stderr   io.Writer
    Stdin    io.Reader
    StdinTTY bool
    Registry *harness.Registry
}
```

All commands receive it in `Run()`. Use `g.Stdout`/`g.Stderr` — never `os.Stdout`. The Registry is pre-populated with all four harness adapters in `cli.go:run()`.

## Adding a command

1. Create `<command>.go` with a Kong struct and `Run(g *Globals) error`
2. Add it to `cliCore` in `cli.go` with Kong tags for grouping and help text
3. Flag validation goes in `Validate() error` (Kong calls before Run)
4. For profile/scope/harness flags: reuse `loadProfile()` from `profile_load.go` and `cmdutil.ResolveHarnesses()`/`cmdutil.ResolveScope()` — don't reimplement the resolution chain
5. Call `app.Run*()` for business logic, format results to `g.Stdout`
6. Return `ExitError{Code: cmdutil.ExitFail}` for non-zero exit codes
7. Add `runApp()`-based tests (see Testing below)

## Shared flag patterns

**Profile loading** is centralized in `profile_load.go`. The resolution chain — `--profile-path` → `--profile` → sync-config default → `"default"` — is shared by sync, save, status, trace, doctor, and other commands. Changes here have wide blast radius.

**Scope and harness resolution** is in `internal/cmdutil/inputs.go`. Use `ResolveScope()` and `ResolveHarnesses()` rather than parsing these flags manually.

**`--json` output convention**: human-readable output goes to `g.Stderr`, JSON to `g.Stdout`. This keeps stdout machine-parseable. Follow the pattern in `status.go` or `trace.go`.

## Exit codes

| Code | Constant | Meaning |
|------|----------|---------|
| 0 | `cmdutil.ExitOK` | Success |
| 1 | `cmdutil.ExitFail` | Runtime error |
| 2 | `cmdutil.ExitUsage` | Bad flags or arguments |

Kong's `--help` exits via `exitPanic{code: 0}` caught in `run()`. Return `ExitError{Code: n}` from `Run()` to set a specific exit code.

## Testing

Two helpers in `cli_test_helpers_test.go`:

```go
func runApp(t *testing.T, args ...string) (stdout, stderr string, code int)
func runAppWithInput(t *testing.T, input string, stdinTTY bool, args ...string) (stdout, stderr string, code int)
```

These invoke the full `run()` function with buffer I/O — full CLI parse-and-execute path. Every new command needs `runApp()`-based tests. For commands with `--json`, parse stdout as JSON and assert on the shape. Tests that exercise business logic without CLI parsing go in `internal/app/`.

## TUI (`tui/`)

Bubbletea app for `aipack manage`. Tab-based layout: Profiles, Packs, Sync, Save, Search, Config.

Key patterns:
- `Model` holds all state. `Update(msg) (Model, Cmd)` handles events. `View() string` renders.
- `msg.go` defines all message types. `async.go` wraps long operations as Bubbletea Cmds.
- Tests use `teatest.NewModel()` and assert on rendered output.
- TUI delegates to `internal/app/` for all business logic — no direct config/harness access.
