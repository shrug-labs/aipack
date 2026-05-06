package save

import (
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
)

type HarnessDetectedMsg struct {
	Harnesses []domain.Harness
}

type VectorsDiscoveredMsg struct {
	Vectors []domain.PackCategory
	Err     error
}

type FilesDiscoveredMsg struct {
	Candidates []app.SaveCandidate
	Warnings   []string
	Err        error
}

type FileDeletedMsg struct {
	Path string
	Err  error
}

type PipelineDoneMsg struct {
	Result *app.SavePipelineResult
	Err    error
}

type DiffLoadedMsg struct {
	Dst      string
	Title    string
	DiffText string
	IsNew    bool
	NewBody  string
	Err      error
}
