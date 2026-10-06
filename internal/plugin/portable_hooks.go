package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
)

var errHookUnavailable = errors.New("native hook mapping unavailable")

// codexHookGroups reads only declarations admitted by the source manifest.
// In particular, an explicit empty hooks object suppresses fallback discovery.
func codexHookGroups(s domain.NativePluginSelection, id string) ([]map[string]any, error) {
	if s.Package.Format != CodexLegacy {
		return nil, fmt.Errorf("source format does not declare native hooks")
	}
	event := s.Package.HookEvents[id]
	if event == "" || len(s.Package.Components[domain.CategoryHooks][id]) == 0 {
		return nil, fmt.Errorf("unknown hook selector %q", id)
	}
	root := filepath.Join(s.Root, "upstream")
	manifest, err := readObject(root, s.Package.Manifest)
	if err != nil {
		return nil, err
	}
	var result []map[string]any
	err = visitHooks(root, manifest, func(path string, obj map[string]json.RawMessage) error {
		if !slices.Contains(s.Package.Components[domain.CategoryHooks][id], path) {
			return nil
		}
		var events map[string]json.RawMessage
		if err := json.Unmarshal(obj["hooks"], &events); err != nil {
			return err
		}
		if raw := events[event]; len(raw) > 0 {
			var groups []map[string]any
			if err := json.Unmarshal(raw, &groups); err != nil {
				return err
			}
			result = append(result, groups...)
		}
		return nil
	})
	if err == nil && len(result) == 0 {
		err = fmt.Errorf("hook %q no longer has a source declaration", id)
	}
	return result, err
}

// GenericHooks maps declared command hooks onto the existing pack adapters.
// Missing lifecycle events are reported automatically, not profile repair work.
func GenericHooks(s domain.NativePluginSelection, target domain.Harness, data string) ([]domain.Hook, []domain.Warning, error) {
	if target != domain.HarnessCline && target != domain.HarnessOpenCode {
		return nil, nil, fmt.Errorf("generic hooks to %s are not supported", target)
	}
	events := map[string]domain.HookEventName{
		"SessionStart": domain.HookEventRunStart, "UserPromptSubmit": domain.HookEventPromptSubmit,
		"PreToolUse": domain.HookEventToolBefore, "PostToolUse": domain.HookEventToolAfter,
		"PreCompact": domain.HookEventCompactBefore,
	}
	var hooks []domain.Hook
	var warnings []domain.Warning
	for _, id := range s.Selected[domain.CategoryHooks] {
		event := s.Package.HookEvents[id]
		on, available := events[event]
		if !available {
			warnings = append(warnings, domain.Warning{Field: "hooks", Message: fmt.Sprintf("%s/%s: %s has no equivalent %s lifecycle event; this hook is unavailable on that target", s.SourcePack, id, event, target)})
			continue
		}
		groups, err := codexHookGroups(s, id)
		if err != nil {
			return nil, warnings, err
		}
		hook := domain.Hook{ID: id, Name: id, SourcePack: s.SourcePack, SourcePath: filepath.Join(s.Root, "upstream", s.Package.Components[domain.CategoryHooks][id][0])}
		for _, group := range groups {
			matcher, ok := group["matcher"].(string)
			if _, exists := group["matcher"]; exists && !ok {
				return nil, warnings, fmt.Errorf("hook %s matcher must be a string", id)
			}
			if on == domain.HookEventCompactBefore && matcher != "" && matcher != "*" {
				warnings = append(warnings, domain.Warning{Field: "hooks", Message: fmt.Sprintf("%s/%s: source compaction-trigger matching is unavailable on %s", s.SourcePack, id, target)})
				continue
			}
			match := domain.HookMatch{}
			if on == domain.HookEventToolBefore || on == domain.HookEventToolAfter {
				match.Tool = matcher
			} else if on != domain.HookEventPromptSubmit {
				match.Source = matcher
			}
			rawHandlers, ok := group["hooks"].([]any)
			if !ok {
				return nil, warnings, fmt.Errorf("hook %s requires a handlers array", id)
			}
			e := domain.HookEvent{On: on, Match: match}
			for _, raw := range rawHandlers {
				h, ok := raw.(map[string]any)
				if !ok {
					return nil, warnings, fmt.Errorf("hook %s handler must be an object", id)
				}
				if h["type"] != "command" {
					warnings = append(warnings, domain.Warning{Field: "hooks", Message: fmt.Sprintf("%s/%s: non-command handler is unavailable on %s", s.SourcePack, id, target)})
					continue
				}
				command, ok := h["command"].(string)
				if !ok || strings.TrimSpace(command) == "" {
					return nil, warnings, fmt.Errorf("hook %s command must be a nonempty string", id)
				}
				if async, exists := h["async"]; exists && async != false {
					warnings = append(warnings, domain.Warning{Field: "hooks", Message: fmt.Sprintf("%s/%s: source asynchronous handler remains inactive", s.SourcePack, id)})
					continue
				}
				timeout, err := codexHookTimeout(h)
				if err != nil {
					return nil, warnings, fmt.Errorf("hook %s: %w", id, err)
				}
				e.Handlers = append(e.Handlers, domain.HookHandler{Type: domain.HookHandlerTypeCommand, Command: command, Timeout: fmt.Sprintf("%.0f", timeout), PluginEvent: event, PluginRoot: filepath.Join(s.Root, "upstream"), PluginData: data})
			}
			if len(e.Handlers) > 0 {
				hook.Events = append(hook.Events, e)
			}
		}
		if len(hook.Events) > 0 {
			hooks = append(hooks, hook)
		}
	}
	return hooks, warnings, nil
}

