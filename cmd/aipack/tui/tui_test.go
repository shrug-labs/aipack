package tui

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

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
)

func buildTUITestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeTUITestArchiveRegistry(t *testing.T, configDir, name, archiveURL string) {
	t.Helper()
	registry := fmt.Sprintf(`schema_version: 1
packs:
  %s:
    method: archive
    url: %q
    quiet: true
    content_paths:
      prompts: material/prompts
`, name, archiveURL)
	if err := os.MkdirAll(config.RegistriesCacheDir(configDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.SourceCachePath(configDir, "test-source"), []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}
	sc := config.SyncConfig{
		SchemaVersion: 1,
		RegistrySources: []config.RegistrySourceEntry{
			{Name: "test-source", URL: "https://example.com/test-registry.yaml"},
		},
	}
	if err := config.SaveSyncConfig(config.SyncConfigPath(configDir), sc); err != nil {
		t.Fatal(err)
	}
}

func serveTUITestArchive(t *testing.T) *httptest.Server {
	t.Helper()
	archive := buildTUITestZip(t, map[string]string{
		"repo-main/material/prompts/run.md": "Archive content_paths prompt body.\n",
	})
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
}

func TestInstallPack_RegistryArchiveUsesContentPathsAndQuiet(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	srv := serveTUITestArchive(t)
	defer srv.Close()
	writeTUITestArchiveRegistry(t, configDir, "archive-catalog", srv.URL+"/pack.zip")

	msg := installPack(context.Background(), configDir, "archive-catalog", "", nil)()
	ready, ok := msg.(packInstallReadyMsg)
	if !ok {
		t.Fatalf("expected packInstallReadyMsg, got %T: %+v", msg, msg)
	}

	var terminal packInstalledMsg
	for {
		next := readNextInstallEvent(ready.EventCh, ready.ResultCh)()
		if done, ok := next.(packInstalledMsg); ok {
			terminal = done
			break
		}
	}
	if terminal.Err != nil {
		t.Fatalf("installPack: %v", terminal.Err)
	}

	if _, err := os.Stat(filepath.Join(configDir, "packs", "archive-catalog", "prompts", "run.md")); err != nil {
		t.Fatalf("expected prompt extracted through content_paths: %v", err)
	}
	lf, err := config.EnsureLockfileMigrated(configDir)
	if err != nil {
		t.Fatal(err)
	}
	meta := lf.Packs["archive-catalog"]
	if meta.Method != config.MethodArchive {
		t.Fatalf("lockfile method = %q, want %q", meta.Method, config.MethodArchive)
	}
	if !meta.InstallQuiet {
		t.Fatal("registry quiet hint was not recorded in lockfile")
	}
	if meta.ContentPaths[domain.CategoryPrompts] != "material/prompts" {
		t.Fatalf("content_paths.prompts = %q", meta.ContentPaths[domain.CategoryPrompts])
	}
}

func TestRootModel_InitLoadsProfilesBeforePacks(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected profile load command")
	}

	msg := cmd()
	if _, ok := msg.(tea.BatchMsg); ok {
		t.Fatalf("root init returned a batch; packs/registry init must wait for profiles to load")
	}
	if _, ok := msg.(profilescreen.LoadedMsg); !ok {
		t.Fatalf("root init returned %T, want profilescreen.LoadedMsg", msg)
	}
}

func TestScreenFromPanicsOnWrongConcreteType(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	m.router = m.router.Set(tuiapp.ScreenSearch, configscreen.New(t.TempDir()))

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for wrong screen concrete type")
		}
	}()
	_ = m.searchScreen()
}

func TestRootModel_VInSaveTabDoesNotOpenPlanView(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabSave
	m.width = 120
	m.height = 40
	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = result.(rootModel)
	result, _ = m.Update(savescreen.FilesDiscoveredMsg{Candidates: []app.SaveCandidate{{
		HarnessFile: app.HarnessFile{
			HarnessPath: "/tmp/.claude.json",
			RelPath:     "atlassian",
			Category:    domain.CategoryMCP,
			State:       app.FileConflict,
			Content:     []byte("{\"name\":\"atlassian\"}\n"),
		},
		Selected: true,
	}}})
	m = result.(rootModel)

	result, cmd := m.Update(testKeyText("v"))
	rm := result.(rootModel)
	if rm.planView != nil {
		t.Fatal("expected save tab v key to stay in save tab, not open plan view")
	}
	if !rm.saveScreen().DiffOpen() {
		t.Fatal("expected save tab diff view to open on v")
	}
	if cmd == nil {
		t.Fatal("expected diff load command")
	}
}

func TestRootModel_PackContentActionsHideLocalFileActionsForIndexedContent(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	m.activeTab = tabPacks
	result, _ := m.Update(packsscreen.IndexLoadedMsg{
		Items: []app.IndexedPackDetail{{
			Name:   "preview-pack",
			Status: "inspected",
			Source: "inspected",
			Resources: []app.IndexedPackResource{{
				Kind: "workflow",
				Name: "release-check",
			}},
		}},
	})
	m = result.(rootModel)
	packs := m.packsScreen().ApplyCursorHint("preview-pack")
	var cmd tea.Cmd
	var ok bool
	packs, cmd, ok = packs.ApplyContentHint("workflow", "release-check")
	if !ok {
		t.Fatal("expected content hint to resolve")
	}
	if cmd != nil {
		t.Fatal("did not expect file preview load for indexed-only content")
	}
	m = m.setPacksScreen(packs)

	result, _ = m.openPackContentActions()
	m = result.(rootModel)
	if m.dialog != nil {
		t.Fatalf("expected no local file action dialog, got %+v", m.dialog)
	}
	if !strings.Contains(m.statusText, "install pack") {
		t.Fatalf("statusText = %q, want install pack guidance", m.statusText)
	}
}

func TestRootModel_ProfilesTabShowsLastProfileAtBottom(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabProfiles
	m.width = 100
	m.height = 14
	var seeds []profilescreen.Seed
	for i := range 7 {
		seeds = append(seeds, profilescreen.Seed{
			Name:   fmt.Sprintf("profile-%d", i),
			Config: config.ProfileConfig{Packs: []config.PackEntry{{Name: "pack-a"}}},
		})
	}
	m = seedProfiles(m, seeds...)
	profiles := m.profilesScreen().SetFocus(profilescreen.FocusProfiles).SetCursor(6)
	m = m.setProfilesScreen(profiles)
	m.router = m.router.SetSize(tuiapp.ScreenProfiles, 100, 10)

	view := m.View().Content
	if !strings.Contains(view, "profile-6") {
		t.Fatalf("expected root view to include last visible profile, got:\n%s", view)
	}
}

func TestRootModel_TabSwitching(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	if m.activeTab != tabProfiles {
		t.Fatalf("expected initial tab = profiles, got %d", m.activeTab)
	}

	// Tab key is Type: tea.KeyTab.
	m.activeTab = tabProfiles
	result, _ := m.Update(testKeyCode(tea.KeyTab))
	m = result.(rootModel)
	if m.activeTab != tabPacks {
		t.Fatalf("expected tab to switch to packs, got %d", m.activeTab)
	}

	result, _ = m.Update(testKeyCode(tea.KeyTab))
	m = result.(rootModel)
	if m.activeTab != tabSync {
		t.Fatalf("expected tab to switch to sync, got %d", m.activeTab)
	}

	result, _ = m.Update(testKeyCode(tea.KeyTab))
	m = result.(rootModel)
	if m.activeTab != tabSave {
		t.Fatalf("expected tab to switch to save, got %d", m.activeTab)
	}

	result, _ = m.Update(testKeyCode(tea.KeyTab))
	m = result.(rootModel)
	if m.activeTab != tabSearch {
		t.Fatalf("expected tab to switch to search, got %d", m.activeTab)
	}

	result, _ = m.Update(testKeyCode(tea.KeyTab))
	m = result.(rootModel)
	if m.activeTab != tabConfig {
		t.Fatalf("expected tab to switch to config, got %d", m.activeTab)
	}

	result, _ = m.Update(testKeyCode(tea.KeyTab))
	m = result.(rootModel)
	if m.activeTab != tabProfiles {
		t.Fatalf("expected tab to wrap back to profiles, got %d", m.activeTab)
	}
}

func TestRootModel_InitialSearchTab(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{
		InitialTab:         "search",
		InitialSearchQuery: "deploy",
		InitialSearchKind:  "workflow",
		InitialSearchCat:   "ops",
		InitialSearchState: "registered",
	})
	m.width = 100
	m.height = 24
	m.router = m.router.SetSizeAll(100, 20)

	if m.activeTab != tabSearch {
		t.Fatalf("activeTab = %v, want search", m.activeTab)
	}
	if got := m.activeScreenID(); got != tuiapp.ScreenSearch {
		t.Fatalf("active screen = %q, want search", got)
	}
	view := stripSGRForTUITest(m.View().Content)
	for _, want := range []string{"Search: deploy", "Kind: [workflow]", "Category: [ops]", "Show: [registered]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestRootModel_QuitKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"q", testKeyText("q")},
		{"ctrl+c", testKeyCtrl('c')},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newRootModel(context.Background(), RunConfig{})
			result, cmd := m.Update(tt.key)
			rm := result.(rootModel)
			if !rm.quitting {
				t.Fatalf("expected quitting=true after %s", tt.name)
			}
			if cmd == nil {
				t.Fatalf("expected tea.Quit cmd after %s", tt.name)
			}
		})
	}
}

func TestRootModel_ViewMouseHitUsesSearchLayer(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabSearch
	result, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = result.(rootModel)

	v := m.View()
	if v.OnMouse == nil {
		t.Fatal("expected mouse handler")
	}
	cmd := v.OnMouse(tea.MouseClickMsg(tea.Mouse{
		X:      2,
		Y:      m.contentOffsetY() + 1,
		Button: tea.MouseLeft,
	}))
	if cmd == nil {
		t.Fatal("expected layer hit command")
	}
	msg := cmd()
	hit, ok := msg.(common.LayerHitMsg)
	if !ok {
		t.Fatalf("expected LayerHitMsg, got %T", msg)
	}
	if hit.ID != "search:input" {
		t.Fatalf("expected search:input hit, got %q", hit.ID)
	}
}

func TestRootModel_ViewMouseHitUsesConfigLayer(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabConfig
	m = seedProfiles(m, profilescreen.Seed{Name: "default", IsActive: true})
	result, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = result.(rootModel)

	v := m.View()
	if v.OnMouse == nil {
		t.Fatal("expected mouse handler")
	}
	lines := strings.Split(stripSGRForTUITest(v.Content), "\n")
	x, y := findRenderedTextInTUIView(t, lines, "> Sync Defaults")
	cmd := v.OnMouse(tea.MouseClickMsg(tea.Mouse{
		X:      x + 2,
		Y:      y,
		Button: tea.MouseLeft,
	}))
	if cmd == nil {
		t.Fatal("expected layer hit command")
	}
	msg := cmd()
	hit, ok := msg.(common.LayerHitMsg)
	if !ok {
		t.Fatalf("expected LayerHitMsg, got %T", msg)
	}
	if hit.ID != "config:section:sync-defaults" {
		t.Fatalf("expected config:section:sync-defaults hit, got %q", hit.ID)
	}
}

