package codex

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

type codexHookEventSpec struct {
	name          string
	stateLabel    string
	allowsMatcher bool
}

var codexHookEvents = []codexHookEventSpec{
	{name: "PreToolUse", stateLabel: "pre_tool_use", allowsMatcher: true},
	{name: "PermissionRequest", stateLabel: "permission_request", allowsMatcher: true},
	{name: "PostToolUse", stateLabel: "post_tool_use", allowsMatcher: true},
	{name: "PreCompact", stateLabel: "pre_compact", allowsMatcher: true},
	{name: "PostCompact", stateLabel: "post_compact", allowsMatcher: true},
	{name: "SessionStart", stateLabel: "session_start", allowsMatcher: true},
	{name: "UserPromptSubmit", stateLabel: "user_prompt_submit"},
	{name: "SubagentStart", stateLabel: "subagent_start", allowsMatcher: true},
	{name: "SubagentStop", stateLabel: "subagent_stop", allowsMatcher: true},
	{name: "Stop", stateLabel: "stop"},
}

var codexHookEventByName = buildCodexHookEventMap()

type codexHooksFile struct {
	Hooks map[string][]any `json:"hooks"`
}

// RenderedHooks is the rendered hooks.json payload plus the config.toml trust
// state required for those hooks.
type RenderedHooks struct {
	JSON       []byte
	TrustState map[string]string
	SourcePack string
}

// RenderHooksJSON renders AIPack hook descriptors into the single hooks.json
// file Codex loads for the config layer.
func RenderHooksJSON(hooks []domain.Hook, hooksPath string) (RenderedHooks, error) {
	if len(hooks) == 0 {
		return RenderedHooks{}, nil
	}
	events := map[string][]any{}
	for _, hook := range hooks {
		parsed, err := renderCodexHook(hook)
		if err != nil {
			return RenderedHooks{}, err
		}
		for _, spec := range codexHookEvents {
			if groups := parsed[spec.name]; len(groups) > 0 {
				events[spec.name] = append(events[spec.name], groups...)
			}
		}
	}
	if len(events) == 0 {
		return RenderedHooks{}, nil
	}
	out, err := util.MarshalPrettyJSON(codexHooksFile{Hooks: events})
	if err != nil {
		return RenderedHooks{}, err
	}
	state, err := buildCodexHookTrustState(events, hooksPath)
	if err != nil {
		return RenderedHooks{}, err
	}
	return RenderedHooks{
		JSON:       out,
		TrustState: state,
		SourcePack: compositeHookSourcePack(hooks),
	}, nil
}

func renderCodexHook(hook domain.Hook) (map[string][]any, error) {
	out := map[string][]any{}
	for eventIndex, event := range hook.Events {
		spec, matcher, err := codexNativeEvent(event)
		if err != nil {
			return nil, fmt.Errorf("hook %s/%s events[%d]: %w", hook.SourcePack, hook.ID, eventIndex, err)
		}
		group := map[string]any{}
		if matcher != "" {
			group["matcher"] = matcher
		}
		var handlers []any
		for handlerIndex, handler := range event.EffectiveHandlers() {
			nativeHandler, err := codexNativeHandler(handler)
			if err != nil {
				return nil, fmt.Errorf("hook %s/%s events[%d].handlers[%d]: %w", hook.SourcePack, hook.ID, eventIndex, handlerIndex, err)
			}
			handlers = append(handlers, nativeHandler)
		}
		if len(handlers) == 0 {
			continue
		}
		group["hooks"] = handlers
		out[spec.name] = append(out[spec.name], group)
	}
	return out, nil
}

func codexNativeEvent(event domain.HookEvent) (codexHookEventSpec, string, error) {
	switch event.On {
	case domain.HookEventRunStart:
		return codexHookEventByName["SessionStart"], strings.TrimSpace(event.Match.Source), nil
	case domain.HookEventPromptSubmit:
		return codexHookEventByName["UserPromptSubmit"], "", nil
	case domain.HookEventToolBefore:
		return codexHookEventByName["PreToolUse"], strings.TrimSpace(event.Match.Tool), nil
	case domain.HookEventToolAfter:
		return codexHookEventByName["PostToolUse"], strings.TrimSpace(event.Match.Tool), nil
	case domain.HookEventCompactBefore:
		return codexHookEventSpec{}, "", fmt.Errorf("%s is not supported by the Codex renderer yet", domain.HookEventCompactBefore)
	default:
		return codexHookEventSpec{}, "", fmt.Errorf("unsupported hook event %q", event.On)
	}
}

