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
	"github.com/shrug-labs/aipack/internal/util"
)

func TestClaudeNativeSharedDefaultSelection(t *testing.T) {
	if os.Getenv("AIPACK_TEST_CLAUDE_NATIVE") != "1" {
		t.Skip("set AIPACK_TEST_CLAUDE_NATIVE=1 for shared default discovery")
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
	for _, converted := range []bool{false, true} {
		for _, linked := range []bool{false, true} {
			if linked && !converted {
				continue
			}
			for _, key := range []string{"commands", "agents"} {
				for _, enabled := range []string{"both", "first", "second", "neither"} {
					t.Run(fmt.Sprintf("%s/%s/converted=%t/linked=%t", key, enabled, converted, linked), func(t *testing.T) {
						root, home, cwd := t.TempDir(), t.TempDir(), t.TempDir()
						configHome := filepath.Join(home, "native")
						write(t, configHome, "settings.json", `{}`, 0o600)
						path := key + "/ops/shared.md"
						id, name := "ops:shared", "Shared"
						if key == "agents" {
							id = "ops:Shared"
							if !converted && enabled != "both" {
								name = id
							}
						}
						body := "---\nname: " + name + "\ndescription: Native shared default fixture.\n---\nSHARED_DEFAULT_BODY\n"
						if converted || enabled == "both" {
							write(t, root, path, body, 0o440)
						} else {
							write(t, root, path+".aipack-source", body, 0o440)
							if err := os.Symlink(filepath.Base(path)+".aipack-source", filepath.Join(root, path)); err != nil {
								t.Fatal(err)
							}
						}
						var entries []map[string]any
						for _, plugin := range []string{"first", "second"} {
							entry := map[string]any{"name": plugin, "source": "./", "version": "1.0.0"}
							if !converted && plugin == enabled {
								if key == "commands" {
									entry[key] = map[string]any{id: map[string]any{"source": "./" + path}}
								} else {
									entry[key] = []string{"./" + path}
								}
							}
							entries = append(entries, entry)
						}
						catalog, err := json.Marshal(map[string]any{"name": "shared-market", "owner": map[string]any{"name": "shrug-labs"}, "plugins": entries})
						if err != nil {
							t.Fatal(err)
						}
						write(t, root, ".claude-plugin/marketplace.json", string(catalog), 0o644)
						if linked {
							if err := os.Rename(filepath.Join(root, key), filepath.Join(root, "custom")); err != nil {
								t.Fatal(err)
							}
							if err := os.Symlink("custom", filepath.Join(root, key)); err != nil {
								t.Fatal(err)
							}
						}
						if converted {
							original, err := ReadFiles(root)
							if err != nil {
								t.Fatal(err)
							}
							view := t.TempDir()
							var actions []domain.NativePluginAction
							for _, entry := range entries {
								pack := t.TempDir()
								m, err := MaterializeClaude(root, pack, entry["name"].(string), domain.PluginSource{Marketplace: "shared-market", MarketplaceURL: root, Entry: entry})
								if err != nil {
									t.Fatal(err)
								}
								s := selection(m, pack)
								if enabled != "both" && enabled != s.Package.Name {
									cat := domain.CategoryWorkflows
									if key == "agents" {
										cat = domain.CategoryAgents
									}
									s.Selected[cat] = nil
								}
								files, rendered, err := RenderClaudePackage(s)
								if err != nil {
									t.Fatal(err)
								}
								pkg := s.Package
								pkg.MarketplaceEntry = rendered
								actions = append(actions, domain.NativePluginAction{Package: pkg, Selection: &s, Files: files, MarketplaceDir: view})
							}
							if err := RenderClaudeSharedRoots(actions); err != nil {
								t.Fatal(err)
							}
							if enabled == "both" && !reflect.DeepEqual(original, actions[0].Files) {
								t.Fatal("all-selected shared source changed bytes, modes, or links")
							}
							if err := WriteFiles(view, actions[0].Files); err != nil {
								t.Fatal(err)
							}
							for _, action := range actions {
								files, err := ReadFiles(filepath.Join(action.Selection.Root, "upstream"))
								if err != nil || !reflect.DeepEqual(original, files) {
									t.Fatalf("shared selection changed installed source: %v", err)
								}
								m, err := ReadClaude(filepath.Join(action.Selection.Root, "upstream"), domain.PluginSource{Marketplace: "shared-market", MarketplaceURL: root, Entry: action.Selection.Package.MarketplaceEntry})
								if err != nil || !reflect.DeepEqual(action.Selection.Package.Components, m.NativePlugin.Components) {
									t.Fatal("shared delivery mutated original component mappings")
								}
							}
							root = view
						}
						claudeNativeAt(t, home, configHome, cwd, "plugin", "marketplace", "add", root)
						for _, plugin := range []string{"first", "second"} {
							claudeNativeAt(t, home, configHome, cwd, "plugin", "install", plugin+"@shared-market", "--scope", "user", "--json")
						}
						run := func(invoke bool) []byte {
							t.Helper()
							ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
							defer cancel()
							args := []string{"--print", "--model", "claude-sonnet-4-5", "--output-format", "stream-json", "--verbose", "--tools", "", "--no-session-persistence"}
							prompt := "AIPACK_SHARED_DEFAULT_PROMPT"
							if invoke {
								if key == "commands" {
									prompt = "/" + enabled + ":" + id + " " + prompt
								} else {
									args = append(args, "--agent", enabled+":"+id)
								}
							}
							cmd := exec.CommandContext(ctx, "claude", append(args, prompt)...)
							cmd.Dir, cmd.WaitDelay = cwd, time.Second
							cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + configHome, "ANTHROPIC_API_KEY=aipack-offline-fixture", "ANTHROPIC_BASE_URL=" + api.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "HTTP_PROXY=" + api.URL, "HTTPS_PROXY=" + api.URL, "NO_PROXY=127.0.0.1,localhost"}
							mu.Lock()
							requests = nil
							mu.Unlock()
							out, err := cmd.CombinedOutput()
							if err != nil || !bytes.Contains(out, []byte("AIPACK_SCAN_RESPONSE")) {
								t.Fatalf("native shared default invocation: %v\n%s", err, out)
							}
							return out
						}
						found := false
						for _, line := range bytes.Split(run(false), []byte("\n")) {
							var init struct {
								Subtype  string   `json:"subtype"`
								Commands []string `json:"slash_commands"`
								Agents   []string `json:"agents"`
							}
							if json.Unmarshal(line, &init) != nil || init.Subtype != "init" {
								continue
							}
							found = true
							resources := init.Commands
							if key == "agents" {
								resources = init.Agents
							}
							var matched []string
							for _, resource := range resources {
								if strings.HasPrefix(resource, "first:") || strings.HasPrefix(resource, "second:") {
									matched = append(matched, resource)
								}
							}
							if enabled == "both" {
								if len(matched) != 2 || !slices.Contains(matched, "first:"+id) || !slices.Contains(matched, "second:"+id) {
									t.Fatalf("native shared defaults absent: %v", resources)
								}
								t.Logf("native shared default names: %v", matched)
							} else if enabled == "neither" {
								if len(matched) != 0 {
									t.Fatalf("disabled shared defaults leaked: %v", matched)
								}
							} else if len(matched) != 1 || matched[0] != enabled+":"+id {
								t.Fatalf("shared default selection leaked a namespace: %v", resources)
							}
						}
						if !found {
							t.Fatal("native inventory missing")
						}
						if enabled == "first" || enabled == "second" {
							run(true)
							mu.Lock()
							loaded := bytes.Contains(requests, []byte("SHARED_DEFAULT_BODY"))
							mu.Unlock()
							if !loaded {
								t.Fatal("selected shared default body did not reach the native model")
							}
						}
					})
				}
			}
		}
	}
}

