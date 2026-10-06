package plugin

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
)

// codexClaudeMCP translates only fields with a verified target meaning. Native
// setup, credentials and executable approval remain the client's responsibility.
func codexClaudeMCP(s domain.NativePluginSelection, id string) (map[string]any, error) {
	return codexStdioMCP(s, id, domain.HarnessClaudeCode, "${CLAUDE_PLUGIN_ROOT}/payload", "${CLAUDE_PLUGIN_DATA}")
}

func codexStdioMCP(s domain.NativePluginSelection, id string, target domain.Harness, payloadRoot, dataRoot string) (map[string]any, error) {
	out, err := codexStdioMCPCommand(s, id, target, payloadRoot, dataRoot)
	if err != nil {
		return nil, err
	}
	if target == domain.HarnessOpenCode {
		timeout := out["timeout"]
		args := out["args"].([]string)
		argv := slices.Clone(args[4:])
		if s.Package.Format == AgentPlugins {
			argv = append([]string{"sh"}, args...)
		}
		env, _ := out["env"].(map[string]string)
		if env == nil {
			env = map[string]string{}
		}
		out = map[string]any{"type": "local", "command": argv, "cwd": args[3], "environment": env}
		if timeout != nil {
			out["timeout"] = timeout
		}
	}
	return out, nil
}

