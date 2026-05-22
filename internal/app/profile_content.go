package app

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// ProfileContentAction is the profile content mutation to apply.
type ProfileContentAction string

const (
	ProfileContentActionInclude ProfileContentAction = "include"
	ProfileContentActionExclude ProfileContentAction = "exclude"
)

// ProfileContentRequest holds inputs for smart profile content toggles.
type ProfileContentRequest struct {
	ConfigDir   string
	ProfileName string
	IDs         []string
	PackName    string
	Kind        domain.PackCategory
	Stdout      io.Writer
}

// ProfileContentResult describes the content entries affected by a profile
// content mutation.
type ProfileContentResult struct {
	Profile     string
	ProfilePath string
	Action      ProfileContentAction
	Items       []ProfileContentItem
	Changed     bool
}

// ProfileContentItem is one exact profile content match.
type ProfileContentItem struct {
	ID       string
	PackName string
	Category domain.PackCategory
	Changed  bool
}

type profileContentMatch struct {
	packIndex int
	packName  string
	category  domain.PackCategory
	id        string
}

// ProfileContentInclude makes exact content IDs active in a profile.
func ProfileContentInclude(req ProfileContentRequest) (ProfileContentResult, error) {
	return mutateProfileContent(req, ProfileContentActionInclude)
}

// ProfileContentExclude makes exact content IDs inactive in a profile.
func ProfileContentExclude(req ProfileContentRequest) (ProfileContentResult, error) {
	return mutateProfileContent(req, ProfileContentActionExclude)
}

func mutateProfileContent(req ProfileContentRequest, action ProfileContentAction) (ProfileContentResult, error) {
	if req.ConfigDir == "" {
		return ProfileContentResult{}, fmt.Errorf("config dir is required")
	}
	profileName, err := config.NormalizeProfileName(req.ProfileName)
	if err != nil {
		return ProfileContentResult{}, err
	}
	if len(req.IDs) == 0 {
		return ProfileContentResult{}, fmt.Errorf("at least one content ID is required")
	}
	for _, id := range req.IDs {
		if strings.TrimSpace(id) == "" {
			return ProfileContentResult{}, fmt.Errorf("content IDs must not be empty")
		}
		if isProfileContentGlob(id) {
			return ProfileContentResult{}, fmt.Errorf("glob selectors are not supported by profile %s; use exact content IDs", action)
		}
	}

	profilePath := filepath.Join(req.ConfigDir, "profiles", profileName+".yaml")
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return ProfileContentResult{}, err
	}
	packs, errs := ResolveProfilePacks(req.ConfigDir, cfg.Packs)

	resolved := make([]profileContentMatch, 0, len(req.IDs))
	for _, id := range req.IDs {
		matches := findProfileContentMatches(packs, cfg.Packs, id, req.PackName, req.Kind, profilePackMatchEnabled)
		if len(matches) == 0 {
			disabledMatches := findProfileContentMatches(packs, cfg.Packs, id, req.PackName, req.Kind, profilePackMatchDisabled)
			if len(disabledMatches) > 0 {
				return ProfileContentResult{}, disabledProfileContentError(profileName, id, disabledMatches)
			}
			return ProfileContentResult{}, missingProfileContentError(req.ConfigDir, profileName, id, req.PackName, req.Kind, errs)
		}
		if len(matches) > 1 {
			return ProfileContentResult{}, ambiguousProfileContentError(id, matches)
		}
		resolved = append(resolved, matches[0])
	}

	result := ProfileContentResult{
		Profile:     profileName,
		ProfilePath: profilePath,
		Action:      action,
		Items:       make([]ProfileContentItem, 0, len(resolved)),
	}
	packsByIndex := packsByProfileIndex(packs)
	for _, match := range resolved {
		pe := &cfg.Packs[match.packIndex]
		var changed bool
		if match.category == domain.CategoryMCP {
			changed = mutateProfileMCP(pe, match.id, action)
		} else {
			inventory := packsByIndex[match.packIndex].Manifest.ContentIDs(match.category)
			changed, err = mutateProfileVector(pe, match.packName, match.category, inventory, match.id, action)
			if err != nil {
				return ProfileContentResult{}, err
			}
		}
		result.Changed = result.Changed || changed
		result.Items = append(result.Items, ProfileContentItem{
			ID:       match.id,
			PackName: match.packName,
			Category: match.category,
			Changed:  changed,
		})
	}
	if result.Changed {
		if req.Stdout != nil {
			warnBundledProfileEdit(req.ConfigDir, profileName, req.Stdout)
		}
		if err := ProfileSave(ProfileSaveRequest{ConfigDir: req.ConfigDir, Name: profileName, Config: cfg}); err != nil {
			return ProfileContentResult{}, err
		}
	}
	return result, nil
}

