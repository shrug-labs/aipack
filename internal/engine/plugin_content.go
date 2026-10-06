package engine

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/plugin"
)

// ProfileForHarness projects representable imports onto ordinary pack content.
// The resolved source profile stays native for inventory, updates and provenance.
func ProfileForHarness(profile domain.Profile, target domain.Harness, req PlanRequest) (domain.Profile, []domain.Warning, error) {
	profile.Packs = slices.Clone(profile.Packs)
	profile.MCPServers = slices.Clone(profile.MCPServers)
	var warnings []domain.Warning
	genericMCP := false
	for i, pack := range profile.Packs {
		s := pack.NativePlugin
		if s != nil && s.Package.Harness != target {
			if target == domain.HarnessClaudeCode && len(s.Selected[domain.CategoryHooks]) > 0 {
				for _, notice := range plugin.Compatibility(*s, target).Warnings {
					if strings.HasPrefix(notice, "hooks/") {
						warnings = append(warnings, domain.Warning{Field: "hooks", Message: notice})
					}
				}
			}
			for _, id := range s.Selected[domain.CategoryMCP] {
				if notice := plugin.StartupTimeoutNotice(*s, id, target); notice != "" {
					warnings = append(warnings, domain.Warning{Field: "mcp", Message: notice})
				}
			}
		}
		if s == nil || s.Package.Harness == target {
			continue
		}
		root := filepath.Join(s.Root, "upstream")
		data := filepath.Join(config.FallbackConfigDir(req.ConfigDir, req.Home), "plugin-data", s.Package.Binding())
		// Retain each target's existing runtime data path across native/ordinary
		// route changes. Data never belongs to the replaced content tree.
		base := req.TargetDir
		if base == "" {
			base = req.Home
			if req.Scope == domain.ScopeProject {
				base = req.ProjectDir
			}
		}
		if target == domain.HarnessClaudeCode {
			nativeHome := req.NativeConfigDir
			if nativeHome == "" {
				nativeHome = filepath.Join(req.Home, ".claude")
			}
			if req.Scope == domain.ScopeGlobal && req.TargetConfigDir {
				nativeHome = base
			}
			data = filepath.Join(nativeHome, "plugins/data", s.Package.Name+"-"+s.Package.Marketplace)
		} else if target == domain.HarnessOpenCode {
			if req.Scope == domain.ScopeProject {
				base = filepath.Join(base, ".opencode")
			} else if !req.TargetConfigDir {
				base = filepath.Join(base, ".config/opencode")
			}
			data = filepath.Join(base, "aipack-data", s.Package.Binding())
		}
		if s.Package.Harness == domain.HarnessCodex && (target == domain.HarnessOpenCode || target == domain.HarnessCline) && len(s.Selected[domain.CategoryHooks]) > 0 {
			hooks, notices, err := plugin.GenericHooks(*s, target, data)
			warnings = append(warnings, notices...)
			if err != nil {
				return domain.Profile{}, warnings, err
			}
			pack.Hooks = append(slices.Clone(pack.Hooks), hooks...)
			profile.Packs[i] = pack
		}
		if !plugin.GenericContentSelection(*s, target) {
			continue
		}
		rp := config.ResolvedPack{Name: pack.Name, Root: s.Root, Skills: s.Selected[domain.CategorySkills], Manifest: config.PackManifest{NativePlugin: &s.Package}}
		skills, parsedWarnings, err := New(nil, nil).parseSkills(rp)
		if err != nil {
			return domain.Profile{}, warnings, err
		}
		warnings = append(warnings, parsedWarnings...)
		for _, id := range s.Selected[domain.CategoryMCP] {
			server, err := plugin.GenericMCPServer(*s, id, target, data)
			if err != nil {
				return domain.Profile{}, warnings, err
			}
			profile.MCPServers = append(profile.MCPServers, server)
			genericMCP = true
		}
		for j := range skills {
			skill := &skills[j]
			raw, err := os.ReadFile(filepath.Join(skill.DirPath, domain.SkillEntryFile))
			if err != nil {
				return domain.Profile{}, warnings, err
			}
			skill.Raw, err = plugin.GenericSkillBytes(raw, skill.DirPath, root, data)
			if err != nil {
				return domain.Profile{}, warnings, err
			}
			skill.SourceBoundary = root
		}
		pack.Skills, pack.NativePlugin = skills, nil
		profile.Packs[i] = pack
	}
	owners := maps.Clone(profile.SkillOverrideOwners)
	for _, pack := range profile.Packs {
		if pack.NativePlugin != nil {
			for id, owner := range owners {
				if owner == pack.Name {
					delete(owners, id)
				}
			}
		}
	}
	seen := map[string]int{}
	for i := range profile.Packs {
		pack := &profile.Packs[i]
		pack.Skills = slices.Clone(pack.Skills)
		var drop []string
		for _, skill := range pack.Skills {
			if prev, exists := seen[skill.Name]; exists {
				previous := &profile.Packs[prev]
				winner, warning, err := config.ResolveContentCollision("skills", skill.Name, previous.Name, pack.Name, owners[skill.Name], config.CollisionStrategy(profile.CollisionStrategy), req.Namespaced)
				if err != nil {
					return domain.Profile{}, warnings, fmt.Errorf("harness %s: %w", target, err)
				}
				if warning != nil {
					warnings = append(warnings, *warning)
				}
				if winner == previous.Name {
					drop = append(drop, skill.Name)
					continue
				}
				if winner == pack.Name {
					previous.Skills = slices.DeleteFunc(previous.Skills, func(s domain.Skill) bool { return s.Name == skill.Name })
				}
			}
			seen[skill.Name] = i
		}
		pack.Skills = slices.DeleteFunc(pack.Skills, func(s domain.Skill) bool { return slices.Contains(drop, s.Name) })
	}
	if !genericMCP {
		return profile, warnings, nil
	}
	// Resolve server collisions in profile order using the ordinary MCP policy.
	order := map[string]int{}
	for i, pack := range profile.Packs {
		order[pack.Name] = i
	}
	slices.SortStableFunc(profile.MCPServers, func(a, b domain.MCPServer) int { return order[a.SourcePack] - order[b.SourcePack] })
	seen = map[string]int{}
	var effective []domain.MCPServer
	for _, server := range profile.MCPServers {
		if prev, exists := seen[server.Name]; exists {
			winner, warning, err := config.ResolveContentCollision("mcp", server.Name, effective[prev].SourcePack, server.SourcePack, profile.MCPOverrideOwners[server.Name], config.CollisionStrategy(profile.CollisionStrategy), false)
			if err != nil {
				return domain.Profile{}, warnings, fmt.Errorf("harness %s: %w", target, err)
			}
			if warning != nil {
				warnings = append(warnings, *warning)
			}
			if winner == server.SourcePack {
				effective[prev] = server
			}
			continue
		}
		seen[server.Name] = len(effective)
		effective = append(effective, server)
	}
	profile.MCPServers = effective
	return profile, warnings, nil
}
