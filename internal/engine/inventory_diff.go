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
// Callers fold the profile in at print time via FormatDriftReport.
func DiffInventory(packName string, old, new domain.PackInventory) domain.InventoryDiff {
	d := domain.InventoryDiff{PackName: packName}
	d.AddedRules, d.RemovedRules = DiffStrings(old.Rules, new.Rules)
	d.AddedAgents, d.RemovedAgents = DiffStrings(old.Agents, new.Agents)
	d.AddedWorkflows, d.RemovedWorkflows = DiffStrings(old.Workflows, new.Workflows)
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

// FormatDriftReport writes a per-pack drift summary to w. It splits removed
// items into "affects your profile" (referenced via a broken ref for this
// pack) and "other removals" (not referenced). Empty diffs are skipped.
// oldVersion and newVersion appear in the header; either may be empty.
func FormatDriftReport(w io.Writer, diff domain.InventoryDiff, brokenRefs []domain.BrokenRef, oldVersion, newVersion string) {
	if diff.Empty() && len(brokenRefs) == 0 {
		return
	}

	header := fmt.Sprintf("Drift detected in %s", diff.PackName)
	if oldVersion != "" && newVersion != "" && oldVersion != newVersion {
		header = fmt.Sprintf("%s (%s → %s)", header, oldVersion, newVersion)
	}
	fmt.Fprintln(w, header+":")

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
	collect(domain.CategorySkills, diff.RemovedSkills)
	collect(domain.CategoryMCP, diff.RemovedServers)

	if len(affects) > 0 {
		fmt.Fprintln(w, "  Removed (affects your profile):")
		for _, r := range affects {
			br := brokenByKey[brokenRefKey(r.category, r.id)]
			fmt.Fprintf(w, "    ! %-8s %-30s — referenced via %s\n", r.category, r.id, refLocation(br))
		}
	}
	if len(other) > 0 {
		fmt.Fprintln(w, "  Removed (other):")
		for _, r := range other {
			fmt.Fprintf(w, "    - %-8s %s\n", r.category, r.id)
		}
	}
	hasAdds := len(diff.AddedRules) > 0 || len(diff.AddedAgents) > 0 ||
		len(diff.AddedWorkflows) > 0 || len(diff.AddedSkills) > 0 || len(diff.AddedServers) > 0
	if hasAdds {
		fmt.Fprintln(w, "  Added:")
		writeAdds(w, domain.CategoryRules, diff.AddedRules)
		writeAdds(w, domain.CategoryAgents, diff.AddedAgents)
		writeAdds(w, domain.CategoryWorkflows, diff.AddedWorkflows)
		for _, name := range slices.Sorted(maps.Keys(diff.AddedSkills)) {
			snap := diff.AddedSkills[name]
			line := fmt.Sprintf("    + %-8s %s", domain.CategorySkills, name)
			if snap.Description != "" {
				line += "  — " + snap.Description
			}
			fmt.Fprintln(w, line)
			for _, asset := range snap.Assets {
				fmt.Fprintf(w, "        ship: %s\n", asset)
			}
		}
		for _, name := range slices.Sorted(maps.Keys(diff.AddedServers)) {
			snap := diff.AddedServers[name]
			line := fmt.Sprintf("    + %-8s %s", domain.CategoryMCP, name)
			if len(snap.RequiredRefs) > 0 {
				parts := make([]string, len(snap.RequiredRefs))
				for i, r := range snap.RequiredRefs {
					parts[i] = r.Display()
				}
				line += fmt.Sprintf("  (requires: %s)", strings.Join(parts, ", "))
			}
			fmt.Fprintln(w, line)
			if len(snap.AvailableTools) > 0 {
				fmt.Fprintf(w, "        tools: %s\n", strings.Join(snap.AvailableTools, ", "))
			}
		}
	}

	if len(diff.ChangedSkills) > 0 || len(diff.ChangedServers) > 0 {
		fmt.Fprintln(w, "  Changed:")
		for _, name := range slices.Sorted(maps.Keys(diff.ChangedSkills)) {
			ch := diff.ChangedSkills[name]
			fmt.Fprintf(w, "    ~ %-8s %s\n", domain.CategorySkills, name)
			if ch.OldDescription != ch.NewDescription {
				fmt.Fprintf(w, "        description: %q → %q\n", ch.OldDescription, ch.NewDescription)
			}
			for _, a := range ch.AddedAssets {
				fmt.Fprintf(w, "        +ship: %s\n", a)
			}
			for _, a := range ch.RemovedAssets {
				fmt.Fprintf(w, "        -ship: %s\n", a)
			}
		}
		for _, name := range slices.Sorted(maps.Keys(diff.ChangedServers)) {
			ch := diff.ChangedServers[name]
			fmt.Fprintf(w, "    ~ %-8s %s\n", domain.CategoryMCP, name)
			for _, t := range ch.AddedTools {
				fmt.Fprintf(w, "        +tool: %s\n", t)
			}
			for _, t := range ch.RemovedTools {
				fmt.Fprintf(w, "        -tool: %s\n", t)
			}
			for _, r := range ch.AddedRefs {
				fmt.Fprintf(w, "        +requires: %s\n", r.Display())
			}
			for _, r := range ch.RemovedRefs {
				fmt.Fprintf(w, "        -requires: %s\n", r.Display())
			}
		}
	}
}

func writeAdds(w io.Writer, cat domain.PackCategory, ids []string) {
	for _, id := range ids {
		fmt.Fprintf(w, "    + %-8s %s\n", cat, id)
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
