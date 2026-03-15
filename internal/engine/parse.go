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

// ---------------------------------------------------------------------------
// Generic parse infrastructure
// ---------------------------------------------------------------------------

// parseSpec configures parseContent for a specific content type.
type parseSpec[T any, FM any] struct {
	kind  domain.PackCategory
	label string
	build func(id string, fm FM, body, raw []byte, path, sourcePack string) T
}

// parseContent reads and parses pack content files, returning typed structs.
// SourcePack is set at parse time — no retroactive attribution needed.
func parseContent[T any, FM any](spec parseSpec[T, FM], packRoot string, ids []string, sourcePack string) ([]T, []domain.Warning, error) {
	var items []T
	var warnings []domain.Warning

	for _, id := range ids {
		path := filepath.Join(packRoot, filepath.FromSlash(spec.kind.PrimaryRelPath(id)))
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("reading %s %s: %w", spec.label, path, err)
		}

		fmBytes, body, err := domain.SplitFrontmatter(raw)
		if err != nil {
			return nil, nil, err
		}

		var fm FM
		if len(fmBytes) > 0 {
			if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
				warnings = append(warnings, domain.Warning{
					Path:    path,
					Field:   "frontmatter",
					Message: "invalid YAML: " + err.Error(),
				})
			}
		}

		items = append(items, spec.build(id, fm, body, raw, path, sourcePack))
	}
	return items, warnings, nil
}

// parseBytesContent parses a single content item from raw bytes with
// best-effort frontmatter unmarshalling.
func parseBytesContent[T any, FM any](spec parseSpec[T, FM], raw []byte, name, sourcePack string) (T, error) {
	fmBytes, body, err := domain.SplitFrontmatter(raw)
	if err != nil {
		var zero T
		return zero, err
	}
	var fm FM
	if len(fmBytes) > 0 {
		_ = yaml.Unmarshal(fmBytes, &fm) // best-effort
	}
	return spec.build(name, fm, body, raw, "", sourcePack), nil
}

// ---------------------------------------------------------------------------
// Content type specs
// ---------------------------------------------------------------------------

var ruleSpec = parseSpec[domain.Rule, domain.RuleFrontmatter]{
	kind:  domain.CategoryRules,
	label: "rule",
	build: func(id string, fm domain.RuleFrontmatter, body, raw []byte, path, sp string) domain.Rule {
		return domain.Rule{Name: id, Frontmatter: fm, Body: body, Raw: raw, SourcePath: path, SourcePack: sp}
	},
}

var agentSpec = parseSpec[domain.Agent, domain.AgentFrontmatter]{
	kind:  domain.CategoryAgents,
	label: "agent",
	build: func(id string, fm domain.AgentFrontmatter, body, raw []byte, path, sp string) domain.Agent {
		name := id
		if fm.Name != "" {
			name = fm.Name
		}
		return domain.Agent{Name: name, Frontmatter: fm, Body: body, Raw: raw, SourcePath: path, SourcePack: sp}
	},
}

var workflowSpec = parseSpec[domain.Workflow, domain.WorkflowFrontmatter]{
	kind:  domain.CategoryWorkflows,
	label: "workflow",
	build: func(id string, fm domain.WorkflowFrontmatter, body, raw []byte, path, sp string) domain.Workflow {
		return domain.Workflow{Name: id, Frontmatter: fm, Body: body, Raw: raw, SourcePath: path, SourcePack: sp}
	},
}

var skillSpec = parseSpec[domain.Skill, domain.SkillFrontmatter]{
	kind:  domain.CategorySkills,
	label: "skill",
	build: func(id string, fm domain.SkillFrontmatter, body, raw []byte, path, sp string) domain.Skill {
		return domain.Skill{Name: id, Frontmatter: fm, Body: body, DirPath: filepath.Dir(path), SourcePack: sp}
	},
}

// ---------------------------------------------------------------------------
// File-based parse wrappers (used by profile resolution)
// ---------------------------------------------------------------------------

func parseRules(packRoot string, ids []string, sourcePack string) ([]domain.Rule, []domain.Warning, error) {
	return parseContent(ruleSpec, packRoot, ids, sourcePack)
}

func parseAgents(packRoot string, ids []string, sourcePack string) ([]domain.Agent, []domain.Warning, error) {
	return parseContent(agentSpec, packRoot, ids, sourcePack)
}

func parseWorkflows(packRoot string, ids []string, sourcePack string) ([]domain.Workflow, []domain.Warning, error) {
	return parseContent(workflowSpec, packRoot, ids, sourcePack)
}

func parseSkills(packRoot string, ids []string, sourcePack string) ([]domain.Skill, []domain.Warning, error) {
	return parseContent(skillSpec, packRoot, ids, sourcePack)
}

// ---------------------------------------------------------------------------
// Byte-based parse helpers (for capture / round-trip save)
// ---------------------------------------------------------------------------

// ParseRuleBytes parses a single rule from raw bytes.
func ParseRuleBytes(raw []byte, name, sourcePack string) (domain.Rule, error) {
	return parseBytesContent(ruleSpec, raw, name, sourcePack)
}

// ParseAgentBytes parses a single agent from raw bytes.
func ParseAgentBytes(raw []byte, name, sourcePack string) (domain.Agent, error) {
	return parseBytesContent(agentSpec, raw, name, sourcePack)
}

// ParseWorkflowBytes parses a single workflow from raw bytes.
func ParseWorkflowBytes(raw []byte, name, sourcePack string) (domain.Workflow, error) {
	return parseBytesContent(workflowSpec, raw, name, sourcePack)
}

// ---------------------------------------------------------------------------
// Render helpers
// ---------------------------------------------------------------------------

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