type profilePackMatchMode int

const (
	profilePackMatchEnabled profilePackMatchMode = iota
	profilePackMatchDisabled
)

func findProfileContentMatches(packs []ProfilePackInfo, entries []config.PackEntry, id, packFilter string, kind domain.PackCategory, mode profilePackMatchMode) []profileContentMatch {
	var matches []profileContentMatch
	for _, p := range packs {
		if packFilter != "" && p.Name != packFilter {
			continue
		}
		disabled := profilePackDisabled(p, entries)
		if mode == profilePackMatchEnabled && disabled {
			continue
		}
		if mode == profilePackMatchDisabled && !disabled {
			continue
		}
		for _, cat := range profileContentSearchCategories() {
			if kind != "" && cat != kind {
				continue
			}
			if slices.Contains(p.Manifest.ContentIDs(cat), id) {
				matches = append(matches, profileContentMatch{
					packIndex: p.Index,
					packName:  p.Name,
					category:  cat,
					id:        id,
				})
			}
		}
	}
	return matches
}

func profilePackDisabled(p ProfilePackInfo, entries []config.PackEntry) bool {
	if p.Index < 0 || p.Index >= len(entries) {
		return false
	}
	return !config.PackEnabled(entries[p.Index].Enabled)
}

func profileContentSearchCategories() []domain.PackCategory {
	return append(domain.SelectableCategories(), domain.CategoryMCP)
}

func mutateProfileVector(pe *config.PackEntry, packName string, cat domain.PackCategory, inventory []string, id string, action ProfileContentAction) (bool, error) {
	return (&profileVectorMutation{
		entry:     pe,
		packName:  packName,
		category:  cat,
		inventory: inventory,
		id:        id,
		action:    action,
	}).apply()
}

type profileVectorMutation struct {
	entry     *config.PackEntry
	packName  string
	category  domain.PackCategory
	inventory []string
	id        string
	action    ProfileContentAction

	selector          *config.VectorSelector
	before            config.VectorSelector
	beforeHookEnabled *bool
}

func (m *profileVectorMutation) apply() (bool, error) {
	sel := m.entry.VectorSelectorFor(m.category)
	if sel == nil {
		return false, fmt.Errorf("%s content cannot be changed with profile %s", m.category.DirName(), m.action)
	}
	m.selector = sel
	m.before = *sel
	if m.category == domain.CategoryHooks {
		m.beforeHookEnabled = m.entry.Hooks.Enabled
	}

	if m.hooksCurrentlyDisabled() {
		return m.applyDisabledHooks()
	}

	changed, err := m.applySelector()
	if err != nil {
		return false, err
	}
	if changed && m.category == domain.CategoryHooks {
		updateHookEnabled(m.entry, m.packName, m.inventory)
	}

	return m.changed(), nil
}

func (m *profileVectorMutation) hooksCurrentlyDisabled() bool {
	return m.category == domain.CategoryHooks && m.entry.Hooks.Enabled != nil && !*m.entry.Hooks.Enabled
}

func (m *profileVectorMutation) applyDisabledHooks() (bool, error) {
	if m.action == ProfileContentActionExclude {
		return false, nil
	}
	*m.selector = config.SelectionsToProfileVector(m.inventory, []string{m.id}, m.entry.Quiet)
	m.entry.Hooks.Enabled = nil
	return m.changed(), nil
}

func (m *profileVectorMutation) applySelector() (bool, error) {
	switch {
	case m.selector.Include != nil && len(*m.selector.Include) == 0:
		return m.mutateDefaultSelector(), nil
	case m.selector.Exclude != nil && len(*m.selector.Exclude) == 0:
		return m.mutateDefaultSelector(), nil
	case m.selector.Include != nil:
		return m.mutateIncludeSelector()
	case m.selector.Exclude != nil:
		return m.mutateExcludeSelector()
	default:
		return m.mutateImplicitSelector(), nil
	}
}

func (m *profileVectorMutation) mutateDefaultSelector() bool {
	switch m.action {
	case ProfileContentActionInclude:
		if !m.entry.Quiet {
			return false
		}
		include := []string{m.id}
		*m.selector = config.VectorSelector{Include: &include}
		return true
	case ProfileContentActionExclude:
		if m.entry.Quiet {
			return false
		}
		exclude := []string{m.id}
		*m.selector = config.VectorSelector{Exclude: &exclude}
		return true
	default:
		return false
	}
}