// codexStdioMCPCommand validates source semantics and returns the shared cwd/argv
// launcher before converting it to a target's native configuration shape.
func codexStdioMCPCommand(s domain.NativePluginSelection, id string, target domain.Harness, payloadRoot, dataRoot string) (map[string]any, error) {
	targetName := "Claude"
	if target == domain.HarnessOpenCode {
		targetName = "OpenCode"
	} else if target == domain.HarnessCline {
		targetName = "Cline"
	}
	literalReferences := func(value string) bool {
		if target == domain.HarnessCline {
			return strings.Contains(value, "{env:") || strings.Contains(value, "{params.") || strings.Contains(value, "{pack:root}")
		}
		if target == domain.HarnessOpenCode {
			return strings.Contains(value, "{env:") || strings.Contains(value, "{file:")
		}
		if s.Package.Format == AgentPlugins {
			value = strings.NewReplacer("${PLUGIN_ROOT}", "", "${PLUGIN_DATA}", "").Replace(value)
		}
		return strings.Contains(value, "${")
	}
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("stdio working-directory translation requires a POSIX shell")
	}
	paths := s.Package.Components[domain.CategoryMCP][id]
	if !domain.ValidNativeName(id) || len(paths) != 1 || !filepath.IsLocal(paths[0]) {
		return nil, fmt.Errorf("requires one local MCP declaration and a preserved server identity")
	}
	root := filepath.Join(s.Root, "upstream")
	if s.Package.Format == AgentPlugins {
		if overlay, err := readObject(root, ".codex-plugin/plugin.json"); err == nil {
			if err := visitMCP(root, overlay, func(_ string, obj map[string]json.RawMessage) error {
				servers, err := mcpServers(obj)
				if err != nil {
					return err
				}
				var fields map[string]json.RawMessage
				if json.Unmarshal(servers[id], &fields) == nil && fields["env_vars"] != nil {
					return fmt.Errorf("plugin MCP env_vars overlay from Codex is unsupported on %s", targetName)
				}
				return nil
			}); err != nil {
				return nil, fmt.Errorf("unsupported Codex MCP overlay: %w", err)
			}
		}
	}
	entry, err := nativeMCPEntry(s, id)
	if err != nil {
		return nil, err
	}
	var unsupported []string
	fields := []string{"type", "command", "args", "cwd", "env"}
	if s.Package.Format == CodexLegacy && (s.MCPPolicy[id].StartupTimeout == "" || s.MCPPolicy[id].StartupTimeout == "host" || s.MCPPolicy[id].StartupTimeout == "unified") {
		fields = append(fields, "startup_timeout_sec")
	}
	if s.Package.Format == CodexLegacy && target == domain.HarnessClaudeCode {
		fields = append(fields, "tool_timeout_sec")
	}
	for key := range entry {
		if !slices.Contains(fields, key) {
			unsupported = append(unsupported, key)
		}
	}
	if len(unsupported) > 0 {
		slices.Sort(unsupported)
		return nil, fmt.Errorf("fields unsupported on %s: %s", targetName, strings.Join(unsupported, ", "))
	}
	var transport, command, cwd string
	var args []string
	env := map[string]string{}
	if raw, exists := entry["type"]; (exists || s.Package.Format == AgentPlugins) && (json.Unmarshal(raw, &transport) != nil || transport != "stdio") {
		return nil, fmt.Errorf("only stdio transport is supported")
	}
	if json.Unmarshal(entry["command"], &command) != nil || command == "" || strings.ContainsRune(command, 0) || strings.HasPrefix(command, "-") {
		return nil, fmt.Errorf("command must be a nonempty string")
	}
	if s.Package.Format == AgentPlugins && filepath.IsAbs(command) {
		return nil, fmt.Errorf("absolute commands are not valid in native Agent Plugins")
	}
	if literalReferences(command) {
		return nil, fmt.Errorf("literal braced environment references in command require native Codex; %s expands them", targetName)
	}
	if !filepath.IsAbs(command) && strings.ContainsAny(command, "/\\") {
		if s.Package.Format != AgentPlugins || !strings.HasPrefix(command, "./") || command == "./" || strings.Contains(command, "\\") {
			return nil, fmt.Errorf("relative program paths require native Codex or a contained Agent Plugins ./ path")
		}
		if _, err := safePath(filepath.Join(s.Root, "upstream"), command); err != nil {
			return nil, fmt.Errorf("program must stay within the referenced payload root: %w", err)
		}
		command = payloadRoot + "/" + strings.TrimPrefix(command, "./")
	}
	if raw, exists := entry["args"]; exists {
		var values []*string
		if json.Unmarshal(raw, &values) != nil || values == nil {
			return nil, fmt.Errorf("args must be a string array")
		}
		for _, value := range values {
			if value == nil {
				return nil, fmt.Errorf("args must be a string array")
			}
			args = append(args, *value)
		}
	}
	if raw, exists := entry["env"]; exists {
		var values map[string]*string
		if json.Unmarshal(raw, &values) != nil || values == nil {
			return nil, fmt.Errorf("env must contain string values")
		}
		for key, value := range values {
			if value == nil {
				return nil, fmt.Errorf("env must contain string values")
			}
			env[key] = *value
		}
	}
	if _, exists := entry["cwd"]; !exists && s.Package.Format == AgentPlugins {
		cwd = "${PLUGIN_ROOT}"
	} else if json.Unmarshal(entry["cwd"], &cwd) != nil || cwd == "" || strings.ContainsRune(cwd, 0) {
		return nil, fmt.Errorf("requires an explicit working directory")
	}
	if literalReferences(cwd) {
		return nil, fmt.Errorf("literal braced environment references in cwd require native Codex; %s expands them", targetName)
	}
	// Agent Plugins expands only these braced variables. Legacy declarations
	// retain literal values; instruction-body translation is a separate contract.
	translate := func(value string) string {
		if s.Package.Format == AgentPlugins {
			return strings.NewReplacer("${PLUGIN_ROOT}", payloadRoot, "${PLUGIN_DATA}", dataRoot).Replace(value)
		}
		return value
	}
	if s.Package.Format == AgentPlugins && !strings.HasPrefix(cwd, "./") && cwd != "${PLUGIN_ROOT}" && !strings.HasPrefix(cwd, "${PLUGIN_ROOT}/") && cwd != "${PLUGIN_DATA}" && !strings.HasPrefix(cwd, "${PLUGIN_DATA}/") {
		return nil, fmt.Errorf("native Agent Plugins cwd requires a contained ./, ${PLUGIN_ROOT} or ${PLUGIN_DATA} path")
	}
	translatedCwd := translate(cwd)
	for _, prefix := range []string{payloadRoot, dataRoot} {
		if strings.HasPrefix(translatedCwd, prefix) && translatedCwd != prefix &&
			(!strings.HasPrefix(translatedCwd, prefix+"/") || !filepath.IsLocal(strings.TrimPrefix(translatedCwd, prefix+"/"))) {
			return nil, fmt.Errorf("cwd must stay within the referenced root or data directory")
		}
	}
	if !filepath.IsAbs(cwd) && !(s.Package.Format == AgentPlugins && (strings.HasPrefix(cwd, "${PLUGIN_ROOT}") || strings.HasPrefix(cwd, "${PLUGIN_DATA}"))) {
		if !filepath.IsLocal(cwd) || strings.Contains(cwd, "$") {
			return nil, fmt.Errorf("relative cwd must stay within the plugin payload")
		}
		translatedCwd = payloadRoot + "/" + filepath.ToSlash(cwd)
	}
	if strings.HasPrefix(translatedCwd, payloadRoot+"/") {
		if _, err := safePath(filepath.Join(s.Root, "upstream"), strings.TrimPrefix(translatedCwd, payloadRoot+"/")); err != nil {
			return nil, fmt.Errorf("cwd must stay within the plugin payload: %w", err)
		}
	}
	for i := range args {
		if strings.ContainsRune(args[i], 0) {
			return nil, fmt.Errorf("args must not contain NUL")
		}
		if literalReferences(args[i]) {
			return nil, fmt.Errorf("literal braced environment references in args require native Codex; %s expands them", targetName)
		}
		args[i] = translate(args[i])
	}
	for key, value := range env {
		if literalReferences(key) {
			return nil, fmt.Errorf("environment names contain references expanded by %s", targetName)
		}
		if s.Package.Format == AgentPlugins && (key == "PLUGIN_ROOT" || key == "PLUGIN_DATA") {
			return nil, fmt.Errorf("reserved environment overrides are not valid in native Agent Plugins")
		}
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("env contains an invalid name or value")
		}
		if target == domain.HarnessClaudeCode && (key == "CLAUDE_PLUGIN_ROOT" || key == "CLAUDE_PLUGIN_DATA") {
			return nil, fmt.Errorf("env overrides a target-owned plugin variable")
		}
		if literalReferences(value) {
			return nil, fmt.Errorf("literal braced environment references in env require native Codex; %s expands them", targetName)
		}
		env[key] = translate(value)
	}
	if s.Package.Format == AgentPlugins {
		env["PLUGIN_ROOT"] = payloadRoot
		env["PLUGIN_DATA"] = dataRoot
	}
	// Pass the directory and argv separately. Source strings never become shell
	// syntax, and exec leaves the real server attached to the client's stdio.
	out := map[string]any{"type": "stdio", "command": "sh", "args": append([]string{"-c", `cd -- "$1" && shift && exec "$@"`, "aipack", translatedCwd, command}, args...)}
	if s.Package.Format == AgentPlugins {
		allowedRoot := payloadRoot
		if strings.HasPrefix(cwd, "${PLUGIN_DATA}") {
			allowedRoot = dataRoot
		}
		// Retain native containment when runtime data contains directory links.
		out["args"] = append([]string{"-c", `cd -- "$2" && allowed=$(pwd -P) && cd -- "$1" && case "$(pwd -P)/" in "$allowed/"*) shift 2; exec "$@";; *) exit 1;; esac`, "aipack", translatedCwd, allowedRoot, command}, args...)
	}
	if len(env) > 0 {
		out["env"] = env
	}
	if raw, exists := entry["startup_timeout_sec"]; exists {
		seconds, err := portableStartupTimeout(s.MCPPolicy[id].StartupTimeout, target, raw)
		if err != nil {
			return nil, err
		}
		if seconds > 0 {
			out["timeout"] = int64(seconds) * 1000
		}
	}
	if raw, exists := entry["tool_timeout_sec"]; exists {
		var seconds float64
		if json.Unmarshal(raw, &seconds) != nil || seconds*1000 < 1000 || seconds*1000 > math.MaxInt32 || math.Trunc(seconds*1000) != seconds*1000 {
			return nil, fmt.Errorf("tool timeout must be whole milliseconds between 1000 and 2147483647")
		}
		out["timeout"] = int64(seconds * 1000)
	}
	return out, nil
}

