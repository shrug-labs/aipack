package util

import (
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
)

func ShortPath(p string) string {
	home := config.HomeDir()
	if home == "" {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
