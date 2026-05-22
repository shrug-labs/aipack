package packs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/cmd/aipack/tui/common"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/source"
)

func Load(configDir string) tea.Cmd {
	return func() tea.Msg {
		entries, err := app.PackListDetailed(configDir)
		if err != nil {
			return LoadedMsg{Err: err}
		}
		return LoadedMsg{Items: entries}
	}
}

func LoadRegistry(configDir string) tea.Cmd {
	return func() tea.Msg {
		results, err := app.RegistryList(app.RegistryListRequest{
			ConfigDir: configDir,
		})
		if err != nil {
			return RegistryLoadedMsg{Err: err}
		}
		items := make([]RegistryItem, 0, len(results))
		for _, r := range results {
			items = append(items, RegistryItem{
				Name:        r.Name,
				Description: r.Description,
				Repo:        r.Repo,
				Path:        r.Path,
				Ref:         r.Ref,
				Owner:       r.Owner,
			})
		}
		return RegistryLoadedMsg{Items: items}
	}
}

func LoadIndexDetails(configDir string) tea.Cmd {
	return func() tea.Msg {
		items, err := app.PackIndexDetails(configDir)
		if err != nil {
			return IndexLoadedMsg{Err: err}
		}
		return IndexLoadedMsg{Items: items}
	}
}

func ComputeSizes(entry app.PackShowEntry) tea.Cmd {
	return func() tea.Msg {
		sizes := map[string]int64{}
		var total int64
		for _, cat := range packTabCategories() {
			for _, id := range entry.ContentIDs(cat) {
				sz := entry.ContentSize(cat, id)
				sizes[cat.DirName()+"/"+id] = sz
				if sz > 0 {
					total += sz
				}
			}
		}
		sizes["total"] = total
		return SizesMsg{PackName: entry.Name, Sizes: sizes}
	}
}

func Create(configDir, name string) tea.Cmd {
	return func() tea.Msg {
		if err := app.PackCreate(app.PackCreateRequest{
			Name:      name,
			ConfigDir: configDir,
			Local:     true,
		}); err != nil {
			return CreatedMsg{Name: name, Err: err}
		}
		return CreatedMsg{Name: name}
	}
}

func Remove(configDir, name string, registry *harness.Registry) tea.Cmd {
	return func() tea.Msg {
		result, err := app.PackDeleteWithOptions(nil, app.PackDeleteRequest{
			ConfigDir: configDir,
			Name:      name,
			Registry:  registry,
		}, io.Discard)
		if err != nil {
			return RemovedMsg{Name: name, Err: err}
		}
		if len(result.BundledProfiles) > 0 {
			app.RemoveBundledProfiles(configDir, result.BundledProfiles, io.Discard)
		}
		return RemovedMsg{Name: name}
	}
}

func LoadDrift(ctx context.Context, configDir string) tea.Cmd {
	return func() tea.Msg {
		lf, err := config.EnsureLockfileMigrated(configDir)
		if err != nil {
			return DriftLoadedMsg{Err: err}
		}
		drifted := app.DetectPackDrift(ctx, configDir, lf.Packs, nil)
		return DriftLoadedMsg{Drifted: drifted}
	}
}

func LoadVersions(ctx context.Context, configDir, name string) tea.Cmd {
	return func() tea.Msg {
		tctx, cancel := context.WithTimeout(ctx, tuiVersionLoadTimeout)
		defer cancel()
		result, err := app.PackListVersions(tctx, app.PackListVersionsRequest{
			ConfigDir: configDir,
			Name:      name,
		})
		if err != nil {
			return VersionsLoadedMsg{PackName: name, Err: err}
		}
		return VersionsLoadedMsg{PackName: name, Versions: result.Versions}
	}
}

const tuiVersionLoadTimeout = 5 * time.Second

