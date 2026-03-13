package config

import (
	"testing"
)

func TestValidatePackJSONSchema_Valid(t *testing.T) {
	t.Parallel()
	data := []byte(`{"schema_version":1,"name":"demo","root":"."}`)
	findings := ValidatePackJSONSchema(data)
	if len(findings) > 0 {
		t.Fatalf("expected no findings, got %v", findings)
	}
}

func TestValidatePackJSONSchema_InvalidSchemaVersion(t *testing.T) {
	t.Parallel()
	data := []byte(`{"schema_version":2,"name":"demo","root":"."}`)
	findings := ValidatePackJSONSchema(data)
	if len(findings) == 0 {
		t.Fatal("expected schema findings for invalid schema_version")
	}
	if findings[0].Category != FindingCategorySchema {
		t.Fatalf("expected schema category, got %q", findings[0].Category)
	}
}

func TestValidatePackJSONSchema_ExtraFieldRejected(t *testing.T) {
	t.Parallel()
	data := []byte(`{"schema_version":1,"name":"demo","root":".","bogus":true}`)
	findings := ValidatePackJSONSchema(data)
	if len(findings) == 0 {
		t.Fatal("expected schema finding for extra field")
	}
}

func TestValidatePackJSONSchema_UppercaseNameRejected(t *testing.T) {
	t.Parallel()
	data := []byte(`{"schema_version":1,"name":"MyPack","root":"."}`)
	findings := ValidatePackJSONSchema(data)
	if len(findings) == 0 {
		t.Fatal("expected schema finding for uppercase name")
	}
}

func TestValidatePackJSONSchema_WithOptionalFields(t *testing.T) {
	t.Parallel()
	data := []byte(`{"schema_version":1,"name":"demo","root":".","version":"1.0","description":"a pack","rules":["a","b"],"skills":["s"],"agents":[],"workflows":[]}`)
	findings := ValidatePackJSONSchema(data)
	if len(findings) > 0 {
		t.Fatalf("expected no findings, got %v", findings)
	}
}

func TestValidateMCPServerSchema_ValidStdio(t *testing.T) {
	t.Parallel()
	data := []byte(`{"name":"my-server","transport":"stdio","command":["run"]}`)
	findings := ValidateMCPServerSchema("mcp/my-server.json", data)
	if len(findings) > 0 {
		t.Fatalf("expected no findings, got %v", findings)
	}
}

func TestValidateMCPServerSchema_StdioMissingCommand(t *testing.T) {
	t.Parallel()
	data := []byte(`{"name":"my-server","transport":"stdio"}`)
	findings := ValidateMCPServerSchema("mcp/my-server.json", data)
	if len(findings) == 0 {
		t.Fatal("expected finding for stdio without command")
	}
}

func TestValidateMCPServerSchema_RuntimeFieldRejected(t *testing.T) {
	t.Parallel()
	data := []byte(`{"name":"x","transport":"stdio","command":["r"],"allowed_tools":["y"]}`)
	findings := ValidateMCPServerSchema("mcp/x.json", data)
	if len(findings) == 0 {
		t.Fatal("expected finding for runtime-only field")
	}
}

func TestValidateMCPServerSchema_ValidSSE(t *testing.T) {
	t.Parallel()
	data := []byte(`{"name":"my-server","transport":"sse","url":"http://localhost:8080"}`)
	findings := ValidateMCPServerSchema("mcp/my-server.json", data)
	if len(findings) > 0 {
		t.Fatalf("expected no findings, got %v", findings)
	}
}

func TestValidateMCPServerSchema_SSEMissingURL(t *testing.T) {
	t.Parallel()
	data := []byte(`{"name":"my-server","transport":"sse"}`)
	findings := ValidateMCPServerSchema("mcp/my-server.json", data)
	if len(findings) == 0 {
		t.Fatal("expected finding for SSE without url")
	}
}
