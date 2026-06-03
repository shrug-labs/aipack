package harness

import (
	"fmt"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
)

// CompositeSourcePack returns the SourcePack for a generated file built from
// multiple profile items. If all items share a pack, that name is returned;
// otherwise "(composite)" signals multi-pack provenance.
func CompositeSourcePack[T any](items []T, sourcePack func(T) string) string {
	if len(items) == 0 {
		return ""
	}
	first := sourcePack(items[0])
	for _, item := range items[1:] {
		if sourcePack(item) != first {
			return "(composite)"
		}
	}
	return first
}

// CommandHookHandler carries the portable command fields needed by harnesses
// that defer platform selection to a generated runtime.
type CommandHookHandler struct {
	Command        string
	CommandWindows string
	TimeoutSeconds int
	Label          string
}

func NormalizeCommandHookHandler(hook domain.Hook, handler domain.HookHandler, eventIndex, handlerIndex int) (CommandHookHandler, error) {
	if handler.Type != "" && handler.Type != domain.HookHandlerTypeCommand {
		return CommandHookHandler{}, fmt.Errorf("hook %s/%s events[%d].handlers[%d]: handler type %q must be %q", hook.SourcePack, hook.ID, eventIndex, handlerIndex, handler.Type, domain.HookHandlerTypeCommand)
	}
	if handler.Mode == domain.HookHandlerModeAsync {
		return CommandHookHandler{}, fmt.Errorf("hook %s/%s events[%d].handlers[%d]: handler mode %q cannot render a synchronous command hook", hook.SourcePack, hook.ID, eventIndex, handlerIndex, handler.Mode)
	}
	command := strings.TrimSpace(handler.Command)
	commandWindows := strings.TrimSpace(handler.CommandWindows)
	if command == "" && commandWindows == "" {
		return CommandHookHandler{}, fmt.Errorf("hook %s/%s events[%d].handlers[%d]: command or command_windows is required", hook.SourcePack, hook.ID, eventIndex, handlerIndex)
	}
	timeoutSeconds, err := domain.HookTimeoutSeconds(handler.Timeout)
	if err != nil {
		return CommandHookHandler{}, fmt.Errorf("hook %s/%s events[%d].handlers[%d]: %w", hook.SourcePack, hook.ID, eventIndex, handlerIndex, err)
	}
	return CommandHookHandler{
		Command:        command,
		CommandWindows: commandWindows,
		TimeoutSeconds: timeoutSeconds,
		Label:          hook.SourcePack + "/" + hook.ID,
	}, nil
}
