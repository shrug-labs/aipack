package opencode

import (
	"maps"
	"path/filepath"
	"slices"
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

// BuildInstructionsSpec builds the instructions spec for OpenCode. Pass
// the managed rules directory that aipack writes profile-filtered rules
// into; pointing instructions there — rather than at each pack's source —
// means profile enable/disable takes effect at runtime. Encoded nested
// rule filenames (`team-a__style.md`) stay flat in renderedDir, so the
// single `<dir>/*.md` glob loads them. Empty renderedDir means "nothing
// to manage".
func BuildInstructionsSpec(renderedDir string) InstructionsSpec {
	if renderedDir == "" {
		return InstructionsSpec{Manage: false}
	}
	glob := filepath.Join(renderedDir, "*.md")
	return InstructionsSpec{
		Manage:  true,
		Managed: []string{glob, renderedDir},
		Desired: []string{glob},
	}
}

// BuildSkillsSpec builds the skills.paths spec for OpenCode. Same contract
// as BuildInstructionsSpec — renderedDir points at the managed skills
// directory, empty means "nothing to manage".
func BuildSkillsSpec(renderedDir string) SkillsSpec {
	if renderedDir == "" {
		return SkillsSpec{Manage: false}
	}
	return SkillsSpec{
		Manage:  true,
		Managed: []string{renderedDir},
		Desired: []string{renderedDir},
	}
}

// MergeInstructions applies the instruction spec to a settings root.
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
		return slices.Clone(v)
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
	maps.Copy(rest, obj)
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
