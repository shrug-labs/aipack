package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func newTestPacksModel(items []packItemDetail) packsModel {
	m := packsModel{
		items:         items,
		installedMap:  map[string]int{},
		versionsCache: map[string]packVersionsCacheEntry{},
		driftSet:      map[string]app.PackDrift{},
	}
	for i, item := range items {
		m.installedMap[item.entry.Name] = i
	}
	m.rebuildList()
	return m
}

func TestPacksModel_EnterOpensContentPanel(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:   "test-pack",
			Path:   "/tmp/pack",
			Rules:  []string{"rule-a", "rule-b"},
			Agents: []string{"agent-a"},
		}},
	})

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != packPanelContent {
		t.Fatal("expected focus=packPanelContent after enter on installed pack")
	}
	if cmd == nil {
		t.Fatal("expected inline preview load command after entering content")
	}
	// Should have header + 2 rules + header + 1 agent = 5 items.
	if len(m.contentItems) != 5 {
		t.Fatalf("expected 5 content items, got %d", len(m.contentItems))
	}
	// Cursor should skip the header.
	if m.contentCursor != 1 {
		t.Fatalf("expected contentCursor=1 (skip header), got %d", m.contentCursor)
	}
	if m.contentItems[1].id != "rule-a" {
		t.Fatalf("expected first non-header item 'rule-a', got %q", m.contentItems[1].id)
	}
}

func TestPacksModel_ListSortedInstalledThenAlphabetical(t *testing.T) {
	t.Parallel()
	m := packsModel{
		items: []packItemDetail{
			{entry: app.PackShowEntry{Name: "zeta"}},
			{entry: app.PackShowEntry{Name: "alpha"}},
		},
		installedMap: map[string]int{"zeta": 0, "alpha": 1},
		registry: []registryItem{
			{name: "beta", installed: false},
			{name: "alpha", installed: true},
			{name: "aardvark", installed: false},
		},
	}
	m.rebuildList()

	got := []string{
		m.listItems[0].name,
		m.listItems[1].name,
		m.listItems[2].name,
		m.listItems[3].name,
	}
	want := []string{"alpha", "zeta", "aardvark", "beta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected sort order %v, want %v", got, want)
		}
	}
}

func TestPacksModel_ContentNavigation(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a", "rule-b"},
		}},
	})

	// Enter content panel.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.contentCursor != 1 {
		t.Fatalf("expected contentCursor=1, got %d", m.contentCursor)
	}

	// Move down.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.contentCursor != 2 {
		t.Fatalf("expected contentCursor=2 after j, got %d", m.contentCursor)
	}

	// Move up.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if m.contentCursor != 1 {
		t.Fatalf("expected contentCursor=1 after k, got %d", m.contentCursor)
	}
}

func TestPacksModel_ContentEscReturns(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != packPanelContent {
		t.Fatal("expected focus=packPanelContent")
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != packPanelList {
		t.Fatal("expected focus=packPanelList after esc")
	}
}

func TestPacksModel_ContentRightMovesToPreviewAndScrolls(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})
	m.focus = packPanelContent
	m.previewState = asyncLoaded
	m.previewData = previewLoadedMsg{
		title:    "rule-a",
		category: domain.CategoryRules,
		filePath: "/tmp/pack/rules/rule-a.md",
		body:     "line-1\nline-2\nline-3\nline-4\nline-5\nline-6\nline-7",
	}
	m.height = 8
	m.width = 100

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.focus != packPanelPreview {
		t.Fatalf("expected focus=packPanelPreview after right, got %d", m.focus)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.previewOffset == 0 {
		t.Fatal("expected previewOffset to advance after scrolling in preview focus")
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != packPanelContent {
		t.Fatalf("expected esc from preview to return to content, got %d", m.focus)
	}
}

func TestPacksModel_ContentEnterEmitsPreview(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})

	// Enter content panel.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	// Enter on the content item should emit previewRequestMsg.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd for preview request")
	}
	msg := cmd()
	req, ok := msg.(previewRequestMsg)
	if !ok {
		t.Fatalf("expected previewRequestMsg, got %T", msg)
	}
	if req.title != "rule-a" {
		t.Fatalf("expected title 'rule-a', got %q", req.title)
	}
	if req.packName != "test-pack" {
		t.Fatalf("expected packName 'test-pack', got %q", req.packName)
	}
	if req.category != domain.CategoryRules {
		t.Fatalf("expected category %q, got %q", domain.CategoryRules, req.category)
	}
}

func TestPacksModel_BuildContentItems(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:       "test-pack",
			Rules:      []string{"r1"},
			Agents:     []string{"a1", "a2"},
			Skills:     []string{"s1"},
			MCPServers: []string{},
		}},
	})

	items := m.buildContentItems(0)
	// Rules header + 1 rule + Agents header + 2 agents + Skills header + 1 skill = 7.
	if len(items) != 7 {
		t.Fatalf("expected 7 items, got %d", len(items))
	}
	if !items[0].isHeader || items[0].id != "Rules" {
		t.Fatalf("expected first item to be Rules header, got %+v", items[0])
	}
	if items[1].id != "r1" || items[1].category != domain.CategoryRules {
		t.Fatalf("expected rules item 'r1', got %+v", items[1])
	}
}

func TestPacksModel_ListSelectionDoesNotLoadInlinePreview(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
		{entry: app.PackShowEntry{
			Name:  "other-pack",
			Path:  "/tmp/other-pack",
			Rules: []string{"rule-b"},
		}},
	})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if cmd != nil {
		t.Fatal("expected no inline preview load command while focus remains in list")
	}
}