func codexNativeHandler(handler domain.HookHandler) (map[string]any, error) {
	if handler.Type != "" && handler.Type != domain.HookHandlerTypeCommand {
		return nil, fmt.Errorf("handler type %q is not supported", handler.Type)
	}
	timeoutSeconds, err := domain.HookTimeoutSeconds(handler.Timeout)
	if err != nil {
		return nil, err
	}
	command := handler.Command
	if runtime.GOOS == "windows" && handler.CommandWindows != "" {
		command = handler.CommandWindows
	}
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("command is required")
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

func parseCodexHookConfig(file domain.ConfigFile) (map[string][]any, error) {
	var root map[string]any
	switch strings.ToLower(filepath.Ext(file.Filename)) {
	case ".json":
		if err := json.Unmarshal(file.Content, &root); err != nil {
			return nil, fmt.Errorf("parsing codex hook %s from pack %q: %w", file.Filename, file.SourcePack, err)
		}
		rawHooks, ok := root["hooks"]
		if !ok {
			return nil, fmt.Errorf("codex hook %s from pack %q: missing top-level hooks object", file.Filename, file.SourcePack)
		}
		hooks, ok := rawHooks.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("codex hook %s from pack %q: hooks must be an object", file.Filename, file.SourcePack)
		}
		return codexHookEventsFromMap(file, hooks)
	case ".toml":
		if err := toml.Unmarshal(file.Content, &root); err != nil {
			return nil, fmt.Errorf("parsing codex hook %s from pack %q: %w", file.Filename, file.SourcePack, err)
		}
		normalized, err := normalizeConfigMap(root)
		if err != nil {
			return nil, fmt.Errorf("normalizing codex hook %s from pack %q: %w", file.Filename, file.SourcePack, err)
		}
		return codexHookEventsFromMap(file, normalized)
	default:
		return nil, fmt.Errorf("unsupported codex hook config extension for %q (expected .json or .toml)", file.Filename)
	}
}

func normalizeConfigMap(root map[string]any) (map[string]any, error) {
	b, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func codexHookEventsFromMap(file domain.ConfigFile, root map[string]any) (map[string][]any, error) {
	for key := range root {
		if _, ok := codexHookEventByName[key]; !ok {
			return nil, fmt.Errorf("codex hook %s from pack %q: unsupported hook event %q", file.Filename, file.SourcePack, key)
		}
	}
	out := map[string][]any{}
	for _, spec := range codexHookEvents {
		raw, ok := root[spec.name]
		if !ok {
			continue
		}
		groups, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("codex hook %s from pack %q: %s must be an array", file.Filename, file.SourcePack, spec.name)
		}
		out[spec.name] = append([]any(nil), groups...)
	}
	return out, nil
}

func buildCodexHookTrustState(events map[string][]any, hooksPath string) (map[string]string, error) {
	state := map[string]string{}
	keySource := filepath.Clean(hooksPath)
	for _, spec := range codexHookEvents {
		for groupIndex, rawGroup := range events[spec.name] {
			if err := addCodexHookTrustGroup(state, keySource, spec, groupIndex, rawGroup); err != nil {
				return nil, err
			}
		}
	}
	return state, nil
}

func addCodexHookTrustGroup(state map[string]string, keySource string, spec codexHookEventSpec, groupIndex int, rawGroup any) error {
	group, ok := rawGroup.(map[string]any)
	if !ok {
		return fmt.Errorf("codex hook %s[%d]: matcher group must be an object", spec.name, groupIndex)
	}
	matcher, err := matcherForCodexHook(spec, group)
	if err != nil {
		return fmt.Errorf("codex hook %s[%d]: %w", spec.name, groupIndex, err)
	}
	rawHandlers, ok := group["hooks"]
	if !ok {
		return nil
	}
	handlers, ok := rawHandlers.([]any)
	if !ok {
		return fmt.Errorf("codex hook %s[%d].hooks must be an array", spec.name, groupIndex)
	}
	for handlerIndex, rawHandler := range handlers {
		key, hash, ok, err := codexHookTrustedEntry(keySource, spec, groupIndex, matcher, handlerIndex, rawHandler)
		if err != nil {
			return err
		}
		if ok {
			state[key] = hash
		}
	}
	return nil
}

func codexHookTrustedEntry(keySource string, spec codexHookEventSpec, groupIndex int, matcher *string, handlerIndex int, rawHandler any) (string, string, bool, error) {
	handler, ok := rawHandler.(map[string]any)
	if !ok {
		return "", "", false, fmt.Errorf("codex hook %s[%d].hooks[%d] must be an object", spec.name, groupIndex, handlerIndex)
	}
	normalized, shouldHash, err := normalizeCodexCommandHook(handler)
	if err != nil {
		return "", "", false, fmt.Errorf("codex hook %s[%d].hooks[%d]: %w", spec.name, groupIndex, handlerIndex, err)
	}
	if !shouldHash {
		return "", "", false, nil
	}
	identity := map[string]any{
		"event_name": spec.stateLabel,
		"hooks":      []any{normalized},
	}
	if matcher != nil {
		identity["matcher"] = *matcher
	}
	key := fmt.Sprintf("%s:%s:%d:%d", keySource, spec.stateLabel, groupIndex, handlerIndex)
	return key, codexHookHash(identity), true, nil
}

