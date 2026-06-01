package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
)

func findingExists(findings []config.Finding, path, message string) bool {
	for _, f := range findings {
		if f.Path == path && f.Message == message {
			return true
		}
	}
	return false
}

func TestRunPackValidate_MissingFrontmatterFinding(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, "rules", "missing-frontmatter.md"), "body\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if rep.OK {
		t.Fatal("expected invalid pack")
	}
	if !findingExists(rep.Findings, "rules/missing-frontmatter.md", "missing YAML frontmatter") {
		t.Fatalf("expected frontmatter finding, got %v", rep.Findings)
	}
}

func TestRunPackValidate_MissingFrontmatterFindingAcrossAuthoredKinds(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, "agents", "reviewer.md"), "body\n")
	writeFile(t, filepath.Join(packDir, "workflows", "ship.md"), "body\n")
	writeFile(t, filepath.Join(packDir, "skills", "triage", "SKILL.md"), "body\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if rep.OK {
		t.Fatal("expected invalid pack")
	}
	for _, want := range []struct{ path, msg string }{
		{"agents/reviewer.md", "missing YAML frontmatter"},
		{"workflows/ship.md", "missing YAML frontmatter"},
		{"skills/triage/SKILL.md", "missing YAML frontmatter"},
	} {
		if !findingExists(rep.Findings, want.path, want.msg) {
			t.Fatalf("expected finding %q at %q, got %v", want.msg, want.path, rep.Findings)
		}
	}
}

func TestRunPackValidate_UsesDiscoveredNestedContentPaths(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, "agents", "review", "reviewer.md"), "---\nname: reviewer\ndescription: test\n---\nbody\n")
	writeFile(t, filepath.Join(packDir, "workflows", "release", "ship.md"), "---\nname: ship\ndescription: test\n---\nbody\n")
	writeFile(t, filepath.Join(packDir, "skills", "ops", "triage", "SKILL.md"), "---\nname: triage\ndescription: Use when testing nested validation paths.\n---\nbody\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if !rep.OK {
		t.Fatalf("expected nested content to validate through discovered paths, got %v", rep.Findings)
	}
}

func TestRunPackValidate_SkillSupportingFilesDoNotRequireFrontmatter(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, "skills", "triage", "notes.md"), "body\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if !rep.OK {
		t.Fatalf("expected supporting skill markdown to be ignored, got %v", rep.Findings)
	}
}

func TestRunPackValidate_LeadingFrontmatterMarkerCountsAsPresent(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, "rules", "open-frontmatter.md"), "---\nname: broken\nbody\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if findingExists(rep.Findings, "rules/open-frontmatter.md", "missing YAML frontmatter") {
		t.Fatalf("expected leading frontmatter marker to count as present, got %v", rep.Findings)
	}
}

