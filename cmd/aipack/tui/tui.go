package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	tuiapp "github.com/shrug-labs/aipack/cmd/aipack/tui/app"
	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	configscreen "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/config"
	packsscreen "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/packs"
	profilescreen "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/profiles"
	savescreen "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/save"
	"github.com/shrug-labs/aipack/cmd/aipack/tui/screens/search"
	syncscreen "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/sync"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/source"
)

// RunConfig provides the TUI with its initial configuration.
type RunConfig struct {
	ConfigDir          string
	SyncCfg            config.SyncConfig
	Registry           *harness.Registry
	InitialTab         string
	InitialSearchQuery string
	InitialSearchKind  string
	InitialSearchCat   string
	InitialSearchState string
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
func Run(ctx context.Context, cfg RunConfig) (RunResult, error) {
	// Cancel on SIGHUP so Bubble Tea exits before macOS revokes the pty.
	// Without this, a closed terminal leaves the process stuck in an
	// uninterruptible read() on the defunct pty file descriptor.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGHUP)
	defer stop()

	m := newRootModel(ctx, cfg)
	p := tea.NewProgram(m, tea.WithContext(ctx))
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
	tabSync
	tabSave
	tabSearch
	tabConfig
)

const tabCount = 6

const (
	dialogNewProfile         = "new-profile"
	dialogDuplicateProfile   = "duplicate-profile"
	dialogDeleteProfile      = "delete-profile"
	dialogActivateProfile    = "activate-profile"
	dialogProfileParamSelect = "profile-param-select"
	dialogProfileParamEdit   = "profile-param-edit"
	dialogProfileParamAdd    = "profile-param-add"
	dialogProfileParamDelete = "profile-param-delete"
	dialogSaveOnExit         = "save-on-exit"
	dialogSyncOnExit         = "sync-on-exit"
	dialogSyncScope          = "sync-scope"
	dialogSyncHarness        = "sync-harness"
	dialogSyncSelectProfile  = "sync-select-profile"
	dialogConfigHarnesses    = "config-harnesses"
	dialogConfigEnvAdd       = "config-env-add"
	dialogConfigEnvEdit      = "config-env-edit"
	dialogConfigEnvDelete    = "config-env-delete"
	dialogAddPack            = "add-pack"
	dialogRemovePack         = "remove-pack"
	dialogPackInstall        = "pack-install"
	dialogPackRemove         = "pack-remove"
	dialogWarnings           = "warnings"
	dialogActionSave         = "action-save"
	dialogActionContent      = "action-content"
	dialogContentMoveTo      = "content-move-to"
	dialogCreatePack         = "create-pack"
	dialogDeleteSaveFile     = "delete-save-file"
	dialogActionTree         = "action-tree"
	dialogInstallWith        = "install-with"
	dialogInstallVersion     = "install-version"
	dialogUpdateWith         = "update-with"
	dialogPinVersion         = "pin-version"
	dialogPackAddToProfile   = "pack-add-to-profile"
	dialogMCPToolPicker      = "mcp-tool-picker"
	dialogMCPBulkAction      = "mcp-bulk-action"
)

type rootModel struct {
	ctx       context.Context
	cfg       RunConfig
	eng       *engine.Engine
	activeTab tabID

	router tuiapp.Router

	dialog               *dialogModel
	picker               *toolPicker    // MCP tool picker overlay (separate from dialog)
	preview              *previewModel  // full-screen markdown preview overlay
	planView             *planViewModel // full-screen sync plan overlay
	planViewDirectDiff   bool           // true when Sync tab opened a diff without an explicit plan overlay
	dirty                bool
	quitting             bool
	exitErr              error
	statusText           string           // transient status message (auto-cleared after timeout)
	statusID             int              // monotonic counter to match statusClearMsg to current status
	pendingReturnToTools bool             // when true, bulk menu returns to tool picker instead of tree
	mcpProbeSeq          int              // monotonic sequence for async MCP probe requests
	mcpProbeActive       *mcpProbeRequest // currently active MCP probe tied to the open picker
	mcpProbeStream       *activeMCPProbe  // streaming channels for the in-flight probe (M-toolpicker)
	width                int
	height               int

	// Exit-flow state.
	pendingExit   bool
	pendingSaves  int
	exitSyncScope string
	runResult     RunResult

	// Dialog chain state.
	pendingSync            bool   // true when save-before-sync is in progress
	pendingCursorHint      string // name hint for cursor position after profile create
	pendingPackCursorHint  string // name hint for cursor position after pack install/create/update
	pendingPackContentKind string // content kind hint for search-to-packs drill-in
	pendingPackContentName string // content name hint for search-to-packs drill-in
	pendingProfileParamKey string // param key selected before value edit dialog
	pendingConfigEnvKey    string // .env key selected before value edit/delete dialog
	pendingConfigParamHint string // param key to keep selected after config refresh
	pendingConfigEnvHint   string // env key to keep selected after config refresh
	pendingAutoSync        *pendingAutoSyncState
	pendingSyncRecheck     string // active profile needing a fresh sync check after current loading check completes
	autoSyncSeq            int

	// Save plan state: stashed context for executing save after user confirms.
	savePlanCtx *savePlanContext

	// Save tab delete state: harness path stashed between dialog open and confirm.
	pendingDeletePath string

	// Per-flow chain state. Each flow groups the data carried across a
	// multi-dialog interaction so stale fields don't bleed across flows.
	installFlow installFlowState
	updateFlow  updateFlowState

	// Single-field chain stashes that don't share a flow with anything else.
	pendingCreateAddToProf bool // true when create-pack should add to current profile
}

// installFlowState is the chain of stashed values during pack install. Cleared
// after the install command is dispatched. Empty value == no install in flight.
type installFlowState struct {
	Input   string // stashed pack name/URL from text input
	Version string // stashed semver tag picked by version dialog ("" = default ref)
}

func (s *installFlowState) reset() { *s = installFlowState{} }

// updateFlowState is the chain of stashed values during pack update. PackName
// and All are cleared when the update command is dispatched; Results is
// populated later for the bundled-candidate dialog and cleared when that
// completes.
type updateFlowState struct {
	PackName string                 // empty means update-all is in progress
	All      bool                   // true when update-all is in progress
	Results  []app.PackUpdateResult // stashed for bundled candidate dialog
}

type pendingAutoSyncState struct {
	id          int
	profileName string
}

func newRootModel(ctx context.Context, cfg RunConfig) rootModel {
	eng := engine.New(nil, nil)
	searchScreen := search.NewWithInitial(
		cfg.ConfigDir,
		cfg.InitialSearchQuery,
		cfg.InitialSearchKind,
		cfg.InitialSearchCat,
		cfg.InitialSearchState,
	)
	m := rootModel{
		ctx: ctx,
		cfg: cfg,
		eng: eng,
		router: tuiapp.NewRouter().
			Set(tuiapp.ScreenProfiles, profilescreen.New(ctx, eng, cfg.ConfigDir)).
			Set(tuiapp.ScreenPacks, packsscreen.New(cfg.ConfigDir)).
			Set(tuiapp.ScreenConfig, configscreen.New(cfg.ConfigDir)).
			Set(tuiapp.ScreenSearch, searchScreen).
			Set(tuiapp.ScreenSave, savescreen.New(ctx, eng, cfg.ConfigDir, cfg.Registry)).
			Set(tuiapp.ScreenSync, syncscreen.New(cfg.ConfigDir)),
	}
	if cfg.InitialTab == "search" {
		m.activeTab = tabSearch
		m.router = m.router.SetActive(tuiapp.ScreenSearch)
	}
	return m
}

// Screen accessors. These type-assert against the router's registry; a panic
// here would indicate the router was constructed with the wrong concrete type
// for a screen — a programming error, never a runtime path.
func screenFrom[T common.Screen](m rootModel, id tuiapp.ScreenID) T {
	screen, ok := tuiapp.ScreenAs[T](m.router, id)
	if !ok {
		panic(fmt.Sprintf("screen %q registered with wrong concrete type", id))
	}
	return screen
}

func (m rootModel) searchScreen() search.Model {
	return screenFrom[search.Model](m, tuiapp.ScreenSearch)
}

func (m rootModel) profilesScreen() profilescreen.Model {
	return screenFrom[profilescreen.Model](m, tuiapp.ScreenProfiles)
}

func (m rootModel) setProfilesScreen(screen profilescreen.Model) rootModel {
	m.router = m.router.Set(tuiapp.ScreenProfiles, screen)
	return m
}

func (m rootModel) withDialog(dialog dialogModel) rootModel {
	m.dialog = &dialog
	m.picker = nil
	m.preview = nil
	m.planView = nil
	m.planViewDirectDiff = false
	return m
}

func (m rootModel) withPicker(picker toolPicker) rootModel {
	m.dialog = nil
	m.picker = &picker
	m.preview = nil
	m.planView = nil
	m.planViewDirectDiff = false
	return m
}

func (m rootModel) withPreview(preview previewModel) rootModel {
	m.dialog = nil
	m.picker = nil
	m.preview = &preview
	m.planView = nil
	m.planViewDirectDiff = false
	return m
}

func (m rootModel) withPlanView(planView planViewModel) rootModel {
	m.dialog = nil
	m.picker = nil
	m.preview = nil
	m.planView = &planView
	m.planViewDirectDiff = false
	return m
}

func (m rootModel) withPlanViewMode(planView planViewModel, directDiff bool) rootModel {
	m = m.withPlanView(planView)
	m.planViewDirectDiff = directDiff
	return m
}

func (m rootModel) updateProfiles(msg tea.Msg) (rootModel, tea.Cmd) {
	var cmd tea.Cmd
	m.router, cmd = m.router.UpdateScreen(tuiapp.ScreenProfiles, msg)
	return m, cmd
}

func (m rootModel) packsScreen() packsscreen.Model {
	return screenFrom[packsscreen.Model](m, tuiapp.ScreenPacks)
}

func (m rootModel) setPacksScreen(screen packsscreen.Model) rootModel {
	m.router = m.router.Set(tuiapp.ScreenPacks, screen)
	return m
}

func (m rootModel) applyPendingPackHint() (rootModel, tea.Cmd) {
	if m.pendingPackCursorHint == "" {
		return m, nil
	}
	packs := m.packsScreen()
	if !packs.HasListItem(m.pendingPackCursorHint) {
		return m, nil
	}
	packs = packs.ApplyCursorHint(m.pendingPackCursorHint)
	if m.pendingPackContentKind == "" || m.pendingPackContentName == "" {
		m.pendingPackCursorHint = ""
		return m.setPacksScreen(packs), nil
	}
	if next, cmd, ok := packs.ApplyContentHint(m.pendingPackContentKind, m.pendingPackContentName); ok {
		m.pendingPackCursorHint = ""
		m.pendingPackContentKind = ""
		m.pendingPackContentName = ""
		return m.setPacksScreen(next), cmd
	}
	return m.setPacksScreen(packs), nil
}

func (m rootModel) updatePacks(msg tea.Msg) (rootModel, tea.Cmd) {
	var cmd tea.Cmd
	m.router, cmd = m.router.UpdateScreen(tuiapp.ScreenPacks, msg)
	return m, cmd
}

func (m rootModel) configScreen() configscreen.Model {
	return screenFrom[configscreen.Model](m, tuiapp.ScreenConfig)
}

func (m rootModel) setConfigScreen(screen configscreen.Model) rootModel {
	m.router = m.router.Set(tuiapp.ScreenConfig, screen)
	return m
}

func (m rootModel) applyPendingConfigHint() rootModel {
	screen := m.configScreen()
	if m.pendingConfigParamHint != "" {
		if next, ok := screen.SelectParam(m.pendingConfigParamHint); ok {
			m.pendingConfigParamHint = ""
			return m.setConfigScreen(next)
		}
	}
	if m.pendingConfigEnvHint != "" {
		if next, ok := screen.SelectEnv(m.pendingConfigEnvHint); ok {
			m.pendingConfigEnvHint = ""
			return m.setConfigScreen(next)
		}
	}
	return m
}

func (m rootModel) updateConfig(msg tea.Msg) (rootModel, tea.Cmd) {
	screen := m.configScreen()
	if profile, ok := m.configProfile(); ok {
		screen = screen.SetContext(profile, m.cfg.SyncCfg)
		m = m.setConfigScreen(screen)
	}
	var cmd tea.Cmd
	m.router, cmd = m.router.UpdateScreen(tuiapp.ScreenConfig, msg)
	return m, cmd
}

func (m rootModel) updateSearch(msg tea.Msg) (rootModel, tea.Cmd) {
	var cmd tea.Cmd
	m.router, cmd = m.router.UpdateScreen(tuiapp.ScreenSearch, msg)
	return m, cmd
}

func (m rootModel) saveScreen() savescreen.Model {
	return screenFrom[savescreen.Model](m, tuiapp.ScreenSave)
}

func (m rootModel) setSaveScreen(screen savescreen.Model) rootModel {
	m.router = m.router.Set(tuiapp.ScreenSave, screen)
	return m
}

func (m rootModel) updateSave(msg tea.Msg) (rootModel, tea.Cmd) {
	var cmd tea.Cmd
	m.router, cmd = m.router.UpdateScreen(tuiapp.ScreenSave, msg)
	return m, cmd
}

func (m rootModel) refreshSaveHarnesses() (rootModel, tea.Cmd) {
	screen, cmd := m.saveScreen().RefreshHarnesses()
	m = m.setSaveScreen(screen)
	return m, cmd
}

func (m rootModel) syncScreen() syncscreen.Model {
	return screenFrom[syncscreen.Model](m, tuiapp.ScreenSync)
}

func (m rootModel) updateSync(msg tea.Msg) (rootModel, tea.Cmd) {
	screen := m.syncScreen().WithContext(m.activeSyncSnapshot())
	m.router = m.router.Set(tuiapp.ScreenSync, screen)
	var cmd tea.Cmd
	m.router, cmd = m.router.UpdateScreen(tuiapp.ScreenSync, msg)
	return m, cmd
}

func (m rootModel) Init() tea.Cmd {
	// Load profiles first; packs and registry load after profiles arrive
	// (triggered by profilescreen.LoadedMsg handler). Sequencing avoids a race
	// condition where concurrent Init commands can freeze the TUI.
	cmds := []tea.Cmd{m.profilesScreen().InitCmd(m.cfg.SyncCfg)}
	if m.activeTab == tabSearch {
		if cmd := m.searchScreen().Init(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Auto-clear transient status text after timeout.
	if msg, ok := msg.(statusClearMsg); ok {
		if msg.id == m.statusID {
			m.statusText = ""
		}
		return m, nil
	}

	// Snapshot current status so we can detect changes after the update.
	oldStatus := m.statusText

	result, cmd := m.update(msg)
	rm := result.(rootModel)

	// If status text changed to something non-empty, schedule auto-clear.
	if rm.statusText != oldStatus && rm.statusText != "" {
		rm.statusID++
		cmd = tea.Batch(cmd, scheduleStatusClear(rm.statusID))
	}
	return rm, cmd
}

func (m rootModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(autoSyncDueMsg); ok {
		if m.pendingAutoSync == nil ||
			m.pendingAutoSync.id != msg.id ||
			m.pendingAutoSync.profileName != msg.profileName {
			return m, nil
		}
		m.pendingAutoSync = nil
		item, ok := m.profilesScreen().ActiveProfile()
		if !ok || item.Name != msg.profileName {
			return m, nil
		}
		return m.doSyncItem(item, "", "")
	}

	// Result messages must be handled before the overlay intercepts,
	// otherwise the overlay swallows its own result.
	if msg, ok := msg.(dialogResultMsg); ok {
		return m.handleDialogResult(msg)
	}
	if msg, ok := msg.(toolPickerResultMsg); ok {
		return m.handleToolPickerResult(msg)
	}
	if msg, ok := msg.(toolPickerRefreshMsg); ok {
		return m.handleToolPickerRefresh(msg)
	}

	// MCP probe streaming + result. All three message types are handled
	// before the overlay intercept so non-key messages aren't swallowed by
	// the picker's KeyMsg-only Update.
	if msg, ok := msg.(mcpProbeReadyMsg); ok {
		return m.handleMCPProbeReady(msg)
	}
	if msg, ok := msg.(mcpProbeProgressMsg); ok {
		return m.handleMCPProbeProgress(msg)
	}
	if msg, ok := msg.(mcpProbeResultMsg); ok {
		return m.handleMCPProbeResult(msg)
	}

	// Spinner ticks must be handled before any overlay intercept; the
	// picker overlay's Update only handles KeyMsg, so a tick reaching it
	// would be silently dropped and the animation would freeze.
	if msg, ok := msg.(spinner.TickMsg); ok {
		var cmds []tea.Cmd
		if m.packsScreen().ActiveUpdate() || m.packsScreen().ActiveInstall() {
			var cmd tea.Cmd
			m, cmd = m.updatePacks(msg)
			cmds = append(cmds, cmd)
		}
		if m.picker != nil && m.picker.Loading {
			var cmd tea.Cmd
			m.picker.Spinner, cmd = m.picker.Spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}

	// Preview overlay message handling.
	if msg, ok := msg.(common.PreviewRequestMsg); ok {
		p := newPreviewModel(m.width, m.height)
		m = m.withPreview(p)
		return m, loadPreview(msg.Title, msg.Category, msg.PackName, msg.FilePath)
	}
	if msg, ok := msg.(previewLoadedMsg); ok {
		if m.preview != nil {
			p := m.preview.SetContent(msg)
			m = m.withPreview(p)
			return m, nil
		}
	}
	if msg, ok := msg.(systemOpenErrorMsg); ok {
		m.statusText = common.ErrorStyle.Render(fmt.Sprintf("open: %v", msg.err))
		return m, nil
	}
	if msg, ok := msg.(editorFinishedMsg); ok {
		if msg.err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("editor: %v", msg.err))
		}
		// Re-read the file to refresh preview after editing.
		if m.preview != nil {
			title, category, packName, filePath := m.preview.Request()
			return m, loadPreview(title, category, packName, filePath)
		}
		// Reload sync-config in case it was edited.
		if cfg, err := app.ReloadSyncConfig(m.cfg.ConfigDir); err == nil {
			m.cfg.SyncCfg = cfg
		}
		// Reload all data after editing a config file.
		return m, tea.Batch(
			profilescreen.Load(m.cfg.ConfigDir, m.cfg.SyncCfg),
			loadPacks(m.cfg.ConfigDir),
			loadRegistry(m.cfg.ConfigDir),
			packsscreen.LoadIndexDetails(m.cfg.ConfigDir),
		)
	}
	switch msg := msg.(type) {
	case profilescreen.AddPackRequestMsg:
		d := m.addPackDialog()
		m = m.withDialog(d)
		return m, nil
	case profilescreen.ActionRequestMsg:
		if m.activeTab == tabProfiles {
			return m.openActionMenu()
		}
		return m, nil
	case packsscreen.ActionRequestMsg:
		if m.activeTab == tabPacks {
			return m.openActionMenu()
		}
		return m, nil
	case search.OpenPackMsg:
		m.pendingPackCursorHint = msg.PackName
		m.pendingPackContentKind = msg.Kind
		m.pendingPackContentName = msg.Name
		next, cmd := m.switchToTab(tabPacks)
		m = next.(rootModel)
		var hintCmd tea.Cmd
		m, hintCmd = m.applyPendingPackHint()
		return m, tea.Batch(cmd, hintCmd)
	case common.SyncDiffRequestMsg:
		return m.openSyncPlanDiff(msg)
	default:
	}

	if isHardExitKey(msg) {
		m.quitting = true
		return m, tea.Quit
	}
	if hit, ok := msg.(common.LayerHitMsg); ok && hit.ID == chromeBackID {
		next, cmd, _ := m.handleChromeLayerHit(hit)
		return next, cmd
	}

	// Plan view overlay captures all input when active.
	if m.planView != nil {
		return m.updatePlanView(msg)
	}

	// Preview captures all input when active.
	if m.preview != nil {
		return m.updatePreview(msg)
	}

	// Tool picker and dialog are mutually exclusive overlays — both
	// capture all input while active. Picker takes precedence so it can
	// handle its own key set before generic dialog routing.
	if m.picker != nil {
		return m.updatePicker(msg)
	}
	if m.dialog != nil {
		return m.updateDialog(msg)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.router = m.router.SetSizeAll(msg.Width, msg.Height-4)
		return m, nil

	case common.LayerHitMsg:
		if next, cmd, handled := m.handleChromeLayerHit(msg); handled {
			return next, cmd
		}
		var cmd tea.Cmd
		m.router, cmd = m.router.RouteLayerHit(msg)
		return m, cmd

	case tea.KeyPressMsg:
		// When a sub-model has a text input focused, delegate all keys
		// except hard-exit so character input isn't swallowed by global
		// hotkeys (tab-switch, edit, sync, refresh, etc.).
		if m.activeTab == tabSave && m.saveScreen().CapturesKey(msg) {
			switch msg.String() {
			case "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			default:
				return m.updateSave(msg)
			}
		}
		if m.activeTab == tabSearch && m.searchScreen().CapturesKey(msg) {
			return m.updateSearch(msg)
		}

		switch msg.String() {
		case "tab":
			return m.switchToTab((m.activeTab + 1) % tabCount)
		case "shift+tab":
			return m.switchToTab((m.activeTab - 1 + tabCount) % tabCount)
		case "1", "2", "3", "4", "5", "6":
			target := tabID(msg.String()[0] - '1')
			if target != m.activeTab {
				return m.switchToTab(target)
			}
			return m, nil
		case "esc":
			// Cancel an in-flight pack update before any navigation/exit
			// dispatch. This is the most common Esc context during updates
			// — the user triggers update from the list panel and the focus
			// stays there, so without this pre-check Esc would route into
			// startExit (save-on-exit dialog) instead of cancelling.
			if m.activeTab == tabPacks {
				packs, cancelled := m.packsScreen().CancelActiveUpdate()
				if cancelled {
					m = m.setPacksScreen(packs)
					return m, nil
				}
			}
			if m.activeTab == tabPacks {
				packs, cancelled := m.packsScreen().CancelActiveInstall()
				if cancelled {
					m = m.setPacksScreen(packs)
					return m, nil
				}
			}
			if m.activeTab == tabPacks && !m.packsScreen().IsListFocused() {
				break
			}
			// Let sub-models handle esc for internal navigation.
			if m.activeTab == tabProfiles && !m.profilesScreen().IsProfilesFocused() {
				break
			}
			if m.activeTab == tabSearch && m.searchScreen().ResultsFocused() {
				break
			}
			if m.activeTab == tabConfig && m.configScreen().IsContentFocused() {
				break
			}
			if m.activeTab == tabSave && m.saveScreen().CapturesKey(msg) {
				break // let save tab handle back-navigation
			}
			return m.startExit()
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "ctrl+s":
			return m.startSave()
		case "s":
			if m.activeTab == tabSave && m.saveScreen().CapturesKey(msg) {
				break // let save tab handle 's' for proceed-to-save
			}
			if m.activeTab == tabProfiles || m.activeTab == tabConfig || m.activeTab == tabSync || m.activeTab == tabSave {
				return m.startSync()
			}
		case "v":
			if m.activeTab == tabProfiles || m.activeTab == tabConfig || m.activeTab == tabSync {
				return m.openPlanView()
			}
		case "r":
			return m.refresh()
		case "w":
			return m.showWarnings()
		case "e", "i":
			if m.activeTab == tabSearch {
				break
			}
			return m.editCurrentFile()
		case "o":
			return m.openCurrentFile()
		case ".":
			return m.openActionMenu()
		case "t", "enter":
			if m.activeTab == tabProfiles && m.profilesScreen().IsTreeFocused() {
				if sel, ok := m.profilesScreen().CurrentTreeSelection(); ok && sel.IsMCP {
					return m.openMCPToolPicker()
				}
			}
		}

	case profilescreen.CreatedMsg:
		if msg.Err == nil {
			m.pendingCursorHint = msg.Name
			return m, profilescreen.Load(m.cfg.ConfigDir, m.cfg.SyncCfg)
		}
		m.statusText = common.ErrorStyle.Render(fmt.Sprintf("create error: %v", msg.Err))
		return m, nil

	case profilescreen.DeletedMsg:
		if msg.Err == nil {
			return m, profilescreen.Load(m.cfg.ConfigDir, m.cfg.SyncCfg)
		}
		m.statusText = common.ErrorStyle.Render(fmt.Sprintf("delete error: %v", msg.Err))
		return m, nil

	case profilescreen.ActivatedMsg:
		if msg.Err == nil {
			m.setActiveProfile(msg.ProfileName)
			return m.syncCheckAndMaybeAutoSync(msg.ProfileName, true, true)
		} else {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("activate error: %v", msg.Err))
		}
		return m.syncCheckAndMaybeAutoSync(msg.ProfileName, false, false)

	case profilescreen.SavedMsg:
		if msg.Err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("save error: %v", msg.Err))
		} else {
			m.statusText = common.DimStyle.Render(m.profileSaveStatus(msg.ProfileName))

			screen := m.profilesScreen().MarkSaved(msg.ProfileName)
			m = m.setProfilesScreen(screen)

			// Derive global dirty from per-profile state.
			m.dirty = m.anyProfileDirty()
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
					m.statusText = common.ErrorStyle.Render("save failed; press q again to discard")
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
		return m.syncCheckAndMaybeAutoSync(msg.ProfileName, msg.Err == nil, false)

	}

	// Handle sync completion.
	if msg, ok := msg.(syncDoneMsg); ok {
		screen := m.profilesScreen().MarkSyncDone(msg.profileName, msg.err, msg.warnings)
		m = m.setProfilesScreen(screen)
		if msg.err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("sync error: %v", msg.err))
		} else {
			m.statusText = common.DimStyle.Render(fmt.Sprintf("synced %d file(s)", msg.filesWritten))
		}
		// Re-check sync status and refresh save tab.
		m, cmds := m.syncCheckAndInspectCmds()
		return m, tea.Batch(cmds...)
	}

	// Handle save plan (dry-run) result.
	if msg, ok := msg.(savePlanMsg); ok {
		if msg.err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("save error: %v", msg.err))
			return m, nil
		}
		if len(msg.ops) == 0 {
			m.statusText = common.DimStyle.Render("nothing to save — harness files match packs")
			return m, nil
		}
		// Stash context for confirm and open save plan view.
		m.savePlanCtx = &savePlanContext{
			profileName: msg.profileName,
		}
		pv := newPlanViewModel(m.width, m.height, msg.profileName, "", msg.ops, true)
		m = m.withPlanView(pv)
		m.statusText = ""
		return m, nil
	}

	// Handle save (round-trip) completion.
	if msg, ok := msg.(saveDoneMsg); ok {
		m.savePlanCtx = nil
		if msg.err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("save error: %v", msg.err))
		} else {
			m.statusText = common.DimStyle.Render(fmt.Sprintf("saved %d, unchanged %d", msg.saved, msg.unchanged))
		}
		// Re-check sync status and refresh save tab since pack content changed.
		m, cmds := m.syncCheckAndInspectCmds()
		return m, tea.Batch(cmds...)
	}

	// Handle adopt-to-pack completion.
	if msg, ok := msg.(saveToPackMsg); ok {
		if msg.err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("adopt error: %v", msg.err))
		} else {
			m.statusText = common.DimStyle.Render(fmt.Sprintf("added %s to %s", filepath.Base(msg.harnessPath), msg.packName))
		}
		// Refresh save tab + sync status since ledger/pack changed.
		m, cmds := m.syncCheckAndInspectCmds()
		return m, tea.Batch(cmds...)
	}

	// Handle move-to-pack completion.
	if msg, ok := msg.(moveToPackMsg); ok {
		if msg.err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("move error: %v", msg.err))
		} else {
			m.statusText = common.DimStyle.Render(fmt.Sprintf("moved %s/%s from %s to %s", msg.category, msg.id, msg.fromPack, msg.toPack))
		}
		// Refresh packs, save tab, and sync status.
		cmds := []tea.Cmd{
			loadPacks(m.cfg.ConfigDir),
			packsscreen.LoadIndexDetails(m.cfg.ConfigDir),
		}
		var refreshCmds []tea.Cmd
		m, refreshCmds = m.syncCheckAndInspectCmds()
		cmds = append(cmds, refreshCmds...)
		return m, tea.Batch(cmds...)
	}

	// Handle config tab requesting default profile selection.
	if _, ok := msg.(configscreen.ProfileSelectMsg); ok {
		if names := m.profilesScreen().ProfileNames(); len(names) > 0 {
			d := newListSelectDialog(dialogSyncSelectProfile, "Select profile:", names)
			selected := strings.TrimSpace(m.cfg.SyncCfg.Defaults.Profile)
			if selected == "" {
				if profile, ok := m.configProfile(); ok {
					selected = profile.Name
				}
			}
			if idx := slices.Index(names, selected); idx >= 0 {
				d.ListCursor = idx
			}
			m = m.withDialog(d)
		}
		return m, nil
	}

	// Handle sync config intent messages from the Config tab.
	if _, ok := msg.(configscreen.HarnessSelectMsg); ok {
		d := newChecklistDialog(dialogConfigHarnesses, "Select harnesses:", harnessCheckItems(m.cfg.SyncCfg.Defaults.Harnesses))
		m = m.withDialog(d)
		return m, nil
	}
	if _, ok := msg.(configscreen.CycleScopeMsg); ok {
		m.cfg.SyncCfg = app.CycleSyncScope(m.cfg.SyncCfg)
		m, _ = m.syncConfigScreenContext(false)
		return m, saveSyncConfig(m.cfg.ConfigDir, m.cfg.SyncCfg)
	}
	if _, ok := msg.(configscreen.CycleCollisionMsg); ok {
		m.cfg.SyncCfg = app.CycleCollisionStrategy(m.cfg.SyncCfg)
		m, _ = m.syncConfigScreenContext(false)
		return m, saveSyncConfig(m.cfg.ConfigDir, m.cfg.SyncCfg)
	}
	if _, ok := msg.(configscreen.ToggleNamespacedMsg); ok {
		m.cfg.SyncCfg = app.ToggleNamespaced(m.cfg.SyncCfg)
		m, _ = m.syncConfigScreenContext(false)
		m.statusText = common.DimStyle.Render("saved; press s to sync rendered name changes")
		return m, saveSyncConfig(m.cfg.ConfigDir, m.cfg.SyncCfg)
	}
	if msg, ok := msg.(configscreen.EditParamMsg); ok {
		if profile, ok := m.configProfile(); ok {
			m = m.setProfilesScreen(m.profilesScreen().ApplyCursorHint(profile.Name))
		}
		return m.openProfileParamValueDialog(msg.Key)
	}
	if _, ok := msg.(configscreen.EditEnvMsg); ok {
		envPath := config.DotEnvPath(m.cfg.ConfigDir)
		if _, err := os.Stat(envPath); os.IsNotExist(err) {
			if err := os.WriteFile(envPath, nil, 0o600); err != nil {
				m.statusText = common.DimStyle.Render(fmt.Sprintf("create .env: %v", err))
				return m, nil
			}
		}
		return m, openFileInEditor(envPath)
	}

	if msg, ok := msg.(mcpInventorySavedMsg); ok {
		if msg.err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("save MCP inventory: %v", msg.err))
		} else {
			m.statusText = common.DimStyle.Render(fmt.Sprintf("saved probed inventory for %s", msg.key.server))
		}
		if m.pendingReturnToTools {
			m.pendingReturnToTools = false
			return m.openMCPToolPicker()
		}
		return m, nil
	}

	// Handle syncConfigSavedMsg: update config, re-run sync check.
	if msg, ok := msg.(syncConfigSavedMsg); ok {
		if msg.err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("save sync-config: %v", msg.err))
			return m, nil
		}
		m.cfg.SyncCfg = msg.syncCfg
		m, _ = m.syncConfigScreenContext(false)
		var syncCmd tea.Cmd
		m, syncCmd = m.requestActiveSyncCheck()
		return m, syncCmd
	}
	if msg, ok := msg.(envSetMsg); ok {
		if msg.err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("env set: %v", msg.err))
			return m, nil
		}
		m.statusText = common.DimStyle.Render(fmt.Sprintf("set %s in .env", msg.key))
		m.pendingConfigEnvHint = msg.key
		var refsCmd tea.Cmd
		m, refsCmd = m.syncConfigScreenContext(true)
		m = m.applyPendingConfigHint()
		if refsCmd != nil {
			m.pendingConfigEnvHint = msg.key
		}
		return m, refsCmd
	}
	if msg, ok := msg.(envUnsetMsg); ok {
		if msg.err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("env unset: %v", msg.err))
			return m, nil
		}
		m.statusText = common.DimStyle.Render(fmt.Sprintf("removed %s from .env", msg.key))
		var refsCmd tea.Cmd
		m, refsCmd = m.syncConfigScreenContext(true)
		return m, refsCmd
	}

	// Handle pack lifecycle messages.
	if msg, ok := msg.(packCreatedMsg); ok {
		addToProfile := m.pendingCreateAddToProf
		m.pendingCreateAddToProf = false
		if msg.Err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("create error: %v", msg.Err))
		} else {
			m.statusText = common.DimStyle.Render(fmt.Sprintf("created %s", msg.Name))
			m.pendingPackCursorHint = msg.Name
			if addToProfile {
				profiles := m.profilesScreen().AddPackToCurrent(msg.Name)
				m = m.setProfilesScreen(profiles)
				m.dirty = m.dirty || profiles.Dirty()
				if saveCmd := profiles.SaveCurrentIfDirty(); saveCmd != nil {
					return m, tea.Batch(
						saveCmd,
						loadPacks(m.cfg.ConfigDir),
					)
				}
			}
		}
		return m, tea.Batch(
			loadPacks(m.cfg.ConfigDir),
			profilescreen.Load(m.cfg.ConfigDir, m.cfg.SyncCfg),
		)
	}
	if msg, ok := msg.(packInstallReadyMsg); ok {
		var cmd tea.Cmd
		m, cmd = m.updatePacks(msg)
		m.statusText = common.DimStyle.Render(m.packsScreen().InstallStatus())
		return m, cmd
	}
	if msg, ok := msg.(packInstallProgressMsg); ok {
		var cmd tea.Cmd
		m, cmd = m.updatePacks(msg)
		if m.packsScreen().ActiveInstall() {
			m.statusText = common.DimStyle.Render(m.packsScreen().InstallStatus())
		}
		return m, cmd
	}
	if msg, ok := msg.(packInstalledMsg); ok {
		var cmd tea.Cmd
		m, cmd = m.updatePacks(msg)
		if msg.Err != nil {
			if errors.Is(msg.Err, context.Canceled) {
				m.statusText = common.DimStyle.Render("install cancelled")
			} else {
				m.statusText = common.ErrorStyle.Render(fmt.Sprintf("install error: %v", msg.Err))
			}
		} else {
			m.statusText = common.DimStyle.Render(fmt.Sprintf("installed %s", msg.Name))
			m.pendingPackCursorHint = msg.Name
		}
		searchScreen, searchCmd := m.searchScreen().Refresh()
		m.router = m.router.Set(tuiapp.ScreenSearch, searchScreen)
		// Reload both packs and profiles — installing a pack may bundle new profiles.
		return m, tea.Batch(
			cmd,
			loadPacks(m.cfg.ConfigDir),
			packsscreen.LoadIndexDetails(m.cfg.ConfigDir),
			profilescreen.Load(m.cfg.ConfigDir, m.cfg.SyncCfg),
			searchCmd,
		)
	}
	if msg, ok := msg.(packRemovedMsg); ok {
		if msg.Err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("remove error: %v", msg.Err))
		} else {
			m.statusText = common.DimStyle.Render(fmt.Sprintf("removed %s", msg.Name))
		}
		// Reload packs and profiles (pack may have been in a profile).
		return m, tea.Batch(
			loadPacks(m.cfg.ConfigDir),
			packsscreen.LoadIndexDetails(m.cfg.ConfigDir),
			profilescreen.Load(m.cfg.ConfigDir, m.cfg.SyncCfg),
		)
	}
	if msg, ok := msg.(packUpdateReadyMsg); ok {
		var cmd tea.Cmd
		m, cmd = m.updatePacks(msg)
		m.statusText = common.DimStyle.Render(m.packsScreen().BatchStatus())
		return m, cmd
	}
	if msg, ok := msg.(packProgressMsg); ok {
		var cmd tea.Cmd
		m, cmd = m.updatePacks(msg)
		if m.packsScreen().ActiveUpdate() {
			m.statusText = common.DimStyle.Render(m.packsScreen().BatchStatus())
		}
		return m, cmd
	}
	if msg, ok := msg.(packRowClearMsg); ok {
		return m.updatePacks(msg)
	}
	if msg, ok := msg.(packUpdatedMsg); ok {
		var cmd tea.Cmd
		m, cmd = m.updatePacks(msg)
		if msg.Err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("update error: %v", msg.Err))
		} else if packsscreen.AnyCancelled(msg.Results) {
			m.statusText = common.DimStyle.Render("Cancelled update")
		} else if len(msg.Results) > 0 {
			if msg.Name != "" {
				m.pendingPackCursorHint = msg.Name
			}
			summaries := make([]string, len(msg.Results))
			for i, r := range msg.Results {
				// Prefer r.Message — it carries pin status ("pinned at
				// v1.2.3 (latest: v2.0.0)") for skipped pinned packs and
				// short hashes for normal updates. Falls back to the
				// status enum when the underlying call provides no detail.
				detail := r.Message
				if detail == "" {
					detail = string(r.Status)
				}
				summaries[i] = fmt.Sprintf("%s: %s", r.Name, detail)
			}
			items, hasDeclined := bundledUpdateChecklist(msg.Results)
			// If any pack actually changed, remind the user to sync — pack
			// content updated but harnesses won't see it until the next
			// sync. When everything was up-to-date or a no-op, show the
			// per-pack summary instead.
			if packsscreen.AnyUpdated(msg.Results) {
				m.statusText = common.DimStyle.Render("Updates complete. Press s to sync to harnesses.")
			} else {
				m.statusText = common.DimStyle.Render(strings.Join(summaries, ", "))
			}
			if len(items) > 0 {
				m.updateFlow.Results = msg.Results
				title := "Bundled content available:"
				if hasDeclined {
					title = "Bundled content available (previously declined items start unchecked):"
				}
				d := newChecklistDialog(dialogBundledCandidates, title, items)
				m = m.withDialog(d)
			}
		} else {
			m.statusText = common.DimStyle.Render("updated")
		}
		cmds := []tea.Cmd{loadPacks(m.cfg.ConfigDir), packsscreen.LoadIndexDetails(m.cfg.ConfigDir)}
		if packsscreen.AnyUpdated(msg.Results) {
			var refreshCmds []tea.Cmd
			m, refreshCmds = m.syncCheckAndInspectCmds()
			cmds = append(cmds, refreshCmds...)
		}
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)
	}
	if msg, ok := msg.(bundledApprovedMsg); ok {
		if msg.err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("bundled content error: %v", msg.err))
		} else {
			m.statusText = common.DimStyle.Render("bundled content applied")
		}
		return m, tea.Batch(
			loadPacks(m.cfg.ConfigDir),
			packsscreen.LoadIndexDetails(m.cfg.ConfigDir),
			profilescreen.Load(m.cfg.ConfigDir, m.cfg.SyncCfg),
		)
	}

	// Route async data messages to the correct sub-model regardless of active tab.
	// Without this, messages arriving while the other tab is active get dropped.
	switch msg := msg.(type) {
	case profilescreen.LoadedMsg, profilescreen.SyncStatusMsg, profilescreen.FileSizeMsg:
		var cmd tea.Cmd
		m, cmd = m.updateProfiles(msg)
		profiles := m.profilesScreen()
		m.dirty = m.dirty || profiles.Dirty()
		if profiles.LoadErr() != "" {
			m.statusText = common.ErrorStyle.Render(profiles.LoadErr())
		}
		// Trigger initial sync checks and refresh the Config screen context.
		// Screens no longer read profilescreen.LoadedMsg directly — the
		// shell adapts the message envelopes.
		if pmsg, ok := msg.(profilescreen.LoadedMsg); ok {
			// Apply cursor hint from profile create/rename.
			if m.pendingCursorHint != "" {
				profiles = profiles.ApplyCursorHint(m.pendingCursorHint)
				m = m.setProfilesScreen(profiles)
				m.pendingCursorHint = ""
			}
			// Load packs and registry after profiles arrive (sequenced
			// from Init to avoid concurrent-command race condition).
			cmd = tea.Batch(cmd, m.packsScreen().Init())
			var refreshCmds []tea.Cmd
			m, refreshCmds = m.syncCheckAndInspectCmds()
			for _, refreshCmd := range refreshCmds {
				cmd = tea.Batch(cmd, refreshCmd)
			}
			if pmsg.Err == nil {
				var refsCmd tea.Cmd
				m, refsCmd = m.syncConfigScreenContext(m.activeTab == tabConfig)
				if refsCmd != nil {
					cmd = tea.Batch(cmd, refsCmd)
				}
			}
		}
		if smsg, ok := msg.(profilescreen.SyncStatusMsg); ok && m.pendingSyncRecheck == smsg.ProfileName {
			m.pendingSyncRecheck = ""
			if active, ok := m.profilesScreen().ActiveProfile(); ok && active.Name == smsg.ProfileName {
				var syncCmd tea.Cmd
				m, syncCmd = m.requestActiveSyncCheck()
				if syncCmd != nil {
					cmd = tea.Batch(cmd, syncCmd)
				}
			}
		}
		return m, cmd
	case packsLoadedMsg, packSizesMsg, registryLoadedMsg, indexLoadedMsg, packDriftLoadedMsg, packVersionsLoadedMsg, versionsDebounceMsg:
		var cmd tea.Cmd
		m, cmd = m.updatePacks(msg)
		if m.packsScreen().LoadErr() != "" {
			m.statusText = common.ErrorStyle.Render(m.packsScreen().LoadErr())
		}
		// Apply pack/content cursor hints after list rebuilds.
		switch msg.(type) {
		case packsLoadedMsg, indexLoadedMsg:
			var hintCmd tea.Cmd
			m, hintCmd = m.applyPendingPackHint()
			cmd = tea.Batch(cmd, hintCmd)
		}
		return m, cmd
	case savescreen.FileDeletedMsg:
		if msg.Err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("delete: %v", msg.Err))
			return m, nil
		}
		m.statusText = common.DimStyle.Render(fmt.Sprintf("deleted %s", filepath.Base(msg.Path)))
		screen, cmd := m.saveScreen().RediscoverFiles()
		m = m.setSaveScreen(screen)
		return m, cmd
	case savescreen.PipelineDoneMsg:
		var cmd tea.Cmd
		m, cmd = m.updateSave(msg)
		m.statusText = ""
		if msg.Err == nil {
			return m, tea.Batch(cmd, loadPacks(m.cfg.ConfigDir), packsscreen.LoadIndexDetails(m.cfg.ConfigDir))
		}
		return m, cmd
	case savescreen.HarnessDetectedMsg, savescreen.VectorsDiscoveredMsg, savescreen.FilesDiscoveredMsg, savescreen.DiffLoadedMsg:
		var cmd tea.Cmd
		m, cmd = m.updateSave(msg)
		m.statusText = ""
		return m, cmd
	case search.ResultsMsg:
		return m.updateSearch(msg)
	case configscreen.LoadedMsg:
		if msg.Err != nil {
			m.statusText = common.ErrorStyle.Render(fmt.Sprintf("config refs: %v", msg.Err))
			return m, nil
		}
		if profile, ok := m.configProfile(); ok && msg.ProfileName != "" && msg.ProfileName != profile.Name {
			return m, nil
		}
		m = m.setConfigScreen(m.configScreen().SetRefs(msg.Refs).SetEnvEntries(msg.Env))
		m = m.applyPendingConfigHint()
		return m, nil
	}

	// Delegate interactive input to active tab only.
	var cmd tea.Cmd
	switch m.activeTab {
	case tabProfiles:
		m, cmd = m.updateProfiles(msg)
		profiles := m.profilesScreen()
		m.dirty = m.dirty || profiles.Dirty()
		// Auto-save dirty profiles so on-disk config stays current.
		// The profilescreen.SavedMsg handler triggers checkSyncCmd after save,
		// which uses the in-memory config for an accurate sync preview.
		if profiles.Dirty() {
			if saveCmd := profiles.SaveAll(); saveCmd != nil {
				cmd = tea.Batch(cmd, saveCmd)
			}
		}
	case tabPacks:
		m, cmd = m.updatePacks(msg)
	case tabConfig:
		m, cmd = m.updateConfig(msg)
	case tabSave:
		m, cmd = m.updateSave(msg)
	case tabSync:
		m, cmd = m.updateSync(msg)
	case tabSearch:
		m, cmd = m.updateSearch(msg)
	}
	return m, cmd
}

