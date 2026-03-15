package domain

import "fmt"

// validateNameAndDescription checks the common name + description fields
// shared by rules, agents, and skills.
func validateNameAndDescription(name, description, fileID string) []Warning {
	var ws []Warning
	if name == "" {
		ws = append(ws, Warning{Field: "name", Message: "missing required field"})
	} else if name != fileID {
		ws = append(ws, Warning{Field: "name", Message: fmt.Sprintf("frontmatter name %q differs from file ID %q", name, fileID)})
	}
	if description == "" {
		ws = append(ws, Warning{Field: "description", Message: "missing required field"})
	}
	return ws
}

// Validate checks required fields and name consistency for a rule.
func (fm RuleFrontmatter) Validate(fileID string) []Warning {
	return validateNameAndDescription(fm.Name, fm.Description, fileID)
}

// Validate checks required fields and name consistency for an agent.
func (fm AgentFrontmatter) Validate(fileID string) []Warning {
	return validateNameAndDescription(fm.Name, fm.Description, fileID)
}

// ValidateRefs checks that agent mcp_servers and skills reference known IDs.
// Pass nil for either set to skip that check.
func (fm AgentFrontmatter) ValidateRefs(knownServers, knownSkills map[string]struct{}) []Warning {
	var ws []Warning
	if knownServers != nil {
		for _, s := range fm.MCPServers {
			if _, ok := knownServers[s]; !ok {
				ws = append(ws, Warning{Field: "mcp_servers", Message: fmt.Sprintf("references unknown MCP server %q", s)})
			}
		}
	}
	if knownSkills != nil {
		for _, s := range fm.Skills {
			if _, ok := knownSkills[s]; !ok {
				ws = append(ws, Warning{Field: "skills", Message: fmt.Sprintf("references unknown skill %q", s)})
			}
		}
	}
	return ws
}

// Validate checks required fields and name consistency for a workflow.
// Unlike rules/agents/skills, name is optional for workflows — the filename
// serves as the canonical identifier. Name is only validated when present.
func (fm WorkflowFrontmatter) Validate(fileID string) []Warning {
	var ws []Warning
	name := fm.DisplayName()
	if name != "" && name != fileID {
		ws = append(ws, Warning{Field: "name", Message: fmt.Sprintf("frontmatter name %q differs from file ID %q", name, fileID)})
	}
	if fm.Description == "" {
		ws = append(ws, Warning{Field: "description", Message: "missing required field"})
	}
	return ws
}

// Validate checks required fields and name consistency for a skill.
func (fm SkillFrontmatter) Validate(fileID string) []Warning {
	return validateNameAndDescription(fm.Name, fm.Description, fileID)
}
