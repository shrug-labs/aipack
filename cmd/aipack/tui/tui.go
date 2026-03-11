package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
)

// RunConfig provides the TUI with its initial configuration.
type RunConfig struct {
	ConfigDir string
	SyncCfg   config.SyncConfig
	Registry  *harness.Registry
}

// RunResult carries post-TUI actions back to the CLI layer.
type RunResult struct {
	SyncRequested bool
	ProfileName   string
	ProfilePath   string
	Scope         string
	Harness       string
}

// Run starts the TUI program in alt-screen mode.
func Run(cfg RunConfig) (RunResult, error) {
	m := newRootModel(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return RunResult{}, err
	}
	rm := final.(rootModel)
	return rm.runResult, rm.exitErr
}

type tabID int

const (
	tabProfiles tabID = iota
	tabPacks
	tabSave
	tabSync
	tabSearch
)

const tabCount = 5

const (
	dialogNewProfile        = "new-profile"
	dialogDuplicateProfile  = "duplicate-profile"
	dialogDeleteProfile     = "delete-profile"
	dialogActivateProfile   = "activate-profile"
	dialogSaveOnExit        = "save-on-exit"
	dialogSyncOnExit        = "sync-on-exit"
	dialogSyncScope         = "sync-scope"
	dialogSyncHarness       = "sync-harness"
	dialogSyncSelectProfile = "sync-select-profile"
	dialogAddPack           = "add-pack"
	dialogRemovePack        = "remove-pack"
	dialogPackAdd           = "pack-add"
	dialogPackRemove        = "pack-remove"
	dialogWarnings          = "warnings"
	dialogActionSave        = "action-save"
	dialogSaveAddToPack     = "save-add-to-pack"
	dialogActionContent     = "action-content"
	dialogContentMoveTo     = "content-move-to"
	dialogSearchInstall     = "search-install"
)

type rootModel struct {
	cfg       RunConfig
	activeTab tabID

	profiles profilesModel
	packs    packsModel
	saveTab  saveTabModel
	syncTab  syncTabModel
	search   searchTabModel

	dialog     *dialogModel
	preview    *previewModel  // full-screen markdown preview overlay
	planView   *planViewModel // full-screen sync plan overlay
	dirty      bool
	quitting   bool
	exitErr    error
	statusText string // transient status message (cleared on next key press)
	width      int
	height     int

	// Exit-flow state.
	pendingExit     bool
	pendingSaves    int
	exitSyncScope   string
	exitSyncHarness string
	runResult       RunResult

	// Dialog chain state.
	pendingSync       bool   // true when save-before-sync is in progress
	pendingCursorHint string // name hint for cursor position after profile create

	// Save plan state: stashed context for executing save after user confirms.
	savePlanCtx *savePlanContext

	// Search install state: pack name stashed between dialog open and confirm.
	pendingSearchInstall string
}

func newRootModel(cfg RunConfig) rootModel {
	return rootModel{
		cfg:      cfg,
		profiles: newProfilesModel(cfg.ConfigDir),
		packs:    newPacksModel(cfg.ConfigDir),
		saveTab:  newSaveTabModel(cfg.ConfigDir),
		syncTab:  newSyncTabModel(cfg.ConfigDir),
		search:   newSearchTabModel(cfg.ConfigDir),
	}
}