func TestRootModel_ConfigRowHitLayersAlignWithRenderedRows(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabConfig
	m = seedProfiles(m, profilescreen.Seed{
		Name:     "default",
		IsActive: true,
		Config: config.ProfileConfig{
			Params: map[string]string{"workspace": "team-alpha"},
		},
	})
	result, _ := m.Update(configscreen.LoadedMsg{
		ProfileName: "default",
		Refs:        []app.ProfileRef{{Kind: "env", Name: "API_TOKEN", Status: "dotenv", Pack: "jira"}},
		Env:         []app.EnvEntry{{Key: "API_TOKEN", Length: 6}},
	})
	m = result.(rootModel)
	result, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	m = result.(rootModel)

	result, _ = m.Update(testKeyText("j"))
	m = result.(rootModel)
	v := m.View()
	lines := strings.Split(stripSGRForTUITest(v.Content), "\n")
	x, y := findRenderedTextInTUIView(t, lines, "team-alpha")
	cmd := v.OnMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("expected param row layer hit command")
	}
	msg := cmd()
	hit, ok := msg.(common.LayerHitMsg)
	if !ok {
		t.Fatalf("expected LayerHitMsg, got %T", msg)
	}
	if hit.ID != "config:param:workspace" {
		t.Fatalf("expected config:param:workspace hit, got %q", hit.ID)
	}

	result, _ = m.Update(testKeyText("j"))
	m = result.(rootModel)
	v = m.View()
	lines = strings.Split(stripSGRForTUITest(v.Content), "\n")
	x, y = findRenderedTextInTUIView(t, lines, "API_TOKEN")
	cmd = v.OnMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("expected env row layer hit command")
	}
	msg = cmd()
	hit, ok = msg.(common.LayerHitMsg)
	if !ok {
		t.Fatalf("expected LayerHitMsg, got %T", msg)
	}
	if hit.ID != "config:env:API_TOKEN" {
		t.Fatalf("expected config:env:API_TOKEN hit, got %q", hit.ID)
	}
}

func TestRootModel_SearchOpenPackSwitchesToPacksDetail(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabSearch

	result, _ := m.Update(search.OpenPackMsg{PackName: "preview-pack", Kind: "workflow", Name: "release-check"})
	m = result.(rootModel)
	if m.activeTab != tabPacks {
		t.Fatalf("activeTab = %v, want packs", m.activeTab)
	}

	result, _ = m.Update(packsscreen.IndexLoadedMsg{
		Items: []app.IndexedPackDetail{{
			Name:      "preview-pack",
			Status:    "inspected",
			Source:    "inspected",
			Resources: []app.IndexedPackResource{{Kind: "workflow", Name: "release-check"}},
		}},
	})
	m = result.(rootModel)
	li, ok := m.packsScreen().CurrentListItem()
	if !ok || li.Name != "preview-pack" {
		t.Fatalf("current pack = %+v, %v; want preview-pack", li, ok)
	}
	if m.packsScreen().Focus() != packsscreen.FocusContent {
		t.Fatalf("packs focus = %v, want content", m.packsScreen().Focus())
	}
	sel, ok := m.packsScreen().CurrentContentSelection()
	if !ok {
		t.Fatal("expected current content selection")
	}
	if sel.Category != domain.CategoryWorkflows || sel.ID != "release-check" {
		t.Fatalf("current content = %+v, want workflow release-check", sel)
	}
}

func TestRootModel_SearchInstallKeyDoesNotInstall(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	m.activeTab = tabSearch
	result, _ := m.Update(search.ResultsMsg{Results: []app.SearchResult{{
		Pack:      "preview-pack",
		Kind:      "pack",
		Name:      "preview-pack",
		Installed: false,
	}}})
	m = result.(rootModel)
	result, _ = m.Update(testKeyText("down"))
	m = result.(rootModel)

	result, cmd := m.Update(testKeyText("i"))
	m = result.(rootModel)
	if cmd != nil {
		t.Fatal("search install key should not emit a command")
	}
	if m.dialog != nil {
		t.Fatalf("search install key should not open a dialog, got %+v", m.dialog)
	}
}

func TestRootModel_PacksInstallActionPrefillsIndexedRepo(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	m.activeTab = tabPacks
	repo := "https://example.com/preview-pack.git"
	result, _ := m.Update(packsscreen.IndexLoadedMsg{
		Items: []app.IndexedPackDetail{{
			Name:   "preview-pack",
			Repo:   repo,
			Status: "inspected",
			Source: "inspected",
		}},
	})
	m = result.(rootModel)
	m = m.setPacksScreen(m.packsScreen().ApplyCursorHint("preview-pack"))

	result, _ = m.handleDialogResult(dialogResultMsg{
		ID:        dialogActionPackTab,
		Confirmed: true,
		Value:     actInstall,
	})
	m = result.(rootModel)
	if m.dialog == nil || m.dialog.ID != dialogPackInstall {
		t.Fatalf("expected pack install dialog, got %+v", m.dialog)
	}
	if m.dialog.TextValue != repo {
		t.Fatalf("install dialog value = %q, want repo %q", m.dialog.TextValue, repo)
	}
}

func TestRootModel_PackActionsOfferPreviewInstallForUninstalledPack(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	m.activeTab = tabPacks
	result, _ := m.Update(packsscreen.IndexLoadedMsg{
		Items: []app.IndexedPackDetail{{
			Name:   "preview-pack",
			Repo:   "https://example.com/preview-pack.git",
			Status: "inspected",
			Source: "inspected",
		}},
	})
	m = result.(rootModel)
	m = m.setPacksScreen(m.packsScreen().ApplyCursorHint("preview-pack"))

	result, _ = m.openPackTabActions()
	m = result.(rootModel)
	if m.dialog == nil || m.dialog.ID != dialogActionPackTab {
		t.Fatalf("expected pack action dialog, got %+v", m.dialog)
	}
	if !slices.Contains(m.dialog.ListItems, "Preview install") {
		t.Fatalf("actions = %v, want Preview install", m.dialog.ListItems)
	}
}

func TestRootModel_PackActionsOfferPreviewUpdateForInstalledPack(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	m.activeTab = tabPacks
	m = m.setPacksScreen(packsscreen.New(m.cfg.ConfigDir).WithInstalled([]app.PackShowEntry{{
		Name:   "installed-pack",
		Method: config.MethodCopy,
	}}))

	result, _ := m.openPackTabActions()
	m = result.(rootModel)
	if m.dialog == nil || m.dialog.ID != dialogActionPackTab {
		t.Fatalf("expected pack action dialog, got %+v", m.dialog)
	}
	if !slices.Contains(m.dialog.ListItems, "Preview update") {
		t.Fatalf("actions = %v, want Preview update", m.dialog.ListItems)
	}
}

func TestRootModel_PacksActionRequestOpensActionMenu(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{ConfigDir: t.TempDir()})
	m.activeTab = tabPacks
	m = m.setPacksScreen(packsscreen.New(m.cfg.ConfigDir).WithInstalled([]app.PackShowEntry{{
		Name: "installed-pack",
	}}))

	result, cmd := m.Update(packsscreen.ActionRequestMsg{})
	m = result.(rootModel)
	if cmd != nil {
		t.Fatal("packs action request should not emit a command")
	}
	if m.dialog == nil || m.dialog.ID != dialogActionPackTab {
		t.Fatalf("expected pack action dialog, got %+v", m.dialog)
	}
}

func TestRootModel_PreviewInstallInspectsUninstalledPack(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	srcDir := writeTUITestPack(t, t.TempDir(), "preview-pack", "initial")
	m := newRootModel(context.Background(), RunConfig{ConfigDir: configDir})
	result, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = result.(rootModel)
	m.activeTab = tabPacks
	result, _ = m.Update(packsscreen.IndexLoadedMsg{
		Items: []app.IndexedPackDetail{{
			Name:   "preview-pack",
			Repo:   srcDir,
			Status: "inspected",
			Source: "inspected",
		}},
	})
	m = result.(rootModel)
	m = m.setPacksScreen(m.packsScreen().ApplyCursorHint("preview-pack"))

	result, cmd := m.handleDialogResult(dialogResultMsg{
		ID:        dialogActionPackTab,
		Confirmed: true,
		Value:     "Preview install",
	})
	m = result.(rootModel)
	if m.preview == nil {
		t.Fatal("expected preview overlay to open")
	}
	if cmd == nil {
		t.Fatal("expected preview install command")
	}

	msg, ok := cmd().(previewLoadedMsg)
	if !ok {
		t.Fatalf("expected previewLoadedMsg, got %T", cmd())
	}
	result, _ = m.Update(msg)
	m = result.(rootModel)
	rendered := stripSGRForTUITest(m.preview.Render())
	for _, want := range []string{"Preview install: preview-pack", "Name: preview-pack", "Rules", "demo"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("preview missing %q:\n%s", want, rendered)
		}
	}
	if _, err := os.Stat(filepath.Join(configDir, "packs", "preview-pack")); !os.IsNotExist(err) {
		t.Fatalf("preview install should not install pack, stat err=%v", err)
	}
}

func TestRootModel_PreviewUpdateUsesDryRunWithoutMutatingPack(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	configDir := t.TempDir()
	if err := config.SaveSyncConfig(config.SyncConfigPath(configDir), config.SyncConfig{
		SchemaVersion: config.SyncConfigSchemaVersion,
	}); err != nil {
		t.Fatal(err)
	}
	srcDir := writeTUITestPack(t, t.TempDir(), "copy-pack", "initial")
	if err := app.PackInstall(ctx, app.PackInstallRequest{
		PackPath:  srcDir,
		ConfigDir: configDir,
		Add:       false,
		With:      domain.NewBundledSet(),
	}, io.Discard); err != nil {
		t.Fatalf("PackInstall: %v", err)
	}
	writeTUITestPack(t, srcDir, "copy-pack", "updated")

	m := newRootModel(ctx, RunConfig{ConfigDir: configDir})
	result, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = result.(rootModel)
	result, _ = m.Update(packsscreen.Load(configDir)())
	m = result.(rootModel)
	m.activeTab = tabPacks
	m = m.setPacksScreen(m.packsScreen().ApplyCursorHint("copy-pack"))

	result, cmd := m.handleDialogResult(dialogResultMsg{
		ID:        dialogActionPackTab,
		Confirmed: true,
		Value:     "Preview update",
	})
	m = result.(rootModel)
	if m.preview == nil {
		t.Fatal("expected preview overlay to open")
	}
	if cmd == nil {
		t.Fatal("expected preview update command")
	}

	msg, ok := cmd().(previewLoadedMsg)
	if !ok {
		t.Fatalf("expected previewLoadedMsg, got %T", cmd())
	}
	result, _ = m.Update(msg)
	m = result.(rootModel)
	rendered := stripSGRForTUITest(m.preview.Render())
	for _, want := range []string{"Preview update: copy-pack", "Would update", "dry-run", "Changes:", "~ rules/demo.md"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("preview missing %q:\n%s", want, rendered)
		}
	}

	body, err := os.ReadFile(filepath.Join(configDir, "packs", "copy-pack", "rules", "demo.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\ninitial\n") || strings.Contains(string(body), "\nupdated\n") {
		t.Fatalf("preview update mutated installed pack:\n%s", string(body))
	}
}

