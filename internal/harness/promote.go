package harness

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/util"
)

// SourceType discriminates promoted content in SKILL.md frontmatter.
// Used by harnesses that promote agents or workflows to skill directories.
type SourceType string

const (
	SourceTypeAgent    SourceType = "agent"
	SourceTypeWorkflow SourceType = "workflow"
)

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

// CapturePromotedContent scans skillsDir for subdirectories containing
// SKILL.md files. It reads the enriched frontmatter to determine whether
// each entry was originally an agent, workflow, or plain skill, and populates
// the CaptureResult accordingly.
func CapturePromotedContent(skillsDir string, res *CaptureResult) {
	dirs := util.ListSubDirs(skillsDir)
	for _, d := range dirs {
		name := filepath.Base(d)
		skillFile := filepath.Join(d, "SKILL.md")

		raw, err := os.ReadFile(skillFile)
		if err != nil {
			// No SKILL.md — treat as plain skill directory (copy as-is).
			res.Copies = append(res.Copies, domain.CopyAction{
				Src: d, Dst: filepath.Join("skills", name), Kind: domain.CopyKindDir,
			})
			res.Skills = append(res.Skills, domain.Skill{Name: name, DirPath: d})
			continue
		}

		fm, body, parseErr := ParsePromotedFrontmatter(raw)
		if parseErr != nil {
			res.Warnings = append(res.Warnings, domain.Warning{
				Path:    skillFile,
				Message: fmt.Sprintf("parse promoted frontmatter: %v", parseErr),
			})
			res.Copies = append(res.Copies, domain.CopyAction{
				Src: d, Dst: filepath.Join("skills", name), Kind: domain.CopyKindDir,
			})
			res.Skills = append(res.Skills, domain.Skill{Name: name, DirPath: d})
			continue
		}

		switch fm.SourceType {
		case SourceTypeAgent:
			CaptureAsAgent(res, fm, body, name, skillFile, raw)
		case SourceTypeWorkflow:
			CaptureAsWorkflow(res, fm, body, name, skillFile, raw)
		default:
			// Plain skill — directory copy.
			res.Copies = append(res.Copies, domain.CopyAction{
				Src: d, Dst: filepath.Join("skills", name), Kind: domain.CopyKindDir,
			})
			res.Skills = append(res.Skills, domain.Skill{Name: name, DirPath: d})
		}
	}
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
			Description:     fm.Description,
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
			Description: fm.Description,
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
