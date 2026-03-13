package config

import "fmt"

// Finding is a structured validation result from pack validation.
type Finding struct {
	Path        string `json:"path"`
	Category    string `json:"category"` // schema, frontmatter, policy, consistency, inventory
	Severity    string `json:"severity"` // error, warning
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

// Category constants for Finding.
const (
	FindingCategorySchema      = "schema"
	FindingCategoryFrontmatter = "frontmatter"
	FindingCategoryPolicy      = "policy"
	FindingCategoryConsistency = "consistency"
	FindingCategoryInventory   = "inventory"
)

// Severity constants for Finding.
const (
	FindingSeverityError   = "error"
	FindingSeverityWarning = "warning"
)

// String formats the finding as a human-readable line.
func (f Finding) String() string {
	if f.Path != "" {
		return fmt.Sprintf("%s: %s", f.Path, f.Message)
	}
	return f.Message
}
