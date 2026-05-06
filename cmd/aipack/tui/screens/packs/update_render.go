package packs

import (
	"fmt"
	"slices"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
)

// rowClearTTL is how long a terminal row's outcome glyph stays visible
// before reverting to the clean installed state. Long enough that a user
// glancing back at the screen still sees what completed in the last
// batch, short enough that visual debt doesn't accumulate.
const rowClearTTL = 10 * time.Second

// applyProgress folds a single PackUpdateEvent into the per-row state
// machine and updates the model's batch counters. Returns the model and
// any tea.Cmd that needs scheduling for terminal auto-clear ticks.
func (m Model) applyProgress(e app.PackUpdateEvent) (Model, tea.Cmd) {
	if e.Pack == "" {
		return m, nil
	}
	idx, ok := m.installedMap[e.Pack]
	if !ok {
		return m, nil
	}
	prev := m.items[idx].updateState
	it := &m.items[idx]
	it.updateErr = e.Err

	if e.Phase != "" {
		it.updateType = string(e.Phase)
		it.updateState = asyncLoading
		it.updateClearAt = time.Time{}
		// Each row counts as in-flight at most once across its lifecycle in
		// this batch. Re-entering loading after a clear is rare enough to
		// ignore for the counter (the counter is a UX hint, not bookkeeping).
		if prev != asyncLoading {
			m.batchInFlight++
		}
		return m, nil
	}

	if e.Result != nil {
		it.updateType = string(e.Result.Status)
	}
	if e.Err != nil || (e.Result != nil && e.Result.Status == app.StatusError) {
		it.updateState = asyncError
		clearAt := time.Now().Add(rowClearTTL)
		it.updateClearAt = clearAt
		if prev == asyncLoading {
			m.batchInFlight--
		}
		m.batchDone++
		return m, scheduleRowClear(e.Pack, clearAt)
	}

	if e.Result != nil {
		it.updateState = asyncLoaded
		clearAt := time.Now().Add(rowClearTTL)
		it.updateClearAt = clearAt
		if prev == asyncLoading {
			m.batchInFlight--
		}
		m.batchDone++
		return m, scheduleRowClear(e.Pack, clearAt)
	}

	return m, nil
}

// applyRowClear handles a packRowClearMsg: revert the row's update state
// to idle if the recorded clearAt still matches. Mismatched clearAt means
// the row was re-loaded between the schedule and the fire, so the tick
// is stale and dropped. Both successful and error terminals are eligible
// for clearing — the user wants every glyph to disappear after the TTL.
func (m Model) applyRowClear(msg packRowClearMsg) Model {
	idx, ok := m.installedMap[msg.PackName]
	if !ok {
		return m
	}
	it := &m.items[idx]
	if it.updateState != asyncLoaded && it.updateState != asyncError {
		return m
	}
	if !it.updateClearAt.Equal(msg.When) {
		return m
	}
	it.updateState = asyncPending
	it.updateType = ""
	it.updateErr = nil
	it.updateClearAt = time.Time{}
	return m
}

// scheduleRowClear returns a tea.Cmd that fires a packRowClearMsg after
// rowClearTTL. The when parameter encodes the freshness signal: applyRowClear
// drops the message if the row's recorded clearAt no longer matches.
func scheduleRowClear(packName string, when time.Time) tea.Cmd {
	return tea.Tick(rowClearTTL, func(time.Time) tea.Msg {
		return packRowClearMsg{PackName: packName, When: when}
	})
}

// packsBatchStatus formats the status-bar batch summary line for an
// in-flight pack update operation. Pending = total - in-flight - done.
func packsBatchStatus(m Model) string {
	total := m.batchTotal
	if total <= 0 {
		total = m.batchInFlight + m.batchDone
	}
	pending := max(total-m.batchInFlight-m.batchDone, 0)
	return fmt.Sprintf(
		"Updating: %d in flight, %d done, %d pending — Esc to cancel",
		m.batchInFlight, m.batchDone, pending,
	)
}

// installPhaseLabel maps a PackInstallPhase to a human status-bar verb.
// Empty phase falls back to "installing" so the string is never empty
// while activeInstall is non-nil.
func installPhaseLabel(phase app.PackInstallPhase) string {
	switch phase {
	case app.PackInstallPhaseProbing:
		return "probing"
	case app.PackInstallPhaseResolving:
		return "resolving version"
	case app.PackInstallPhaseCloning:
		return "cloning"
	case app.PackInstallPhaseExtracting:
		return "extracting"
	case app.PackInstallPhaseLinking:
		return "linking"
	case app.PackInstallPhaseCopying:
		return "copying"
	case app.PackInstallPhaseIndexing:
		return "indexing"
	default:
		return "installing"
	}
}

