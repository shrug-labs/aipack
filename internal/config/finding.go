package config

import "fmt"

// FindingCategory classifies what aspect of a pack the finding relates to.
type FindingCategory string

// FindingSeverity indicates how serious a finding is.
type FindingSeverity string

// Finding is a structured validation result from pack validation.
type Finding struct {
	Path     string          `json:"path"`
	Category FindingCategory `json:"category"`
	Severity FindingSeverity `json:"severity"`
	Field    string          `json:"field,omitempty"` // frontmatter field name when applicable
	Message  string          `json:"message"`
}

const (
	FindingCategoryFrontmatter FindingCategory = "frontmatter"
	FindingCategoryPolicy      FindingCategory = "policy"
	FindingCategoryConsistency FindingCategory = "consistency"
	FindingCategoryInventory   FindingCategory = "inventory"
)

const (
	FindingSeverityError   FindingSeverity = "error"
	FindingSeverityWarning FindingSeverity = "warning"
)

// String formats the finding as a human-readable line including severity.
func (f Finding) String() string {
	sev := string(f.Severity)
	switch {
	case f.Path != "" && f.Field != "":
		return fmt.Sprintf("[%s] %s: [%s] %s", sev, f.Path, f.Field, f.Message)
	case f.Path != "":
		return fmt.Sprintf("[%s] %s: %s", sev, f.Path, f.Message)
	default:
		return fmt.Sprintf("[%s] %s", sev, f.Message)
	}
}
