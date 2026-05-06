package profiles

import (
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
)

type Focus int

const (
	FocusProfiles Focus = iota
	FocusPacks
	FocusTree
)

type ProfileSnapshot struct {
	Name         string
	Path         string
	IsActive     bool
	SyncStatus   common.SyncStatus
	SyncTarget   common.SyncTarget
	SyncErrText  string
	SyncWarnings []domain.Warning
	BundledFrom  []string
	Config       config.ProfileConfig
	Dirty        bool
}

type Seed struct {
	Name         string
	Path         string
	IsActive     bool
	SyncStatus   common.SyncStatus
	SyncTarget   common.SyncTarget
	SyncErrText  string
	SyncWarnings []domain.Warning
	BundledFrom  []string
	Config       config.ProfileConfig
	Dirty        bool
}

type PackSelection struct {
	Entry config.PackEntry
	Index int
}

type TreeSelection struct {
	Category            domain.PackCategory
	ID                  string
	PackIdx             int
	PackName            string
	PackRoot            string
	FilePath            string
	IsItem              bool
	IsMCP               bool
	Enabled             bool
	Conflict            bool
	IsOverride          bool
	IsOverridden        bool
	CompetingOverride   bool
	MCPToolsEnabled     int
	MCPToolsAvailable   int
	ProfileEnabledTools int
	ProfileTotalTools   int
}

func (m Model) InitCmd(syncCfg config.SyncConfig) tea.Cmd {
	return m.initCmd(syncCfg)
}

func (m Model) CheckSyncCmd(syncCfg config.SyncConfig, reg *harness.Registry) tea.Cmd {
	return m.checkSyncCmd(syncCfg, reg)
}

func (m Model) SaveAll() tea.Cmd {
	return m.saveAll()
}

func (m Model) ComputeFileSizesCmd() tea.Cmd {
	return m.computeFileSizesCmd()
}

func (m Model) Dirty() bool {
	return m.dirty
}

func (m Model) LoadErr() string {
	return m.loadErr
}

func (m Model) Focus() Focus {
	return Focus(m.focus)
}

func (m Model) WithProfiles(seeds []Seed) Model {
	m.items = itemsFromSeeds(seeds)
	m.dirty = false
	for _, item := range m.items {
		if item.dirty {
			m.dirty = true
			break
		}
	}
	if m.cursor >= len(m.items) {
		m.cursor = max(len(m.items)-1, 0)
	}
	return m
}

func LoadedFromSeeds(seeds []Seed) LoadedMsg {
	return LoadedMsg{Items: itemsFromSeeds(seeds)}
}

func itemsFromSeeds(seeds []Seed) []profileItem {
	items := make([]profileItem, len(seeds))
	for i, seed := range seeds {
		items[i] = profileItem{
			name:         seed.Name,
			path:         seed.Path,
			isActive:     seed.IsActive,
			syncState:    seed.SyncStatus,
			syncTarget:   seed.SyncTarget,
			syncErrText:  seed.SyncErrText,
			syncWarnings: slices.Clone(seed.SyncWarnings),
			bundledFrom:  slices.Clone(seed.BundledFrom),
			cfg:          cloneProfileConfig(seed.Config),
			dirty:        seed.Dirty,
		}
	}
	return items
}

func (m Model) SetCursor(cursor int) Model {
	if len(m.items) == 0 {
		m.cursor = 0
		return m
	}
	m.cursor = min(max(cursor, 0), len(m.items)-1)
	m.clampProfileOffset()
	return m
}

func (m Model) SetFocus(focus Focus) Model {
	m.focus = panelFocus(focus)
	return m
}

func (m Model) IsTreeFocused() bool {
	return m.focus == panelTree
}

func (m Model) IsProfilesFocused() bool {
	return m.focus == panelProfiles
}

func (m Model) AnyDirty() bool {
	for _, item := range m.items {
		if item.dirty {
			return true
		}
	}
	return false
}

func (m Model) PendingSaveCount() int {
	count := 0
	for _, item := range m.items {
		if item.dirty {
			count++
		}
	}
	return count
}

func (m Model) MarkAllClean() Model {
	m.dirty = false
	return m
}

func (m Model) MarkSaved(profileName string) Model {
	for i := range m.items {
		if m.items[i].name == profileName {
			m.items[i].syncState = common.SyncStatusUnsynced
			m.items[i].dirty = false
		}
	}
	m.dirty = m.AnyDirty()
	return m
}