func TestClaudeSharedRootSelection(t *testing.T) {
	for _, kind := range []string{"distinct", "duplicate", "scan"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			first := map[string]any{"name": "first", "source": "./", "version": "1.0.0", "skills": []string{"./skills/alpha"}}
			second := map[string]any{"name": "second", "source": "./", "version": "1.0.0", "skills": []string{"./skills/beta"}}
			if kind == "duplicate" {
				second["skills"] = first["skills"]
			} else if kind == "scan" {
				first["skills"], second["skills"] = []string{"./skills"}, []string{"./skills/alpha"}
			}
			body, err := json.Marshal(map[string]any{"name": "shared-market", "owner": map[string]any{"name": "shrug-labs"}, "plugins": []any{first, second}})
			if err != nil {
				t.Fatal(err)
			}
			write(t, root, ".claude-plugin/marketplace.json", string(body), 0o644)
			write(t, root, "skills/alpha/SKILL.md", "---\nname: alpha-name\ndescription: Shared alpha fixture.\n---\nALPHA\n", 0o644)
			write(t, root, "skills/beta/SKILL.md", "---\nname: beta-name\ndescription: Shared beta fixture.\n---\nBETA\n", 0o644)
			var selections []domain.NativePluginSelection
			for _, entry := range []map[string]any{first, second} {
				pack := t.TempDir()
				m, err := MaterializeClaude(root, pack, entry["name"].(string), domain.PluginSource{Marketplace: "shared-market", MarketplaceURL: root, Entry: entry})
				if err != nil {
					t.Fatal(err)
				}
				selections = append(selections, selection(m, pack))
			}
			build := func() []domain.NativePluginAction {
				t.Helper()
				var actions []domain.NativePluginAction
				for _, selected := range selections {
					files, entry, err := RenderClaudePackage(selected)
					if err != nil {
						t.Fatal(err)
					}
					pkg := selected.Package
					pkg.MarketplaceEntry = entry
					actions = append(actions, domain.NativePluginAction{Package: pkg, Selection: &selected, Files: files, MarketplaceDir: filepath.Join(root, "view")})
				}
				return actions
			}
			actions := build()
			original := actions[0].Files
			if err := RenderClaudeSharedRoots(actions); err != nil {
				t.Fatal(err)
			}
			for _, file := range original {
				found := false
				for _, rendered := range actions[0].Files {
					if file.Path == rendered.Path {
						found = reflect.DeepEqual(file, rendered)
						break
					}
				}
				if !found {
					t.Fatalf("all-selected shared root changed original file %s", file.Path)
				}
			}
			selections[0].Selected = maps.Clone(selections[0].Selected)
			selections[0].Selected[domain.CategorySkills] = nil
			if kind == "scan" {
				selections[0].Selected[domain.CategorySkills] = []string{"beta"}
			}
			actions = build()
			err = RenderClaudeSharedRoots(actions)
			if kind == "scan" {
				if err == nil || !strings.Contains(err.Error(), "excluded source") {
					t.Fatalf("shared scan silently activated excluded content: %v", err)
				}
				return
			}
			if err != nil || !actions[0].SharedRoot || !reflect.DeepEqual(actions[0].Files, actions[1].Files) || !strings.Contains(strings.Join(actions[0].Package.MarketplaceEntry["skills"].([]string), ","), "empty-skills") {
				t.Fatalf("shared root lost independent declarations or joint payload: %v", err)
			}
			actions = build()
			actions[1].Files[0].Mode ^= 0o100
			if err := RenderClaudeSharedRoots(actions); err == nil || !strings.Contains(err.Error(), "conflicting selected payload") {
				t.Fatalf("conflicting source modes were silently merged: %v", err)
			}
		})
	}
}

