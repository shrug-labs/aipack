package harness

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

// SourceType discriminates promoted content in SKILL.md frontmatter.
// Used by harnesses that promote agents or workflows to skill directories.
type SourceType string

const (
	SourceTypeAgent    SourceType = "agent"
	SourceTypeWorkflow SourceType = "workflow"
)

// Prefixes injected into promoted descriptions so users can distinguish
// content types in harnesses that flatten everything to skill directories.
const (
	DescPrefixWorkflow = "[Workflow] "
	DescPrefixAgent    = "[Agent] "
)

// StripDescPrefix removes a promotion-injected type prefix from a description
// during capture round-trip, preventing accumulation across sync cycles.
func StripDescPrefix(desc string) string {
	desc = strings.TrimPrefix(desc, DescPrefixWorkflow)
	desc = strings.TrimPrefix(desc, DescPrefixAgent)
	return desc
}

// CheckPromotionCollisions detects collisions between content types that render
// to the same promoted skill directory. In namespaced mode the pack name is
// part of the key, but content type is not; same-pack source IDs still collide.
func CheckPromotionCollisions(namespaced bool, skills []domain.Skill, workflows []domain.Workflow, agents []domain.Agent) error {
	names := map[string]string{} // rendered directory name → content type
	for _, s := range skills {
		rendered := ContentName(namespaced, s.SourcePack, s.Name)
		names[rendered] = "skill"
	}
	for _, w := range workflows {
		name := workflowSourceID(w)
		rendered := ContentName(namespaced, w.SourcePack, name)
		if existing, ok := names[rendered]; ok {
			return fmt.Errorf(
				"name collision: %s %q and workflow %q would both be promoted to the same skill directory; rename one to avoid silent overwrite",
				existing, name, name,
			)
		}
		names[rendered] = "workflow"
	}
	for _, a := range agents {
		name := agentSourceID(a)
		rendered := ContentName(namespaced, a.SourcePack, name)
		if existing, ok := names[rendered]; ok {
			return fmt.Errorf(
				"name collision: %s %q and agent %q would both be promoted to the same skill directory; rename one to avoid silent overwrite",
				existing, name, name,
			)
		}
		names[rendered] = "agent"
	}
	return nil
}

// PromotedFrontmatter is the enriched SKILL.md frontmatter written during
// promotion. It carries the original content type and metadata so that
// capture can reconstruct the correct domain type on round-trip.
type PromotedFrontmatter struct {
	Name            string         `yaml:"name"`
	Description     string         `yaml:"description"`
	SourceType      SourceType     `yaml:"source_type,omitempty"`
	Tools           []string       `yaml:"tools,omitempty"`
	DisallowedTools []string       `yaml:"disallowed_tools,omitempty"`
	Skills          []string       `yaml:"skills,omitempty"`
	MCPServers      []string       `yaml:"mcp_servers,omitempty"`
	Metadata        map[string]any `yaml:"metadata,omitempty"`
}

// BuildPromotedMD generates SKILL.md content with enriched YAML frontmatter.
func BuildPromotedMD(fm PromotedFrontmatter, body string) string {
	out, err := yaml.Marshal(&fm)
	if err != nil {
		// Fallback: minimal frontmatter on marshal failure.
		var buf strings.Builder
		buf.WriteString("---\nname: ")
		buf.WriteString(fm.Name)
		buf.WriteString("\ndescription: ")
		buf.WriteString(fm.Description)
		buf.WriteString("\n---\n\n")
		buf.WriteString(body)
		buf.WriteString("\n")
		return buf.String()
	}
	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(bytes.TrimRight(out, "\n"))
	buf.WriteString("\n---\n\n")
	buf.WriteString(body)
	buf.WriteString("\n")
	return buf.String()
}

func AddPromotedSkillWrite(f *domain.Fragment, skillsDir, name string, content []byte, sourcePack, sourcePath string) {
	skillDir := filepath.Join(skillsDir, name)
	dst := filepath.Join(skillDir, domain.SkillEntryFile)
	f.Writes = append(f.Writes, domain.WriteAction{
		Dst:        dst,
		Content:    content,
		SourcePack: sourcePack,
		Src:        sourcePath,
	})
	f.Desired = append(f.Desired, skillDir, dst)
}

// ParsePromotedFrontmatter splits SKILL.md raw bytes into the enriched
// frontmatter struct and the markdown body.
func ParsePromotedFrontmatter(raw []byte) (PromotedFrontmatter, []byte, error) {
	fmBytes, body, err := domain.SplitFrontmatter(raw)
	if err != nil {
		return PromotedFrontmatter{}, nil, err
	}
	var fm PromotedFrontmatter
	if len(fmBytes) > 0 {
		if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
			return PromotedFrontmatter{}, nil, err
		}
	}
	return fm, body, nil
}

