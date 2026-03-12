package main

import "strings"

// InstallCmd is a top-level alias for "pack install".
type InstallCmd struct {
	PackInstallCmd
}

func (c *InstallCmd) Help() string {
	base := c.PackInstallCmd.Help()
	base = strings.ReplaceAll(base, "aipack pack install", "aipack install")
	return base + "\n\nThis command is an alias for \"aipack pack install\"."
}
