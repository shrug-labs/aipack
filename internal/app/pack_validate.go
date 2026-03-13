package app

import "github.com/shrug-labs/aipack/internal/config"

type PackValidateRequest struct {
	PackRoot string
}

type PackValidateReport struct {
	OK       bool             `json:"ok"`
	Findings []config.Finding `json:"findings,omitempty"`
}

func RunPackValidate(req PackValidateRequest) PackValidateReport {
	findings := config.ValidatePackRoot(req.PackRoot)
	hasError := false
	for _, f := range findings {
		if f.Severity == config.FindingSeverityError {
			hasError = true
			break
		}
	}
	return PackValidateReport{OK: !hasError, Findings: findings}
}
