package app

import "github.com/shrug-labs/aipack/internal/config"

type PackValidateRequest struct {
	PackRoot string
}

type PackValidateReport struct {
	OK       bool     `json:"ok"`
	Findings []string `json:"findings,omitempty"`
}

func RunPackValidate(req PackValidateRequest) PackValidateReport {
	findings := config.ValidatePackRoot(req.PackRoot)
	return PackValidateReport{OK: len(findings) == 0, Findings: findings}
}
