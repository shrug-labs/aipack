package sync

import (
	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
)

func (m Model) Update(msg tea.Msg) (common.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case common.FocusMsg, common.BlurMsg:
		return m, nil
	case common.LayerHitMsg:
		return m.handleLayerHit(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (common.Screen, tea.Cmd) {
	switch msg.String() {
	case "j", "down", "k", "up":
		pv := m.planView(m.planPanelWidth(), m.panelHeight())
		next, _ := pv.UpdateModel(msg)
		m.planCursor = next.Cursor
		m.planOffset = next.ScrollOffset
	case "enter":
		return m.openDiff()
	case "home":
		m.planCursor = 0
		m.planOffset = 0
		m.clampPlanOffset()
	case "end":
		pv := m.planView(m.planPanelWidth(), m.panelHeight())
		for i := len(pv.Items) - 1; i >= 0; i-- {
			if !pv.Items[i].IsHeader {
				m.planCursor = i
				m.planOffset = max(i-pv.ViewportHeight()+1, 0)
				break
			}
		}
		m.clampPlanOffset()
	}
	return m, nil
}

func (m Model) handleLayerHit(msg common.LayerHitMsg) (common.Screen, tea.Cmd) {
	if !common.IsLeftClick(msg.Mouse) {
		return m, nil
	}
	index, ok := common.ParseLayerIndex(msg.ID, "sync:plan:item:")
	if !ok {
		return m, nil
	}
	pv := m.planView(m.planPanelWidth(), m.panelHeight())
	if index < 0 || index >= len(pv.Items) || pv.Items[index].IsHeader {
		return m, nil
	}
	m.planCursor = index
	m.planOffset = pv.ScrollOffset
	return m.openDiff()
}

func (m Model) openDiff() (common.Screen, tea.Cmd) {
	pv := m.planView(m.planPanelWidth(), m.panelHeight())
	if m.planCursor < 0 || m.planCursor >= len(pv.Items) || pv.Items[m.planCursor].IsHeader {
		return m, nil
	}
	return m, func() tea.Msg {
		return common.SyncDiffRequestMsg{
			ProfileName:  m.snapshot.ProfileName,
			ProjectDir:   m.snapshot.Target.ProjectDir,
			Ops:          append([]app.PlanOp(nil), m.snapshot.Target.Ops...),
			Cursor:       m.planCursor,
			ScrollOffset: m.planOffset,
		}
	}
}