func TestPacksModel_ContentFilePath(t *testing.T) {
	t.Parallel()
	root := filepath.Join("/tmp", "pack")
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "test-pack", Path: root}},
	})

	tests := []struct {
		ci       contentItem
		expected string
	}{
		{contentItem{category: domain.CategoryRules, id: "rule-a"}, filepath.Join(root, "rules", "rule-a.md")},
		{contentItem{category: domain.CategoryAgents, id: "agent-a"}, filepath.Join(root, "agents", "agent-a.md")},
		{contentItem{category: domain.CategoryWorkflows, id: "wf-a"}, filepath.Join(root, "workflows", "wf-a.md")},
		{contentItem{category: domain.CategorySkills, id: "skill-a"}, filepath.Join(root, "skills", "skill-a", "SKILL.md")},
	}
	for _, tt := range tests {
		got := m.contentFilePath(tt.ci)
		if got != tt.expected {
			t.Errorf("contentFilePath(%+v) = %q, want %q", tt.ci, got, tt.expected)
		}
	}
}

func TestPacksModel_EmptyPackNoContentFocus(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "empty-pack", Path: "/tmp/empty"}},
	})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != packPanelList {
		t.Fatal("expected focus=packPanelList for pack with no content")
	}
}

func TestPacksModel_ContentPanelShowsCategoryCounts(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:      "test-pack",
			Path:      "/tmp/pack",
			Rules:     []string{"rule-a", "rule-b"},
			Workflows: []string{"wf-a"},
		}},
	})
	m.width = 120
	m.height = 24

	view := m.viewContentPanel(36, 20)
	if !strings.Contains(view, "Rules (2)") {
		t.Fatalf("expected Rules count in content panel, got:\n%s", view)
	}
	if !strings.Contains(view, "Workflows (1)") {
		t.Fatalf("expected Workflows count in content panel, got:\n%s", view)
	}
}

func TestPacksModel_PackInfoIsCompact(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:        "test-pack",
			Path:        "/tmp/pack",
			Origin:      "/very/long/source/path/to/test-pack",
			Version:     "2026.03.10",
			Method:      "link",
			InstalledAt: "2026-03-12T21:18:52Z",
			Rules:       []string{"rule-a"},
		}},
	})
	m.width = 120
	m.height = 24
	m.registry = []registryItem{{
		name:        "test-pack",
		description: "A longish description for compact rendering.",
		repo:        "ssh://git/example/repo.git",
		ref:         "main",
		owner:       "myteam",
	}}
	m.registryState = asyncLoaded
	m.rebuildList()
	m.items[0].fileSizes = map[string]int64{"total": 4096}

	view := m.viewPackInfoPanel(40, 18)
	if strings.Contains(view, "Registry:") {
		t.Fatalf("expected compact pack info without verbose Registry section, got:\n%s", view)
	}
	if strings.Contains(view, "Installed:") {
		t.Fatalf("expected compact pack info without Installed field label, got:\n%s", view)
	}
	if !strings.Contains(view, "v2026.03.10") {
		t.Fatalf("expected compact version badge, got:\n%s", view)
	}
	if !strings.Contains(view, "A longish description") {
		t.Fatalf("expected compact description, got:\n%s", view)
	}
	if strings.Contains(view, "1 rules") {
		t.Fatalf("expected content counts to move out of pack info, got:\n%s", view)
	}
}

func TestWrapWords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{
			name:  "fits on one line",
			input: "short text",
			width: 20,
			want:  "short text",
		},
		{
			name:  "wraps on word boundaries",
			input: "the quick brown fox",
			width: 10,
			want:  "the quick\nbrown fox",
		},
		{
			name:  "hard-breaks unbreakable single token longer than width",
			input: "supercalifragilistic",
			width: 8,
			want:  "supercal\nifragili\nstic",
		},
		{
			name:  "mixes hard-break and soft-break",
			input: "ok superlongword next",
			width: 6,
			want:  "ok\nsuperl\nongwor\nd next",
		},
		{
			name:  "empty string",
			input: "",
			width: 10,
			want:  "",
		},
		{
			name:  "zero width returns input",
			input: "anything",
			width: 0,
			want:  "anything",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := wrapWords(tt.input, tt.width); got != tt.want {
				t.Errorf("wrapWords(%q, %d):\ngot:  %q\nwant: %q", tt.input, tt.width, got, tt.want)
			}
		})
	}
}

func TestPacksModel_DetailsPanelWrapsValuesUnderColumn(t *testing.T) {
	t.Parallel()
	// Long About description should wrap at the value column boundary,
	// not bleed into where the next label would appear. Continuation lines
	// must align at column labelW (12 chars), not column 0.
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:   "wide-pack",
			Method: "clone",
		}},
	})
	m.registry = []registryItem{{
		name:        "wide-pack",
		description: "A reasonably long description that will need to wrap across multiple lines inside the details panel value column.",
	}}
	m.registryState = asyncLoaded
	m.rebuildList()
	m.width = 140
	m.height = 30
	m.listCursor = 0

	view := m.viewPackInfoPanel(48, 24)
	lines := strings.Split(view, "\n")

	// Find a non-first line that begins inside the wrapped About value.
	// Every wrapped continuation should be padded to column labelW, never
	// start at column 0 (which would visually collide with the label).
	foundContinuation := false
	for i, line := range lines {
		stripped := strings.TrimRight(line, " ")
		if i == 0 || stripped == "" {
			continue
		}
		// Look for a line that starts with whitespace and contains text —
		// that's a continuation row of a wrapped value.
		if strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "" {
			// All wrap-continuation rows must be indented past the label
			// gutter. The "About" label is 5 chars, padded to labelW=12,
			// so continuation rows must start with at least 12 spaces of
			// indent. Verify by checking that the first non-space char is
			// at column ≥ labelW.
			firstNonSpace := len(line) - len(strings.TrimLeft(line, " "))
			if strings.Contains(line, "wrap") || strings.Contains(line, "lines") || strings.Contains(line, "panel") {
				if firstNonSpace < 12 {
					t.Errorf("continuation line %q has first non-space at col %d, expected >= 12", line, firstNonSpace)
				}
				foundContinuation = true
			}
		}
	}
	if !foundContinuation {
		t.Fatalf("expected to find a wrapped About continuation line in:\n%s", view)
	}
}

