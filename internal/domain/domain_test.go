package domain

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestSplitFrontmatter_WithFrontmatter(t *testing.T) {
	t.Parallel()
	raw := []byte("---\ntitle: hello\n---\nbody content\n")
	fm, body, err := SplitFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(fm) != "title: hello" {
		t.Errorf("frontmatter = %q, want %q", fm, "title: hello")
	}
	if string(body) != "body content\n" {
		t.Errorf("body = %q, want %q", body, "body content\n")
	}
}

func TestSplitFrontmatter_NoFrontmatter(t *testing.T) {
	t.Parallel()
	raw := []byte("just a body\n")
	fm, body, err := SplitFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	if fm != nil {
		t.Errorf("frontmatter should be nil, got %q", fm)
	}
	if string(body) != "just a body\n" {
		t.Errorf("body = %q, want %q", body, "just a body\n")
	}
}

func TestSplitFrontmatter_CRLFVariant(t *testing.T) {
	t.Parallel()
	raw := []byte("---\r\ntitle: hello\r\n---\r\nbody\r\n")
	fm, body, err := SplitFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(fm) != "title: hello" {
		t.Errorf("frontmatter = %q, want %q", fm, "title: hello")
	}
	if string(body) != "body\r\n" {
		t.Errorf("body = %q, want %q", body, "body\r\n")
	}
}

func TestSplitFrontmatter_UnclosedDelimiter(t *testing.T) {
	t.Parallel()
	raw := []byte("---\ntitle: hello\nbody without closing\n")
	fm, body, err := SplitFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	if fm != nil {
		t.Errorf("frontmatter should be nil for unclosed delimiter, got %q", fm)
	}
	if string(body) != string(raw) {
		t.Errorf("body should be full raw content")
	}
}

func TestHasFrontmatterPrefix_UnclosedDelimiterCountsAsPresent(t *testing.T) {
	t.Parallel()
	raw := []byte("---\ntitle: hello\nbody without closing\n")
	if !HasFrontmatterPrefix(raw) {
		t.Fatal("expected leading delimiter to count as frontmatter prefix")
	}
	fm, _, err := SplitFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	if fm != nil {
		t.Fatalf("expected SplitFrontmatter to reject unclosed frontmatter, got %q", fm)
	}
}

func TestHasFrontmatterPrefix_MalformedLeadingDelimiter(t *testing.T) {
	t.Parallel()
	raw := []byte("----\nnot really frontmatter\n")
	if HasFrontmatterPrefix(raw) {
		t.Fatal("expected malformed leading delimiter (----) to be rejected by HasFrontmatterPrefix")
	}
	fm, _, err := SplitFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	if fm != nil {
		t.Fatalf("expected SplitFrontmatter to reject malformed delimiter, got %q", fm)
	}
}

func TestFragment_Apply(t *testing.T) {
	t.Parallel()
	plan := Plan{Desired: map[string]struct{}{}}
	frag := Fragment{
		Writes:  []WriteAction{{Dst: "/a/b.md", Content: []byte("hello")}},
		Copies:  []CopyAction{{Src: "/src", Dst: "/dst", Kind: CopyKindFile}},
		Desired: []string{"/a/b.md", "/dst"},
	}
	frag.Apply(&plan)
	if len(plan.Writes) != 1 {
		t.Errorf("plan.Writes = %d, want 1", len(plan.Writes))
	}
	if len(plan.Copies) != 1 {
		t.Errorf("plan.Copies = %d, want 1", len(plan.Copies))
	}
	if len(plan.Desired) != 2 {
		t.Errorf("plan.Desired = %d, want 2", len(plan.Desired))
	}
}

