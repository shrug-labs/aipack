package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	markerBegin = "# begin aipack completions"
	markerEnd   = "# end aipack completions"
)

// InstallCompletionsCmd installs or removes shell completions.
type InstallCompletionsCmd struct {
	Uninstall bool   `help:"Remove completions from shell config"`
	Print     bool   `help:"Print the completion snippet to stdout instead of installing"`
	Yes       bool   `help:"Skip confirmation prompt"`
	RCFile    string `help:"Path to shell rc file (default: auto-detect)" name:"rc-file"`
}

// Run executes the install-completions command.
func (c *InstallCompletionsCmd) Run(ctx context.Context, g *Globals) error {
	shell := detectShell()
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable path: %w", err)
	}

	if c.Print {
		snippet := completionSnippet(shell, bin, "aipack")
		if snippet == "" {
			return fmt.Errorf("unsupported shell: %s", shell)
		}
		fmt.Fprint(g.Stdout, snippet)
		return nil
	}

	rcPath := c.RCFile
	if rcPath == "" {
		rcPath = defaultRCFile(shell)
		if rcPath == "" {
			return fmt.Errorf("could not determine rc file for shell %q; use --rc-file", shell)
		}
	}

	if c.Uninstall {
		if !requireConfirm(ctx, g, c.Yes, fmt.Sprintf("Remove completions from %s? [y/N] ", rcPath)) {
			return nil
		}
		removed, err := uninstallFromRC(rcPath)
		if err != nil {
			return err
		}
		if removed {
			fmt.Fprintf(g.Stdout, "Removed completions from %s\n", rcPath)
		} else {
			fmt.Fprintln(g.Stdout, "Completions not installed — nothing to remove")
		}
		return nil
	}

	// Check if already installed before prompting.
	existing, err := os.ReadFile(rcPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", rcPath, err)
	}
	if strings.Contains(string(existing), markerBegin) {
		fmt.Fprintf(g.Stdout, "Completions already installed in %s\n", rcPath)
		return nil
	}

	snippet := completionSnippet(shell, bin, "aipack")
	if snippet == "" {
		return fmt.Errorf("unsupported shell: %s", shell)
	}

	if !requireConfirm(ctx, g, c.Yes, fmt.Sprintf("Append to %s:\n\n%s\nProceed? [y/N] ", rcPath, snippet)) {
		return nil
	}

	installed, err := installToRC(rcPath, shell, bin, "aipack")
	if err != nil {
		return err
	}
	if installed {
		fmt.Fprintf(g.Stdout, "Installed completions in %s\nRestart your shell or run: source %s\n", rcPath, rcPath)
	}
	return nil
}

// requireConfirm handles the TTY check, prompt display, and user confirmation.
// Returns true to proceed, false if skipped (non-interactive) or declined.
func requireConfirm(ctx context.Context, g *Globals, yes bool, prompt string) bool {
	if yes {
		return true
	}
	if !g.StdinTTY {
		fmt.Fprintln(g.Stdout, "Skipped (non-interactive, use --yes to confirm).")
		return false
	}
	fmt.Fprint(g.Stderr, prompt)
	if !confirmPrompt(ctx, g) {
		fmt.Fprintln(g.Stdout, "Aborted.")
		return false
	}
	return true
}

// confirmPrompt reads a y/N answer from stdin with context cancellation support.
func confirmPrompt(ctx context.Context, g *Globals) bool {
	type result struct {
		answer string
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		var answer string
		_, err := fmt.Fscan(g.Stdin, &answer)
		ch <- result{answer, err}
	}()
	select {
	case <-ctx.Done():
		return false
	case res := <-ch:
		if res.err != nil {
			return false
		}
		return strings.EqualFold(strings.TrimSpace(res.answer), "y")
	}
}

func detectShell() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return "bash"
	}
	return filepath.Base(shell)
}

func defaultRCFile(shell string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "bash":
		return filepath.Join(home, ".bashrc")
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish")
	default:
		return ""
	}
}

// completionSnippet returns the shell completion snippet for the given shell,
// wrapped in marker comments. Returns "" for unsupported shells.
func completionSnippet(shell, bin, cmd string) string {
	var body string
	switch shell {
	case "zsh":
		body = fmt.Sprintf("autoload -U +X bashcompinit && bashcompinit\ncomplete -C %s %s\n", bin, cmd)
	case "bash":
		body = fmt.Sprintf("complete -C %s %s\n", bin, cmd)
	case "fish":
		body = fmt.Sprintf(`function __complete_%s
    set -lx COMP_LINE (commandline -cp)
    test -z (commandline -ct)
    and set COMP_LINE "$COMP_LINE "
    %s
end
complete -f -c %s -a "(__complete_%s)"
`, cmd, bin, cmd, cmd)
	default:
		return ""
	}
	return markerBegin + "\n" + body + markerEnd + "\n"
}

// installToRC appends the completion snippet to the rc file if not already present.
// Returns true if the snippet was added, false if already present.
func installToRC(rcPath, shell, bin, cmd string) (bool, error) {
	existing, err := os.ReadFile(rcPath)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("reading %s: %w", rcPath, err)
	}

	if strings.Contains(string(existing), markerBegin) {
		return false, nil
	}

	snippet := completionSnippet(shell, bin, cmd)
	if snippet == "" {
		return false, fmt.Errorf("unsupported shell: %s", shell)
	}

	f, err := os.OpenFile(rcPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return false, fmt.Errorf("opening %s: %w", rcPath, err)
	}
	defer f.Close()

	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		fmt.Fprintln(f)
	}

	_, err = fmt.Fprint(f, snippet)
	if err != nil {
		return false, fmt.Errorf("writing to %s: %w", rcPath, err)
	}
	return true, nil
}

// uninstallFromRC removes the completion snippet (between markers) from the rc file.
// Returns true if something was removed, false if markers weren't found.
func uninstallFromRC(rcPath string) (bool, error) {
	content, err := os.ReadFile(rcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading %s: %w", rcPath, err)
	}

	s := string(content)
	before, rest, found := strings.Cut(s, markerBegin)
	if !found {
		return false, nil
	}
	_, after, found := strings.Cut(rest, markerEnd)
	if !found {
		return false, nil
	}
	after = strings.TrimPrefix(after, "\n")

	cleaned := before + after
	if err := os.WriteFile(rcPath, []byte(cleaned), 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", rcPath, err)
	}
	return true, nil
}