func writeTUITestPack(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          name,
		Root:          ".",
		Rules:         []string{"demo"},
	}
	if err := config.SavePackManifest(filepath.Join(dir, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("---\nname: demo\ndescription: Demo rule\nmetadata:\n  owner: test\n  last_updated: 2026-05-07\n---\n\n%s\n", body)
	if err := os.WriteFile(filepath.Join(dir, "rules", "demo.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRootModel_ViewMouseHitUsesPreviewOverlayLayer(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	result, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = result.(rootModel)
	p := newPreviewModel(80, 24)
	p = p.SetContent(previewLoadedMsg{
		Title:    "anti-slop",
		Category: domain.CategoryRules,
		Body:     "body",
	})
	m.preview = &p

	v := m.View()
	if v.OnMouse == nil {
		t.Fatal("expected mouse handler")
	}
	cmd := v.OnMouse(tea.MouseClickMsg(tea.Mouse{
		X:      72,
		Y:      1,
		Button: tea.MouseLeft,
	}))
	if cmd == nil {
		t.Fatal("expected layer hit command")
	}
	msg := cmd()
	hit, ok := msg.(common.LayerHitMsg)
	if !ok {
		t.Fatalf("expected LayerHitMsg, got %T", msg)
	}
	if hit.ID != "preview:close" {
		t.Fatalf("expected preview:close hit, got %q", hit.ID)
	}

	result, _ = m.Update(hit)
	if rm := result.(rootModel); rm.preview != nil {
		t.Fatal("expected preview to close from overlay mouse hit")
	}
}

func TestRootModel_FullScreenOverlayLeavesBottomBorderAboveHelpBar(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	result, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = result.(rootModel)

	p := newPreviewModel(80, 12)
	p = p.SetContent(previewLoadedMsg{
		Title: "Preview update: pack",
		Body:  strings.Repeat("line\n", 20),
	})
	m.preview = &p

	lines := strings.Split(stripSGRForTUITest(m.View().Content), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least three rendered lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	helpLine := lines[len(lines)-1]
	if !strings.Contains(helpLine, "j/k:scroll") || !strings.Contains(helpLine, "esc:close") {
		t.Fatalf("expected help bar on final row, got %q\nfull view:\n%s", helpLine, strings.Join(lines, "\n"))
	}
	gapLine := lines[len(lines)-2]
	if strings.TrimSpace(gapLine) != "" {
		t.Fatalf("expected blank row between overlay border and help bar, got %q\nfull view:\n%s", gapLine, strings.Join(lines, "\n"))
	}
	borderLine := lines[len(lines)-3]
	if !strings.Contains(borderLine, "╰") || !strings.Contains(borderLine, "╯") {
		t.Fatalf("expected visible bottom border above help gap, got %q\nfull view:\n%s", borderLine, strings.Join(lines, "\n"))
	}
}

func TestRootModel_ViewMouseHitUsesDialogOverlayLayer(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	result, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = result.(rootModel)
	d := newConfirmDialog(dialogSaveOnExit, "Save changes?")
	m.dialog = &d

	v := m.View()
	if v.OnMouse == nil {
		t.Fatal("expected mouse handler")
	}
	cmd := v.OnMouse(tea.MouseClickMsg(tea.Mouse{
		X:      5,
		Y:      m.contentOffsetY() + 5,
		Button: tea.MouseLeft,
	}))
	if cmd == nil {
		t.Fatal("expected layer hit command")
	}
	msg := cmd()
	hit, ok := msg.(common.LayerHitMsg)
	if !ok {
		t.Fatalf("expected LayerHitMsg, got %T", msg)
	}
	if hit.ID != "dialog:yes" {
		t.Fatalf("expected dialog:yes hit, got %q", hit.ID)
	}

	_, cmd = m.Update(hit)
	if cmd == nil {
		t.Fatal("expected dialog result command")
	}
	msg = cmd()
	resultMsg, ok := msg.(dialogResultMsg)
	if !ok {
		t.Fatalf("expected dialogResultMsg, got %T", msg)
	}
	if !resultMsg.Confirmed {
		t.Fatal("expected dialog mouse hit to confirm")
	}
}

func TestRootModel_ViewMouseHitUsesChromeTabLayer(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	result, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = result.(rootModel)
	m.activeTab = tabProfiles

	v := m.View()
	if v.OnMouse == nil {
		t.Fatal("expected mouse handler")
	}
	cmd := v.OnMouse(tea.MouseClickMsg(tea.Mouse{
		X:      14,
		Y:      0,
		Button: tea.MouseLeft,
	}))
	if cmd == nil {
		t.Fatal("expected layer hit command")
	}
	msg := cmd()
	hit, ok := msg.(common.LayerHitMsg)
	if !ok {
		t.Fatalf("expected LayerHitMsg, got %T", msg)
	}
	if hit.ID != "chrome:tab:1" {
		t.Fatalf("expected chrome:tab:1 hit, got %q", hit.ID)
	}

	result, _ = m.Update(hit)
	if got := result.(rootModel).activeTab; got != tabPacks {
		t.Fatalf("activeTab = %v, want %v", got, tabPacks)
	}
}

func TestRootModel_ViewMouseHitUsesFullChromeTabHeight(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	result, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = result.(rootModel)
	m.activeTab = tabProfiles

	v := m.View()
	if v.OnMouse == nil {
		t.Fatal("expected mouse handler")
	}
	cmd := v.OnMouse(tea.MouseClickMsg(tea.Mouse{
		X:      14,
		Y:      m.contentOffsetY() - 1,
		Button: tea.MouseLeft,
	}))
	if cmd == nil {
		t.Fatal("expected layer hit command")
	}
	msg := cmd()
	hit, ok := msg.(common.LayerHitMsg)
	if !ok {
		t.Fatalf("expected LayerHitMsg, got %T", msg)
	}
	if hit.ID != "chrome:tab:1" {
		t.Fatalf("expected chrome:tab:1 hit, got %q", hit.ID)
	}
}

func TestRootModel_EscWithDirtyAutoSaves(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.dirty = true
	m = seedProfiles(m, profilescreen.Seed{
		Name:   "test",
		Path:   "/tmp/test.yaml",
		Config: config.ProfileConfig{Packs: []config.PackEntry{{Name: "test-pack"}}},
		Dirty:  true,
	})

	result, cmd := m.Update(testKeyCode(tea.KeyEsc))
	rm := result.(rootModel)

	// Should auto-save (no dialog), pending exit.
	if rm.dialog != nil {
		t.Fatal("expected no dialog — auto-save should be implicit")
	}
	if !rm.pendingExit {
		t.Fatal("expected pendingExit=true for auto-save")
	}
	if cmd == nil {
		t.Fatal("expected save command")
	}
}

func TestRootModel_EscWithoutDirtyQuits(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.dirty = false
	// No unsynced profiles → should quit directly.
	result, cmd := m.Update(testKeyCode(tea.KeyEsc))
	rm := result.(rootModel)

	if !rm.quitting {
		t.Fatal("expected quitting=true on esc without dirty")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd")
	}
}

func TestRootModel_HelpTextChangesWithContext(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	// Default: profile list (panelProfiles).
	help := m.helpText()
	if !strings.Contains(help, "enter:packs") {
		t.Fatalf("expected profile list help to mention enter:packs, got %q", help)
	}
	if !strings.Contains(help, ".:actions") {
		t.Fatalf("expected profile list help to mention .:actions, got %q", help)
	}
	if !strings.Contains(help, "s:sync") {
		t.Fatalf("expected profile list help to mention s:sync, got %q", help)
	}

	// Pack roster (panelPacks).
	m = m.setProfilesScreen(m.profilesScreen().SetFocus(profilescreen.FocusPacks))
	help = m.helpText()
	if !strings.Contains(help, "space:toggle") {
		t.Fatalf("expected pack roster help to mention space:toggle, got %q", help)
	}
	if !strings.Contains(help, "enter:tree") {
		t.Fatalf("expected pack roster help to mention enter:tree, got %q", help)
	}
	if !strings.Contains(help, "esc:back") {
		t.Fatalf("expected pack roster help to mention esc:back, got %q", help)
	}

	// Content tree (panelTree).
	m = m.setProfilesScreen(m.profilesScreen().SetFocus(profilescreen.FocusTree))
	help = m.helpText()
	if !strings.Contains(help, "space:toggle") {
		t.Fatalf("expected tree mode help to mention space:toggle, got %q", help)
	}
	if !strings.Contains(help, "esc:back") {
		t.Fatalf("expected tree mode help to mention esc:back, got %q", help)
	}

	// Packs tab.
	m.activeTab = tabPacks
	help = m.helpText()
	if !strings.Contains(help, "j/k:navigate") {
		t.Fatalf("expected packs tab help to mention j/k:navigate, got %q", help)
	}
	if !strings.Contains(help, ".:actions") {
		t.Fatalf("expected packs tab help to mention .:actions, got %q", help)
	}
}

func TestRootModel_PacksLoadedWhileProfilesTabActive(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	if m.activeTab != tabProfiles {
		t.Fatal("expected initial tab to be profiles")
	}

	result, _ := m.Update(packsscreen.LoadedFromEntries([]app.PackShowEntry{
		{Name: "test-pack", Version: "1.0"},
	}))
	rm := result.(rootModel)
	packs := rm.packsScreen()
	if packs.InstalledCount() != 1 {
		t.Fatalf("expected packs to receive message, got %d items", packs.InstalledCount())
	}
	names := packs.InstalledNames()
	if names[0] != "test-pack" {
		t.Fatalf("expected pack name 'test-pack', got %q", names[0])
	}
}

func TestRootModel_PackCreateMsgReloadsPacksAndProfiles(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	result, cmd := m.Update(packCreatedMsg{Name: "fresh-pack"})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "created fresh-pack") {
		t.Fatalf("expected status 'created fresh-pack', got %q", rm.statusText)
	}
	if cmd == nil {
		t.Fatal("expected reload command")
	}
}

func TestRootModel_PackCreateMsgError(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	result, _ := m.Update(packCreatedMsg{
		Name: "bad",
		Err:  fmt.Errorf("already exists"),
	})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "create error") {
		t.Fatalf("expected status to mention 'create error', got %q", rm.statusText)
	}
}

func TestRootModel_DialogResultNotSwallowed(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	d := newConfirmDialog(dialogSaveOnExit, "Save changes?")
	m.dialog = &d

	// Dialog result should be handled, not swallowed.
	result, cmd := m.Update(dialogResultMsg{ID: dialogSaveOnExit, Confirmed: false})
	rm := result.(rootModel)
	if rm.dialog != nil {
		t.Fatal("expected dialog to be cleared after result message")
	}
	// save-on-exit is a legacy dialog ID that just quits now.
	if !rm.quitting {
		t.Fatal("expected quitting=true")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd")
	}
}

func TestRootModel_ProfileSavedClearsDirty(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.dirty = true
	m = seedProfiles(m, profilescreen.Seed{Name: "test", Path: "/tmp/test.yaml", Dirty: true})

	result, _ := m.Update(profilescreen.SavedMsg{ProfileName: "test"})
	rm := result.(rootModel)
	if rm.dirty {
		t.Fatal("expected dirty=false after successful save")
	}
	if rm.profilesScreen().Dirty() {
		t.Fatal("expected profiles.dirty=false after successful save")
	}

	// Verify dirty does NOT reappear when a subsequent message is processed.
	result2, _ := rm.Update(testKeyText("j"))
	rm2 := result2.(rootModel)
	if rm2.dirty {
		t.Fatal("dirty should remain false after subsequent key message")
	}
}

func TestRootModel_ProfileSavedSchedulesAutoSyncForActiveProfile(t *testing.T) {
	t.Parallel()
	cfg := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	cfg.Defaults.AutoSync = true
	m := newRootModel(context.Background(), RunConfig{SyncCfg: cfg})
	m = seedProfiles(m, profilescreen.Seed{
		Name:     "default",
		Path:     "/tmp/default.yaml",
		IsActive: true,
		Dirty:    true,
		SyncTarget: common.SyncTarget{
			Scope:     domain.ScopeProject,
			Harnesses: []string{string(domain.HarnessClaudeCode)},
		},
	})

	result, cmd := m.Update(profilescreen.SavedMsg{ProfileName: "default"})
	rm := result.(rootModel)
	if rm.pendingAutoSync == nil {
		t.Fatal("expected pending auto-sync after active profile save")
	}
	if rm.pendingAutoSync.profileName != "default" {
		t.Fatalf("pending profile = %q, want default", rm.pendingAutoSync.profileName)
	}
	if cmd == nil {
		t.Fatal("expected debounce command to be scheduled")
	}
}

func TestRootModel_ProfileSavedShowsSyncHintWhenAutoSyncDisabled(t *testing.T) {
	t.Parallel()
	cfg := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	m := newRootModel(context.Background(), RunConfig{SyncCfg: cfg})
	m = seedProfiles(m, profilescreen.Seed{
		Name:     "default",
		Path:     "/tmp/default.yaml",
		IsActive: true,
		Dirty:    true,
	})

	result, _ := m.Update(profilescreen.SavedMsg{ProfileName: "default"})
	rm := result.(rootModel)
	if !strings.Contains(rm.statusText, "press s to sync") {
		t.Fatalf("statusText = %q, want sync hint", rm.statusText)
	}
	if rm.pendingAutoSync != nil {
		t.Fatalf("auto_sync disabled should not schedule auto-sync: %+v", rm.pendingAutoSync)
	}
}

func TestRootModel_ProfileActivatedSchedulesAutoSyncForActiveProfile(t *testing.T) {
	t.Parallel()
	cfg := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	cfg.Defaults.AutoSync = true
	m := newRootModel(context.Background(), RunConfig{SyncCfg: cfg})
	m = seedProfiles(m,
		profilescreen.Seed{Name: "default", IsActive: true},
		profilescreen.Seed{
			Name: "team",
			Path: "/tmp/team.yaml",
			SyncTarget: common.SyncTarget{
				Scope:     domain.ScopeProject,
				Harnesses: []string{string(domain.HarnessClaudeCode)},
			},
		},
	)

	result, cmd := m.Update(profilescreen.ActivatedMsg{ProfileName: "team"})
	rm := result.(rootModel)
	if rm.pendingAutoSync == nil {
		t.Fatal("expected pending auto-sync after activating profile")
	}
	if rm.pendingAutoSync.profileName != "team" {
		t.Fatalf("pending profile = %q, want team", rm.pendingAutoSync.profileName)
	}
	if cmd == nil {
		t.Fatal("expected debounce command to be scheduled")
	}
}

func TestRootModel_ProfileSavedDoesNotScheduleAutoSyncForInactiveProfile(t *testing.T) {
	t.Parallel()
	cfg := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	cfg.Defaults.AutoSync = true
	m := newRootModel(context.Background(), RunConfig{SyncCfg: cfg})
	m = seedProfiles(m,
		profilescreen.Seed{Name: "default", IsActive: true},
		profilescreen.Seed{Name: "scratch", Dirty: true},
	)

	result, _ := m.Update(profilescreen.SavedMsg{ProfileName: "scratch"})
	rm := result.(rootModel)
	if rm.pendingAutoSync != nil {
		t.Fatalf("inactive profile save scheduled auto-sync: %+v", rm.pendingAutoSync)
	}
}

func TestRootModel_ProfileSavedResetsPendingAutoSync(t *testing.T) {
	t.Parallel()
	cfg := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	cfg.Defaults.AutoSync = true
	m := newRootModel(context.Background(), RunConfig{SyncCfg: cfg})
	m = seedProfiles(m, profilescreen.Seed{Name: "default", IsActive: true, Dirty: true})

	result, _ := m.Update(profilescreen.SavedMsg{ProfileName: "default"})
	rm := result.(rootModel)
	firstID := rm.pendingAutoSync.id

	result, _ = rm.Update(profilescreen.SavedMsg{ProfileName: "default"})
	rm = result.(rootModel)
	if rm.pendingAutoSync == nil {
		t.Fatal("expected pending auto-sync after second save")
	}
	if rm.pendingAutoSync.id == firstID {
		t.Fatal("expected second save to reset debounce id")
	}

	result, cmd := rm.Update(autoSyncDueMsg{id: firstID, profileName: "default"})
	rm = result.(rootModel)
	if rm.pendingAutoSync == nil {
		t.Fatal("stale auto-sync tick should not clear newer pending sync")
	}
	if cmd != nil {
		t.Fatal("stale auto-sync tick should not run sync")
	}
}

func TestRootModel_ManualSyncCancelsPendingAutoSync(t *testing.T) {
	t.Parallel()
	cfg := config.SyncConfig{SchemaVersion: config.SyncConfigSchemaVersion}
	cfg.Defaults.AutoSync = true
	m := newRootModel(context.Background(), RunConfig{SyncCfg: cfg})
	m = seedProfiles(m, profilescreen.Seed{
		Name:     "default",
		IsActive: true,
		SyncTarget: common.SyncTarget{
			Scope:     domain.ScopeProject,
			Harnesses: []string{string(domain.HarnessClaudeCode)},
		},
	})
	m.pendingAutoSync = &pendingAutoSyncState{id: 1, profileName: "default"}

	result, _ := m.doSync("", "")
	rm := result.(rootModel)
	if rm.pendingAutoSync != nil {
		t.Fatalf("manual sync did not cancel pending auto-sync: %+v", rm.pendingAutoSync)
	}
}

func TestRootModel_ProfileSavedClearsDirty_MultipleProfiles(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.dirty = true
	m = seedProfiles(m,
		profilescreen.Seed{Name: "alpha", Path: "/tmp/alpha.yaml", Dirty: true},
		profilescreen.Seed{Name: "beta", Path: "/tmp/beta.yaml", Dirty: true},
	)

	// Save alpha — beta is still dirty, so global dirty must stay true.
	result, _ := m.Update(profilescreen.SavedMsg{ProfileName: "alpha"})
	rm := result.(rootModel)
	if !rm.dirty {
		t.Fatal("expected dirty=true while beta is still unsaved")
	}
	if !rm.profilesScreen().Dirty() {
		t.Fatal("expected profiles.dirty=true while beta is still unsaved")
	}
	if item, _ := rm.profilesScreen().CurrentProfile(); item.Dirty {
		t.Fatal("expected alpha.dirty=false after save")
	}

	// Save beta — now all profiles are clean.
	result2, _ := rm.Update(profilescreen.SavedMsg{ProfileName: "beta"})
	rm2 := result2.(rootModel)
	if rm2.dirty {
		t.Fatal("expected dirty=false after all profiles saved")
	}
	if rm2.profilesScreen().Dirty() {
		t.Fatal("expected profiles.dirty=false after all profiles saved")
	}
}

func TestRootModel_ProfileSaveFailKeepsDirty(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.dirty = true
	m = seedProfiles(m, profilescreen.Seed{Name: "test", Dirty: true})

	result, _ := m.Update(profilescreen.SavedMsg{
		ProfileName: "test",
		Err:         fmt.Errorf("disk full"),
	})
	rm := result.(rootModel)
	if !rm.dirty {
		t.Fatal("expected dirty=true when save failed")
	}
	if !rm.profilesScreen().Dirty() {
		t.Fatal("expected profiles.dirty=true when save failed")
	}
}

func TestDialogModel_ConfirmYes(t *testing.T) {
	t.Parallel()
	d := newConfirmDialog("test", "Confirm?")

	// Press y.
	d, cmd := d.UpdateModel(testKeyText("y"))
	_ = d
	if cmd == nil {
		t.Fatal("expected cmd after pressing y")
	}

	msg := cmd()
	result, ok := msg.(dialogResultMsg)
	if !ok {
		t.Fatalf("expected dialogResultMsg, got %T", msg)
	}
	if !result.Confirmed {
		t.Fatal("expected confirmed=true after pressing y")
	}
}

func TestDialogModel_ConfirmNo(t *testing.T) {
	t.Parallel()
	d := newConfirmDialog("test", "Confirm?")

	d, cmd := d.UpdateModel(testKeyText("n"))
	_ = d
	if cmd == nil {
		t.Fatal("expected cmd after pressing n")
	}

	msg := cmd()
	result, ok := msg.(dialogResultMsg)
	if !ok {
		t.Fatalf("expected dialogResultMsg, got %T", msg)
	}
	if result.Confirmed {
		t.Fatal("expected confirmed=false after pressing n")
	}
}

func TestDialogHelpText(t *testing.T) {
	t.Parallel()

	// Confirm dialog.
	d := newConfirmDialog("test", "Confirm?")
	help := d.HelpText()
	if help != "enter:confirm  esc:cancel" {
		t.Fatalf("expected default help for confirm, got %q", help)
	}

	// Plain list select dialog.
	d2 := newListSelectDialog("test", "Select:", []string{"a", "b"})
	help2 := d2.HelpText()
	if !strings.Contains(help2, "enter:select") {
		t.Fatalf("expected 'enter:select' in list help, got %q", help2)
	}

	// List select dialog with actions.
	d3 := newListSelectDialog("test", "Select:", []string{"a", "b"})
	d3.ListActions = []listAction{
		{Key: "a", Name: "activate"},
		{Key: "d", Name: "delete"},
	}
	help3 := d3.HelpText()
	if !strings.Contains(help3, "a:activate") {
		t.Fatalf("expected 'a:activate' in help, got %q", help3)
	}
	if !strings.Contains(help3, "d:delete") {
		t.Fatalf("expected 'd:delete' in help, got %q", help3)
	}
}

// ---------------------------------------------------------------------------
// toolPicker
// ---------------------------------------------------------------------------

func TestToolPicker_SpaceCycles(t *testing.T) {
	t.Parallel()
	items := []toolPickerItem{
		{Label: "read", State: triOff},
		{Label: "write", State: triAsk},
	}
	p := newToolPicker("test", "Tools:", items)

	// Space on cursor 0 (triOff) → triAsk.
	p, _ = p.UpdateModel(testKeyText(" "))
	if p.Items[0].State != triAsk {
		t.Errorf("after 1 space: state = %v, want triAsk", p.Items[0].State)
	}
	// → triAuto.
	p, _ = p.UpdateModel(testKeyText(" "))
	if p.Items[0].State != triAuto {
		t.Errorf("after 2 space: state = %v, want triAuto", p.Items[0].State)
	}
	// → triOff (wrap).
	p, _ = p.UpdateModel(testKeyText(" "))
	if p.Items[0].State != triOff {
		t.Errorf("after 3 space: state = %v, want triOff (wrap)", p.Items[0].State)
	}
}

func TestToolPicker_ShortcutsJumpToStates(t *testing.T) {
	t.Parallel()
	items := []toolPickerItem{{Label: "read", State: triOff}}
	p := newToolPicker("test", "Tools:", items)

	// 'A' → triAuto.
	p, _ = p.UpdateModel(testKeyText("A"))
	if p.Items[0].State != triAuto {
		t.Errorf("'A' should jump to triAuto, got %v", p.Items[0].State)
	}
	// 'a' → triAsk.
	p, _ = p.UpdateModel(testKeyText("a"))
	if p.Items[0].State != triAsk {
		t.Errorf("'a' should jump to triAsk, got %v", p.Items[0].State)
	}
	// 'x' → triOff.
	p, _ = p.UpdateModel(testKeyText("x"))
	if p.Items[0].State != triOff {
		t.Errorf("'x' should jump to triOff, got %v", p.Items[0].State)
	}
}

func TestToolPicker_EnterPartitionsResult(t *testing.T) {
	t.Parallel()
	items := []toolPickerItem{
		{Label: "alpha", State: triAsk},
		{Label: "beta", State: triOff},
		{Label: "gamma", State: triAuto},
		{Label: "delta", State: triAsk},
	}
	p := newToolPicker("test", "Tools:", items)

	_, cmd := p.UpdateModel(testKeyCode(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("enter should produce a command")
	}
	result := cmd().(toolPickerResultMsg)
	if !result.Confirmed {
		t.Fatal("enter should confirm")
	}
	// "ask" → ask, preserving input order.
	if len(result.Ask) != 2 || result.Ask[0] != "alpha" || result.Ask[1] != "delta" {
		t.Errorf("ask = %v, want [alpha delta]", result.Ask)
	}
	if len(result.Auto) != 1 || result.Auto[0] != "gamma" {
		t.Errorf("auto = %v, want [gamma]", result.Auto)
	}
	// "off" tools are absent from both slices.
}

func TestToolPicker_EscSaves(t *testing.T) {
	t.Parallel()
	items := []toolPickerItem{{Label: "alpha", State: triAuto}}
	p := newToolPicker("test", "Tools:", items)

	_, cmd := p.UpdateModel(testKeyCode(tea.KeyEsc))
	result := cmd().(toolPickerResultMsg)
	if !result.Confirmed {
		t.Error("esc should save (confirmed=true) — no cancel in the tool picker")
	}
	if len(result.Auto) != 1 || result.Auto[0] != "alpha" {
		t.Errorf("esc should preserve State: auto = %v, want [alpha]", result.Auto)
	}
}

func TestToolPicker_NavigationBounded(t *testing.T) {
	t.Parallel()
	items := []toolPickerItem{
		{Label: "a"},
		{Label: "b"},
		{Label: "c"},
	}
	p := newToolPicker("test", "Tools:", items)

	for range 5 {
		p, _ = p.UpdateModel(testKeyText("j"))
	}
	if p.Cursor != 2 {
		t.Errorf("cursor stuck at %d after 5 'j', want 2", p.Cursor)
	}
	for range 5 {
		p, _ = p.UpdateModel(testKeyText("k"))
	}
	if p.Cursor != 0 {
		t.Errorf("cursor = %d after 5 'k', want 0", p.Cursor)
	}
}

// TestToolPicker_FastNavigation pins the fast-nav keys added after the
// v0.23.0 picker shipped with only j/k cursor movement — users with long
// tool lists (e.g., multi-tool MCP servers) couldn't reach items past the
// viewport without excessive j-spam. g/G jump to the first/last item, and
// PgUp/PgDn page by the visible-window height.
func TestToolPicker_FastNavigation(t *testing.T) {
	t.Parallel()
	items := make([]toolPickerItem, 20)
	for i := range items {
		items[i] = toolPickerItem{Label: fmt.Sprintf("tool-%d", i)}
	}

	p := newToolPicker("test", "Tools:", items)
	p.VisibleH = 5 // Simulate a small viewport: 5 rows at a time.

	p, _ = p.UpdateModel(testKeyText("G"))
	if p.Cursor != len(items)-1 {
		t.Errorf("G: cursor = %d, want %d (last)", p.Cursor, len(items)-1)
	}

	p, _ = p.UpdateModel(testKeyText("g"))
	if p.Cursor != 0 {
		t.Errorf("g: cursor = %d, want 0 (first)", p.Cursor)
	}

	p, _ = p.UpdateModel(testKeyCode(tea.KeyPgDown))
	if p.Cursor != 5 {
		t.Errorf("PgDn from 0: cursor = %d, want 5 (one page)", p.Cursor)
	}
	p, _ = p.UpdateModel(testKeyCode(tea.KeyPgDown))
	if p.Cursor != 10 {
		t.Errorf("PgDn from 5: cursor = %d, want 10", p.Cursor)
	}

	p, _ = p.UpdateModel(testKeyCode(tea.KeyPgUp))
	if p.Cursor != 5 {
		t.Errorf("PgUp from 10: cursor = %d, want 5", p.Cursor)
	}

	// End and Home are aliases for G and g.
	p, _ = p.UpdateModel(testKeyCode(tea.KeyEnd))
	if p.Cursor != len(items)-1 {
		t.Errorf("End: cursor = %d, want %d", p.Cursor, len(items)-1)
	}
	p, _ = p.UpdateModel(testKeyCode(tea.KeyHome))
	if p.Cursor != 0 {
		t.Errorf("Home: cursor = %d, want 0", p.Cursor)
	}

	// Page keys must stay bounded — no underflow past 0 or overflow past last.
	for range 10 {
		p, _ = p.UpdateModel(testKeyCode(tea.KeyPgUp))
	}
	if p.Cursor != 0 {
		t.Errorf("repeated PgUp: cursor = %d, want 0 (clamped)", p.Cursor)
	}
	p.Cursor = len(items) - 1
	for range 10 {
		p, _ = p.UpdateModel(testKeyCode(tea.KeyPgDown))
	}
	if p.Cursor != len(items)-1 {
		t.Errorf("repeated PgDn: cursor = %d, want %d (clamped)", p.Cursor, len(items)-1)
	}
}

// TestToolPicker_HelpTextAdvertisesNavigation pins the fix for the v0.23.0
// gap: the help bar omitted j/k and had no mention of fast-nav, so users
// scrolling past the viewport concluded they couldn't. Both sets of keys
// must appear in helpText so the UI is self-documenting.
func TestToolPicker_HelpTextAdvertisesNavigation(t *testing.T) {
	t.Parallel()
	p := newToolPicker("test", "Tools:", []toolPickerItem{{Label: "x"}})
	help := p.HelpText()
	for _, want := range []string{"j/k", "g/G"} {
		if !strings.Contains(help, want) {
			t.Errorf("helpText = %q, missing %q", help, want)
		}
	}
}

func TestToolPicker_View_AutoRendersSolidMarker(t *testing.T) {
	t.Parallel()
	items := []toolPickerItem{
		{Label: "a", State: triAuto},
		{Label: "b", State: triAuto},
	}
	p := newToolPicker("test", "Tools:", items)
	view := p.Render()
	if !strings.Contains(view, "[■]") {
		t.Errorf("all-auto view should show solid-box markers, got:\n%s", view)
	}
	// The picker writes literal sorted lists; no sentinel is emitted.
	if strings.Contains(view, `"*"`) {
		t.Errorf("view should not mention a [\"*\"] sentinel, got:\n%s", view)
	}
}

func TestChecklistDialog_SpaceToggles(t *testing.T) {
	t.Parallel()
	items := []checkItem{
		{Label: "alpha", Checked: false},
		{Label: "beta", Checked: true},
	}
	d := newChecklistDialog("test", "Pick:", items)

	// Space toggles first item on.
	d, _ = d.UpdateModel(testKeyText(" "))
	if !d.CheckItems[0].Checked {
		t.Error("space should toggle first item to checked")
	}
	// Space again toggles it off.
	d, _ = d.UpdateModel(testKeyText(" "))
	if d.CheckItems[0].Checked {
		t.Error("second space should toggle first item back to unchecked")
	}
}

func TestChecklistDialog_EnterConfirms(t *testing.T) {
	t.Parallel()
	items := []checkItem{
		{Label: "alpha", Checked: true},
		{Label: "beta", Checked: false},
		{Label: "gamma", Checked: true},
	}
	d := newChecklistDialog("test", "Pick:", items)

	_, cmd := d.UpdateModel(testKeyCode(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("enter should produce a command")
	}
	msg := cmd()
	result, ok := msg.(dialogResultMsg)
	if !ok {
		t.Fatalf("expected dialogResultMsg, got %T", msg)
	}
	if !result.Confirmed {
		t.Fatal("enter should set confirmed=true")
	}
	if len(result.Values) != 2 || result.Values[0] != "alpha" || result.Values[1] != "gamma" {
		t.Fatalf("expected [alpha gamma], got %v", result.Values)
	}
}

func TestChecklistDialog_EscCancels(t *testing.T) {
	t.Parallel()
	items := []checkItem{
		{Label: "alpha", Checked: true},
	}
	d := newChecklistDialog("test", "Pick:", items)

	_, cmd := d.UpdateModel(testKeyCode(tea.KeyEsc))
	if cmd == nil {
		t.Fatal("esc should produce a command")
	}
	msg := cmd()
	result, ok := msg.(dialogResultMsg)
	if !ok {
		t.Fatalf("expected dialogResultMsg, got %T", msg)
	}
	if result.Confirmed {
		t.Fatal("esc should set confirmed=false")
	}
}

func TestChecklistDialog_EmptyConfirmation(t *testing.T) {
	t.Parallel()
	items := []checkItem{
		{Label: "alpha", Checked: false},
		{Label: "beta", Checked: false},
	}
	d := newChecklistDialog("test", "Pick:", items)

	_, cmd := d.UpdateModel(testKeyCode(tea.KeyEnter))
	msg := cmd()
	result := msg.(dialogResultMsg)
	if len(result.Values) != 0 {
		t.Fatalf("expected empty values when nothing checked, got %v", result.Values)
	}
}

func TestChecklistDialog_Navigation(t *testing.T) {
	t.Parallel()
	items := []checkItem{
		{Label: "alpha"},
		{Label: "beta"},
		{Label: "gamma"},
	}
	d := newChecklistDialog("test", "Pick:", items)

	// Start at 0, move down.
	d, _ = d.UpdateModel(testKeyText("j"))
	if d.ListCursor != 1 {
		t.Fatalf("j should move to 1, got %d", d.ListCursor)
	}
	d, _ = d.UpdateModel(testKeyText("j"))
	if d.ListCursor != 2 {
		t.Fatalf("j should move to 2, got %d", d.ListCursor)
	}
	// At bottom, j doesn't go past end.
	d, _ = d.UpdateModel(testKeyText("j"))
	if d.ListCursor != 2 {
		t.Fatalf("j at bottom should stay at 2, got %d", d.ListCursor)
	}
	// Move up.
	d, _ = d.UpdateModel(testKeyText("k"))
	if d.ListCursor != 1 {
		t.Fatalf("k should move to 1, got %d", d.ListCursor)
	}
}

func TestChecklistDialog_HelpText(t *testing.T) {
	t.Parallel()
	d := newChecklistDialog("test", "Pick:", nil)
	help := d.HelpText()
	if help != "space:toggle  enter:confirm  esc:cancel" {
		t.Fatalf("unexpected help text: %q", help)
	}
}

func TestBundledCandidatesDialog_ConfirmRehydratesApprovedContent(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcDir, "extras"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "extras", "helper.sh"), []byte("#!/bin/sh\necho hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := config.PackManifest{
		SchemaVersion: 2,
		Name:          "copy-pack",
		Version:       "1.0.0",
		Root:          ".",
		Extras:        []string{"extras"},
	}
	if err := config.SavePackManifest(filepath.Join(srcDir, "pack.json"), manifest); err != nil {
		t.Fatal(err)
	}

	configDir := t.TempDir()
	if err := config.SaveSyncConfig(config.SyncConfigPath(configDir), config.SyncConfig{
		SchemaVersion: config.SyncConfigSchemaVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.PackInstall(context.Background(), app.PackInstallRequest{
		PackPath:  srcDir,
		ConfigDir: configDir,
		Add:       false,
		With:      domain.NewBundledSet(),
	}, os.Stdout); err != nil {
		t.Fatalf("PackInstall: %v", err)
	}

	packDir := filepath.Join(configDir, "packs", "copy-pack")
	if _, err := os.Stat(filepath.Join(packDir, "extras", "helper.sh")); !os.IsNotExist(err) {
		t.Fatalf("extras should be absent before approval, got err=%v", err)
	}

	m := newRootModel(context.Background(), RunConfig{ConfigDir: configDir})
	packMsg := drivePackUpdate(t, context.Background(), configDir, "copy-pack", false, "", nil)
	if len(packMsg.Results) != 1 || packMsg.Results[0].BundledCandidates == nil {
		t.Fatalf("expected bundled candidates, got %+v", packMsg.Results)
	}

	result, _ := m.Update(packMsg)
	rm := result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogBundledCandidates {
		t.Fatalf("expected bundled candidates dialog, got %+v", rm.dialog)
	}

	result, cmd := rm.handleDialogResult(dialogResultMsg{
		ID:        dialogBundledCandidates,
		Confirmed: true,
		Values:    []string{string(domain.BundledExtras)},
	})
	_ = result.(rootModel)
	if cmd == nil {
		t.Fatal("expected follow-up command after confirming bundled candidates")
	}
	_ = cmd()

	got, err := os.ReadFile(filepath.Join(packDir, "extras", "helper.sh"))
	if err != nil {
		t.Fatalf("expected approved extras to be restored: %v", err)
	}
	if string(got) != "#!/bin/sh\necho hi\n" {
		t.Fatalf("restored extras = %q", string(got))
	}
}

func TestPromptSync_ShowsDialog(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m, profilescreen.Seed{Name: "test", SyncStatus: common.SyncStatusSynced})

	result, _ := m.promptSync()
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected sync dialog")
	}
	if rm.dialog.ID != dialogSyncOnExit {
		t.Fatalf("expected sync-on-exit dialog, got %q", rm.dialog.ID)
	}
}

func TestPromptSync_UnsyncedProfileShowsPendingCount(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m, profilescreen.Seed{
		Name:       "default",
		SyncStatus: common.SyncStatusUnsynced,
		SyncTarget: common.SyncTarget{
			PlanSummary: app.PlanSummary{NumRules: 2, NumSkills: 1},
			Harnesses:   []string{"cline"},
			Scope:       "project",
		},
	})

	result, _ := m.promptSync()
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected sync dialog for unsynced profile")
	}
	if rm.dialog.ID != dialogSyncOnExit {
		t.Fatalf("expected sync-on-exit dialog, got %q", rm.dialog.ID)
	}
	if len(rm.dialog.ListItems) != 3 {
		t.Fatalf("expected 3 options, got %d", len(rm.dialog.ListItems))
	}
	// First option should contain harness/scope info.
	if !strings.Contains(rm.dialog.ListItems[0], "cline") {
		t.Fatalf("expected first option to mention cline, got %q", rm.dialog.ListItems[0])
	}
	if rm.dialog.ListItems[1] != "Customize..." {
		t.Fatalf("expected second option to be 'Customize...', got %q", rm.dialog.ListItems[1])
	}
	if rm.dialog.ListItems[2] != "Cancel" {
		t.Fatalf("expected third option to be 'Cancel', got %q", rm.dialog.ListItems[2])
	}
	// Title should mention pending changes.
	if !strings.Contains(rm.dialog.Title, "3 pending") {
		t.Fatalf("expected title to mention pending changes, got %q", rm.dialog.Title)
	}
}

func TestSyncOnExitDialog_CancelReturnsToTUI(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.pendingExit = true
	m = seedProfiles(m, profilescreen.Seed{Name: "test", SyncStatus: common.SyncStatusUnsynced})

	result, cmd := m.handleDialogResult(dialogResultMsg{
		ID: dialogSyncOnExit, Confirmed: true, Value: "Cancel",
	})
	rm := result.(rootModel)
	if rm.quitting {
		t.Fatal("expected quitting=false after cancel")
	}
	if rm.pendingExit {
		t.Fatal("expected pendingExit=false after cancel")
	}
	if cmd != nil {
		t.Fatal("expected no cmd after cancel")
	}
	if rm.runResult.SyncRequested {
		t.Fatal("expected SyncRequested=false after cancel")
	}
}

func TestSyncOnExitDialog_DefaultSyncFiresCmd(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m, profilescreen.Seed{
		Name: "default",
		Path: "/tmp/default.yaml",
		SyncTarget: common.SyncTarget{
			Harnesses: []string{"cline"},
			Scope:     "project",
		},
	})

	result, cmd := m.handleDialogResult(dialogResultMsg{
		ID: dialogSyncOnExit, Confirmed: true, Value: "Sync (cline, project)",
	})
	rm := result.(rootModel)
	if rm.quitting {
		t.Fatal("expected quitting=false — sync runs inline")
	}
	if cmd == nil {
		t.Fatal("expected async sync cmd")
	}
	// Sync status should be loading.
	if item, _ := rm.profilesScreen().CurrentProfile(); item.SyncStatus != common.SyncStatusLoading {
		t.Fatalf("expected syncState=common.SyncStatusLoading, got %d", item.SyncStatus)
	}
}

