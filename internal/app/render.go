package app

import (
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/render"
)

// RunRender renders pack content to the given directory using all registered harnesses.
func RunRender(profile domain.Profile, outDir string, reg *harness.Registry) error {
	return render.RunToDir(profile, outDir, reg.All())
}