func Install(ctx context.Context, configDir, input, ref string, with domain.BundledSet) tea.Cmd {
	return func() tea.Msg {
		req := app.PackInstallRequest{
			ConfigDir: configDir,
			With:      with,
			Ref:       ref,
		}
		if source.IsRemoteInstallInput(input) {
			req.URL = input
		} else if isRegistryName(input) {
			regReq := app.RegistryListRequest{ConfigDir: configDir}
			entry, err := app.RegistryLookup(regReq, input)
			if err != nil {
				fetchErr := app.RegistryFetch(ctx, app.RegistryFetchRequest{ConfigDir: configDir}, nil)
				if fetchErr == nil {
					entry, err = app.RegistryLookup(regReq, input)
				}
			}
			if err != nil {
				return InstalledMsg{Name: input, Err: fmt.Errorf("registry lookup for %q: %w", input, err)}
			}
			req = app.PackInstallRequestFromRegistryEntry(configDir, input, entry)
			req.With = with
			if ref != "" {
				req.Ref = ref
			}
		} else {
			req.PackPath = input
			req.Link = true
		}

		opCtx, cancel := context.WithCancel(ctx)
		eventCh := make(chan app.PackInstallEvent, 16)
		resultCh := make(chan InstallResult, 1)
		req.Events = eventCh
		name := req.Name
		if name == "" {
			name = filepath.Base(input)
		}
		go func() {
			defer cancel()
			defer close(eventCh)
			defer func() {
				if r := recover(); r != nil {
					resultCh <- InstallResult{Name: name, Err: fmt.Errorf("panic: %v", r)}
				}
			}()
			err := app.PackInstall(opCtx, req, nil)
			resultCh <- InstallResult{Name: name, Err: err}
		}()
		return InstallReadyMsg{
			EventCh:  eventCh,
			ResultCh: resultCh,
			Cancel:   cancel,
			PackName: name,
		}
	}
}

func ReadNextInstallEvent(eventCh <-chan app.PackInstallEvent, resultCh <-chan InstallResult) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-eventCh
		if ok {
			return InstallProgressMsg{Event: e}
		}
		res := <-resultCh
		return InstalledMsg(res)
	}
}

var IsRegistryName = cmdutil.IsRegistryName
var isRegistryName = IsRegistryName

func PreviewInstall(ctx context.Context, configDir string, sel ListSelection) tea.Cmd {
	return func() tea.Msg {
		req := app.PackInspectRequest{
			ConfigDir: configDir,
			Ref:       sel.Ref,
			SubPath:   sel.RegPath,
		}
		switch {
		case sel.InRegistry:
			req.Input = sel.Name
		case strings.TrimSpace(sel.Repo) != "":
			req.Input = sel.Repo
		case strings.TrimSpace(sel.Source) != "":
			req.Input = sel.Source
		default:
			req.Input = sel.Name
		}

		result, err := app.PackInspect(ctx, req)
		titleName := sel.Name
		if result.Name != "" {
			titleName = result.Name
		}
		return common.PreviewLoadedMsg{
			Title:    "Preview install: " + titleName,
			PackName: titleName,
			Body:     formatInspectPreview(result),
			Err:      err,
		}
	}
}

func Update(ctx context.Context, configDir, name string, all bool, ref string, with domain.BundledSet) tea.Cmd {
	return func() tea.Msg {
		req := app.PackUpdateRequest{
			ConfigDir: configDir,
			Name:      name,
			All:       all,
			Ref:       ref,
			With:      with,
		}
		opCtx, cancel := context.WithCancel(ctx)
		eventCh := make(chan app.PackUpdateEvent, 32)
		resultCh := make(chan UpdateResult, 1)
		go func() {
			defer cancel()
			defer close(eventCh)
			defer func() {
				if r := recover(); r != nil {
					resultCh <- UpdateResult{Err: fmt.Errorf("panic: %v", r)}
				}
			}()
			results, err := app.PackUpdate(opCtx, req, nil, eventCh)
			resultCh <- UpdateResult{Results: results, Err: err}
		}()
		return UpdateReadyMsg{
			EventCh:  eventCh,
			ResultCh: resultCh,
			Cancel:   cancel,
			PackName: name,
		}
	}
}

