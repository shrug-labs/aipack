package domain

import "path/filepath"

// Plan holds all actions to be applied during a sync operation.
type Plan struct {
	Writes   []WriteAction
	Copies   []CopyAction
	Settings []SettingsAction
	Plugins  []SettingsAction // harness plugins + generated configs; NOT gated by --skip-settings
	Desired  map[string]struct{}
	Ledger   string // path to ledger file
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
	Writes   []WriteAction
	Copies   []CopyAction
	Settings []SettingsAction
	Plugins  []SettingsAction
	Desired  []string
}

// Apply accumulates fragment actions into the plan.
func (f Fragment) Apply(plan *Plan) {
	plan.Writes = append(plan.Writes, f.Writes...)
	plan.Copies = append(plan.Copies, f.Copies...)
	plan.Settings = append(plan.Settings, f.Settings...)
	plan.Plugins = append(plan.Plugins, f.Plugins...)
	for _, d := range f.Desired {
		plan.AddDesired(d)
	}
}

// AddRuleWrites appends WriteActions for each rule using Raw bytes.
func (f *Fragment) AddRuleWrites(baseDir, subDir string, rules []Rule) {
	for _, r := range rules {
		dst := filepath.Join(baseDir, subDir, r.Name+".md")
		f.Writes = append(f.Writes, WriteAction{
			Dst:        dst,
			Content:    r.Raw,
			SourcePack: r.SourcePack,
			Src:        r.SourcePath,
		})
		f.Desired = append(f.Desired, dst)
	}
}

// AddWorkflowWrites appends WriteActions for each workflow using Raw bytes.
func (f *Fragment) AddWorkflowWrites(baseDir, subDir string, workflows []Workflow) {
	for _, w := range workflows {
		dst := filepath.Join(baseDir, subDir, w.Name+".md")
		f.Writes = append(f.Writes, WriteAction{
			Dst:        dst,
			Content:    w.Raw,
			SourcePack: w.SourcePack,
			Src:        w.SourcePath,
		})
		f.Desired = append(f.Desired, dst)
	}
}

// AddAgentWrites appends WriteActions for each agent using Raw bytes.
func (f *Fragment) AddAgentWrites(baseDir, subDir string, agents []Agent) {
	for _, agent := range agents {
		dst := filepath.Join(baseDir, subDir, agent.Name+".md")
		f.Writes = append(f.Writes, WriteAction{
			Dst: dst, Content: agent.Raw, SourcePack: agent.SourcePack, Src: agent.SourcePath,
		})
		f.Desired = append(f.Desired, dst)
	}
}

// AddSkillCopies appends CopyActions for each skill directory.
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
	}
}
