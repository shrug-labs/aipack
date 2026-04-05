package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersionInfo_UsesBuildInfoFallback(t *testing.T) {
	orig := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "v0.17.0"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "49d4d252f441"},
			},
		}, true
	}
	t.Cleanup(func() { readBuildInfo = orig })

	version, commit := resolveVersionInfo("dev", "unknown")
	if version != "0.17.0" {
		t.Fatalf("version = %q, want %q", version, "0.17.0")
	}
	if commit != "49d4d25" {
		t.Fatalf("commit = %q, want %q", commit, "49d4d25")
	}
}

func TestResolveVersionInfo_PreservesLinkerValues(t *testing.T) {
	orig := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "v0.17.0"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "49d4d252f441"},
			},
		}, true
	}
	t.Cleanup(func() { readBuildInfo = orig })

	version, commit := resolveVersionInfo("0.17.0", "49d4d25")
	if version != "0.17.0" {
		t.Fatalf("version = %q, want %q", version, "0.17.0")
	}
	if commit != "49d4d25" {
		t.Fatalf("commit = %q, want %q", commit, "49d4d25")
	}
}

func TestResolveVersionInfo_MarksDirtyBuilds(t *testing.T) {
	orig := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "(devel)"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "49d4d252f441"},
				{Key: "vcs.modified", Value: "true"},
			},
		}, true
	}
	t.Cleanup(func() { readBuildInfo = orig })

	version, commit := resolveVersionInfo("dev", "unknown")
	if version != "dev" {
		t.Fatalf("version = %q, want %q", version, "dev")
	}
	if commit != "49d4d25-dirty" {
		t.Fatalf("commit = %q, want %q", commit, "49d4d25-dirty")
	}
}
