package opencode

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// InstructionsSpec controls management of the instructions array in opencode.json.
type InstructionsSpec struct {
	Manage  bool
	Managed []string
	Desired []string
}

// SkillsSpec controls management of the skills.paths array in opencode.json.
type SkillsSpec struct {
	Manage  bool
	Managed []string
	Desired []string
}

// BuildInstructionsSpec builds the instructions spec from rule directories.
func BuildInstructionsSpec(ruleDirs []string, ruleFiles []string, manage bool) InstructionsSpec {
	if !manage || len(ruleDirs) == 0 {
		return InstructionsSpec{Manage: false}
	}
	var managed, desired []string
	for _, dir := range ruleDirs {
		glob := filepath.Join(dir, "*.md")
		managed = append(managed, glob, dir)
		if hasRulesInDir(ruleFiles, dir) {
			desired = append(desired, glob)
		}
	}
	sort.Strings(managed)
	sort.Strings(desired)
	return InstructionsSpec{Manage: true, Managed: managed, Desired: desired}
}

// BuildSkillsSpec builds the skills spec from skill root directories.
func BuildSkillsSpec(managedDirs []string, desiredDirs []string, manage bool) SkillsSpec {
	if !manage || len(managedDirs) == 0 {
		return SkillsSpec{Manage: false}
	}
	return SkillsSpec{
		Manage:  true,
		Managed: uniqueSorted(managedDirs),
		Desired: uniqueSorted(desiredDirs),
	}
}

func hasRulesInDir(ruleFiles []string, dir string) bool {
	for _, rf := range ruleFiles {
		rel, err := filepath.Rel(dir, rf)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(rel, "..") {
			return true
		}
	}
	return false
}

func uniqueSorted(items []string) []string {
	set := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, v := range items {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := set[v]; ok {
			continue
		}
		set[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// MergeInstructions applies instruction spec to a settings root.
func MergeInstructions(root map[string]any, instr InstructionsSpec) {
	if !instr.Manage {
		return
	}
	managed := map[string]struct{}{}
	for _, m := range instr.Managed {
		if m != "" {
			managed[m] = struct{}{}
		}
	}
	existing := readInstructions(root)
	var filtered []string
	for _, e := range existing {
		if _, ok := managed[e]; !ok {
			filtered = append(filtered, e)
		}
	}
	for _, d := range instr.Desired {
		if d != "" && !slices.Contains(filtered, d) {
			filtered = append(filtered, d)
		}
	}
	if len(filtered) == 0 {
		delete(root, "instructions")
		return
	}
	root["instructions"] = filtered
}

// MergeSkills applies skills spec to a settings root.
func MergeSkills(root map[string]any, skills SkillsSpec) {
	if !skills.Manage {
		return
	}
	managed := map[string]struct{}{}
	for _, m := range skills.Managed {
		if m != "" {
			managed[m] = struct{}{}
		}
	}
	existingPaths, existingRest := readSkills(root)
	var filtered []string
	for _, e := range existingPaths {
		if _, ok := managed[e]; !ok {
			filtered = append(filtered, e)
		}
	}
	for _, d := range skills.Desired {
		if d != "" && !slices.Contains(filtered, d) {
			filtered = append(filtered, d)
		}
	}
	if len(filtered) == 0 {
		if len(existingRest) == 0 {
			delete(root, "skills")
			return
		}
		delete(existingRest, "paths")
		root["skills"] = existingRest
		return
	}
	existingRest["paths"] = filtered
	root["skills"] = existingRest
}

func readInstructions(root map[string]any) []string {
	raw, ok := root["instructions"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return append([]string{}, v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func readSkills(root map[string]any) ([]string, map[string]any) {
	rest := map[string]any{}
	raw, ok := root["skills"]
	if !ok {
		return nil, rest
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, rest
	}
	for k, v := range obj {
		rest[k] = v
	}
	var paths []string
	if p, ok := obj["paths"]; ok {
		switch v := p.(type) {
		case []string:
			paths = append(paths, v...)
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					paths = append(paths, s)
				}
			}
		}
	}
	return paths, rest
}