func (m rootModel) Init() tea.Cmd {
	return tea.Batch(
		m.profiles.initCmd(m.cfg.SyncCfg),
		m.packs.Init(),
	)
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Dialog result must be handled before the dialog intercept,
	// otherwise the dialog swallows its own result message.
	if msg, ok := msg.(dialogResultMsg); ok {
		return m.handleDialogResult(msg)
	}

	// Preview overlay message handling.
	switch msg := msg.(type) {
	case previewRequestMsg:
		p := newPreviewModel(m.width, m.height)
		m.preview = &p
		return m, loadPreview(msg.title, msg.category, msg.packName, msg.filePath)
	case previewLoadedMsg:
		if m.preview != nil {
			m.preview.setContent(msg)
		}
		return m, nil
	case editorFinishedMsg:
		if msg.err != nil {
			m.statusText = errorStyle.Render(fmt.Sprintf("editor: %v", msg.err))
		}
		// Re-read the file to refresh preview after editing.
		if m.preview != nil {
			return m, loadPreview(m.preview.title, m.preview.category, m.preview.packName, m.preview.filePath)
		}
		// Reload sync-config in case it was edited.
		if cfg, err := config.LoadSyncConfig(config.SyncConfigPath(m.cfg.ConfigDir)); err == nil {
			m.cfg.SyncCfg = cfg
		}
		// Reload all data after editing a config file.
		return m, tea.Batch(
			loadProfiles(m.cfg.ConfigDir, m.cfg.SyncCfg),
			loadPacks(m.cfg.ConfigDir),
			loadRegistry(m.cfg.ConfigDir),
		)
	case requestAddPackMsg:
		names := m.unregisteredPacks()
		if len(names) > 0 {
			d := newListSelectDialog(dialogAddPack, "Add pack to profile:", names)
			m.dialog = &d
		} else {
			m.statusText = dimStyle.Render("no unregistered packs available")
		}
		return m, nil
	case requestSearchInstallMsg:
		m.pendingSearchInstall = msg.packName
		d := newConfirmDialog(dialogSearchInstall, fmt.Sprintf("Install pack %q and register in active profile?", msg.packName))
		m.dialog = &d
		return m, nil
	case searchInstallMsg:
		if msg.err != nil {
			m.statusText = errorStyle.Render(fmt.Sprintf("install error: %v", msg.err))
		} else {
			m.statusText = dimStyle.Render(fmt.Sprintf("installed %s", msg.name))
		}
		// Reload packs, profiles, and re-run the current search to reflect new install state.
		var cmds []tea.Cmd
		cmds = append(cmds, loadPacks(m.cfg.ConfigDir), loadProfiles(m.cfg.ConfigDir, m.cfg.SyncCfg))
		if m.search.searched {
			cmds = append(cmds, runSearch(m.cfg.ConfigDir, m.search.query, m.search.kindFilter, m.search.categoryFilter, m.search.installedFilter))
		}
		return m, tea.Batch(cmds...)
	default:
	}

	// Plan view overlay captures all input when active.
	if m.planView != nil {
		return m.updatePlanView(msg)
	}

	// Preview captures all input when active.
	if m.preview != nil {
		return m.updatePreview(msg)
	}

	// Dialog captures all input when active.
	if m.dialog != nil {
		return m.updateDialog(msg)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.profiles.width = msg.Width
		m.profiles.height = msg.Height - 4
		m.packs.width = msg.Width
		m.packs.height = msg.Height - 4
		m.saveTab.width = msg.Width
		m.saveTab.height = msg.Height - 4
		m.syncTab.width = msg.Width
		m.syncTab.height = msg.Height - 4
		m.search.width = msg.Width
		m.search.height = msg.Height - 4
		return m, nil

	case tea.KeyMsg:
		m.statusText = "" // Clear transient status on any key press.

		// When search tab input is focused, only handle navigation keys globally;
		// delegate everything else so character input works.
		if m.activeTab == tabSearch && m.search.focus == searchFocusInput {
			switch msg.String() {
			case "tab", "shift+tab", "ctrl+c":
				// Fall through to normal global handling below.
			default:
				var cmd tea.Cmd
				m.search, cmd = m.search.Update(msg)
				return m, cmd
			}
		}

		switch msg.String() {
		case "tab":
			m.activeTab = (m.activeTab + 1) % tabCount
			if m.activeTab == tabSave {
				m.saveTab.loading = true
				return m, m.triggerInspect()
			}
			if m.activeTab == tabSearch && !m.search.searched && !m.search.loading {
				m.search.loading = true
				return m, runSearch(m.search.configDir, "", "", "", "")
			}
			return m, nil
		case "shift+tab":
			m.activeTab = (m.activeTab - 1 + tabCount) % tabCount
			if m.activeTab == tabSave {
				m.saveTab.loading = true
				return m, m.triggerInspect()
			}
			if m.activeTab == tabSearch && !m.search.searched && !m.search.loading {
				m.search.loading = true
				return m, runSearch(m.search.configDir, "", "", "", "")
			}
			return m, nil
		case "esc":
			// Let sub-models handle esc for internal navigation.
			if m.activeTab == tabProfiles && m.profiles.focus != panelProfiles {
				break
			}
			if m.activeTab == tabPacks && m.packs.focus != packPanelList {
				break
			}
			if m.activeTab == tabSearch && m.search.focus == searchFocusResults {
				break
			}
			return m.startExit()
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "ctrl+s":
			return m.startSave()
		case "s":
			if m.activeTab == tabProfiles || m.activeTab == tabSync || m.activeTab == tabSave {
				return m.startSync()
			}
		case "v":
			if m.activeTab == tabProfiles || m.activeTab == tabSync || m.activeTab == tabSave {
				return m.openPlanView()
			}
		case "r":
			return m.refresh()
		case "w":
			return m.showWarnings()
		case "e", "i":
			return m.editCurrentFile()
		case ".":
			return m.openActionMenu()
		}

	case profileCreatedMsg:
		if msg.err == nil {
			m.pendingCursorHint = msg.name
			return m, loadProfiles(m.cfg.ConfigDir, m.cfg.SyncCfg)
		}
		m.statusText = errorStyle.Render(fmt.Sprintf("create error: %v", msg.err))
		return m, nil

	case profileDeletedMsg:
		if msg.err == nil {
			return m, loadProfiles(m.cfg.ConfigDir, m.cfg.SyncCfg)
		}
		m.statusText = errorStyle.Render(fmt.Sprintf("delete error: %v", msg.err))
		return m, nil

	case profileActivatedMsg:
		if msg.err == nil {
			m.setActiveProfile(msg.profileName)
		} else {
			m.statusText = errorStyle.Render(fmt.Sprintf("activate error: %v", msg.err))
		}
		return m, m.profiles.checkSyncCmd(m.cfg.SyncCfg, m.cfg.Registry)

	case profileSavedMsg:
		if msg.err != nil {
			m.statusText = errorStyle.Render(fmt.Sprintf("save error: %v", msg.err))
		} else {
			m.statusText = dimStyle.Render("saved")

			// Clear per-profile dirty flag and mark as needing sync re-check.
			for i := range m.profiles.items {
				if m.profiles.items[i].name == msg.profileName {
					m.profiles.items[i].syncState = syncUnsynced
					m.profiles.items[i].dirty = false
				}
			}

			// Derive global dirty from per-profile state.
			m.dirty = m.anyProfileDirty()
			m.profiles.dirty = m.dirty
		}

		// Handle pending exit/sync flow.
		if m.pendingExit {
			m.pendingSaves--
			if m.pendingSaves <= 0 {
				if m.anyProfileDirty() {
					// Some saves failed — abort exit, let user retry or discard.
					m.pendingExit = false
					m.pendingSync = false
					m.dirty = true
					m.statusText = errorStyle.Render("save failed; press q again to discard")
					return m, nil
				}
				if m.pendingSync {
					m.pendingSync = false
					return m.promptSync()
				}
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil
		}
		// Re-run sync check now that on-disk profile has changed.
		return m, m.profiles.checkSyncCmd(m.cfg.SyncCfg, m.cfg.Registry)

	}

	// Handle sync completion.
	if msg, ok := msg.(syncDoneMsg); ok {
		for i := range m.profiles.items {
			if m.profiles.items[i].name == msg.profileName {
				if msg.err != nil {
					m.profiles.items[i].syncState = syncError
					m.profiles.items[i].syncErrText = msg.err.Error()
					m.statusText = errorStyle.Render(fmt.Sprintf("sync error: %v", msg.err))
				} else {
					m.profiles.items[i].syncState = syncPending // reset so re-check can fire
					m.profiles.items[i].syncErrText = ""
					m.profiles.items[i].syncWarnings = msg.warnings
					m.statusText = dimStyle.Render(fmt.Sprintf("synced %d file(s)", msg.filesWritten))
				}
			}
		}
		// Re-check sync status and refresh save tab.
		cmds := []tea.Cmd{m.profiles.checkSyncCmd(m.cfg.SyncCfg, m.cfg.Registry)}
		if inspCmd := m.triggerInspect(); inspCmd != nil {
			m.saveTab.loading = true
			cmds = append(cmds, inspCmd)
		}
		return m, tea.Batch(cmds...)
	}

	// Handle save plan (dry-run) result.
	if msg, ok := msg.(savePlanMsg); ok {
		if msg.err != nil {
			m.statusText = errorStyle.Render(fmt.Sprintf("save error: %v", msg.err))
			return m, nil
		}
		if len(msg.ops) == 0 {
			m.statusText = dimStyle.Render("nothing to save — harness files match packs")
			return m, nil
		}
		// Stash context for confirm and open save plan view.
		m.savePlanCtx = &savePlanContext{
			profileName: msg.profileName,
			profilePath: msg.profilePath,
		}
		pv := newPlanViewModel(m.width, m.height, msg.profileName, "", msg.ops, true)
		m.planView = &pv
		m.statusText = ""
		return m, nil
	}

	// Handle save (round-trip) completion.
	if msg, ok := msg.(saveDoneMsg); ok {
		m.savePlanCtx = nil
		if msg.err != nil {
			m.statusText = errorStyle.Render(fmt.Sprintf("save error: %v", msg.err))
		} else {
			m.statusText = dimStyle.Render(fmt.Sprintf("saved %d, unchanged %d", msg.saved, msg.unchanged))
		}
		// Re-check sync status and refresh save tab since pack content changed.
		cmds := []tea.Cmd{m.profiles.checkSyncCmd(m.cfg.SyncCfg, m.cfg.Registry)}
		if inspCmd := m.triggerInspect(); inspCmd != nil {
			m.saveTab.loading = true
			cmds = append(cmds, inspCmd)
		}
		return m, tea.Batch(cmds...)
	}

	// Handle adopt-to-pack completion.
	if msg, ok := msg.(saveToPackMsg); ok {
		if msg.err != nil {
			m.statusText = errorStyle.Render(fmt.Sprintf("adopt error: %v", msg.err))
		} else {
			m.statusText = dimStyle.Render(fmt.Sprintf("added %s to %s", filepath.Base(msg.harnessPath), msg.packName))
		}
		// Refresh save tab + sync status since ledger/pack changed.
		cmds := []tea.Cmd{m.profiles.checkSyncCmd(m.cfg.SyncCfg, m.cfg.Registry)}
		if inspCmd := m.triggerInspect(); inspCmd != nil {
			m.saveTab.loading = true
			cmds = append(cmds, inspCmd)
		}
		return m, tea.Batch(cmds...)
	}

	// Handle move-to-pack completion.
	if msg, ok := msg.(moveToPackMsg); ok {
		if msg.err != nil {
			m.statusText = errorStyle.Render(fmt.Sprintf("move error: %v", msg.err))
		} else {
			m.statusText = dimStyle.Render(fmt.Sprintf("moved %s/%s from %s to %s", msg.category, msg.id, msg.fromPack, msg.toPack))
		}
		// Refresh packs, save tab, and sync status.
		cmds := []tea.Cmd{
			loadPacks(m.cfg.ConfigDir),
			m.profiles.checkSyncCmd(m.cfg.SyncCfg, m.cfg.Registry),
		}
		if inspCmd := m.triggerInspect(); inspCmd != nil {
			m.saveTab.loading = true
			cmds = append(cmds, inspCmd)
		}
		return m, tea.Batch(cmds...)
	}

	// Handle sync tab requesting profile selection dialog.
	if _, ok := msg.(syncProfileSelectMsg); ok {
		if len(m.syncTab.profileNames) > 0 {
			d := newListSelectDialog(dialogSyncSelectProfile, "Select profile:", m.syncTab.profileNames)
			m.dialog = &d
		}
		return m, nil
	}

	// Handle sync config intent messages from sync tab.
	if msg, ok := msg.(syncToggleHarnessMsg); ok {
		m.cfg.SyncCfg = toggleHarness(m.cfg.SyncCfg, msg.harness)
		return m, saveSyncConfig(m.cfg.ConfigDir, m.cfg.SyncCfg)
	}
	if _, ok := msg.(syncCycleScopeMsg); ok {
		m.cfg.SyncCfg = cycleScope(m.cfg.SyncCfg)
		return m, saveSyncConfig(m.cfg.ConfigDir, m.cfg.SyncCfg)
	}

	// Handle syncConfigSavedMsg: update config, re-run sync check.
	if msg, ok := msg.(syncConfigSavedMsg); ok {
		if msg.err == nil {
			m.cfg.SyncCfg = msg.syncCfg
		}
		return m, m.profiles.checkSyncCmd(m.cfg.SyncCfg, m.cfg.Registry)
	}

	// Handle pack lifecycle messages.
	if msg, ok := msg.(packAddedMsg); ok {
		if msg.err != nil {
			m.statusText = errorStyle.Render(fmt.Sprintf("add error: %v", msg.err))
		} else {
			m.statusText = dimStyle.Render(fmt.Sprintf("added %s", msg.name))
		}
		// Reload both packs and profiles — installing a pack may seed new profiles.
		return m, tea.Batch(
			loadPacks(m.cfg.ConfigDir),
			loadProfiles(m.cfg.ConfigDir, m.cfg.SyncCfg),
		)
	}
	if msg, ok := msg.(packRemovedMsg); ok {
		if msg.err != nil {
			m.statusText = errorStyle.Render(fmt.Sprintf("remove error: %v", msg.err))
		} else {
			m.statusText = dimStyle.Render(fmt.Sprintf("removed %s", msg.name))
		}
		// Reload packs and profiles (pack may have been in a profile).
		return m, tea.Batch(
			loadPacks(m.cfg.ConfigDir),
			loadProfiles(m.cfg.ConfigDir, m.cfg.SyncCfg),
		)
	}
	if msg, ok := msg.(packUpdatedMsg); ok {
		if msg.err != nil {
			m.statusText = errorStyle.Render(fmt.Sprintf("update error: %v", msg.err))
		} else if len(msg.results) > 0 {
			summaries := make([]string, len(msg.results))
			for i, r := range msg.results {
				summaries[i] = fmt.Sprintf("%s: %s", r.Name, r.Status)
			}
			m.statusText = dimStyle.Render(strings.Join(summaries, ", "))
		} else {
			m.statusText = dimStyle.Render("updated")
		}
		return m, loadPacks(m.cfg.ConfigDir)
	}

	// Route async data messages to the correct sub-model regardless of active tab.
	// Without this, messages arriving while the other tab is active get dropped.
	switch msg.(type) {
	case profilesLoadedMsg, syncStatusMsg, fileSizeMsg:
		var cmd tea.Cmd
		m.profiles, cmd = m.profiles.Update(msg)
		m.dirty = m.dirty || m.profiles.dirty
		if m.profiles.loadErr != "" {
			m.statusText = errorStyle.Render(m.profiles.loadErr)
		}
		// Route profilesLoadedMsg to sync tab for profile names,
		// and trigger initial sync check from rootModel.
		if _, ok := msg.(profilesLoadedMsg); ok {
			m.syncTab, _ = m.syncTab.Update(msg)
			// Apply cursor hint from profile create/rename.
			if m.pendingCursorHint != "" {
				for i, item := range m.profiles.items {
					if item.name == m.pendingCursorHint {
						m.profiles.cursor = i
						m.profiles = m.profiles.ensureTree()
						break
					}
				}
				m.pendingCursorHint = ""
			}
			syncCmd := m.profiles.checkSyncCmd(m.cfg.SyncCfg, m.cfg.Registry)
			if syncCmd != nil {
				cmd = tea.Batch(cmd, syncCmd)
			}
			if inspCmd := m.triggerInspect(); inspCmd != nil {
				m.saveTab.loading = true
				cmd = tea.Batch(cmd, inspCmd)
			}
		}
		return m, cmd
	case packsLoadedMsg, packSizesMsg, registryLoadedMsg:
		var cmd tea.Cmd
		m.packs, cmd = m.packs.Update(msg)
		if m.packs.loadErr != "" {
			m.statusText = errorStyle.Render(m.packs.loadErr)
		}
		return m, cmd
	case inspectResultMsg:
		var cmd tea.Cmd
		m.saveTab, cmd = m.saveTab.Update(msg)
		m.statusText = "" // clear transient status (e.g. "refreshing...")
		return m, cmd
	case searchResultsMsg:
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		return m, cmd
	}

	// Delegate interactive input to active tab only.
	var cmd tea.Cmd
	switch m.activeTab {
	case tabProfiles:
		m.profiles, cmd = m.profiles.Update(msg)
		m.dirty = m.dirty || m.profiles.dirty
		// Auto-save dirty profiles so on-disk config stays current.
		// The profileSavedMsg handler triggers checkSyncCmd after save,
		// which uses the in-memory config for an accurate prune-aware check.
		if m.profiles.dirty {
			if saveCmd := m.profiles.saveAll(); saveCmd != nil {
				cmd = tea.Batch(cmd, saveCmd)
			}
		}
	case tabPacks:
		m.packs, cmd = m.packs.Update(msg)
	case tabSave:
		m.saveTab, cmd = m.saveTab.Update(msg)
	case tabSync:
		m.syncTab.activeSync = m.activeSyncSnapshot()
		m.syncTab.syncCfg = m.cfg.SyncCfg
		m.syncTab, cmd = m.syncTab.Update(msg)
	case tabSearch:
		m.search, cmd = m.search.Update(msg)
	}
	return m, cmd
}

func (m rootModel) updatePreview(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = msg.Width
		m.height = msg.Height
		if m.preview.ready {
			m.preview.viewport.Width = msg.Width - 4
			m.preview.viewport.Height = msg.Height - 4
		}
		return m, nil
	}
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "esc", "q":
			m.preview = nil
			return m, nil
		}
	}
	p, cmd := m.preview.Update(msg)
	m.preview = &p
	return m, cmd
}