func (m Model) MarkSyncStarted(profileName string) Model {
	for i := range m.items {
		if m.items[i].name == profileName {
			m.items[i].syncState = common.SyncStatusLoading
			break
		}
	}
	return m
}

func (m Model) MarkSyncDone(profileName string, err error, warnings []domain.Warning) Model {
	for i := range m.items {
		if m.items[i].name == profileName {
			if err != nil {
				m.items[i].syncState = common.SyncStatusError
				m.items[i].syncErrText = err.Error()
			} else {
				m.items[i].syncState = common.SyncStatusPending
				m.items[i].syncErrText = ""
				m.items[i].syncWarnings = slices.Clone(warnings)
			}
			break
		}
	}
	return m
}

func (m Model) SetActiveProfile(name string) Model {
	for i := range m.items {
		m.items[i].isActive = (m.items[i].name == name)
	}
	return m
}

func (m Model) ProfileNames() []string {
	names := make([]string, len(m.items))
	for i, item := range m.items {
		names[i] = item.name
	}
	return names
}

func (m Model) ApplyCursorHint(name string) Model {
	for i, item := range m.items {
		if item.name == name {
			m.cursor = i
			m.clampProfileOffset()
			m = m.ensureTree()
			break
		}
	}
	return m
}

func (m Model) ActiveSyncSnapshot() common.SyncSnapshot {
	if item := m.activeItem(); item != nil {
		return common.SyncSnapshot{
			ProfileName: item.name,
			Status:      item.syncState,
			Target:      item.syncTarget,
			ErrText:     item.syncErrText,
			Warnings:    slices.Clone(item.syncWarnings),
		}
	}
	return common.SyncSnapshot{}
}

func (m Model) CurrentProfile() (ProfileSnapshot, bool) {
	return snapshotProfile(m.currentItem())
}

func (m Model) ActiveProfile() (ProfileSnapshot, bool) {
	return snapshotProfile(m.activeItem())
}

func (m Model) SyncTargetProfile(active bool) (ProfileSnapshot, bool) {
	if active {
		return m.ActiveProfile()
	}
	return m.CurrentProfile()
}

func snapshotProfile(item *profileItem) (ProfileSnapshot, bool) {
	if item == nil {
		return ProfileSnapshot{}, false
	}
	return ProfileSnapshot{
		Name:         item.name,
		Path:         item.path,
		IsActive:     item.isActive,
		SyncStatus:   item.syncState,
		SyncTarget:   item.syncTarget,
		SyncErrText:  item.syncErrText,
		SyncWarnings: slices.Clone(item.syncWarnings),
		BundledFrom:  slices.Clone(item.bundledFrom),
		Config:       cloneProfileConfig(item.cfg),
		Dirty:        item.dirty,
	}, true
}

func cloneProfileConfig(in config.ProfileConfig) config.ProfileConfig {
	out := in
	out.Packs = slices.Clone(in.Packs)
	if in.Params != nil {
		out.Params = make(map[string]string, len(in.Params))
		maps.Copy(out.Params, in.Params)
	}
	return out
}

func (m Model) ProfilePackNames() []string {
	return m.profilePackNames()
}

func (m Model) CurrentPack() (PackSelection, bool) {
	item := m.currentItem()
	if item == nil || m.packCursor < 0 || m.packCursor >= len(item.cfg.Packs) {
		return PackSelection{}, false
	}
	return PackSelection{Entry: item.cfg.Packs[m.packCursor], Index: m.packCursor}, true
}

func (m Model) CurrentTreeSelection() (TreeSelection, bool) {
	item := m.currentItem()
	if item == nil || item.tree == nil {
		return TreeSelection{}, false
	}
	n := item.tree.cursorNode()
	if n == nil {
		return TreeSelection{}, false
	}
	sel := TreeSelection{
		Category:            n.category,
		ID:                  n.id,
		PackIdx:             n.packIdx,
		IsItem:              n.kind == nodeItem,
		IsMCP:               n.kind == nodeItem && n.category == domain.CategoryMCP,
		Enabled:             n.enabled,
		Conflict:            n.conflict,
		IsOverride:          n.isOverride,
		IsOverridden:        n.isOverridden,
		CompetingOverride:   n.competingOverride,
		MCPToolsEnabled:     n.mcpToolsEnabled,
		MCPToolsAvailable:   n.mcpToolsAvailable,
		FilePath:            item.tree.filePath(),
		ProfileTotalTools:   item.tree.mcpCounts.TotalAvailable,
		ProfileEnabledTools: item.tree.mcpCounts.TotalEnabled,
	}
	if n.packIdx >= 0 && n.packIdx < len(item.tree.packs) {
		packInfo := item.tree.packs[n.packIdx]
		sel.PackName = packInfo.Name
		sel.PackRoot = packInfo.Root
	}
	return sel, true
}

