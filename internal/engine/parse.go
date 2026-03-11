package engine

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/domain"
)

// parseRules reads and parses rule files, returning typed Rule structs.
// SourcePack is set at parse time — no retroactive attribution needed.
func parseRules(packRoot string, ids []string, sourcePack string) ([]domain.Rule, []domain.Warning, error) {
	var rules []domain.Rule
	var warnings []domain.Warning

	for _, id := range ids {
		path := filepath.Join(packRoot, filepath.FromSlash(domain.ContentRules.PrimaryRelPath(id)))
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("reading rule %s: %w", path, err)
		}

		fmBytes, body, err := domain.SplitFrontmatter(raw)
		if err != nil {
			return nil, nil, err
		}

		var fm domain.RuleFrontmatter
		if len(fmBytes) > 0 {
			if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
				warnings = append(warnings, domain.Warning{
					Path:    path,
					Field:   "frontmatter",
					Message: "invalid YAML: " + err.Error(),
				})
			}
		}

		rules = append(rules, domain.Rule{
			Name:        id,
			Frontmatter: fm,
			Body:        body,
			Raw:         raw,
			SourcePath:  path,
			SourcePack:  sourcePack,
		})
	}
	return rules, warnings, nil
}

// parseAgents reads and parses agent files, returning typed Agent structs.
func parseAgents(packRoot string, ids []string, sourcePack string) ([]domain.Agent, []domain.Warning, error) {
	var agents []domain.Agent
	var warnings []domain.Warning

	for _, id := range ids {
		path := filepath.Join(packRoot, filepath.FromSlash(domain.ContentAgents.PrimaryRelPath(id)))
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("reading agent %s: %w", path, err)
		}

		fmBytes, body, err := domain.SplitFrontmatter(raw)
		if err != nil {
			return nil, nil, err
		}

		var fm domain.AgentFrontmatter
		if len(fmBytes) > 0 {
			if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
				warnings = append(warnings, domain.Warning{
					Path:    path,
					Field:   "frontmatter",
					Message: "invalid YAML: " + err.Error(),
				})
			}
		}

		name := id
		if fm.Name != "" {
			name = fm.Name
		}

		agents = append(agents, domain.Agent{
			Name:        name,
			Frontmatter: fm,
			Body:        body,
			Raw:         raw,
			SourcePath:  path,
			SourcePack:  sourcePack,
		})
	}
	return agents, warnings, nil
}

// parseWorkflows reads and parses workflow files, returning typed Workflow structs.
func parseWorkflows(packRoot string, ids []string, sourcePack string) ([]domain.Workflow, []domain.Warning, error) {
	var workflows []domain.Workflow
	var warnings []domain.Warning

	for _, id := range ids {
		path := filepath.Join(packRoot, filepath.FromSlash(domain.ContentWorkflows.PrimaryRelPath(id)))
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("reading workflow %s: %w", path, err)
		}

		fmBytes, body, err := domain.SplitFrontmatter(raw)
		if err != nil {
			return nil, nil, err
		}

		var fm domain.WorkflowFrontmatter
		if len(fmBytes) > 0 {
			if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
				warnings = append(warnings, domain.Warning{
					Path:    path,
					Field:   "frontmatter",
					Message: "invalid YAML: " + err.Error(),
				})
			}
		}

		workflows = append(workflows, domain.Workflow{
			Name:        id,
			Frontmatter: fm,
			Body:        body,
			Raw:         raw,
			SourcePath:  path,
			SourcePack:  sourcePack,
		})
	}
	return workflows, warnings, nil
}

// parseSkills resolves skill directories, returning typed Skill structs.
// Each skill's SKILL.md frontmatter is parsed for metadata.
func parseSkills(packRoot string, ids []string, sourcePack string) ([]domain.Skill, []domain.Warning, error) {
	var skills []domain.Skill
	var warnings []domain.Warning

	for _, id := range ids {
		dirPath := filepath.Join(packRoot, domain.ContentSkills.DirName(), id)
		skillMD := filepath.Join(packRoot, filepath.FromSlash(domain.ContentSkills.PrimaryRelPath(id)))

		raw, err := os.ReadFile(skillMD)
		if err != nil {
			return nil, nil, fmt.Errorf("reading skill %s: %w", skillMD, err)
		}

		fmBytes, body, err := domain.SplitFrontmatter(raw)
		if err != nil {
			return nil, nil, err
		}

		var fm domain.SkillFrontmatter
		if len(fmBytes) > 0 {
			if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
				warnings = append(warnings, domain.Warning{
					Path:    skillMD,
					Field:   "frontmatter",
					Message: "invalid YAML: " + err.Error(),
				})
			}
		}

		skills = append(skills, domain.Skill{
			Name:        id,
			Frontmatter: fm,
			Body:        body,
			DirPath:     dirPath,
			SourcePack:  sourcePack,
		})
	}
	return skills, warnings, nil
}