func nativeMCPEntry(s domain.NativePluginSelection, id string) (map[string]json.RawMessage, error) {
	if !domain.ValidNativeName(id) {
		return nil, fmt.Errorf("requires one local MCP declaration and a preserved server identity")
	}
	return MCPEntry(s.Root, s.Package, id)
}

// MCPEntry reads the source declaration without projecting it onto a target.
func MCPEntry(packRoot string, pkg domain.NativePlugin, id string) (map[string]json.RawMessage, error) {
	paths := pkg.Components[domain.CategoryMCP][id]
	if id == "" || len(paths) != 1 || (paths[0] != "" && !filepath.IsLocal(paths[0])) {
		return nil, fmt.Errorf("requires one local MCP declaration and a preserved server identity")
	}
	var obj map[string]json.RawMessage
	var err error
	if paths[0] == "" && pkg.Format == Claude {
		obj, err = claudeCatalogFields(pkg.MarketplaceEntry, pkg.CatalogCommandOrder)
	} else {
		obj, err = readObject(filepath.Join(packRoot, "upstream"), paths[0])
	}
	if err != nil {
		return nil, err
	}
	servers, err := mcpServers(obj)
	if err != nil {
		return nil, err
	}
	var entry map[string]json.RawMessage
	if json.Unmarshal(servers[id], &entry) != nil || entry == nil {
		return nil, fmt.Errorf("server declaration cannot be read")
	}
	return entry, nil
}

