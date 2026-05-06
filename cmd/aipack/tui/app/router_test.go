package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
)

type testScreen struct {
	updates int
	width   int
	height  int
}

func (s testScreen) Init() tea.Cmd { return nil }

func (s testScreen) Update(tea.Msg) (common.Screen, tea.Cmd) {
	s.updates++
	return s, nil
}

func (s testScreen) View() *lipgloss.Layer { return lipgloss.NewLayer("") }

func (s testScreen) SetSize(width, height int) common.Screen {
	s.width = width
	s.height = height
	return s
}

func TestRouterDispatchesThroughScreenInterface(t *testing.T) {
	t.Parallel()

	r := NewRouter().Set(ScreenSearch, testScreen{})
	var cmd tea.Cmd
	r, cmd = r.UpdateScreen(ScreenSearch, tea.KeyPressMsg(tea.Key{Text: "x", Code: 'x'}))
	if cmd != nil {
		t.Fatal("unexpected command from test screen")
	}
	got := r.Screen(ScreenSearch).(testScreen)
	if got.updates != 1 {
		t.Fatalf("expected update count 1, got %d", got.updates)
	}
}

func TestRouterSizesScreens(t *testing.T) {
	t.Parallel()

	r := NewRouter().Set(ScreenSearch, testScreen{})
	r = r.SetSize(ScreenSearch, 120, 36)
	got := r.Screen(ScreenSearch).(testScreen)
	if got.width != 120 || got.height != 36 {
		t.Fatalf("expected 120x36, got %dx%d", got.width, got.height)
	}
}

func TestRouterImplementsTeaModelAndRoutesActiveScreen(t *testing.T) {
	t.Parallel()

	r := NewRouter().Set(ScreenSearch, testScreen{})
	model, cmd := r.Update(tea.KeyPressMsg(tea.Key{Text: "x", Code: 'x'}))
	if cmd != nil {
		t.Fatal("unexpected command from test screen")
	}
	got := model.(Router).Screen(ScreenSearch).(testScreen)
	if got.updates != 1 {
		t.Fatalf("expected update count 1, got %d", got.updates)
	}
}

func TestRouterViewOnMouseEmitsLayerHitMsg(t *testing.T) {
	t.Parallel()

	r := NewRouter().Set(ScreenSearch, hitScreen{})
	v := r.View()
	if !v.AltScreen {
		t.Fatal("expected altscreen to be enabled")
	}
	if v.MouseMode != tea.MouseModeAllMotion {
		t.Fatalf("expected all-motion mouse mode, got %v", v.MouseMode)
	}
	if v.OnMouse == nil {
		t.Fatal("expected mouse handler")
	}
	cmd := v.OnMouse(tea.MouseClickMsg(tea.Mouse{X: 0, Y: 0, Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("expected LayerHit command")
	}
	msg := cmd()
	hit, ok := msg.(common.LayerHitMsg)
	if !ok {
		t.Fatalf("expected LayerHitMsg, got %T", msg)
	}
	if hit.ID != "search:button" {
		t.Fatalf("expected search:button hit, got %q", hit.ID)
	}
	if _, ok := hit.Mouse.(tea.MouseClickMsg); !ok {
		t.Fatalf("expected original MouseClickMsg, got %T", hit.Mouse)
	}
}

func TestRouterRoutesLayerHitsByNamespace(t *testing.T) {
	t.Parallel()

	r := NewRouter().Set(ScreenSearch, testScreen{})
	r, cmd := r.RouteLayerHit(common.LayerHitMsg{
		ID:    "search:button",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("unexpected command from test screen")
	}
	got := r.Screen(ScreenSearch).(testScreen)
	if got.updates != 1 {
		t.Fatalf("expected update count 1, got %d", got.updates)
	}
}

func TestRouterRoutesSyncLayerHitsByNamespace(t *testing.T) {
	t.Parallel()

	r := NewRouter().Set(ScreenSync, testScreen{})
	r, cmd := r.RouteLayerHit(common.LayerHitMsg{
		ID:    "sync:field:profile",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("unexpected command from test screen")
	}
	got := r.Screen(ScreenSync).(testScreen)
	if got.updates != 1 {
		t.Fatalf("expected update count 1, got %d", got.updates)
	}
}

func TestRouterRoutesSaveLayerHitsByNamespace(t *testing.T) {
	t.Parallel()

	r := NewRouter().Set(ScreenSave, testScreen{})
	r, cmd := r.RouteLayerHit(common.LayerHitMsg{
		ID:    "save:file:0",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	if cmd != nil {
		t.Fatal("unexpected command from test screen")
	}
	got := r.Screen(ScreenSave).(testScreen)
	if got.updates != 1 {
		t.Fatalf("expected update count 1, got %d", got.updates)
	}
}

type hitScreen struct{}

func (s hitScreen) Init() tea.Cmd { return nil }

func (s hitScreen) Update(tea.Msg) (common.Screen, tea.Cmd) { return s, nil }

func (s hitScreen) View() *lipgloss.Layer {
	return lipgloss.NewLayer("button").ID("search:button")
}

func (s hitScreen) SetSize(width, height int) common.Screen { return s }