func TestSyncOnExitDialog_CustomizeChainsScopeDialog(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	result, _ := m.handleDialogResult(dialogResultMsg{
		ID: dialogSyncOnExit, Confirmed: true, Value: "Customize...",
	})
	rm := result.(rootModel)
	if rm.quitting {
		t.Fatal("expected not quitting after Customize")
	}
	if rm.dialog == nil {
		t.Fatal("expected sync-scope dialog")
	}
	if rm.dialog.ID != dialogSyncScope {
		t.Fatalf("expected sync-scope dialog, got %q", rm.dialog.ID)
	}
}

func TestSyncScopeDialog_ChainsHarnessDialog(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	result, _ := m.handleDialogResult(dialogResultMsg{
		ID: dialogSyncScope, Confirmed: true, Value: "project",
	})
	rm := result.(rootModel)
	if rm.exitSyncScope != "project" {
		t.Fatalf("expected exitSyncScope=project, got %q", rm.exitSyncScope)
	}
	if rm.dialog == nil {
		t.Fatal("expected sync-harness dialog")
	}
	if rm.dialog.ID != dialogSyncHarness {
		t.Fatalf("expected sync-harness dialog, got %q", rm.dialog.ID)
	}
}

func TestSyncHarnessDialog_FiresSyncCmd(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.exitSyncScope = "global"
	m = seedProfiles(m, profilescreen.Seed{Name: "custom", Path: "/tmp/custom.yaml"})

	result, cmd := m.handleDialogResult(dialogResultMsg{
		ID: dialogSyncHarness, Confirmed: true, Value: "opencode",
	})
	rm := result.(rootModel)
	if rm.quitting {
		t.Fatal("expected quitting=false — sync runs inline")
	}
	if cmd == nil {
		t.Fatal("expected async sync cmd")
	}
	if item, _ := rm.profilesScreen().CurrentProfile(); item.SyncStatus != common.SyncStatusLoading {
		t.Fatalf("expected syncState=common.SyncStatusLoading, got %d", item.SyncStatus)
	}
}

