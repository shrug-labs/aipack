package config

import (
	"fmt"
	"strings"
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
	packDoc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(schemas.PackSchemaJSON)))
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
	mcpDoc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(schemas.MCPServerSchemaJSON)))
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
func ValidatePackJSONSchema(data []byte) []string {
	initSchemas()
	if schemaInitErr != nil {
		return []string{fmt.Sprintf("pack.json: %v", schemaInitErr)}
	}
	return validateAgainstSchema(packSchema, "pack.json", data)
}

// ValidateMCPServerSchema validates raw mcp/*.json bytes against the embedded schema.
func ValidateMCPServerSchema(relPath string, data []byte) []string {
	initSchemas()
	if schemaInitErr != nil {
		return []string{fmt.Sprintf("%s: %v", relPath, schemaInitErr)}
	}
	return validateAgainstSchema(mcpServerSchema, relPath, data)
}

func validateAgainstSchema(schema *jsonschema.Schema, path string, data []byte) []string {
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(data)))
	if err != nil {
		return []string{fmt.Sprintf("%s: invalid JSON: %v", path, err)}
	}
	err = schema.Validate(inst)
	if err == nil {
		return nil
	}
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []string{fmt.Sprintf("%s: %v", path, err)}
	}
	return schemaErrorToFindings(path, ve)
}

func schemaErrorToFindings(path string, ve *jsonschema.ValidationError) []string {
	output := ve.BasicOutput()
	var findings []string
	if output.Error != nil {
		findings = append(findings, fmt.Sprintf("%s: %s", path, formatSchemaError(output.InstanceLocation, output.Error)))
	}
	for _, unit := range output.Errors {
		if unit.Error == nil {
			continue
		}
		findings = append(findings, fmt.Sprintf("%s: %s", path, formatSchemaError(unit.InstanceLocation, unit.Error)))
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