func TestPacksModel_DetailsPanelShowsPin(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:       "pinned-pack",
			Path:       "/tmp/pinned-pack",
			Method:     "clone",
			Version:    "1.2.3",
			Pin:        "v1.2.3",
			Ref:        "v1.2.3",
			CommitHash: "abc1234def5678",
			Origin:     "https://example.com/pinned-pack",
		}},
	})
	m.width = 120
	m.height = 30
	m.listCursor = 0

	view := m.viewPackInfoPanel(40, 24)

	// Pin label must appear so users can see the pack is locked.
	if !strings.Contains(view, "v1.2.3 (pinned)") {
		t.Fatalf("expected pin label in details, got:\n%s", view)
	}
	// Ref row is suppressed when Pin is present (Pin already encodes the
	// same information plus the "(pinned)" status).
	if strings.Contains(view, "Ref     ") {
		t.Fatalf("expected Ref row to be hidden when Pin is present, got:\n%s", view)
	}
	// Short commit hash, not full.
	if !strings.Contains(view, "abc1234") {
		t.Fatalf("expected short commit hash in details, got:\n%s", view)
	}
	if strings.Contains(view, "abc1234def5678") {
		t.Fatalf("expected commit hash to be shortened, got:\n%s", view)
	}
}

func TestPacksModel_DetailsPanelShowsRefForUnpinned(t *testing.T) {
	t.Parallel()
	// Unpinned clone tracking a branch — Ref should appear since Pin is empty.
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:       "tracking-pack",
			Path:       "/tmp/tracking-pack",
			Method:     "clone",
			Ref:        "main",
			CommitHash: "deadbeef0000",
			Origin:     "https://example.com/tracking-pack",
		}},
	})
	m.width = 120
	m.height = 30
	m.listCursor = 0

	view := m.viewPackInfoPanel(40, 24)
	if strings.Contains(view, "(pinned)") {
		t.Fatalf("expected no pin label for unpinned pack, got:\n%s", view)
	}
	if !strings.Contains(view, "main") {
		t.Fatalf("expected branch ref in details, got:\n%s", view)
	}
	if !strings.Contains(view, "deadbee") {
		t.Fatalf("expected commit hash (short form) in details, got:\n%s", view)
	}
}

func TestPacksModel_DetailsPanelShowsLatestForInstalledClone(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:    "behind-pack",
			Path:    "/tmp/behind-pack",
			Method:  "clone",
			Version: "1.2.3",
			Pin:     "v1.2.3",
		}},
	})
	m.width = 120
	m.height = 30
	// Seed the version cache as if loadPackVersions had completed:
	// remote has v2.0.0, v1.5.0, v1.2.3 — installed pin is v1.2.3.
	m.versionsCache["behind-pack"] = packVersionsCacheEntry{
		state: asyncLoaded,
		versions: []app.PackVersion{
			{Version: "v2.0.0"},
			{Version: "v1.5.0"},
			{Version: "v1.2.3"},
		},
	}

	view := m.viewPackInfoPanel(50, 24)
	if !strings.Contains(view, "Latest") {
		t.Fatalf("expected Latest row when newer version available, got:\n%s", view)
	}
	if !strings.Contains(view, "v2.0.0") {
		t.Fatalf("expected latest version label, got:\n%s", view)
	}
}

func TestPacksModel_DetailsPanelHidesLatestWhenOnNewest(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:    "current-pack",
			Path:    "/tmp/current-pack",
			Method:  "clone",
			Version: "2.0.0",
			Pin:     "v2.0.0",
		}},
	})
	m.width = 120
	m.height = 30
	m.versionsCache["current-pack"] = packVersionsCacheEntry{
		state: asyncLoaded,
		versions: []app.PackVersion{
			{Version: "v2.0.0"},
			{Version: "v1.5.0"},
		},
	}

	view := m.viewPackInfoPanel(50, 24)
	// No drift signal — Latest row would just be noise.
	if strings.Contains(view, "Latest") {
		t.Fatalf("expected no Latest row when on newest version, got:\n%s", view)
	}
}

func TestPacksModel_RegistryPanelShowsVersions(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m.registry = []registryItem{{
		name:        "discoverable",
		description: "test pack",
		owner:       "team",
		repo:        "https://example.com/discoverable",
		ref:         "main",
	}}
	m.registryState = asyncLoaded
	m.rebuildList()
	m.versionsCache["discoverable"] = packVersionsCacheEntry{
		state: asyncLoaded,
		versions: []app.PackVersion{
			{Version: "v3.0.0"},
			{Version: "v2.5.1"},
			{Version: "v2.0.0"},
			{Version: "v1.0.0"},
		},
	}
	m.width = 120
	m.height = 30

	view := m.viewPackInfoPanel(60, 30)
	if !strings.Contains(view, "Versions") {
		t.Fatalf("expected Versions row in registry block, got:\n%s", view)
	}
	if !strings.Contains(view, "v3.0.0") {
		t.Fatalf("expected newest version in inline list, got:\n%s", view)
	}
	// Caps at three plus a "+N more" suffix; v1.0.0 is the 4th and should
	// not appear in the inline list itself.
	if !strings.Contains(view, "+1 more") {
		t.Fatalf("expected '+1 more' overflow indicator, got:\n%s", view)
	}
}

