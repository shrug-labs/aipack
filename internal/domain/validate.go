package domain

import "fmt"

// FrontmatterValidator is implemented by all content frontmatter types.
type FrontmatterValidator interface {
	Validate(fileID string) []Warning
}

// validateNameDesc checks name and description fields shared by all content types.
func validateNameDesc(name, fileID, description string) []Warning {
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
	return validateNameDesc(fm.Name, fileID, fm.Description)
}

// Validate checks required fields and name consistency for an agent.
func (fm AgentFrontmatter) Validate(fileID string) []Warning {
	return validateNameDesc(fm.Name, fileID, fm.Description)
}

// ValidateRefs checks that agent mcp_servers and skills reference known IDs.
// Pass nil for either set to skip that check.
func (fm AgentFrontmatter) ValidateRefs(fileID string, knownServers, knownSkills map[string]struct{}) []Warning {
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
func (fm WorkflowFrontmatter) Validate(fileID string) []Warning {
	return validateNameDesc(fm.Name, fileID, fm.Description)
}

// Validate checks required fields and name consistency for a skill.
func (fm SkillFrontmatter) Validate(fileID string) []Warning {
	return validateNameDesc(fm.Name, fileID, fm.Description)
}
