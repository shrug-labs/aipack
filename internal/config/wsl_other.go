//go:build !linux

package config

// IsWSL reports whether the process is running inside Windows Subsystem for Linux.
// On non-Linux platforms this is always false.
func IsWSL() bool { return false }
