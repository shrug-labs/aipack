package main

// InstallCmd is a hidden top-level alias for "pack install".
// It exists so that "aipack install ..." works for users who reach for
// the obvious command, without cluttering --help output.
type InstallCmd struct {
	PackInstallCmd
}
