package sync

import (
	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	planviewoverlay "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/overlays/planview"
)

type Model struct {
	configDir string

	snapshot common.SyncSnapshot

	width      int
	height     int
	planCursor int
	planOffset int

	// Cached planview items. BuildItems is O(7 × len(ops)) and used to run
	// twice per keystroke plus once per render. WithContext is the only path
	// that introduces a new ops slice, so refreshing the cache there suffices.
	cachedPlanItems []planviewoverlay.Item
}

func New(configDir string) Model {
	return Model{configDir: configDir}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) SetSize(width, height int) common.Screen {
	m.width = width
	m.height = height
	m.clampPlanOffset()
	return m
}

func (m Model) WithContext(snap common.SyncSnapshot) Model {
	m.snapshot = snap
	m.refreshPlanItems()
	m.clampPlanOffset()
	return m
}

func (m *Model) refreshPlanItems() {
	tmp := planviewoverlay.Model{Ops: m.snapshot.Target.Ops}
	m.cachedPlanItems = tmp.BuildItems()
}

func (m Model) planView(width, height int) planviewoverlay.Model {
	if m.cachedPlanItems == nil && len(m.snapshot.Target.Ops) > 0 {
		m.refreshPlanItems()
	}
	pv := planviewoverlay.Model{
		ProfileName: m.snapshot.ProfileName,
		Ops:         m.snapshot.Target.Ops,
		ProjectDir:  m.snapshot.Target.ProjectDir,
		IsSavePlan:  false,
		Width:       width,
		Height:      height,
		Ready:       true,
		Items:       m.cachedPlanItems,
	}
	m.clampPlanView(&pv)
	return pv
}

func (m Model) clampPlanView(pv *planviewoverlay.Model) {
	if len(pv.Items) == 0 {
		pv.Cursor = 0
		pv.ScrollOffset = 0
		return
	}
	cursor := min(max(m.planCursor, 0), len(pv.Items)-1)
	if pv.Items[cursor].IsHeader {
		cursor = firstSelectablePlanItem(pv.Items)
	}
	pv.Cursor = cursor
	maxOffset := max(len(pv.Items)-pv.ViewportHeight(), 0)
	pv.ScrollOffset = min(max(m.planOffset, 0), maxOffset)
}

func (m *Model) clampPlanOffset() {
	if m.cachedPlanItems == nil && len(m.snapshot.Target.Ops) > 0 {
		m.refreshPlanItems()
	}
	items := m.cachedPlanItems
	if len(items) == 0 {
		m.planCursor = 0
		m.planOffset = 0
		return
	}
	if m.planCursor < 0 || m.planCursor >= len(items) || items[m.planCursor].IsHeader {
		m.planCursor = firstSelectablePlanItem(items)
	}
	pv := planviewoverlay.Model{Width: m.planPanelWidth(), Height: m.panelHeight(), Items: items}
	maxOffset := max(len(items)-pv.ViewportHeight(), 0)
	m.planOffset = min(max(m.planOffset, 0), maxOffset)
}

func firstSelectablePlanItem(items []planviewoverlay.Item) int {
	for i, item := range items {
		if !item.IsHeader {
			return i
		}
	}
	return 0
}
