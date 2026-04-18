package config

import (
	"os"
	"path/filepath"
	"slices"
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
	slices.Sort(out)
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
	slices.Sort(out)
	return out, nil
}

// DiscoverContent populates empty content fields in m by scanning packRoot.
// Fields that already contain entries are left unchanged. This allows
// manifests to omit content lists (or leave them as empty arrays) and have
// them auto-discovered from the conventional directory structure.
func DiscoverContent(m *PackManifest, packRoot string) error {
	if len(m.Rules) == 0 {
		ids, err := DiscoverIDs(filepath.Join(packRoot, "rules"), ".md")
		if err != nil {
			return err
		}
		m.Rules = ids
	}
	if len(m.Agents) == 0 {
		ids, err := DiscoverIDs(filepath.Join(packRoot, "agents"), ".md")
		if err != nil {
			return err
		}
		m.Agents = ids
	}
	if len(m.Workflows) == 0 {
		ids, err := DiscoverIDs(filepath.Join(packRoot, "workflows"), ".md")
		if err != nil {
			return err
		}
		m.Workflows = ids
	}
	if len(m.Skills) == 0 {
		ids, err := DiscoverSkills(filepath.Join(packRoot, "skills"))
		if err != nil {
			return err
		}
		m.Skills = ids
	}
	if len(m.Prompts) == 0 {
		ids, err := DiscoverIDs(filepath.Join(packRoot, "prompts"), ".md")
		if err != nil {
			return err
		}
		m.Prompts = ids
	}
	if len(m.Profiles) == 0 {
		ids, err := DiscoverIDs(filepath.Join(packRoot, "profiles"), ".yaml")
		if err != nil {
			return err
		}
		m.Profiles = ids
	}
	if len(m.Registries) == 0 {
		ids, err := DiscoverIDs(filepath.Join(packRoot, "registries"), ".yaml")
		if err != nil {
			return err
		}
		m.Registries = ids
	}
	if len(m.MCP) == 0 {
		ids, err := DiscoverIDs(filepath.Join(packRoot, "mcp"), ".json")
		if err != nil {
			return err
		}
		m.MCP = ids
	}
	return nil
}
