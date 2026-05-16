package save

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
)

func init() {
	common.StatusDotActive = "*"
	common.StatusDotInactive = "o"
	common.StatusDotLoading = "~"
}

func TestSaveTabMCPUsesServerNameInListAndDetail(t *testing.T) {
	t.Parallel()

	m := Model{
		stage:      stageFiles,
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
			got := CandidateLabel(tt.file)
			if got != tt.want {
				t.Errorf("CandidateLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSaveTabHelpTextMentionsDiffKey(t *testing.T) {
	t.Parallel()

	m := Model{stage: stageFiles}
	if got := m.helpText(); !strings.Contains(got, "v:diff") {
		t.Fatalf("expected file-stage help text to mention diff key, got %q", got)
	}
}

func TestNewPackInputAcceptsBracketedPaste(t *testing.T) {
	t.Parallel()

	screen, cmd := Model{newPackInput: true, newPackName: "team-"}.Update(tea.PasteMsg{Content: "ops\r\n"})
	if cmd != nil {
		t.Fatal("paste should not execute save pipeline")
	}
	m := screen.(Model)
	if m.newPackName != "team-ops" {
		t.Fatalf("newPackName = %q, want pasted text appended", m.newPackName)
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

	m := Model{
		stage:         stageFiles,
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

func TestViewRegistersClickableLayers(t *testing.T) {
	t.Parallel()

	m := Model{
		stage:         stageFiles,
		width:         120,
		height:        20,
		candidates:    []app.SaveCandidate{{HarnessFile: app.HarnessFile{HarnessPath: "/tmp/rule.md", Category: domain.CategoryRules, State: app.FileModified}}},
		sortedIndices: []int{0},
		stateCounts:   map[app.FileState]int{app.FileModified: 1},
	}

	comp := lipgloss.NewCompositor(m.View())
	if comp.GetLayer("save:file:0") == nil {
		t.Fatal("expected save:file:0 layer")
	}
}

func TestLayerHitDispatchesByMouseKind(t *testing.T) {
	t.Parallel()

	m := Model{
		stage:         stageFiles,
		width:         120,
		height:        20,
		candidates:    []app.SaveCandidate{{HarnessFile: app.HarnessFile{HarnessPath: "/tmp/rule.md", Category: domain.CategoryRules, State: app.FileModified}}},
		sortedIndices: []int{0},
		stateCounts:   map[app.FileState]int{app.FileModified: 1},
	}

	screen, cmd := m.Update(common.LayerHitMsg{
		ID:    "save:file:0",
		Mouse: tea.MouseMotionMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("motion should not trigger an action")
	}
	m = screen.(Model)
	if m.selCount != 0 {
		t.Fatalf("motion changed selection count to %d", m.selCount)
	}

	screen, _ = m.Update(common.LayerHitMsg{
		ID:    "save:file:0",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	m = screen.(Model)
	if m.selCount != 1 || !m.candidates[0].Selected {
		t.Fatalf("expected click to select candidate, count=%d selected=%v", m.selCount, m.candidates[0].Selected)
	}
}