func PreviewUpdate(ctx context.Context, configDir, name string) tea.Cmd {
	return func() tea.Msg {
		var stdout bytes.Buffer
		results, err := app.PackUpdate(ctx, app.PackUpdateRequest{
			ConfigDir: configDir,
			Name:      name,
			DryRun:    true,
		}, &stdout, nil)
		return common.PreviewLoadedMsg{
			Title:    "Preview update: " + name,
			PackName: name,
			Body:     formatUpdatePreview(stdout.String(), results),
			Err:      err,
		}
	}
}

func ReadNextEvent(eventCh <-chan app.PackUpdateEvent, resultCh <-chan UpdateResult, packName string) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-eventCh
		if ok {
			return ProgressMsg{Event: e}
		}
		res := <-resultCh
		return UpdatedMsg{Name: packName, Results: res.Results, Err: res.Err}
	}
}

func formatInspectPreview(result app.PackInspectResult) string {
	if result.Name == "" {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Name: %s\n", result.Name)
	if result.Version != "" {
		fmt.Fprintf(&sb, "Version: %s\n", result.Version)
	}
	fmt.Fprintf(&sb, "Source: %s\n", result.Source)
	fmt.Fprintln(&sb, "Status: inspected")
	if result.Method != "" {
		fmt.Fprintf(&sb, "Method: %s\n", result.Method)
	}
	if result.Ref != "" {
		fmt.Fprintf(&sb, "Ref: %s\n", result.Ref)
	}
	if result.SubPath != "" {
		fmt.Fprintf(&sb, "Path: %s\n", result.SubPath)
	}
	fmt.Fprintf(&sb, "Content: %s\n", result.Counts.String())
	appendPreviewList(&sb, "Rules", result.Rules)
	appendPreviewList(&sb, "Agents", result.Agents)
	appendPreviewList(&sb, "Workflows", result.Workflows)
	appendPreviewList(&sb, "Skills", result.Skills)
	appendPreviewList(&sb, "Hooks", result.Hooks)
	appendPreviewList(&sb, "Plugins", result.Plugins)
	appendPreviewList(&sb, "Prompts", result.Prompts)
	appendPreviewList(&sb, "MCP", result.MCPServers)
	appendPreviewList(&sb, "Profiles", result.Profiles)
	appendPreviewList(&sb, "Registries", result.Registries)
	appendPreviewList(&sb, "Extras", result.Extras)
	appendPreviewList(&sb, "Warnings", result.Warnings)
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "Preview only. No pack files, profiles, lockfile, or harness files were changed.")
	return sb.String()
}

func formatUpdatePreview(stdout string, results []app.PackUpdateResult) string {
	var sb strings.Builder
	if trimmed := strings.TrimSpace(stdout); trimmed != "" {
		sb.WriteString(trimmed)
		sb.WriteString("\n\n")
	}
	if len(results) == 0 {
		sb.WriteString("No update result returned.\n")
	} else {
		sb.WriteString("Results:\n")
		for _, r := range results {
			detail := r.Message
			if detail == "" {
				detail = string(r.Status)
			}
			fmt.Fprintf(&sb, "- %s: %s", r.Name, detail)
			if r.DryRun {
				sb.WriteString(" (dry-run)")
			}
			sb.WriteString("\n")
			appendPreviewList(&sb, "  New bundled content", bundledCandidateNames(r.BundledCandidates))
		}
	}
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "Preview only. This dry-run did not change pack files, bundled content, the lockfile, profiles, or harness files.")
	return sb.String()
}

func appendPreviewList(sb *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(sb, "%s:\n", label)
	for _, value := range values {
		fmt.Fprintf(sb, "- %s\n", value)
	}
}

func bundledCandidateNames(candidates *app.BundledCandidates) []string {
	if candidates == nil {
		return nil
	}
	cats := candidates.Categories()
	names := make([]string, len(cats))
	for i, cat := range cats {
		names[i] = string(cat)
	}
	return names
}
