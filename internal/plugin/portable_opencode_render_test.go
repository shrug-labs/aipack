package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestCodexOpenCodeRendering(t *testing.T) {
	for _, format := range []string{CodexLegacy, AgentPlugins} {
		t.Run(format, func(t *testing.T) {
			source, pack, target, data := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
			manifest, mcpPath := ".codex-plugin/plugin.json", ".mcp.json"
			body := `{"name":"portable-probe","version":"1.0.0"}`
			entry := map[string]any{"command": "python3", "args": []string{"server.py", "${TOKEN}"}, "cwd": ".", "env": map[string]string{"LITERAL": "${TOKEN}"}}
			if format == AgentPlugins {
				manifest, mcpPath = "plugin.json", "mcp.json"
				body = `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"portable-probe","version":"1.0.0"}`
				delete(entry, "cwd")
				entry["type"] = "stdio"
				entry["env"].(map[string]string)["ROOT"] = "${PLUGIN_ROOT}"
			}
			write(t, source, manifest, body, 0o644)
			write(t, source, "skills/selected/SKILL.md", "---\nname: selected\ndescription: Trigger ${CODEX_PLUGIN_ROOT}\nmetadata:\n  owner: shrug-labs\n---\nRead ../excluded/shared.txt. Root ${CODEX_PLUGIN_ROOT}; data ${CODEX_PLUGIN_DATA}; keep $CODEX_PLUGIN_ROOT_SUFFIX.\n", 0o644)
			write(t, source, "skills/selected/references/nested/SKILL.md", "---\nname: nested\ndescription: Nested asset\n---\nNESTED_BODY\n", 0o644)
			write(t, source, "skills/excluded/SKILL.md", "---\nname: excluded\ndescription: Excluded fixture\n---\nEXCLUDED_BODY\n", 0o644)
			write(t, source, "skills/excluded/shared.txt", "shared", 0o440)
			write(t, source, "server.py", "# owned executable asset\n", 0o755)
			if err := os.Symlink("skills/excluded/shared.txt", filepath.Join(source, "shared-alias.txt")); err != nil {
				t.Fatal(err)
			}
			servers := map[string]any{"mcpServers": map[string]any{"probe": entry}}
			if format == AgentPlugins {
				servers["$schema"] = agentMCPSchema
			}
			encoded, _ := json.Marshal(servers)
			write(t, source, mcpPath, string(encoded), 0o644)
			m, err := MaterializeCodex(source, pack, "alias", "portable-market")
			if err != nil {
				t.Fatal(err)
			}
			s := selection(m, pack)
			s.Selected[domain.CategorySkills] = []string{"selected"}
			s.MCPPolicy = map[string]domain.NativeMCPPolicy{"probe": {AllowedTools: []string{"read.file"}, AlwaysAllowedTools: []string{"read.file"}, DisabledTools: []string{"hidden", "read.file"}}}
			before, _ := ReadFiles(filepath.Join(pack, "upstream"))
			files, settings, err := RenderCodexForOpenCode(s, target, data)
			if err != nil {
				t.Fatal(err)
			}
			byPath := map[string]File{}
			for _, file := range files {
				byPath[file.Path] = file
			}
			if selected := string(byPath["skills/selected/SKILL.md"].Content); !strings.Contains(selected, "name: portable-probe@portable-market:selected") || !strings.Contains(selected, target) || !strings.Contains(selected, data) || !strings.Contains(selected, "Trigger ${CODEX_PLUGIN_ROOT}") || !strings.Contains(selected, "$CODEX_PLUGIN_ROOT_SUFFIX") {
				t.Fatalf("skill identity, metadata or reference translation failed: %s", selected)
			}
			if byPath["shared-alias.txt"].Link != "skills/excluded/shared.txt" || byPath["skills/excluded/shared.txt"].Mode.Perm() != 0o440 || byPath["server.py"].Mode.Perm() != 0o755 {
				t.Fatal("portable payload lost links or modes")
			}
			permissions := settings["permission"].(map[string]any)
			skills := permissions["skill"].(map[string]string)
			if skills["portable-probe@portable-market:*"] != "deny" || skills["portable-probe@portable-market:selected"] != "ask" || skills["portable-probe@portable-market:nested"] != "" || permissions["portable-probe_portable-market_probe_read_file"] != "deny" {
				t.Fatalf("selection or tool precedence failed: %#v", permissions)
			}
			if err := WriteFiles(target, files); err != nil {
				t.Fatal(err)
			}
			after, _ := ReadFiles(filepath.Join(pack, "upstream"))
			if !reflect.DeepEqual(before, after) {
				t.Fatal("portable rendering changed installed source")
			}
			if _, _, err := RenderCodexForOpenCode(s, target, filepath.Join(target, "data")); err == nil {
				t.Fatal("runtime data inside a replaced payload was accepted")
			}
			write(t, pack, "upstream/skills/selected/references/nested/SKILL.md", "---\nname: selected\ndescription: Shadow fixture\n---\nSHADOW_BODY\n", 0o644)
			if _, _, err := RenderCodexForOpenCode(s, target, data); err == nil || !strings.Contains(err.Error(), "collides") {
				t.Fatalf("recursive skill shadow was accepted: %v", err)
			}
		})
	}
}

func TestCodexOpenCodeRefusals(t *testing.T) {
	for _, test := range []struct{ name, fields, reason string }{
		{"startup", `"startup_timeout_sec":0`, "positive finite"},
		{"tool-timeout", `"tool_timeout_sec":3`, "tool_timeout_sec"},
		{"config-arg", `"args":["{file:secret}"]`, "references"},
		{"config-env", `"env":{"TOKEN":"{env:TOKEN}"}`, "references"},
		{"config-env-name", `"env":{"{env:NAME}":"literal"}`, "environment names"},
		{"null-args", `"args":[null]`, "string array"},
		{"null-env", `"env":{"TOKEN":null}`, "string values"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			entry := map[string]json.RawMessage{"command": json.RawMessage(`"python3"`), "cwd": json.RawMessage(`"."`)}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte("{"+test.fields+"}"), &fields); err != nil {
				t.Fatal(err)
			}
			for key, value := range fields {
				entry[key] = value
			}
			body, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"probe": entry}})
			write(t, root, "upstream/mcp.json", string(body), 0o644)
			s := domain.NativePluginSelection{Root: root, Package: domain.NativePlugin{Harness: domain.HarnessCodex, Format: CodexLegacy, Name: "probe", Marketplace: "market", ConverterVersion: ConverterVersion, Components: map[domain.PackCategory]map[string][]string{domain.CategoryMCP: {"probe": {"mcp.json"}}}}, Selected: map[domain.PackCategory][]string{domain.CategoryMCP: {"probe"}}}
			if issues := CodexOpenCodeIssues(s); len(issues) != 1 || !strings.Contains(issues[0], test.reason) {
				t.Fatalf("unmapped declaration accepted: %v", issues)
			}
		})
	}
}
