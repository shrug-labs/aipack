package main

import "fmt"

// Set via -ldflags "-X main.version=<version> -X main.commit=<sha>".
var (
	version = "dev"
	commit  = "unknown"
)

type VersionCmd struct{}

func (c *VersionCmd) Run(g *Globals) error {
	fmt.Fprintf(g.Stdout, "aipack %s (%s)\n", version, commit)
	return nil
}
