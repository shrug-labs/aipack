package main

import "github.com/shrug-labs/aipack/internal/update"

// UpdateCmd implements `aipack update`.
type UpdateCmd struct{}

func (c *UpdateCmd) Run(g *Globals) error {
	return update.Update(version, g.Stdout)
}
