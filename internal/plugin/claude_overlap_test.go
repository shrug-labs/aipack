package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
)

func claudeScanFixture(t *testing.T, source, kind string) (path, commandID, agentID, skillID string, entry map[string]any) {
	t.Helper()
	catalog, hidden := strings.HasPrefix(kind, "catalog-"), strings.HasSuffix(kind, "-hidden")
	kind = strings.TrimSuffix(strings.TrimPrefix(kind, "catalog-"), "-hidden")
	entry = map[string]any{"name": "scan-probe", "source": "./probe", "version": "1.0.0"}
	path, commandID, agentID = "commands/ops/shared.md", "ops:shared", "Shared"
	manifest := `{"name":"scan-probe","version":"1.0.0","agents":["./commands/ops/shared.md"]}`
	if kind == "default-command-file-alias" || kind == "default-command-directory-alias" {
		manifest = `{"name":"scan-probe","version":"1.0.0","agents":["./links/shared.md"]}`
	}
	if strings.HasPrefix(kind, "default-agent") {
		path, commandID, agentID = "agents/ops/shared.md", "command-alias", "ops:Shared"
		manifest = `{"name":"scan-probe","version":"1.0.0","commands":{"command-alias":{"source":"./agents/ops/shared.md"}}}`
		keepPath := "agents/ops/keep.md"
		if kind == "default-agent-link" {
			keepPath = "custom/keep.md"
		}
		write(t, source, keepPath, "---\nname: Keep\ndescription: Retained nested agent.\nmodel: inherit\nunknown: 18446744073709551618\n---\nKEEP_NATIVE_BODY\n", 0o644)
		if kind == "default-agent-link" {
			if err := os.MkdirAll(filepath.Join(source, "agents/ops"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../../custom/keep.md", filepath.Join(source, "agents/ops/keep.md")); err != nil {
				t.Fatal(err)
			}
		}
	} else if kind == "explicit-skill" {
		path, commandID, agentID, skillID = "custom/skill/SKILL.md", "command-alias", "", "Shared"
		manifest = `{"name":"scan-probe","version":"1.0.0","commands":{"command-alias":{"source":"./custom/skill/SKILL.md"}},"skills":["./custom/skill"],"agents":[]}`
	} else if strings.HasPrefix(kind, "explicit-agent-") {
		path, commandID, agentID = "custom/shared.md", "command-alias", "Shared"
		manifest = `{"name":"scan-probe","version":"1.0.0","commands":{"command-alias":{"source":"./links/shared.md"}},"agents":["./links/shared.md"]}`
	} else if strings.HasPrefix(kind, "command-alias-") {
		path, agentID = "custom/shared.md", "Shared"
		commandID = "original"
		commands := `"original":{"source":"./custom/shared.md"},"file-link":{"source":"./links/shared.md"},"directory-link":{"source":"./directory/shared.md"}`
		if kind == "command-alias-file" {
			commandID = "file-link"
			commands = `"file-link":{"source":"./links/shared.md"},"directory-link":{"source":"./directory/shared.md"},"original":{"source":"./custom/shared.md"}`
		} else if kind == "command-alias-directory" {
			commandID = "directory-link"
			commands = `"directory-link":{"source":"./directory/shared.md"},"original":{"source":"./custom/shared.md"},"file-link":{"source":"./links/shared.md"}`
		} else if kind == "command-alias-file-only" {
			commandID = "file-link"
			commands = `"file-link":{"source":"./links/shared.md"},"directory-link":{"source":"./directory/shared.md"}`
		} else if kind == "command-alias-directory-only" {
			commandID = "directory-link"
			commands = `"directory-link":{"source":"./directory/shared.md"},"file-link":{"source":"./links/shared.md"}`
		}
		manifest = `{"name":"scan-probe","version":"1.0.0","commands":{` + commands + `},"agents":["./custom/shared.md"]}`
	}
	if catalog {
		if err := json.Unmarshal([]byte(manifest), &entry); err != nil {
			t.Fatal(err)
		}
	} else {
		write(t, source, claudeManifest, manifest, 0o644)
	}
	mode := os.FileMode(0o644)
	if catalog {
		mode = 0o440
		write(t, source, path+".aipack-source", "SOURCE_COLLISION_SENTINEL", 0o640)
	}
	write(t, source, path, "---\nname: Shared\ndescription: Shared native source.\n---\nSHARED_NATIVE_BODY\n", mode)
	if strings.HasPrefix(kind, "default-command") {
		keepPath := "commands/ops/keep.md"
		if kind == "default-command-link" {
			keepPath = "custom/keep.md"
		}
		write(t, source, keepPath, "---\ndescription: Retained nested command.\n---\nKEEP_NATIVE_BODY\n", 0o644)
		if kind == "default-command-link" {
			if err := os.Symlink("../../custom/keep.md", filepath.Join(source, "commands/ops/keep.md")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if kind == "explicit-agent-link" {
		if err := os.MkdirAll(filepath.Join(source, "links"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../custom/shared.md", filepath.Join(source, "links/shared.md")); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "default-command-file-alias" || strings.HasPrefix(kind, "command-alias-") {
		if err := os.MkdirAll(filepath.Join(source, "links"), 0o755); err != nil {
			t.Fatal(err)
		}
		target := "../commands/ops/shared.md"
		if strings.HasPrefix(kind, "command-alias-") {
			target = "../custom/shared.md"
		}
		if err := os.Symlink(target, filepath.Join(source, "links/shared.md")); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "default-command-directory-alias" {
		if err := os.Symlink("commands/ops", filepath.Join(source, "links")); err != nil {
			t.Fatal(err)
		}
	}
	if strings.HasPrefix(kind, "command-alias-") {
		if err := os.Symlink("custom", filepath.Join(source, "directory")); err != nil {
			t.Fatal(err)
		}
	}
	if strings.HasSuffix(kind, "-dir") {
		dir, target := "links", "custom"
		if kind == "default-command-dir" {
			dir = "commands"
		} else if kind == "default-agent-dir" {
			dir = "agents"
		}
		if dir != "links" {
			if err := os.Rename(filepath.Join(source, dir), filepath.Join(source, target)); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(target, filepath.Join(source, dir)); err != nil {
			t.Fatal(err)
		}
	}
	if hidden {
		target := "retained/shared.md"
		if err := os.MkdirAll(filepath.Join(source, "retained"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(source, path), filepath.Join(source, target)); err != nil {
			t.Fatal(err)
		}
		link, err := filepath.Rel(filepath.Dir(path), target)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(link, filepath.Join(source, path)); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func TestClaudeCrossCategoryScans(t *testing.T) {
	for _, kind := range []string{"default-command", "default-command-link", "default-command-dir", "default-command-file-alias", "default-command-directory-alias", "default-agent", "default-agent-link", "default-agent-dir", "explicit-agent-link", "explicit-agent-dir", "explicit-skill", "command-alias-original", "command-alias-file", "command-alias-directory", "command-alias-file-only", "command-alias-directory-only", "catalog-default-command", "catalog-default-agent", "catalog-default-command-dir", "catalog-default-agent-dir"} {
		t.Run(kind, func(t *testing.T) {
			scanKind := strings.TrimPrefix(kind, "catalog-")
			keepCommand := strings.HasPrefix(scanKind, "default-command") && scanKind != "default-command-link"
			keepAgent := scanKind == "default-agent" || scanKind == "default-agent-dir"
			source, pack := t.TempDir(), t.TempDir()
			path, commandID, agentID, skillID, entry := claudeScanFixture(t, source, kind)
			spec := domain.PluginSource{Marketplace: "scan-market", Entry: entry}
			m, err := MaterializeClaude(source, pack, "alias", spec)
			if err != nil {
				t.Fatal(err)
			}
			if (kind == "default-command-link" && slices.Contains(m.Workflows, "ops:keep")) || (kind == "default-agent-link" && slices.Contains(m.Agents, "ops:Keep")) {
				t.Fatal("converter included a symlink ignored by native default scanning")
			}
			if strings.HasPrefix(kind, "command-alias-") {
				want := []string{"directory-link", "file-link", "original"}
				if strings.HasSuffix(kind, "-only") {
					want = want[:2]
				}
				if !slices.Equal(m.Workflows, want) || len(m.NativePlugin.Components[domain.CategoryWorkflows][commandID]) != 1 {
					t.Fatal("same-file command declarations lost selectable aliases")
				}
			}
			s := selection(m, pack)
			original, err := ReadFiles(source)
			if err != nil {
				t.Fatal(err)
			}
			for _, cat := range []domain.PackCategory{domain.CategoryWorkflows, domain.CategoryAgents, domain.CategorySkills} {
				s = selection(m, pack)
				s.Selected[cat] = nil
				if cat == domain.CategoryWorkflows && keepCommand {
					s.Selected[cat] = []string{"ops:keep"}
				}
				if cat == domain.CategoryAgents && keepAgent {
					s.Selected[cat] = []string{"ops:Keep"}
				}
				files, entry, err := RenderClaudePackage(s)
				if err != nil {
					t.Fatal(err)
				}
				filtered := t.TempDir()
				if err := WriteFiles(filtered, files); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(kind, "catalog-") {
					if sentinel, err := os.ReadFile(filepath.Join(filtered, path+".aipack-source")); err != nil || string(sentinel) != "SOURCE_COLLISION_SENTINEL" {
						t.Fatal("selection overwrote an original neighboring source asset")
					}
					if (strings.HasPrefix(scanKind, "default-command") && cat == domain.CategoryWorkflows) || (strings.HasPrefix(scanKind, "default-agent") && cat == domain.CategoryAgents) {
						link, err := os.Readlink(filepath.Join(filtered, path))
						st, statErr := os.Stat(filepath.Join(filtered, path))
						if err != nil || link != filepath.Base(path)+".aipack-source.1" || statErr != nil || st.Mode().Perm() != 0o440 {
							t.Fatalf("shared default selection lost source mode or collision-safe link: %s %v %v", link, err, statErr)
						}
					}
				}
				spec.Entry = entry
				loaded, err := ReadClaude(filtered, spec)
				if err != nil || slices.Contains(loaded.Workflows, commandID) != (cat != domain.CategoryWorkflows) || (agentID != "" && slices.Contains(loaded.Agents, agentID) != (cat != domain.CategoryAgents)) || (skillID != "" && slices.Contains(loaded.Skills, skillID) != (cat != domain.CategorySkills)) {
					t.Fatalf("independent scan selection: %+v %v", loaded, err)
				}
				if keepAgent && !slices.Contains(loaded.Agents, "ops:Keep") {
					t.Fatal("nested agent identity changed")
				}
				if keepAgent && cat == domain.CategoryAgents {
					for _, file := range files {
						if file.Path == "agents/ops/keep.md" && (!bytes.Contains(file.Content, []byte("18446744073709551618")) || !bytes.HasSuffix(file.Content, []byte("KEEP_NATIVE_BODY\n"))) {
							t.Fatal("agent metadata or original body changed")
						}
					}
				}
			}
			stored, err := ReadFiles(filepath.Join(pack, "upstream"))
			if err != nil || !reflect.DeepEqual(original, stored) {
				t.Fatal("profile selection mutated installed source")
			}
		})
	}
}

func TestClaudeSkillDeclarationAssets(t *testing.T) {
	source, pack := t.TempDir(), t.TempDir()
	write(t, source, claudeManifest, `{"name":"scan-probe","skills":["."],"hooks":"./config/hooks.json"}`, 0o644)
	write(t, source, "SKILL.md", "---\nname: root\ndescription: Root fixture.\n---\nROOT_BODY\n", 0o644)
	write(t, source, "config/hooks.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo retained"}]}]}}`, 0o644)
	m, err := MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "scan-market"})
	if err != nil {
		t.Fatal(err)
	}
	s := selection(m, pack)
	s.Selected[domain.CategorySkills] = nil
	files, _, err := RenderClaudePackage(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Path == claudeManifest && !bytes.Contains(file.Content, []byte(`"skills":["."]`)) {
			t.Fatal("unrelated retained hooks changed the skill declaration")
		}
		if file.Path == "SKILL.md" {
			t.Fatal("excluded root skill remained active")
		}
	}
}

func claudeScanResponse(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range []string{
		`{"type":"message_start","message":{"id":"msg_scan","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-5","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"AIPACK_SCAN_RESPONSE"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`,
	} {
		var item struct{ Type string }
		if err := json.Unmarshal([]byte(event), &item); err != nil {
			t.Error(err)
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", item.Type, event)
	}
}

func TestClaudeNativeCrossCategoryScans(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for cross-category scans")
	}
	var mu sync.Mutex
	var requests []byte
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if strings.HasSuffix(req.URL.Path, "/count_tokens") {
			fmt.Fprint(w, `{"input_tokens":10}`)
			return
		}
		mu.Lock()
		requests = append(requests, body...)
		mu.Unlock()
		claudeScanResponse(t, w)
	}))
	defer api.Close()
	for _, kind := range []string{"default-command", "default-command-link", "default-command-dir", "default-command-file-alias", "default-command-directory-alias", "default-agent", "default-agent-link", "default-agent-dir", "explicit-agent-link", "explicit-agent-dir", "explicit-skill", "command-alias-original", "command-alias-file", "command-alias-directory", "command-alias-file-only", "command-alias-directory-only", "catalog-default-command", "catalog-default-agent", "catalog-default-command-dir", "catalog-default-agent-dir", "catalog-default-command-hidden", "catalog-default-agent-hidden"} {
		for _, converted := range []bool{false, true} {
			if converted && strings.HasSuffix(kind, "-hidden") {
				continue
			}
			t.Run(fmt.Sprintf("%s/converted-%t", kind, converted), func(t *testing.T) {
				scanKind := strings.TrimSuffix(strings.TrimPrefix(kind, "catalog-"), "-hidden")
				keepCommand := strings.HasPrefix(scanKind, "default-command") && scanKind != "default-command-link"
				keepAgent := scanKind == "default-agent" || scanKind == "default-agent-dir"
				source, pack, market, home, cwd := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
				_, commandID, agentID, skillID, entry := claudeScanFixture(t, source, kind)
				var s domain.NativePluginSelection
				if converted {
					m, err := MaterializeClaude(source, pack, "alias", domain.PluginSource{Marketplace: "scan-market", Entry: entry})
					if err != nil {
						t.Fatal(err)
					}
					if (kind == "default-command-link" && slices.Contains(m.Workflows, "ops:keep")) || (kind == "default-agent-link" && slices.Contains(m.Agents, "ops:Keep")) {
						t.Fatal("converter included a symlink ignored by native default scanning")
					}
					s = selection(m, pack)
				}
				configHome := filepath.Join(home, "native")
				write(t, configHome, "settings.json", `{}`, 0o600)
				for phase := 0; phase < 5; phase++ {
					if !converted && phase > 0 {
						break
					}
					commandActive, otherActive := phase != 1 && phase != 3, phase != 2 && phase != 3
					if strings.HasSuffix(kind, "-hidden") {
						commandActive, otherActive = scanKind != "default-command", scanKind != "default-agent"
					}
					files, err := ReadFiles(source)
					if converted {
						s.Selected[domain.CategoryWorkflows] = nil
						s.Selected[domain.CategoryAgents] = nil
						s.Selected[domain.CategorySkills] = nil
						if keepAgent {
							s.Selected[domain.CategoryAgents] = []string{"ops:Keep"}
						}
						if keepCommand {
							s.Selected[domain.CategoryWorkflows] = []string{"ops:keep"}
						}
						if commandActive {
							s.Selected[domain.CategoryWorkflows] = append(s.Selected[domain.CategoryWorkflows], commandID)
							if strings.HasPrefix(kind, "command-alias-") && phase != 2 {
								s.Selected[domain.CategoryWorkflows] = slices.Sorted(maps.Keys(s.Package.Components[domain.CategoryWorkflows]))
							}
						}
						if otherActive && agentID != "" {
							s.Selected[domain.CategoryAgents] = append(s.Selected[domain.CategoryAgents], agentID)
						}
						if otherActive && skillID != "" {
							s.Selected[domain.CategorySkills] = []string{skillID}
						}
						original := files
						files, entry, err = RenderClaudePackage(s)
						if err == nil && (phase == 0 || phase == 4) && !reflect.DeepEqual(original, files) {
							t.Fatal("all-selected rendering changed original files")
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					payload := filepath.Join(market, "probe")
					if err := os.RemoveAll(payload); err != nil {
						t.Fatal(err)
					}
					if err := WriteFiles(payload, files); err != nil {
						t.Fatal(err)
					}
					catalog, err := json.Marshal(map[string]any{"name": "scan-market", "owner": map[string]any{"name": "shrug-labs"}, "plugins": []any{entry}})
					if err != nil {
						t.Fatal(err)
					}
					write(t, market, ".claude-plugin/marketplace.json", string(catalog), 0o644)
					claudeNativeAt(t, home, configHome, cwd, "plugin", "marketplace", "add", market)
					if phase > 0 {
						claudeNativeAt(t, home, configHome, cwd, "plugin", "uninstall", "scan-probe@scan-market", "--scope", "user", "--keep-data", "--json")
					}
					claudeNativeAt(t, home, configHome, cwd, "plugin", "install", "scan-probe@scan-market", "--scope", "user", "--json")
					run := func(prompt, agent string) []byte {
						t.Helper()
						ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
						defer cancel()
						args := []string{"--print", "--model", "claude-sonnet-4-5", "--output-format", "stream-json", "--verbose", "--tools", "", "--no-session-persistence"}
						if agent != "" {
							args = append(args, "--agent", "scan-probe:"+agent)
						}
						cmd := exec.CommandContext(ctx, "claude", append(args, prompt)...)
						cmd.Dir, cmd.WaitDelay = cwd, time.Second
						cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + configHome, "ANTHROPIC_API_KEY=aipack-offline-fixture", "ANTHROPIC_BASE_URL=" + api.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "HTTP_PROXY=" + api.URL, "HTTPS_PROXY=" + api.URL, "NO_PROXY=127.0.0.1,localhost"}
						mu.Lock()
						requests = nil
						mu.Unlock()
						out, err := cmd.CombinedOutput()
						if err != nil || !bytes.Contains(out, []byte("AIPACK_SCAN_RESPONSE")) {
							t.Fatalf("native phase=%d: %v\n%s", phase, err, out)
						}
						return out
					}
					out := run("AIPACK_SCAN_PROMPT", "")
					found := false
					for _, line := range bytes.Split(out, []byte("\n")) {
						var init struct {
							Subtype  string   `json:"subtype"`
							Commands []string `json:"slash_commands"`
							Agents   []string `json:"agents"`
						}
						if json.Unmarshal(line, &init) == nil && init.Subtype == "init" {
							found = true
							if strings.HasPrefix(kind, "command-alias-") {
								count := 0
								for _, alias := range []string{"original", "file-link", "directory-link"} {
									if !slices.Contains(init.Commands, "scan-probe:"+alias) {
										continue
									}
									count++
									if !commandActive || (phase == 2 && alias != commandID) || (strings.HasSuffix(kind, "-only") && alias == "original") {
										t.Fatalf("native same-file alias identity differs phase=%d: %v", phase, init.Commands)
									}
								}
								if (count == 1) != commandActive || count > 1 {
									t.Fatalf("native canonical alias deduplication differs phase=%d: %v", phase, init.Commands)
								}
							}
							if strings.HasPrefix(scanKind, "default-command") && slices.Contains(init.Commands, "scan-probe:ops:keep") != keepCommand {
								t.Fatalf("nested retained command identity changed: %v", init.Commands)
							}
							if strings.HasPrefix(scanKind, "default-agent") && slices.Contains(init.Agents, "scan-probe:ops:Keep") != keepAgent {
								t.Fatalf("nested retained agent identity changed: %v", init.Agents)
							}
							if (!strings.HasPrefix(kind, "command-alias-") && slices.Contains(init.Commands, "scan-probe:"+commandID) != commandActive) || (agentID != "" && slices.Contains(init.Agents, "scan-probe:"+agentID) != otherActive) || (skillID != "" && slices.Contains(init.Commands, "scan-probe:"+skillID) != otherActive) {
								t.Fatalf("independent native scan selection failed phase=%d: commands=%v agents=%v", phase, init.Commands, init.Agents)
							}
						}
					}
					if !found {
						t.Fatal("native inventory missing")
					}
					if otherActive && strings.HasPrefix(kind, "command-alias-") {
						run("AIPACK_AGENT_PROMPT", agentID)
					} else if commandActive {
						out = run("/scan-probe:"+commandID+" AIPACK_COMMAND_PROMPT", "")
					} else if otherActive && agentID != "" {
						run("AIPACK_AGENT_PROMPT", agentID)
					} else {
						continue
					}
					mu.Lock()
					loaded := bytes.Contains(requests, []byte("SHARED_NATIVE_BODY"))
					mu.Unlock()
					if !loaded {
						t.Fatalf("selected native source body was not invoked phase=%d: %s", phase, out)
					}
					if (keepAgent && phase == 2) || (keepCommand && phase == 1) {
						if keepAgent {
							run("AIPACK_KEEP_PROMPT", "ops:Keep")
						} else {
							run("/scan-probe:ops:keep AIPACK_KEEP_PROMPT", "")
						}
						mu.Lock()
						loaded := bytes.Contains(requests, []byte("KEEP_NATIVE_BODY"))
						mu.Unlock()
						if !loaded {
							t.Fatal("retained nested component did not load its original body")
						}
					}
				}
			})
		}
	}
}
