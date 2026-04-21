package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
)

// resolveWalkRoot returns a path suitable for walking, following the root
// symlink so pack_create-style layouts (rules/ or skills/ symlinked at an
// external content source) are descended into — WalkDir does not follow
// symlinks on its own. Empty return means dir is absent or not a directory.
func resolveWalkRoot(dir string) (string, error) {
	st, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if !st.IsDir() {
		return "", nil
	}
	return filepath.EvalSymlinks(dir)
}

// DiscoverIDs walks dir recursively and returns slashed ids preserving the
// relative path. Used for vectors whose ids may be slashed (rules, prompts,
// mcp, profiles, registries). Nil when dir does not exist.
func DiscoverIDs(dir string, suffix string) ([]string, error) {
	root, err := resolveWalkRoot(dir)
	if err != nil {
		return nil, err
	}
	if root == "" {
		return nil, nil
	}
	lowerSuffix := strings.ToLower(suffix)
	out := []string{}
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), lowerSuffix) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		base := rel[:len(rel)-len(suffix)]
		if base == "" {
			return nil
		}
		out = append(out, filepath.ToSlash(base))
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	slices.Sort(out)
	return out, nil
}

// DiscoverIDsByLeaf walks dir recursively for files matching suffix and
// returns leaf-only ids (no slashes) plus an id → relpath map keyed under
// `<dirName>/`. Used for agents and workflows, where subdirectories under
// the category root are organizational only and the id is always the leaf.
// Returns an error when two files share the same leaf within the tree.
func DiscoverIDsByLeaf(dir, dirName, suffix string) ([]string, map[string]string, error) {
	root, err := resolveWalkRoot(dir)
	if err != nil {
		return nil, nil, err
	}
	if root == "" {
		return nil, nil, nil
	}
	lowerSuffix := strings.ToLower(suffix)
	paths := map[string]string{}
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), lowerSuffix) {
			return nil
		}
		id := strings.TrimSuffix(d.Name(), suffix)
		if id == "" {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		relSlashed := filepath.ToSlash(filepath.Join(dirName, rel))
		if existing, dup := paths[id]; dup {
			return fmt.Errorf(
				"duplicate %s id %q: %s and %s — leaf names must be unique within a pack",
				dirName, id, existing, relSlashed,
			)
		}
		paths[id] = relSlashed
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}
	ids := make([]string, 0, len(paths))
	for id := range paths {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, paths, nil
}

// DiscoverSkills walks skillsDir recursively for directories containing a
// regular SKILL.md and returns leaf-only skill names plus an id → relpath
// map for each. Returns an error when two skill directories share the same
// leaf. A SKILL.md nested inside another skill's tree (a bundled fixture)
// stays an asset of the outer skill — descent stops at the first match.
func DiscoverSkills(skillsDir string) ([]string, map[string]string, error) {
	root, err := resolveWalkRoot(skillsDir)
	if err != nil {
		return nil, nil, err
	}
	if root == "" {
		return nil, nil, nil
	}
	paths := map[string]string{}
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || path == root {
			return nil
		}
		entryStat, statErr := os.Stat(filepath.Join(path, domain.SkillEntryFile))
		if statErr != nil || !entryStat.Mode().IsRegular() {
			return nil
		}
		id := d.Name()
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		relSlashed := filepath.ToSlash(filepath.Join("skills", rel, domain.SkillEntryFile))
		if existing, dup := paths[id]; dup {
			return fmt.Errorf(
				"duplicate skill id %q: %s and %s — leaf names must be unique within a pack",
				id, existing, relSlashed,
			)
		}
		paths[id] = relSlashed
		return filepath.SkipDir
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}
	ids := make([]string, 0, len(paths))
	for id := range paths {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, paths, nil
}

// DiscoverContent populates empty content fields in m by scanning packRoot.
// Fields with explicit entries are left unchanged. Recursion is uniform —
// every category walks subdirectories — but ids differ:
//
//   - rules, prompts, mcp, profiles, registries: id preserves the slashed
//     relative path (the directory structure is part of the id).
//   - agents, workflows, skills: id is the leaf only (subdirectories are
//     authoring organization). Same-leaf collisions within one pack are an
//     error. The actual on-disk path is recorded via SetResolvedPath so
//     downstream callers (parse, validation, save round-trip) find the file.
func DiscoverContent(m *PackManifest, packRoot string) error {
	type slashedSpec struct {
		field  *[]string
		dir    string
		suffix string
	}
	for _, s := range []slashedSpec{
		{field: &m.Rules, dir: "rules", suffix: ".md"},
		{field: &m.Prompts, dir: "prompts", suffix: ".md"},
		{field: &m.Profiles, dir: "profiles", suffix: ".yaml"},
		{field: &m.Registries, dir: "registries", suffix: ".yaml"},
		{field: &m.MCP, dir: "mcp", suffix: ".json"},
	} {
		if len(*s.field) != 0 {
			continue
		}
		ids, err := DiscoverIDs(filepath.Join(packRoot, s.dir), s.suffix)
		if err != nil {
			return fmt.Errorf("discover %s: %w", s.dir, err)
		}
		*s.field = ids
	}

	type leafSpec struct {
		field   *[]string
		dir     string
		suffix  string
		dirName string
		cat     domain.PackCategory
	}
	for _, s := range []leafSpec{
		{field: &m.Agents, dir: "agents", suffix: ".md", dirName: "agents", cat: domain.CategoryAgents},
		{field: &m.Workflows, dir: "workflows", suffix: ".md", dirName: "workflows", cat: domain.CategoryWorkflows},
	} {
		ids, paths, err := DiscoverIDsByLeaf(filepath.Join(packRoot, s.dir), s.dirName, s.suffix)
		if err != nil {
			return fmt.Errorf("discover %s: %w", s.dir, err)
		}
		if len(*s.field) == 0 {
			*s.field = ids
		}
		for id, p := range paths {
			m.SetResolvedPath(s.cat, id, p)
		}
	}

	skillIDs, skillPaths, err := DiscoverSkills(filepath.Join(packRoot, "skills"))
	if err != nil {
		return fmt.Errorf("discover skills: %w", err)
	}
	if len(m.Skills) == 0 {
		m.Skills = skillIDs
	}
	for id, p := range skillPaths {
		m.SetResolvedPath(domain.CategorySkills, id, p)
	}
	return nil
}
