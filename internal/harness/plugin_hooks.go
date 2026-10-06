package harness

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// RewriteHookPack edits the generated handler inventory while retaining the
// runner and every sibling handler. An empty newName removes the pack.
func RewriteHookPack(content []byte, oldName, newName, oldRoot, newRoot string) ([]byte, string, bool, error) {
	prefix := []byte("const handlers = ")
	encoded := false
	start := bytes.Index(content, prefix)
	if start < 0 {
		prefix = []byte(`$handlersJson = [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String("`)
		start = bytes.Index(content, prefix)
		encoded = true
	}
	if start < 0 {
		return content, "", false, nil
	}
	start += len(prefix)
	raw, end := content[start:], start
	if encoded {
		length := bytes.IndexByte(raw, '"')
		if length < 0 {
			return nil, "", false, fmt.Errorf("invalid generated hook inventory")
		}
		end += length
		var err error
		raw, err = base64.StdEncoding.DecodeString(string(raw[:length]))
		if err != nil {
			return nil, "", false, err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var handlers []map[string]json.RawMessage
	if err := decoder.Decode(&handlers); err != nil {
		return nil, "", false, fmt.Errorf("read generated hook inventory: %w", err)
	}
	if !encoded {
		end += int(decoder.InputOffset())
	}
	kept := make([]map[string]json.RawMessage, 0, len(handlers))
	var owners []string
	changed := false
	for _, handler := range handlers {
		var label string
		if err := json.Unmarshal(handler["label"], &label); err != nil {
			return nil, "", false, fmt.Errorf("invalid generated hook label: %w", err)
		}
		if strings.HasPrefix(label, oldName+"/") {
			if newName == "" {
				changed = true
				continue
			}
			nextLabel := newName + strings.TrimPrefix(label, oldName)
			if nextLabel != label {
				changed = true
				label = nextLabel
				handler["label"], _ = json.Marshal(label)
			}
			for key, raw := range handler {
				var value string
				if json.Unmarshal(raw, &value) == nil {
					if next := RelocatePackPath(value, oldRoot, newRoot); next != value {
						changed = true
						handler[key], _ = json.Marshal(next)
					}
				}
			}
		}
		owner, _, _ := strings.Cut(label, "/")
		owners = append(owners, owner)
		kept = append(kept, handler)
	}
	if !changed {
		return content, "", false, nil
	}
	if len(kept) == 0 {
		return nil, "", true, nil
	}
	body, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return nil, "", false, err
	}
	if encoded {
		body = []byte(base64.StdEncoding.EncodeToString(body))
	}
	out := append(bytes.Clone(content[:start]), body...)
	out = append(out, content[end:]...)
	return out, CompositeSourcePack(owners, func(owner string) string { return owner }), true, nil
}

// RelocatePackPath replaces complete pack-root references, including children.
func RelocatePackPath(value, oldRoot, newRoot string) string {
	if oldRoot == "" || oldRoot == newRoot {
		return value
	}
	if value == oldRoot {
		return newRoot
	}
	return strings.ReplaceAll(value, oldRoot+string(filepath.Separator), newRoot+string(filepath.Separator))
}

// PluginHookRuntime bridges native command hook envelopes through the existing
// OpenCode/Cline runners. It never invents transcript paths or turn identifiers.
const PluginHookRuntime = `function pluginHookOptions(handler, cwd) {
  const options = {shell: true, cwd, detached: process.platform !== "win32", stdio: ["pipe", "pipe", "pipe"]};
  if (!handler.pluginEvent) return options;
  mkdirSync(handler.pluginData, {recursive: true, mode: 0o700});
  options.env = {...process.env};
  for (const prefix of ["PLUGIN", "CODEX_PLUGIN", "CLAUDE_PLUGIN"]) {
    options.env[prefix + "_ROOT"] = handler.pluginRoot;
    options.env[prefix + "_DATA"] = handler.pluginData;
  }
  return options;
}

function stopHookCommand(child) {
  if (!child.pid) return;
  if (process.platform !== "win32") {
    try { process.kill(-child.pid, "SIGKILL"); }
    catch (error) { if (error.code !== "ESRCH") child.kill("SIGKILL"); }
    return;
  }
  spawn("taskkill.exe", ["/PID", String(child.pid), "/T", "/F"], {stdio: "ignore", windowsHide: true})
    .on("error", () => child.kill());
}

function pluginHookInput(handler, payload, cwd) {
  const input = payload.input ?? payload;
  const output = payload.output ?? {};
  const before = input.preToolUse ?? input.tool_call ?? {};
  const after = input.postToolUse ?? input.tool_result ?? {};
  const result = {
    hook_event_name: handler.pluginEvent,
    session_id: input.session_id ?? input.sessionID ?? input.taskId,
    cwd: input.cwd ?? input.workspaceRoots?.[0] ?? cwd ?? process.cwd(),
  };
  if (input.turn_id !== undefined) result.turn_id = input.turn_id;
  if (input.transcript_path !== undefined) result.transcript_path = input.transcript_path;
  if (handler.pluginEvent === "SessionStart") result.source = input.source ?? (input.taskResume ? "resume" : "startup");
  if (handler.pluginEvent === "PreCompact" && (input.trigger ?? input.source) !== undefined) result.trigger = input.trigger ?? input.source;
  if (handler.pluginEvent === "UserPromptSubmit") {
    result.prompt = input.prompt ?? input.userPromptSubmit?.prompt ?? (output.parts ?? []).filter(p => p.type === "text").map(p => p.text).join("\n");
  }
  if (handler.pluginEvent === "PreToolUse" || handler.pluginEvent === "PostToolUse") {
    result.tool_name = before.toolName ?? before.name ?? after.toolName ?? after.name ?? toolName(input);
    result.tool_input = before.parameters ?? before.input ?? after.parameters ?? after.input ?? output.args ?? input.args ?? {};
    result.tool_use_id = input.callID ?? before.id ?? after.id;
    if (handler.pluginEvent === "PostToolUse") result.tool_response = after.result ?? after.output ?? output.output;
  }
  return result;
}

function pluginHookOutput(handler, stdout, stderr, code) {
  if (code !== 0 && code !== 2) {
    console.error("[aipack hooks] " + handler.label + " exited " + code + (stderr.trim() ? ": " + stderr.trim() : ""));
    return null;
  }
  let value;
  try { value = JSON.parse(stdout); } catch {}
  const specific = value?.hookSpecificOutput ?? {};
  const context = specific.additionalContext ?? value?.additionalContext ?? value?.additional_context;
  const blocked = code === 2 || value?.continue === false || value?.decision === "block" || specific.permissionDecision === "deny";
  const result = { cancel: blocked };
  if (typeof context === "string") result.contextModification = context;
  else if (!value && code === 0 && (handler.pluginEvent === "SessionStart" || handler.pluginEvent === "UserPromptSubmit") && stdout.trim()) result.contextModification = stdout.trim();
  const reason = specific.permissionDecisionReason ?? value?.reason ?? value?.stopReason ?? stderr;
  if (blocked) result.errorMessage = String(reason || "Blocked by imported hook");
  if (specific.permissionDecision === "allow" && specific.updatedInput !== undefined) result.overrideInput = specific.updatedInput;
  if (specific.permissionDecision === "ask") result.review = true;
  return result;
}
`
