package domain

import "testing"

func TestPackCategoryPrimaryRelPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind PackCategory
		id   string
		want string
	}{
		{kind: CategoryRules, id: "triage", want: "rules/triage.md"},
		{kind: CategoryAgents, id: "reviewer", want: "agents/reviewer.md"},
		{kind: CategoryWorkflows, id: "ship", want: "workflows/ship.md"},
		{kind: CategorySkills, id: "oncall", want: "skills/oncall/SKILL.md"},
		// Slashed ids — only rules support nested authoring.
		{kind: CategoryRules, id: "group/triage", want: "rules/group/triage.md"},
		{kind: CategoryRules, id: "group/sub/deep", want: "rules/group/sub/deep.md"},
	}
	for _, tt := range tests {
		got := tt.kind.PrimaryRelPath(tt.id)
		if got != tt.want {
			t.Fatalf("%s.PrimaryRelPath(%q) = %q, want %q", tt.kind, tt.id, got, tt.want)
		}
		// Round-trip: MatchPrimaryContentFile on the produced path must
		// yield back the same category and id.
		gotK, gotID, gotOK := MatchPrimaryContentFile(got)
		if !gotOK || gotK != tt.kind || gotID != tt.id {
			t.Fatalf("round-trip MatchPrimaryContentFile(%q) = (%q, %q, %v), want (%q, %q, true)",
				got, gotK, gotID, gotOK, tt.kind, tt.id)
		}
	}
}

func TestMatchPrimaryContentFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		rel    string
		wantID string
		wantOK bool
		wantK  PackCategory
	}{
		// Rules: nested authoring; id preserves the slashed path.
		{rel: "rules/triage.md", wantID: "triage", wantOK: true, wantK: CategoryRules},
		{rel: "rules/group/triage.md", wantID: "group/triage", wantOK: true, wantK: CategoryRules},
		{rel: "rules/group/sub/deep.md", wantID: "group/sub/deep", wantOK: true, wantK: CategoryRules},
		// Agents: nested authoring; id is the leaf.
		{rel: "agents/reviewer.md", wantID: "reviewer", wantOK: true, wantK: CategoryAgents},
		{rel: "agents/team/reviewer.md", wantID: "reviewer", wantOK: true, wantK: CategoryAgents},
		{rel: "agents/team-a/sub/foo.md", wantID: "foo", wantOK: true, wantK: CategoryAgents},
		// Workflows: nested authoring; id is the leaf.
		{rel: "workflows/ship.md", wantID: "ship", wantOK: true, wantK: CategoryWorkflows},
		{rel: "workflows/team/ship.md", wantID: "ship", wantOK: true, wantK: CategoryWorkflows},
		// Skills: nested authoring; id is the leaf basename of the skill dir.
		{rel: "skills/oncall/SKILL.md", wantID: "oncall", wantOK: true, wantK: CategorySkills},
		{rel: "skills/grp/oncall/SKILL.md", wantID: "oncall", wantOK: true, wantK: CategorySkills},
		// Bundled assets inside a skill (anything not directly named SKILL.md)
		// — not a primary file. Discovery's SkipDir at first match means
		// nested SKILL.md inside another skill is captured as ITS OWN match
		// here at parse-time; the walker decides which directory wins.
		{rel: "skills/oncall/notes.md", wantOK: false},
		{rel: "skills/oncall/inner/SKILL.md", wantID: "inner", wantOK: true, wantK: CategorySkills},
		{rel: "skills/oncall/inner/notes.md", wantOK: false},
		{rel: "skills/SKILL.md", wantOK: false}, // no skill dir between category root and SKILL.md
		{rel: "docs/guide.md", wantOK: false},
		{rel: "rules", wantOK: false},  // no separator
		{rel: "rules/", wantOK: false}, // trailing slash
	}
	for _, tt := range tests {
		gotK, gotID, gotOK := MatchPrimaryContentFile(tt.rel)
		if gotOK != tt.wantOK || gotID != tt.wantID || gotK != tt.wantK {
			t.Fatalf("MatchPrimaryContentFile(%q) = (%q, %q, %v), want (%q, %q, %v)", tt.rel, gotK, gotID, gotOK, tt.wantK, tt.wantID, tt.wantOK)
		}
	}
}

// TestRuleHarnessFilenameRoundTrip pins the encoding contract: a slashed
// rule id encodes via `__` for the harness filename, and decoding the
// filename returns the original id verbatim. Other categories pass names
// through untouched (they're flat by definition).
func TestRuleHarnessFilenameRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id          string
		wantHarness string
	}{
		{id: "triage", wantHarness: "triage"},
		{id: "team-a/triage", wantHarness: "team-a__triage"},
		{id: "group/sub/deep", wantHarness: "group__sub__deep"},
	}
	for _, tt := range cases {
		gotHarness := CategoryRules.HarnessFilename(tt.id)
		if gotHarness != tt.wantHarness {
			t.Errorf("CategoryRules.HarnessFilename(%q) = %q, want %q", tt.id, gotHarness, tt.wantHarness)
		}
		gotID := CategoryRules.IDFromHarnessFilename(gotHarness)
		if gotID != tt.id {
			t.Errorf("CategoryRules.IDFromHarnessFilename(%q) = %q, want %q", gotHarness, gotID, tt.id)
		}
	}

	for _, cat := range []PackCategory{CategoryAgents, CategoryWorkflows, CategorySkills} {
		const id = "any-name"
		if got := cat.HarnessFilename(id); got != id {
			t.Errorf("%s.HarnessFilename(%q) = %q, want pass-through %q", cat, id, got, id)
		}
		if got := cat.IDFromHarnessFilename(id); got != id {
			t.Errorf("%s.IDFromHarnessFilename(%q) = %q, want pass-through %q", cat, id, got, id)
		}
		// Even names that look encoded — like `team__deploy` — must not be
		// decoded for non-rule categories. The id IS the invocation handle.
		const literalUnderscores = "team__deploy"
		if got := cat.IDFromHarnessFilename(literalUnderscores); got != literalUnderscores {
			t.Errorf("%s.IDFromHarnessFilename(%q) decoded; non-rules must pass through", cat, literalUnderscores)
		}
	}
}

func TestHasFrontmatterPrefix(t *testing.T) {
	t.Parallel()
	if !HasFrontmatterPrefix([]byte("---\nname: test\n")) {
		t.Fatal("expected leading frontmatter marker to be detected")
	}
	if HasFrontmatterPrefix([]byte("body\n---\n")) {
		t.Fatal("did not expect later delimiter to count as leading frontmatter")
	}
}