func TestRunPackValidate_DoesNotRequireContentDirectoriesWhenManifestVectorsAreEmpty(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"schema_version":2,"name":"demo","version":"0.1.0","root":".","rules":[],"agents":[],"workflows":[],"skills":[],"mcp":[],"configs":{"harness_settings":{}}}`)

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if !rep.OK {
		t.Fatalf("expected empty vectors to allow missing content dirs, got %v", rep.Findings)
	}
}

func TestRunPackValidate_DoesNotRejectDotEnvFiles(t *testing.T) {
	t.Parallel()
	packDir := writePackValidateFixture(t)
	writeFile(t, filepath.Join(packDir, ".env.production"), "SECRET=1\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	if !rep.OK {
		t.Fatalf("expected validate to ignore non-pack-shape files, got %v", rep.Findings)
	}
}

func writePackValidateFixture(t *testing.T) string {
	t.Helper()
	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"schema_version":2,"name":"demo","version":"0.1.0","root":".","rules":[],"agents":[],"workflows":[],"skills":[],"mcp":[],"configs":{"harness_settings":{}}}`)
	if err := os.MkdirAll(filepath.Join(packDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packDir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"agents", "workflows", "skills"} {
		if err := os.MkdirAll(filepath.Join(packDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return packDir
}

func TestRunPackValidate_FrontmatterMissingDescription(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"schema_version":2,"name":"demo","root":".","rules":["no-desc"],"agents":[],"workflows":[],"skills":[]}`)
	writeFile(t, filepath.Join(packDir, "rules", "no-desc.md"), "---\nname: no-desc\n---\nbody\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	// Should still be OK=true since frontmatter issues are warnings.
	found := false
	for _, f := range rep.Findings {
		if f.Path == "rules/no-desc.md" && f.Category == config.FindingCategoryFrontmatter {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected frontmatter warning for missing description, got %v", rep.Findings)
	}
	if !rep.OK {
		t.Fatal("frontmatter warnings should not set OK=false")
	}
}

func TestRunPackValidate_AgentUnknownMCPServer(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"schema_version":2,"name":"demo","root":".","rules":[],"agents":["bad"],"workflows":[],"skills":[],"mcp":[]}`)
	writeFile(t, filepath.Join(packDir, "agents", "bad.md"), "---\nname: bad\ndescription: test\nmcp_servers:\n  - nonexistent\n---\nbody\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	found := false
	for _, f := range rep.Findings {
		if f.Category == config.FindingCategoryConsistency {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected consistency warning for unknown mcp_server, got %v", rep.Findings)
	}
}

func TestRunPackValidate_AgentUnknownFieldEmitsWarning(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"schema_version":2,"name":"demo","root":".","rules":[],"agents":["typo"],"workflows":[],"skills":[]}`)
	writeFile(t, filepath.Join(packDir, "agents", "typo.md"),
		"---\nname: typo\ndescription: test\ndissallowed_tools:\n  - Bash\n---\nbody\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	found := false
	for _, f := range rep.Findings {
		if f.Path == "agents/typo.md" && f.Category == config.FindingCategoryFrontmatter &&
			f.Severity == config.FindingSeverityWarning {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected frontmatter warning for unknown field, got %v", rep.Findings)
	}
}

func TestRunPackValidate_MalformedFrontmatterEmitsWarning(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"schema_version":2,"name":"demo","root":".","rules":[],"agents":["bad"],"workflows":[],"skills":[]}`)
	// tools should be a list, not a string — yaml.Unmarshal will error
	writeFile(t, filepath.Join(packDir, "agents", "bad.md"),
		"---\nname: bad\ndescription: test\ntools: not-a-list\n---\nbody\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	found := false
	for _, f := range rep.Findings {
		if f.Path == "agents/bad.md" && f.Category == config.FindingCategoryFrontmatter {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected frontmatter warning for malformed YAML, got %v", rep.Findings)
	}
}

func TestRunPackValidate_EmptyFrontmatterBlockEmitsWarning(t *testing.T) {
	t.Parallel()
	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"schema_version":2,"name":"demo","root":".","rules":["empty-fm"],"agents":[],"workflows":[],"skills":[]}`)
	// Frontmatter markers present but no content between them — unclosed delimiter.
	writeFile(t, filepath.Join(packDir, "rules", "empty-fm.md"), "---\nname: broken\nbody\n")

	rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
	found := false
	for _, f := range rep.Findings {
		if f.Path == "rules/empty-fm.md" && f.Category == config.FindingCategoryFrontmatter {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected frontmatter warning for empty/malformed frontmatter, got %v", rep.Findings)
	}
}

func TestRunPackValidate_Extras(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		extras  string                         // JSON array value for "extras"
		setup   func(t *testing.T, dir string) // create files/dirs the test needs
		wantOK  bool
		wantMsg string // substring expected in a finding message (ignored when wantOK)
	}{
		// --- valid cases ---
		{
			name:   "directory",
			extras: `["wrappers"]`,
			setup:  func(t *testing.T, d string) { os.MkdirAll(filepath.Join(d, "wrappers"), 0o755) },
			wantOK: true,
		},
		{
			name:   "file",
			extras: `["proxy.py"]`,
			setup:  func(t *testing.T, d string) { writeFile(t, filepath.Join(d, "proxy.py"), "#!/usr/bin/env python3\n") },
			wantOK: true,
		},
		{
			name:   "mixed files and dirs",
			extras: `["wrappers","bootstrap.sh"]`,
			setup: func(t *testing.T, d string) {
				os.MkdirAll(filepath.Join(d, "wrappers"), 0o755)
				writeFile(t, filepath.Join(d, "bootstrap.sh"), "#!/bin/sh\n")
			},
			wantOK: true,
		},

		// --- rejected: missing ---
		{
			name:    "missing path",
			extras:  `["nonexistent"]`,
			wantMsg: `extras path "nonexistent" not found`,
		},

		// --- valid: repo-relative ---
		{
			name:   "parent-relative directory",
			extras: `["../shared-scripts"]`,
			setup: func(t *testing.T, d string) {
				os.MkdirAll(filepath.Join(d, "..", "shared-scripts"), 0o755)
			},
			wantOK: true,
		},

		// --- rejected: path safety ---
		{
			name:    "absolute path",
			extras:  `["/tmp/escape"]`,
			wantMsg: `must be relative`,
		},

		// --- rejected: degenerate entries ---
		{
			name:    "empty string",
			extras:  `[""]`,
			wantMsg: `must not be empty or '.'`,
		},
		{
			name:    "dot resolves to pack root",
			extras:  `["."]`,
			wantMsg: `must not be empty or '.'`,
		},
		{
			name:    "only dotdot resolves to empty staging name",
			extras:  `[".."]`,
			wantMsg: `resolves to empty staging name`,
		},

		// --- rejected: structural ---
		{
			name:    "overlaps standard content dir",
			extras:  `["rules"]`,
			setup:   func(t *testing.T, d string) { os.MkdirAll(filepath.Join(d, "rules"), 0o755) },
			wantMsg: `conflicts with standard content directory`,
		},
		{
			name:    "parent-relative overlaps standard content dir",
			extras:  `["../rules"]`,
			setup:   func(t *testing.T, d string) { os.MkdirAll(filepath.Join(d, "..", "rules"), 0o755) },
			wantMsg: `conflicts with standard content directory`,
		},
		{
			name:    "duplicate entry",
			extras:  `["wrappers","wrappers"]`,
			setup:   func(t *testing.T, d string) { os.MkdirAll(filepath.Join(d, "wrappers"), 0o755) },
			wantMsg: `collides with another extras entry`,
		},
		{
			name:   "staging name collision between local and parent-relative",
			extras: `["scripts","../scripts"]`,
			setup: func(t *testing.T, d string) {
				os.MkdirAll(filepath.Join(d, "scripts"), 0o755)
				os.MkdirAll(filepath.Join(d, "..", "scripts"), 0o755)
			},
			wantMsg: `collides with another extras entry`,
		},
		{
			name:   "prefix containment",
			extras: `["shared","shared/sub"]`,
			setup: func(t *testing.T, d string) {
				os.MkdirAll(filepath.Join(d, "shared", "sub"), 0o755)
			},
			wantMsg: `overlaps with`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			packDir := t.TempDir()
			writeFile(t, filepath.Join(packDir, "pack.json"),
				`{"schema_version":2,"name":"test","root":".","extras":`+tt.extras+`}`)
			if tt.setup != nil {
				tt.setup(t, packDir)
			}

			rep := RunPackValidate(PackValidateRequest{PackRoot: packDir})
			if tt.wantOK {
				if !rep.OK {
					t.Fatalf("expected valid, got findings: %v", rep.Findings)
				}
				return
			}
			if rep.OK {
				t.Fatal("expected validation to fail")
			}
			if tt.wantMsg != "" {
				found := false
				for _, f := range rep.Findings {
					if strings.Contains(f.Message, tt.wantMsg) {
						found = true
					}
				}
				if !found {
					t.Fatalf("expected finding containing %q, got %v", tt.wantMsg, rep.Findings)
				}
			}
		})
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