// CapturePromotedContent reads skillsDir for direct child directories
// containing a SKILL.md. The enriched SKILL.md frontmatter determines
// whether the entry is reconstructed as an agent, workflow, or plain skill.
// The directory leaf name becomes the content name.
func CapturePromotedContent(skillsDir string, knownPacks map[string]struct{}, res *CaptureResult) {
	captureSkillEntries(skillsDir, res, func(entry CapturedSkillEntry) {
		captureName := entry.DirName
		if id, ok := ParseKnownRenderedContentName(entry.DirName, knownPacks); ok {
			captureName = id.ID
		}
		fm := entry.Frontmatter
		StripRenderedPromotedFrontmatter(&fm, knownPacks)

		switch fm.SourceType {
		case SourceTypeAgent:
			CaptureAsAgent(res, fm, entry.Body, captureName, entry.SkillFile, entry.Raw)
		case SourceTypeWorkflow:
			CaptureAsWorkflow(res, fm, entry.Body, captureName, entry.SkillFile, entry.Raw)
		default:
			entry.Frontmatter = fm
			CaptureRenderedPlainSkill(entry, knownPacks, res)
		}
	})
}

// CaptureAsAgent reconstructs a domain.Agent from promoted frontmatter and
// emits a WriteAction with re-rendered agent bytes. rawSkill is the unmodified
// SKILL.md content used to compute SourceDigest for ledger-consistent change
// detection.
func CaptureAsAgent(res *CaptureResult, fm PromotedFrontmatter, body []byte, name, src string, rawSkill []byte) {
	agent := domain.Agent{
		Name: name,
		Frontmatter: domain.AgentFrontmatter{
			Name:            fm.Name,
			Description:     StripDescPrefix(fm.Description),
			Tools:           fm.Tools,
			DisallowedTools: fm.DisallowedTools,
			Skills:          fm.Skills,
			MCPServers:      fm.MCPServers,
		},
		Body: body,
	}

	rendered, err := engine.RenderAgentBytes(agent)
	if err != nil {
		res.Warnings = append(res.Warnings, domain.Warning{
			Path:    src,
			Message: fmt.Sprintf("render agent %s failed, falling back to plain skill: %v", name, err),
		})
		res.Copies = append(res.Copies, domain.CopyAction{
			Src: filepath.Dir(src), Dst: filepath.Join("skills", name), Kind: domain.CopyKindDir,
		})
		res.Skills = append(res.Skills, domain.Skill{Name: name, DirPath: filepath.Dir(src)})
		return
	}
	agent.Raw = rendered
	agent.SourcePath = src

	res.Writes = append(res.Writes, domain.WriteAction{
		Dst:          filepath.Join("agents", name+".md"),
		Content:      rendered,
		Src:          src,
		IsContent:    true,
		SourceDigest: domain.SingleFileDigest(rawSkill),
	})
	res.Agents = append(res.Agents, agent)
}

// CaptureAsWorkflow reconstructs a domain.Workflow from promoted frontmatter
// and emits a WriteAction with re-rendered workflow bytes. rawSkill is the
// unmodified SKILL.md content used to compute SourceDigest.
func CaptureAsWorkflow(res *CaptureResult, fm PromotedFrontmatter, body []byte, name, src string, rawSkill []byte) {
	wf := domain.Workflow{
		Name: name,
		Frontmatter: domain.WorkflowFrontmatter{
			Name:        fm.Name,
			Description: StripDescPrefix(fm.Description),
			Metadata:    fm.Metadata,
		},
		Body: body,
	}

	rendered, err := engine.RenderWorkflowBytes(wf)
	if err != nil {
		res.Warnings = append(res.Warnings, domain.Warning{
			Path:    src,
			Message: fmt.Sprintf("render workflow %s failed, falling back to plain skill: %v", name, err),
		})
		res.Copies = append(res.Copies, domain.CopyAction{
			Src: filepath.Dir(src), Dst: filepath.Join("skills", name), Kind: domain.CopyKindDir,
		})
		res.Skills = append(res.Skills, domain.Skill{Name: name, DirPath: filepath.Dir(src)})
		return
	}
	wf.Raw = rendered
	wf.SourcePath = src

	res.Writes = append(res.Writes, domain.WriteAction{
		Dst:          filepath.Join("workflows", name+".md"),
		Content:      rendered,
		Src:          src,
		IsContent:    true,
		SourceDigest: domain.SingleFileDigest(rawSkill),
	})
	res.Workflows = append(res.Workflows, wf)
}
