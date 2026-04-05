package main

import (
	"path/filepath"
	"runtime"
)

func defaultConfigDirDisplay() string {
	if runtime.GOOS == "windows" {
		return `%APPDATA%\aipack`
	}
	return "~/.config/aipack"
}

func configPathDisplay(parts ...string) string {
	if len(parts) == 0 {
		return defaultConfigDirDisplay()
	}
	all := make([]string, 0, len(parts)+1)
	all = append(all, defaultConfigDirDisplay())
	all = append(all, parts...)
	return filepath.Join(all...)
}