func matcherForCodexHook(spec codexHookEventSpec, group map[string]any) (*string, error) {
	if !spec.allowsMatcher {
		return nil, nil
	}
	matcher, ok, err := optionalString(group, "matcher")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return &matcher, nil
}

func normalizeCodexCommandHook(handler map[string]any) (map[string]any, bool, error) {
	typ, ok, err := optionalString(handler, "type")
	if err != nil {
		return nil, false, err
	}
	if !ok || typ != string(domain.HookHandlerTypeCommand) {
		return nil, false, nil
	}
	async, err := optionalBoolDefault(handler, "async", false)
	if err != nil {
		return nil, false, err
	}
	if async {
		return nil, false, nil
	}
	command, ok, err := optionalString(handler, "command")
	if err != nil {
		return nil, false, err
	}
	if !ok || strings.TrimSpace(command) == "" {
		return nil, false, nil
	}
	if runtime.GOOS == "windows" {
		if commandWindows, ok, err := optionalString(handler, "commandWindows"); err != nil {
			return nil, false, err
		} else if ok {
			command = commandWindows
		} else if commandWindows, ok, err := optionalString(handler, "command_windows"); err != nil {
			return nil, false, err
		} else if ok {
			command = commandWindows
		}
	}
	timeout, err := optionalIntDefault(handler, "timeout", 600)
	if err != nil {
		return nil, false, err
	}
	if timeout < 1 {
		timeout = 1
	}
	normalized := map[string]any{
		"type":    string(domain.HookHandlerTypeCommand),
		"command": command,
		"timeout": timeout,
		"async":   false,
	}
	if statusMessage, ok, err := optionalString(handler, "statusMessage"); err != nil {
		return nil, false, err
	} else if ok {
		normalized["statusMessage"] = statusMessage
	}
	return normalized, true, nil
}

func optionalString(m map[string]any, key string) (string, bool, error) {
	raw, ok := m[key]
	if !ok {
		return "", false, nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("%s must be a string", key)
	}
	return s, true, nil
}

func optionalBoolDefault(m map[string]any, key string, def bool) (bool, error) {
	raw, ok := m[key]
	if !ok {
		return def, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return b, nil
}

func optionalIntDefault(m map[string]any, key string, def int) (int, error) {
	raw, ok := m[key]
	if !ok {
		return def, nil
	}
	switch v := raw.(type) {
	case float64:
		if math.Trunc(v) != v {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return int(v), nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
}

func codexHookHash(identity map[string]any) string {
	b, _ := json.Marshal(identity)
	return "sha256:" + util.ContentDigest(b)
}

func mergeHookState(root map[string]any, hookState map[string]string) {
	if len(hookState) == 0 {
		return
	}
	hooks := map[string]any{}
	if existing, ok := root["hooks"].(map[string]any); ok {
		maps.Copy(hooks, existing)
	}
	state := map[string]any{}
	if existing, ok := hooks["state"].(map[string]any); ok {
		maps.Copy(state, existing)
	}
	for key, hash := range hookState {
		entry := map[string]any{}
		if existing, ok := state[key].(map[string]any); ok {
			maps.Copy(entry, existing)
		}
		entry["trusted_hash"] = hash
		state[key] = entry
	}
	hooks["state"] = state
	root["hooks"] = hooks
}

func stripManagedHookState(root map[string]any, hooksPath string) {
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return
	}
	state, ok := hooks["state"].(map[string]any)
	if !ok {
		return
	}
	prefix := filepath.Clean(hooksPath) + ":"
	for key := range state {
		if strings.HasPrefix(key, prefix) {
			delete(state, key)
		}
	}
	if len(state) == 0 {
		delete(hooks, "state")
	} else {
		hooks["state"] = state
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}
}

func compositeHookSourcePack(hooks []domain.Hook) string {
	return compositeSourcePack(hooks, func(h domain.Hook) string { return h.SourcePack })
}

func buildCodexHookEventMap() map[string]codexHookEventSpec {
	out := make(map[string]codexHookEventSpec, len(codexHookEvents))
	for _, spec := range codexHookEvents {
		out[spec.name] = spec
	}
	return out
}