func TestProfileSavedMsg_MarksUnsynced(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m, profilescreen.Seed{Name: "test", Path: "/tmp/test.yaml", SyncStatus: common.SyncStatusSynced})

	result, _ := m.Update(profilescreen.SavedMsg{ProfileName: "test"})
	rm := result.(rootModel)
	if item, _ := rm.profilesScreen().CurrentProfile(); item.SyncStatus != common.SyncStatusUnsynced {
		t.Fatalf("expected syncState=common.SyncStatusUnsynced after save, got %d", item.SyncStatus)
	}
}

func TestPendingExitFlow_AutoSaveThenQuit(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m.dirty = true
	m = seedProfiles(m, profilescreen.Seed{
		Name:       "test",
		Path:       "/tmp/test.yaml",
		SyncStatus: common.SyncStatusSynced,
		Config:     config.ProfileConfig{Packs: []config.PackEntry{{Name: "test-pack"}}},
		Dirty:      true,
	})

	// startExit with dirty state → auto-save + pending exit.
	result, cmd := m.startExit()
	rm := result.(rootModel)
	if !rm.pendingExit {
		t.Fatal("expected pendingExit=true after startExit with dirty state")
	}
	if cmd == nil {
		t.Fatal("expected save command")
	}

	// Simulate profileSavedMsg arriving → should quit (no sync prompt on exit).
	result2, cmd2 := rm.Update(profilescreen.SavedMsg{ProfileName: "test"})
	rm2 := result2.(rootModel)
	if !rm2.quitting {
		t.Fatal("expected quitting=true after save completes during exit")
	}
	if cmd2 == nil {
		t.Fatal("expected tea.Quit cmd")
	}
}