func portableStartupTimeout(policy string, target domain.Harness, raw json.RawMessage) (int, error) {
	var seconds float64
	if json.Unmarshal(raw, &seconds) != nil || seconds <= 0 || math.IsInf(seconds, 0) || math.IsNaN(seconds) {
		return 0, fmt.Errorf("startup_timeout_sec must be a positive finite number")
	}
	switch policy {
	case "", "host":
		return 0, nil
	case "unified":
		if target != domain.HarnessOpenCode && target != domain.HarnessCline {
			return 0, fmt.Errorf("startup_timeout unified requires OpenCode or Cline's combined connection/request timeout; %s cannot express it", target)
		}
		if seconds != math.Trunc(seconds) || seconds > math.MaxInt32/1000 {
			return 0, fmt.Errorf("startup_timeout unified requires whole seconds between 1 and 2147483")
		}
		return int(seconds), nil
	default:
		return 0, fmt.Errorf("startup_timeout_sec has no equivalent %s mapping; select explicit startup_timeout host or unified policy", target)
	}
}

// StartupTimeoutNotice describes the semantic change accepted by the profile.
// Native Codex keeps its declaration; no policy changes its startup timer.
func StartupTimeoutNotice(s domain.NativePluginSelection, id string, target domain.Harness) string {
	if target == s.Package.Harness || s.Package.Format != CodexLegacy {
		return ""
	}
	entry, err := nativeMCPEntry(s, id)
	if err != nil || entry["startup_timeout_sec"] == nil {
		return ""
	}
	policy := s.MCPPolicy[id].StartupTimeout
	if policy == "" {
		policy = "host"
	}
	if _, err := portableStartupTimeout(policy, target, entry["startup_timeout_sec"]); err != nil {
		return ""
	}
	label := fmt.Sprintf("%s mcp/%s on %s: startup_timeout_sec=%s", s.SourcePack, id, target, entry["startup_timeout_sec"])
	if policy == "host" {
		return label + "; startup_timeout=host delegates startup to the client's timeout; the source per-server deadline is not enforced"
	}
	return label + "; startup_timeout=unified also sets the client's catalog and request/tool timeout, changing execution timing"
}
