package engine

import (
	"fmt"
	"os"
	"path/filepath"
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
	if !util.HasParamRef(s) {
		return s, nil
	}
	var refs []util.ParamRef
	if err := util.WalkParamRefs(s, func(ref util.ParamRef) error {
		refs = append(refs, ref)
		return nil
	}); err != nil {
		return "", err
	}
	out := s
	for i := len(refs) - 1; i >= 0; i-- {
		ref := refs[i]
		name, fallback, hasFallback := strings.Cut(ref.Name, ":-")
		if name == "" {
			return "", fmt.Errorf("empty param reference in %q", s)
		}
		if hasFallback && strings.ContainsAny(fallback, "{}") {
			return "", fmt.Errorf("nested refs in param defaults are not supported in %q", s)
		}
		val, ok := params[name]
		if !ok {
			if hasFallback {
				val = fallback
				ok = true
			}
		}
		if !ok {
			hint := ""
			if ref.Prefix != "{params." {
				hint = fmt.Sprintf(" (hint: rename %s*} to {params.*})", ref.Prefix)
			}
			return "", fmt.Errorf("unresolved param reference in %q%s", s, hint)
		}
		out = out[:ref.Start] + val + out[ref.End:]
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
	return ExpandRefsWithEnv(params, nil, s)
}

// ExpandRefsWithEnv resolves parameter refs from params and environment refs
// from env first, then the process environment.
func ExpandRefsWithEnv(params map[string]string, env map[string]string, s string) (string, error) {
	out, err := expandParams(params, s)
	if err != nil {
		return "", err
	}
	if !strings.Contains(out, util.EnvRefPrefix) {
		return out, nil
	}
	return util.ExpandEnvRefsWith(out, envLookup(env))
}

func envLookup(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if env != nil {
			if val, ok := env[name]; ok {
				return val, true
			}
		}
		return os.LookupEnv(name)
	}
}

// PackRootRef is the literal token resolved to the installed pack's root path.
const PackRootRef = "{pack:root}"

func hasTemplateRefs(s string) bool {
	return util.HasParamRef(s) || strings.Contains(s, util.EnvRefPrefix) || strings.Contains(s, PackRootRef)
}

func expandTemplateRefsWithEnv(params map[string]string, env map[string]string, packRoot string, s string) (string, error) {
	if strings.Contains(s, PackRootRef) {
		if packRoot == "" {
			return "", fmt.Errorf("unresolved %s reference in %q (content not loaded from a pack)", PackRootRef, s)
		}
		s = strings.ReplaceAll(s, PackRootRef, filepath.Clean(packRoot))
	}
	return ExpandRefsWithEnv(params, env, s)
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
	return expandMCPServerWithEnv(params, nil, server, warningFn)
}

func expandMCPServerWithEnv(params map[string]string, env map[string]string, server domain.MCPServer, warningFn func(string)) (expandedMCP, error) {
	expandStr := func(s string) (string, error) {
		// Resolve {pack:root} before param/env expansion so the pack path
		// is available for use in {env:HOME}-style composition.
		if server.PackRoot != "" && strings.Contains(s, "{pack:root}") {
			s = filepath.Clean(strings.ReplaceAll(s, "{pack:root}", server.PackRoot))
		}
		if strings.Contains(s, "{pack:root}") {
			return "", fmt.Errorf("unresolved {pack:root} reference in %q (server not loaded from a pack)", s)
		}
		exp, err := ExpandRefsWithEnv(params, env, s)
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

// ExpandSingleMCPServer expands {params.*}, {env:*}, and {pack:root} refs in
// a single server definition. Returns the server with expanded Command, URL,
// Env, and Headers. Returns an error if any required ref cannot be resolved.
func ExpandSingleMCPServer(params map[string]string, server domain.MCPServer) (domain.MCPServer, error) {
	return ExpandSingleMCPServerWithEnv(params, nil, server)
}

func ExpandSingleMCPServerWithEnv(params map[string]string, env map[string]string, server domain.MCPServer) (domain.MCPServer, error) {
	var detail string
	exp, err := expandMCPServerWithEnv(params, env, server, func(msg string) { detail = msg })
	if err != nil || exp.Skip {
		if detail != "" {
			return server, fmt.Errorf("%s", detail)
		}
		if err != nil {
			return server, err
		}
		return server, fmt.Errorf("unresolved references in server %q", server.Name)
	}
	server.Command = exp.Command
	server.URL = exp.URL
	server.Env = exp.Env
	server.Headers = exp.Headers
	return server, nil
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
