package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/source"
)

// Explicit HTTP registry sources, including fork defaults, stay local to the
// test. Declared Git sources must reach the fake Git executable instead.
type authFixtureHTTPTransport struct{}

func (authFixtureHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("schema_version: 1\npacks: {}\n")),
		Request:    req,
	}, nil
}

func TestGitAuthenticationPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		stdin, stderr, json, disabled, want bool
	}{
		{"terminal", true, true, false, false, true},
		{"stdin-pipe", false, true, false, false, false},
		{"stderr-pipe", true, false, false, false, false},
		{"no-terminal", false, false, false, false, false},
		{"json", true, true, true, false, false},
		{"explicit-override", true, true, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := Globals{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
				StdinTTY: tc.stdin, StderrTTY: tc.stderr, NonInteractive: tc.disabled}
			ctx, cancel := g.gitContext(context.Background(), tc.json)
			defer cancel()
			if got := source.GitMayPrompt(ctx); got != tc.want {
				t.Fatalf("prompt=%v want=%v", got, tc.want)
			}
			// Redirected stdout intentionally has no effect on the policy.
			if tc.want {
				nested, done := g.gitContext(ctx, false)
				defer done()
				if nested != ctx {
					t.Fatal("nested operation did not share its command session")
				}
			}
		})
	}
	if source.GitMayPrompt(context.Background()) {
		t.Fatal("background caller can prompt")
	}
}

func TestGitAuthenticationCommandBoundaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	transport := http.DefaultTransport
	http.DefaultTransport = authFixtureHTTPTransport{}
	t.Cleanup(func() { http.DefaultTransport = transport })
	bin := t.TempDir()
	script := `#!/bin/sh
if [ "$GIT_TERMINAL_PROMPT" = 0 ]; then
  printf 'unattended\n' >> "$AIPACK_TEST_AUTH_LOG"
else
  printf 'interactive\n' >> "$AIPACK_TEST_AUTH_LOG"
fi
printf 'Permission denied (publickey).\n' >&2
exit 128
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_TERMINAL_PROMPT", "")
	t.Setenv("AIPACK_TELEMETRY_DISABLED", "1")
	type command interface {
		Run(context.Context, *Globals) error
	}
	for _, tc := range []struct {
		name string
		cmd  command
		json bool
	}{
		{"install", &PackInstallCmd{Sources: []string{"demo"}}, false},
		{"multi-install", &PackInstallCmd{Sources: []string{"demo", "other"}}, false},
		{"missing-install", &PackInstallCmd{}, false},
		{"install-ref", &PackInstallCmd{Sources: []string{"demo@v1"}}, false},
		{"update", &PackUpdateCmd{Name: "installed"}, false},
		{"update-all", &PackUpdateCmd{All: true}, false},
		{"update-check-json", &PackUpdateCmd{All: true, DryRun: true, JSON: true}, true},
		{"inspect", &PackInspectCmd{Input: "demo"}, false},
		{"inspect-json", &PackInspectCmd{Input: "demo", JSON: true}, true},
		{"versions", &PackVersionsCmd{Name: "demo"}, false},
		{"versions-json", &PackVersionsCmd{Name: "demo", JSON: true}, true},
		{"registry-fetch", &RegistryFetchCmd{}, false},
		{"init", &InitCmd{}, false},
		{"collection", &CollectionInstallCmd{Name: "demo-set"}, false},
		{"profile-install", &ProfileSetCmd{Name: "default", Install: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, body string) {
				t.Helper()
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("sync-config.yaml", "schema_version: 1\ndefaults:\n  profile: default\n  auto_sync: false\nregistry_sources:\n  - name: fixture\n    url: ssh://git@example.invalid/registry.git\n    ref: main\n")
			write("profiles/default.yaml", "schema_version: 1\npacks:\n  - name: demo\n")
			write("registries/fixture.yaml", "schema_version: 1\npacks:\n  demo:\n    repo: ssh://git@example.invalid/demo.git\n  other:\n    repo: ssh://git@example.invalid/other.git\ncollections:\n  demo-set:\n    packs:\n      - name: demo\n")
			write("packs/installed/pack.json", `{"schema_version":1,"name":"installed","root":"."}`)
			if err := config.SaveLockfile(config.LockfilePath(dir), config.Lockfile{
				LockVersion: config.LockfileVersion,
				Packs:       map[string]config.InstalledPackMeta{"installed": {Origin: "ssh://git@example.invalid/installed.git", Method: "clone"}},
			}); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, "auth.log")
			t.Setenv("AIPACK_TEST_AUTH_LOG", log)
			var stdout, stderr bytes.Buffer
			g := Globals{ConfigDir: dir, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, StdinTTY: true, StderrTTY: true}
			if validator, ok := tc.cmd.(interface{ Validate() error }); ok {
				if err := validator.Validate(); err != nil {
					t.Fatal(err)
				}
			}
			err := tc.cmd.Run(context.Background(), &g)
			calls, readErr := os.ReadFile(log)
			if readErr != nil {
				t.Fatalf("no Git call: %v; command=%v stdout=%s stderr=%s", readErr, err, &stdout, &stderr)
			}
			want := "interactive"
			if tc.json {
				want = "unattended"
			}
			for mode := range strings.FieldsSeq(string(calls)) {
				if mode != want {
					t.Errorf("Git mode=%s, want %s", mode, want)
				}
			}
			if tc.name == "update-check-json" && !json.Valid(stdout.Bytes()) {
				t.Fatalf("update-check JSON was contaminated: %s", &stdout)
			}
		})
	}
}

func TestGitAuthenticationFlagAndTerminalDetection(t *testing.T) {
	stdout, stderr, code := runApp(t, "--non-interactive", "version")
	if code != 0 || !strings.Contains(stdout, "aipack") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminalFile(f) || isTerminalWriter(io.Discard) {
		t.Fatal("non-terminal character device or writer treated as a terminal")
	}
}
