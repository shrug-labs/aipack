package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMarketplaceExplicitFormat(t *testing.T) {
	t.Parallel()
	catalog := []byte(`{"$schema":"https://anthropic.com/claude-code/marketplace.schema.json","name":"market","plugins":[{"name":"probe","source":{"source":"npm","package":"probe@1","registry":"http://localhost:1234"}}]}`)
	for _, format := range []string{"", "claude", "codex-legacy", "agent-plugins"} {
		for _, path := range []string{"catalog.json", ".claude-plugin/marketplace.json"} {
			reg, err := ParseRegistrySource(catalog, RegistrySourceEntry{URL: "https://registry.invalid/" + path, Format: format})
			if err != nil {
				t.Fatal(err)
			}
			effective := format
			if format == "" && path == ".claude-plugin/marketplace.json" {
				effective = "claude"
			}
			entry := reg.Packs["probe"]
			if entry.Plugin.Format != effective || (entry.Unsupported == "") != (effective == "claude") {
				t.Fatalf("format=%q path=%q: %+v", format, path, entry)
			}
		}
	}
	if _, err := ParseMarketplace(catalog, RegistrySourceEntry{Format: "typo"}); err == nil {
		t.Fatal("invalid declared format accepted")
	}
	if _, err := ParseRegistrySource([]byte("schema_version: 1\npacks: {}\n"), RegistrySourceEntry{Format: "claude"}); err == nil || !strings.Contains(err.Error(), "requires a native marketplace") {
		t.Fatalf("native format silently applied to ordinary registry: %v", err)
	}
}

func TestDiscoverRegistrySource(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"registry.yaml": "schema_version: 1\npacks:\n  shared:\n    repo: https://example.com/preferred.git\ncollections:\n  starter:\n    packs: [shared]\n",
		"pack.json":     `{"schema_version":2,"name":"root-pack","root":"."}`,
		".agents/plugins/marketplace.json": `{"name":"market","plugins":[
			{"name":"shared","source":"./plugins/shared"},
			{"name":"probe","source":"./plugins/probe","policy":{"installation":"NOT_AVAILABLE"}}]}`,
	}
	for _, paths := range [][]string{
		{"pack.json"},
		{".agents/plugins/marketplace.json"},
		{"registry.yaml", "pack.json", ".agents/plugins/marketplace.json"},
	} {
		t.Run(strings.Join(paths, "+"), func(t *testing.T) {
			reg, err := DiscoverRegistrySource(RegistrySourceEntry{URL: "https://example.com/source.git", Ref: "main"}, func(path string) ([]byte, error) {
				if slices.Contains(paths, path) {
					return []byte(files[path]), nil
				}
				return nil, os.ErrNotExist
			})
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(paths, "pack.json") {
				entry := reg.Packs["root-pack"]
				if entry.Repo != "https://example.com/source.git" || entry.Ref != "main" || entry.Path != "" || entry.Plugin != nil {
					t.Fatalf("root pack lost source: %+v", entry)
				}
			}
			if slices.Contains(paths, ".agents/plugins/marketplace.json") {
				entry := reg.Packs["probe"]
				if entry.Plugin == nil || entry.Plugin.MarketplacePath != ".agents/plugins/marketplace.json" || entry.Plugin.Entry["policy"] == nil {
					t.Fatalf("catalog lost policy or path: %+v", entry)
				}
			}
			if slices.Contains(paths, "registry.yaml") && (reg.Packs["shared"].Repo != "https://example.com/preferred.git" || len(reg.Collections) != 1 || len(reg.Packs) != 3) {
				t.Fatalf("discovery lost precedence or entries: %+v", reg)
			}
		})
	}
	if _, err := DiscoverRegistrySource(RegistrySourceEntry{}, func(path string) ([]byte, error) {
		if path == "pack.json" {
			return []byte(`{"schema_version":999,"name":"broken"}`), nil
		}
		return nil, os.ErrNotExist
	}); err == nil {
		t.Fatal("malformed pack manifest was silently skipped")
	}
}

