package engine

import "testing"

func TestClassifySettings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name             string
		hasManagedKeys   bool
		hasBase          bool
		skipSettings     bool
		wantEmitSettings bool
		wantEmitMCP      bool
		wantMergeMode    bool
	}{
		{name: "managed_and_base", hasManagedKeys: true, hasBase: true, wantEmitSettings: true},
		{name: "managed_only", hasManagedKeys: true, wantEmitSettings: true},
		{name: "base_only", hasBase: true, wantEmitSettings: true},
		{name: "nothing" /* all false */},

		{name: "skip_managed_and_base", hasManagedKeys: true, hasBase: true, skipSettings: true, wantEmitMCP: true, wantMergeMode: true},
		{name: "skip_managed_only", hasManagedKeys: true, skipSettings: true, wantEmitMCP: true, wantMergeMode: true},
		{name: "skip_base_only", hasBase: true, skipSettings: true /* nothing emitted */},
		{name: "skip_nothing", skipSettings: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := ClassifySettings(tc.hasManagedKeys, tc.hasBase, tc.skipSettings)
			if d.EmitSettings != tc.wantEmitSettings {
				t.Errorf("EmitSettings = %v, want %v", d.EmitSettings, tc.wantEmitSettings)
			}
			if d.EmitMCP != tc.wantEmitMCP {
				t.Errorf("EmitMCP = %v, want %v", d.EmitMCP, tc.wantEmitMCP)
			}
			if d.MergeMode != tc.wantMergeMode {
				t.Errorf("MergeMode = %v, want %v", d.MergeMode, tc.wantMergeMode)
			}
		})
	}
}
