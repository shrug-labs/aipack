package opencode

import (
	"encoding/json"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
)

const HooksPluginFile = "aipack-hooks.js"

type openCodeHookHandler struct {
	Event          string `json:"event"`
	Tool           string `json:"tool,omitempty"`
	Source         string `json:"source,omitempty"`
	Command        string `json:"command"`
	CommandWindows string `json:"commandWindows,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	Label          string `json:"label"`
}

func RenderHooksPlugin(hooks []domain.Hook) ([]byte, string, []domain.Warning, error) {
	if len(hooks) == 0 {
		return nil, "", nil, nil
	}
	var handlers []openCodeHookHandler
	for _, hook := range hooks {
		for eventIndex, event := range hook.Events {
			for handlerIndex, handler := range event.EffectiveHandlers() {
				native, err := openCodeNativeHookHandler(hook, event, handler, eventIndex, handlerIndex)
				if err != nil {
					return nil, "", nil, err
				}
				handlers = append(handlers, native)
			}
		}
	}
	if len(handlers) == 0 {
		return nil, "", nil, nil
	}
	handlerJSON, err := json.MarshalIndent(handlers, "", "  ")
	if err != nil {
		return nil, "", nil, err
	}
	var b strings.Builder
	b.WriteString("import { spawn } from \"node:child_process\";\n\n")
	b.WriteString("const handlers = ")
	b.Write(handlerJSON)
	b.WriteString(";\n\n")
	b.WriteString(openCodeHooksRuntime)
	return []byte(b.String()), harness.CompositeSourcePack(hooks, func(h domain.Hook) string { return h.SourcePack }), nil, nil
}

func openCodeNativeHookHandler(hook domain.Hook, event domain.HookEvent, handler domain.HookHandler, eventIndex, handlerIndex int) (openCodeHookHandler, error) {
	normalized, err := harness.NormalizeCommandHookHandler(hook, handler, eventIndex, handlerIndex)
	if err != nil {
		return openCodeHookHandler{}, err
	}
	return openCodeHookHandler{
		Event:          string(event.On),
		Tool:           domain.NormalizeHookMatcher(event.Match.Tool),
		Source:         domain.NormalizeHookMatcher(event.Match.Source),
		Command:        normalized.Command,
		CommandWindows: normalized.CommandWindows,
		TimeoutSeconds: normalized.TimeoutSeconds,
		Label:          normalized.Label,
	}, nil
}

const openCodeHooksRuntime = `function matcherMatches(pattern, value, options = {}) {
  if (!pattern || pattern === "*") return true;
  const text = String(value ?? "");
  try {
    return new RegExp(pattern, options.caseInsensitive ? "i" : "").test(text);
  } catch (error) {
    console.error("[aipack hooks] invalid matcher /" + pattern + "/: " + error.message);
    return false;
  }
}

function toolName(input) {
  return input?.tool?.name ?? input?.toolName ?? input?.tool ?? "";
}

function sourceName(eventName, input) {
  const source =
    input?.source ??
    input?.hook?.source ??
    input?.event?.source ??
    input?.event?.metadata?.source ??
    input?.session?.source ??
    input?.message?.source ??
    "";
  if (source) return source;
  if (eventName === "run.start" && input?.event?.type === "session.created") return "startup";
  return "";
}

function matchesHandler(handler, eventName, input) {
  if (handler.event !== eventName) return false;
  if (handler.tool && !matcherMatches(handler.tool, toolName(input), { caseInsensitive: true })) return false;
  if (handler.source && !matcherMatches(handler.source, sourceName(eventName, input))) return false;
  return true;
}

async function runCommand(handler, payloadJSON) {
  const command = process.platform === "win32" && handler.commandWindows ? handler.commandWindows : handler.command;
  if (!command) return;
  await new Promise((resolve) => {
    const child = spawn(command, { shell: true, stdio: ["pipe", "ignore", "pipe"] });
    let done = false;
    let stderr = "";
    const finish = (message) => {
      if (done) return;
      done = true;
      if (timer) clearTimeout(timer);
      if (message) console.error(message);
      resolve();
    };
    const timer = handler.timeoutSeconds > 0 ? setTimeout(() => {
      child.kill();
      finish("[aipack hooks] " + handler.label + " timed out after " + handler.timeoutSeconds + "s");
    }, handler.timeoutSeconds * 1000) : null;
    child.stderr.on("data", (chunk) => {
      stderr += String(chunk);
      if (stderr.length > 4096) stderr = stderr.slice(-4096);
    });
    child.stdin.on("error", () => {});
    child.on("error", (error) => finish("[aipack hooks] " + handler.label + " failed to start: " + error.message));
    child.on("close", (code, signal) => {
      if (code === 0) return finish();
      const suffix = stderr.trim() ? ": " + stderr.trim() : "";
      finish("[aipack hooks] " + handler.label + " exited " + (signal || code) + suffix);
    });
    try {
      child.stdin.end(payloadJSON);
    } catch {
      finish("[aipack hooks] " + handler.label + " failed to receive stdin payload");
    }
  });
}

async function runHandlers(eventName, input = {}, output = {}) {
  const payloadJSON = JSON.stringify({ event: eventName, input, output });
  for (const handler of handlers) {
    if (!matchesHandler(handler, eventName, input)) continue;
    await runCommand(handler, payloadJSON);
  }
}

async function server() {
  return {
    event: async ({ event }) => {
      if (event?.type === "session.created") await runHandlers("run.start", { event }, {});
    },
    "chat.message": async (input, output) => {
      await runHandlers("prompt.submit", input, output);
    },
    "tool.execute.before": async (input, output) => {
      await runHandlers("tool.before", input, output);
    },
    "tool.execute.after": async (input, output) => {
      await runHandlers("tool.after", input, output);
    },
    "experimental.session.compacting": async (input, output) => {
      await runHandlers("compact.before", input, output);
    }
  };
}

export default { id: "aipack-hooks", server };
`
