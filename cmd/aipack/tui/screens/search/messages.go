package search

import "github.com/shrug-labs/aipack/internal/app"

type ResultsMsg struct {
	Results           []app.SearchResult
	Err               error
	PreserveSelection bool
}

type OpenPackMsg struct {
	PackName string
	Kind     string
	Name     string
}