func isHardExitKey(msg tea.Msg) bool {
	key, ok := msg.(tea.KeyPressMsg)
	return ok && key.String() == "ctrl+c"
}

func (m rootModel) updatePreview(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = msg.Width
		m.height = msg.Height
		p := m.preview.SetSizeModel(msg.Width, msg.Height)
		m = m.withPreview(p)
		return m, nil
	}
	if msg, ok := msg.(common.LayerHitMsg); ok && common.IsLeftClick(msg.Mouse) {
		switch msg.ID {
		case "preview:close":
			m.preview = nil
			return m, nil
		}
	}
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.String() {
		case "esc", "q":
			m.preview = nil
			return m, nil
		}
	}
	p, cmd := m.preview.UpdateModel(msg)
	m = m.withPreview(p)
	return m, cmd
}

func (m rootModel) updatePlanView(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = msg.Width
		m.height = msg.Height
		// Delegate resize to planView (and nested diffView if active).
		pv, cmd := m.planView.UpdateModel(msg)
		m = m.withPlanViewMode(pv, m.planViewDirectDiff)
		return m, cmd
	}

	if msg, ok := msg.(common.LayerHitMsg); ok && common.IsLeftClick(msg.Mouse) {
		switch msg.ID {
		case "planview:close":
			if m.planView.IsSavePlanOverlay() {
				m.savePlanCtx = nil
				m.statusText = common.DimStyle.Render("save cancelled")
			}
			m.planView = nil
			m.planViewDirectDiff = false
			return m, nil
		case "planview:diff:close":
			if m.planViewDirectDiff {
				m.planView = nil
				m.planViewDirectDiff = false
				return m, nil
			}
		case "planview:confirm":
			if m.planView.IsSavePlanOverlay() && m.savePlanCtx != nil {
				spc := m.savePlanCtx
				m.planView = nil
				m.planViewDirectDiff = false
				m.statusText = common.DimStyle.Render("saving...")
				return m, runSave(m.ctx, m.eng, m.cfg.ConfigDir, spc.profileName, m.cfg.Registry)
			}
			return m, nil
		}
	}

	// When diff overlay is active inside plan view, delegate everything
	// (including esc) to plan view so it can close the diff first.
	if m.planView.HasDiffView() {
		if m.planViewDirectDiff {
			if key, ok := msg.(tea.KeyPressMsg); ok {
				switch key.String() {
				case "esc", "q":
					m.planView = nil
					m.planViewDirectDiff = false
					return m, nil
				}
			}
		}
		pv, cmd := m.planView.UpdateModel(msg)
		m = m.withPlanViewMode(pv, m.planViewDirectDiff)
		return m, cmd
	}

	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.String() {
		case "esc", "q":
			if m.planView.IsSavePlanOverlay() {
				m.savePlanCtx = nil
				m.statusText = common.DimStyle.Render("save cancelled")
			}
			m.planView = nil
			m.planViewDirectDiff = false
			return m, nil
		case "s":
			if m.planView.IsSavePlanOverlay() && m.savePlanCtx != nil {
				spc := m.savePlanCtx
				m.planView = nil
				m.planViewDirectDiff = false
				m.statusText = common.DimStyle.Render("saving...")
				return m, runSave(m.ctx, m.eng, m.cfg.ConfigDir, spc.profileName, m.cfg.Registry)
			}
		}
	}
	pv, cmd := m.planView.UpdateModel(msg)
	m = m.withPlanViewMode(pv, m.planViewDirectDiff)
	return m, cmd
}