func TestPacksModel_RegistryPanelShowsLoadingState(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m.registry = []registryItem{{
		name: "loading-pack",
		repo: "https://example.com/loading-pack",
	}}
	m.registryState = asyncLoaded
	m.rebuildList()
	m.versionsCache["loading-pack"] = packVersionsCacheEntry{state: asyncLoading}
	m.width = 120
	m.height = 30

	view := m.viewPackInfoPanel(60, 30)
	if !strings.Contains(view, "loading") {
		t.Fatalf("expected loading indicator while versions in flight, got:\n%s", view)
	}
}

func TestPacksModel_VersionsLoadedMsgPopulatesCache(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m, _ = m.Update(packVersionsLoadedMsg{
		packName: "fresh-pack",
		versions: []app.PackVersion{
			{Version: "v1.0.0"},
		},
	})
	cached, ok := m.versionsCacheEntry("fresh-pack")
	if !ok {
		t.Fatal("expected cache entry after packVersionsLoadedMsg")
	}
	if cached.state != asyncLoaded {
		t.Errorf("expected state asyncLoaded, got %v", cached.state)
	}
	if len(cached.versions) != 1 || cached.versions[0].Version != "v1.0.0" {
		t.Errorf("unexpected cached versions: %+v", cached.versions)
	}
}

func TestPacksModel_VersionsLoadedMsgErrorMarksFailed(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m, _ = m.Update(packVersionsLoadedMsg{
		packName: "broken-pack",
		err:      fmt.Errorf("network down"),
	})
	cached, ok := m.versionsCacheEntry("broken-pack")
	if !ok {
		t.Fatal("expected cache entry to record the failure")
	}
	if cached.state != asyncError {
		t.Errorf("expected state asyncError, got %v", cached.state)
	}
}

func TestPacksModel_MaybeLoadVersionsSkipsLinkPacks(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "linked", Method: "link"}},
	})
	cmd := m.maybeLoadVersionsForCursor()
	if cmd != nil {
		t.Fatal("link-method packs should not trigger version loads")
	}
	if _, ok := m.versionsCacheEntry("linked"); ok {
		t.Fatal("link-method packs should not be added to the cache")
	}
}

func TestPacksModel_MaybeLoadVersionsClonePacksSchedule(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "remote", Method: "clone"}},
	})
	cmd := m.maybeLoadVersionsForCursor()
	if cmd == nil {
		t.Fatal("clone-method packs should trigger a version load")
	}
	cached, ok := m.versionsCacheEntry("remote")
	if !ok {
		t.Fatal("expected cache entry seeded with loading state")
	}
	if cached.state != asyncLoading {
		t.Errorf("expected loading state, got %v", cached.state)
	}
	// A second call must NOT spawn a duplicate request — the cache entry
	// already records the in-flight load. Without this guard, every cursor
	// blip would re-fire the network call.
	if again := m.maybeLoadVersionsForCursor(); again != nil {
		t.Fatal("expected no duplicate load when one is already in flight")
	}
}

func TestPacksModel_DriftLoadedPopulatesSet(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	m, _ = m.Update(packDriftLoadedMsg{
		drifted: []app.PackDrift{
			{Name: "alpha", Method: "clone", Reason: "remote ref has moved since install"},
			{Name: "bravo", Method: "clone", Reason: "remote ref has moved since install"},
		},
	})
	if _, ok := m.driftSet["alpha"]; !ok {
		t.Error("expected alpha in drift set")
	}
	if _, ok := m.driftSet["bravo"]; !ok {
		t.Error("expected bravo in drift set")
	}
	if _, ok := m.driftSet["charlie"]; ok {
		t.Error("charlie should not be in drift set")
	}
}

func TestPacksModel_DriftLoadedReplacesPriorState(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel(nil)
	// Seed an existing entry that should disappear after a fresh scan.
	m.driftSet["stale"] = app.PackDrift{Name: "stale"}

	m, _ = m.Update(packDriftLoadedMsg{
		drifted: []app.PackDrift{
			{Name: "fresh"},
		},
	})
	if _, ok := m.driftSet["stale"]; ok {
		t.Error("stale entry should be cleared by fresh scan")
	}
	if _, ok := m.driftSet["fresh"]; !ok {
		t.Error("fresh entry should be present")
	}
}

func TestPacksModel_ListPanelShowsDriftIndicator(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "drifted", Method: "clone"}},
		{entry: app.PackShowEntry{Name: "current", Method: "clone"}},
	})
	m.driftSet["drifted"] = app.PackDrift{Name: "drifted", Reason: "remote ref has moved"}
	m.width = 120
	m.height = 30

	view := m.viewListPanel(40, 16)
	// The drifted row gets the indicator; the current row doesn't.
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, "drifted") && !strings.Contains(line, "↑") {
			t.Fatalf("drifted row should have ↑ indicator, got: %q", line)
		}
		if strings.Contains(line, "current") && strings.Contains(line, "↑") {
			t.Fatalf("current row should not have ↑ indicator, got: %q", line)
		}
	}
}

func TestPacksModel_ListPanelShowsPinSuffix(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:   "pinned-pack",
			Method: "clone",
			Pin:    "v2.0.0",
		}},
		{entry: app.PackShowEntry{
			Name:   "loose-pack",
			Method: "clone",
		}},
	})
	m.width = 120
	m.height = 30

	view := m.viewListPanel(40, 16)
	// Pinned row gets the suffix.
	if !strings.Contains(view, "v2.0.0 (pinned)") {
		t.Fatalf("expected pin suffix on pinned row, got:\n%s", view)
	}
	// Unpinned row appears without "(pinned)" anywhere on its line.
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, "loose-pack") && strings.Contains(line, "pinned") {
			t.Fatalf("loose-pack row should not have pin suffix, got line: %q", line)
		}
	}
}

