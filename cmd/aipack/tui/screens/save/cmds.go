package save

import (
	"context"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
)

func DetectHarnesses(reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		all := reg.All()
		ids := make([]domain.Harness, len(all))
		for i, h := range all {
			ids[i] = h.ID()
		}
		return HarnessDetectedMsg{Harnesses: ids}
	}
}

func DiscoverVectors(ctx context.Context, harnessID domain.Harness, projectDir, home string, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		vectors, err := app.DiscoverContentVectorsAllScopes(ctx, harnessID, projectDir, home, nil, reg)
		return VectorsDiscoveredMsg{Vectors: vectors, Err: err}
	}
}

func DiscoverFiles(ctx context.Context, eng *engine.Engine, req app.DiscoverSaveRequest, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		candidates, warnings, err := app.DiscoverSaveFilesAllScopes(ctx, eng, req, reg)
		return FilesDiscoveredMsg{Candidates: candidates, Warnings: warnings, Err: err}
	}
}

func DeleteFile(path string) tea.Cmd {
	return func() tea.Msg {
		err := os.RemoveAll(path)
		return FileDeletedMsg{Path: path, Err: err}
	}
}

func RunPipeline(eng *engine.Engine, req app.SavePipelineRequest, reg *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		result, err := app.RunSavePipeline(eng, req, reg)
		return PipelineDoneMsg{Result: &result, Err: err}
	}
}