func (m rootModel) updatePlanView(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = msg.Width
		m.height = msg.Height
		// Delegate resize to planView (and nested diffView if active).
		pv, cmd := m.planView.Update(msg)
		m.planView = &pv
		return m, cmd
	}

	// When diff overlay is active inside plan view, delegate everything
	// (including esc) to plan view so it can close the diff first.
	if m.planView.diffView != nil {
		pv, cmd := m.planView.Update(msg)
		m.planView = &pv
		return m, cmd
	}

	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "esc", "q":
			if m.planView.isSavePlan {
				m.savePlanCtx = nil
				m.statusText = dimStyle.Render("save cancelled")
			}
			m.planView = nil
			return m, nil
		case "s":
			if m.planView.isSavePlan && m.savePlanCtx != nil {
				ctx := m.savePlanCtx
				m.planView = nil
				m.statusText = dimStyle.Render("saving...")
				return m, runSave(m.cfg.ConfigDir, ctx.profileName, ctx.profilePath, m.cfg.SyncCfg, m.cfg.Registry)
			}
		}
	}
	pv, cmd := m.planView.Update(msg)
	m.planView = &pv
	return m, cmd
}

// openPlanView creates the plan view overlay from the target profile's sync plan.
func (m rootModel) openPlanView() (tea.Model, tea.Cmd) {
	item := m.syncTargetItem()
	if item != nil && len(item.syncTarget.Ops) > 0 {
		pv := newPlanViewModel(m.width, m.height, item.name, item.syncTarget.projectDir, item.syncTarget.Ops, false)
		m.planView = &pv
		return m, nil
	}
	m.statusText = dimStyle.Render("no pending changes to view")
	return m, nil
}