func TestFragment_AddRuleWrites(t *testing.T) {
	t.Parallel()
	f := Fragment{}
	rules := []Rule{
		{Name: "alpha", Raw: []byte("alpha-content"), SourcePack: "pack1", SourcePath: "/src/alpha.md"},
		{Name: "beta", Raw: []byte("beta-content"), SourcePack: "pack1", SourcePath: "/src/beta.md"},
	}
	f.AddRuleWrites("/project", ".claude/rules", rules)
	if len(f.Writes) != 2 {
		t.Fatalf("writes = %d, want 2", len(f.Writes))
	}
	if f.Writes[0].Dst != filepath.Join("/project", ".claude/rules", "alpha.md") {
		t.Errorf("writes[0].Dst = %q", f.Writes[0].Dst)
	}
	if string(f.Writes[0].Content) != "alpha-content" {
		t.Errorf("writes[0].Content = %q", f.Writes[0].Content)
	}
	if f.Writes[0].SourcePack != "pack1" {
		t.Errorf("writes[0].SourcePack = %q", f.Writes[0].SourcePack)
	}
	if len(f.Desired) != 2 {
		t.Errorf("desired = %d, want 2", len(f.Desired))
	}
}

func TestFragment_AddWorkflowWrites(t *testing.T) {
	t.Parallel()
	f := Fragment{}
	workflows := []Workflow{
		{Name: "deploy", Raw: []byte("deploy-steps"), SourcePack: "pack1"},
	}
	f.AddWorkflowWrites("/project", ".claude/commands", workflows)
	if len(f.Writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(f.Writes))
	}
	if f.Writes[0].Dst != filepath.Join("/project", ".claude/commands", "deploy.md") {
		t.Errorf("writes[0].Dst = %q", f.Writes[0].Dst)
	}
}

func TestFragment_AddSkillCopies(t *testing.T) {
	t.Parallel()
	f := Fragment{}
	skills := []Skill{
		{Name: "onboard", DirPath: "/pack/skills/onboard", SourcePack: "pack1"},
	}
	f.AddSkillCopies("/project", ".claude/skills", skills)
	if len(f.Copies) != 1 {
		t.Fatalf("copies = %d, want 1", len(f.Copies))
	}
	if f.Copies[0].Kind != CopyKindDir {
		t.Errorf("copies[0].Kind = %q, want %q", f.Copies[0].Kind, CopyKindDir)
	}
	if f.Copies[0].Src != "/pack/skills/onboard" {
		t.Errorf("copies[0].Src = %q", f.Copies[0].Src)
	}
}