func TestPacksModel_ListPanelOmitsStatusBadges(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "alpha"}},
	})
	m.registry = []registryItem{{
		name:  "beta",
		repo:  "ssh://git/example/repo.git",
		owner: "myteam",
	}}
	m.registryState = asyncLoaded
	m.rebuildList()
	m.listCursor = 0

	view := m.viewListPanel(24, 10)
	if !strings.Contains(view, selectedStyle.Render("alpha")) {
		t.Fatalf("expected selected pack name to stay pink, got:\n%s", view)
	}
	if strings.Contains(view, selectedStyle.Render("alpha")+"  installed") {
		t.Fatalf("expected installed badge removed from selected row, got:\n%s", view)
	}
	if strings.Contains(view, "beta  registry") {
		t.Fatalf("expected registry badge removed from pack rows, got:\n%s", view)
	}
	if strings.Contains(view, "ssh://git/example/repo.git") {
		t.Fatalf("expected list panel to avoid duplicated repo metadata, got:\n%s", view)
	}
}

func TestPacksModel_ContentSelectionStaysHighlightedInPreviewFocus(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a", "rule-b"},
		}},
	})
	m.focus = packPanelPreview
	m.contentCursor = 2
	m.items[0].fileSizes = map[string]int64{
		"rules/rule-a": 1024,
		"rules/rule-b": 2048,
	}

	view := m.viewContentPanel(40, 12)
	if !strings.Contains(view, selectedStyle.Render("rule-b")) {
		t.Fatalf("expected selected content item to stay highlighted in preview focus, got:\n%s", view)
	}
}

func TestPacksModel_ContentPanelDoesNotInsertGapAfterCategoryHeader(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a", "rule-b"},
		}},
	})
	m.width = 120
	m.height = 20

	view := m.viewContentPanel(40, 16)
	if strings.Contains(view, "Rules (2)\n\n") {
		t.Fatalf("expected first content item immediately after category header, got:\n%s", view)
	}
}

func TestPacksModel_HelpTextChangesInContentFocus(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks

	help := m.helpText()
	if !strings.Contains(help, "enter:content") {
		t.Fatalf("expected packs help to contain 'enter:content', got %q", help)
	}
	if !strings.Contains(help, ".:actions") {
		t.Fatalf("expected packs help to contain '.:actions', got %q", help)
	}

	m.packs.focus = packPanelContent
	help = m.helpText()
	if !strings.Contains(help, "enter:preview") {
		t.Fatalf("expected content focus help to contain 'enter:preview', got %q", help)
	}
	if !strings.Contains(help, "esc:back") {
		t.Fatalf("expected content focus help to contain 'esc:back', got %q", help)
	}

	m.packs.focus = packPanelPreview
	help = m.helpText()
	if !strings.Contains(help, "j/k:scroll") {
		t.Fatalf("expected preview focus help to contain 'j/k:scroll', got %q", help)
	}
	if !strings.Contains(help, "e:edit") {
		t.Fatalf("expected preview focus help to contain 'e:edit', got %q", help)
	}
}

func TestPacksTab_InstallDialogResult(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks

	result, cmd := m.Update(dialogResultMsg{id: dialogPackInstall, confirmed: true, value: "/tmp/my-pack"})
	rm := result.(rootModel)
	if rm.dialog != nil {
		t.Fatal("expected dialog to be cleared after result")
	}
	if cmd == nil {
		t.Fatal("expected installPack command")
	}
	if !strings.Contains(rm.statusText, "installing") {
		t.Fatalf("expected status text to mention 'installing', got %q", rm.statusText)
	}
}

func TestPacksTab_RemoveDialogResult(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	m.packs = newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "test-pack"}},
	})

	result, cmd := m.Update(dialogResultMsg{id: dialogPackRemove, confirmed: true})
	rm := result.(rootModel)
	if rm.dialog != nil {
		t.Fatal("expected dialog to be cleared after result")
	}
	if cmd == nil {
		t.Fatal("expected removePack command")
	}
	if !strings.Contains(rm.statusText, "removing") {
		t.Fatalf("expected status text to mention 'removing', got %q", rm.statusText)
	}
}

func TestPackInstalledMsg_ReloadsPacks(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	result, cmd := m.Update(packInstalledMsg{name: "new-pack"})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "installed new-pack") {
		t.Fatalf("expected status text 'installed new-pack', got %q", rm.statusText)
	}
	if cmd == nil {
		t.Fatal("expected loadPacks reload command")
	}
}

func TestPackInstalledMsg_Error(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	result, _ := m.Update(packInstalledMsg{name: "bad-pack", err: fmt.Errorf("not found")})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "install error") {
		t.Fatalf("expected status text to mention 'install error', got %q", rm.statusText)
	}
}

func TestPackRemovedMsg_ReloadsPacksAndProfiles(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	result, cmd := m.Update(packRemovedMsg{name: "old-pack"})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "removed old-pack") {
		t.Fatalf("expected status text 'removed old-pack', got %q", rm.statusText)
	}
	if cmd == nil {
		t.Fatal("expected reload command (packs + profiles)")
	}
}

func TestPackUpdatedMsg_ShowsSummary(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	result, cmd := m.Update(packUpdatedMsg{
		name: "test-pack",
		results: []app.PackUpdateResult{
			{Name: "test-pack", Status: "updated"},
		},
	})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "test-pack: updated") {
		t.Fatalf("expected status text to contain update summary, got %q", rm.statusText)
	}
	if cmd == nil {
		t.Fatal("expected loadPacks reload command")
	}
}

// TestPackActionMenu_OffersPinVersionForClonePackWithVersions verifies the
// Pin to version... entry only appears when (a) the selected pack is clone
// method (link/copy/local can't be pinned) and (b) the versions cache has
// at least one tag — without versions, the picker would be empty.
func TestPackActionMenu_OffersPinVersionForClonePackWithVersions(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	m.packs = newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:   "pinnable",
			Method: "clone",
		}},
	})
	m.packs.versionsCache["pinnable"] = packVersionsCacheEntry{
		state: asyncLoaded,
		versions: []app.PackVersion{
			{Version: "v2.0.0"},
			{Version: "v1.5.0"},
		},
	}

	result, _ := m.openPackTabActions()
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected action menu dialog")
	}
	if !containsItem(rm.dialog.listItems, actPinVersion) {
		t.Errorf("expected %q in action menu, got %v", actPinVersion, rm.dialog.listItems)
	}
}