func (m Model) StatusHint() string {
	if m.focus != panelTree {
		return ""
	}
	item := m.currentItem()
	if item == nil || item.tree == nil {
		return ""
	}
	n := item.tree.cursorNode()
	if n == nil || n.kind != nodeItem || n.id == "" || n.packIdx < 0 || n.packIdx >= len(item.tree.packs) {
		return ""
	}
	pack := item.tree.packs[n.packIdx]
	return n.id + " from " + packColorBright(pack.Index).Render(pack.Name)
}

func (m Model) CurrentBrowsingFilePath() string {
	if m.focus != panelTree {
		return ""
	}
	if sel, ok := m.CurrentTreeSelection(); ok {
		return sel.FilePath
	}
	return ""
}

func (m Model) ProfilesWithoutPack(packName string) []string {
	var names []string
	for _, item := range m.items {
		has := false
		for _, pe := range item.cfg.Packs {
			if pe.Name == packName {
				has = true
				break
			}
		}
		if !has {
			names = append(names, item.name)
		}
	}
	return names
}

func (m Model) AddPackToCurrent(packName string) Model {
	return m.addPackToProfile(packName)
}

func (m Model) AddPackToNamedProfile(profileName, packName string) (Model, tea.Cmd) {
	for i := range m.items {
		if m.items[i].name == profileName {
			m.items[i].cfg.Packs = append(m.items[i].cfg.Packs, config.PackEntry{Name: packName})
			m.items[i].dirty = true
			m.dirty = true
			return m, Save(m.configDir, profileName, m.items[i].cfg)
		}
	}
	return m, nil
}

func (m Model) RemovePackFromCurrent(packName string) Model {
	return m.removePackFromProfile(packName)
}

func (m Model) RemoveCurrentPack() Model {
	pack, ok := m.CurrentPack()
	if !ok {
		return m
	}
	return m.removePackFromProfile(pack.Entry.Name)
}

func (m Model) ToggleCurrentPackSettings() Model {
	pack, ok := m.CurrentPack()
	if !ok {
		return m
	}
	return m.toggleSettingsDisabled(pack.Entry.Name)
}

func (m Model) SetCurrentOverride() Model {
	sel, ok := m.CurrentTreeSelection()
	if !ok || !sel.IsItem {
		return m
	}
	return m.setOverride(sel.PackIdx, sel.Category, sel.ID)
}

func (m Model) RemoveCurrentOverride() Model {
	sel, ok := m.CurrentTreeSelection()
	if !ok || !sel.IsItem {
		return m
	}
	return m.removeOverride(sel.PackIdx, sel.Category, sel.ID)
}

func (m Model) SaveCurrentIfDirty() tea.Cmd {
	item := m.currentItem()
	if item == nil || !item.dirty {
		return nil
	}
	return Save(m.configDir, item.name, item.cfg)
}

func (m Model) ApplyProfileParam(key, value string) (Model, string, tea.Cmd) {
	key = strings.TrimSpace(key)
	item := m.currentItem()
	if item == nil || key == "" {
		return m, "", nil
	}
	if item.cfg.Params == nil {
		item.cfg.Params = map[string]string{}
	}
	item.cfg.Params[key] = value
	item.dirty = true
	if item.isActive {
		item.syncState = common.SyncStatusPending
	}
	m.dirty = true
	return m, bundledProfileStatus(item), Save(m.configDir, item.name, item.cfg)
}

func (m Model) RemoveProfileParam(key string) (Model, string, tea.Cmd) {
	key = strings.TrimSpace(key)
	item := m.currentItem()
	if item == nil || key == "" {
		return m, "", nil
	}
	delete(item.cfg.Params, key)
	if len(item.cfg.Params) == 0 {
		item.cfg.Params = nil
	}
	item.dirty = true
	if item.isActive {
		item.syncState = common.SyncStatusPending
	}
	m.dirty = true
	return m, bundledProfileStatus(item), Save(m.configDir, item.name, item.cfg)
}

func bundledProfileStatus(item *profileItem) string {
	if item == nil || len(item.bundledFrom) == 0 {
		return ""
	}
	return "Warning: " + item.name + " is pack-provided from " + strings.Join(item.bundledFrom, ", ") + "; pack update may overwrite edits."
}