// openPlanView creates the plan view overlay from the target profile's sync plan.
func (m rootModel) openPlanView() (tea.Model, tea.Cmd) {
	item, ok := m.syncTargetItem()
	if ok && len(item.SyncTarget.Ops) > 0 {
		pv := newPlanViewModel(m.width, m.height, item.Name, item.SyncTarget.ProjectDir, item.SyncTarget.Ops, false)
		m = m.withPlanView(pv)
		return m, nil
	}
	m.statusText = common.DimStyle.Render("no pending changes to view")
	return m, nil
}

func (m rootModel) openSyncPlanDiff(msg common.SyncDiffRequestMsg) (tea.Model, tea.Cmd) {
	if len(msg.Ops) == 0 {
		m.statusText = common.DimStyle.Render("no pending changes to view")
		return m, nil
	}
	pv := newPlanViewModel(m.width, m.height, msg.ProfileName, msg.ProjectDir, msg.Ops, false)
	if msg.Cursor >= 0 && msg.Cursor < len(pv.Items) && !pv.Items[msg.Cursor].IsHeader {
		pv.Cursor = msg.Cursor
	}
	maxOffset := max(len(pv.Items)-pv.ViewportHeight(), 0)
	pv.ScrollOffset = min(max(msg.ScrollOffset, 0), maxOffset)
	pv, cmd := pv.UpdateModel(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = m.withPlanViewMode(pv, true)
	return m, cmd
}

func (m rootModel) updateDialog(msg tea.Msg) (tea.Model, tea.Cmd) {
	d, cmd := m.dialog.UpdateModel(msg)
	m = m.withDialog(d)
	// Check if dialog produced a result.
	if cmd != nil {
		return m, cmd
	}
	return m, nil
}

func (m rootModel) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	p, cmd := m.picker.UpdateModel(msg)
	m = m.withPicker(p)
	if cmd != nil {
		return m, cmd
	}
	return m, nil
}