func TestPackActionMenu_HidesPinVersionWithoutCachedVersions(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	m.packs = newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "no-versions", Method: "clone"}},
	})
	// versionsCache empty — no Pin entry should be offered.

	result, _ := m.openPackTabActions()
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected action menu dialog")
	}
	if containsItem(rm.dialog.listItems, actPinVersion) {
		t.Errorf("Pin entry should be hidden without cached versions, got %v", rm.dialog.listItems)
	}
}

func TestPackActionMenu_HidesPinVersionForLinkPack(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	m.packs = newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "linked", Method: "link"}},
	})
	// Even with versions in cache, link packs can't be pinned.
	m.packs.versionsCache["linked"] = packVersionsCacheEntry{
		state:    asyncLoaded,
		versions: []app.PackVersion{{Version: "v1.0.0"}},
	}

	result, _ := m.openPackTabActions()
	rm := result.(rootModel)
	if containsItem(rm.dialog.listItems, actPinVersion) {
		t.Errorf("link packs should not get Pin entry, got %v", rm.dialog.listItems)
	}
	if containsItem(rm.dialog.listItems, actUnpin) {
		t.Errorf("link packs should not get Unpin entry, got %v", rm.dialog.listItems)
	}
}

func TestPackActionMenu_OffersUnpinOnlyForPinnedPack(t *testing.T) {
	t.Parallel()
	// Unpinned clone — Unpin entry must be hidden.
	mUnpinned := newRootModel(context.Background(), RunConfig{})
	mUnpinned.activeTab = tabPacks
	mUnpinned.packs = newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "loose", Method: "clone"}},
	})
	result, _ := mUnpinned.openPackTabActions()
	rm := result.(rootModel)
	if containsItem(rm.dialog.listItems, actUnpin) {
		t.Errorf("Unpin should be hidden for unpinned pack, got %v", rm.dialog.listItems)
	}

	// Pinned clone — Unpin entry must appear.
	mPinned := newRootModel(context.Background(), RunConfig{})
	mPinned.activeTab = tabPacks
	mPinned.packs = newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "tight", Method: "clone", Pin: "v1.2.3"}},
	})
	result, _ = mPinned.openPackTabActions()
	rm = result.(rootModel)
	if !containsItem(rm.dialog.listItems, actUnpin) {
		t.Errorf("Unpin should be offered for pinned pack, got %v", rm.dialog.listItems)
	}
}

// containsItem reports whether s appears in items. Used by action-menu tests
// where the menu order isn't load-bearing — only presence/absence matters.
func containsItem(items []string, s string) bool {
	return slices.Contains(items, s)
}

// TestPinVersionDialog_PicksOpensListSelect verifies that selecting Pin to
// version... in the action menu transitions through to a populated list-
// select dialog of available versions, with no "latest" sentinel (that's
// the Unpin action's job, not the picker's).
func TestPinVersionDialog_PicksOpensListSelect(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	m.packs = newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "my-pack", Method: "clone"}},
	})
	m.packs.versionsCache["my-pack"] = packVersionsCacheEntry{
		state: asyncLoaded,
		versions: []app.PackVersion{
			{Version: "v2.0.0"},
			{Version: "v1.5.0"},
		},
	}

	result, _ := m.Update(dialogResultMsg{
		id:        dialogActionPackTab,
		confirmed: true,
		value:     actPinVersion,
	})
	rm := result.(rootModel)
	if rm.dialog == nil || rm.dialog.id != dialogPinVersion {
		t.Fatalf("expected dialogPinVersion, got %v", rm.dialog)
	}
	// No latest sentinel — the picker is for moving the pin only.
	for _, item := range rm.dialog.listItems {
		if item == installVersionLatestSentinel {
			t.Errorf("Pin picker should not include the latest sentinel")
		}
	}
	if len(rm.dialog.listItems) != 2 {
		t.Errorf("expected 2 versions in pin picker, got %v", rm.dialog.listItems)
	}
}

// TestPinVersionDialog_ConfirmTriggersUpdate verifies that confirming the
// pin picker dispatches a pack update with the picked version. The status
// text should reflect the in-flight pin operation so the user knows the
// click landed.
func TestPinVersionDialog_ConfirmTriggersUpdate(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	m.packs = newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "my-pack", Method: "clone"}},
	})

	result, cmd := m.Update(dialogResultMsg{
		id:        dialogPinVersion,
		confirmed: true,
		value:     "v1.5.0",
	})
	rm := result.(rootModel)
	if cmd == nil {
		t.Fatal("expected an update command from pin confirm")
	}
	if !strings.Contains(rm.statusText, "pinning my-pack") {
		t.Errorf("expected pinning status text, got %q", rm.statusText)
	}
	if !strings.Contains(rm.statusText, "v1.5.0") {
		t.Errorf("expected target version in status text, got %q", rm.statusText)
	}
}

func TestPinVersionDialog_CancelIsNoop(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	m.packs = newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "my-pack", Method: "clone"}},
	})
	priorStatus := m.statusText

	result, cmd := m.Update(dialogResultMsg{
		id:        dialogPinVersion,
		confirmed: false,
	})
	rm := result.(rootModel)
	if cmd != nil {
		t.Fatal("expected no command when pin picker is cancelled")
	}
	if rm.statusText != priorStatus {
		t.Errorf("expected status unchanged on cancel, got %q", rm.statusText)
	}
}

