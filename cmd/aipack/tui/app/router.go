package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
)

type ScreenID string

const (
	ScreenProfiles ScreenID = "profiles"
	ScreenPacks    ScreenID = "packs"
	ScreenConfig   ScreenID = "config"
	ScreenSearch   ScreenID = "search"
	ScreenSave     ScreenID = "save"
	ScreenSync     ScreenID = "sync"
)

type Router struct {
	screens map[ScreenID]common.Screen
	active  ScreenID
}

func NewRouter() Router {
	return Router{screens: make(map[ScreenID]common.Screen)}
}

func (r Router) Set(id ScreenID, screen common.Screen) Router {
	if r.screens == nil {
		r.screens = make(map[ScreenID]common.Screen)
	}
	r.screens[id] = screen
	if r.active == "" {
		r.active = id
	}
	return r
}

func (r Router) Screen(id ScreenID) common.Screen {
	return r.screens[id]
}

func (r Router) SetActive(id ScreenID) Router {
	if r.screens[id] != nil {
		r.active = id
	}
	return r
}

func (r Router) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(r.screens))
	for _, screen := range r.screens {
		cmds = append(cmds, screen.Init())
	}
	return tea.Batch(cmds...)
}

func (r Router) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case common.LayerHitMsg:
		next, cmd := r.RouteLayerHit(msg)
		return next, cmd
	default:
		next, cmd := r.UpdateScreen(r.active, msg)
		return next, cmd
	}
}

func (r Router) UpdateScreen(id ScreenID, msg tea.Msg) (Router, tea.Cmd) {
	screen := r.screens[id]
	if screen == nil {
		return r, nil
	}
	var cmd tea.Cmd
	r.screens[id], cmd = screen.Update(msg)
	return r, cmd
}

func (r Router) SetSize(id ScreenID, width, height int) Router {
	screen := r.screens[id]
	if screen == nil {
		return r
	}
	r.screens[id] = screen.SetSize(width, height)
	return r
}

// SetSizeAll resizes every registered screen. WindowSizeMsg fans out
// identically to all of them.
func (r Router) SetSizeAll(width, height int) Router {
	for id, screen := range r.screens {
		if screen == nil {
			continue
		}
		r.screens[id] = screen.SetSize(width, height)
	}
	return r
}

// ScreenAs returns the screen registered under id assertion-cast to T. The
// second return is false if the screen is missing or has the wrong concrete
// type — callers are expected to treat that as a programming error, not a
// runtime case to handle.
func ScreenAs[T common.Screen](r Router, id ScreenID) (T, bool) {
	screen, ok := r.screens[id].(T)
	return screen, ok
}

func (r Router) RouteLayerHit(msg common.LayerHitMsg) (Router, tea.Cmd) {
	id, ok := screenIDForLayer(msg.ID)
	if !ok {
		return r, nil
	}
	return r.UpdateScreen(id, msg)
}

func (r Router) View() tea.View {
	return r.Compose(lipgloss.NewLayer(""), r.active, 0)
}

func (r Router) Compose(base *lipgloss.Layer, screenID ScreenID, screenY int, extraLayers ...*lipgloss.Layer) tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion

	root := base
	if root == nil {
		root = lipgloss.NewLayer("")
	}
	if screen := r.screens[screenID]; screen != nil {
		root.AddLayers(screen.View().Y(screenY).Z(1))
	}
	for _, layer := range extraLayers {
		if layer != nil {
			root.AddLayers(layer)
		}
	}
	comp := lipgloss.NewCompositor(root)
	v.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
		return func() tea.Msg {
			mouse := msg.Mouse()
			if id := comp.Hit(mouse.X, mouse.Y).ID(); id != "" {
				return common.LayerHitMsg{ID: id, Mouse: msg}
			}
			return nil
		}
	}
	v.SetContent(comp.Render())
	return v
}

func screenIDForLayer(id string) (ScreenID, bool) {
	prefix, _, ok := strings.Cut(id, ":")
	if !ok {
		return "", false
	}
	switch ScreenID(prefix) {
	case ScreenProfiles, ScreenPacks, ScreenConfig, ScreenSearch, ScreenSave, ScreenSync:
		return ScreenID(prefix), true
	default:
		return "", false
	}
}