func TestSyncFlow_SaveThenPrompt(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m.dirty = true
	m = seedProfiles(m, profilescreen.Seed{
		Name:       "test",
		Path:       "/tmp/test.yaml",
		SyncStatus: common.SyncStatusUnsynced,
		Config:     config.ProfileConfig{Packs: []config.PackEntry{{Name: "test-pack"}}},
		Dirty:      true,
		SyncTarget: common.SyncTarget{
			PlanSummary: app.PlanSummary{NumRules: 3},
			Harnesses:   []string{"cline"},
			Scope:       "project",
		},
	})

	// startSync with dirty state → auto-save + pending sync.
	result, cmd := m.startSync()
	rm := result.(rootModel)
	if !rm.pendingExit {
		t.Fatal("expected pendingExit=true")
	}
	if !rm.pendingSync {
		t.Fatal("expected pendingSync=true")
	}
	if cmd == nil {
		t.Fatal("expected save command")
	}

	// Simulate profileSavedMsg → should show sync prompt (not quit).
	result2, _ := rm.Update(profilescreen.SavedMsg{ProfileName: "test"})
	rm2 := result2.(rootModel)
	if rm2.quitting {
		t.Fatal("expected quitting=false, should show sync dialog")
	}
	if rm2.dialog == nil {
		t.Fatal("expected sync dialog after save")
	}
	if rm2.dialog.ID != dialogSyncOnExit {
		t.Fatalf("expected sync-on-exit dialog, got %q", rm2.dialog.ID)
	}
}

