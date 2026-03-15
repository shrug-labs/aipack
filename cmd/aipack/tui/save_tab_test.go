package tui

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestSaveTabMCPUsesServerNameInListAndDetail(t *testing.T) {
	t.Parallel()

	m := saveTabModel{
		stage:      saveStageFiles,
		width:      120,
		height:     40,
		fileCursor: 0,
		candidates: []app.SaveCandidate{{
			HarnessFile: app.HarnessFile{
				HarnessPath: filepath.Join("/tmp", ".claude.json"),
				RelPath:     "atlassian",
				Category:    domain.CategoryMCP,
				State:       app.FileConflict,
				Size:        366,
				PackName:    "ocm-ai-runbooks",
			},
			Selected: true,
		}},
		sortedIndices: []int{0},
		stateCounts:   map[app.FileState]int{app.FileConflict: 1},
		selCount:      1,
	}

	list := m.viewFileList(80)
	if !strings.Contains(list, "atlassian") {
		t.Fatalf("expected MCP server name in list, got:\n%s", list)
	}
	if !strings.Contains(list, ".claude.json") {
		t.Fatalf("expected backing config path in list, got:\n%s", list)
	}

	detail := m.viewFileDetail(40)
	if !strings.Contains(detail, "atlassian") {
		t.Fatalf("expected MCP server name in detail, got:\n%s", detail)
	}
	if !strings.Contains(detail, "Config:") {
		t.Fatalf("expected MCP config label in detail, got:\n%s", detail)
	}
}

func TestSaveTabHelpTextMentionsDiffKey(t *testing.T) {
	t.Parallel()

	m := saveTabModel{stage: saveStageFiles}
	if got := m.helpText(); !strings.Contains(got, "v:diff") {
		t.Fatalf("expected file-stage help text to mention diff key, got %q", got)
	}
}

func TestSaveTabFileListScrollsWithCursor(t *testing.T) {
	t.Parallel()

	candidates := make([]app.SaveCandidate, 0, 6)
	for i := range 6 {
		name := "rule-" + strconv.Itoa(i) + ".md"
		candidates = append(candidates, app.SaveCandidate{
			HarnessFile: app.HarnessFile{
				HarnessPath: filepath.Join("/tmp", name),
				Category:    domain.CategoryRules,
				State:       app.FileConflict,
			},
			Selected: true,
		})
	}

	m := saveTabModel{
		stage:         saveStageFiles,
		width:         120,
		height:        10,
		fileCursor:    5,
		candidates:    candidates,
		sortedIndices: []int{0, 1, 2, 3, 4, 5},
		stateCounts:   map[app.FileState]int{app.FileConflict: 6},
		selCount:      6,
	}

	list := m.viewFileList(80)
	if !strings.Contains(list, "rule-5.md") {
		t.Fatalf("expected list to scroll to focused item, got:\n%s", list)
	}
	if strings.Contains(list, "rule-0.md") {
		t.Fatalf("expected list to move past top items when cursor is below the fold, got:\n%s", list)
	}
}
