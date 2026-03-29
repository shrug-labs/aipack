package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
	ccharness "github.com/shrug-labs/aipack/internal/harness/claudecode"
	clharness "github.com/shrug-labs/aipack/internal/harness/cline"
	cxharness "github.com/shrug-labs/aipack/internal/harness/codex"
	ocharness "github.com/shrug-labs/aipack/internal/harness/opencode"
)

// contractEnv is a self-contained sync environment for contract tests.
// One project directory, one home, one harness, real implementations.
type contractEnv struct {
	t          *testing.T
	projectDir string
	home       string
	harness    domain.Harness
	registry   *harness.Registry
	packRoot   string
}

func newContractEnv(t *testing.T, hid domain.Harness) *contractEnv {
	t.Helper()
	return &contractEnv{
		t:          t,
		projectDir: t.TempDir(),
		home:       t.TempDir(),
		harness:    hid,
		registry: harness.NewRegistry(
			ccharness.Harness{}, clharness.Harness{}, cxharness.Harness{}, ocharness.Harness{},
		),
		packRoot: t.TempDir(),
	}
}

// sync applies a profile with force and auto-confirm.
func (e *contractEnv) sync(profile domain.Profile) SyncResult {
	e.t.Helper()
	result, warnings, err := RunSync(context.Background(), profile, SyncRequest{
		TargetSpec: TargetSpec{
			Scope:      domain.ScopeProject,
			ProjectDir: e.projectDir,
			Harnesses:  []domain.Harness{e.harness},
			Home:       e.home,
		},
		Force: true,
		Yes:   true,
		Quiet: true,
	}, e.registry, io.Discard, io.Discard)
	if err != nil {
		e.t.Fatalf("sync: %v", err)
	}
	for _, w := range warnings {
		e.t.Logf("warning: %s: %s", w.Field, w.Message)
	}
	return result
}

// files returns all files under projectDir as relative-path -> content.
func (e *contractEnv) files() map[string]string {
	e.t.Helper()
	return walkDir(e.t, e.projectDir)
}

// contentExists returns true if any file's path or content contains marker.
// This works across both per-file harnesses (match on path) and bundle-file
// harnesses like Codex (match on flattened content).
func (e *contractEnv) contentExists(marker string) bool {
	e.t.Helper()
	for path, content := range e.files() {
		if strings.Contains(path, marker) || strings.Contains(content, marker) {
			return true
		}
	}
	return false
}

func walkDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		content, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		result[rel] = string(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// forAllHarnesses runs fn as a parallel subtest for every registered harness.
func forAllHarnesses(t *testing.T, fn func(t *testing.T, env *contractEnv)) {
	t.Helper()
	for _, hid := range domain.AllHarnesses() {
		t.Run(string(hid), func(t *testing.T) {
			t.Parallel()
			env := newContractEnv(t, hid)
			fn(t, env)
		})
	}
}

// --- Profile builders ---

type profileOpt func(*domain.Pack, string)

// withRules adds rules with default content to the pack.
func withRules(names ...string) profileOpt {
	return func(pack *domain.Pack, _ string) {
		for _, name := range names {
			pack.Rules = append(pack.Rules, domain.Rule{
				Name:       name,
				Raw:        []byte("Rule: " + name + ".\n"),
				SourcePack: pack.Name,
			})
		}
	}
}

// ruleWithContent adds a single rule with specific content.
func ruleWithContent(name, content string) profileOpt {
	return func(pack *domain.Pack, _ string) {
		pack.Rules = append(pack.Rules, domain.Rule{
			Name:       name,
			Raw:        []byte(content),
			SourcePack: pack.Name,
		})
	}
}

// withAgents adds agents with default content to the pack.
func withAgents(names ...string) profileOpt {
	return func(pack *domain.Pack, _ string) {
		for _, name := range names {
			pack.Agents = append(pack.Agents, domain.Agent{
				Name:        name,
				Frontmatter: domain.AgentFrontmatter{Name: name},
				Body:        []byte("Agent: " + name + ".\n"),
				SourcePack:  pack.Name,
			})
		}
	}
}

// profileWith builds a single-pack profile with the given options.
func profileWith(packRoot string, opts ...profileOpt) domain.Profile {
	pack := domain.Pack{
		Name:    "test-pack",
		Version: "1.0.0",
		Root:    packRoot,
	}
	for _, opt := range opts {
		opt(&pack, packRoot)
	}
	return domain.Profile{Packs: []domain.Pack{pack}}
}

// packDef defines a named pack with options, for multi-pack profiles.
type packDef struct {
	name string
	opts []profileOpt
}

func packWith(name string, opts ...profileOpt) packDef {
	return packDef{name: name, opts: opts}
}

// multiPackProfile builds a profile with multiple independently configured packs.
func multiPackProfile(packRoot string, packs ...packDef) domain.Profile {
	var result []domain.Pack
	for _, pd := range packs {
		pack := domain.Pack{
			Name:    pd.name,
			Version: "1.0.0",
			Root:    packRoot,
		}
		for _, opt := range pd.opts {
			opt(&pack, packRoot)
		}
		result = append(result, pack)
	}
	return domain.Profile{Packs: result}
}
