package app

import (
	"context"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/render"
)

// RunRender renders pack content to the given directory using all registered harnesses.
func RunRender(ctx context.Context, profile domain.Profile, outDir string, reg *harness.Registry) error {
	return render.RunToDir(ctx, profile, outDir, reg.All())
}