func TestDiscoverLocalRootPackRegistryValidates(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(`{"schema_version":2,"name":"root-pack","root":"."}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := DiscoverRegistrySource(RegistrySourceEntry{URL: root}, func(path string) ([]byte, error) {
		return ReadRepositoryFile(root, path)
	})
	if err != nil {
		t.Fatal(err)
	}
	if issues := ValidateRegistry(reg); len(issues) != 0 {
		t.Fatalf("discovered local pack creates an invalid registry: %v", issues)
	}
	entry := reg.Packs["root-pack"]
	if entry.Method != MethodCopy || entry.Repo != root || entry.Plugin != nil {
		t.Fatalf("local pack lost its acquisition coordinates: %+v", entry)
	}
	entry.Repo = "relative-root"
	reg.Packs["root-pack"] = entry
	if len(ValidateRegistry(reg)) == 0 {
		t.Fatal("copy source accepted a relative root")
	}
}

func TestMarketplaceGitHubSource(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"claude", "codex-legacy", "agent-plugins", ""} {
		catalog := []byte(`{"name":"market","plugins":[{"name":"branch","source":{"source":"github","repo":"Team/probe.git","ref":"main","path":"ignored"}},{"name":"pinned","source":{"source":"github","repo":"team/.github","ref":"main","sha":"0123456789012345678901234567890123456789"}}]}`)
		reg, err := ParseMarketplace(catalog, RegistrySourceEntry{URL: "https://example.invalid/catalog.json", Format: format})
		if err != nil || len(ValidateRegistry(reg)) != 0 {
			t.Fatalf("github source normalization: %+v %v", reg, err)
		}
		branch, pinned := reg.Packs["branch"], reg.Packs["pinned"]
		if format != "claude" {
			if branch.Unsupported == "" || pinned.Unsupported == "" {
				t.Fatal("Codex catalog accepted Claude github source object")
			}
			continue
		}
		if branch.Repo != "https://github.com/Team/probe.git.git" || branch.Ref != "main" || branch.Path != "" || branch.Plugin.Entry["source"].(map[string]any)["repo"] != "Team/probe.git" || pinned.Repo != "https://github.com/team/.github.git" || pinned.Ref != "0123456789012345678901234567890123456789" {
			t.Fatalf("github origin/ref/raw source lost: %+v %+v", branch, pinned)
		}
	}
	for _, repo := range []string{"", "team", "team/../escape", "../probe", "team/..", "team/probe?credential=fixture", "https://github.com/team/probe", "team/probe\n", "team/probe\\other", "team/probe extra"} {
		if _, err := claudeGitHubRepo(repo); err == nil {
			t.Fatalf("invalid github source accepted: %q", repo)
		}
	}
	for _, repo := range []string{"team/probe", "team/probe.git"} {
		body, _ := json.Marshal(map[string]any{"name": "market", "plugins": []any{map[string]any{"name": "probe", "source": map[string]string{"source": "git-subdir", "url": repo, "path": "plugins/probe"}}}})
		reg, err := ParseMarketplace(body, RegistrySourceEntry{Format: "claude"})
		entry := reg.Packs["probe"]
		if err != nil || entry.Unsupported != "" || entry.Repo != "https://github.com/"+repo+".git" || entry.Path != "plugins/probe" || entry.Plugin.Entry["source"].(map[string]any)["url"] != repo {
			t.Fatalf("Claude subdirectory shorthand changed original coordinates: %+v %v", entry, err)
		}
	}
	reg, err := ParseMarketplace([]byte(`{"name":"market","plugins":[{"name":"good","source":{"source":"github","repo":"team/probe"}},{"name":"bad","source":{"source":"github","repo":null}}]}`), RegistrySourceEntry{Format: "claude"})
	if err != nil || len(reg.Packs) != 2 || reg.Packs["good"].Unsupported != "" || reg.Packs["bad"].Unsupported == "" {
		t.Fatalf("invalid github entry hid valid sibling: %+v %v", reg, err)
	}
	for _, kind := range []string{"github", "url", "git-subdir"} {
		for _, field := range []string{`"ref":null`, `"ref":123`, `"sha":""`, `"sha":"1234567"`, `"sha":null`, `"sha":123`, `"sha":"01234567890123456789012345678901234567890"`, `"sha":"012345678901234567890123456789012345678A"`} {
			body := []byte(`{"name":"market","plugins":[{"name":"probe","source":{"source":"` + kind + `","repo":"team/probe","url":"https://example.invalid/probe.git","path":"plugin",` + field + `}}]}`)
			reg, err := ParseMarketplace(body, RegistrySourceEntry{Format: "claude"})
			if err != nil || reg.Packs["probe"].Unsupported == "" {
				t.Fatalf("invalid Claude %s selector accepted: %s %v", kind, field, err)
			}
		}
	}
}

func TestCodexGitURLBoundary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, repo := range []string{"./a/../repo", "../repo", `..\repo`, "ftp://example.invalid/repo", "--upload-pack=fixture", "", "team/../repo"} {
		if _, err := codexGitRepo(repo, RegistrySourceEntry{URL: root}); err == nil {
			t.Fatalf("invalid Git source accepted: %q", repo)
		}
	}
	for _, repo := range []string{"./repo", `.\repo`} {
		actual, err := codexGitRepo(repo, RegistrySourceEntry{URL: root})
		if err != nil || actual != "file://"+filepath.ToSlash(filepath.Join(root, "repo")) {
			t.Fatalf("local Git source lost root: %s %v", actual, err)
		}
		if actual, err := codexGitRepo(repo, RegistrySourceEntry{URL: "https://catalog.invalid/market.git"}); err != nil || actual != "./repo" {
			t.Fatalf("remote relative Git URL lost marketplace-relative identity: %s %v", actual, err)
		}
		if actual, err := codexGitRepo(repo, RegistrySourceEntry{URL: "https://catalog.invalid/market", Path: ".agents/plugins/marketplace.json"}); err != nil || actual != "./repo" {
			t.Fatalf("explicit Git marketplace path lost relative identity: %s %v", actual, err)
		}
		if _, err := codexGitRepo(repo, RegistrySourceEntry{URL: "https://catalog.invalid/marketplace.json"}); err == nil {
			t.Fatal("HTTP file catalog supplied a nonexistent marketplace root")
		}
	}
	for _, repo := range []string{"git@github.com:team/repo", "ssh://git@example.invalid/repo", "file:///tmp/probe.git", "http://example.invalid/repo"} {
		if actual, err := codexGitRepo(repo, RegistrySourceEntry{URL: root}); err != nil || actual != repo {
			t.Fatalf("explicit Git source rewritten: %s %v", actual, err)
		}
	}
}

func TestMarketplaceGitSourceDialect(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"codex-legacy", "agent-plugins", "claude"} {
		for _, test := range []struct {
			source, repo, ref, path string
			unsupported             bool
		}{
			{`{"source":"url","url":"https://example.invalid/probe.git","path":"nested"}`, "https://example.invalid/probe.git", "", "nested", false},
			{`{"source":"url","url":"https://example.invalid/probe.git","ref":null,"sha":null}`, "https://example.invalid/probe.git", "", "", format == "claude"},
			{`{"source":"url","url":"https://example.invalid/probe.git","ref":" main ","sha":" "}`, "https://example.invalid/probe.git", "main", "", format == "claude"},
			{`{"source":"url","url":"https://example.invalid/probe.git","ref":" main ","sha":" abc1234 "}`, "", "", "", true},
			{`{"source":"url","url":"https://example.invalid/probe.git","ref":1}`, "", "", "", true},
			{`{"source":"url","url":"https://example.invalid/probe.git","sha":1}`, "", "", "", true},
			{`{"source":"url","url":null}`, "", "", "", true},
			{`{"source":"url","url":"aipack-fixture/probe"}`, "https://github.com/aipack-fixture/probe.git", "", "", false},
			{`{"source":"url","url":"aipack-fixture/probe.git"}`, "https://github.com/aipack-fixture/probe.git", "", "", false},
			{`{"source":"url","url":" https://github.com/aipack-fixture/probe "}`, "https://github.com/aipack-fixture/probe.git", "", "", false},
			{`{"source":"git-subdir","url":"aipack-fixture/probe","path":" ./nested "}`, "https://github.com/aipack-fixture/probe.git", "", "nested", false},
			{`{"source":"git-subdir","url":"https://example.invalid/probe.git","path":null}`, "", "", "", true},
			{`{"source":"git-subdir","url":"https://example.invalid/probe.git","path":"./"}`, "", "", "", true},
			{`{"source":"git-subdir","url":"https://example.invalid/probe.git","path":"a/../b"}`, "", "", "", true},
		} {
			if format == "claude" && (strings.Contains(test.source, `"url":"aipack-fixture/`) || strings.Contains(test.source, `"url":" https://`) || strings.Contains(test.source, `"source":"git-subdir"`)) {
				continue // Claude source URL/path normalization is checked independently.
			}
			body := []byte(`{"name":"market","plugins":[{"name":"probe","source":` + test.source + `}]}`)
			reg, err := ParseMarketplace(body, RegistrySourceEntry{URL: "https://catalog.invalid/catalog.json", Format: format})
			if err != nil {
				t.Fatal(err)
			}
			entry := reg.Packs["probe"]
			path := test.path
			if format == "claude" {
				path = ""
			}
			if (entry.Unsupported != "") != test.unsupported || !test.unsupported && (entry.Repo != test.repo || entry.Ref != test.ref || entry.Path != path) {
				t.Fatalf("%s source %s: %+v", format, test.source, entry)
			}
			original, err := json.Marshal(entry.Plugin.Entry["source"])
			var expected map[string]any
			if err != nil || json.Unmarshal([]byte(test.source), &expected) != nil {
				t.Fatal(err)
			}
			canonical, err := json.Marshal(expected)
			if err != nil || !bytes.Equal(original, canonical) {
				t.Fatalf("normalized source rewrote original metadata: %s vs %s", original, canonical)
			}
		}
	}
}

