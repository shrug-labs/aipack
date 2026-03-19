package main

// CLI is the root command struct.
type CLI struct {
	cliCore
	InstallCompletions InstallCompletionsCmd `cmd:"" help:"Install or remove shell completions for bash, zsh, or fish" group:"Other:"`
}