func TestFragment_AddSkillCopies_ExpandsFiles(t *testing.T) {
	t.Parallel()
	// Create a real skill directory with files to verify walk expansion.
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(srcDir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "helper.md"), []byte("helper"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := Fragment{}
	f.AddSkillCopies("/project", ".claude/skills", []Skill{
		{Name: "deploy", DirPath: srcDir, SourcePack: "pack1"},
	})

	// Desired should contain the directory + both expanded file paths.
	want := map[string]bool{
		filepath.Join("/project", ".claude/skills", "deploy"):                  true,
		filepath.Join("/project", ".claude/skills", "deploy", "SKILL.md"):      true,
		filepath.Join("/project", ".claude/skills", "deploy", "sub/helper.md"): true,
	}
	if len(f.Desired) != len(want) {
		t.Fatalf("desired = %v, want %d entries", f.Desired, len(want))
	}
	for _, d := range f.Desired {
		if !want[d] {
			t.Errorf("unexpected desired entry: %s", d)
		}
	}
}

func TestLedger_Record(t *testing.T) {
	t.Parallel()
	lg := NewLedger()
	content := []byte("hello world")
	lg.Record("/tmp/test-file", content, "mypack", nil, time.Now())
	e, ok := lg.Managed["/tmp/test-file"]
	if !ok {
		t.Fatal("expected ledger entry")
	}
	if e.Digest == "" {
		t.Error("digest should be set")
	}
	if e.SourcePack != "mypack" {
		t.Errorf("SourcePack = %q, want %q", e.SourcePack, "mypack")
	}
	if e.SyncedAtEpochS == 0 {
		t.Error("SyncedAtEpochS should be set")
	}
	// mtime will be 0 since /tmp/test-file doesn't exist — that's fine (best-effort).
}

func TestLedger_UpdateMetadata(t *testing.T) {
	t.Parallel()
	lg := NewLedger()
	lg.Managed["/a/b"] = Entry{Digest: "abc123", SourcePack: "old"}
	lg.UpdateMetadata("/a/b", "newpack", []byte("overlay"), time.Now())
	e := lg.Managed["/a/b"]
	if e.SourcePack != "newpack" {
		t.Errorf("SourcePack = %q, want %q", e.SourcePack, "newpack")
	}
	if string(e.ManagedOverlay) != "overlay" {
		t.Errorf("ManagedOverlay = %q, want %q", e.ManagedOverlay, "overlay")
	}
	if e.Digest != "abc123" {
		t.Errorf("Digest should be unchanged, got %q", e.Digest)
	}
}

func TestLedger_UpdateMetadata_Nonexistent(t *testing.T) {
	t.Parallel()
	lg := NewLedger()
	lg.UpdateMetadata("/does/not/exist", "pack", nil, time.Now())
	if len(lg.Managed) != 0 {
		t.Error("UpdateMetadata on nonexistent key should be a no-op")
	}
}

func TestLedger_Delete(t *testing.T) {
	t.Parallel()
	lg := NewLedger()
	lg.Managed["/a/b"] = Entry{Digest: "abc"}
	lg.Delete("/a/b")
	if _, ok := lg.Managed["/a/b"]; ok {
		t.Error("entry should be deleted")
	}
}

func TestLedger_PrevDigest(t *testing.T) {
	t.Parallel()
	lg := NewLedger()
	lg.Managed["/a/b"] = Entry{Digest: "abc123"}
	if got := lg.PrevDigest("/a/b"); got != "abc123" {
		t.Errorf("PrevDigest = %q, want %q", got, "abc123")
	}
	if got := lg.PrevDigest("/not/found"); got != "" {
		t.Errorf("PrevDigest for missing = %q, want empty", got)
	}
}

func TestLedger_PrevManagedOverlay(t *testing.T) {
	t.Parallel()
	lg := NewLedger()
	lg.Managed["/a/b"] = Entry{ManagedOverlay: []byte("overlay")}
	if got := lg.PrevManagedOverlay("/a/b"); string(got) != "overlay" {
		t.Errorf("PrevManagedOverlay = %q, want %q", got, "overlay")
	}
	if got := lg.PrevManagedOverlay("/not/found"); got != nil {
		t.Errorf("PrevManagedOverlay for missing = %v, want nil", got)
	}
}

func TestSettingsBundle_FileBytes(t *testing.T) {
	t.Parallel()
	b := SettingsBundle{
		HarnessClaudeCode: []ConfigFile{
			{Filename: "settings.local.json", Content: []byte(`{"key":"val"}`), SourcePack: "pack1"},
		},
	}
	got := b.FileBytes(HarnessClaudeCode, "settings.local.json")
	if string(got) != `{"key":"val"}` {
		t.Errorf("FileBytes = %q", got)
	}
	if got := b.FileBytes(HarnessClaudeCode, "missing.json"); got != nil {
		t.Errorf("FileBytes for missing = %v, want nil", got)
	}
	if got := b.FileBytes(HarnessOpenCode, "settings.local.json"); got != nil {
		t.Errorf("FileBytes for wrong harness = %v, want nil", got)
	}
}

func TestSettingsBundle_SourcePack(t *testing.T) {
	t.Parallel()
	b := SettingsBundle{
		HarnessClaudeCode: []ConfigFile{
			{Filename: "settings.local.json", SourcePack: "pack1"},
		},
	}
	if got := b.SourcePack(HarnessClaudeCode, "settings.local.json"); got != "pack1" {
		t.Errorf("SourcePack = %q, want %q", got, "pack1")
	}
}

func TestParseHarness(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  Harness
		ok    bool
	}{
		{"claudecode", HarnessClaudeCode, true},
		{"ClaudeCode", HarnessClaudeCode, true},
		{"opencode", HarnessOpenCode, true},
		{"codex", HarnessCodex, true},
		{"cline", HarnessCline, true},
		{"unknown", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseHarness(tt.input)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseHarness(%q) = (%q, %v), want (%q, %v)", tt.input, got, ok, tt.want, tt.ok)
		}
	}
}