func TestMarketplaceCommandOrderRoundTrip(t *testing.T) {
	root := t.TempDir()
	reg, err := ParseMarketplace([]byte(`{"name":"market","plugins":[{"name":"probe","source":"./probe","commands":{"z-first":{"source":"./action.md"},"a-later":{"source":"./action.md"},"2":{"source":"./numeric.md"}}}]}`), RegistrySourceEntry{URL: root, Path: ".claude-plugin/marketplace.json"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "registry.yaml")
	body, err := yaml.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRegistry(path)
	if err != nil || !slices.Equal(loaded.Packs["probe"].Plugin.CatalogCommandOrder, []string{"z-first", "a-later", "2"}) {
		t.Fatalf("registry cache changed command key order: %+v %v", loaded.Packs, err)
	}
}

func TestMarketplaceSourceNormalization(t *testing.T) {
	root := t.TempDir()
	catalog := []byte(`{"name":"market","interface":{"displayName":"Example"},"plugins":[{"name":"local","source":"./plugins/local","policy":{"authentication":"ON_INSTALL"}},{"name":"remote","source":{"source":"git-subdir","url":"https://example.invalid/repo.git","path":"plugin","ref":"main","sha":"0123456789012345678901234567890123456789"}},{"name":"npm","source":{"source":"npm","package":"@example/plugin"}}]}`)
	path := filepath.Join(root, ".agents/plugins/marketplace.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, catalog, 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Packs["local"].Method != MethodCopy || reg.Packs["local"].Repo != root || reg.Packs["local"].Plugin.MarketplacePath != ".agents/plugins/marketplace.json" || reg.Packs["remote"].Ref != "0123456789012345678901234567890123456789" || reg.Packs["npm"].Unsupported != "" || reg.Packs["npm"].Method != MethodNPM || reg.Packs["npm"].Plugin.NPM.Package != "@example/plugin" {
		t.Fatalf("incorrect native coordinates: %+v", reg.Packs)
	}
	if len(ValidateRegistry(reg)) != 0 {
		t.Fatalf("native catalog is invalid: %v", ValidateRegistry(reg))
	}
	if reg.Packs["local"].Plugin.Entry["policy"] == nil || reg.Packs["local"].Plugin.MarketplaceMetadata["interface"] == nil {
		t.Fatal("native auth or catalog metadata lost")
	}
	reg, err = ParseMarketplace(catalog, RegistrySourceEntry{URL: "https://example.invalid/catalog.json"})
	if err != nil || reg.Packs["local"].Unsupported == "" || reg.Packs["remote"].Unsupported != "" {
		t.Fatalf("HTTP catalog cannot supply relative Git sources: %+v %v", reg, err)
	}
	for _, source := range []string{"../outside", "/absolute", "plugins\\escape"} {
		if _, err := ParseMarketplace([]byte(`{"name":"market","plugins":[{"name":"bad","source":"`+source+`"}]}`), RegistrySourceEntry{URL: root}); err == nil {
			t.Fatalf("accepted unsafe source %q", source)
		}
	}
}

func TestMarketplaceNPMRejectedSibling(t *testing.T) {
	for _, bad := range []string{
		`{"package":null}`, `{"package":"../escape"}`, `{"package":"probe","registry":"http://localhost"}`, `{"package":"probe","version":"file:payload"}`,
	} {
		catalog := []byte(`{"name":"market","plugins":[{"name":"good","source":{"source":"npm","package":"@example/plugin","version":null}},{"name":"bad","source":{"source":"npm",` + bad[1:] + `}]}`)
		reg, err := ParseMarketplace(catalog, RegistrySourceEntry{URL: "https://example.invalid/catalog.json"})
		if err != nil || len(reg.Packs) != 2 || reg.Packs["good"].Method != MethodNPM || reg.Packs["bad"].Unsupported == "" || len(ValidateRegistry(reg)) != 0 {
			t.Fatalf("rejected npm source hid a valid sibling: %+v %v", reg.Packs, err)
		}
	}
}

func TestMarketplaceClaudeNPMSource(t *testing.T) {
	catalog := []byte(`{"name":"market","plugins":[{"name":"inline","source":{"source":"npm","package":"npm:@team/probe@1.0.0","registry":"http://localhost:1234"}},{"name":"tarball","source":{"source":"npm","package":"https://registry.invalid/archive.tgz"}},{"name":"conflict","source":{"source":"npm","package":"probe@1","version":"2"}}]}`)
	reg, err := ParseMarketplace(catalog, RegistrySourceEntry{URL: "https://registry.invalid/.claude-plugin/marketplace.json"})
	if err != nil || len(ValidateRegistry(reg)) != 0 {
		t.Fatalf("Claude npm catalog: %+v %v", reg, err)
	}
	inline := reg.Packs["inline"]
	if inline.Method != MethodNPM || inline.Ref != "1.0.0" || inline.Repo != "npm:@team/probe" || inline.Plugin.NPM.Registry != "http://localhost:1234" || inline.Plugin.Entry["source"].(map[string]any)["package"] != "npm:@team/probe@1.0.0" {
		t.Fatalf("Claude normalization lost original or effective coordinates: %+v", inline)
	}
	if reg.Packs["tarball"].Method != MethodNPM || reg.Packs["conflict"].Unsupported == "" {
		t.Fatal("Claude tarball or conflicting selector handling differs")
	}
	body, err := yaml.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ParseRegistry(body)
	if err != nil || len(ValidateRegistry(loaded)) != 0 || loaded.Packs["inline"].Plugin.NPM.Package != "@team/probe" {
		t.Fatalf("Claude normalized registry roundtrip: %+v %v", loaded, err)
	}
}
