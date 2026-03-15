package main

import (
	"fmt"
	"os"
	"time"

	"github.com/shrug-labs/aipack/internal/update"
)

// Set via -ldflags "-X main.version=<version> -X main.commit=<sha>".
var (
	version = "dev"
	commit  = "unknown"
)

type VersionCmd struct{}

func (c *VersionCmd) Run(g *Globals) error {
	updateCh := update.CheckAsync(version, os.Getenv("HOME"))

	fmt.Fprintf(g.Stdout, "aipack %s (%s)\n", version, commit)

	select {
	case res := <-updateCh:
		if notice := res.Notice(); notice != "" {
			fmt.Fprint(g.Stderr, notice)
		}
	case <-time.After(2 * time.Second):
	}
	return nil
}