// handleToolPickerResult dispatches the picker's result. Only one picker ID
// exists today (the MCP tool picker); the switch is future-proofing for
// additional pickers that could reuse toolPicker as a sub-model.
func (m rootModel) handleToolPickerResult(msg toolPickerResultMsg) (tea.Model, tea.Cmd) {
	m.picker = nil
	m.cancelInflightMCPProbe()
	switch msg.ID {
	case dialogMCPToolPicker:
		return m.handleMCPToolPickerResult(msg)
	}
	return m, nil
}

// handleToolPickerRefresh fires a fresh probe for the server the picker is
// currently editing. The picker stays open in loading state; on success the
// probe-result handler overwrites the cached entry. We do NOT invalidate
// the cache up-front — if the re-probe fails (server down, network blip),
// keeping the previous entry means the next session still shows useful
// data instead of falling back to the static inventory.
func (m rootModel) handleToolPickerRefresh(_ toolPickerRefreshMsg) (tea.Model, tea.Cmd) {
	if m.picker == nil {
		return m, nil
	}
	item, ok := m.profilesScreen().CurrentProfile()
	if !ok {
		return m, nil
	}
	sel, ok := m.profilesScreen().CurrentTreeSelection()
	if !ok || !sel.IsMCP || sel.PackRoot == "" {
		return m, nil
	}
	key := newMCPProbeKey(sel.PackRoot, sel.ID)

	m.cancelInflightMCPProbe()
	m.mcpProbeSeq++
	m.mcpProbeActive = &mcpProbeRequest{key: key, seq: m.mcpProbeSeq}
	return m, tea.Batch(
		probeMCPServer(m.ctx, m.cfg.ConfigDir, sel.PackRoot, sel.ID, item.Config.Params, key, m.mcpProbeSeq),
		m.picker.Spinner.Tick,
	)
}

func (m rootModel) handleDialogResult(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	m.dialog = nil
	switch msg.ID {
	// Profile CRUD dialogs.
	case dialogNewProfile, dialogDuplicateProfile, dialogDeleteProfile,
		dialogActivateProfile, dialogProfileParamSelect, dialogProfileParamEdit,
		dialogProfileParamAdd, dialogProfileParamDelete:
		return m.handleProfileDialogResult(msg)
	// Sync flow dialogs.
	case dialogSaveOnExit, dialogSyncOnExit, dialogSyncScope, dialogSyncHarness, dialogSyncSelectProfile,
		dialogConfigHarnesses:
		return m.handleSyncDialogResult(msg)
	// Pack roster mutations (profile packs panel + pack tab installs).
	case dialogAddPack, dialogRemovePack, dialogPackInstall, dialogPackRemove,
		dialogInstallWith, dialogInstallVersion, dialogUpdateWith, dialogPinVersion,
		dialogPackAddToProfile:
		return m.handlePackDialogResult(msg)
	// Action menu results.
	case dialogActionProfile, dialogActionPack, dialogActionPackTab, dialogActionConfig, dialogActionSync:
		return m.handleActionMenuResult(msg)
	case dialogConfigEnvAdd:
		if msg.Confirmed && strings.TrimSpace(msg.Value) != "" {
			return m.applyConfigEnvAdd(msg.Value)
		}
	case dialogConfigEnvEdit:
		if msg.Confirmed {
			return m.applyConfigEnvEdit(msg.Value)
		}
		m.pendingConfigEnvKey = ""
	case dialogConfigEnvDelete:
		key := strings.TrimSpace(m.pendingConfigEnvKey)
		m.pendingConfigEnvKey = ""
		if msg.Confirmed && key != "" {
			m.statusText = common.DimStyle.Render(fmt.Sprintf("removing %s...", key))
			return m, unsetConfigEnv(m.cfg.ConfigDir, key)
		}
	case dialogActionTree:
		return m.handleTreeAction(msg)
	// MCP bulk action menu (regular list-select dialog opened from the
	// tool picker's `.` key). The picker itself lives on m.picker and
	// emits toolPickerResultMsg, handled separately.
	case dialogMCPBulkAction:
		return m.handleMCPBulkActionResult(msg)
	// Save tab dialogs.
	case dialogActionSave:
		return m.handleSaveActionResult(msg)
	// Pack content dialogs.
	case dialogActionContent:
		return m.handlePackContentAction(msg)
	case dialogContentMoveTo:
		return m.handleContentMoveTo(msg)
	// Create pack dialog.
	case dialogCreatePack:
		if msg.Confirmed && msg.Value != "" {
			m.statusText = common.DimStyle.Render(fmt.Sprintf("creating %s...", msg.Value))
			return m, createPack(m.cfg.ConfigDir, msg.Value)
		}
		m.pendingCreateAddToProf = false
	// Save tab file deletion.
	case dialogDeleteSaveFile:
		if msg.Confirmed && m.pendingDeletePath != "" {
			path := m.pendingDeletePath
			m.pendingDeletePath = ""
			return m, savescreen.DeleteFile(path)
		}
		m.pendingDeletePath = ""
	// Bundled candidates checklist.
	case dialogBundledCandidates:
		results := m.updateFlow.Results
		m.updateFlow.Results = nil
		if msg.Confirmed && len(msg.Values) > 0 {
			var cats []domain.BundledCategory
			for _, v := range msg.Values {
				if c, ok := domain.ParseBundledCategory(v); ok {
					cats = append(cats, c)
				}
			}
			return m, approveBundled(m.cfg.ConfigDir, results, domain.NewBundledSet(cats...))
		}
	}
	return m, nil
}

func (m rootModel) handleProfileDialogResult(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	switch msg.ID {
	case dialogNewProfile:
		if msg.Confirmed && msg.Value != "" {
			m.pendingCursorHint = msg.Value
			return m, profilescreen.Create(m.cfg.ConfigDir, msg.Value)
		}
	case dialogDuplicateProfile:
		if msg.Confirmed && msg.Value != "" {
			if item, ok := m.profilesScreen().CurrentProfile(); ok {
				m.pendingCursorHint = msg.Value
				return m, profilescreen.Duplicate(m.cfg.ConfigDir, item.Name, msg.Value)
			}
		}
	case dialogDeleteProfile:
		if msg.Confirmed {
			if item, ok := m.profilesScreen().CurrentProfile(); ok {
				return m, profilescreen.Delete(m.cfg.ConfigDir, item.Name)
			}
		}
	case dialogActivateProfile:
		if msg.Confirmed {
			if item, ok := m.profilesScreen().CurrentProfile(); ok {
				return m, profilescreen.Activate(m.cfg.ConfigDir, item.Name)
			}
		}
	case dialogProfileParamSelect:
		if msg.Confirmed {
			return m.openProfileParamValueDialog(msg.Value)
		}
	case dialogProfileParamEdit:
		if msg.Confirmed {
			return m.applyProfileParamEdit(msg.Value)
		}
		m.pendingProfileParamKey = ""
	case dialogProfileParamAdd:
		if msg.Confirmed && strings.TrimSpace(msg.Value) != "" {
			return m.applyProfileParamAdd(msg.Value)
		}
	case dialogProfileParamDelete:
		key := strings.TrimSpace(m.pendingProfileParamKey)
		m.pendingProfileParamKey = ""
		if msg.Confirmed && key != "" {
			return m.applyProfileParamDelete(key)
		}
	}
	return m, nil
}

func (m rootModel) handleSyncDialogResult(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	switch msg.ID {
	case dialogSaveOnExit:
		m.quitting = true
		return m, tea.Quit
	case dialogSyncOnExit:
		if msg.Confirmed {
			switch msg.Value {
			case "Cancel":
				m.pendingExit = false
				return m, nil
			case "Customize...":
				scopes := []string{
					string(domain.ScopeProject),
					string(domain.ScopeGlobal),
				}
				d := newListSelectDialog(dialogSyncScope, "Select scope:", scopes)
				m = m.withDialog(d)
				return m, nil
			default:
				return m.doSync("", "")
			}
		}
		m.pendingExit = false
		return m, nil
	case dialogSyncScope:
		if msg.Confirmed && msg.Value != "" {
			m.exitSyncScope = msg.Value
			harnesses := domain.HarnessNames()
			d := newListSelectDialog(dialogSyncHarness, "Select harness:", harnesses)
			m = m.withDialog(d)
			return m, nil
		}
		m.pendingExit = false
		return m, nil
	case dialogSyncHarness:
		if msg.Confirmed && msg.Value != "" {
			return m.doSync(m.exitSyncScope, msg.Value)
		}
		m.pendingExit = false
		return m, nil
	case dialogSyncSelectProfile:
		if msg.Confirmed && msg.Value != "" {
			return m, tea.Batch(
				profilescreen.Activate(m.cfg.ConfigDir, msg.Value),
				profilescreen.Load(m.cfg.ConfigDir, m.cfg.SyncCfg),
			)
		}
	case dialogConfigHarnesses:
		if msg.Confirmed {
			m.cfg.SyncCfg.Defaults.Harnesses = slices.Clone(msg.Values)
			m, _ = m.syncConfigScreenContext(false)
			return m, saveSyncConfig(m.cfg.ConfigDir, m.cfg.SyncCfg)
		}
	}
	return m, nil
}

func (m rootModel) handlePackDialogResult(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	switch msg.ID {
	case dialogAddPack:
		if msg.Confirmed && msg.Value != "" {
			if msg.Value == addPackCreateSentinel {
				m.pendingCreateAddToProf = true
				d := newTextInputDialog(dialogCreatePack, "New pack Name:")
				m = m.withDialog(d)
				return m, nil
			}
			profiles := m.profilesScreen().AddPackToCurrent(msg.Value)
			m = m.setProfilesScreen(profiles)
			m.dirty = m.dirty || profiles.Dirty()
			var cmds []tea.Cmd
			cmds = append(cmds, profiles.ComputeFileSizesCmd())
			if saveCmd := profiles.SaveCurrentIfDirty(); saveCmd != nil {
				cmds = append(cmds, saveCmd)
			}
			return m, tea.Batch(cmds...)
		}
	case dialogRemovePack:
		if msg.Confirmed && msg.Value != "" {
			profiles := m.profilesScreen().RemovePackFromCurrent(msg.Value)
			m = m.setProfilesScreen(profiles)
			m.dirty = m.dirty || profiles.Dirty()
			var cmds []tea.Cmd
			if saveCmd := profiles.SaveCurrentIfDirty(); saveCmd != nil {
				cmds = append(cmds, saveCmd)
			}
			return m, tea.Batch(cmds...)
		}
	case dialogPackInstall:
		if msg.Confirmed && msg.Value != "" {
			// Local path installs get BundledAll automatically; skip the checklist.
			if !isRemoteInstallInput(msg.Value) {
				m.statusText = common.DimStyle.Render("installing pack...")
				return m, installPack(m.ctx, m.cfg.ConfigDir, msg.Value, "", nil)
			}
			m.installFlow.Input = msg.Value
			m.installFlow.Version = ""
			// Registry-name installs with cached version data get a version
			// picker step before the bundled-content checklist. URL installs,
			// raw-name installs without cached versions, and packs with no
			// semver tags fall through directly to the checklist — the
			// existing default-ref behavior preserves muscle memory and
			// keeps the picker from feeling like an obstacle when discovery
			// hasn't happened.
			if d, opened := m.maybeOpenInstallVersionDialog(msg.Value); opened {
				m = m.withDialog(d)
				return m, nil
			}
			d := newChecklistDialog(dialogInstallWith, "Include bundled content:", bundledCheckItems())
			m = m.withDialog(d)
			return m, nil
		}
	case dialogInstallVersion:
		// Result from the version picker. value is the literal version
		// string ("v1.2.3") or the "latest stable" sentinel; the sentinel
		// maps to an empty Version on the install request, which makes the
		// install path use the registry's default ref. Cancellation aborts
		// the install rather than installing at default — the user clearly
		// wanted a deliberate choice and changed their mind.
		if !msg.Confirmed {
			m.installFlow.reset()
			return m, nil
		}
		// Always assign — the sentinel maps to empty (default ref). Without
		// the unconditional clear, a stale value from a prior aborted
		// attempt could leak into the install request.
		if msg.Value == installVersionLatestSentinel {
			m.installFlow.Version = ""
		} else {
			m.installFlow.Version = msg.Value
		}
		d := newChecklistDialog(dialogInstallWith, "Include bundled content:", bundledCheckItems())
		m = m.withDialog(d)
		return m, nil
	case dialogInstallWith:
		input := m.installFlow.Input
		version := m.installFlow.Version
		m.installFlow.reset()
		// Esc cancels the whole install. Treating empty selection as
		// "install with nothing bundled" silently kicked off work the
		// user thought they were aborting.
		if !msg.Confirmed {
			return m, nil
		}
		with := parseBundledChecklist(msg.Values)
		m.statusText = common.DimStyle.Render("installing pack...")
		return m, installPack(m.ctx, m.cfg.ConfigDir, input, version, with)
	case dialogPackRemove:
		if msg.Confirmed {
			if pi, ok := m.packsScreen().CurrentInstalledPack(); ok {
				m.statusText = common.DimStyle.Render(fmt.Sprintf("removing %s...", pi.Entry.Name))
				return m, removePack(m.cfg.ConfigDir, pi.Entry.Name, m.cfg.Registry)
			}
		}
	case dialogUpdateWith:
		name := m.updateFlow.PackName
		all := m.updateFlow.All
		m.updateFlow.PackName = ""
		m.updateFlow.All = false
		// Note: not full updateFlow.reset() — Results are populated later by
		// the bundled-candidate flow and cleared in handleUpdateBundled.
		// Esc cancels the whole update. Treating empty selection as
		// "update with nothing bundled" silently kicked off work the
		// user thought they were aborting.
		if !msg.Confirmed {
			return m, nil
		}
		with := parseBundledChecklist(msg.Values)
		if all {
			m.statusText = common.DimStyle.Render("updating all packs...")
		} else {
			m.statusText = common.DimStyle.Render(fmt.Sprintf("updating %s...", name))
		}
		return m, updatePack(m.ctx, m.cfg.ConfigDir, name, all, "", with)
	case dialogPinVersion:
		// Pin move: thread the picked version straight to PackUpdate. No
		// bundled-content step — the user already approved bundled content
		// at install time and a pin move doesn't change those approvals.
		// Cancellation is a no-op (the existing pin stays in place).
		if !msg.Confirmed || msg.Value == "" {
			return m, nil
		}
		pi, ok := m.packsScreen().CurrentInstalledPack()
		if !ok {
			return m, nil
		}
		m.statusText = common.DimStyle.Render(fmt.Sprintf("pinning %s to %s...", pi.Entry.Name, msg.Value))
		return m, updatePack(m.ctx, m.cfg.ConfigDir, pi.Entry.Name, false, msg.Value, nil)
	case dialogPackAddToProfile:
		if msg.Confirmed && msg.Value != "" {
			pi, ok := m.packsScreen().CurrentInstalledPack()
			if !ok {
				return m, nil
			}
			profileName := msg.Value
			profiles, cmd := m.profilesScreen().AddPackToNamedProfile(profileName, pi.Entry.Name)
			m = m.setProfilesScreen(profiles)
			if cmd != nil {
				m.dirty = true
				m.statusText = common.DimStyle.Render(fmt.Sprintf("added %s to %s", pi.Entry.Name, profileName))
				return m, cmd
			}
		}
	}
	return m, nil
}