func TestEnrichedSyncStatus(t *testing.T) {
	t.Parallel()
	m := profilescreen.New(context.Background(), nil, "").
		WithProfiles([]profilescreen.Seed{{Name: "test"}})

	target := common.SyncTarget{
		PlanSummary: app.PlanSummary{NumRules: 5, NumSkills: 3, NumSettings: 2},
		Harnesses:   []string{"cline", "opencode"},
		Scope:       "project",
		ProjectDir:  "/tmp/project",
	}

	screen, _ := m.Update(profilescreen.SyncStatusMsg{
		ProfileName: "test",
		Synced:      false,
		Target:      target,
	})
	m = screen.(profilescreen.Model)

	item, ok := m.CurrentProfile()
	if !ok {
		t.Fatal("expected current profile")
	}
	if item.SyncStatus != common.SyncStatusUnsynced {
		t.Fatalf("expected common.SyncStatusUnsynced, got %d", item.SyncStatus)
	}
	if item.SyncTarget.TotalChanges() != 10 {
		t.Fatalf("expected 10 total changes, got %d", item.SyncTarget.TotalChanges())
	}
	if len(item.SyncTarget.Harnesses) != 2 {
		t.Fatalf("expected 2 harnesses, got %d", len(item.SyncTarget.Harnesses))
	}
}

func TestCountPendingSaves(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m,
		profilescreen.Seed{Name: "a", Dirty: true},
		profilescreen.Seed{Name: "b"},
		profilescreen.Seed{Name: "c", Dirty: true},
	)

	count := m.countPendingSaves()
	if count != 2 {
		t.Fatalf("expected 2 pending saves, got %d", count)
	}
}

func TestRunResult_DefaultValues(t *testing.T) {
	t.Parallel()
	r := RunResult{}
	if r.SyncRequested {
		t.Fatal("expected SyncRequested=false by default")
	}
	if r.ProfileName != "" {
		t.Fatal("expected empty ProfileName by default")
	}
}

func TestActionMenu_ProfileActions(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m,
		profilescreen.Seed{Name: "default", IsActive: true},
		profilescreen.Seed{Name: "staging"},
	)
	m = m.setProfilesScreen(m.profilesScreen().SetCursor(0))

	// Press "." on profiles tab → should open action menu.
	result, _ := m.Update(testKeyText("."))
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected action dialog")
	}
	if rm.dialog.ID != dialogActionProfile {
		t.Fatalf("expected %s dialog, got %q", dialogActionProfile, rm.dialog.ID)
	}
	// Active profile should not have "Activate" option.
	for _, item := range rm.dialog.ListItems {
		if item == actActivate {
			t.Fatal("expected no 'Activate' option for already-active profile")
		}
	}
}

func TestActionMenu_ProfileActionsInactive(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m,
		profilescreen.Seed{Name: "default", IsActive: true},
		profilescreen.Seed{Name: "staging"},
	)
	m = m.setProfilesScreen(m.profilesScreen().SetCursor(1))

	result, _ := m.Update(testKeyText("."))
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected action dialog")
	}
	// Inactive profile should have "Activate" option.
	found := false
	for _, item := range rm.dialog.ListItems {
		if item == actActivate {
			found = true
		}
	}
	if !found {
		t.Fatal("expected 'Activate' option for inactive profile")
	}
}

func TestProfileActionRequestOpensActionMenu(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m, profilescreen.Seed{Name: "default", IsActive: true})

	result, _ := m.Update(profilescreen.ActionRequestMsg{})
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected action dialog")
	}
	if rm.dialog.ID != dialogActionProfile {
		t.Fatalf("expected %s dialog, got %q", dialogActionProfile, rm.dialog.ID)
	}
}

func TestBackChromeRoutesProfileSubscreenBack(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m.width = 120
	m.height = 40
	m = seedProfiles(m, profilescreen.Seed{Name: "default", IsActive: true})
	m = m.setProfilesScreen(m.profilesScreen().SetFocus(profilescreen.FocusTree))

	view := m.View()
	if view.OnMouse == nil {
		t.Fatal("expected mouse handler")
	}
	cmd := view.OnMouse(tea.MouseClickMsg(tea.Mouse{X: 1, Y: m.height - 1, Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("expected back layer hit command")
	}
	msg := cmd()
	hit, ok := msg.(common.LayerHitMsg)
	if !ok {
		t.Fatalf("expected LayerHitMsg, got %T", msg)
	}
	if hit.ID != "chrome:back" {
		t.Fatalf("expected chrome:back hit, got %q", hit.ID)
	}

	result, _ := m.Update(common.LayerHitMsg{
		ID:    "chrome:back",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	rm := result.(rootModel)
	if rm.profilesScreen().Focus() != profilescreen.FocusPacks {
		t.Fatalf("expected Back to route like esc to pack focus, got %v", rm.profilesScreen().Focus())
	}
}

func TestBackChromeCancelsDialog(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m.width = 120
	m.height = 40
	m = seedProfiles(m, profilescreen.Seed{Name: "default", IsActive: true})
	m = m.withDialog(newListSelectDialog(dialogActionProfile, "Profile actions:", []string{actNewProfile}))

	result, cmd := m.Update(common.LayerHitMsg{
		ID:    "chrome:back",
		Mouse: tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft}),
	})
	rm := result.(rootModel)
	if cmd == nil {
		t.Fatal("expected Back to emit dialog cancel command")
	}
	result, _ = rm.Update(cmd())
	rm = result.(rootModel)
	if rm.dialog != nil {
		t.Fatal("expected Back to cancel dialog")
	}
}

func TestActionMenu_NewProfileChain(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m, profilescreen.Seed{Name: "default"})

	// Simulate selecting "New profile" from the action menu.
	result, _ := m.handleDialogResult(dialogResultMsg{
		ID: dialogActionProfile, Confirmed: true, Value: actNewProfile,
	})
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected new-profile text input dialog")
	}
	if rm.dialog.ID != dialogNewProfile {
		t.Fatalf("expected %s dialog, got %q", dialogNewProfile, rm.dialog.ID)
	}
}

func TestConfigTab_EditParamsChain(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m, profilescreen.Seed{
		Name: "default",
		Config: config.ProfileConfig{
			Params: map[string]string{"workspace": "default"},
		},
	})

	result, _ := m.Update(configscreen.EditParamMsg{Key: "workspace"})
	rm := result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogProfileParamEdit {
		t.Fatalf("expected param edit dialog, got %+v", rm.dialog)
	}
	if rm.dialog.TextValue != "default" {
		t.Fatalf("textValue = %q", rm.dialog.TextValue)
	}
}

func TestDialogProfileParamEditMarksDirty(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m, profilescreen.Seed{
		Name:   "default",
		Config: config.ProfileConfig{Params: map[string]string{"workspace": "default"}},
	})
	m.pendingProfileParamKey = "workspace"

	result, cmd := m.handleDialogResult(dialogResultMsg{
		ID: dialogProfileParamEdit, Confirmed: true, Value: "team-alpha",
	})
	rm := result.(rootModel)
	if cmd == nil {
		t.Fatal("expected save command")
	}
	item, ok := rm.profilesScreen().CurrentProfile()
	if !ok {
		t.Fatal("expected current profile")
	}
	if got := item.Config.Params["workspace"]; got != "team-alpha" {
		t.Fatalf("workspace = %q", got)
	}
	if !rm.dirty || !item.Dirty {
		t.Fatal("expected root/profile dirty after param edit")
	}
}

func TestActionMenu_DeleteChain(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m, profilescreen.Seed{Name: "staging", Path: "/tmp/staging.yaml"})

	result, _ := m.handleDialogResult(dialogResultMsg{
		ID: dialogActionProfile, Confirmed: true, Value: actDelete,
	})
	rm := result.(rootModel)
	if rm.dialog == nil {
		t.Fatal("expected delete confirm dialog")
	}
	if rm.dialog.ID != dialogDeleteProfile {
		t.Fatalf("expected %s dialog, got %q", dialogDeleteProfile, rm.dialog.ID)
	}
}

func TestSyncErrorDisplay(t *testing.T) {
	t.Parallel()
	// Sync error is now displayed on the Sync tab, not the profiles view.
	m := syncscreen.New("").WithContext(common.SyncSnapshot{
		Status:  common.SyncStatusError,
		ErrText: "harness not found",
	})
	m = m.SetSize(120, 40).(syncscreen.Model)

	view := lipgloss.NewCompositor(m.View()).Render()
	if !strings.Contains(view, "error") {
		t.Fatalf("expected view to contain 'error', got:\n%s", view)
	}
	if !strings.Contains(view, "harness not found") {
		t.Fatalf("expected view to contain error text 'harness not found', got:\n%s", view)
	}
}

func TestDeleteCancel_ClosesDialog(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m, profilescreen.Seed{Name: "default", Path: "/tmp/default.yaml"})

	result, _ := m.handleDialogResult(dialogResultMsg{
		ID: dialogDeleteProfile, Confirmed: false,
	})
	rm := result.(rootModel)

	if rm.dialog != nil {
		t.Fatal("expected no dialog after cancel")
	}
}

func TestNewCancel_ClosesDialog(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	result, _ := m.handleDialogResult(dialogResultMsg{
		ID: dialogNewProfile, Confirmed: false,
	})
	rm := result.(rootModel)

	if rm.dialog != nil {
		t.Fatal("expected no dialog after cancel")
	}
}

func TestRootModel_PreviewRequestOpensOverlay(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.width = 120
	m.height = 40

	result, cmd := m.Update(common.PreviewRequestMsg{
		Title:    "rule-a",
		Category: domain.CategoryRules,
		PackName: "test-pack",
		FilePath: "/tmp/pack/rules/rule-a.md",
	})
	rm := result.(rootModel)
	if rm.preview == nil {
		t.Fatal("expected preview to be non-nil")
	}
	if cmd == nil {
		t.Fatal("expected loadPreview cmd")
	}
}

func TestRootModel_PreviewLoadedMsgUpdatesPacksInlinePreview(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabPacks
	m = m.setPacksScreen(
		packsscreen.New(m.cfg.ConfigDir).
			WithInstalled([]app.PackShowEntry{{
				Name:  "test-pack",
				Path:  "/tmp/pack",
				Rules: []string{"rule-a"},
			}}).
			PrimeInlinePreview("/tmp/pack/rules/rule-a.md"),
	)

	result, _ := m.Update(previewLoadedMsg{
		Title:    "rule-a",
		Category: domain.CategoryRules,
		PackName: "test-pack",
		FilePath: "/tmp/pack/rules/rule-a.md",
		Body:     "# Inline",
	})
	rm := result.(rootModel)
	packs := rm.packsScreen()
	if !packs.PreviewLoaded() {
		t.Fatal("expected inline preview state loaded")
	}
	if packs.PreviewBody() != "# Inline" {
		t.Fatalf("expected inline preview body to be updated, got %q", packs.PreviewBody())
	}
}