func TestClaudeSharedRootCatalogMetadata(t *testing.T) {
	for _, secondHeader := range []string{
		`"owner":{"name":"original"},"unknown":18446744073709551617`,
		`"owner":{"name":"changed"},"unknown":18446744073709551617`,
		`"owner":{"name":"original"}`,
		`"owner":{"name":"original"},"unknown":18446744073709551618`,
	} {
		t.Run(secondHeader, func(t *testing.T) {
			var actions []domain.NativePluginAction
			for i, header := range []string{`"owner":{"name":"original"},"unknown":18446744073709551617`, secondHeader} {
				name := []string{"first", "second"}[i]
				actions = append(actions, domain.NativePluginAction{Package: domain.NativePlugin{Name: name, Marketplace: "shared-market", MarketplaceEntry: map[string]any{"name": name, "source": "./"}}, MarketplaceDir: "view",
					Files: []File{{Path: ".claude-plugin/marketplace.json", Content: []byte(`{"name":"shared-market",` + header + `,"plugins":[{"name":"first","source":"./"},{"name":"second","source":"./"}]}`), Mode: 0o644}}})
			}
			err := RenderClaudeSharedRoots(actions)
			if secondHeader == `"owner":{"name":"original"},"unknown":18446744073709551617` {
				if err != nil || actions[0].Package.MarketplaceMetadata["unknown"] != json.Number("18446744073709551617") || !reflect.DeepEqual(actions[0].Package.MarketplaceMetadata, actions[1].Package.MarketplaceMetadata) {
					t.Fatalf("coherent catalog metadata was lost or rounded: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "conflicting marketplace metadata") {
				t.Fatalf("shared catalog silently discarded metadata: %v", err)
			}
		})
	}
}

func TestClaudeSharedRootDefaultCommandsAndAgents(t *testing.T) {
	for key, cat := range map[string]domain.PackCategory{"commands": domain.CategoryWorkflows, "agents": domain.CategoryAgents} {
		for _, declared := range []bool{false, true} {
			t.Run(key+"/"+map[bool]string{false: "default", true: "catalog"}[declared], func(t *testing.T) {
				root := t.TempDir()
				first := map[string]any{"name": "first", "source": "./", "version": "1.0.0"}
				second := map[string]any{"name": "second", "source": "./", "version": "1.0.0"}
				if declared {
					first[key], second[key] = []string{"./custom/explicit.md"}, []string{"./custom/explicit.md"}
				}
				catalog, err := json.Marshal(map[string]any{"name": "shared-market", "owner": map[string]any{"name": "shrug-labs"}, "plugins": []any{first, second}})
				if err != nil {
					t.Fatal(err)
				}
				write(t, root, ".claude-plugin/marketplace.json", string(catalog), 0o644)
				write(t, root, key+"/default.md", "---\nname: default\ndescription: Shared default fixture.\n---\nDEFAULT\n", 0o644)
				write(t, root, "custom/explicit.md", "---\nname: explicit\ndescription: Shared explicit fixture.\n---\nEXPLICIT\n", 0o644)
				var actions []domain.NativePluginAction
				for i, entry := range []map[string]any{first, second} {
					pack := t.TempDir()
					m, err := MaterializeClaude(root, pack, entry["name"].(string), domain.PluginSource{Marketplace: "shared-market", MarketplaceURL: root, Entry: entry})
					if err != nil {
						t.Fatal(err)
					}
					s := selection(m, pack)
					if i == 0 {
						s.Selected[cat] = nil
					}
					files, rendered, err := RenderClaudePackage(s)
					if err != nil {
						t.Fatal(err)
					}
					pkg := s.Package
					pkg.MarketplaceEntry = rendered
					actions = append(actions, domain.NativePluginAction{Package: pkg, Selection: &s, Files: files, MarketplaceDir: filepath.Join(root, "view")})
				}
				if err := RenderClaudeSharedRoots(actions); err != nil {
					t.Fatal(err)
				}
				view := t.TempDir()
				if err := WriteFiles(view, actions[0].Files); err != nil {
					t.Fatal(err)
				}
				for i, action := range actions {
					m, err := ReadClaude(view, domain.PluginSource{Marketplace: "shared-market", MarketplaceURL: view, Entry: action.Package.MarketplaceEntry})
					if err != nil {
						t.Fatal(err)
					}
					want := []string(nil)
					if i == 1 {
						want = []string{"default"}
						if declared {
							want = append(want, "explicit")
						}
					}
					if got := slices.Sorted(maps.Keys(m.NativePlugin.Components[cat])); !slices.Equal(got, want) {
						t.Fatalf("shared selection leaked or lost %s: got %v, want %v", action.Package.Name, got, want)
					}
				}
			})
		}
	}
}

func TestClaudeSharedRootCatalogEntries(t *testing.T) {
	for _, fixture := range []struct{ name, first, second, conflict string }{
		{"missing", `[{"name":"first","source":"./"},{"name":"third","source":"./"}]`, `[{"name":"second","source":"./"},{"name":"third","source":"./"}]`, ""},
		{"reordered", `[{"name":"first","source":"./"},{"name":"second","source":"./"},{"name":"third","source":"./"}]`, `[{"name":"third","source":"./"},{"name":"second","source":"./"},{"name":"first","source":"./"}]`, ""},
		{"inactive-command-order", `[{"name":"first","source":"./"},{"name":"third","source":"./","commands":{"z-first":{"source":"./action.md"},"a-later":{"source":"./action.md"}}}]`, `[{"name":"second","source":"./"},{"name":"third","source":"./","commands":{"z-first":{"source":"./action.md"},"a-later":{"source":"./action.md"}}}]`, ""},
		{"inactive-conflict", `[{"name":"first","source":"./"},{"name":"second","source":"./"},{"name":"third","source":"./","counter":18446744073709551617}]`, `[{"name":"first","source":"./"},{"name":"second","source":"./"},{"name":"third","source":"./","counter":18446744073709551618}]`, "conflicting inactive catalog entry"},
		{"invalid-name", `[{"name":"../outside","source":"./"}]`, `[]`, "invalid or duplicate catalog entry"},
		{"duplicate-name", `[{"name":"first","source":"./"},{"name":"first","source":"./"}]`, `[]`, "invalid or duplicate catalog entry"},
		{"invalid-array", `{}`, `[]`, "catalog plugins array"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var rendered []File
			for _, reverse := range []bool{false, true} {
				var actions []domain.NativePluginAction
				for i, entries := range []string{fixture.first, fixture.second} {
					name := []string{"first", "second"}[i]
					actions = append(actions, domain.NativePluginAction{Package: domain.NativePlugin{Name: name, Marketplace: "shared-market", MarketplaceEntry: map[string]any{"name": name, "source": "./", "version": "selected"}}, MarketplaceDir: "view",
						Files: []File{{Path: ".claude-plugin/marketplace.json", Content: []byte(`{"name":"shared-market","owner":{"name":"shrug-labs"},"plugins":` + entries + `}`), Mode: 0o644}}})
				}
				if reverse {
					slices.Reverse(actions)
				}
				err := RenderClaudeSharedRoots(actions)
				if fixture.conflict != "" {
					if err == nil || !strings.Contains(err.Error(), fixture.conflict) {
						t.Fatalf("ambiguous inactive catalog was silently merged: %v", err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				var catalog struct{ Plugins []map[string]any }
				if err := util.UnmarshalJSON(actions[0].Files[0].Content, &catalog); err != nil {
					t.Fatal(err)
				}
				var names []string
				for _, entry := range catalog.Plugins {
					names = append(names, entry["name"].(string))
					if entry["name"] != "third" && entry["version"] != "selected" {
						t.Fatal("shared catalog lost an active entry's selected declaration")
					}
				}
				if !slices.Equal(names, []string{"first", "second", "third"}) || (reverse && !reflect.DeepEqual(rendered, actions[0].Files)) {
					t.Fatalf("catalog membership or profile-order invariance changed: %v", names)
				}
				if fixture.name == "inactive-command-order" {
					entries, err := CatalogEntries(actions[0].Files[0].Content)
					if err != nil {
						t.Fatal(err)
					}
					raw, err := json.Marshal(entries[2]["commands"])
					if err != nil {
						t.Fatal(err)
					}
					order, err := util.JSONPropertyNames(raw)
					if err != nil || !slices.Equal(order, []string{"z-first", "a-later"}) {
						t.Fatalf("shared catalog changed an inactive alias: %v %v", order, err)
					}
				}
				rendered = actions[0].Files
			}
		})
	}
}