func (m rootModel) handleActionMenuResult(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	if !msg.Confirmed {
		return m, nil
	}

	// Handle actions that are context-independent across dialog IDs.
	switch msg.Value {
	case actEditSyncConfig:
		return m, openFileInEditor(filepath.Join(m.cfg.ConfigDir, "sync-config.yaml"))
	case actUpdateAll:
		m.updateFlow.PackName = ""
		m.updateFlow.All = true
		d := newChecklistDialog(dialogUpdateWith, "Include bundled content:", bundledCheckItems())
		m = m.withDialog(d)
		return m, nil
	}

	switch msg.ID {
	case dialogActionProfile:
		if msg.Value == actNewProfile {
			d := newTextInputDialog(dialogNewProfile, "New profile Name:")
			m = m.withDialog(d)
			return m, nil
		}
		item, ok := m.profilesScreen().CurrentProfile()
		if !ok {
			return m, nil
		}
		switch msg.Value {
		case actProfileAddPack:
			d := m.addPackDialog()
			m = m.withDialog(d)
			return m, nil
		case actEditFile, actOpenFile:
			return m, launchFile(msg.Value, item.Path)
		case actDuplicate:
			d := newTextInputDialog(dialogDuplicateProfile,
				fmt.Sprintf("Duplicate %q as:", item.Name))
			m = m.withDialog(d)
		case actActivate:
			d := newConfirmDialog(dialogActivateProfile,
				fmt.Sprintf("Set %q as the active/default profile?", item.Name))
			m = m.withDialog(d)
		case actDelete:
			d := newDestructiveConfirmDialog(dialogDeleteProfile,
				fmt.Sprintf("Delete profile %q?", item.Name))
			m = m.withDialog(d)
		}
	case dialogActionPack:
		switch msg.Value {
		case actProfileAddPack:
			d := m.addPackDialog()
			m = m.withDialog(d)
		case actProfileRemovePack:
			if pack, ok := m.profilesScreen().CurrentPack(); ok {
				profiles := m.profilesScreen().RemovePackFromCurrent(pack.Entry.Name)
				m = m.setProfilesScreen(profiles)
				m.dirty = m.dirty || profiles.Dirty()
				if saveCmd := profiles.SaveCurrentIfDirty(); saveCmd != nil {
					return m, saveCmd
				}
			} else {
				m.statusText = common.DimStyle.Render("no packs in profile")
			}
		case actSettingsDisable, actSettingsEnable:
			profiles := m.profilesScreen().ToggleCurrentPackSettings()
			m = m.setProfilesScreen(profiles)
			m.dirty = m.dirty || profiles.Dirty()
			if saveCmd := profiles.SaveCurrentIfDirty(); saveCmd != nil {
				return m, saveCmd
			}
		case actEditManifest, actOpenFile:
			if pack, ok := m.profilesScreen().CurrentPack(); ok {
				return m, launchFile(msg.Value, filepath.Join(m.cfg.ConfigDir, "packs", pack.Entry.Name, "pack.json"))
			}
		case actUpdate:
			if pack, ok := m.profilesScreen().CurrentPack(); ok {
				m.updateFlow.PackName = pack.Entry.Name
				m.updateFlow.All = false
				d := newChecklistDialog(dialogUpdateWith, "Include bundled content:", bundledCheckItems())
				m = m.withDialog(d)
				return m, nil
			}
		}
	case dialogActionSync:
		switch msg.Value {
		case actViewPlan:
			return m.openPlanView()
		case actSyncNow:
			return m.startSync()
		case actOpenSyncConfig, actOpenFile:
			return m, openFileWithSystem(filepath.Join(m.cfg.ConfigDir, "sync-config.yaml"))
		}
		return m, nil
	case dialogActionConfig:
		switch msg.Value {
		case actAddParam:
			if profile, ok := m.configProfile(); ok {
				m = m.setProfilesScreen(m.profilesScreen().ApplyCursorHint(profile.Name))
			}
			return m.openProfileParamValueDialog(addProfileParamSentinel)
		case actEditParam:
			if profile, ok := m.configProfile(); ok {
				m = m.setProfilesScreen(m.profilesScreen().ApplyCursorHint(profile.Name))
			}
			if key, ok := m.configScreen().CurrentParamKey(); ok {
				return m.openProfileParamValueDialog(key)
			}
			m.statusText = common.DimStyle.Render("no param selected")
			return m, nil
		case actDeleteParam:
			if profile, ok := m.configProfile(); ok {
				m = m.setProfilesScreen(m.profilesScreen().ApplyCursorHint(profile.Name))
			}
			if sel, ok := m.configScreen().CurrentParamSelection(); ok && sel.FromProfile {
				m.pendingProfileParamKey = sel.Key
				d := newDestructiveConfirmDialog(dialogProfileParamDelete,
					fmt.Sprintf("Delete param %s from profile?", sel.Key))
				m = m.withDialog(d)
				return m, nil
			}
			m.statusText = common.DimStyle.Render("no profile param selected")
			return m, nil
		case actAddEnv:
			d := newTextInputDialog(dialogConfigEnvAdd, "Env key=value:")
			m = m.withDialog(d)
			return m, nil
		case actEditEnv:
			if sel, ok := m.configScreen().CurrentEnvSelection(); ok && sel.FromDotEnv {
				return m.openConfigEnvEditDialog(sel.Key)
			}
			m.statusText = common.DimStyle.Render("no .env entry selected")
			return m, nil
		case actDeleteEnv:
			if sel, ok := m.configScreen().CurrentEnvSelection(); ok && sel.FromDotEnv {
				m.pendingConfigEnvKey = sel.Key
				d := newDestructiveConfirmDialog(dialogConfigEnvDelete,
					fmt.Sprintf("Delete %s from .env?", sel.Key))
				m = m.withDialog(d)
				return m, nil
			}
			m.statusText = common.DimStyle.Render("no .env entry selected")
			return m, nil
		}
	case dialogActionPackTab:
		switch msg.Value {
		case actCreatePack:
			d := newTextInputDialog(dialogCreatePack, "New pack Name:")
			m = m.withDialog(d)
			return m, nil
		case actEditManifest, actOpenFile:
			if pi, ok := m.packsScreen().CurrentInstalledPack(); ok {
				return m, launchFile(msg.Value, filepath.Join(m.cfg.ConfigDir, "packs", pi.Entry.Name, "pack.json"))
			}
		case actPreviewInstall:
			if li, ok := m.packsScreen().CurrentListItem(); ok && !li.Installed {
				p := newPreviewModel(m.width, m.height)
				m = m.withPreview(p)
				m.statusText = common.DimStyle.Render(fmt.Sprintf("inspecting %s...", li.Name))
				return m, previewInstallPack(m.ctx, m.cfg.ConfigDir, li)
			}
		case actInstall:
			// Pre-fill from the selected uninstalled pack when possible.
			prefill := ""
			if li, ok := m.packsScreen().CurrentListItem(); ok && !li.Installed {
				if li.InRegistry {
					prefill = li.Name
				} else if li.Repo != "" {
					prefill = li.Repo
				}
			}
			d := newTextInputDialog(dialogPackInstall, "Pack name, path, or URL:")
			if prefill != "" {
				d.TextValue = prefill
			}
			m = m.withDialog(d)
			return m, nil
		case actPackDelete:
			if pi, ok := m.packsScreen().CurrentInstalledPack(); ok {
				d := newDestructiveConfirmDialog(dialogPackRemove,
					fmt.Sprintf("Delete pack %q from disk?", pi.Entry.Name))
				m = m.withDialog(d)
			}
			return m, nil
		case actUpdate:
			if pi, ok := m.packsScreen().CurrentInstalledPack(); ok {
				m.updateFlow.PackName = pi.Entry.Name
				m.updateFlow.All = false
				d := newChecklistDialog(dialogUpdateWith, "Include bundled content:", bundledCheckItems())
				m = m.withDialog(d)
				return m, nil
			}
		case actPreviewUpdate:
			if pi, ok := m.packsScreen().CurrentInstalledPack(); ok {
				p := newPreviewModel(m.width, m.height)
				m = m.withPreview(p)
				m.statusText = common.DimStyle.Render(fmt.Sprintf("previewing update for %s...", pi.Entry.Name))
				return m, previewUpdatePack(m.ctx, m.cfg.ConfigDir, pi.Entry.Name)
			}
		case actPinVersion:
			if pi, ok := m.packsScreen().CurrentInstalledPack(); ok {
				if d, opened := m.openPinVersionDialog(pi.Entry.Name); opened {
					m = m.withDialog(d)
					return m, nil
				}
				m.statusText = common.DimStyle.Render("no versions available to pin")
				return m, nil
			}
		case actUnpin:
			if pi, ok := m.packsScreen().CurrentInstalledPack(); ok {
				m.statusText = common.DimStyle.Render(fmt.Sprintf("unpinning %s...", pi.Entry.Name))
				return m, updatePack(m.ctx, m.cfg.ConfigDir, pi.Entry.Name, false, "latest", nil)
			}
		case actAddToProfile:
			if pi, ok := m.packsScreen().CurrentInstalledPack(); ok {
				names := m.profilesWithoutPack(pi.Entry.Name)
				if len(names) == 0 {
					m.statusText = common.DimStyle.Render("pack is in all profiles")
					return m, nil
				}
				d := newListSelectDialog(dialogPackAddToProfile, "Add to profile:", names)
				m = m.withDialog(d)
				return m, nil
			}
		}
	}
	return m, nil
}

func (m rootModel) handleSaveActionResult(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	if !msg.Confirmed {
		return m, nil
	}
	switch msg.Value {
	case actPreview:
		if f := m.saveScreen().CurrentFile(); f != nil {
			p := newPreviewModel(m.width, m.height)
			m = m.withPreview(p)
			return m, loadPreview(
				savescreen.CandidateLabel(*f), f.Category, f.PackName, f.HarnessPath)
		}
	case actViewDiff:
		screen, cmd := m.saveScreen().OpenCurrentDiff()
		m = m.setSaveScreen(screen)
		return m, cmd
	case actEditFile, actOpenFile:
		if f := m.saveScreen().CurrentFile(); f != nil {
			return m, launchFile(msg.Value, f.HarnessPath)
		}
	case actDeleteFile:
		if f := m.saveScreen().CurrentFile(); f != nil {
			m.pendingDeletePath = f.HarnessPath
			d := newDestructiveConfirmDialog(dialogDeleteSaveFile,
				fmt.Sprintf("Delete %s from harness?", filepath.Base(f.HarnessPath)))
			m = m.withDialog(d)
			return m, nil
		}
	case actSaveToPack:
		screen, cmd := m.saveScreen().SelectCurrentOnlyAndAdvanceToPack()
		m = m.setSaveScreen(screen)
		return m, cmd
	}
	return m, nil
}