// packsInstallStatus formats the status-bar progress line for an in-flight
// pack install. The pack name is best-effort (req.Name when explicit, else
// the URL/path the user supplied).
func packsInstallStatus(m Model) string {
	if m.activeInstall == nil {
		return ""
	}
	verb := installPhaseLabel(m.activeInstall.phase)
	name := m.activeInstall.packName
	if name == "" {
		name = "pack"
	}
	return fmt.Sprintf("%s %s — Esc to cancel", verb, name)
}

// anyPackUpdated reports whether any result represents a real content
// change (cloned, copied, or otherwise updated). Up-to-date and pinned
// outcomes don't count — they don't change pack content, so the user
// doesn't need to sync.
func anyPackUpdated(results []app.PackUpdateResult) bool {
	for _, r := range results {
		if r.Status == app.StatusUpdated {
			return true
		}
	}
	return false
}

// isCancelledResult reports whether a result represents a user-initiated
// cancellation (Esc during update). Cancellation is render-derived from
// results — there's no pack.update.cancelled emit site — so the test is
// structural (StatusError + Message == "cancelled") rather than typed.
func isCancelledResult(r app.PackUpdateResult) bool {
	return r.Status == app.StatusError && r.Message == "cancelled"
}

// anyCancelled reports whether any result was cancelled. Used by the
// status-text branch to switch from "update error" to "Cancelled update"
// after Esc-cancellation; partial success is preserved alongside any
// cancelled-mid-flight rows.
func anyCancelled(results []app.PackUpdateResult) bool {
	return slices.ContainsFunc(results, isCancelledResult)
}

// applyCancellation walks results and clears the per-row state for any
// cancelled row, returning it to the idle render (no glyph, no text).
// Cancellation acknowledgment lives in the status bar ("Cancelled
// update"), not in the rows — per the user's preference for minimal
// row decoration.
func (m Model) applyCancellation(results []app.PackUpdateResult) Model {
	for _, r := range results {
		if !isCancelledResult(r) {
			continue
		}
		idx, ok := m.installedMap[r.Name]
		if !ok {
			continue
		}
		it := &m.items[idx]
		it.updateState = asyncPending
		it.updateType = ""
		it.updateErr = nil
		it.updateClearAt = time.Time{}
	}
	return m
}

// reconcileFromResults backfills per-row state from the terminal results
// for any row still stuck in asyncLoading. Most rows transition out via
// applyProgress as terminal events arrive, but code paths that return a
// result without emitting a terminal event (StatusSkipped on unknown
// methods, future migrations) would leave the spinner spinning forever.
// This sweep ensures every row reaches a stable terminal decoration and
// returns auto-clear tea.Cmds for the freshly-transitioned rows.
//
// Rows already in asyncLoaded or asyncError (terminal event arrived
// first) are left alone — applyProgress already set their state.
func (m Model) reconcileFromResults(results []app.PackUpdateResult) (Model, []tea.Cmd) {
	var cmds []tea.Cmd
	for _, r := range results {
		if isCancelledResult(r) {
			continue // applyCancellation owns these
		}
		idx, ok := m.installedMap[r.Name]
		if !ok {
			continue
		}
		it := &m.items[idx]
		if it.updateState != asyncLoading {
			continue
		}
		switch r.Status {
		case app.StatusUpdated:
			it.updateState = asyncLoaded
			it.updateType = string(app.StatusUpdated)
			it.updateClearAt = time.Now().Add(rowClearTTL)
			cmds = append(cmds, scheduleRowClear(r.Name, it.updateClearAt))
		case app.StatusUpToDate:
			it.updateState = asyncLoaded
			it.updateType = string(app.StatusUpToDate)
			it.updateClearAt = time.Now().Add(rowClearTTL)
			cmds = append(cmds, scheduleRowClear(r.Name, it.updateClearAt))
		case app.StatusError:
			it.updateState = asyncError
			it.updateClearAt = time.Now().Add(rowClearTTL)
			cmds = append(cmds, scheduleRowClear(r.Name, it.updateClearAt))
		default:
			// StatusSkipped or unknown — wipe the loading decoration
			// quietly so the row reverts to clean.
			it.updateState = asyncPending
			it.updateType = ""
			it.updateErr = nil
			it.updateClearAt = time.Time{}
		}
	}
	return m, cmds
}

// formatRowSuffix returns the per-row update decoration that follows a pack
// name in the list panel. Minimal by design — terse glyphs only, no phase
// labels or detail text. Empty for idle and cancelled rows.
//
//   - in-flight: animated spinner glyph
//   - terminal success: green ✓
//   - terminal cancellation: nothing (row looks identical to idle)
//   - terminal error: red ✗
func formatRowSuffix(it packItemDetail, sp spinner.Model) string {
	switch it.updateState {
	case asyncLoading:
		return sp.View()
	case asyncLoaded:
		return successStyle.Render("✓")
	case asyncError:
		return common.ErrorStyle.Render("✗")
	default:
		return ""
	}
}
