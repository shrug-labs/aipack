package engine

import "testing"

func TestClassifySettings_FullSettings(t *testing.T) {
	t.Parallel()
	d := ClassifySettings(true, true, false)
	if !d.EmitSettings {
		t.Error("expected EmitSettings=true when skipSettings=false and hasManagedContent=true")
	}
	if d.EmitMCP {
		t.Error("expected EmitMCP=false when EmitSettings=true")
	}
	if d.MergeMode {
		t.Error("expected MergeMode=false for full settings")
	}
}

func TestClassifySettings_SkipSettingsWithMCP(t *testing.T) {
	t.Parallel()
	d := ClassifySettings(true, true, true)
	if d.EmitSettings {
		t.Error("expected EmitSettings=false when skipSettings=true")
	}
	if !d.EmitMCP {
		t.Error("expected EmitMCP=true when skipSettings=true and hasMCP=true")
	}
	if !d.MergeMode {
		t.Error("expected MergeMode=true when skipSettings=true")
	}
}

func TestClassifySettings_NoContent(t *testing.T) {
	t.Parallel()
	d := ClassifySettings(false, false, false)
	if d.EmitSettings {
		t.Error("expected EmitSettings=false when no content")
	}
	if d.EmitMCP {
		t.Error("expected EmitMCP=false when no content")
	}
}

func TestClassifySettings_NoContentSkipSettings(t *testing.T) {
	t.Parallel()
	d := ClassifySettings(false, false, true)
	if d.EmitSettings || d.EmitMCP {
		t.Error("expected nothing emitted when no content and skipSettings")
	}
}

func TestClassifySettings_MCPOnlyNoSkip(t *testing.T) {
	t.Parallel()
	d := ClassifySettings(true, false, false)
	if !d.EmitSettings {
		t.Error("expected EmitSettings=true when hasMCP=true and skipSettings=false")
	}
}