func (m *profileVectorMutation) mutateImplicitSelector() bool {
	switch m.action {
	case ProfileContentActionInclude:
		if !m.entry.Quiet {
			return false
		}
		include := []string{m.id}
		m.selector.Include = &include
		return true
	case ProfileContentActionExclude:
		if m.entry.Quiet {
			return false
		}
		exclude := []string{m.id}
		m.selector.Exclude = &exclude
		return true
	default:
		return false
	}
}

func (m *profileVectorMutation) mutateIncludeSelector() (bool, error) {
	switch m.action {
	case ProfileContentActionInclude:
		if selectorListContains(m.selector.Include, m.id) {
			return false, nil
		}
		active, err := m.containsID()
		if err != nil {
			return false, err
		}
		if active {
			return false, nil
		}
		next := appendSortedUnique(*m.selector.Include, m.id)
		m.selector.Include = &next
		return true, nil
	case ProfileContentActionExclude:
		if selectorListContains(m.selector.Include, m.id) {
			next := removeSortedExact(*m.selector.Include, m.id)
			if len(next) == 0 {
				*m.selector = config.SelectionsToProfileVector(m.inventory, nil, m.entry.Quiet)
			} else {
				m.selector.Include = &next
			}
			return true, nil
		}
		active, err := m.containsID()
		if err != nil {
			return false, err
		}
		if active {
			return false, fmt.Errorf("%s/%s is selected by an existing include pattern; edit the profile YAML to change pattern-based selectors", m.category.DirName(), m.id)
		}
		return false, nil
	default:
		return false, fmt.Errorf("unknown profile content action %q", m.action)
	}
}

func (m *profileVectorMutation) mutateExcludeSelector() (bool, error) {
	switch m.action {
	case ProfileContentActionInclude:
		if selectorListContains(m.selector.Exclude, m.id) {
			next := removeSortedExact(*m.selector.Exclude, m.id)
			if len(next) == 0 {
				*m.selector = config.VectorSelector{}
			} else {
				m.selector.Exclude = &next
			}
			return true, nil
		}
		active, err := m.containsID()
		if err != nil {
			return false, err
		}
		if !active {
			return false, fmt.Errorf("%s/%s is disabled by an existing exclude pattern; edit the profile YAML to change pattern-based selectors", m.category.DirName(), m.id)
		}
		return false, nil
	case ProfileContentActionExclude:
		active, err := m.containsID()
		if err != nil {
			return false, err
		}
		if !active {
			return false, nil
		}
		next := appendSortedUnique(*m.selector.Exclude, m.id)
		m.selector.Exclude = &next
		return true, nil
	default:
		return false, fmt.Errorf("unknown profile content action %q", m.action)
	}
}

func (m *profileVectorMutation) containsID() (bool, error) {
	selected, err := config.ResolveProfileVectorSelection(m.packName, m.category, m.inventory, *m.selector, m.entry.Quiet)
	if err != nil {
		return false, err
	}
	return slices.Contains(selected, m.id), nil
}

func (m *profileVectorMutation) changed() bool {
	return !config.VectorEqual(m.before, *m.selector) || !ptrBoolSame(m.beforeHookEnabled, m.entry.Hooks.Enabled)
}

func updateHookEnabled(pe *config.PackEntry, packName string, inventory []string) {
	selected, err := config.ResolveProfileVectorSelection(packName, domain.CategoryHooks, inventory, pe.Hooks.VectorSelector, pe.Quiet)
	if err != nil || len(selected) > 0 {
		pe.Hooks.Enabled = nil
		return
	}
	pe.Hooks.Enabled = config.BoolPtr(false)
}

func mutateProfileMCP(pe *config.PackEntry, id string, action ProfileContentAction) bool {
	before := cloneMCPConfig(pe.MCP)
	switch action {
	case ProfileContentActionInclude:
		cfg, ok := pe.MCP[id]
		if !ok {
			if pe.Quiet || (len(pe.MCP) > 0 && !config.MCPSelectionIsDisableOnly(pe.MCP)) {
				if pe.MCP == nil {
					pe.MCP = map[string]config.MCPServerConfig{}
				}
				pe.MCP[id] = config.MCPServerConfig{Enabled: config.BoolPtr(true)}
			}
			break
		}
		if cfg.Enabled != nil && !*cfg.Enabled {
			if !pe.Quiet && !cfg.HasToolPolicy() && config.MCPSelectionIsDisableOnly(pe.MCP) {
				delete(pe.MCP, id)
			} else {
				cfg.Enabled = config.BoolPtr(true)
				pe.MCP[id] = cfg
			}
		}
	case ProfileContentActionExclude:
		if pe.MCP == nil {
			pe.MCP = map[string]config.MCPServerConfig{}
		}
		cfg := pe.MCP[id]
		if cfg.Enabled == nil || *cfg.Enabled {
			cfg.Enabled = config.BoolPtr(false)
			pe.MCP[id] = cfg
		}
	}
	if len(pe.MCP) == 0 {
		pe.MCP = nil
	}
	return !config.MCPConfigEqual(before, pe.MCP)
}

