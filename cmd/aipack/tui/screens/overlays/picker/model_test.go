package picker

import (
	"regexp"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestViewHitLayersDoNotAlterRenderedPicker(t *testing.T) {
	t.Parallel()

	m := New("tools", "Tools:", []Item{{Label: "read", State: TriAsk}})
	got := stripSGR(lipgloss.NewCompositor(m.View()).Render())
	want := stripSGR(m.Render())
	if got != want {
		t.Fatalf("composited view changed rendered picker\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

var sgrPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripSGR(s string) string {
	return sgrPattern.ReplaceAllString(s, "")
}
