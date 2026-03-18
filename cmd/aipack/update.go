package main

import (
	"context"

	"github.com/shrug-labs/aipack/internal/update"
)

// UpdateCmd implements `aipack update`.
type UpdateCmd struct{}

func (c *UpdateCmd) Run(ctx context.Context, g *Globals) error {
	return update.Update(ctx, version, g.Stdout)
}