func (m rootModel) updateDialog(msg tea.Msg) (tea.Model, tea.Cmd) {
	d, cmd := m.dialog.Update(msg)
	m.dialog = &d
	// Check if dialog produced a result.
	if cmd != nil {
		return m, cmd
	}
	return m, nil
}

func (m rootModel) handleDialogResult(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	m.dialog = nil
	switch msg.id {
	// Profile CRUD dialogs.
	case dialogNewProfile, dialogDuplicateProfile, dialogDeleteProfile,
		dialogActivateProfile:
		return m.handleProfileDialogResult(msg)
	// Sync flow dialogs.
	case dialogSaveOnExit, dialogSyncOnExit, dialogSyncScope, dialogSyncHarness, dialogSyncSelectProfile:
		return m.handleSyncDialogResult(msg)
	// Pack roster mutations (profile packs panel + pack tab installs).
	case dialogAddPack, dialogRemovePack, dialogPackAdd, dialogPackRemove:
		return m.handlePackDialogResult(msg)
	// Action menu results.
	case dialogActionProfile, dialogActionPack, dialogActionPackTab, dialogActionSync:
		return m.handleActionMenuResult(msg)
	// Save tab dialogs.
	case dialogActionSave:
		return m.handleSaveActionResult(msg)
	case dialogSaveAddToPack:
		return m.handleSaveAddToPack(msg)
	// Pack content dialogs.
	case dialogActionContent:
		return m.handlePackContentAction(msg)
	case dialogContentMoveTo:
		return m.handleContentMoveTo(msg)
	// Search install dialog.
	case dialogSearchInstall:
		if msg.confirmed && m.pendingSearchInstall != "" {
			name := m.pendingSearchInstall
			m.pendingSearchInstall = ""
			m.statusText = dimStyle.Render(fmt.Sprintf("installing %s...", name))
			profile := m.cfg.SyncCfg.Defaults.Profile
			if profile == "" {
				profile = "default"
			}
			return m, installFromSearch(m.cfg.ConfigDir, name, profile)
		}
		m.pendingSearchInstall = ""
	}
	return m, nil
}

func (m rootModel) handleProfileDialogResult(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	switch msg.id {
	case dialogNewProfile:
		if msg.confirmed && msg.value != "" {
			m.pendingCursorHint = msg.value
			return m, createProfile(m.cfg.ConfigDir, msg.value)
		}
	case dialogDuplicateProfile:
		if msg.confirmed && msg.value != "" {
			if item := m.profiles.currentItem(); item != nil {
				m.pendingCursorHint = msg.value
				return m, duplicateProfile(m.cfg.ConfigDir, item.name, msg.value)
			}
		}
	case dialogDeleteProfile:
		if msg.confirmed {
			if item := m.profiles.currentItem(); item != nil {
				return m, deleteProfile(m.cfg.ConfigDir, item.name)
			}
		}
	case dialogActivateProfile:
		if msg.confirmed {
			if item := m.profiles.currentItem(); item != nil {
				return m, activateProfile(m.cfg.ConfigDir, item.name)
			}
		}
	}
	return m, nil
}

func (m rootModel) handleSyncDialogResult(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	switch msg.id {
	case dialogSaveOnExit:
		m.quitting = true
		return m, tea.Quit
	case dialogSyncOnExit:
		if msg.confirmed {
			switch msg.value {
			case "Cancel":
				m.pendingExit = false
				return m, nil
			case "Customize...":
				scopes := []string{
					string(domain.ScopeProject),
					string(domain.ScopeGlobal),
				}
				d := newListSelectDialog(dialogSyncScope, "Select scope:", scopes)
				m.dialog = &d
				return m, nil
			default:
				return m.doSync("", "")
			}
		}
		m.pendingExit = false
		return m, nil
	case dialogSyncScope:
		if msg.confirmed && msg.value != "" {
			m.exitSyncScope = msg.value
			harnesses := domain.HarnessNames()
			d := newListSelectDialog(dialogSyncHarness, "Select harness:", harnesses)
			m.dialog = &d
			return m, nil
		}
		m.pendingExit = false
		return m, nil
	case dialogSyncHarness:
		if msg.confirmed && msg.value != "" {
			return m.doSync(m.exitSyncScope, msg.value)
		}
		m.pendingExit = false
		return m, nil
	case dialogSyncSelectProfile:
		if msg.confirmed && msg.value != "" {
			return m, tea.Batch(
				activateProfile(m.cfg.ConfigDir, msg.value),
				loadProfiles(m.cfg.ConfigDir, m.cfg.SyncCfg),
			)
		}
	}
	return m, nil
}