// TestInstallVersionPicker_OpensWhenVersionsCached verifies that confirming
// the install text-input dialog with a registry name (and cached versions)
// transitions to the version picker dialog instead of jumping straight to
// the bundled-content checklist.
func TestInstallVersionPicker_OpensWhenVersionsCached(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	if m.packs.versionsCache == nil {
		m.packs.versionsCache = map[string]packVersionsCacheEntry{}
	}
	m.packs.versionsCache["my-team-pack"] = packVersionsCacheEntry{
		state: asyncLoaded,
		versions: []app.PackVersion{
			{Version: "v2.0.0"},
			{Version: "v1.5.0"},
		},
	}

	result, _ := m.Update(dialogResultMsg{
		id:        dialogPackInstall,
		confirmed: true,
		value:     "my-team-pack",
	})
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected a dialog to be opened after install confirmation")
	}
	if rm.dialog.id != dialogInstallVersion {
		t.Fatalf("expected dialogInstallVersion, got %q", rm.dialog.id)
	}
	// First item is the latest-stable sentinel; the cached versions follow.
	if len(rm.dialog.listItems) < 3 {
		t.Fatalf("expected sentinel + 2 versions in picker, got %v", rm.dialog.listItems)
	}
	if rm.dialog.listItems[0] != installVersionLatestSentinel {
		t.Fatalf("first picker item should be the latest sentinel, got %q", rm.dialog.listItems[0])
	}
	if rm.packs.versionsCache == nil || rm.pendingInstallInput != "my-team-pack" {
		t.Fatalf("expected pendingInstallInput to be set, got %q", rm.pendingInstallInput)
	}
}

// TestInstallVersionPicker_SkippedWhenNoCache verifies that with no cached
// version data, the install flow falls through to the bundled-content
// checklist directly — preserving the existing default-ref install path.
// Without this fallback, every "install immediately without browsing" use
// case would block on a missing dialog.
func TestInstallVersionPicker_SkippedWhenNoCache(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	// Cache is empty — no versions for "uncharted".

	result, _ := m.Update(dialogResultMsg{
		id:        dialogPackInstall,
		confirmed: true,
		value:     "uncharted",
	})
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected a dialog to be opened after install confirmation")
	}
	if rm.dialog.id != dialogInstallWith {
		t.Fatalf("expected dialogInstallWith (skip picker), got %q", rm.dialog.id)
	}
}

// TestInstallVersionPicker_LatestSentinelClearsVersion verifies that picking
// the "latest stable" sentinel leaves pendingInstallVersion empty so the
// downstream install runs at the registry's default ref. Picking a literal
// version stores it for the install request.
func TestInstallVersionPicker_LatestSentinelClearsVersion(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	m.pendingInstallInput = "my-pack"
	m.pendingInstallVersion = "stale-from-prior-attempt"

	result, _ := m.Update(dialogResultMsg{
		id:        dialogInstallVersion,
		confirmed: true,
		value:     installVersionLatestSentinel,
	})
	rm := result.(rootModel)
	if rm.pendingInstallVersion != "" {
		t.Fatalf("latest sentinel should clear pendingInstallVersion, got %q", rm.pendingInstallVersion)
	}
	if rm.dialog == nil || rm.dialog.id != dialogInstallWith {
		t.Fatalf("expected dialogInstallWith next, got %v", rm.dialog)
	}
}

func TestInstallVersionPicker_LiteralVersionStored(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	m.pendingInstallInput = "my-pack"

	result, _ := m.Update(dialogResultMsg{
		id:        dialogInstallVersion,
		confirmed: true,
		value:     "v1.5.0",
	})
	rm := result.(rootModel)
	if rm.pendingInstallVersion != "v1.5.0" {
		t.Fatalf("expected pendingInstallVersion=v1.5.0, got %q", rm.pendingInstallVersion)
	}
}

// TestInstallVersionPicker_CancelAborts verifies that escaping out of the
// version picker fully aborts the install rather than silently falling
// through to a default-ref install — the user explicitly opted into the
// version step and changed their mind.
func TestInstallVersionPicker_CancelAborts(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	m.pendingInstallInput = "my-pack"
	m.pendingInstallVersion = "v1.5.0"

	result, _ := m.Update(dialogResultMsg{
		id:        dialogInstallVersion,
		confirmed: false,
	})
	rm := result.(rootModel)
	if rm.pendingInstallInput != "" || rm.pendingInstallVersion != "" {
		t.Fatalf("expected pending state cleared on cancel, got input=%q version=%q",
			rm.pendingInstallInput, rm.pendingInstallVersion)
	}
}

// TestPackUpdatedMsg_PrefersMessageForPinnedPacks verifies that the TUI
// surfaces the pinned-pack hint from r.Message instead of collapsing every
// up-to-date result to "up-to-date". Without this, a user updating a pinned
// pack via the TUI would see no indication that the pack is pinned, no hint
// about a newer available version — they'd think nothing happened.
func TestPackUpdatedMsg_PrefersMessageForPinnedPacks(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	result, _ := m.Update(packUpdatedMsg{
		name: "pinned-pack",
		results: []app.PackUpdateResult{
			{
				Name:    "pinned-pack",
				Status:  "up-to-date",
				Message: "pinned at v1.2.3 (latest: v2.0.0)",
			},
		},
	})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "pinned at v1.2.3") {
		t.Fatalf("expected pin status to surface in status text, got %q", rm.statusText)
	}
	if !strings.Contains(rm.statusText, "latest: v2.0.0") {
		t.Fatalf("expected latest-version hint to surface in status text, got %q", rm.statusText)
	}
}

