package planview

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/engine"
)

func TestViewHitLayersDoNotAlterRenderedPlanView(t *testing.T) {
	t.Parallel()

	m := New(80, 24, "default", "/tmp/project", []app.PlanOp{
		{Kind: app.PlanOpRule, Dst: "/tmp/project/rules/r.md", SourcePack: "core"},
	}, false, nil)
	got := stripSGR(lipgloss.NewCompositor(m.View()).Render())
	want := stripSGR(m.Render())
	if got != want {
		t.Fatalf("composited view changed rendered plan view\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestViewHitLayersAlignWithRenderedPlanRows(t *testing.T) {
	t.Parallel()

	m := New(100, 24, "default", "/tmp/project", []app.PlanOp{
		{Kind: app.PlanOpSkill, Dst: "/tmp/project/skills/first/SKILL.md", SourcePack: "core"},
		{Kind: app.PlanOpSkill, Dst: "/tmp/project/skills/second/SKILL.md", SourcePack: "core"},
	}, false, nil)
	comp := lipgloss.NewCompositor(m.View())
	rendered := strings.Split(stripSGR(comp.Render()), "\n")

	tests := []struct {
		text string
		want string
	}{
		{text: "first/SKILL.md", want: "planview:item:1"},
		{text: "second/SKILL.md", want: "planview:item:2"},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			x, y := findRenderedText(t, rendered, tt.text)
			if got := comp.Hit(x, y).ID(); got != tt.want {
				t.Fatalf("Hit(%d,%d) on %q = %q, want %q\n%s", x, y, tt.text, got, tt.want, strings.Join(rendered, "\n"))
			}
		})
	}
}

func TestDiffViewDoesNotRenderTallerThanConfiguredHeight(t *testing.T) {
	t.Parallel()

	m := NewDiffViewModel(80, 6, "large.md", "./large.md")
	m.SetContent(strings.Repeat("-old line\n+new line\n", 40), false, "", "")

	rendered := stripSGR(m.View())
	if got := lipgloss.Height(rendered); got > m.Height {
		t.Fatalf("diff view height = %d, want <= %d\n%s", got, m.Height, rendered)
	}
	lines := strings.Split(rendered, "\n")
	last := strings.TrimRight(lines[len(lines)-1], " ")
	if !strings.Contains(last, "╰") || !strings.Contains(last, "╯") {
		t.Fatalf("expected bottom border on final overlay row, got %q\n%s", last, rendered)
	}
}

func TestShortDstHandlesWindowsSeparators(t *testing.T) {
	t.Parallel()

	m := New(80, 24, "default", `C:\work\project`, nil, false, nil)
	got := m.ShortDst(`C:\work\project\rules\review.md`)
	if got != "./rules/review.md" {
		t.Fatalf("ShortDst windows path = %q, want %q", got, "./rules/review.md")
	}
}

func TestLoadDiffForMergedMCPShowsAfterMergeAndPreservesRuntimeApproval(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dst := filepath.Join(dir, "config.toml")
	onDisk := []byte(`[mcp_servers.github]
enabled = true
command = "node"
enabled_tools = ["get_pr"]
startup_timeout_sec = 10

[mcp_servers.github.tools.get_pr]
approval_mode = "approve"
`)
	desired := []byte(`[mcp_servers.github]
enabled = true
command = "node"
enabled_tools = ["get_pr", "list_repos"]
startup_timeout_sec = 10

[mcp_servers.github.tools.get_pr]
approval_mode = "approve"
`)
	if err := os.WriteFile(dst, onDisk, 0o644); err != nil {
		t.Fatal(err)
	}

	op := app.PlanOp{
		Kind:    app.PlanOpMCP,
		Dst:     dst,
		Content: desired,
		Diff:    "--- config.toml (current)\n+++ config.toml (after merge)\n@@ -1,3 +1,3 @@\n [mcp_servers.github]\n-enabled_tools = ['get_pr']\n+enabled_tools = ['get_pr', 'list_repos']\n",
		MergeOps: []engine.MergeOp{{
			Key:    "mcp_servers.github.enabled_tools",
			Action: engine.MergeUpdate,
		}},
	}
	m := New(100, 24, "default", dir, []app.PlanOp{op}, false, nil)

	msg := m.LoadDiff(op)()
	loaded, ok := msg.(DiffLoadedMsg)
	if !ok {
		t.Fatalf("LoadDiff returned %T, want DiffLoadedMsg", msg)
	}
	if loaded.Err != nil {
		t.Fatal(loaded.Err)
	}
	if !strings.Contains(loaded.DiffText, "+++ config.toml (after merge)") {
		t.Fatalf("diff should label merged destination, got:\n%s", loaded.DiffText)
	}
	if !strings.Contains(loaded.Note, "user-owned keys outside the shown hunks are preserved") {
		t.Fatalf("diff should explain preserved merge context, got note %q", loaded.Note)
	}
	if strings.Contains(loaded.DiffText, "\n-approval_mode") {
		t.Fatalf("runtime approval should not be shown as removed:\n%s", loaded.DiffText)
	}

	dv := NewDiffViewModel(100, 24, "config.toml", "./config.toml")
	m.DiffView = &dv
	m, _ = m.UpdateModel(loaded)
	if m.DiffView == nil {
		t.Fatal("diff view closed unexpectedly")
	}
	rendered := stripSGR(m.DiffView.View())
	if !strings.Contains(rendered, "Merged settings diff") {
		t.Fatalf("diff note should be visible, got:\n%s", rendered)
	}
}

func findRenderedText(t *testing.T, lines []string, text string) (int, int) {
	t.Helper()
	for y, line := range lines {
		if x := strings.Index(line, text); x >= 0 {
			return x, y
		}
	}
	t.Fatalf("could not find %q in render:\n%s", text, strings.Join(lines, "\n"))
	return 0, 0
}

var sgrPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripSGR(s string) string {
	return sgrPattern.ReplaceAllString(s, "")
}