// ---------------------------------------------------------------------------
// Byte-based parse helpers (for capture / round-trip save)
// ---------------------------------------------------------------------------

// ParseRuleBytes parses a single rule from raw bytes.
func ParseRuleBytes(raw []byte, name, sourcePack string) (domain.Rule, error) {
	fmBytes, body, err := domain.SplitFrontmatter(raw)
	if err != nil {
		return domain.Rule{}, err
	}
	var fm domain.RuleFrontmatter
	if len(fmBytes) > 0 {
		_ = yaml.Unmarshal(fmBytes, &fm) // best-effort
	}
	return domain.Rule{
		Name:        name,
		Frontmatter: fm,
		Body:        body,
		Raw:         raw,
		SourcePack:  sourcePack,
	}, nil
}

// ParseAgentBytes parses a single agent from raw bytes.
func ParseAgentBytes(raw []byte, name, sourcePack string) (domain.Agent, error) {
	fmBytes, body, err := domain.SplitFrontmatter(raw)
	if err != nil {
		return domain.Agent{}, err
	}
	var fm domain.AgentFrontmatter
	if len(fmBytes) > 0 {
		_ = yaml.Unmarshal(fmBytes, &fm) // best-effort
	}
	agentName := name
	if fm.Name != "" {
		agentName = fm.Name
	}
	return domain.Agent{
		Name:        agentName,
		Frontmatter: fm,
		Body:        body,
		Raw:         raw,
		SourcePack:  sourcePack,
	}, nil
}

// ParseWorkflowBytes parses a single workflow from raw bytes.
func ParseWorkflowBytes(raw []byte, name, sourcePack string) (domain.Workflow, error) {
	fmBytes, body, err := domain.SplitFrontmatter(raw)
	if err != nil {
		return domain.Workflow{}, err
	}
	var fm domain.WorkflowFrontmatter
	if len(fmBytes) > 0 {
		_ = yaml.Unmarshal(fmBytes, &fm) // best-effort
	}
	return domain.Workflow{
		Name:        name,
		Frontmatter: fm,
		Body:        body,
		Raw:         raw,
		SourcePack:  sourcePack,
	}, nil
}

func RenderRuleBytes(rule domain.Rule) ([]byte, error) {
	return renderTypedContent(rule.Frontmatter, rule.Body)
}

func RenderAgentBytes(agent domain.Agent) ([]byte, error) {
	return renderTypedContent(agent.Frontmatter, agent.Body)
}

func RenderWorkflowBytes(workflow domain.Workflow) ([]byte, error) {
	return renderTypedContent(workflow.Frontmatter, workflow.Body)
}

func renderTypedContent(frontmatter any, body []byte) ([]byte, error) {
	if reflect.ValueOf(frontmatter).IsZero() {
		return append([]byte(nil), body...), nil
	}
	fm, err := yaml.Marshal(frontmatter)
	if err != nil {
		return nil, err
	}
	fm = bytes.TrimRight(fm, "\n")
	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(fm)
	out.WriteString("\n---\n")
	out.Write(body)
	return out.Bytes(), nil
}

// FlattenRules concatenates all rule Raw bytes with source comments, matching v1's format.
func FlattenRules(rules []domain.Rule) string {
	if len(rules) == 0 {
		return ""
	}
	parts := make([]string, 0, len(rules)*3)
	for _, r := range rules {
		t := strings.TrimRight(string(r.Raw), "\n")
		parts = append(parts, "<!-- source: "+r.Name+".md -->\n")
		parts = append(parts, t+"\n")
		parts = append(parts, "\n---\n\n")
	}
	out := strings.Join(parts, "")
	out = strings.TrimRight(out, "\n") + "\n"
	return out
}