// switchToTab changes the active tab and fires any init commands needed.
func (m rootModel) switchToTab(target tabID) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if m.activeTab == tabSearch {
		var cmd tea.Cmd
		m, cmd = m.updateSearch(common.BlurMsg{})
		cmds = append(cmds, cmd)
	}
	if m.activeTab == tabSync {
		var cmd tea.Cmd
		m, cmd = m.updateSync(common.BlurMsg{})
		cmds = append(cmds, cmd)
	}
	if m.activeTab == tabConfig {
		var cmd tea.Cmd
		m, cmd = m.updateConfig(common.BlurMsg{})
		cmds = append(cmds, cmd)
	}
	if m.activeTab == tabSave {
		var cmd tea.Cmd
		m, cmd = m.updateSave(common.BlurMsg{})
		cmds = append(cmds, cmd)
	}
	m.activeTab = target
	if target == tabSave {
		var cmd tea.Cmd
		m.router = m.router.SetActive(tuiapp.ScreenSave)
		m, cmd = m.refreshSaveHarnesses()
		cmds = append(cmds, cmd)
		m, cmd = m.updateSave(common.FocusMsg{})
		cmds = append(cmds, cmd)
	}
	if target == tabPacks {
		var cmd tea.Cmd
		m.router = m.router.SetActive(tuiapp.ScreenPacks)
		m, cmd = m.updatePacks(common.FocusMsg{})
		cmds = append(cmds, cmd)
	}
	if target == tabConfig {
		var cmd tea.Cmd
		m.router = m.router.SetActive(tuiapp.ScreenConfig)
		m, cmd = m.syncConfigScreenContext(true)
		cmds = append(cmds, cmd)
		m, cmd = m.updateConfig(common.FocusMsg{})
		cmds = append(cmds, cmd)
	}
	if target == tabSync {
		var cmd tea.Cmd
		m.router = m.router.SetActive(tuiapp.ScreenSync)
		m, cmd = m.updateSync(common.FocusMsg{})
		cmds = append(cmds, cmd)
	}
	if target == tabSearch {
		var cmd tea.Cmd
		m.router = m.router.SetActive(tuiapp.ScreenSearch)
		m, cmd = m.updateSearch(common.FocusMsg{})
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// startExit initiates the exit flow: auto-save if dirty, then quit.
func (m rootModel) startExit() (tea.Model, tea.Cmd) {
	if m.dirty {
		profiles := m.profilesScreen()
		cmd := profiles.SaveAll()
		m.dirty = false
		m = m.setProfilesScreen(profiles.MarkAllClean())
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
	m.statusText = common.DimStyle.Render("refreshing...")
	var cmds []tea.Cmd
	// Always re-check sync status.
	var syncCmd tea.Cmd
	m, syncCmd = m.requestActiveSyncCheck()
	if syncCmd != nil {
		cmds = append(cmds, syncCmd)
	}
	if next, inspCmd := m.triggerInspect(); inspCmd != nil {
		m = next
		cmds = append(cmds, inspCmd)
	}
	// Reload profiles, packs, and registry from disk.
	cmds = append(cmds, profilescreen.Load(m.cfg.ConfigDir, m.cfg.SyncCfg), loadPacks(m.cfg.ConfigDir), loadRegistry(m.cfg.ConfigDir), packsscreen.LoadIndexDetails(m.cfg.ConfigDir))
	if m.activeTab == tabConfig {
		var refsCmd tea.Cmd
		m, refsCmd = m.syncConfigScreenContext(true)
		if refsCmd != nil {
			cmds = append(cmds, refsCmd)
		}
	}
	// On the packs tab, also force-refresh the version cache for the pack
	// under the cursor. Versions are kept in an in-memory cache keyed by
	// pack name that disk reloads don't touch — a user recovering from an
	// ssh-auth failure needs this cache cleared to re-probe the remote.
	if m.activeTab == tabPacks {
		packs, vcmd := m.packsScreen().RefreshVersionsForCursor()
		m = m.setPacksScreen(packs)
		if vcmd != nil {
			cmds = append(cmds, vcmd)
		}
	}
	if len(cmds) == 0 {
		m.statusText = common.DimStyle.Render("nothing to refresh")
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m rootModel) requestActiveSyncCheck() (rootModel, tea.Cmd) {
	snap := m.activeSyncSnapshot()
	if snap.ProfileName == "" {
		return m, nil
	}
	if snap.Status == common.SyncStatusLoading {
		m.pendingSyncRecheck = snap.ProfileName
		return m, nil
	}
	if m.pendingSyncRecheck == snap.ProfileName {
		m.pendingSyncRecheck = ""
	}
	return m, m.profilesScreen().CheckSyncCmd(m.cfg.SyncCfg, m.cfg.Registry)
}

func (m rootModel) syncCheckAndInspectCmds() (rootModel, []tea.Cmd) {
	var cmds []tea.Cmd
	m, cmds = m.syncCheckCmds()
	if next, inspCmd := m.triggerInspect(); inspCmd != nil {
		m = next
		cmds = append(cmds, inspCmd)
	}
	return m, cmds
}

func (m rootModel) syncCheckCmds() (rootModel, []tea.Cmd) {
	syncCmdModel, syncCmd := m.requestActiveSyncCheck()
	if syncCmd == nil {
		return syncCmdModel, nil
	}
	return syncCmdModel, []tea.Cmd{syncCmd}
}

// triggerInspect fires an async harness detection for the Save tab pipeline.
func (m rootModel) triggerInspect() (rootModel, tea.Cmd) {
	if _, ok := m.profilesScreen().ActiveProfile(); !ok {
		return m, nil
	}
	return m.refreshSaveHarnesses()
}

// startSave initiates the save flow: dry-run first, then show plan view.
func (m rootModel) startSave() (tea.Model, tea.Cmd) {
	item, ok := m.syncTargetItem()
	if !ok {
		m.statusText = common.DimStyle.Render("no active profile")
		return m, nil
	}
	m.statusText = common.DimStyle.Render("checking for changes...")
	return m, runSavePlan(m.ctx, m.eng, m.cfg.ConfigDir, item.Name, m.cfg.Registry)
}

// startSync initiates the sync flow: auto-save if dirty, then prompt sync.
func (m rootModel) startSync() (tea.Model, tea.Cmd) {
	m.pendingAutoSync = nil
	if m.dirty {
		profiles := m.profilesScreen()
		cmd := profiles.SaveAll()
		m.dirty = false
		m = m.setProfilesScreen(profiles.MarkAllClean())
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
	item, ok := m.syncTargetItem()
	if !ok {
		return m, nil
	}
	return m.doSyncItem(item, scope, harness)
}

func (m rootModel) doSyncItem(item profilescreen.ProfileSnapshot, scope, harness string) (tea.Model, tea.Cmd) {
	// Use defaults from target info if not overridden.
	if scope == "" {
		scope = string(item.SyncTarget.Scope)
	}
	if scope == "" {
		scope = string(domain.ScopeGlobal)
	}
	// An empty harness means "all configured harnesses" — runSync syncs each
	// resolved harness independently. The customize flow may pass a specific
	// harness to narrow the sync to one.

	m = m.setProfilesScreen(m.profilesScreen().MarkSyncStarted(item.Name))
	m.statusText = common.DimStyle.Render("syncing...")
	m.pendingExit = false
	m.pendingAutoSync = nil

	return m, runSync(m.ctx, m.eng, m.cfg.ConfigDir, item.Name, item.Path, scope, harness, m.cfg.SyncCfg, m.cfg.Registry)
}

// promptSync shows the sync options dialog for the target profile.
func (m rootModel) promptSync() (tea.Model, tea.Cmd) {
	item, ok := m.syncTargetItem()
	if !ok {
		m.quitting = true
		return m, tea.Quit
	}

	n := item.SyncTarget.TotalChanges()
	var title string
	if item.SyncStatus == common.SyncStatusUnsynced && n > 0 {
		title = fmt.Sprintf("Sync %q (%d pending changes):", item.Name, n)
	} else {
		title = fmt.Sprintf("Sync %q:", item.Name)
	}

	// Build default sync label from target info. The default sync targets all
	// configured harnesses, each synced independently.
	defaultLabel := "Sync"
	if len(item.SyncTarget.Harnesses) > 0 {
		defaultLabel = fmt.Sprintf("Sync (%s, %s)",
			strings.Join(item.SyncTarget.Harnesses, ", "),
			item.SyncTarget.Scope)
	}

	options := []string{defaultLabel, "Customize...", "Cancel"}
	d := newListSelectDialog(dialogSyncOnExit, title, options)
	m = m.withDialog(d)
	m.pendingExit = true // sync always exits after
	return m, nil
}

// syncTargetItem returns the profile that sync/plan/save actions should operate on.
// From the Config and Sync tabs it returns the active (default) profile; from
// the Profiles tab it returns the cursor-selected profile.
func (m rootModel) syncTargetItem() (profilescreen.ProfileSnapshot, bool) {
	if m.activeTab == tabConfig || m.activeTab == tabSync {
		return m.profilesScreen().ActiveProfile()
	}
	return m.profilesScreen().CurrentProfile()
}

// setActiveProfile optimistically marks a profile as active across all state.
func (m *rootModel) setActiveProfile(name string) {
	*m = m.setProfilesScreen(m.profilesScreen().SetActiveProfile(name))
	m.cfg.SyncCfg.Defaults.Profile = name
}

// activeSyncSnapshot returns the active profile's sync state as a snapshot
// in the form the sync screen consumes.
func (m rootModel) activeSyncSnapshot() common.SyncSnapshot {
	return m.profilesScreen().ActiveSyncSnapshot()
}

func (m rootModel) configProfile() (configscreen.Profile, bool) {
	item, ok := m.profilesScreen().ActiveProfile()
	if !ok {
		item, ok = m.profilesScreen().CurrentProfile()
	}
	if !ok {
		return configscreen.Profile{}, false
	}
	return configscreen.Profile{
		Name:   item.Name,
		Path:   item.Path,
		Config: item.Config,
	}, true
}

func (m rootModel) syncConfigScreenContext(loadRefs bool) (rootModel, tea.Cmd) {
	profile, ok := m.configProfile()
	if !ok {
		return m, nil
	}
	screen := m.configScreen().SetContext(profile, m.cfg.SyncCfg)
	m = m.setConfigScreen(screen)
	if loadRefs && m.cfg.ConfigDir != "" {
		return m, configscreen.LoadRefs(m.cfg.ConfigDir, profile)
	}
	return m, nil
}

// unregisteredPacks returns installed pack names not already in the current profile.
func (m rootModel) unregisteredPacks() []string {
	registered := map[string]bool{}
	for _, n := range m.profilesScreen().ProfilePackNames() {
		registered[n] = true
	}
	var names []string
	for _, name := range m.packsScreen().InstalledNames() {
		if !registered[name] {
			names = append(names, name)
		}
	}
	return names
}

// addPackDialog builds the "Add pack to profile" list dialog with a dimmed
// "Create new pack..." sentinel at the bottom.
func (m rootModel) addPackDialog() dialogModel {
	names := append(m.unregisteredPacks(), addPackCreateSentinel)
	d := newListSelectDialog(dialogAddPack, "Add pack to profile:", names)
	dim := make([]bool, len(names))
	dim[len(names)-1] = true
	d.ListDim = dim
	return d
}

func harnessCheckItems(enabled []string) []checkItem {
	items := make([]checkItem, 0, len(domain.HarnessNames()))
	for _, name := range domain.HarnessNames() {
		items = append(items, checkItem{
			Label:   name,
			Checked: slices.Contains(enabled, name),
		})
	}
	return items
}

func (m rootModel) openProfileParamValueDialog(key string) (tea.Model, tea.Cmd) {
	item, ok := m.profilesScreen().CurrentProfile()
	if !ok {
		return m, nil
	}
	if key == addProfileParamSentinel {
		d := newTextInputDialog(dialogProfileParamAdd, "Param key=value:")
		m = m.withDialog(d)
		return m, nil
	}
	m.pendingProfileParamKey = key
	d := newTextInputDialog(dialogProfileParamEdit, "Value for "+key+":")
	d.TextValue = item.Config.Params[key]
	m = m.withDialog(d)
	return m, nil
}

func (m rootModel) applyProfileParamEdit(value string) (tea.Model, tea.Cmd) {
	key := strings.TrimSpace(m.pendingProfileParamKey)
	m.pendingProfileParamKey = ""
	if key == "" {
		return m, nil
	}
	profiles, warning, cmd := m.profilesScreen().ApplyProfileParam(key, value)
	m = m.setProfilesScreen(profiles)
	m.dirty = true
	m.pendingConfigParamHint = key
	if warning != "" {
		m.statusText = common.DimStyle.Render(warning)
	}
	var refsCmd tea.Cmd
	if m.activeTab == tabConfig {
		m, refsCmd = m.syncConfigScreenContext(true)
		m = m.applyPendingConfigHint()
		if refsCmd != nil {
			m.pendingConfigParamHint = key
		}
	}
	return m, tea.Batch(cmd, refsCmd)
}

func (m rootModel) applyProfileParamAdd(input string) (tea.Model, tea.Cmd) {
	key, value, ok := strings.Cut(input, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" || strings.ContainsAny(key, "{} \t\r\n") {
		m.statusText = common.DimStyle.Render("invalid param")
		return m, nil
	}
	profiles, warning, cmd := m.profilesScreen().ApplyProfileParam(key, value)
	m = m.setProfilesScreen(profiles)
	m.dirty = true
	m.pendingConfigParamHint = key
	if warning != "" {
		m.statusText = common.DimStyle.Render(warning)
	}
	var refsCmd tea.Cmd
	if m.activeTab == tabConfig {
		m, refsCmd = m.syncConfigScreenContext(true)
		m = m.applyPendingConfigHint()
		if refsCmd != nil {
			m.pendingConfigParamHint = key
		}
	}
	return m, tea.Batch(cmd, refsCmd)
}

func (m rootModel) applyProfileParamDelete(key string) (tea.Model, tea.Cmd) {
	profiles, warning, cmd := m.profilesScreen().RemoveProfileParam(key)
	m = m.setProfilesScreen(profiles)
	m.dirty = true
	if warning != "" {
		m.statusText = common.DimStyle.Render(warning)
	} else {
		m.statusText = common.DimStyle.Render(fmt.Sprintf("removed %s from profile params", key))
	}
	var refsCmd tea.Cmd
	if m.activeTab == tabConfig {
		m, refsCmd = m.syncConfigScreenContext(true)
	}
	return m, tea.Batch(cmd, refsCmd)
}

func (m rootModel) openConfigEnvEditDialog(key string) (tea.Model, tea.Cmd) {
	key = strings.TrimSpace(key)
	if key == "" {
		return m, nil
	}
	value, ok, err := app.EnvGet(m.cfg.ConfigDir, key)
	if err != nil {
		m.statusText = common.ErrorStyle.Render(fmt.Sprintf("env get: %v", err))
		return m, nil
	}
	m.pendingConfigEnvKey = key
	d := newTextInputDialog(dialogConfigEnvEdit, "New value for "+key+":")
	if ok {
		d.TextValue = value
	}
	m = m.withDialog(d)
	return m, nil
}

func (m rootModel) applyConfigEnvAdd(input string) (tea.Model, tea.Cmd) {
	key, value, ok := strings.Cut(input, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		m.statusText = common.DimStyle.Render("invalid env entry")
		return m, nil
	}
	m.statusText = common.DimStyle.Render(fmt.Sprintf("setting %s...", key))
	return m, setConfigEnv(m.cfg.ConfigDir, key, value)
}

func (m rootModel) applyConfigEnvEdit(value string) (tea.Model, tea.Cmd) {
	key := strings.TrimSpace(m.pendingConfigEnvKey)
	m.pendingConfigEnvKey = ""
	if key == "" {
		return m, nil
	}
	m.statusText = common.DimStyle.Render(fmt.Sprintf("setting %s...", key))
	return m, setConfigEnv(m.cfg.ConfigDir, key, value)
}

// profilesWithoutPack returns profile names that don't already contain the given pack.
func (m rootModel) profilesWithoutPack(packName string) []string {
	return m.profilesScreen().ProfilesWithoutPack(packName)
}

// anyProfileDirty returns true if any profile has unsaved changes.
func (m rootModel) anyProfileDirty() bool {
	return m.profilesScreen().AnyDirty()
}

// countPendingSaves returns the number of profiles that will produce save commands.
func (m rootModel) countPendingSaves() int {
	return m.profilesScreen().PendingSaveCount()
}

// editCurrentFile opens $EDITOR for content files in browsing panels.
// Config/structural files are edited via action menus instead.
func (m rootModel) editCurrentFile() (tea.Model, tea.Cmd) {
	if fp := m.currentBrowsingFilePath(); fp != "" {
		return m, openFileInEditor(fp)
	}
	return m, nil
}

// openCurrentFile hands the same browsing-panel files to the OS default
// opener (`open` / `xdg-open` / `cmd /c start`). Detached — TUI keeps running.
func (m rootModel) openCurrentFile() (tea.Model, tea.Cmd) {
	if fp := m.currentBrowsingFilePath(); fp != "" {
		return m, openFileWithSystem(fp)
	}
	return m, nil
}

// currentBrowsingFilePath resolves the file under the cursor in browsing
// panels. Returns "" when the focus is on something that isn't a file.
func (m rootModel) currentBrowsingFilePath() string {
	switch m.activeTab {
	case tabProfiles:
		if fp := m.profilesScreen().CurrentBrowsingFilePath(); fp != "" {
			return fp
		}
	case tabPacks:
		if fp := m.packsScreen().CurrentBrowsingFilePath(); fp != "" {
			return fp
		}
	}
	return ""
}

func (m rootModel) View() tea.View {
	router := m.router
	if m.activeTab == tabSync && m.dialog == nil && m.picker == nil && m.preview == nil && m.planView == nil {
		router = router.Set(tuiapp.ScreenSync, m.syncScreen().WithContext(m.activeSyncSnapshot()))
	}
	if m.activeTab == tabConfig && m.dialog == nil && m.picker == nil && m.preview == nil && m.planView == nil {
		if profile, ok := m.configProfile(); ok {
			router = router.Set(tuiapp.ScreenConfig, m.configScreen().SetContext(profile, m.cfg.SyncCfg))
		}
	}
	root := lipgloss.NewLayer(m.renderFrame())
	layers := m.chromeLayers()
	overlay := m.activeOverlayLayer()
	if overlay != nil {
		layers = append(layers, overlay)
		return router.Compose(root, "", 0, layers...)
	}
	return router.Compose(root, m.activeScreenID(), m.contentOffsetY(), layers...)
}

func (m rootModel) activeOverlayLayer() *lipgloss.Layer {
	switch {
	case m.planView != nil:
		help := m.helpTextWithBack(m.planView.HelpText())
		pv := m.planView.SetSizeModel(m.width, m.fullScreenOverlayHeight(help))
		return pv.View().Z(10)
	case m.preview != nil:
		help := m.helpTextWithBack(m.preview.HelpText())
		p := m.preview.SetSizeModel(m.width, m.fullScreenOverlayHeight(help))
		return p.View().Z(10)
	case m.picker != nil:
		return m.picker.View().Y(m.contentOffsetY() + 1).Z(10)
	case m.dialog != nil:
		help := m.helpTextWithBack(m.dialog.HelpText())
		d := m.dialog.SetSizeModel(m.width, max(m.fullScreenOverlayHeight(help)-m.contentOffsetY()-1, 1))
		return d.View().Y(m.contentOffsetY() + 1).Z(10)
	default:
		return nil
	}
}

func (m rootModel) activeScreenID() tuiapp.ScreenID {
	switch m.activeTab {
	case tabProfiles:
		return tuiapp.ScreenProfiles
	case tabPacks:
		return tuiapp.ScreenPacks
	case tabConfig:
		return tuiapp.ScreenConfig
	case tabSave:
		return tuiapp.ScreenSave
	case tabSync:
		return tuiapp.ScreenSync
	case tabSearch:
		return tuiapp.ScreenSearch
	default:
		return ""
	}
}

func (m rootModel) handleChromeLayerHit(msg common.LayerHitMsg) (rootModel, tea.Cmd, bool) {
	if !common.IsLeftClick(msg.Mouse) {
		return m, nil, false
	}
	if msg.ID == chromeBackID {
		next, cmd := m.goBack()
		return next.(rootModel), cmd, true
	}
	raw, ok := strings.CutPrefix(msg.ID, "chrome:tab:")
	if !ok {
		return m, nil, false
	}
	idx, err := strconv.Atoi(raw)
	if err != nil || idx < 0 || idx >= tabCount {
		return m, nil, true
	}
	next, cmd := m.switchToTab(tabID(idx))
	return next.(rootModel), cmd, true
}

func (m rootModel) chromeLayers() []*lipgloss.Layer {
	if m.quitting {
		return nil
	}

	layers := make([]*lipgloss.Layer, 0, tabCount+1)
	if m.planView == nil && m.preview == nil {
		snap := m.activeSyncSnapshot()
		tabH := max(m.contentOffsetY(), 1)
		x := 0
		for i, name := range m.tabNames(snap.Status) {
			rendered := inactiveTabStyle.Render(name)
			if tabID(i) == m.activeTab {
				rendered = activeTabStyle.Render(name)
			}
			w := lipgloss.Width(rendered)
			if w <= 0 {
				continue
			}
			layers = append(layers,
				lipgloss.NewLayer(blankBlock(w, tabH)).
					ID(fmt.Sprintf("chrome:tab:%d", i)).
					X(x).
					Y(0).
					Z(-1),
			)
			x += w
		}
	}
	if back := m.backChromeLayer(); back != nil {
		layers = append(layers, back)
	}
	return layers
}

const (
	chromeBackID  = "chrome:back"
	backHelpLabel = "< Back"
)

func (m rootModel) backChromeLayer() *lipgloss.Layer {
	if !m.backAvailable() || m.width <= 0 || m.height <= 0 {
		return nil
	}
	return lipgloss.NewLayer(blankBlock(lipgloss.Width(backHelpLabel), 1)).
		ID(chromeBackID).
		X(0).
		Y(m.height - 1).
		Z(-1)
}

func (m rootModel) goBack() (tea.Model, tea.Cmd) {
	if !m.backAvailable() {
		return m, nil
	}
	return m.update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
}

func blankBlock(width, height int) string {
	width = max(width, 1)
	height = max(height, 1)
	lines := make([]string, height)
	for i := range lines {
		lines[i] = strings.Repeat(" ", width)
	}
	return strings.Join(lines, "\n")
}

func (m rootModel) contentOffsetY() int {
	snap := m.activeSyncSnapshot()
	return lipgloss.Height(m.renderTabLabels(snap.Status))
}

func (m rootModel) renderTabLabels(state common.SyncStatus) string {
	tabs := make([]string, 0, tabCount)
	for i, name := range m.tabNames(state) {
		if tabID(i) == m.activeTab {
			tabs = append(tabs, activeTabStyle.Render(name))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(name))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Bottom, tabs...)
}

func (m rootModel) renderTabBar(state common.SyncStatus) string {
	tabBar := m.renderTabLabels(state)
	gap := tabGapStyle.Render(strings.Repeat(" ", max(0, m.width-lipgloss.Width(tabBar))))
	return lipgloss.JoinHorizontal(lipgloss.Bottom, tabBar, gap)
}

func (m rootModel) fullScreenOverlayHeight(helpText string) int {
	help := helpBarStyle.Render(helpText)
	return max(0, m.height-lipgloss.Height(help))
}

func (m rootModel) renderFrame() string {
	if m.quitting {
		return ""
	}

	if m.planView != nil {
		helpText := m.helpTextWithBack(m.planView.HelpText())
		help := helpBarStyle.Render(helpText)
		h := m.fullScreenOverlayHeight(helpText)
		content := lipgloss.NewStyle().Height(h).MaxHeight(h).Render("")
		return content + "\n" + help
	}

	if m.preview != nil {
		helpText := m.helpTextWithBack(m.preview.HelpText())
		help := helpBarStyle.Render(helpText)
		h := m.fullScreenOverlayHeight(helpText)
		content := lipgloss.NewStyle().Height(h).MaxHeight(h).Render("")
		return content + "\n" + help
	}

	// Compute active profile snapshot once for tab bar + sync tab.
	snap := m.activeSyncSnapshot()

	// Tab bar.
	tabBar := m.renderTabBar(snap.Status)

	// Content — picker and dialog overlays replace tab content when active.
	// Picker takes precedence (it can't be stacked with a dialog anyway).
	content := ""
	help := helpBarStyle.Render(m.helpTextWithBack(m.helpText()))
	if m.picker != nil {
		help = helpBarStyle.Render(m.helpTextWithBack(m.picker.HelpText()))
	} else if m.dialog != nil {
		help = helpBarStyle.Render(m.helpTextWithBack(m.dialog.HelpText()))
	}

	// Status line: persistent profile context on the left, transient message on the right.
	statusLine := m.statusLine()

	// Fix content height so help bar is pinned to the bottom.
	contentH := m.height - lipgloss.Height(tabBar) - lipgloss.Height(statusLine) - lipgloss.Height(help)
	content = lipgloss.NewStyle().Height(max(0, contentH)).MaxHeight(max(0, contentH)).Render(content)

	return fmt.Sprintf("%s\n%s\n%s\n%s", tabBar, content, statusLine, help)
}

// --- Action menu system ---

const (
	dialogActionProfile     = "action-profile"
	dialogActionPack        = "action-pack"
	dialogActionPackTab     = "action-pack-tab"
	dialogActionConfig      = "action-config"
	dialogActionSync        = "action-sync"
	dialogBundledCandidates = "bundled-candidates"
)

// Action menu item labels — used in both open*Actions() and handleDialogResult().
const (
	actNewProfile      = "New profile"
	actDuplicate       = "Duplicate"
	actActivate        = "Activate"
	actDelete          = "Delete"
	actSettingsDisable = "Disable settings"
	actSettingsEnable  = "Enable settings"
	// Profile pack roster actions (pack enable/disable = profile membership).
	actProfileAddPack    = "Add to profile"
	actProfileRemovePack = "Remove from profile"
	// Packs tab actions (pack install/delete = disk operations).
	actInstall        = "Install"
	actPreviewInstall = "Preview install"
	actPackDelete     = "Delete"
	actUpdate         = "Update"
	actPreviewUpdate  = "Preview update"
	actAddToProfile   = "Add to profile"
	actUpdateAll      = "Update all"
	actPinVersion     = "Pin to version..."
	actUnpin          = "Unpin"
	// Save tab actions.
	actCreatePack         = "Create pack"
	addPackCreateSentinel = "Create new pack..."
	actMoveToPack         = "Move to pack"
	actPreview            = "Preview"
	actViewDiff           = "View diff"
	actSaveToPack         = "Save to pack"
	actDeleteFile         = "Delete file"
	// Config tab actions.
	actAddParam    = "Add param"
	actEditParam   = "Edit param"
	actDeleteParam = "Delete param"
	actAddEnv      = "Add env entry"
	actEditEnv     = "Edit env entry"
	actDeleteEnv   = "Delete env entry"
	// Override actions (content tree).
	actSetOverride    = "Set override"
	actRemoveOverride = "Remove override"
	// MCP tree action (content tree, MCP items — routes into the picker).
	actMCPToolList = "Tool list"
	// MCP tool-list bulk actions (surfaced inside the tool picker only).
	actMCPEnableAll      = "Enable all tools"
	actMCPAlwaysAllowAll = "Always allow all tools"
	actMCPDisableServer  = "Disable MCP server"
	actMCPEnableServer   = "Enable MCP server"
	actMCPReset          = "Reset to pack defaults"
	actMCPSaveInventory  = "Save probed inventory to pack"
	// Sync tab actions.
	actViewPlan       = "View plan"
	actSyncNow        = "Sync now"
	actOpenSyncConfig = "Open sync-config"
	// Edit actions (open in $EDITOR).
	actEditFile             = "Edit file"
	actEditManifest         = "Edit manifest"
	actEditSyncConfig       = "Edit sync-config"
	actOpenFile             = "Open file"
	addProfileParamSentinel = "Add param..."
)

// openActionMenu opens a context-sensitive action dialog based on the current focus.
func (m rootModel) openActionMenu() (tea.Model, tea.Cmd) {
	switch m.activeTab {
	case tabProfiles:
		switch m.profilesScreen().Focus() {
		case profilescreen.FocusProfiles:
			return m.openProfileActions()
		case profilescreen.FocusPacks:
			return m.openPackRosterActions()
		case profilescreen.FocusTree:
			return m.openTreeActions()
		}
	case tabSave:
		return m.openSaveActions()
	case tabPacks:
		switch m.packsScreen().Focus() {
		case packsscreen.FocusList:
			return m.openPackTabActions()
		case packsscreen.FocusContent, packsscreen.FocusPreview:
			return m.openPackContentActions()
		}
	case tabSync:
		return m.openSyncActions()
	case tabConfig:
		return m.openConfigActions()
	}
	return m, nil
}

func (m rootModel) openProfileActions() (tea.Model, tea.Cmd) {
	item, ok := m.profilesScreen().CurrentProfile()
	if !ok {
		d := newListSelectDialog(dialogActionProfile, "Profile actions:", []string{actNewProfile})
		m = m.withDialog(d)
		return m, nil
	}
	actions := []string{actNewProfile, actDuplicate}
	if !item.IsActive {
		actions = append(actions, actActivate)
	}
	if names := m.unregisteredPacks(); len(names) > 0 {
		actions = append(actions, actProfileAddPack)
	}
	actions = append(actions, actOpenFile, actEditFile, actDelete)
	if m.packsScreen().InstalledCount() > 0 {
		actions = append(actions, actUpdateAll)
	}
	d := newListSelectDialog(dialogActionProfile, "Profile actions:", actions)
	m = m.withDialog(d)
	return m, nil
}

func (m rootModel) openPackRosterActions() (tea.Model, tea.Cmd) {
	var actions []string
	if pack, ok := m.profilesScreen().CurrentPack(); ok {
		pe := pack.Entry
		// Offer update if the pack is installed on disk.
		if m.packsScreen().HasInstalled(pe.Name) {
			actions = append(actions, actUpdate)
		}
		actions = append(actions, actProfileRemovePack)
		// Offer settings toggle: disable if currently contributing, enable if opted out.
		if pe.Settings.Enabled != nil && !*pe.Settings.Enabled {
			actions = append(actions, actSettingsEnable)
		} else {
			actions = append(actions, actSettingsDisable)
		}
		actions = append(actions, actOpenFile, actEditManifest)
	}
	if m.packsScreen().InstalledCount() > 0 {
		actions = append(actions, actUpdateAll)
	}
	if len(actions) == 0 {
		m.statusText = common.DimStyle.Render("no actions available")
		return m, nil
	}
	d := newListSelectDialog(dialogActionPack, "Pack actions:", actions)
	m = m.withDialog(d)
	return m, nil
}

func (m rootModel) openTreeActions() (tea.Model, tea.Cmd) {
	sel, ok := m.profilesScreen().CurrentTreeSelection()
	if !ok || !sel.IsItem {
		m.statusText = common.DimStyle.Render("no actions available")
		return m, nil
	}
	// MCP items get a slim tree action menu — Edit file plus a Tool list
	// entry that routes into the tri-state picker. The broader bulk
	// actions (enable all / always allow / disable server / reset /
	// save inventory) live inside the picker itself so the tree menu
	// stays focused on the two most common intents.
	if sel.IsMCP {
		actions := []string{actOpenFile, actEditFile, actMCPToolList}
		d := newListSelectDialog(dialogActionTree,
			fmt.Sprintf("Actions for %s/%s:", sel.Category, sel.ID),
			actions)
		m = m.withDialog(d)
		return m, nil
	}
	var actions []string
	if sel.Conflict {
		if sel.IsOverride {
			actions = append(actions, actRemoveOverride)
		} else {
			actions = append(actions, actSetOverride)
		}
	}
	actions = append(actions, actOpenFile, actEditFile)
	d := newListSelectDialog(dialogActionTree,
		fmt.Sprintf("Actions for %s/%s:", sel.Category, sel.ID),
		actions)
	m = m.withDialog(d)
	return m, nil
}

func (m rootModel) handleTreeAction(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	if !msg.Confirmed {
		return m, nil
	}
	sel, ok := m.profilesScreen().CurrentTreeSelection()
	if !ok || !sel.IsItem {
		return m, nil
	}

	switch msg.Value {
	case actSetOverride:
		m = m.setProfilesScreen(m.profilesScreen().SetCurrentOverride())
	case actRemoveOverride:
		m = m.setProfilesScreen(m.profilesScreen().RemoveCurrentOverride())
	case actEditFile, actOpenFile:
		if sel.FilePath != "" {
			return m, launchFile(msg.Value, sel.FilePath)
		}
		return m, nil
	case actMCPToolList:
		return m.openMCPToolPicker()
	}
	profiles := m.profilesScreen()
	m.dirty = m.dirty || profiles.Dirty()
	if saveCmd := profiles.SaveCurrentIfDirty(); saveCmd != nil {
		return m, saveCmd
	}
	return m, nil
}

func (m rootModel) openPackTabActions() (tea.Model, tea.Cmd) {
	var actions []string
	li, hasList := m.packsScreen().CurrentListItem()
	item, hasInstalled := m.packsScreen().CurrentInstalledPack()
	installed := hasList && li.Installed
	clonePack := installed && hasInstalled && item.Entry.Method == config.MethodClone

	if installed {
		actions = append(actions, actPreviewUpdate)
		actions = append(actions, actUpdate)
	} else {
		actions = append(actions, actPreviewInstall)
		actions = append(actions, actInstall)
	}
	if m.packsScreen().InstalledCount() > 0 {
		actions = append(actions, actUpdateAll)
	}
	// Pin / Unpin entries surface the v0.21 pin model in the action menu.
	// Pin is offered when versions are loaded and at least one semver tag
	// exists; without that data the picker would be empty. Unpin is only
	// offered for packs that are actually pinned — otherwise it would be
	// a no-op that confuses the user about what state they're in.
	if clonePack {
		if cached, ok := m.packsScreen().VersionCache(li.Name); ok &&
			cached.Loaded && len(cached.Versions) > 0 {
			actions = append(actions, actPinVersion)
		}
		if item.Entry.PinLabel() != "" {
			actions = append(actions, actUnpin)
		}
	}
	if installed {
		actions = append(actions, actAddToProfile)
	}
	actions = append(actions, actCreatePack)
	if installed {
		actions = append(actions, actOpenFile, actEditManifest, actPackDelete)
	}

	d := newListSelectDialog(dialogActionPackTab, "Pack actions:", actions)
	m = m.withDialog(d)
	return m, nil
}

func (m rootModel) openPackContentActions() (tea.Model, tea.Cmd) {
	ci, ok := m.packsScreen().CurrentContentSelection()
	if !ok {
		m.statusText = common.DimStyle.Render("no actions available")
		return m, nil
	}
	if ci.IsHeader {
		m.statusText = common.DimStyle.Render("no actions available")
		return m, nil
	}
	if ci.FilePath == "" {
		m.statusText = common.DimStyle.Render("install pack to edit local files")
		return m, nil
	}
	var actions []string
	actions = append(actions, actOpenFile, actEditFile)
	// Only offer move if there are other installed packs.
	if len(m.packsScreen().MoveTargetNames()) > 0 {
		actions = append(actions, actMoveToPack)
	}
	d := newListSelectDialog(dialogActionContent,
		fmt.Sprintf("Actions for %s/%s:", ci.Category, ci.ID),
		actions)
	m = m.withDialog(d)
	return m, nil
}

func (m rootModel) handlePackContentAction(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	if !msg.Confirmed {
		return m, nil
	}
	if msg.Value == actEditFile || msg.Value == actOpenFile {
		if ci, ok := m.packsScreen().CurrentContentSelection(); ok && !ci.IsHeader && ci.FilePath != "" {
			return m, launchFile(msg.Value, ci.FilePath)
		}
		return m, nil
	}
	if msg.Value != actMoveToPack {
		return m, nil
	}
	ci, ok := m.packsScreen().CurrentContentSelection()
	if !ok || ci.IsHeader {
		return m, nil
	}

	// Build list of other installed packs as move targets.
	targets := m.packsScreen().MoveTargetNames()
	if len(targets) == 0 {
		m.statusText = common.DimStyle.Render("no other packs")
		return m, nil
	}
	d := newListSelectDialog(dialogContentMoveTo,
		fmt.Sprintf("Move %s/%s to:", ci.Category, ci.ID), targets)
	m = m.withDialog(d)
	return m, nil
}

func (m rootModel) handleContentMoveTo(msg dialogResultMsg) (tea.Model, tea.Cmd) {
	if !msg.Confirmed || msg.Value == "" {
		return m, nil
	}
	pi, ok := m.packsScreen().CurrentInstalledPack()
	if !ok {
		return m, nil
	}
	ci, ok := m.packsScreen().CurrentContentSelection()
	if !ok || ci.IsHeader {
		return m, nil
	}
	toPack := msg.Value
	fromPack := pi.Entry.Name

	m.statusText = common.DimStyle.Render(fmt.Sprintf("moving %s/%s to %s...", ci.Category, ci.ID, toPack))
	return m, moveContentToPack(m.eng, m.cfg.ConfigDir, ci.ID, ci.Category, fromPack, toPack)
}

func (m rootModel) openSyncActions() (tea.Model, tea.Cmd) {
	actions := []string{actViewPlan, actSyncNow, actOpenSyncConfig, actEditSyncConfig}
	d := newListSelectDialog(dialogActionSync, "Sync actions:", actions)
	m = m.withDialog(d)
	return m, nil
}

func (m rootModel) openConfigActions() (tea.Model, tea.Cmd) {
	var actions []string
	screen := m.configScreen()
	switch screen.SectionName() {
	case "params":
		actions = append(actions, actAddParam)
		if sel, ok := screen.CurrentParamSelection(); ok {
			actions = append(actions, actEditParam)
			if sel.FromProfile {
				actions = append(actions, actDeleteParam)
			}
		}
	case "env":
		actions = append(actions, actAddEnv)
		if sel, ok := screen.CurrentEnvSelection(); ok && sel.FromDotEnv {
			actions = append(actions, actEditEnv, actDeleteEnv)
		}
	}
	if len(actions) == 0 {
		m.statusText = common.DimStyle.Render("no actions available")
		return m, nil
	}
	d := newListSelectDialog(dialogActionConfig, "Config actions:", actions)
	m = m.withDialog(d)
	return m, nil
}

func (m rootModel) openSaveActions() (tea.Model, tea.Cmd) {
	if !m.saveScreen().HasFileActions() {
		m.statusText = common.DimStyle.Render("no actions available")
		return m, nil
	}
	f := m.saveScreen().CurrentFile()
	if f == nil {
		m.statusText = common.DimStyle.Render("no actions available")
		return m, nil
	}
	actions := []string{actPreview, actViewDiff, actOpenFile, actEditFile, actDeleteFile, actSaveToPack}
	d := newListSelectDialog(dialogActionSave,
		fmt.Sprintf("Actions for %s:", filepath.Base(f.HarnessPath)), actions)
	m = m.withDialog(d)
	return m, nil
}

// tabNames returns the tab labels with dynamic status dot for the Sync tab.
func (m rootModel) tabNames(state common.SyncStatus) []string {
	syncLabel := "Sync"
	switch state {
	case common.SyncStatusSynced:
		syncLabel = "Sync " + statusDotActive
	case common.SyncStatusUnsynced:
		syncLabel = "Sync " + statusDotInactive
	case common.SyncStatusLoading:
		syncLabel = "Sync " + statusDotLoading
	case common.SyncStatusError:
		syncLabel = "Sync " + statusDotInactive
	}
	saveLabel := "Save"
	return []string{"Profiles", "Packs", syncLabel, saveLabel, "Search", "Config"}
}

func (m rootModel) showWarnings() (tea.Model, tea.Cmd) {
	item, ok := m.profilesScreen().ActiveProfile()
	if !ok || len(item.SyncWarnings) == 0 {
		m.statusText = common.DimStyle.Render("no warnings")
		return m, nil
	}
	lines := make([]string, len(item.SyncWarnings))
	for i, w := range item.SyncWarnings {
		lines[i] = w.String()
	}
	d := newListSelectDialog(dialogWarnings, fmt.Sprintf("Warnings (%d):", len(lines)), lines)
	m = m.withDialog(d)
	return m, nil
}

// statusLine returns a persistent info bar showing active profile + transient status.
func (m rootModel) statusLine() string {
	// Left side: active profile summary.
	var left string
	if item, ok := m.profilesScreen().ActiveProfile(); ok {
		dot := statusDotInactive
		switch item.SyncStatus {
		case common.SyncStatusSynced:
			dot = statusDotActive
		case common.SyncStatusLoading:
			dot = statusDotLoading
		}
		packCount := len(item.Config.Packs)
		noun := "packs"
		if packCount == 1 {
			noun = "pack"
		}
		left = fmt.Sprintf(" %s %s (%d %s)", dot, item.Name, packCount, noun)
	}

	// Right side: transient status message (or empty). Prefix with the
	// shared spinner glyph while a pack install is in flight — the install
	// has no per-row decoration (the pack doesn't exist yet) so the
	// status bar is the only material evidence the operation is alive.
	right := m.statusText
	if right == "" && m.activeTab == tabProfiles {
		if hint := m.profilesScreen().StatusHint(); hint != "" {
			right = hint
		}
	}
	if m.activeTab == tabPacks && m.packsScreen().ActiveInstall() {
		if right == "" {
			right = common.DimStyle.Render(m.packsScreen().InstallStatus())
		}
		if right != "" {
			right = m.packsScreen().SpinnerView() + " " + right
		}
	}

	// Compose: left-align profile, right-align status.
	leftW := lipgloss.Width(left)
	if m.width > 0 {
		right = fitStatusSegment(right, max(m.width-leftW-2, 0))
	}
	rightW := lipgloss.Width(right)
	gap := max(2, m.width-leftW-rightW)
	line := panelSubtleStyle.Render(left) + strings.Repeat(" ", gap) + right
	return line
}

func fitStatusSegment(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

func (m rootModel) helpTextWithBack(help string) string {
	if !m.backAvailable() {
		return help
	}
	if help == "" {
		return backHelpLabel
	}
	return backHelpLabel + " │ " + help
}

func (m rootModel) backAvailable() bool {
	if m.planView != nil || m.preview != nil || m.picker != nil || m.dialog != nil {
		return true
	}
	switch m.activeTab {
	case tabProfiles:
		return !m.profilesScreen().IsProfilesFocused()
	case tabPacks:
		return !m.packsScreen().IsListFocused()
	case tabConfig:
		return m.configScreen().IsContentFocused()
	case tabSave:
		return strings.Contains(m.saveScreen().HelpText(), "esc:back")
	case tabSearch:
		return strings.Contains(m.searchScreen().HelpText(), "esc:back")
	default:
		return false
	}
}

// helpText returns context-sensitive key binding hints.
func (m rootModel) helpText() string {
	base := ""
	switch m.activeTab {
	case tabProfiles:
		switch m.profilesScreen().Focus() {
		case profilescreen.FocusProfiles:
			base = "j/k:navigate  enter:packs  .:actions │ v:plan  s:sync  ctrl+s:save  r:refresh │ 1-6/tab:switch  esc:quit"
		case profilescreen.FocusPacks:
			base = "j/k:navigate  J/K:reorder  space:toggle  enter:tree  .:actions │ esc:back"
		case profilescreen.FocusTree:
			base = "j/k:navigate  space:toggle  enter:preview  e:edit  o:open  .:actions │ v:plan  s:sync  ctrl+s:save │ esc:back"
			if sel, ok := m.profilesScreen().CurrentTreeSelection(); ok && sel.IsMCP {
				base = "j/k:navigate  space:toggle  enter/t:tools  e:edit  o:open  .:actions │ v:plan  s:sync  ctrl+s:save │ esc:back"
			}
		}
	case tabPacks:
		switch m.packsScreen().Focus() {
		case packsscreen.FocusContent:
			return "j/k:navigate  enter:preview  e:edit  o:open  .:actions │ esc:back"
		case packsscreen.FocusPreview:
			return "j/k:scroll  enter:preview  e:edit  o:open  .:actions │ esc:back"
		default:
			return "j/k:navigate  enter:content  .:actions  r:refresh │ 1-6/tab:switch  esc:quit"
		}
	case tabConfig:
		base = m.configScreen().HelpText() + " │ v:plan  s:sync  r:refresh │ 1-6/tab:switch  esc:quit"
	case tabSave:
		base = m.saveScreen().HelpText()
		if base == "" {
			base = "1-6/tab:switch  esc:quit"
		}
	case tabSync:
		base = "v:plan  s:sync  ctrl+s:save  .:actions  r:refresh │ 1-6/tab:switch  esc:quit"
	case tabSearch:
		return m.searchScreen().HelpText()
	default:
		return "1-6/tab:switch  esc:quit"
	}
	if item, ok := m.profilesScreen().ActiveProfile(); ok && len(item.SyncWarnings) > 0 {
		base += "  " + common.WarningStyle.Render("w:warnings")
	}
	return base
}

const statusClearDuration = 3 * time.Second
const autoSyncDelay = 7 * time.Second

// scheduleStatusClear returns a tea.Cmd that clears the status text
// after statusClearDuration, if the status ID still matches.
func scheduleStatusClear(id int) tea.Cmd {
	return tea.Tick(statusClearDuration, func(time.Time) tea.Msg {
		return statusClearMsg{id: id}
	})
}

func (m rootModel) syncCheckAndMaybeAutoSync(profileName string, allowAutoSync bool, skipCheckWhenAutoScheduled bool) (rootModel, tea.Cmd) {
	var autoCmd tea.Cmd
	if allowAutoSync {
		m, autoCmd = m.scheduleAutoSyncForActiveProfile(profileName)
		if autoCmd != nil && skipCheckWhenAutoScheduled {
			return m, autoCmd
		}
	}
	var cmds []tea.Cmd
	m, cmds = m.syncCheckCmds()
	if autoCmd != nil {
		cmds = append(cmds, autoCmd)
	}
	return m, tea.Batch(cmds...)
}

func (m rootModel) scheduleAutoSyncForActiveProfile(profileName string) (rootModel, tea.Cmd) {
	if !m.cfg.SyncCfg.Defaults.AutoSync {
		return m, nil
	}
	active, ok := m.profilesScreen().ActiveProfile()
	if !ok || active.Name != profileName {
		return m, nil
	}
	m.autoSyncSeq++
	pending := pendingAutoSyncState{id: m.autoSyncSeq, profileName: profileName}
	m.pendingAutoSync = &pending
	return m, scheduleAutoSync(pending)
}

func (m rootModel) profileSaveStatus(profileName string) string {
	if m.cfg.SyncCfg.Defaults.AutoSync {
		return "saved"
	}
	active, ok := m.profilesScreen().ActiveProfile()
	if ok && active.Name == profileName {
		return "saved; press s to sync"
	}
	return "saved"
}

func scheduleAutoSync(pending pendingAutoSyncState) tea.Cmd {
	return tea.Tick(autoSyncDelay, func(time.Time) tea.Msg {
		return autoSyncDueMsg(pending)
	})
}

// --- Bundled content helpers ---

// isRemoteInstallInput returns true if the input looks like a URL or registry name
// (as opposed to a local path). Mirrors the detection logic in installPack.
func isRemoteInstallInput(input string) bool {
	if source.IsRemoteInstallInput(input) {
		return true
	}
	return isRegistryName(input)
}

// installVersionLatestSentinel is the synthetic list-select item that maps to
// "install at the registry's default ref" — i.e., no version pin. Distinct
// from a literal "v0.0.0" tag so the picker can distinguish "user wants
// latest stable, stay unpinned" from "user picked the v0.0.0 tag specifically".
const installVersionLatestSentinel = "latest stable (default)"

// openPinVersionDialog returns a version picker dialog for moving the pin
// on an already-installed clone pack. Skips the "latest stable" sentinel
// because pinning to "latest" is the responsibility of the Unpin action,
// not the picker (the two intents are different even though they share
// underlying machinery). Returns (zero, false) when there's nothing to
// pick from — caller should surface a status text in that case.
func (m *rootModel) openPinVersionDialog(name string) (dialogModel, bool) {
	cached, ok := m.packsScreen().VersionCache(name)
	if !ok || !cached.Loaded || len(cached.Versions) == 0 {
		return dialogModel{}, false
	}
	items := make([]string, len(cached.Versions))
	for i, v := range cached.Versions {
		items[i] = v.Version
	}
	d := newListSelectDialog(dialogPinVersion, "Pin "+name+" to:", items)
	return d, true
}

// maybeOpenInstallVersionDialog returns a version picker dialog when the
// install input is a registry name AND the versions cache for that name has
// at least one semver tag loaded. Returns (zero, false) when the picker
// should be skipped — URL installs, raw paths, packs with no cached version
// data, packs with no semver tags. Skipping is intentional: the picker is
// a discovery affordance, not a hard gate, and the existing default-ref
// install path remains the fallback.
func (m *rootModel) maybeOpenInstallVersionDialog(input string) (dialogModel, bool) {
	if source.IsRemoteInstallInput(input) {
		return dialogModel{}, false
	}
	if !isRegistryName(input) {
		return dialogModel{}, false
	}
	cached, ok := m.packsScreen().VersionCache(input)
	if !ok || !cached.Loaded || len(cached.Versions) == 0 {
		return dialogModel{}, false
	}
	items := make([]string, 0, len(cached.Versions)+1)
	items = append(items, installVersionLatestSentinel)
	for _, v := range cached.Versions {
		items = append(items, v.Version)
	}
	d := newListSelectDialog(dialogInstallVersion, "Pick a version:", items)
	return d, true
}

// bundledCheckItems returns the standard checklist items for bundled content selection.
func bundledCheckItems() []checkItem {
	var items []checkItem
	for _, cat := range domain.AllBundledCategories {
		items = append(items, checkItem{
			Label:   string(cat),
			Checked: cat == domain.BundledProfiles,
		})
	}
	return items
}

func bundledUpdateChecklist(results []app.PackUpdateResult) ([]checkItem, bool) {
	available := domain.NewBundledSet()
	newCandidates := domain.NewBundledSet()
	declinedCandidates := domain.NewBundledSet()
	for _, result := range results {
		newCategories, declinedCategories := result.BundledCandidates.PartitionCategories()
		for _, category := range newCategories {
			available[category] = true
			newCandidates[category] = true
		}
		for _, category := range declinedCategories {
			available[category] = true
			declinedCandidates[category] = true
		}
	}
	var items []checkItem
	for _, category := range domain.AllBundledCategories {
		if !available.Has(category) {
			continue
		}
		items = append(items, checkItem{
			Label:   string(category),
			Checked: category == domain.BundledProfiles && newCandidates.Has(category),
		})
	}
	return items, len(declinedCandidates) > 0
}

// parseBundledChecklist converts selected checklist values into a BundledSet.
func parseBundledChecklist(values []string) domain.BundledSet {
	if len(values) == 0 {
		return nil
	}
	var cats []domain.BundledCategory
	for _, v := range values {
		if c, ok := domain.ParseBundledCategory(v); ok {
			cats = append(cats, c)
		}
	}
	if len(cats) == 0 {
		return nil
	}
	return domain.NewBundledSet(cats...)
}
