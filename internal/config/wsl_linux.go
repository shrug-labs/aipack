//go:build linux

package config

import (
	"os"
	"strings"
	"sync"
)

var (
	wslOnce   sync.Once
	wslResult bool
)

// IsWSL reports whether the process is running inside Windows Subsystem for Linux.
// Detection reads /proc/version for the "microsoft" marker that both WSL1 and WSL2 inject.
func IsWSL() bool {
	wslOnce.Do(func() {
		b, err := os.ReadFile("/proc/version")
		if err != nil {
			return
		}
		wslResult = strings.Contains(strings.ToLower(string(b)), "microsoft")
	})
	return wslResult
}
