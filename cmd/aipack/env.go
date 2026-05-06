package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

// EnvCmd manages the config-dir .env file that backs `{env:*}` resolution.
type EnvCmd struct {
	List  EnvListCmd  `cmd:"" help:"List keys in the config-dir .env file"`
	Get   EnvGetCmd   `cmd:"" help:"Print the value for one key"`
	Set   EnvSetCmd   `cmd:"" help:"Set or replace a KEY=value entry"`
	Unset EnvUnsetCmd `cmd:"" help:"Remove a KEY entry"`
	Path  EnvPathCmd  `cmd:"" help:"Print the absolute path of the config-dir .env file"`
	Edit  EnvEditCmd  `cmd:"" help:"Open the config-dir .env file in $EDITOR"`
}

// EnvListCmd lists the keys present in the config-dir .env. Values are masked
// by default; --show or --json with --show reveals them. Unset keys never
// appear — use `aipack setup` to see what `{env:*}` references the
// active profile expects.
type EnvListCmd struct {
	Show bool `help:"Show values alongside keys" name:"show"`
	JSON bool `help:"Emit machine-readable JSON" name:"json"`
}

func (c *EnvListCmd) Help() string {
	return `Lists keys present in the config-dir .env file. By default values are
masked so the listing is screen-share safe; use --show to reveal them or
'aipack config env get <key>' to print one value.

Examples:
  aipack config env list
  aipack config env list --show
  aipack config env list --json`
}

func (c *EnvListCmd) Run(_ context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	result, err := app.EnvList(cfgDir, c.Show)
	if err != nil {
		return err
	}
	if c.JSON {
		return cmdutil.WriteJSON(g.Stdout, result)
	}
	fmt.Fprintf(g.Stdout, "Path: %s\n", result.Path)
	if len(result.Entries) == 0 {
		fmt.Fprintln(g.Stdout, "No keys set.")
		fmt.Fprintln(g.Stdout, "Use 'aipack config env set KEY VALUE' to add one.")
		return nil
	}
	for _, e := range result.Entries {
		if c.Show {
			fmt.Fprintf(g.Stdout, "  %s=%s\n", e.Key, e.Value)
		} else {
			fmt.Fprintf(g.Stdout, "  %s  (%d chars)\n", e.Key, e.Length)
		}
	}
	if !c.Show {
		fmt.Fprintln(g.Stdout, "\nRun with --show to reveal values, or 'aipack config env get KEY'.")
	}
	return nil
}

// EnvGetCmd prints the value for a single key. Useful for verifying a value
// after `env set` without dumping every secret to the terminal.
type EnvGetCmd struct {
	Key string `arg:"" help:"Env variable key"`
}

func (c *EnvGetCmd) Run(_ context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	value, ok, err := app.EnvGet(cfgDir, c.Key)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintf(g.Stderr, "%s is not set in %s\n", c.Key, app.EnvPath(cfgDir))
		return ExitError{Code: cmdutil.ExitFail}
	}
	fmt.Fprintln(g.Stdout, value)
	return nil
}

// EnvSetCmd writes (or replaces) a KEY=value entry in the .env file.
type EnvSetCmd struct {
	Key   string `arg:"" help:"Env variable key"`
	Value string `arg:"" help:"Env variable value"`
}

func (c *EnvSetCmd) Help() string {
	return `Writes a KEY=value entry to the config-dir .env file. Existing
entries are replaced in place; new entries are appended. Comments, blank
lines, and unrelated entries are preserved.

Examples:
  aipack config env set API_TOKEN abc123
  aipack config env set PROXY_URL https://proxy.example.com`
}

func (c *EnvSetCmd) Run(_ context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	if err := app.EnvSet(cfgDir, c.Key, c.Value); err != nil {
		return err
	}
	fmt.Fprintf(g.Stdout, "Set %s in %s\n", strings.TrimSpace(c.Key), app.EnvPath(cfgDir))
	return nil
}

// EnvUnsetCmd removes a KEY entry from the .env file. Missing keys are a
// no-op rather than an error so scripts can be idempotent.
type EnvUnsetCmd struct {
	Key string `arg:"" help:"Env variable key"`
}

func (c *EnvUnsetCmd) Run(_ context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	if err := app.EnvUnset(cfgDir, c.Key); err != nil {
		return err
	}
	fmt.Fprintf(g.Stdout, "Unset %s in %s\n", strings.TrimSpace(c.Key), app.EnvPath(cfgDir))
	return nil
}

// EnvPathCmd prints the absolute path of the config-dir .env file.
type EnvPathCmd struct{}

func (c *EnvPathCmd) Run(_ context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	fmt.Fprintln(g.Stdout, app.EnvPath(cfgDir))
	return nil
}

// EnvEditCmd opens the config-dir .env file in $EDITOR (or VISUAL, or the
// platform default). The editor inherits the parent's stdio so it's a
// drop-in replacement for `vi ~/.config/aipack/.env`.
type EnvEditCmd struct{}

func (c *EnvEditCmd) Run(_ context.Context, g *Globals) error {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return err
	}
	envPath := app.EnvPath(cfgDir)
	// EnsureConfigDir already makes the dir; create the .env file if missing
	// so the editor opens an existing buffer rather than a new-file prompt.
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		if err := os.WriteFile(envPath, nil, 0o600); err != nil {
			return fmt.Errorf("creating .env: %w", err)
		}
	}
	cmdName, args, err := resolveEnvEditor()
	if err != nil {
		return err
	}
	args = append(args, envPath)
	cmd := exec.Command(cmdName, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = g.Stdout
	cmd.Stderr = g.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor exited with error: %w", err)
	}
	return nil
}

// resolveEnvEditor mirrors the TUI's resolveEditorCommand contract for the
// CLI side. Honors $EDITOR / $VISUAL, falls back to vi (notepad on Windows).
func resolveEnvEditor() (string, []string, error) {
	editor, err := cmdutil.ResolveEditorCommand(os.Getenv("EDITOR"), os.Getenv("VISUAL"), runtime.GOOS)
	if err != nil {
		return "", nil, err
	}
	path, err := exec.LookPath(editor.Name)
	if err != nil {
		if editor.Source == "default" {
			return "", nil, fmt.Errorf("default editor %q not found; set $EDITOR or $VISUAL", editor.Name)
		}
		return "", nil, fmt.Errorf("%s=%q is not available: %w; set $EDITOR or $VISUAL to an installed editor", editor.Source, editor.Raw, err)
	}
	return path, editor.Args, nil
}