func TestAllHarnesses_ReturnsCopy(t *testing.T) {
	t.Parallel()
	a := AllHarnesses()
	b := AllHarnesses()
	a[0] = "mutated"
	if b[0] == "mutated" {
		t.Error("AllHarnesses should return a copy")
	}
}

func TestNewProfile_MapsInitialized(t *testing.T) {
	t.Parallel()
	p := NewProfile()
	if p.Params == nil {
		t.Error("NewProfile().Params should be non-nil")
	}
	if p.SettingsPack != "" {
		t.Error("NewProfile().SettingsPack should be empty")
	}
}

func TestProfile_AllRules_MultiPack(t *testing.T) {
	t.Parallel()
	p := NewProfile()
	p.Packs = []Pack{
		{Name: "pack-a", Rules: []Rule{{Name: "r1", SourcePack: "pack-a"}, {Name: "r2", SourcePack: "pack-a"}}},
		{Name: "pack-b", Rules: []Rule{{Name: "r3", SourcePack: "pack-b"}, {Name: "r4", SourcePack: "pack-b"}}},
	}
	rules := p.AllRules()
	if len(rules) != 4 {
		t.Fatalf("AllRules() returned %d rules, want 4", len(rules))
	}
	want := []string{"r1", "r2", "r3", "r4"}
	for i, r := range rules {
		if r.Name != want[i] {
			t.Errorf("rules[%d].Name = %q, want %q", i, r.Name, want[i])
		}
	}
}

func TestProfile_AllAgents_MultiPack(t *testing.T) {
	t.Parallel()
	p := NewProfile()
	p.Packs = []Pack{
		{Name: "pack-a", Agents: []Agent{{Name: "a1", SourcePack: "pack-a"}, {Name: "a2", SourcePack: "pack-a"}}},
		{Name: "pack-b", Agents: []Agent{{Name: "a3", SourcePack: "pack-b"}, {Name: "a4", SourcePack: "pack-b"}}},
	}
	agents := p.AllAgents()
	if len(agents) != 4 {
		t.Fatalf("AllAgents() returned %d agents, want 4", len(agents))
	}
	want := []string{"a1", "a2", "a3", "a4"}
	for i, a := range agents {
		if a.Name != want[i] {
			t.Errorf("agents[%d].Name = %q, want %q", i, a.Name, want[i])
		}
	}
}

func TestProfile_HasContent_True(t *testing.T) {
	t.Parallel()
	p := NewProfile()
	p.Packs = []Pack{
		{Name: "pack-a", Rules: []Rule{{Name: "r1"}}},
	}
	if !p.HasContent() {
		t.Error("HasContent() = false, want true for profile with rules")
	}
}

func TestProfile_HasContent_False(t *testing.T) {
	t.Parallel()
	p := NewProfile()
	if p.HasContent() {
		t.Error("HasContent() = true, want false for empty profile")
	}
}

func TestProfile_RuleDirs(t *testing.T) {
	t.Parallel()
	p := NewProfile()
	p.Packs = []Pack{
		{Name: "pack-a", Root: "/packs/alpha", Rules: []Rule{{Name: "r1"}}},
		{Name: "pack-b", Root: "/packs/beta", Rules: []Rule{{Name: "r2"}}},
		{Name: "pack-c", Root: "/packs/alpha", Rules: []Rule{{Name: "r3"}}}, // duplicate root
	}
	dirs := p.RuleDirs()
	want := []string{
		filepath.Join("/packs/alpha", "rules"),
		filepath.Join("/packs/beta", "rules"),
	}
	sort.Strings(want)
	if len(dirs) != len(want) {
		t.Fatalf("RuleDirs() returned %d dirs, want %d", len(dirs), len(want))
	}
	for i, d := range dirs {
		if d != want[i] {
			t.Errorf("dirs[%d] = %q, want %q", i, d, want[i])
		}
	}
}