func cloneMCPConfig(in map[string]config.MCPServerConfig) map[string]config.MCPServerConfig {
	if in == nil {
		return nil
	}
	out := make(map[string]config.MCPServerConfig, len(in))
	for k, v := range in {
		v.AllowedTools = slices.Clone(v.AllowedTools)
		v.AlwaysAllowedTools = slices.Clone(v.AlwaysAllowedTools)
		v.DisabledTools = slices.Clone(v.DisabledTools)
		out[k] = v
	}
	return out
}

func packsByProfileIndex(packs []ProfilePackInfo) map[int]ProfilePackInfo {
	out := make(map[int]ProfilePackInfo, len(packs))
	for _, p := range packs {
		out[p.Index] = p
	}
	return out
}

func missingProfileContentError(configDir, profileName, id, packFilter string, kind domain.PackCategory, resolveErrs []string) error {
	installed := findInstalledProfileContentMatches(configDir, id, packFilter, kind)
	if len(installed) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "content id %q is not in profile %q; matching installed content:\n", id, profileName)
		for _, match := range installed {
			fmt.Fprintf(&b, "  %s/%s from %s\n", match.category.DirName(), match.id, match.packName)
		}
		fmt.Fprintf(&b, "\nAdd the pack to the profile first, for example: aipack pack add %s --profile %s", installed[0].packName, profileName)
		return fmt.Errorf("%s", b.String())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "content id %q was not found in profile %q", id, profileName)
	if packFilter != "" {
		fmt.Fprintf(&b, " for pack %q", packFilter)
	}
	if kind != "" {
		fmt.Fprintf(&b, " as %s", kind.DirName())
	}
	if len(resolveErrs) > 0 {
		fmt.Fprintf(&b, " (some profile packs could not be inspected: %s)", strings.Join(resolveErrs, "; "))
	}
	return fmt.Errorf("%s", b.String())
}

func disabledProfileContentError(profileName, id string, matches []profileContentMatch) error {
	var b strings.Builder
	fmt.Fprintf(&b, "content id %q is only available from disabled pack entries in profile %q:\n", id, profileName)
	for _, match := range matches {
		fmt.Fprintf(&b, "  %s/%s from %s\n", match.category.DirName(), match.id, match.packName)
	}
	fmt.Fprintf(&b, "\nEnable the pack first, for example: aipack pack enable %s --profile %s", matches[0].packName, profileName)
	return fmt.Errorf("%s", b.String())
}

func ambiguousProfileContentError(id string, matches []profileContentMatch) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches multiple profile entries:\n", id)
	for _, match := range matches {
		fmt.Fprintf(&b, "  %s/%s from %s\n", match.category.DirName(), match.id, match.packName)
	}
	b.WriteString("\nUse --kind or --pack to choose one.")
	return fmt.Errorf("%s", b.String())
}

func findInstalledProfileContentMatches(configDir, id, packFilter string, kind domain.PackCategory) []profileContentMatch {
	installed, err := PackListDetailed(configDir)
	if err != nil {
		return nil
	}
	var matches []profileContentMatch
	for _, pack := range installed {
		if packFilter != "" && pack.Name != packFilter {
			continue
		}
		for _, cat := range profileContentSearchCategories() {
			if kind != "" && cat != kind {
				continue
			}
			if slices.Contains(pack.ContentIDs(cat), id) {
				matches = append(matches, profileContentMatch{
					packName: pack.Name,
					category: cat,
					id:       id,
				})
			}
		}
	}
	return matches
}

func selectorListContains(items *[]string, id string) bool {
	return items != nil && slices.Contains(*items, id)
}

func appendSortedUnique(items []string, id string) []string {
	out := append([]string{}, items...)
	if !slices.Contains(out, id) {
		out = append(out, id)
	}
	return domain.SortedCopy(out)
}

func removeSortedExact(items []string, id string) []string {
	out := slices.DeleteFunc(slices.Clone(items), func(item string) bool {
		return item == id
	})
	return domain.SortedCopy(out)
}

func isProfileContentGlob(id string) bool {
	return strings.ContainsAny(id, "*?[")
}

func ptrBoolSame(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
