package search

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
)

func init() {
	common.StatusDotActive = "●"
	common.StatusDotInactive = "○"
	common.StatusDotLoading = "⟳"
}

// withContentPadding sets common.ContentStyle for the duration of a test.
func withContentPadding(t *testing.T, s lipgloss.Style) {
	t.Helper()
	prev := common.ContentStyle
	common.ContentStyle = s
	t.Cleanup(func() { common.ContentStyle = prev })
}

func TestResultsMsgLoadsResults(t *testing.T) {
	t.Parallel()

	screen, cmd := New("").Update(ResultsMsg{
		Results: []app.SearchResult{{Pack: "demo", Kind: "rule", Name: "alpha"}},
	})
	if cmd != nil {
		t.Fatal("unexpected command")
	}
	m := screen.(Model)
	if !m.searched || m.loading {
		t.Fatalf("expected searched and not loading, got searched=%v loading=%v", m.searched, m.loading)
	}
	if len(m.results) != 1 || m.results[0].Name != "alpha" {
		t.Fatalf("unexpected results: %+v", m.results)
	}
}

func TestResultsMsgPreservesSelectionOnRefresh(t *testing.T) {
	t.Parallel()

	screen, _ := New("").Update(ResultsMsg{
		Results: []app.SearchResult{
			{Pack: "demo", Kind: "rule", Name: "alpha"},
			{Pack: "demo", Kind: "rule", Name: "beta"},
			{Pack: "demo", Kind: "rule", Name: "charlie"},
		},
	})
	m := screen.(Model)
	m.focus = focusResults
	m.cursor = 1

	screen, _ = m.Update(ResultsMsg{
		PreserveSelection: true,
		Results: []app.SearchResult{
			{Pack: "demo", Kind: "rule", Name: "charlie", Installed: true},
			{Pack: "demo", Kind: "rule", Name: "beta", Installed: true},
			{Pack: "demo", Kind: "rule", Name: "alpha", Installed: true},
		},
	})
	m = screen.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want beta row at 1", m.cursor)
	}
	if got := m.results[m.cursor]; got.Name != "beta" || !got.Installed {
		t.Fatalf("selected result = %+v, want refreshed beta", got)
	}
}

func TestInputAcceptsBracketedPaste(t *testing.T) {
	t.Parallel()

	screen, cmd := New("").Update(tea.PasteMsg{Content: "rules owner\r\n"})
	if cmd != nil {
		t.Fatal("paste should not run search")
	}
	m := screen.(Model)
	if m.query != "rules owner" {
		t.Fatalf("query = %q, want pasted text", m.query)
	}
}

func TestViewRegistersClickableLayers(t *testing.T) {
	t.Parallel()

	m := New("")
	m.width = 100
	m.height = 20
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{{Pack: "demo", Kind: "rule", Name: "alpha"}},
	})
	m = screen.(Model)

	comp := lipgloss.NewCompositor(m.View())
	for _, id := range []string{
		"search:input",
		"search:filter:kind",
		"search:filter:category",
		"search:filter:installed",
		"search:result:0",
	} {
		if comp.GetLayer(id) == nil {
			t.Fatalf("expected layer %q to be registered", id)
		}
	}
	if comp.GetLayer("search:install:0") != nil {
		t.Fatal("search results must not expose install hit layers")
	}
}

func TestClickableLayersRenderBehindContent(t *testing.T) {
	t.Parallel()

	m := New("")
	m.width = 100
	m.height = 20
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{{Pack: "demo", Kind: "rule", Name: "alpha"}},
	})
	m = screen.(Model)

	comp := lipgloss.NewCompositor(m.View())
	if comp.GetLayer("search:root") != nil {
		t.Fatal("search root must not have an ID that blocks child hit layers")
	}
	for _, id := range []string{
		"search:input",
		"search:filter:kind",
		"search:filter:category",
		"search:filter:installed",
		"search:result:0",
	} {
		layer := comp.GetLayer(id)
		if layer == nil {
			t.Fatalf("expected layer %q to be registered", id)
		}
		if layer.GetZ() >= 0 {
			t.Fatalf("expected layer %q to render behind content, got z=%d", id, layer.GetZ())
		}
	}
	if comp.GetLayer("search:install:0") != nil {
		t.Fatal("search results must not expose install hit layers")
	}
}