func TestProfile_HasContent_SkillsOnly(t *testing.T) {
	t.Parallel()
	p := NewProfile()
	p.Packs = []Pack{
		{Name: "pack-a", Skills: []Skill{{Name: "onboard", DirPath: "/skills/onboard"}}},
	}
	if !p.HasContent() {
		t.Error("HasContent() = false, want true for profile with skills only")
	}
}

func TestProfile_AllWorkflows_MultiPack(t *testing.T) {
	t.Parallel()
	p := NewProfile()
	p.Packs = []Pack{
		{Name: "pack-a", Workflows: []Workflow{{Name: "w1", SourcePack: "pack-a"}, {Name: "w2", SourcePack: "pack-a"}}},
		{Name: "pack-b", Workflows: []Workflow{{Name: "w3", SourcePack: "pack-b"}}},
	}
	workflows := p.AllWorkflows()
	if len(workflows) != 3 {
		t.Fatalf("AllWorkflows() returned %d, want 3", len(workflows))
	}
	want := []string{"w1", "w2", "w3"}
	for i, w := range workflows {
		if w.Name != want[i] {
			t.Errorf("workflows[%d].Name = %q, want %q", i, w.Name, want[i])
		}
	}
}

func TestProfile_AllSkills_MultiPack(t *testing.T) {
	t.Parallel()
	p := NewProfile()
	p.Packs = []Pack{
		{Name: "pack-a", Skills: []Skill{{Name: "s1", SourcePack: "pack-a"}}},
		{Name: "pack-b", Skills: []Skill{{Name: "s2", SourcePack: "pack-b"}, {Name: "s3", SourcePack: "pack-b"}}},
	}
	skills := p.AllSkills()
	if len(skills) != 3 {
		t.Fatalf("AllSkills() returned %d, want 3", len(skills))
	}
	want := []string{"s1", "s2", "s3"}
	for i, s := range skills {
		if s.Name != want[i] {
			t.Errorf("skills[%d].Name = %q, want %q", i, s.Name, want[i])
		}
	}
}

func TestLedger_PrevDigest_CleanedPath(t *testing.T) {
	t.Parallel()
	lg := NewLedger()
	lg.Record("/a/b/c", []byte("content"), "pack1", nil, time.Now())
	// Lookup with un-cleaned path should still find it.
	if got := lg.PrevDigest("/a/b/../b/c"); got == "" {
		t.Error("PrevDigest should find entry via un-cleaned path")
	}
}

func TestLedger_PrevManagedOverlay_CleanedPath(t *testing.T) {
	t.Parallel()
	lg := NewLedger()
	lg.Record("/a/b/c", []byte("content"), "pack1", []byte("overlay"), time.Now())
	if got := lg.PrevManagedOverlay("/a/b/../b/c"); got == nil {
		t.Error("PrevManagedOverlay should find entry via un-cleaned path")
	}
}

func TestProfile_SettingsPackName(t *testing.T) {
	t.Parallel()
	p := NewProfile()
	p.SettingsPack = "my-pack"

	// Same pack name returned regardless of which harness is asked.
	got := p.SettingsPackName(HarnessClaudeCode)
	if got != "my-pack" {
		t.Errorf("SettingsPackName(HarnessClaudeCode) = %q, want %q", got, "my-pack")
	}
	got = p.SettingsPackName(HarnessOpenCode)
	if got != "my-pack" {
		t.Errorf("SettingsPackName(HarnessOpenCode) = %q, want %q", got, "my-pack")
	}

	// Empty when no settings pack is set.
	p2 := NewProfile()
	if p2.SettingsPackName(HarnessClaudeCode) != "" {
		t.Errorf("expected empty SettingsPackName on fresh profile")
	}
}
