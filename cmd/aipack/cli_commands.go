package main

import "github.com/willabides/kongplete"

// CLI is the root command struct.
type CLI struct {
	cliCore
	InstallCompletions kongplete.InstallCompletions `cmd:"" help:"Install shell completions for bash, zsh, or fish" group:"Other:"`
}