func TestInputClickLayerCoversWholeRow(t *testing.T) {
	// Mutates package-global common.ContentStyle; cannot run in parallel with
	// readers in the same package.
	withContentPadding(t, lipgloss.NewStyle().Padding(1, 2))
	m := New("")
	m.width = 80
	m.height = 20
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{{Pack: "demo", Kind: "rule", Name: "alpha"}},
	})
	m = screen.(Model)

	layer := lipgloss.NewCompositor(m.View()).GetLayer("search:input")
	if layer == nil {
		t.Fatal("expected search input layer")
	}
	want := m.width - common.ContentStyle.GetPaddingLeft() - common.ContentStyle.GetPaddingRight()
	if got := len(layer.GetContent()); got != want {
		t.Fatalf("input click layer width = %d, want %d", got, want)
	}
}

func TestViewCapsResultsByLineBudget(t *testing.T) {
	t.Parallel()

	m := New("")
	m.width = 100
	m.height = 8
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{
			{Pack: "demo", Kind: "rule", Name: "alpha-0", Snippet: "first snippet"},
			{Pack: "demo", Kind: "rule", Name: "alpha-1", Snippet: "second snippet"},
			{Pack: "demo", Kind: "rule", Name: "alpha-2", Snippet: "third snippet"},
		},
	})
	m = screen.(Model)

	rendered := lipgloss.NewCompositor(m.View()).Render()
	if !strings.Contains(rendered, "alpha-0") {
		t.Fatalf("expected first result to render:\n%s", rendered)
	}
	if strings.Contains(rendered, "alpha-1") || strings.Contains(rendered, "alpha-2") {
		t.Fatalf("rendered results exceed line budget:\n%s", rendered)
	}
}

func TestViewFooterDoesNotDuplicateHelpText(t *testing.T) {
	t.Parallel()

	m := New("")
	m.width = 120
	m.height = 20
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{
			{Pack: "demo", Kind: "rule", Name: "alpha"},
		},
	})
	m = screen.(Model)
	m.focus = focusResults

	rendered := lipgloss.NewCompositor(m.View()).Render()
	if strings.Contains(rendered, "enter:preview") || strings.Contains(rendered, "/search") {
		t.Fatalf("search view should leave action help to the shell help bar:\n%s", rendered)
	}
	if !strings.Contains(rendered, "1 result(s)") {
		t.Fatalf("expected result count footer:\n%s", rendered)
	}
}

func TestViewFitsWithinPaddedScreenHeight(t *testing.T) {
	// Mutates package-global common.ContentStyle; cannot run in parallel with
	// readers in the same package.
	withContentPadding(t, lipgloss.NewStyle().Padding(1, 2))
	m := New("")
	m.width = 100
	m.height = 10
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{
			{Pack: "demo", Kind: "rule", Name: "alpha-0", Snippet: "first snippet"},
			{Pack: "demo", Kind: "rule", Name: "alpha-1", Snippet: "second snippet"},
			{Pack: "demo", Kind: "rule", Name: "alpha-2", Snippet: "third snippet"},
			{Pack: "demo", Kind: "rule", Name: "alpha-3", Snippet: "fourth snippet"},
		},
	})
	m = screen.(Model)

	rendered := lipgloss.NewCompositor(m.View()).Render()
	if got := lipgloss.Height(rendered); got > m.height {
		t.Fatalf("search view height = %d, want <= %d:\n%s", got, m.height, rendered)
	}
}

func TestViewCompactsSnippetToSingleLine(t *testing.T) {
	t.Parallel()

	m := New("")
	m.width = 100
	m.height = 20
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{{
			Pack:    "demo",
			Kind:    "rule",
			Name:    "alpha",
			Snippet: "first line\n\nsecond line\tthird line",
		}},
	})
	m = screen.(Model)

	rendered := lipgloss.NewCompositor(m.View()).Render()
	if !strings.Contains(rendered, "first line second line third line") {
		t.Fatalf("expected compacted snippet:\n%s", rendered)
	}
	if strings.Contains(rendered, "first line\n\nsecond line") {
		t.Fatalf("snippet should not retain embedded blank lines:\n%s", rendered)
	}
}

func TestInstallLayerHitIsIgnored(t *testing.T) {
	t.Parallel()

	m := New("")
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{{Pack: "demo", Kind: "rule", Name: "alpha"}},
	})
	m = screen.(Model)

	_, cmd := m.Update(common.LayerHitMsg{
		ID:    "search:install:0",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("search install hit should be ignored")
	}
}

