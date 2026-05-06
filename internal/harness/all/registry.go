package all

import (
	"github.com/shrug-labs/aipack/internal/harness"
	ccharness "github.com/shrug-labs/aipack/internal/harness/claudecode"
	clharness "github.com/shrug-labs/aipack/internal/harness/cline"
	cxharness "github.com/shrug-labs/aipack/internal/harness/codex"
	ocharness "github.com/shrug-labs/aipack/internal/harness/opencode"
)

func Registry() *harness.Registry {
	return harness.NewRegistry(
		ccharness.Harness{},
		clharness.Harness{},
		cxharness.Harness{},
		ocharness.Harness{},
	)
}