func (m rootModel) handlePackDialogResult(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	switch msg.id {
	case dialogAddPack:
		if msg.confirmed && msg.value != "" {
			m.profiles = m.profiles.addPackToProfile(msg.value)
			m.dirty = m.dirty || m.profiles.dirty
			var cmds []tea.Cmd
			cmds = append(cmds, m.profiles.computeFileSizesCmd())
			if item := m.profiles.currentItem(); item != nil && item.dirty {
				cmds = append(cmds, saveProfile(m.cfg.ConfigDir, item.name, item.cfg))
			}
			return m, tea.Batch(cmds...)
		}
	case dialogRemovePack:
		if msg.confirmed && msg.value != "" {
			m.profiles = m.profiles.removePackFromProfile(msg.value)
			m.dirty = m.dirty || m.profiles.dirty
			var cmds []tea.Cmd
			if item := m.profiles.currentItem(); item != nil && item.dirty {
				cmds = append(cmds, saveProfile(m.cfg.ConfigDir, item.name, item.cfg))
			}
			return m, tea.Batch(cmds...)
		}
	case dialogPackAdd:
		if msg.confirmed && msg.value != "" {
			m.statusText = dimStyle.Render("adding pack...")
			return m, addPack(m.cfg.ConfigDir, msg.value)
		}
	case dialogPackRemove:
		if msg.confirmed {
			if pi := m.packs.currentItem(); pi != nil {
				m.statusText = dimStyle.Render(fmt.Sprintf("removing %s...", pi.entry.Name))
				return m, removePack(m.cfg.ConfigDir, pi.entry.Name)
			}
		}
	}
	return m, nil
}

func (m rootModel) handleActionMenuResult(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	if !msg.confirmed {
		return m, nil
	}

	// Handle edit actions that span multiple dialog IDs.
	switch msg.value {
	case actEditSyncConfig:
		return m, openFileInEditor(filepath.Join(m.cfg.ConfigDir, "sync-config.yaml"))
	case actEditRegistry:
		return m, openFileInEditor(filepath.Join(m.cfg.ConfigDir, "registry.yaml"))
	}

	switch msg.id {
	case dialogActionProfile:
		if msg.value == actNewProfile {
			d := newTextInputDialog(dialogNewProfile, "New profile name:")
			m.dialog = &d
			return m, nil
		}
		item := m.profiles.currentItem()
		if item == nil {
			return m, nil
		}
		switch msg.value {
		case actEditFile:
			return m, openFileInEditor(item.path)
		case actDuplicate:
			d := newTextInputDialog(dialogDuplicateProfile,
				fmt.Sprintf("Duplicate %q as:", item.name))
			m.dialog = &d
		case actActivate:
			d := newConfirmDialog(dialogActivateProfile,
				fmt.Sprintf("Set %q as the active/default profile?", item.name))
			m.dialog = &d
		case actDelete:
			d := newConfirmDialog(dialogDeleteProfile,
				fmt.Sprintf("Delete profile %q?", item.name))
			m.dialog = &d
		}
	case dialogActionPack:
		switch msg.value {
		case actProfileAddPack:
			names := m.unregisteredPacks()
			if len(names) > 0 {
				d := newListSelectDialog(dialogAddPack, "Add pack to profile:", names)
				m.dialog = &d
			} else {
				m.statusText = dimStyle.Render("no unregistered packs available")
			}
		case actProfileRemovePack:
			names := m.profiles.profilePackNames()
			if len(names) > 0 {
				d := newListSelectDialog(dialogRemovePack, "Remove pack from profile:", names)
				m.dialog = &d
			} else {
				m.statusText = dimStyle.Render("no packs in profile")
			}
		case actSettingsSource:
			if item := m.profiles.currentItem(); item != nil && m.profiles.packCursor < len(item.cfg.Packs) {
				packName := item.cfg.Packs[m.profiles.packCursor].Name
				m.profiles = m.profiles.setSettingsSource(packName)
				m.dirty = m.dirty || m.profiles.dirty
				if item.dirty {
					return m, saveProfile(m.cfg.ConfigDir, item.name, item.cfg)
				}
			}
		case actEditManifest:
			if item := m.profiles.currentItem(); item != nil && m.profiles.packCursor < len(item.cfg.Packs) {
				pe := item.cfg.Packs[m.profiles.packCursor]
				return m, openFileInEditor(filepath.Join(m.cfg.ConfigDir, "packs", pe.Name, "pack.json"))
			}
		}
	case dialogActionSync:
		// actEditSyncConfig and actEditRegistry handled above.
		return m, nil
	case dialogActionPackTab:
		switch msg.value {
		case actEditManifest:
			if pi := m.packs.currentItem(); pi != nil {
				return m, openFileInEditor(filepath.Join(m.cfg.ConfigDir, "packs", pi.entry.Name, "pack.json"))
			}
		case actInstall:
			// Pre-fill with registry name if an uninstalled registry item is selected.
			prefill := ""
			if li := m.packs.currentListItem(); li != nil && !li.installed && li.inRegistry {
				prefill = li.name
			}
			d := newTextInputDialog(dialogPackAdd, "Pack name, path, or URL:")
			if prefill != "" {
				d.textValue = prefill
			}
			m.dialog = &d
			return m, nil
		case actPackDelete:
			if pi := m.packs.currentItem(); pi != nil {
				d := newConfirmDialog(dialogPackRemove,
					fmt.Sprintf("Delete pack %q from disk?", pi.entry.Name))
				m.dialog = &d
			}
			return m, nil
		case actUpdate:
			if pi := m.packs.currentItem(); pi != nil {
				m.statusText = dimStyle.Render(fmt.Sprintf("updating %s...", pi.entry.Name))
				return m, updatePack(m.cfg.ConfigDir, pi.entry.Name, false)
			}
		case actUpdateAll:
			m.statusText = dimStyle.Render("updating all packs...")
			return m, updatePack(m.cfg.ConfigDir, "", true)
		}
	}
	return m, nil
}

func (m rootModel) handleSaveActionResult(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	if !msg.confirmed {
		return m, nil
	}
	switch msg.value {
	case actEditFile:
		if f := m.saveTab.currentFile(); f != nil {
			return m, openFileInEditor(f.HarnessPath)
		}
	case actAddToPack:
		f := m.saveTab.currentFile()
		if f == nil || f.State != app.FileUntracked {
			return m, nil
		}
		// Offer list of packs from the active profile + "Create new pack...".
		item := m.profiles.activeItem()
		if item == nil {
			m.statusText = errorStyle.Render("no active profile")
			return m, nil
		}
		var names []string
		for _, pe := range item.cfg.Packs {
			names = append(names, pe.Name)
		}
		names = append(names, "Create new pack...")
		d := newListSelectDialog(dialogSaveAddToPack, fmt.Sprintf("Add %s to pack:", filepath.Base(f.HarnessPath)), names)
		m.dialog = &d
		return m, nil
	case actSaveModified:
		return m.startSave()
	}
	return m, nil
}

func (m rootModel) handleSaveAddToPack(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	if !msg.confirmed || msg.value == "" {
		return m, nil
	}
	f := m.saveTab.currentFile()
	if f == nil {
		return m, nil
	}
	packName := msg.value
	if packName == "Create new pack..." {
		// TODO: chain to a text input dialog for new pack name
		m.statusText = dimStyle.Render("create new pack not yet implemented")
		return m, nil
	}
	m.statusText = dimStyle.Render(fmt.Sprintf("adding to %s...", packName))
	return m, saveFileToPack(m.cfg.ConfigDir, f.HarnessPath, f.Category, f.RelPath, packName, m.cfg.SyncCfg)
}

