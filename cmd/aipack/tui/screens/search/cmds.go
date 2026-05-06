package search

import (
	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/internal/app"
)

func RunSearch(configDir, query, kind, category, installed string) tea.Cmd {
	return runSearch(configDir, query, kind, category, installed, false)
}

func RefreshSearch(configDir, query, kind, category, installed string) tea.Cmd {
	return runSearch(configDir, query, kind, category, installed, true)
}

func runSearch(configDir, query, kind, category, installed string, preserveSelection bool) tea.Cmd {
	return func() tea.Msg {
		req := app.IndexSearchRequest{
			ConfigDir: configDir,
			Terms:     query,
			Kind:      kind,
			Category:  category,
		}
		switch installed {
		case "installed":
			req.Status = "installed"
		case "registered":
			req.Status = "registered"
		case "inspected":
			req.Status = "inspected"
		case "available":
			f := false
			req.Installed = &f
		}
		results, err := app.RunIndexSearch(req)
		if err != nil {
			return ResultsMsg{Err: err, PreserveSelection: preserveSelection}
		}
		return ResultsMsg{Results: results, PreserveSelection: preserveSelection}
	}
}
