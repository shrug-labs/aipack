package engine

import (
	"fmt"
	"os"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// Env-ref transform format constants for transformEnvRefs / expandMCPServer.
const (
	EnvRefFormatBrace = "cline" // {env:VAR} → ${VAR} — used by cline and claudecode
	EnvRefFormatShell = "shell" // {env:VAR} → $VAR  — used by codex
)

// NormalizeServerName lowercases and trims a server name.
func NormalizeServerName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ExpandParams replaces {params.*} references in s with values from the params map.
// Also accepts legacy {global.*} and {param.*} references for backwards compatibility.
func ExpandParams(params map[string]string, s string) (string, error) {
	out := s
	for key, val := range params {
		out = strings.ReplaceAll(out, "{params."+key+"}", val)
		out = strings.ReplaceAll(out, "{param."+key+"}", val)  // legacy
		out = strings.ReplaceAll(out, "{global."+key+"}", val) // legacy
	}
	if strings.Contains(out, "{params.") {
		return "", fmt.Errorf("unresolved param reference in %q", s)
	}
	if strings.Contains(out, "{param.") {
		return "", fmt.Errorf("unresolved param reference in %q (hint: rename {param.*} to {params.*})", s)
	}
	if strings.Contains(out, "{global.") {
		return "", fmt.Errorf("unresolved param reference in %q (hint: rename {global.*} to {params.*})", s)
	}
	return out, nil
}

// ExpandEnvRefs replaces {env:VAR} references with values from the environment.
func ExpandEnvRefs(s string) (string, error) {
	return util.ExpandEnvRefs(s)
}

// transformEnvRefs replaces {env:VAR} references with harness-native syntax.
// Use "cline" for ${VAR} (cline, claudecode) or "shell" for $VAR (codex).
func transformEnvRefs(s string, format string) string {
	for {
		start := strings.Index(s, "{env:")
		if start < 0 {
			return s
		}
		rest := s[start:]
		endRel := strings.Index(rest, "}")
		if endRel < 0 {
			return s // malformed: no closing brace
		}
		end := start + endRel
		name := strings.TrimSpace(s[start+len("{env:") : end])
		if format == "cline" {
			s = s[:start] + "${" + name + "}" + s[end+1:]
		} else {
			s = s[:start] + "$" + name + s[end+1:]
		}
	}
}

func expandEnvRefsBestEffort(s string) (string, error) {
	if !strings.Contains(s, "{env:") {
		return s, nil
	}
	out := s
	for {
		start := strings.Index(out, "{env:")
		if start < 0 {
			return out, nil
		}
		rest := out[start:]
		endRel := strings.Index(rest, "}")
		if endRel < 0 {
			return "", fmt.Errorf("unterminated env reference in %q", s)
		}
		end := start + endRel
		name := strings.TrimSpace(out[start+len("{env:") : end])
		if name == "" {
			return "", fmt.Errorf("empty env reference in %q", s)
		}
		val, ok := os.LookupEnv(name)
		if !ok || val == "" {
			out = out[:end+1] + out[end+1:]
			start = end + 1
			if start >= len(out) || !strings.Contains(out[start:], "{env:") {
				return out, nil
			}
			continue
		}
		out = out[:start] + val + out[end+1:]
	}
}

func expandMCPServerBestEffort(params map[string]string, server domain.MCPServer) (expandedMCP, error) {
	expandStr := func(s string) (string, error) {
		exp, err := ExpandParams(params, s)
		if err != nil {
			return "", err
		}
		return expandEnvRefsBestEffort(exp)
	}

	result := expandedMCP{}
	cmd := make([]string, 0, len(server.Command))
	for _, part := range server.Command {
		exp, err := expandStr(part)
		if err != nil {
			return expandedMCP{}, err
		}
		cmd = append(cmd, exp)
	}
	result.Command = cmd

	if server.URL != "" {
		exp, err := expandStr(server.URL)
		if err != nil {
			return expandedMCP{}, err
		}
		result.URL = exp
	}

	if len(server.Env) > 0 {
		envOut := map[string]string{}
		for k, v := range server.Env {
			exp, err := expandStr(v)
			if err != nil {
				return expandedMCP{}, err
			}
			envOut[k] = exp
		}
		result.Env = envOut
	}

	if len(server.Headers) > 0 {
		headersOut := map[string]string{}
		for k, v := range server.Headers {
			exp, err := expandStr(v)
			if err != nil {
				return expandedMCP{}, err
			}
			headersOut[k] = exp
		}
		result.Headers = headersOut
	}

	return result, nil
}

// expandedMCP holds expanded fields for an MCP server.
type expandedMCP struct {
	Command []string
	URL     string
	Env     map[string]string
	Headers map[string]string
	Skip    bool // true if unresolved env ref encountered during resolve
}

// expandMCPServer expands param and environment variable references in an MCP server.
// When resolveEnv is true, {env:VAR} refs are resolved from the process environment;
// when false, they are transformed using transformFormat ("cline"→${VAR}, "shell"→$VAR, ""→no change).
// If expansion fails for an env ref, Skip is set and a warning is returned via warningFn.
func expandMCPServer(params map[string]string, server domain.MCPServer, resolveEnv bool, transformFormat string, warningFn func(string)) (expandedMCP, error) {
	expandStr := func(s string) (string, error) {
		exp, err := ExpandParams(params, s)
		if err != nil {
			return "", err
		}
		if resolveEnv {
			exp, err = ExpandEnvRefs(exp)
			if err != nil {
				if warningFn != nil {
					warningFn(fmt.Sprintf("WARNING: skipping MCP server %q: %v", server.Name, err))
				}
				return "", err
			}
		} else if transformFormat != "" {
			exp = transformEnvRefs(exp, transformFormat)
		}
		return exp, nil
	}

	result := expandedMCP{}

	// Expand command (stdio).
	cmd := make([]string, 0, len(server.Command))
	for _, part := range server.Command {
		exp, err := expandStr(part)
		if err != nil {
			if resolveEnv {
				return expandedMCP{Skip: true}, nil
			}
			return expandedMCP{}, err
		}
		cmd = append(cmd, exp)
	}
	result.Command = cmd

	// Expand URL (sse/streamable-http).
	if server.URL != "" {
		exp, err := expandStr(server.URL)
		if err != nil {
			if resolveEnv {
				return expandedMCP{Skip: true}, nil
			}
			return expandedMCP{}, err
		}
		result.URL = exp
	}

	// Expand env (stdio).
	if len(server.Env) > 0 {
		envOut := map[string]string{}
		for k, v := range server.Env {
			exp, err := expandStr(v)
			if err != nil {
				if resolveEnv {
					return expandedMCP{Skip: true}, nil
				}
				return expandedMCP{}, err
			}
			envOut[k] = exp
		}
		result.Env = envOut
	}

	// Expand headers (sse/streamable-http).
	if len(server.Headers) > 0 {
		headersOut := map[string]string{}
		for k, v := range server.Headers {
			exp, err := expandStr(v)
			if err != nil {
				if resolveEnv {
					return expandedMCP{Skip: true}, nil
				}
				return expandedMCP{}, err
			}
			headersOut[k] = exp
		}
		result.Headers = headersOut
	}

	return result, nil
}

// ExpandMCPForRender expands env refs in all servers for a specific render target.
// resolveEnv=true resolves {env:VAR} from process environment (skips on failure).
// resolveEnv=false transforms {env:VAR} using transformFormat ("cline"→${VAR}, "shell"→$VAR, ""→no change).
// Returns only non-skipped servers with Command/URL/Env/Headers replaced, plus
// warnings for any servers that were skipped.
func ExpandMCPForRender(servers []domain.MCPServer, resolveEnv bool, transformFormat string) ([]domain.MCPServer, []domain.Warning) {
	out := make([]domain.MCPServer, 0, len(servers))
	var warnings []domain.Warning
	for _, s := range servers {
		expanded, err := expandMCPServer(nil, s, resolveEnv, transformFormat, nil)
		if err != nil {
			warnings = append(warnings, domain.Warning{
				Field:   "mcp." + s.Name,
				Message: fmt.Sprintf("skipped MCP server %q during render: %v", s.Name, err),
			})
			continue
		}
		if expanded.Skip {
			warnings = append(warnings, domain.Warning{
				Field:   "mcp." + s.Name,
				Message: fmt.Sprintf("skipped MCP server %q: unresolved environment variables", s.Name),
			})
			continue
		}
		s.Command = expanded.Command
		s.URL = expanded.URL
		s.Env = expanded.Env
		s.Headers = expanded.Headers
		out = append(out, s)
	}
	return out, warnings
}

func ExpandMCPForRenderBestEffort(servers []domain.MCPServer) ([]domain.MCPServer, []domain.Warning) {
	out := make([]domain.MCPServer, 0, len(servers))
	var warnings []domain.Warning
	for _, s := range servers {
		expanded, err := expandMCPServerBestEffort(nil, s)
		if err != nil {
			warnings = append(warnings, domain.Warning{
				Field:   "mcp." + s.Name,
				Message: fmt.Sprintf("skipped MCP server %q during render: %v", s.Name, err),
			})
			continue
		}
		s.Command = expanded.Command
		s.URL = expanded.URL
		s.Env = expanded.Env
		s.Headers = expanded.Headers
		out = append(out, s)
	}
	return out, warnings
}