// startExit initiates the exit flow: auto-save if dirty, then quit.
func (m rootModel) startExit() (tea.Model, tea.Cmd) {
	if m.dirty {
		cmd := m.profiles.saveAll()
		m.dirty = false
		m.profiles.dirty = false
		m.pendingExit = true
		m.pendingSaves = m.countPendingSaves()
		if m.pendingSaves == 0 {
			m.quitting = true
			return m, tea.Quit
		}
		return m, cmd
	}
	m.quitting = true
	return m, tea.Quit
}

// refresh reloads data relevant to the current tab and re-runs sync checks.
func (m rootModel) refresh() (tea.Model, tea.Cmd) {
	m.statusText = dimStyle.Render("refreshing...")
	var cmds []tea.Cmd
	// Always re-check sync status.
	if syncCmd := m.profiles.checkSyncCmd(m.cfg.SyncCfg, m.cfg.Registry); syncCmd != nil {
		cmds = append(cmds, syncCmd)
	}
	if inspCmd := m.triggerInspect(); inspCmd != nil {
		m.saveTab.loading = true
		cmds = append(cmds, inspCmd)
	}
	// Reload profiles, packs, and registry from disk.
	cmds = append(cmds, loadProfiles(m.cfg.ConfigDir, m.cfg.SyncCfg), loadPacks(m.cfg.ConfigDir), loadRegistry(m.cfg.ConfigDir))
	if len(cmds) == 0 {
		m.statusText = dimStyle.Render("nothing to refresh")
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

// triggerInspect fires an async harness inspection for the Save tab.
func (m rootModel) triggerInspect() tea.Cmd {
	item := m.profiles.activeItem()
	if item == nil {
		return nil
	}
	m.saveTab.loading = true
	return inspectHarness(m.cfg.ConfigDir, item.path, m.cfg.SyncCfg, m.cfg.Registry)
}

// startSave initiates the save flow: dry-run first, then show plan view.
func (m rootModel) startSave() (tea.Model, tea.Cmd) {
	item := m.syncTargetItem()
	if item == nil {
		m.statusText = dimStyle.Render("no active profile")
		return m, nil
	}
	m.statusText = dimStyle.Render("checking for changes...")
	return m, runSavePlan(m.cfg.ConfigDir, item.name, item.path, m.cfg.SyncCfg, m.cfg.Registry)
}

// startSync initiates the sync flow: auto-save if dirty, then prompt sync.
func (m rootModel) startSync() (tea.Model, tea.Cmd) {
	if m.dirty {
		cmd := m.profiles.saveAll()
		m.dirty = false
		m.profiles.dirty = false
		m.pendingExit = true
		m.pendingSaves = m.countPendingSaves()
		if m.pendingSaves == 0 {
			return m.promptSync()
		}
		// pendingSync flag differentiates from plain exit.
		m.pendingSync = true
		return m, cmd
	}
	return m.promptSync()
}

// doSync fires the async sync command for the target profile.
// Empty scope/harness means use defaults from syncCfg.
func (m rootModel) doSync(scope, harness string) (tea.Model, tea.Cmd) {
	item := m.syncTargetItem()
	if item == nil {
		return m, nil
	}

	// Use defaults from target info if not overridden.
	if scope == "" {
		scope = string(item.syncTarget.scope)
	}
	if scope == "" {
		scope = string(domain.ScopeProject)
	}
	if harness == "" {
		harness = strings.Join(item.syncTarget.harnesses, ",")
	}

	item.syncState = syncLoading
	m.statusText = dimStyle.Render("syncing...")
	m.pendingExit = false

	return m, runSync(m.cfg.ConfigDir, item.name, item.path, scope, harness, m.cfg.SyncCfg, m.cfg.Registry)
}

// promptSync shows the sync options dialog for the target profile.
func (m rootModel) promptSync() (tea.Model, tea.Cmd) {
	item := m.syncTargetItem()
	if item == nil {
		m.quitting = true
		return m, tea.Quit
	}

	n := item.syncTarget.TotalChanges()
	var title string
	if item.syncState == syncUnsynced && n > 0 {
		title = fmt.Sprintf("Sync %q (%d pending changes):", item.name, n)
	} else {
		title = fmt.Sprintf("Sync %q:", item.name)
	}

	// Build default sync label from target info.
	defaultLabel := "Sync"
	if len(item.syncTarget.harnesses) > 0 {
		defaultLabel = fmt.Sprintf("Sync (%s, %s)",
			strings.Join(item.syncTarget.harnesses, ","),
			item.syncTarget.scope)
	}

	options := []string{defaultLabel, "Customize...", "Cancel"}
	d := newListSelectDialog(dialogSyncOnExit, title, options)
	m.dialog = &d
	m.pendingExit = true // sync always exits after
	return m, nil
}

// syncTargetItem returns the profile that sync/plan/save actions should operate on.
// From the Sync tab it returns the active (default) profile; from the Profiles tab
// it returns the cursor-selected profile.
func (m rootModel) syncTargetItem() *profileItem {
	if m.activeTab == tabSync {
		return m.profiles.activeItem()
	}
	return m.profiles.currentItem()
}

// setActiveProfile optimistically marks a profile as active across all state.
func (m *rootModel) setActiveProfile(name string) {
	for i := range m.profiles.items {
		m.profiles.items[i].isActive = (m.profiles.items[i].name == name)
	}
	m.cfg.SyncCfg.Defaults.Profile = name
}

// activeSyncSnapshot returns the active profile's sync state as a snapshot.
func (m rootModel) activeSyncSnapshot() syncTabSnapshot {
	for _, item := range m.profiles.items {
		if item.isActive {
			return syncTabSnapshot{
				syncState:    item.syncState,
				syncTarget:   item.syncTarget,
				syncErrText:  item.syncErrText,
				syncWarnings: item.syncWarnings,
			}
		}
	}
	return syncTabSnapshot{}
}

// unregisteredPacks returns installed pack names not already in the current profile.
func (m rootModel) unregisteredPacks() []string {
	registered := map[string]bool{}
	for _, n := range m.profiles.profilePackNames() {
		registered[n] = true
	}
	var names []string
	for _, item := range m.packs.items {
		if !registered[item.entry.Name] {
			names = append(names, item.entry.Name)
		}
	}
	return names
}

// anyProfileDirty returns true if any profile has unsaved changes.
func (m rootModel) anyProfileDirty() bool {
	for _, item := range m.profiles.items {
		if item.dirty {
			return true
		}
	}
	return false
}

// countPendingSaves returns the number of profiles that will produce save commands.
func (m rootModel) countPendingSaves() int {
	count := 0
	for _, item := range m.profiles.items {
		if item.dirty {
			count++
		}
	}
	return count
}

// editCurrentFile opens $EDITOR for content files in browsing panels.
// Config/structural files are edited via action menus instead.
func (m rootModel) editCurrentFile() (tea.Model, tea.Cmd) {
	switch m.activeTab {
	case tabProfiles:
		if m.profiles.focus == panelTree {
			if item := m.profiles.currentItem(); item != nil && item.tree != nil {
				if fp := item.tree.filePath(); fp != "" {
					return m, openFileInEditor(fp)
				}
			}
		}
	case tabPacks:
		if m.packs.focus == packPanelContent {
			if m.packs.contentCursor >= 0 && m.packs.contentCursor < len(m.packs.contentItems) {
				ci := m.packs.contentItems[m.packs.contentCursor]
				if !ci.isHeader {
					if fp := m.packs.contentFilePath(ci); fp != "" {
						return m, openFileInEditor(fp)
					}
				}
			}
		}
	}
	return m, nil
}

func (m rootModel) View() string {
	if m.quitting {
		return ""
	}

	// Plan view overlay takes over the full screen.
	if m.planView != nil {
		help := helpBarStyle.Render(m.planView.helpText())
		h := max(0, m.height-lipgloss.Height(help))
		content := lipgloss.NewStyle().Height(h).MaxHeight(h).Render(m.planView.View())
		return content + "\n" + help
	}

	// Preview overlay takes over the full screen.
	if m.preview != nil {
		help := helpBarStyle.Render(m.preview.helpText())
		h := max(0, m.height-lipgloss.Height(help))
		content := lipgloss.NewStyle().Height(h).MaxHeight(h).Render(m.preview.View())
		return content + "\n" + help
	}

	// Compute active profile snapshot once for tab bar + sync tab.
	snap := m.activeSyncSnapshot()

	// Tab bar.
	tabLabels := m.tabNames(snap.syncState)
	var tabs []string
	for i, name := range tabLabels {
		if tabID(i) == m.activeTab {
			tabs = append(tabs, activeTabStyle.Render(name))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(name))
		}
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Bottom, tabs...)
	gap := tabGapStyle.Render(strings.Repeat(" ", max(0, m.width-lipgloss.Width(tabBar))))
	tabBar = lipgloss.JoinHorizontal(lipgloss.Bottom, tabBar, gap)

	// Content — dialog replaces tab content when active.
	var content string
	var help string
	if m.dialog != nil {
		content = contentStyle.Render("\n" + m.dialog.View() + "\n")
		help = helpBarStyle.Render(m.dialog.helpText())
	} else {
		switch m.activeTab {
		case tabProfiles:
			content = m.profiles.View()
		case tabPacks:
			content = m.packs.View()
		case tabSave:
			content = m.saveTab.View()
		case tabSync:
			m.syncTab.activeSync = snap
			m.syncTab.syncCfg = m.cfg.SyncCfg
			content = m.syncTab.View()
		case tabSearch:
			content = m.search.View()
		}
		help = helpBarStyle.Render(m.helpText())
	}

	// Fix content height so help bar is pinned to the bottom.
	contentH := m.height - lipgloss.Height(tabBar) - lipgloss.Height(help)
	if m.statusText != "" {
		contentH -= lipgloss.Height(m.statusText)
	}
	content = lipgloss.NewStyle().Height(max(0, contentH)).MaxHeight(max(0, contentH)).Render(content)

	if m.statusText != "" {
		return fmt.Sprintf("%s\n%s\n%s\n%s", tabBar, content, m.statusText, help)
	}
	return fmt.Sprintf("%s\n%s\n%s", tabBar, content, help)
}

// --- Action menu system ---

const (
	dialogActionProfile = "action-profile"
	dialogActionPack    = "action-pack"
	dialogActionPackTab = "action-pack-tab"
	dialogActionSync    = "action-sync"
)

// Action menu item labels — used in both open*Actions() and handleDialogResult().
const (
	actNewProfile     = "New profile"
	actDuplicate      = "Duplicate"
	actActivate       = "Activate"
	actDelete         = "Delete"
	actSettingsSource = "Settings source"
	// Profile pack roster actions (pack add/remove = profile membership).
	actProfileAddPack    = "Add to profile"
	actProfileRemovePack = "Remove from profile"
	// Packs tab actions (pack install/delete = disk operations).
	actInstall    = "Install"
	actPackDelete = "Delete"
	actUpdate     = "Update"
	actUpdateAll  = "Update all"
	// Save tab actions.
	actAddToPack    = "Add to pack"
	actMoveToPack   = "Move to pack"
	actSaveModified = "Save modified"
	// Edit actions (open in $EDITOR).
	actEditFile       = "Edit file"
	actEditManifest   = "Edit manifest"
	actEditSyncConfig = "Edit sync-config"
	actEditRegistry   = "Edit registry"
)

// openActionMenu opens a context-sensitive action dialog based on the current focus.
func (m rootModel) openActionMenu() (tea.Model, tea.Cmd) {
	switch m.activeTab {
	case tabProfiles:
		switch m.profiles.focus {
		case panelProfiles:
			return m.openProfileActions()
		case panelPacks:
			return m.openPackRosterActions()
		}
	case tabSave:
		return m.openSaveActions()
	case tabPacks:
		if m.packs.focus == packPanelList {
			return m.openPackTabActions()
		}
		if m.packs.focus == packPanelContent {
			return m.openPackContentActions()
		}
	case tabSync:
		return m.openSyncActions()
	}
	return m, nil
}

func (m rootModel) openProfileActions() (tea.Model, tea.Cmd) {
	item := m.profiles.currentItem()
	if item == nil {
		d := newListSelectDialog(dialogActionProfile, "Profile actions:", []string{actNewProfile})
		m.dialog = &d
		return m, nil
	}
	actions := []string{actNewProfile, actDuplicate}
	if !item.isActive {
		actions = append(actions, actActivate)
	}
	actions = append(actions, actDelete, actEditFile)
	d := newListSelectDialog(dialogActionProfile, "Profile actions:", actions)
	m.dialog = &d
	return m, nil
}

func (m rootModel) openPackRosterActions() (tea.Model, tea.Cmd) {
	var actions []string
	names := m.unregisteredPacks()
	if len(names) > 0 {
		actions = append(actions, actProfileAddPack)
	}
	if len(m.profiles.profilePackNames()) > 0 {
		actions = append(actions, actProfileRemovePack)
	}
	// Offer "Settings source" if the selected pack has harness configs.
	if item := m.profiles.currentItem(); item != nil && item.tree != nil && m.profiles.packCursor < len(item.cfg.Packs) {
		packIdx := m.profiles.packCursor
		for _, pi := range item.tree.packs {
			if pi.idx == packIdx && pi.manifest.Configs.HasAnyConfigs() {
				actions = append(actions, actSettingsSource)
				break
			}
		}
	}
	actions = append(actions, actEditManifest)
	if len(actions) == 0 {
		m.statusText = dimStyle.Render("no actions available")
		return m, nil
	}
	d := newListSelectDialog(dialogActionPack, "Pack actions:", actions)
	m.dialog = &d
	return m, nil
}

func (m rootModel) openPackTabActions() (tea.Model, tea.Cmd) {
	var actions []string
	li := m.packs.currentListItem()
	if li != nil && li.installed {
		actions = append(actions, actPackDelete, actUpdate, actEditManifest)
	}
	actions = append(actions, actInstall)
	if len(m.packs.items) > 0 {
		actions = append(actions, actUpdateAll)
	}
	actions = append(actions, actEditRegistry)
	d := newListSelectDialog(dialogActionPackTab, "Pack actions:", actions)
	m.dialog = &d
	return m, nil
}

func (m rootModel) openPackContentActions() (tea.Model, tea.Cmd) {
	if m.packs.contentCursor < 0 || m.packs.contentCursor >= len(m.packs.contentItems) {
		m.statusText = dimStyle.Render("no actions available")
		return m, nil
	}
	ci := m.packs.contentItems[m.packs.contentCursor]
	if ci.isHeader {
		m.statusText = dimStyle.Render("no actions available")
		return m, nil
	}
	var actions []string
	actions = append(actions, actEditFile)
	// Only offer move if there are other installed packs.
	if len(m.packs.items) >= 2 {
		actions = append(actions, actMoveToPack)
	}
	d := newListSelectDialog(dialogActionContent,
		fmt.Sprintf("Actions for %s/%s:", ci.category, ci.id),
		actions)
	m.dialog = &d
	return m, nil
}

func (m rootModel) handlePackContentAction(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	if !msg.confirmed {
		return m, nil
	}
	if msg.value == actEditFile {
		if m.packs.contentCursor >= 0 && m.packs.contentCursor < len(m.packs.contentItems) {
			ci := m.packs.contentItems[m.packs.contentCursor]
			if !ci.isHeader {
				if fp := m.packs.contentFilePath(ci); fp != "" {
					return m, openFileInEditor(fp)
				}
			}
		}
		return m, nil
	}
	if msg.value != actMoveToPack {
		return m, nil
	}
	pi := m.packs.currentItem()
	if pi == nil {
		return m, nil
	}
	ci := m.packs.contentItems[m.packs.contentCursor]

	// Build list of other installed packs as move targets.
	var targets []string
	for _, item := range m.packs.items {
		if item.entry.Name != pi.entry.Name {
			targets = append(targets, item.entry.Name)
		}
	}
	if len(targets) == 0 {
		m.statusText = dimStyle.Render("no other packs")
		return m, nil
	}
	d := newListSelectDialog(dialogContentMoveTo,
		fmt.Sprintf("Move %s/%s to:", ci.category, ci.id), targets)
	m.dialog = &d
	return m, nil
}

func (m rootModel) handleContentMoveTo(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	if !msg.confirmed || msg.value == "" {
		return m, nil
	}
	pi := m.packs.currentItem()
	if pi == nil {
		return m, nil
	}
	ci := m.packs.contentItems[m.packs.contentCursor]
	toPack := msg.value
	fromPack := pi.entry.Name

	m.statusText = dimStyle.Render(fmt.Sprintf("moving %s/%s to %s...", ci.category, ci.id, toPack))
	return m, moveContentToPack(m.cfg.ConfigDir, ci.id, ci.category, fromPack, toPack, m.cfg.SyncCfg)
}

func (m rootModel) openSyncActions() (tea.Model, tea.Cmd) {
	d := newListSelectDialog(dialogActionSync, "Sync actions:", []string{actEditSyncConfig, actEditRegistry})
	m.dialog = &d
	return m, nil
}

func (m rootModel) openSaveActions() (tea.Model, tea.Cmd) {
	f := m.saveTab.currentFile()
	if f == nil {
		m.statusText = dimStyle.Render("no actions available")
		return m, nil
	}
	actions := []string{actEditFile}
	switch f.State {
	case app.FileUntracked:
		actions = append(actions, actAddToPack)
	case app.FileModified, app.FileConflict, app.FileSettings:
		actions = append(actions, actSaveModified)
	default:
		// Current file is clean — offer bulk save if there are pending changes.
		if m.saveTab.countModified+m.saveTab.countSettings > 0 {
			actions = append(actions, actSaveModified)
		}
	}
	d := newListSelectDialog(dialogActionSave, "Save actions:", actions)
	m.dialog = &d
	return m, nil
}

// tabNames returns the tab labels with dynamic status dot for the Sync tab.
func (m rootModel) tabNames(state syncStatus) []string {
	syncLabel := "Sync"
	switch state {
	case syncSynced:
		syncLabel = "Sync " + statusDotActive
	case syncUnsynced:
		syncLabel = "Sync " + statusDotInactive
	case syncLoading:
		syncLabel = "Sync " + statusDotLoading
	case syncError:
		syncLabel = "Sync " + statusDotInactive
	}
	saveLabel := "Save"
	pending := m.saveTab.pendingCount()
	if pending > 0 {
		saveLabel = fmt.Sprintf("Save %s", statusDotInactive)
	} else if len(m.saveTab.files) > 0 {
		saveLabel = "Save " + statusDotActive
	}
	return []string{"Profiles", "Packs", saveLabel, syncLabel, "Search"}
}

func (m rootModel) showWarnings() (tea.Model, tea.Cmd) {
	item := m.profiles.activeItem()
	if item == nil || len(item.syncWarnings) == 0 {
		m.statusText = dimStyle.Render("no warnings")
		return m, nil
	}
	lines := make([]string, len(item.syncWarnings))
	for i, w := range item.syncWarnings {
		lines[i] = w.String()
	}
	d := newListSelectDialog(dialogWarnings, fmt.Sprintf("Warnings (%d):", len(lines)), lines)
	m.dialog = &d
	return m, nil
}

// helpText returns context-sensitive key binding hints.
func (m rootModel) helpText() string {
	base := ""
	switch m.activeTab {
	case tabProfiles:
		switch m.profiles.focus {
		case panelProfiles:
			base = "j/k:navigate  enter:packs  .:actions  v:plan  s:sync  ctrl+s:save  r:refresh  tab:switch  esc:quit"
		case panelPacks:
			base = "j/k:navigate  space:toggle  enter:tree  .:actions  esc:back"
		case panelTree:
			base = "j/k:navigate  space:toggle  enter:preview  e:edit  v:plan  s:sync  ctrl+s:save  esc:back"
		}
	case tabPacks:
		switch m.packs.focus {
		case packPanelContent:
			return "j/k:navigate  enter:preview  e:edit  .:actions  esc:back"
		default:
			return "j/k:navigate  enter:content  .:actions  r:refresh  tab:switch  esc:quit"
		}
	case tabSave:
		base = "j/k:navigate  enter:preview  .:actions  ctrl+s:save  r:refresh  tab:switch  esc:quit"
	case tabSync:
		base = "j/k:navigate  space:toggle  .:actions  v:plan  s:sync  ctrl+s:save  r:refresh  tab:switch  esc:quit"
	case tabSearch:
		if m.search.focus == searchFocusInput {
			return "enter:search  down:results  ctrl+u:clear  tab:switch  ctrl+c:quit"
		}
		return "j/k:navigate  enter:preview  /:search  f:kind  space:show  tab:switch  esc:back"
	default:
		return "tab:switch  esc:quit"
	}
	if item := m.profiles.activeItem(); item != nil && len(item.syncWarnings) > 0 {
		base += "  " + warningStyle.Render("w:warnings")
	}
	return base
}
