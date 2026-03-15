package engine

import (
	"fmt"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// NormalizeServerName lowercases and trims a server name.
func NormalizeServerName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// expandParams replaces {params.*} (and legacy {param.*}, {global.*}) in s.
func expandParams(params map[string]string, s string) (string, error) {
	out := s
	for key, val := range params {
		for _, prefix := range util.ParamRefPrefixes {
			out = strings.ReplaceAll(out, prefix+key+"}", val)
		}
	}
	// Check for unresolved refs using the same shared prefix list.
	for _, prefix := range util.ParamRefPrefixes {
		if strings.Contains(out, prefix) {
			hint := ""
			if prefix != "{params." {
				hint = fmt.Sprintf(" (hint: rename %s*} to {params.*})", prefix)
			}
			return "", fmt.Errorf("unresolved param reference in %q%s", s, hint)
		}
	}
	return out, nil
}

// ExpandRefs resolves all reference syntax in s:
//   - {params.*} (and legacy {param.*}, {global.*}) from the params map
//   - {env:VAR} from the process environment
//
// Both are strict: unresolved references are always an error. If a value
// can't be resolved, fail fast — don't write broken config.
func ExpandRefs(params map[string]string, s string) (string, error) {
	out, err := expandParams(params, s)
	if err != nil {
		return "", err
	}
	if !strings.Contains(out, "{env:") {
		return out, nil
	}
	return util.ExpandEnvRefs(out)
}

// expandedMCP holds expanded fields for an MCP server.
type expandedMCP struct {
	Command []string
	URL     string
	Env     map[string]string
	Headers map[string]string
	Skip    bool // true if a required env ref could not be resolved
}

// expandMCPServer expands param and environment variable references in an MCP server.
// Unresolvable refs cause the server to be skipped (Skip=true) with an optional warning.
func expandMCPServer(params map[string]string, server domain.MCPServer, warningFn func(string)) (expandedMCP, error) {
	expandStr := func(s string) (string, error) {
		exp, err := ExpandRefs(params, s)
		if err != nil {
			if warningFn != nil {
				warningFn(fmt.Sprintf("WARNING: skipping MCP server %q: %v", server.Name, err))
			}
			return "", err
		}
		return exp, nil
	}

	result := expandedMCP{}

	// Helper for map expansion.
	expandMap := func(m map[string]string) (map[string]string, error) {
		out := map[string]string{}
		for k, v := range m {
			exp, err := expandStr(v)
			if err != nil {
				return nil, err
			}
			out[k] = exp
		}
		return out, nil
	}

	// Expand command (stdio).
	cmd := make([]string, 0, len(server.Command))
	for _, part := range server.Command {
		exp, err := expandStr(part)
		if err != nil {
			return expandedMCP{Skip: true}, nil
		}
		cmd = append(cmd, exp)
	}
	result.Command = cmd

	// Expand URL (sse/streamable-http).
	if server.URL != "" {
		exp, err := expandStr(server.URL)
		if err != nil {
			return expandedMCP{Skip: true}, nil
		}
		result.URL = exp
	}

	// Expand env (stdio).
	if len(server.Env) > 0 {
		envOut, err := expandMap(server.Env)
		if err != nil {
			return expandedMCP{Skip: true}, nil
		}
		result.Env = envOut
	}

	// Expand headers (sse/streamable-http).
	if len(server.Headers) > 0 {
		headersOut, err := expandMap(server.Headers)
		if err != nil {
			return expandedMCP{Skip: true}, nil
		}
		result.Headers = headersOut
	}

	return result, nil
}

// ExpandMCPServers expands env refs in all servers. It is intended for use
// at render time, after param refs have already been resolved during profile
// resolution. Passing nil for params means any residual {params.*} refs will
// cause the server to be skipped.
func ExpandMCPServers(servers []domain.MCPServer) ([]domain.MCPServer, []domain.Warning) {
	out := make([]domain.MCPServer, 0, len(servers))
	var warnings []domain.Warning
	for _, s := range servers {
		var expandDetail string
		warnFn := func(msg string) { expandDetail = msg }
		expanded, err := expandMCPServer(nil, s, warnFn)
		if err != nil {
			warnings = append(warnings, domain.Warning{
				Field:   "mcp." + s.Name,
				Message: fmt.Sprintf("skipped MCP server %q during render: %v", s.Name, err),
			})
			continue
		}
		if expanded.Skip {
			msg := fmt.Sprintf("skipped MCP server %q: unresolved environment variables", s.Name)
			if expandDetail != "" {
				msg = expandDetail
			}
			warnings = append(warnings, domain.Warning{
				Field:   "mcp." + s.Name,
				Message: msg,
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
