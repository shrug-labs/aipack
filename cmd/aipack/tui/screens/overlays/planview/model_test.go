package planview

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/internal/app"
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