func TestPacksTab_KeysIgnoredInContentFocus(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	m.packs = newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "test-pack", Path: "/tmp/pack", Rules: []string{"r1"}}},
	})
	m.packs.focus = packPanelContent
	m.packs.contentItems = m.packs.buildContentItems(0)
	m.packs.contentCursor = 1

	// 'a' should not open dialog when in content focus.
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	rm := result.(rootModel)
	if rm.dialog != nil {
		t.Fatal("expected 'a' key to be ignored in content focus mode")
	}
}

func TestPacksModel_RegistryLoaded(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{Name: "example-pack"}},
	})

	m, _ = m.Update(registryLoadedMsg{
		items: []registryItem{
			{name: "example-pack", description: "Example ops pack"},
			{name: "devtools", description: "Dev tools pack"},
		},
	})

	if m.registryState != asyncLoaded {
		t.Fatal("expected registryState=asyncLoaded")
	}
	if len(m.listItems) != 2 {
		t.Fatalf("expected 2 list items, got %d", len(m.listItems))
	}
	// example-pack should be marked installed.
	if !m.listItems[0].installed {
		t.Fatal("expected example-pack to be marked installed")
	}
	if m.listItems[1].installed {
		t.Fatal("expected devtools to NOT be marked installed")
	}
}

func TestPacksTab_CreatePackDialogChain(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks

	// Step 1: Action menu opens with "Create pack" option.
	result, _ := m.Update(dialogResultMsg{id: dialogActionPackTab, confirmed: true, value: actCreatePack})
	rm := result.(rootModel)
	if rm.dialog == nil || rm.dialog.id != dialogCreatePack {
		t.Fatalf("expected dialogCreatePack, got %v", rm.dialog)
	}

	// Step 2: User enters a pack name.
	result, cmd := rm.Update(dialogResultMsg{id: dialogCreatePack, confirmed: true, value: "my-new-pack"})
	rm = result.(rootModel)
	if rm.dialog != nil {
		t.Fatal("expected dialog to be cleared after create-pack confirm")
	}
	if cmd == nil {
		t.Fatal("expected createPack command")
	}
	if !strings.Contains(rm.statusText, "creating my-new-pack") {
		t.Fatalf("expected status 'creating my-new-pack', got %q", rm.statusText)
	}
}

func TestPackCreatedMsg_ReloadsPacksAndProfiles(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	result, cmd := m.Update(packCreatedMsg{name: "fresh-pack"})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "created fresh-pack") {
		t.Fatalf("expected status 'created fresh-pack', got %q", rm.statusText)
	}
	if cmd == nil {
		t.Fatal("expected reload command")
	}
}

func TestPackCreatedMsg_Error(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	result, _ := m.Update(packCreatedMsg{name: "bad", err: fmt.Errorf("already exists")})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "create error") {
		t.Fatalf("expected status to mention 'create error', got %q", rm.statusText)
	}
}

func TestCreatePack_ScaffoldsAndRegisters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Set up minimal sync-config.
	scPath := config.SyncConfigPath(dir)
	if err := config.SaveSyncConfig(scPath, config.SyncConfig{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}

	cmd := createPack(dir, "test-created")
	msg := cmd()
	created, ok := msg.(packCreatedMsg)
	if !ok {
		t.Fatalf("expected packCreatedMsg, got %T", msg)
	}
	if created.err != nil {
		t.Fatalf("unexpected error: %v", created.err)
	}
	if created.name != "test-created" {
		t.Fatalf("expected name 'test-created', got %q", created.name)
	}

	// Verify pack was scaffolded.
	packDir := filepath.Join(dir, "packs", "test-created")
	if _, err := os.Stat(filepath.Join(packDir, "pack.json")); err != nil {
		t.Fatalf("expected pack.json to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(packDir, "rules")); err != nil {
		t.Fatalf("expected rules/ dir to exist: %v", err)
	}

	// Verify registered in lockfile.
	lf, err := config.LoadLockfile(config.LockfilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := lf.Packs["test-created"]
	if !ok {
		t.Fatal("expected pack to be registered in lockfile")
	}
	if meta.Method != config.MethodLocal {
		t.Fatalf("expected method %q, got %q", config.MethodLocal, meta.Method)
	}
}

func TestCreatePack_DuplicateErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	scPath := config.SyncConfigPath(dir)
	if err := config.SaveSyncConfig(scPath, config.SyncConfig{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}

	// First create succeeds.
	cmd := createPack(dir, "dup-pack")
	msg := cmd().(packCreatedMsg)
	if msg.err != nil {
		t.Fatalf("first create failed: %v", msg.err)
	}

	// Second create should fail (pack.json already exists).
	cmd = createPack(dir, "dup-pack")
	msg = cmd().(packCreatedMsg)
	if msg.err == nil {
		t.Fatal("expected error on duplicate pack create")
	}
}

func TestPacksView_SeparatorHighlightsWithFocus(t *testing.T) {
	t.Parallel()
	m := newTestPacksModel([]packItemDetail{
		{entry: app.PackShowEntry{
			Name:  "test-pack",
			Path:  "/tmp/pack",
			Rules: []string{"rule-a"},
		}},
	})
	m.width = 120
	m.height = 20

	// In list focus, both separators should be dim.
	m.focus = packPanelList
	viewList := m.View()

	// In content focus, the view should differ (left separator lights up).
	m.focus = packPanelContent
	viewContent := m.View()

	if viewList == viewContent {
		t.Fatal("expected view to change when focus moves from list to content")
	}
}

func TestPacksModel_UninstalledNoContentFocus(t *testing.T) {
	t.Parallel()
	m := packsModel{
		installedMap: map[string]int{},
	}
	m.registry = []registryItem{
		{name: "uninstalled-pack", description: "not installed"},
	}
	m.registryState = asyncLoaded
	m.rebuildList()

	// Enter on uninstalled pack should not switch to content panel.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != packPanelList {
		t.Fatal("expected focus=packPanelList for uninstalled pack")
	}
}
