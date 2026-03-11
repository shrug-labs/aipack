package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

// PromptCmd groups commands for browsing/copying prompts from installed packs.
type PromptCmd struct {
	List PromptListCmd `cmd:"" help:"List all prompts from installed packs"`
	Copy PromptCopyCmd `cmd:"" help:"Copy a prompt to clipboard"`
	Show PromptShowCmd `cmd:"" help:"Display a prompt"`
}

type PromptListCmd struct{}

func (c *PromptListCmd) Run(g *Globals) error {
	configDir, err := resolvePromptConfigDir()
	if err != nil {
		return ExitError{cmdutil.ExitFail}
	}

	prompts, err := app.PromptList(configDir)
	if err != nil {
		fmt.Fprintln(g.Stderr, "ERROR:", err)
		return ExitError{cmdutil.ExitFail}
	}
	if len(prompts) == 0 {
		fmt.Fprintln(g.Stdout, "No prompts found in installed packs.")
		return nil
	}

	tw := tabwriter.NewWriter(g.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPACK\tDESCRIPTION")
	for _, p := range prompts {
		desc := p.Description
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, p.Pack, desc)
	}
	return tw.Flush()
}

type PromptCopyCmd struct {
	Name string `arg:"" help:"Prompt name to copy"`
}

func (c *PromptCopyCmd) Run(g *Globals) error {
	configDir, err := resolvePromptConfigDir()
	if err != nil {
		return ExitError{cmdutil.ExitFail}
	}

	if err := app.PromptCopy(c.Name, configDir); err != nil {
		fmt.Fprintln(g.Stderr, "ERROR:", err)
		return ExitError{cmdutil.ExitFail}
	}
	fmt.Fprintln(g.Stdout, "Copied to clipboard.")
	return nil
}

type PromptShowCmd struct {
	Name string `arg:"" help:"Prompt name to display"`
}

func (c *PromptShowCmd) Run(g *Globals) error {
	configDir, err := resolvePromptConfigDir()
	if err != nil {
		return ExitError{cmdutil.ExitFail}
	}

	p, err := app.PromptShow(c.Name, configDir)
	if err != nil {
		fmt.Fprintln(g.Stderr, "ERROR:", err)
		return ExitError{cmdutil.ExitFail}
	}
	_, err = g.Stdout.Write(p.Body)
	return err
}

func resolvePromptConfigDir() (string, error) {
	return config.DefaultConfigDir(os.Getenv("HOME"))
}
