package domain

import (
	"os"
	"path/filepath"
)

// Plan holds all actions to be applied during a sync operation.
type Plan struct {
	Writes     []WriteAction
	Copies     []CopyAction
	Settings   []SettingsAction
	MCP        []SettingsAction // MCP-related config files; NOT gated by --skip-settings
	MCPServers []MCPAction
	Desired    map[string]struct{}
	Ledger     string // path to ledger file
}

// AddDesired marks a path as expected in the plan (for prune tracking).
func (p *Plan) AddDesired(path string) {
	if p.Desired == nil {
		p.Desired = map[string]struct{}{}
	}
	p.Desired[filepath.Clean(path)] = struct{}{}
}

// Fragment is a builder for Plan. Each harness contributes a Fragment
// which is accumulated into the final Plan via Apply.
type Fragment struct {
	Writes     []WriteAction
	Copies     []CopyAction
	Settings   []SettingsAction
	MCP        []SettingsAction
	MCPServers []MCPAction
	Desired    []string
}

// Apply accumulates fragment actions into the plan.
func (f Fragment) Apply(plan *Plan) {
	plan.Writes = append(plan.Writes, f.Writes...)
	plan.Copies = append(plan.Copies, f.Copies...)
	plan.Settings = append(plan.Settings, f.Settings...)
	plan.MCP = append(plan.MCP, f.MCP...)
	plan.MCPServers = append(plan.MCPServers, f.MCPServers...)
	for _, d := range f.Desired {
		plan.AddDesired(d)
	}
}

// addContentWrites is the shared implementation for AddRuleWrites, AddWorkflowWrites, and AddAgentWrites.
func (f *Fragment) addContentWrites(baseDir, subDir string, items []writableContent) {
	for _, item := range items {
		dst := filepath.Join(baseDir, subDir, item.writeName()+".md")
		f.Writes = append(f.Writes, WriteAction{
			Dst:        dst,
			Content:    item.writeRaw(),
			SourcePack: item.writeSourcePack(),
			Src:        item.writeSourcePath(),
		})
		f.Desired = append(f.Desired, dst)
	}
}

// AddRuleWrites appends WriteActions for each rule using Raw bytes.
func (f *Fragment) AddRuleWrites(baseDir, subDir string, rules []Rule) {
	items := make([]writableContent, len(rules))
	for i := range rules {
		items[i] = rules[i]
	}
	f.addContentWrites(baseDir, subDir, items)
}

// AddWorkflowWrites appends WriteActions for each workflow using Raw bytes.
func (f *Fragment) AddWorkflowWrites(baseDir, subDir string, workflows []Workflow) {
	items := make([]writableContent, len(workflows))
	for i := range workflows {
		items[i] = workflows[i]
	}
	f.addContentWrites(baseDir, subDir, items)
}

// AddAgentWrites appends WriteActions for each agent using Raw bytes.
func (f *Fragment) AddAgentWrites(baseDir, subDir string, agents []Agent) {
	items := make([]writableContent, len(agents))
	for i := range agents {
		items[i] = agents[i]
	}
	f.addContentWrites(baseDir, subDir, items)
}

// AddSkillCopies appends CopyActions for each skill directory.
// It also walks each source directory to expand individual file paths
// into Desired, so that desiredForPrune does not need to re-walk.
func (f *Fragment) AddSkillCopies(baseDir, subDir string, skills []Skill) {
	for _, s := range skills {
		dst := filepath.Join(baseDir, subDir, s.Name)
		f.Copies = append(f.Copies, CopyAction{
			Src:        s.DirPath,
			Dst:        dst,
			Kind:       CopyKindDir,
			SourcePack: s.SourcePack,
		})
		f.Desired = append(f.Desired, dst)
		// Expand individual file paths for prune tracking.
		// Best-effort: if the source directory is missing, ClassifyCopy
		// will catch it during apply.
		_ = filepath.WalkDir(s.DirPath, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return filepath.SkipAll
			}
			if d.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(s.DirPath, p)
			if rerr != nil {
				return nil
			}
			f.Desired = append(f.Desired, filepath.Join(dst, rel))
			return nil
		})
	}
}
