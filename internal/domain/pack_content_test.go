package domain

import "testing"

func TestAuthoredContentPrimaryRelPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind AuthoredContentKind
		id   string
		want string
	}{
		{kind: ContentRules, id: "triage", want: "rules/triage.md"},
		{kind: ContentAgents, id: "reviewer", want: "agents/reviewer.md"},
		{kind: ContentWorkflows, id: "ship", want: "workflows/ship.md"},
		{kind: ContentSkills, id: "oncall", want: "skills/oncall/SKILL.md"},
	}
	for _, tt := range tests {
		if got := tt.kind.PrimaryRelPath(tt.id); got != tt.want {
			t.Fatalf("%s.PrimaryRelPath(%q) = %q, want %q", tt.kind, tt.id, got, tt.want)
		}
	}
}

func TestMatchPrimaryContentFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		rel    string
		wantID string
		wantOK bool
		wantK  AuthoredContentKind
	}{
		{rel: "rules/triage.md", wantID: "triage", wantOK: true, wantK: ContentRules},
		{rel: "agents/reviewer.md", wantID: "reviewer", wantOK: true, wantK: ContentAgents},
		{rel: "workflows/ship.md", wantID: "ship", wantOK: true, wantK: ContentWorkflows},
		{rel: "skills/oncall/SKILL.md", wantID: "oncall", wantOK: true, wantK: ContentSkills},
		{rel: "skills/oncall/notes.md", wantOK: false},
		{rel: "docs/guide.md", wantOK: false},
	}
	for _, tt := range tests {
		gotK, gotID, gotOK := MatchPrimaryContentFile(tt.rel)
		if gotOK != tt.wantOK || gotID != tt.wantID || gotK != tt.wantK {
			t.Fatalf("MatchPrimaryContentFile(%q) = (%q, %q, %v), want (%q, %q, %v)", tt.rel, gotK, gotID, gotOK, tt.wantK, tt.wantID, tt.wantOK)
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
