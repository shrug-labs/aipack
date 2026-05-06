package packs

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/domain"
)

type Focus int

const (
	FocusList Focus = iota
	FocusContent
	FocusPreview
)

type ListSelection struct {
	Name        string
	Installed   bool
	InRegistry  bool
	Description string
	Owner       string
	Repo        string
	Ref         string
	RegPath     string
	Source      string
}

type InstalledPack struct {
	Entry app.PackShowEntry
}

type ContentSelection struct {
	Category domain.PackCategory
	ID       string
	FilePath string
	IsHeader bool
	PackName string
}

type VersionCache struct {
	Loaded   bool
	Loading  bool
	Error    bool
	Versions []app.PackVersion
	Err      string
}

func (m Model) LoadErr() string {
	return m.loadErr
}

func (m Model) Focus() Focus {
	return Focus(m.focus)
}

func (m Model) IsListFocused() bool {
	return m.focus == packPanelList
}

func (m Model) ActiveUpdate() bool {
	return m.activeUpdate != nil
}

func (m Model) ActiveInstall() bool {
	return m.activeInstall != nil
}

func (m Model) CancelActiveUpdate() (Model, bool) {
	if m.activeUpdate == nil {
		return m, false
	}
	m.activeUpdate.cancel()
	return m, true
}

func (m Model) CancelActiveInstall() (Model, bool) {
	if m.activeInstall == nil {
		return m, false
	}
	m.activeInstall.cancel()
	return m, true
}

func (m Model) SpinnerTick() tea.Cmd {
	return m.spinner.Tick
}

func (m Model) SpinnerView() string {
	return m.spinner.View()
}

func (m Model) BatchStatus() string {
	return packsBatchStatus(m)
}

func (m Model) InstallStatus() string {
	return packsInstallStatus(m)
}

func AnyUpdated(results []app.PackUpdateResult) bool {
	return anyPackUpdated(results)
}

func AnyCancelled(results []app.PackUpdateResult) bool {
	return anyCancelled(results)
}

func (m Model) RefreshVersionsForCursor() (Model, tea.Cmd) {
	return m, m.refreshVersionsForCursor()
}

func (m Model) ApplyCursorHint(name string) Model {
	for i, li := range m.listItems {
		if li.name == name {
			m.listCursor = i
			m.clampListOffset()
			m.buildContentForCurrent()
			break
		}
	}
	return m
}

func (m Model) ApplyContentHint(kind, name string) (Model, tea.Cmd, bool) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	name = strings.TrimSpace(name)
	if kind == "" || name == "" {
		return m, nil, false
	}
	category, ok := domain.ParseSingularLabel(kind)
	if !ok {
		return m, nil, false
	}
	for i, ci := range m.contentItems {
		if !ci.isHeader && ci.category == category && ci.id == name {
			m.contentCursor = i
			m.clampContentOffset()
			m.focus = packPanelContent
			return m, m.loadInlinePreview(), true
		}
	}
	return m, nil, false
}

func (m Model) HasListItem(name string) bool {
	for _, li := range m.listItems {
		if li.name == name {
			return true
		}
	}
	return false
}

func (m Model) InstalledCount() int {
	return len(m.items)
}

func (m Model) InstalledNames() []string {
	names := make([]string, len(m.items))
	for i, item := range m.items {
		names[i] = item.entry.Name
	}
	return names
}

func (m Model) HasInstalled(name string) bool {
	_, ok := m.installedMap[name]
	return ok
}

func (m Model) CurrentListItem() (ListSelection, bool) {
	li := m.currentListItem()
	if li == nil {
		return ListSelection{}, false
	}
	return ListSelection{
		Name:        li.name,
		Installed:   li.installed,
		InRegistry:  li.inRegistry,
		Description: li.description,
		Owner:       li.owner,
		Repo:        li.repo,
		Ref:         li.ref,
		RegPath:     li.regPath,
		Source:      li.source,
	}, true
}

func (m Model) CurrentInstalledPack() (InstalledPack, bool) {
	item := m.currentItem()
	if item == nil {
		return InstalledPack{}, false
	}
	return InstalledPack{Entry: item.entry}, true
}

func (m Model) VersionCache(name string) (VersionCache, bool) {
	cached, ok := m.versionsCacheEntry(name)
	if !ok {
		return VersionCache{}, false
	}
	return VersionCache{
		Loaded:   cached.state == asyncLoaded,
		Loading:  cached.state == asyncLoading,
		Error:    cached.state == asyncError,
		Versions: slices.Clone(cached.versions),
		Err:      cached.err,
	}, true
}

func (m Model) CurrentBrowsingFilePath() string {
	if m.focus != packPanelContent && m.focus != packPanelPreview {
		return ""
	}
	if sel, ok := m.CurrentContentSelection(); ok && !sel.IsHeader {
		return sel.FilePath
	}
	return ""
}

func (m Model) CurrentContentSelection() (ContentSelection, bool) {
	if m.contentCursor < 0 || m.contentCursor >= len(m.contentItems) {
		return ContentSelection{}, false
	}
	ci := m.contentItems[m.contentCursor]
	out := ContentSelection{
		Category: ci.category,
		ID:       ci.id,
		IsHeader: ci.isHeader,
	}
	if item := m.currentItem(); item != nil {
		out.PackName = item.entry.Name
		if !ci.isHeader {
			out.FilePath = m.contentFilePath(ci)
		}
	}
	return out, true
}

func (m Model) MoveTargetNames() []string {
	current := ""
	if item := m.currentItem(); item != nil {
		current = item.entry.Name
	}
	var targets []string
	for _, item := range m.items {
		if item.entry.Name != current {
			targets = append(targets, item.entry.Name)
		}
	}
	return targets
}

func (m Model) RenderString() string {
	return m.Render()
}

func (m Model) SetFocus(focus Focus) Model {
	m.focus = packPanel(focus)
	return m
}

func (m Model) SetContentCursor(cursor int) Model {
	if len(m.contentItems) == 0 {
		m.contentCursor = 0
		return m
	}
	m.contentCursor = min(max(cursor, 0), len(m.contentItems)-1)
	m.clampContentOffset()
	return m
}

func (m Model) WithInstalled(entries []app.PackShowEntry) Model {
	items := make([]packItemDetail, len(entries))
	for i, e := range entries {
		items[i] = packItemDetail{entry: e, sizeState: asyncPending}
	}
	m.items = items
	m.installedMap = map[string]int{}
	for i, item := range m.items {
		m.installedMap[item.entry.Name] = i
	}
	m.rebuildList()
	return m
}

func LoadedFromEntries(entries []app.PackShowEntry) LoadedMsg {
	return LoadedMsg{Items: entries}
}

func (m Model) PrimeInlinePreview(path string) Model {
	m.previewPath = path
	m.previewState = asyncLoading
	return m
}

func (m Model) PreviewLoaded() bool {
	return m.previewState == asyncLoaded
}

func (m Model) PreviewBody() string {
	return m.previewData.Body
}