func codexHookTimeout(handler map[string]any) (float64, error) {
	v, exists := handler["timeout"]
	if !exists {
		return 600, nil // Preserve Codex's default on every destination.
	}
	n, ok := v.(float64)
	if !ok || n < 1 || n > 2147483 || n != float64(int64(n)) {
		return 0, fmt.Errorf("timeout must be whole seconds between 1 and 2147483")
	}
	return n, nil
}

func codexClaudeHookGroups(s domain.NativePluginSelection, id string) ([]map[string]any, error) {
	event := s.Package.HookEvents[id]
	// These are the source's dispatched events with matching Claude lifecycle names.
	if !slices.Contains([]string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "PreCompact", "PostCompact", "SubagentStart", "SubagentStop", "Stop"}, event) {
		return nil, fmt.Errorf("%w: event %q is unsupported on Claude Code", errHookUnavailable, event)
	}
	groups, err := codexHookGroups(s, id)
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		if matcher, exists := group["matcher"]; exists {
			if _, ok := matcher.(string); !ok {
				return nil, fmt.Errorf("hook matcher must be a string")
			}
		}
		// Codex ignores matchers on prompt/stop; do not give them new semantics.
		if event == "UserPromptSubmit" || event == "Stop" {
			delete(group, "matcher")
		}
		handlers, ok := group["hooks"].([]any)
		if !ok {
			return nil, fmt.Errorf("hook group requires a handlers array")
		}
		for _, raw := range handlers {
			handler, ok := raw.(map[string]any)
			if !ok || handler["type"] != "command" {
				return nil, fmt.Errorf("%w: only command handlers are supported on Claude Code", errHookUnavailable)
			}
			command, ok := handler["command"].(string)
			if !ok || strings.TrimSpace(command) == "" {
				return nil, fmt.Errorf("hook command must be a nonempty string")
			}
			if async, exists := handler["async"]; exists && async != false {
				return nil, fmt.Errorf("%w: source asynchronous hooks are not dispatched by Codex", errHookUnavailable)
			}
			// Source commands can read any of these names, including process.env.
			// Expand the target root once before replacing CLAUDE_PLUGIN_ROOT.
			handler["command"] = `export PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT}/payload" CODEX_PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT}/payload" PLUGIN_DATA="$CLAUDE_PLUGIN_DATA" CODEX_PLUGIN_DATA="$CLAUDE_PLUGIN_DATA"; export CLAUDE_PLUGIN_ROOT="$PLUGIN_ROOT"; ` + command
			timeout, err := codexHookTimeout(handler)
			if err != nil {
				return nil, err
			}
			handler["timeout"] = timeout
		}
	}
	return groups, nil
}
