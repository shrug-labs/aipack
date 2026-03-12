package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoverIDs scans dir for files with the given suffix and returns their
// base names (suffix stripped). Returns nil if the directory does not exist.
func DiscoverIDs(dir string, suffix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lowerSuffix := strings.ToLower(suffix)
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		lowerName := strings.ToLower(name)
		if !strings.HasSuffix(lowerName, lowerSuffix) {
			continue
		}
		base := name[:len(name)-len(suffix)]
		if base != "" {
			out = append(out, base)
		}
	}
	sort.Strings(out)
	return out, nil
}

// DiscoverSkills scans skillsDir for subdirectories containing SKILL.md.
// Returns nil if the directory does not exist.
func DiscoverSkills(skillsDir string) ([]string, error) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(skillsDir, e.Name(), "SKILL.md")
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// DiscoverContent populates nil content fields in m by scanning packRoot.
// Fields that are already non-nil (including explicit empty slices) are left
// unchanged. This allows manifests to omit content lists entirely and have
// them auto-discovered from the conventional directory structure.
func DiscoverContent(m *PackManifest, packRoot string) error {
	if m.Rules == nil {
		ids, err := DiscoverIDs(filepath.Join(packRoot, "rules"), ".md")
		if err != nil {
			return err
		}
		m.Rules = ids
	}
	if m.Agents == nil {
		ids, err := DiscoverIDs(filepath.Join(packRoot, "agents"), ".md")
		if err != nil {
			return err
		}
		m.Agents = ids
	}
	if m.Workflows == nil {
		ids, err := DiscoverIDs(filepath.Join(packRoot, "workflows"), ".md")
		if err != nil {
			return err
		}
		m.Workflows = ids
	}
	if m.Skills == nil {
		ids, err := DiscoverSkills(filepath.Join(packRoot, "skills"))
		if err != nil {
			return err
		}
		m.Skills = ids
	}
	return nil
}
