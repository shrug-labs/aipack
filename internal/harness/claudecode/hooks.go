package claudecode

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
)

func RenderHooks(hooks []domain.Hook) (map[string][]any, string, error) {
	if len(hooks) == 0 {
		return nil, "", nil
	}
	events := map[string][]any{}
	for _, hook := range hooks {
		for eventIndex, event := range hook.Events {
			nativeEvent, matcher, err := claudeNativeHookEvent(event)
			if err != nil {
				return nil, "", fmt.Errorf("hook %s/%s events[%d]: %w", hook.SourcePack, hook.ID, eventIndex, err)
			}
			var handlers []any
			for handlerIndex, handler := range event.EffectiveHandlers() {
				nativeHandler, err := claudeNativeHookHandler(handler)
				if err != nil {
					return nil, "", fmt.Errorf("hook %s/%s events[%d].handlers[%d]: %w", hook.SourcePack, hook.ID, eventIndex, handlerIndex, err)
				}
				if nativeHandler == nil {
					continue
				}
				handlers = append(handlers, nativeHandler)
			}
			if len(handlers) == 0 {
				continue
			}
			group := map[string]any{"hooks": handlers}
			if matcher != "" {
				group["matcher"] = matcher
			}
			events[nativeEvent] = append(events[nativeEvent], group)
		}
	}
	if len(events) == 0 {
		return nil, "", nil
	}
	return events, harness.CompositeSourcePack(hooks, func(h domain.Hook) string { return h.SourcePack }), nil
}

func claudeNativeHookEvent(event domain.HookEvent) (string, string, error) {
	switch event.On {
	case domain.HookEventRunStart:
		return "SessionStart", domain.NormalizeHookMatcher(event.Match.Source), nil
	case domain.HookEventPromptSubmit:
		return "UserPromptSubmit", "", nil
	case domain.HookEventToolBefore:
		return "PreToolUse", domain.NormalizeHookMatcher(event.Match.Tool), nil
	case domain.HookEventToolAfter:
		return "PostToolUse", domain.NormalizeHookMatcher(event.Match.Tool), nil
	case domain.HookEventCompactBefore:
		return "PreCompact", domain.NormalizeHookMatcher(event.Match.Source), nil
	default:
		return "", "", fmt.Errorf("invalid hook event %q", event.On)
	}
}

func claudeNativeHookHandler(handler domain.HookHandler) (map[string]any, error) {
	if handler.Type != "" && handler.Type != domain.HookHandlerTypeCommand {
		return nil, fmt.Errorf("handler type %q must be %q", handler.Type, domain.HookHandlerTypeCommand)
	}
	if handler.Mode == domain.HookHandlerModeAsync {
		return nil, fmt.Errorf("handler mode %q cannot render a synchronous command hook", handler.Mode)
	}
	command := handler.Command
	if runtime.GOOS == "windows" && handler.CommandWindows != "" {
		command = handler.CommandWindows
	}
	if strings.TrimSpace(command) == "" {
		if strings.TrimSpace(handler.CommandWindows) != "" {
			// command_windows-only hook on a non-Windows host: no command applies
			// here, so skip it rather than failing the whole sync.
			return nil, nil
		}
		return nil, fmt.Errorf("command is required")
	}
	timeoutSeconds, err := domain.HookTimeoutSeconds(handler.Timeout)
	if err != nil {
		return nil, err
	}
	native := map[string]any{
		"type":    string(domain.HookHandlerTypeCommand),
		"command": command,
	}
	if timeoutSeconds > 0 {
		native["timeout"] = timeoutSeconds
	}
	if handler.StatusMessage != "" {
		native["statusMessage"] = handler.StatusMessage
	}
	return native, nil
}

func stripManagedHooks(root map[string]any, ctx harness.EditContext) {
	if len(ctx.PreviousManagedOverlay) == 0 {
		return
	}
	prevRoot := map[string]any{}
	if err := json.Unmarshal(ctx.PreviousManagedOverlay, &prevRoot); err != nil {
		return
	}
	prevHooks, ok := prevRoot["hooks"].(map[string]any)
	if !ok {
		return
	}
	diskHooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return
	}
	for event, rawPrevGroups := range prevHooks {
		prevGroups, ok := rawPrevGroups.([]any)
		if !ok {
			continue
		}
		diskGroups, ok := diskHooks[event].([]any)
		if !ok {
			continue
		}
		filtered := removeCanonicalHookGroups(diskGroups, prevGroups)
		if len(filtered) == 0 {
			delete(diskHooks, event)
		} else {
			diskHooks[event] = filtered
		}
	}
	if len(diskHooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = diskHooks
	}
}

func removeCanonicalHookGroups(diskGroups, prevGroups []any) []any {
	prev := map[string]struct{}{}
	for _, group := range prevGroups {
		if key, ok := canonicalHookGroupKey(group); ok {
			prev[key] = struct{}{}
		}
	}
	if len(prev) == 0 {
		return diskGroups
	}
	out := make([]any, 0, len(diskGroups))
	for _, group := range diskGroups {
		key, ok := canonicalHookGroupKey(group)
		if ok {
			if _, managed := prev[key]; managed {
				continue
			}
		}
		out = append(out, group)
	}
	return out
}

func canonicalHookGroupKey(group any) (string, bool) {
	b, err := json.Marshal(group)
	if err != nil {
		return "", false
	}
	return string(b), true
}
