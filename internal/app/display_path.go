package app

import (
	"fmt"

	"github.com/shrug-labs/aipack/internal/domain"
)

func syncDisplayPath(hid domain.Harness, path string) string {
	label := domain.DisplayPath(path)
	if hid != "" {
		return fmt.Sprintf("[%s] %s", hid, label)
	}
	return label
}
