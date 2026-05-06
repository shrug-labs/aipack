package preview

import (
	"regexp"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/domain"
)

func TestViewHitLayersDoNotAlterRenderedPreview(t *testing.T) {
	t.Parallel()

	m := New(80, 24, nil, nil).SetContent(common.PreviewLoadedMsg{
		Title:    "anti-slop",
		Category: domain.CategoryRules,
		FilePath: "/tmp/pack/rules/anti-slop.md",
		Body:     "body",
	})
	got := stripSGR(lipgloss.NewCompositor(m.View()).Render())
	want := stripSGR(m.Render())
	if got != want {
		t.Fatalf("composited view changed rendered preview\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

var sgrPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripSGR(s string) string {
	return sgrPattern.ReplaceAllString(s, "")
}
