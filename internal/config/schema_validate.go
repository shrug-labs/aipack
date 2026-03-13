package config

import (
	"bytes"
	"fmt"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/shrug-labs/aipack/schemas"
)

var (
	packSchema      *jsonschema.Schema
	mcpServerSchema *jsonschema.Schema
	schemaOnce      sync.Once
	schemaInitErr   error
)

func initSchemas() {
	schemaOnce.Do(func() {
		packSchema, mcpServerSchema, schemaInitErr = compileSchemas()
	})
}

func compileSchemas() (*jsonschema.Schema, *jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	packDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemas.PackSchemaJSON))
	if err != nil {
		return nil, nil, fmt.Errorf("parsing pack schema: %w", err)
	}
	if err := c.AddResource("pack.schema.json", packDoc); err != nil {
		return nil, nil, fmt.Errorf("adding pack schema: %w", err)
	}
	ps, err := c.Compile("pack.schema.json")
	if err != nil {
		return nil, nil, fmt.Errorf("compiling pack schema: %w", err)
	}

	c2 := jsonschema.NewCompiler()
	mcpDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemas.MCPServerSchemaJSON))
	if err != nil {
		return nil, nil, fmt.Errorf("parsing mcp-server schema: %w", err)
	}
	if err := c2.AddResource("mcp-server.schema.json", mcpDoc); err != nil {
		return nil, nil, fmt.Errorf("adding mcp-server schema: %w", err)
	}
	ms, err := c2.Compile("mcp-server.schema.json")
	if err != nil {
		return nil, nil, fmt.Errorf("compiling mcp-server schema: %w", err)
	}

	return ps, ms, nil
}

// ValidatePackJSONSchema validates raw pack.json bytes against the embedded schema.
func ValidatePackJSONSchema(data []byte) []Finding {
	initSchemas()
	if schemaInitErr != nil {
		return []Finding{{Path: "pack.json", Category: FindingCategorySchema, Severity: FindingSeverityError, Message: schemaInitErr.Error()}}
	}
	return validateAgainstSchema(packSchema, "pack.json", data)
}

// ValidateMCPServerSchema validates raw mcp/*.json bytes against the embedded schema.
func ValidateMCPServerSchema(relPath string, data []byte) []Finding {
	initSchemas()
	if schemaInitErr != nil {
		return []Finding{{Path: relPath, Category: FindingCategorySchema, Severity: FindingSeverityError, Message: schemaInitErr.Error()}}
	}
	return validateAgainstSchema(mcpServerSchema, relPath, data)
}

func validateAgainstSchema(schema *jsonschema.Schema, path string, data []byte) []Finding {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return []Finding{{Path: path, Category: FindingCategorySchema, Severity: FindingSeverityError, Message: fmt.Sprintf("invalid JSON: %v", err)}}
	}
	err = schema.Validate(inst)
	if err == nil {
		return nil
	}
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []Finding{{Path: path, Category: FindingCategorySchema, Severity: FindingSeverityError, Message: err.Error()}}
	}
	return schemaErrorToFindings(path, ve)
}

func schemaErrorToFindings(path string, ve *jsonschema.ValidationError) []Finding {
	remediation := remediationFixSchemaValue
	output := ve.BasicOutput()
	var findings []Finding
	for _, unit := range output.Errors {
		if unit.Error == nil {
			continue
		}
		findings = append(findings, Finding{
			Path:        path,
			Category:    FindingCategorySchema,
			Severity:    FindingSeverityError,
			Message:     formatSchemaError(unit.InstanceLocation, unit.Error),
			Remediation: remediation,
		})
	}
	// Only emit root error when there are no leaf errors (avoids generic
	// "doesn't match schema" duplicating specific leaf messages).
	if len(findings) == 0 && output.Error != nil {
		findings = append(findings, Finding{
			Path:        path,
			Category:    FindingCategorySchema,
			Severity:    FindingSeverityError,
			Message:     formatSchemaError(output.InstanceLocation, output.Error),
			Remediation: remediation,
		})
	}
	return findings
}

func formatSchemaError(location string, err *jsonschema.OutputError) string {
	msg := err.String()
	if location != "" {
		return location + ": " + msg
	}
	return msg
}
