package harness

import (
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestRenderedContentName_RoundTrip(t *testing.T) {
	t.Parallel()

	name := RenderedContentName("starter-pack", "deploy-helper")
	if name != "deploy-helper__aipack__starter-pack" {
		t.Fatalf("RenderedContentName = %q", name)
	}

	id, ok := ParseRenderedContentName(name)
	if !ok {
		t.Fatal("expected rendered content name to parse")
	}
	if id.Pack != "starter-pack" || id.ID != "deploy-helper" {
		t.Fatalf("parsed identity = %+v", id)
	}
	if got := StripRenderedContentName(name); got != "deploy-helper" {
		t.Fatalf("StripRenderedContentName = %q", got)
	}
}

func TestContentName_RespectsNamespacedPolicy(t *testing.T) {
	t.Parallel()

	if got := ContentName(false, "pack-a", "deploy"); got != "deploy" {
		t.Fatalf("ContentName(false) = %q, want source id", got)
	}
	if got := ContentName(true, "pack-a", "deploy"); got != "deploy__aipack__pack-a" {
		t.Fatalf("ContentName(true) = %q, want rendered id", got)
	}
}

func TestRenderedSkillRefsForPack_PrefersSamePack(t *testing.T) {
	t.Parallel()

	got, err := RenderedSkillRefsForPack("pack-a", []string{"deploy"}, []domain.Skill{
		{Name: "deploy", SourcePack: "pack-b"},
		{Name: "deploy", SourcePack: "pack-a"},
	}, true)
	if err != nil {
		t.Fatalf("RenderedSkillRefsForPack: %v", err)
	}
	if len(got) != 1 || got[0] != "deploy__aipack__pack-a" {
		t.Fatalf("rendered skill refs = %v, want [deploy__aipack__pack-a]", got)
	}
}

func TestRenderedSkillRefsForPack_AmbiguousWithoutSamePack(t *testing.T) {
	t.Parallel()

	_, err := RenderedSkillRefsForPack("pack-c", []string{"deploy"}, []domain.Skill{
		{Name: "deploy", SourcePack: "pack-a"},
		{Name: "deploy", SourcePack: "pack-b"},
	}, true)
	if err == nil {
		t.Fatal("expected ambiguous skill reference error")
	}
}

func TestStripKnownRenderedContentName_RequiresKnownPack(t *testing.T) {
	t.Parallel()

	if got, ok := StripKnownRenderedContentName("deploy__aipack__pack-a", nil); ok || got != "deploy__aipack__pack-a" {
		t.Fatalf("without known packs got %q/%v, want original/false", got, ok)
	}
	if got, ok := StripKnownRenderedContentName("deploy__aipack__pack-a", map[string]struct{}{"pack-a": {}}); !ok || got != "deploy" {
		t.Fatalf("with known pack got %q/%v, want deploy/true", got, ok)
	}
}

func TestRenderedCategoryContentName_RuleSlashRoundTrip(t *testing.T) {
	t.Parallel()

	name := RenderedCategoryContentName("team-pack", domain.CategoryRules, "team-a/triage")
	if name != "team-a__triage__aipack__team-pack" {
		t.Fatalf("RenderedCategoryContentName = %q", name)
	}

	id, ok := StripRenderedCategoryContentName(name, domain.CategoryRules)
	if !ok {
		t.Fatal("expected rendered rule name to strip")
	}
	if id != "team-a/triage" {
		t.Fatalf("stripped id = %q", id)
	}
	if _, ok := StripRenderedCategoryPathName("team-a__triage", domain.CategoryRules); ok {
		t.Fatal("natural nested rule path should not be treated as namespaced")
	}
	if _, ok := StripRenderedCategoryPathName(name, domain.CategoryRules); ok {
		t.Fatal("new namespaced nested rule paths need frontmatter to disambiguate")
	}
}

func TestRewriteFrontmatterName(t *testing.T) {
	t.Parallel()

	raw := []byte("---\ndescription: Use when testing\nmetadata:\n  owner: team\n---\nBody\n")
	got, err := RewriteFrontmatterName(raw, "pack__probe")
	if err != nil {
		t.Fatalf("RewriteFrontmatterName: %v", err)
	}
	text := string(got)
	for _, want := range []string{"name: pack__probe", "description: Use when testing", "owner: team", "Body\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rewritten content missing %q:\n%s", want, text)
		}
	}
}