func TestRootModel_EscClosesPreview(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.width = 120
	m.height = 40
	p := newPreviewModel(120, 40)
	p = p.SetContent(previewLoadedMsg{
		Title:    "rule-a",
		Category: domain.CategoryRules,
		FilePath: "/tmp/pack/rules/rule-a.md",
		Body:     "test body",
	})
	m.preview = &p

	result, _ := m.Update(testKeyCode(tea.KeyEsc))
	rm := result.(rootModel)
	if rm.preview != nil {
		t.Fatal("expected preview to be nil after esc")
	}
	if rm.quitting {
		t.Fatal("expected quitting=false — esc from preview should close preview, not quit")
	}
}

func TestRootModel_QClosesPreview(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.width = 120
	m.height = 40
	p := newPreviewModel(120, 40)
	p = p.SetContent(previewLoadedMsg{
		Title: "rule-a", Category: domain.CategoryRules,
		FilePath: "/tmp/pack/rules/rule-a.md", Body: "test",
	})
	m.preview = &p

	result, _ := m.Update(testKeyText("q"))
	rm := result.(rootModel)
	if rm.preview != nil {
		t.Fatal("expected preview to be nil after q")
	}
	if rm.quitting {
		t.Fatal("expected quitting=false — q from preview should close preview, not quit")
	}
}

func TestRootModel_PreviewViewTakesOver(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.width = 80
	m.height = 40
	p := newPreviewModel(80, 40)
	p = p.SetContent(previewLoadedMsg{
		Title:    "rule-a",
		Category: domain.CategoryRules,
		FilePath: "/tmp/pack/rules/rule-a.md",
		Body:     "# Hello World",
	})
	m.preview = &p

	view := m.View().Content
	// Preview should take over — no tab bar.
	if strings.Contains(view, "Profiles") {
		t.Fatal("expected preview to replace tab bar")
	}
	if !strings.Contains(view, "Hello World") {
		t.Fatal("expected preview body in view")
	}
	if !strings.Contains(view, "e:edit") {
		t.Fatal("expected preview help text in view")
	}
}

func TestHelpText_VPlanOnProfilesAndSync(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	m.activeTab = tabProfiles
	help := m.helpText()
	if !strings.Contains(help, "v:plan") {
		t.Fatalf("expected profiles help to contain 'v:plan', got %q", help)
	}

	m.activeTab = tabSync
	help = m.helpText()
	if !strings.Contains(help, "v:plan") {
		t.Fatalf("expected sync help to contain 'v:plan', got %q", help)
	}
	if !strings.Contains(help, ".:actions") {
		t.Fatalf("expected sync help to contain '.:actions', got %q", help)
	}
	if strings.Contains(help, "p:prune") {
		t.Fatalf("expected sync help to omit 'p:prune', got %q", help)
	}
	m.activeTab = tabPacks
	help = m.helpText()
	if strings.Contains(help, "v:plan") {
		t.Fatalf("expected packs help NOT to contain 'v:plan', got %q", help)
	}
}

func TestSyncActionMenuOffersExpectedActions(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabSync
	m = seedProfiles(m, profilescreen.Seed{Name: "work", IsActive: true})

	result, _ := m.Update(testKeyText("."))
	rm := result.(rootModel)
	if rm.dialog == nil || rm.dialog.ID != dialogActionSync {
		t.Fatalf("expected sync action dialog, got %+v", rm.dialog)
	}
	want := []string{"View plan", "Sync now", "Open sync-config", "Edit sync-config"}
	if got := rm.dialog.ListItems; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sync actions = %v, want %v", got, want)
	}
}

func TestSyncActionMenuViewPlanOpensPlanOverlay(t *testing.T) {
	t.Parallel()

	m := newRootModel(context.Background(), RunConfig{})
	m.activeTab = tabSync
	m.width = 120
	m.height = 40
	m = seedProfiles(m, profilescreen.Seed{
		Name:     "work",
		IsActive: true,
		SyncTarget: common.SyncTarget{
			PlanSummary: app.PlanSummary{
				Ops: []app.PlanOp{{
					Kind:       app.PlanOpRule,
					DiffKind:   domain.DiffCreate,
					SourcePack: "core",
					Dst:        "/tmp/rule.md",
					Content:    []byte("rule body"),
				}},
			},
		},
	})

	result, _ := m.handleDialogResult(dialogResultMsg{
		ID:        dialogActionSync,
		Confirmed: true,
		Value:     "View plan",
	})
	rm := result.(rootModel)
	if rm.planView == nil {
		t.Fatal("expected sync action to open plan overlay")
	}
}

func TestNumberKeys_DirectTabSwitch(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	// Start on Profiles (tab 0), press "3" to jump to Sync (tab 2).
	result, _ := m.Update(testKeyText("3"))
	rm := result.(rootModel)
	if rm.activeTab != tabSync {
		t.Fatalf("expected tab 2 (sync) after pressing 3, got %d", rm.activeTab)
	}

	// Press "4" to jump to Save.
	result, _ = rm.Update(testKeyText("4"))
	rm = result.(rootModel)
	if rm.activeTab != tabSave {
		t.Fatalf("expected tab 3 (save) after pressing 4, got %d", rm.activeTab)
	}

	// Press "1" to jump back to Profiles.
	result, _ = rm.Update(testKeyText("1"))
	rm = result.(rootModel)
	if rm.activeTab != tabProfiles {
		t.Fatalf("expected tab 0 (profiles) after pressing 1, got %d", rm.activeTab)
	}

	// Press "5" to jump to Search.
	result, _ = rm.Update(testKeyText("5"))
	rm = result.(rootModel)
	if rm.activeTab != tabSearch {
		t.Fatalf("expected tab 4 (search) after pressing 5, got %d", rm.activeTab)
	}

	// Press "6" to jump to Config.
	result, _ = rm.Update(testKeyText("6"))
	rm = result.(rootModel)
	if rm.activeTab != tabConfig {
		t.Fatalf("expected tab 5 (config) after pressing 6, got %d", rm.activeTab)
	}

	// Pressing same number is a no-op.
	result, cmd := rm.Update(testKeyText("6"))
	rm = result.(rootModel)
	if rm.activeTab != tabConfig {
		t.Fatalf("expected tab unchanged at config, got %d", rm.activeTab)
	}
	if cmd != nil {
		t.Fatal("expected no cmd when pressing current tab number")
	}
}

func TestDestructiveConfirmDialog_DefaultsToNo(t *testing.T) {
	t.Parallel()
	d := newDestructiveConfirmDialog("test", "Delete everything?")
	if d.Focused != 1 {
		t.Fatalf("expected destructive dialog focused=1 (No), got %d", d.Focused)
	}

	// Pressing enter without moving focus should NOT confirm.
	_, cmd := d.UpdateModel(testKeyCode(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected cmd after pressing enter")
	}
	msg := cmd()
	result, ok := msg.(dialogResultMsg)
	if !ok {
		t.Fatalf("expected dialogResultMsg, got %T", msg)
	}
	if result.Confirmed {
		t.Fatal("expected confirmed=false when defaulting to No")
	}
}

func TestStatusClearMsg_AutoClearsStatus(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.statusText = "some status"
	m.statusID = 5

	// Matching ID clears the status.
	result, _ := m.Update(statusClearMsg{id: 5})
	rm := result.(rootModel)
	if rm.statusText != "" {
		t.Fatalf("expected status cleared, got %q", rm.statusText)
	}

	// Non-matching ID is ignored.
	rm.statusText = "another status"
	rm.statusID = 10
	result, _ = rm.Update(statusClearMsg{id: 7})
	rm = result.(rootModel)
	if rm.statusText != "another status" {
		t.Fatalf("expected status unchanged for stale ID, got %q", rm.statusText)
	}
}

func TestStatusLine_ShowsActiveProfile(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m.width = 100
	m = seedProfiles(m, profilescreen.Seed{
		Name:       "default",
		IsActive:   true,
		SyncStatus: common.SyncStatusSynced,
		Config:     config.ProfileConfig{Packs: []config.PackEntry{{Name: "a"}, {Name: "b"}}},
	})

	line := m.statusLine()
	if !strings.Contains(line, "default") {
		t.Fatalf("expected status line to contain profile name, got %q", line)
	}
	if !strings.Contains(line, "2 packs") {
		t.Fatalf("expected status line to show pack count, got %q", line)
	}
}

func TestHelpText_ContainsNumberKeys(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	m.activeTab = tabProfiles
	help := m.helpText()
	if !strings.Contains(help, "1-6") {
		t.Fatalf("expected profiles help to contain '1-6', got %q", help)
	}

	m.activeTab = tabSync
	help = m.helpText()
	if !strings.Contains(help, "1-6") {
		t.Fatalf("expected sync help to contain '1-6', got %q", help)
	}
}

func TestHelpText_GroupSeparators(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})

	m.activeTab = tabProfiles
	help := m.helpText()
	if !strings.Contains(help, "│") {
		t.Fatalf("expected profiles help to contain separator │, got %q", help)
	}
}

func TestUpdate_StatusChangeSchedulesClear(t *testing.T) {
	t.Parallel()
	m := newRootModel(context.Background(), RunConfig{})
	m = seedProfiles(m, profilescreen.Seed{Name: "test", SyncStatus: common.SyncStatusSynced})

	// Trigger a status-setting message (profileCreatedMsg with error).
	result, cmd := m.Update(profilescreen.CreatedMsg{Name: "x", Err: fmt.Errorf("fail")})
	rm := result.(rootModel)
	if rm.statusText == "" {
		t.Fatal("expected status text to be set on error")
	}
	if rm.statusID == 0 {
		t.Fatal("expected statusID to be incremented")
	}
	if cmd == nil {
		t.Fatal("expected timer cmd to be batched")
	}
}

// drivePackUpdate runs a streaming pack update to completion and returns the
// terminal packUpdatedMsg. updatePack returns channels first so tests drain the
// same stream the Bubble Tea loop reads.
func drivePackUpdate(t *testing.T, ctx context.Context, configDir, name string, all bool, ref string, with domain.BundledSet) packUpdatedMsg {
	t.Helper()
	msg := updatePack(ctx, configDir, name, all, ref, with)()
	ready, ok := msg.(packUpdateReadyMsg)
	if !ok {
		t.Fatalf("expected packUpdateReadyMsg, got %T", msg)
	}
	for {
		next := readNextEvent(ready.EventCh, ready.ResultCh, ready.PackName)()
		switch n := next.(type) {
		case packProgressMsg:
			continue
		case packUpdatedMsg:
			return n
		default:
			t.Fatalf("unexpected msg from readNextEvent: %T", n)
		}
	}
}

var tuiTestSGRPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripSGRForTUITest(s string) string {
	return tuiTestSGRPattern.ReplaceAllString(s, "")
}

func findRenderedTextInTUIView(t *testing.T, lines []string, text string) (int, int) {
	t.Helper()
	for y, line := range lines {
		if x := strings.Index(line, text); x >= 0 {
			return x, y
		}
	}
	t.Fatalf("could not find %q in render:\n%s", text, strings.Join(lines, "\n"))
	return 0, 0
}
