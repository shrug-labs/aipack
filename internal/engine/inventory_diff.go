package engine

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
)

// DiffInventory compares two pack inventories and returns the set of
// categorized changes. Pure function — does not consult the profile.
// Callers fold the profile in at print time via EmitDriftReport.
func DiffInventory(packName string, old, new domain.PackInventory) domain.InventoryDiff {
	d := domain.InventoryDiff{PackName: packName}
	d.AddedRules, d.RemovedRules = DiffStrings(old.Rules, new.Rules)
	d.AddedAgents, d.RemovedAgents = DiffStrings(old.Agents, new.Agents)
	d.AddedWorkflows, d.RemovedWorkflows = DiffStrings(old.Workflows, new.Workflows)
	d.AddedPlugins, d.RemovedPlugins = DiffStrings(old.Plugins, new.Plugins)
	d.AddedSkills, d.RemovedSkills, d.ChangedSkills = diffSkills(old.Skills, new.Skills)
	d.AddedServers, d.RemovedServers, d.ChangedServers = diffServers(old.MCPServers, new.MCPServers)
	return d
}

// DiffStrings returns the elements added and removed between two string
// slices, sorted. Both inputs are treated as sets (duplicates collapse).
func DiffStrings(oldList, newList []string) (added, removed []string) {
	oldSet := make(map[string]struct{}, len(oldList))
	for _, v := range oldList {
		oldSet[v] = struct{}{}
	}
	newSet := make(map[string]struct{}, len(newList))
	for _, v := range newList {
		newSet[v] = struct{}{}
	}
	for _, v := range newList {
		if _, ok := oldSet[v]; !ok {
			added = append(added, v)
		}
	}
	for _, v := range oldList {
		if _, ok := newSet[v]; !ok {
			removed = append(removed, v)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func diffSkills(oldMap, newMap map[string]domain.SkillSnapshot) (added map[string]domain.SkillSnapshot, removed []string, changed map[string]domain.SkillChange) {
	for name, snap := range newMap {
		prev, ok := oldMap[name]
		if !ok {
			if added == nil {
				added = make(map[string]domain.SkillSnapshot)
			}
			added[name] = snap
			continue
		}
		addedAssets, removedAssets := DiffStrings(prev.Assets, snap.Assets)
		if prev.Description == snap.Description && len(addedAssets) == 0 && len(removedAssets) == 0 {
			continue
		}
		if changed == nil {
			changed = make(map[string]domain.SkillChange)
		}
		changed[name] = domain.SkillChange{
			OldDescription: prev.Description,
			NewDescription: snap.Description,
			AddedAssets:    addedAssets,
			RemovedAssets:  removedAssets,
		}
	}
	for name := range oldMap {
		if _, ok := newMap[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	return added, removed, changed
}

func diffServers(oldMap, newMap map[string]domain.MCPServerSnapshot) (added map[string]domain.MCPServerSnapshot, removed []string, changed map[string]domain.MCPServerChange) {
	for name, snap := range newMap {
		prev, ok := oldMap[name]
		if !ok {
			if added == nil {
				added = make(map[string]domain.MCPServerSnapshot)
			}
			added[name] = snap
			continue
		}
		addedTools, removedTools := DiffStrings(prev.AvailableTools, snap.AvailableTools)
		addedRefs, removedRefs := diffRefs(prev.RequiredRefs, snap.RequiredRefs)
		if len(addedTools) == 0 && len(removedTools) == 0 && len(addedRefs) == 0 && len(removedRefs) == 0 {
			continue
		}
		if changed == nil {
			changed = make(map[string]domain.MCPServerChange)
		}
		changed[name] = domain.MCPServerChange{
			AddedTools:   addedTools,
			RemovedTools: removedTools,
			AddedRefs:    addedRefs,
			RemovedRefs:  removedRefs,
		}
	}
	for name := range oldMap {
		if _, ok := newMap[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	return added, removed, changed
}

// diffRefs computes added/removed required refs. Refs are keyed by
// (kind, name) — same name in different kinds is two distinct refs.
func diffRefs(oldRefs, newRefs []domain.RequiredRef) (added, removed []domain.RequiredRef) {
	key := func(r domain.RequiredRef) string { return r.Kind + ":" + r.Name }
	oldSet := make(map[string]domain.RequiredRef, len(oldRefs))
	for _, r := range oldRefs {
		oldSet[key(r)] = r
	}
	newSet := make(map[string]domain.RequiredRef, len(newRefs))
	for _, r := range newRefs {
		newSet[key(r)] = r
	}
	for _, r := range newRefs {
		if _, ok := oldSet[key(r)]; !ok {
			added = append(added, r)
		}
	}
	for _, r := range oldRefs {
		if _, ok := newSet[key(r)]; !ok {
			removed = append(removed, r)
		}
	}
	sortRefs(added)
	sortRefs(removed)
	return added, removed
}

func sortRefs(refs []domain.RequiredRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind == domain.RefKindParam
		}
		return refs[i].Name < refs[j].Name
	})
}

// EmitDriftReport writes the per-pack inventory diff report. Removed items
// are split into "affects your profile" (the pack referenced this id via a
// broken ref) and "other removals" (not referenced). Empty diffs are skipped.
// oldVersion and newVersion populate the header transition; either may be
// empty.
func EmitDriftReport(w io.Writer, diff domain.InventoryDiff, brokenRefs []domain.BrokenRef, oldVersion, newVersion string) {
	if w == nil || (diff.Empty() && len(brokenRefs) == 0) {
		return
	}

	transition := ""
	if oldVersion != "" && newVersion != "" && oldVersion != newVersion {
		transition = fmt.Sprintf("%s → %s", oldVersion, newVersion)
	}
	if transition != "" {
		fmt.Fprintf(w, "Drift detected in %s (%s):\n", diff.PackName, transition)
	} else {
		fmt.Fprintf(w, "Drift detected in %s:\n", diff.PackName)
	}

	brokenByKey := make(map[string]domain.BrokenRef, len(brokenRefs))
	for _, br := range brokenRefs {
		brokenByKey[brokenRefKey(br.Category, br.ID)] = br
	}

	type removal struct {
		category domain.PackCategory
		id       string
	}
	var affects, other []removal
	collect := func(cat domain.PackCategory, ids []string) {
		for _, id := range ids {
			if _, ok := brokenByKey[brokenRefKey(cat, id)]; ok {
				affects = append(affects, removal{cat, id})
			} else {
				other = append(other, removal{cat, id})
			}
		}
	}
	collect(domain.CategoryRules, diff.RemovedRules)
	collect(domain.CategoryAgents, diff.RemovedAgents)
	collect(domain.CategoryWorkflows, diff.RemovedWorkflows)
	collect(domain.CategoryPlugins, diff.RemovedPlugins)
	collect(domain.CategorySkills, diff.RemovedSkills)
	collect(domain.CategoryMCP, diff.RemovedServers)

	if len(affects) > 0 {
		emitDriftSection(w, "removed-referenced")
		for _, r := range affects {
			br := brokenByKey[brokenRefKey(r.category, r.id)]
			fmt.Fprintf(w, "    ! %-8s %-30s — referenced via %s\n", r.category, r.id, refLocation(br))
		}
	}
	if len(other) > 0 {
		emitDriftSection(w, "removed-other")
		for _, r := range other {
			fmt.Fprintf(w, "    - %-8s %s\n", r.category, r.id)
		}
	}

	hasAdds := len(diff.AddedRules) > 0 || len(diff.AddedAgents) > 0 ||
		len(diff.AddedWorkflows) > 0 || len(diff.AddedPlugins) > 0 ||
		len(diff.AddedSkills) > 0 || len(diff.AddedServers) > 0
	if hasAdds {
		emitDriftSection(w, "added")
		emitAddedIDs(w, domain.CategoryRules, diff.AddedRules)
		emitAddedIDs(w, domain.CategoryAgents, diff.AddedAgents)
		emitAddedIDs(w, domain.CategoryWorkflows, diff.AddedWorkflows)
		emitAddedIDs(w, domain.CategoryPlugins, diff.AddedPlugins)
		for _, name := range slices.Sorted(maps.Keys(diff.AddedSkills)) {
			snap := diff.AddedSkills[name]
			suffix := ""
			if snap.Description != "" {
				suffix = "  — " + snap.Description
			}
			emitDriftAdded(w, domain.CategorySkills, name, suffix)
			for _, asset := range snap.Assets {
				emitDriftDetail(w, "ship: "+asset)
			}
		}
		for _, name := range slices.Sorted(maps.Keys(diff.AddedServers)) {
			snap := diff.AddedServers[name]
			suffix := ""
			if len(snap.RequiredRefs) > 0 {
				parts := make([]string, len(snap.RequiredRefs))
				for i, r := range snap.RequiredRefs {
					parts[i] = r.Display()
				}
				suffix = fmt.Sprintf("  (requires: %s)", strings.Join(parts, ", "))
			}
			emitDriftAdded(w, domain.CategoryMCP, name, suffix)
			if len(snap.AvailableTools) > 0 {
				emitDriftDetail(w, "tools: "+strings.Join(snap.AvailableTools, ", "))
			}
		}
	}

	if len(diff.ChangedSkills) > 0 || len(diff.ChangedServers) > 0 {
		emitDriftSection(w, "changed")
		for _, name := range slices.Sorted(maps.Keys(diff.ChangedSkills)) {
			ch := diff.ChangedSkills[name]
			emitDriftChanged(w, domain.CategorySkills, name)
			if ch.OldDescription != ch.NewDescription {
				emitDriftDetail(w, fmt.Sprintf("description: %q → %q", ch.OldDescription, ch.NewDescription))
			}
			for _, a := range ch.AddedAssets {
				emitDriftDetail(w, "+ship: "+a)
			}
			for _, a := range ch.RemovedAssets {
				emitDriftDetail(w, "-ship: "+a)
			}
		}
		for _, name := range slices.Sorted(maps.Keys(diff.ChangedServers)) {
			ch := diff.ChangedServers[name]
			emitDriftChanged(w, domain.CategoryMCP, name)
			for _, t := range ch.AddedTools {
				emitDriftDetail(w, "+tool: "+t)
			}
			for _, t := range ch.RemovedTools {
				emitDriftDetail(w, "-tool: "+t)
			}
			for _, r := range ch.AddedRefs {
				emitDriftDetail(w, "+requires: "+r.Display())
			}
			for _, r := range ch.RemovedRefs {
				emitDriftDetail(w, "-requires: "+r.Display())
			}
		}
	}
}

func emitDriftSection(w io.Writer, key string) {
	fmt.Fprintf(w, "  %s:\n", driftSectionHeader(key))
}

func driftSectionHeader(key string) string {
	switch key {
	case "removed-referenced":
		return "Removed (affects your profile)"
	case "removed-other":
		return "Removed (other)"
	case "added":
		return "Added"
	case "changed":
		return "Changed"
	default:
		return key
	}
}

func emitDriftAdded(w io.Writer, cat domain.PackCategory, id, suffix string) {
	fmt.Fprintf(w, "    + %-8s %s%s\n", cat, id, suffix)
}

func emitDriftChanged(w io.Writer, cat domain.PackCategory, id string) {
	fmt.Fprintf(w, "    ~ %-8s %s\n", cat, id)
}

func emitDriftDetail(w io.Writer, detail string) {
	fmt.Fprintf(w, "        %s\n", detail)
}

func emitAddedIDs(w io.Writer, cat domain.PackCategory, ids []string) {
	for _, id := range ids {
		emitDriftAdded(w, cat, id, "")
	}
}

func brokenRefKey(cat domain.PackCategory, id string) string {
	return string(cat) + "/" + id
}

func refLocation(br domain.BrokenRef) string {
	if br.Direction == "overrides" {
		return string(br.Category) + ".overrides"
	}
	if br.Category == domain.CategoryMCP {
		return "mcp." + br.ID
	}
	if br.Direction != "" {
		return string(br.Category) + "." + br.Direction
	}
	return string(br.Category)
}
