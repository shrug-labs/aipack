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
				PackName:    "my-team-ops",
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

func TestSaveCandidateLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		file app.HarnessFile
		want string
	}{
		{
			name: "rule uses filename without extension",
			file: app.HarnessFile{
				HarnessPath: "/home/.claude/rules/triage.md",
				RelPath:     "triage",
				Category:    domain.CategoryRules,
			},
			want: "triage",
		},
		{
			name: "MCP uses RelPath server name",
			file: app.HarnessFile{
				HarnessPath: "/home/.claude.json",
				RelPath:     "atlassian",
				Category:    domain.CategoryMCP,
			},
			want: "atlassian",
		},
		{
			name: "real skill directory uses skill ID",
			file: app.HarnessFile{
				HarnessPath: "/home/.claude/skills/agent-configuration",
				RelPath:     "agent-configuration",
				Category:    domain.CategorySkills,
				Kind:        domain.CopyKindDir,
			},
			want: "agent-configuration",
		},
		{
			name: "promoted workflow as skill uses skill ID not SKILL.md",
			file: app.HarnessFile{
				HarnessPath: "/home/.claude/skills/execute-plan/SKILL.md",
				RelPath:     "execute-plan",
				Category:    domain.CategorySkills,
				Kind:        domain.CopyKindFile,
			},
			want: "execute-plan",
		},
		{
			name: "workflow file uses name without extension",
			file: app.HarnessFile{
				HarnessPath: "/home/.claude/workflows/deploy.md",
				RelPath:     "deploy",
				Category:    domain.CategoryWorkflows,
			},
			want: "deploy",
		},
		{
			name: "empty RelPath falls back to basename",
			file: app.HarnessFile{
				HarnessPath: "/home/.claude/rules/unknown.md",
				Category:    domain.CategoryRules,
			},
			want: "unknown.md",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := saveCandidateLabel(tt.file)
			if got != tt.want {
				t.Errorf("saveCandidateLabel() = %q, want %q", got, tt.want)
			}
		})
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