func TestEnterOnInstalledResultEmitsOpenPack(t *testing.T) {
	t.Parallel()

	m := New("")
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{{
			Pack:      "demo",
			Kind:      "rule",
			Name:      "alpha",
			Path:      "/tmp/demo/rules/alpha.md",
			Installed: true,
		}},
	})
	m = screen.(Model)
	m.focus = focusResults

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("expected open-pack command")
	}
	msg := cmd()
	req, ok := msg.(OpenPackMsg)
	if !ok {
		t.Fatalf("expected OpenPackMsg, got %T", msg)
	}
	if req.PackName != "demo" {
		t.Fatalf("expected demo pack, got %q", req.PackName)
	}
	if req.Kind != "rule" || req.Name != "alpha" {
		t.Fatalf("expected rule alpha content hint, got kind=%q name=%q", req.Kind, req.Name)
	}
}

func TestPreviewKeyOnInstalledResultEmitsPreview(t *testing.T) {
	t.Parallel()

	m := New("")
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{{
			Pack:      "demo",
			Kind:      "rule",
			Name:      "alpha",
			Path:      "/tmp/demo/rules/alpha.md",
			Installed: true,
		}},
	})
	m = screen.(Model)
	m.focus = focusResults

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "p", Code: 'p'}))
	if cmd == nil {
		t.Fatal("expected preview command")
	}
	msg := cmd()
	req, ok := msg.(common.PreviewRequestMsg)
	if !ok {
		t.Fatalf("expected PreviewRequestMsg, got %T", msg)
	}
	if req.FilePath != "/tmp/demo/rules/alpha.md" {
		t.Fatalf("unexpected preview path: %q", req.FilePath)
	}
}

func TestEnterOnUninstalledResultEmitsOpenPack(t *testing.T) {
	t.Parallel()

	m := New("")
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{{
			Pack:      "demo",
			Kind:      "agent",
			Name:      "alpha",
			Path:      "agents/alpha.md",
			Installed: false,
		}},
	})
	m = screen.(Model)
	m.focus = focusResults

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("expected open-pack command")
	}
	msg := cmd()
	req, ok := msg.(OpenPackMsg)
	if !ok {
		t.Fatalf("expected OpenPackMsg, got %T", msg)
	}
	if req.PackName != "demo" {
		t.Fatalf("expected demo pack, got %q", req.PackName)
	}
	if req.Kind != "agent" || req.Name != "alpha" {
		t.Fatalf("expected agent alpha content hint, got kind=%q name=%q", req.Kind, req.Name)
	}
}

func TestClickOnUninstalledResultEmitsOpenPack(t *testing.T) {
	t.Parallel()

	m := New("")
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{{
			Pack:      "demo",
			Kind:      "agent",
			Name:      "alpha",
			Path:      "agents/alpha.md",
			Installed: false,
		}},
	})
	m = screen.(Model)

	_, cmd := m.Update(common.LayerHitMsg{
		ID:    "search:result:0",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd == nil {
		t.Fatal("expected open-pack command")
	}
	msg := cmd()
	req, ok := msg.(OpenPackMsg)
	if !ok {
		t.Fatalf("expected OpenPackMsg, got %T", msg)
	}
	if req.PackName != "demo" {
		t.Fatalf("expected demo pack, got %q", req.PackName)
	}
	if req.Kind != "agent" || req.Name != "alpha" {
		t.Fatalf("expected agent alpha content hint, got kind=%q name=%q", req.Kind, req.Name)
	}
}

func TestEnterOnPackResultEmitsPackOnlyOpenPack(t *testing.T) {
	t.Parallel()

	m := New("")
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{{
			Pack: "demo",
			Kind: "pack",
			Name: "demo",
		}},
	})
	m = screen.(Model)
	m.focus = focusResults

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("expected open-pack command")
	}
	msg := cmd()
	req, ok := msg.(OpenPackMsg)
	if !ok {
		t.Fatalf("expected OpenPackMsg, got %T", msg)
	}
	if req.PackName != "demo" {
		t.Fatalf("expected demo pack, got %q", req.PackName)
	}
	if req.Kind != "" || req.Name != "" {
		t.Fatalf("expected no content hint for pack result, got kind=%q name=%q", req.Kind, req.Name)
	}
}

func TestHelpTextReflectsUnavailableResultAction(t *testing.T) {
	t.Parallel()

	m := New("")
	screen, _ := m.Update(ResultsMsg{
		Results: []app.SearchResult{{
			Pack:      "demo",
			Kind:      "agent",
			Name:      "alpha",
			Installed: false,
		}},
	})
	m = screen.(Model)
	m.focus = focusResults

	help := m.HelpText()
	if !strings.Contains(help, "enter:details") {
		t.Fatalf("expected unavailable result help to advertise details, got %q", help)
	}
	if strings.Contains(help, "install") {
		t.Fatalf("search help must not advertise install, got %q", help)
	}
	if strings.Contains(help, "enter:preview") {
		t.Fatalf("unavailable result help must not advertise preview, got %q", help)
	}
}
